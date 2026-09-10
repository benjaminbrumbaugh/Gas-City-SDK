package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/coordclass"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

type claimLeaseHeartbeat struct {
	id     string
	holder string
}

type claimLeaseTestStore struct {
	*beads.MemStore
	mu                sync.Mutex
	listErr           error
	heartbeats        []claimLeaseHeartbeat
	heartbeatErr      error
	reclaimCalls      []time.Duration
	reclaimAssignees  [][]string
	reclaimClaimIDs   [][]string
	reclaimCount      int
	reclaimErr        error
	reclaimRows       bool
	leaseExpiresAt    time.Time
	beforeGetLeaseRow func(id string)
}

type concurrentClaimLeaseTestStore struct {
	*claimLeaseTestStore
	active  atomic.Int32
	maximum atomic.Int32
	started chan struct{}
	release chan struct{}
}

type blockingClaimLeaseCensusStore struct {
	*claimLeaseTestStore
	started chan struct{}
	rows    []beads.Bead
}

func (s *blockingClaimLeaseCensusStore) ListOpenSessionRows(ctx context.Context) ([]beads.Bead, error) {
	close(s.started)
	<-ctx.Done()
	return s.rows, ctx.Err()
}

func (s *concurrentClaimLeaseTestStore) HeartbeatClaim(ctx context.Context, _, _ string) error {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maximum.Load()
		if active <= maximum || s.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	s.started <- struct{}{}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *claimLeaseTestStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.MemStore.List(query)
}

func (s *claimLeaseTestStore) ListInProgressClaims(ctx context.Context) ([]beads.Bead, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.List(beads.ListQuery{Status: "in_progress", TierMode: beads.TierBoth, Live: true})
}

func (s *claimLeaseTestStore) ListOpenSessionRows(ctx context.Context) ([]beads.Bead, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.List(beads.ListQuery{AllowScan: true, TierMode: beads.TierBoth, Live: true})
}

func (s *claimLeaseTestStore) GetLeaseRow(ctx context.Context, id string) (beads.Bead, error) {
	if err := ctx.Err(); err != nil {
		return beads.Bead{}, err
	}
	if s.beforeGetLeaseRow != nil {
		s.beforeGetLeaseRow(id)
	}
	return s.Get(id)
}

func (s *claimLeaseTestStore) HeartbeatClaim(_ context.Context, id, holder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeats = append(s.heartbeats, claimLeaseHeartbeat{id: id, holder: holder})
	if s.heartbeatErr == nil {
		s.leaseExpiresAt = time.Now().Add(5 * time.Minute)
	}
	return s.heartbeatErr
}

func (s *claimLeaseTestStore) ReclaimExpiredClaims(_ context.Context, grace time.Duration, scope beads.ClaimLeaseReclaimScope) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reclaimCalls = append(s.reclaimCalls, grace)
	s.reclaimAssignees = append(s.reclaimAssignees, append([]string(nil), scope.Assignees...))
	s.reclaimClaimIDs = append(s.reclaimClaimIDs, append([]string(nil), scope.ClaimIDs...))
	if s.reclaimRows {
		rows, err := s.MemStore.List(beads.ListQuery{Status: "in_progress", TierMode: beads.TierBoth, Live: true})
		if err != nil {
			return 0, err
		}
		open, assignee := "open", ""
		if s.leaseExpiresAt.IsZero() || time.Now().After(s.leaseExpiresAt.Add(grace)) {
			for _, row := range rows {
				if err := s.Update(row.ID, beads.UpdateOpts{Status: &open, Assignee: &assignee}); err != nil {
					return 0, err
				}
			}
		}
	}
	return s.reclaimCount, s.reclaimErr
}

func newClaimLeaseTestStore(t *testing.T, rows ...beads.Bead) *claimLeaseTestStore {
	t.Helper()
	mem := beads.NewMemStore()
	mem.HonorExplicitIDs = true
	store := &claimLeaseTestStore{MemStore: mem}
	for _, row := range rows {
		rowCopy := row
		created, err := mem.Create(rowCopy)
		if err != nil {
			t.Fatalf("seed bead %q: %v", row.ID, err)
		}
		if row.Status != "" && row.Status != created.Status {
			status := row.Status
			if err := mem.Update(created.ID, beads.UpdateOpts{Status: &status}); err != nil {
				t.Fatalf("set seed bead %q status: %v", row.ID, err)
			}
		}
	}
	return store
}

func liveLeaseProvider(t *testing.T, sessionID, name, token string) *runtime.Fake {
	t.Helper()
	p := runtime.NewFake()
	if err := p.Start(context.Background(), name, runtime.Config{}); err != nil {
		t.Fatalf("start fake runtime %q: %v", name, err)
	}
	if err := p.SetMeta(name, "GC_INSTANCE_TOKEN", token); err != nil {
		t.Fatalf("set fake runtime token: %v", err)
	}
	if err := p.SetMeta(name, "GC_SESSION_ID", sessionID); err != nil {
		t.Fatalf("set fake runtime session id: %v", err)
	}
	return p
}

func runClaimLeaseReconcileForTest(ctx context.Context, cr *CityRuntime, now time.Time) {
	scopes, err := cr.claimLeaseScopes()
	cr.runFreshClaimLeaseReconcile(ctx, now, scopes, err)
}

func TestReconcileClaimLeasesRenewsExactOwnersAcrossCityAndRig(t *testing.T) {
	const (
		sessionID   = "session-live"
		sessionName = "builder-1"
		token       = "instance-live"
	)
	city := newClaimLeaseTestStore(t, beads.Bead{
		ID: "city-claim", Status: "in_progress", Assignee: sessionName,
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
	})
	rig := newClaimLeaseTestStore(t, beads.Bead{
		ID: "rig-claim", Status: "in_progress", Assignee: sessionName,
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
	})
	city.reclaimCount = 0
	rig.reclaimCount = 1
	provider := liveLeaseProvider(t, sessionID, sessionName, token)
	infos := []session.Info{{
		ID: sessionID, State: session.StateActive, MetadataState: string(session.StateActive),
		SessionName: sessionName, SessionNameMetadata: sessionName, InstanceToken: token,
	}}

	got := reconcileClaimLeases(context.Background(), provider, infos, []claimLeaseScope{
		{Name: "city", Store: city, Lease: city},
		{Name: "rig-a", Store: rig, Lease: rig},
	}, time.Now())

	if got.LastSuccessfulAt.IsZero() {
		t.Fatal("successful reconciliation did not expose its completion instant")
	}
	if len(city.heartbeats) != 1 || city.heartbeats[0] != (claimLeaseHeartbeat{id: "city-claim", holder: sessionName}) {
		t.Fatalf("city heartbeats = %#v, want the exact live owner", city.heartbeats)
	}
	if len(rig.heartbeats) != 1 || rig.heartbeats[0] != (claimLeaseHeartbeat{id: "rig-claim", holder: sessionName}) {
		t.Fatalf("rig heartbeats = %#v, want the exact live owner", rig.heartbeats)
	}
	if len(city.reclaimCalls) != 0 || len(rig.reclaimCalls) != 0 {
		t.Fatalf("active-owner reclaim calls = city:%v rig:%v, want none", city.reclaimCalls, rig.reclaimCalls)
	}
	if got.Stores[1].Reclaimed != 0 {
		t.Fatalf("rig diagnostics = %#v, want no active-owner reclaim", got.Stores)
	}
}

func TestReconcileClaimLeasesRenewsExpiredActiveRigBeforeReclaimGrace(t *testing.T) {
	const (
		sessionID   = "active-rig-session"
		sessionName = "active-rig-worker"
		token       = "active-rig-token"
	)
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "expired-rig-claim", Status: "in_progress", Assignee: sessionName,
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
	})
	store.leaseExpiresAt = time.Now().Add(-time.Minute)
	store.reclaimRows = true
	provider := liveLeaseProvider(t, sessionID, sessionName, token)

	got := reconcileClaimLeases(context.Background(), provider, []session.Info{{
		ID: sessionID, State: session.StateActive, MetadataState: string(session.StateActive),
		SessionName: sessionName, SessionNameMetadata: sessionName, InstanceToken: token,
	}}, []claimLeaseScope{{Name: "active-rig", Store: store, Lease: store}}, time.Now())

	row, err := store.Get("expired-rig-claim")
	if err != nil {
		t.Fatalf("read active-rig claim: %v", err)
	}
	if row.Status != "in_progress" || row.Assignee != sessionName {
		t.Fatalf("active-rig claim = status %q assignee %q, want renewed ownership", row.Status, row.Assignee)
	}
	if len(store.heartbeats) != 1 || len(store.reclaimCalls) != 0 || got.Stores[0].Renewed != 1 {
		t.Fatalf("active-rig lease result = heartbeats:%v reclaim:%v diagnostics:%#v", store.heartbeats, store.reclaimCalls, got)
	}
}

func TestReconcileClaimLeasesRejectsIncompleteIdentityAndDrainingOwners(t *testing.T) {
	tests := []struct {
		name string
		info session.Info
	}{
		{
			name: "missing persisted session name",
			info: session.Info{ID: "session", State: session.StateActive, MetadataState: string(session.StateActive), SessionName: "worker", InstanceToken: "token"},
		},
		{
			name: "missing persisted instance token",
			info: session.Info{ID: "session", State: session.StateActive, MetadataState: string(session.StateActive), SessionName: "worker", SessionNameMetadata: "worker"},
		},
		{
			name: "draining owner is not renewed",
			info: session.Info{ID: "session", State: session.StateDraining, MetadataState: string(session.StateDraining), SessionName: "worker", SessionNameMetadata: "worker", InstanceToken: "token"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newClaimLeaseTestStore(t, beads.Bead{
				ID: "claim", Status: "in_progress", Assignee: tc.info.SessionName,
				Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: tc.info.ID},
			})
			got := reconcileClaimLeases(context.Background(), nil, []session.Info{tc.info}, []claimLeaseScope{
				{Name: "city", Store: store, Lease: store},
			}, time.Now())
			if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 0 {
				t.Fatalf("fenced operations = heartbeats:%#v reclaim:%#v, want none", store.heartbeats, store.reclaimCalls)
			}
			if got.Stores[0].Errors == 0 {
				t.Fatal("identity rejection must remain visible in diagnostics")
			}
		})
	}
}

func TestReconcileClaimLeasesDoesNotReclaimDrainingExactOwner(t *testing.T) {
	const (
		sessionID   = "draining-session"
		sessionName = "draining-worker"
		token       = "draining-token"
	)
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "draining-claim", Status: "in_progress", Assignee: sessionName,
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
	})
	provider := liveLeaseProvider(t, sessionID, sessionName, token)

	got := reconcileClaimLeases(context.Background(), provider, []session.Info{{
		ID: sessionID, State: session.StateDraining, MetadataState: string(session.StateDraining),
		SessionName: sessionName, SessionNameMetadata: sessionName, InstanceToken: token,
	}}, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())

	if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 0 {
		t.Fatalf("draining exact owner operations = heartbeats:%v reclaim:%v, want none", store.heartbeats, store.reclaimCalls)
	}
	if got.Stores[0].Errors == 0 {
		t.Fatalf("draining diagnostic = %#v, want explicit non-renewable state", got.Stores[0])
	}
}

func TestReconcileClaimLeasesUsesCompleteCensusWithoutProviderObservation(t *testing.T) {
	const sessionID, sessionName, token = "persisted-session", "worker", "instance-token"
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "claim", Status: "in_progress", Assignee: sessionName,
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
	})
	got := reconcileClaimLeases(context.Background(), nil, []session.Info{{
		ID: sessionID, State: session.StateActive, MetadataState: string(session.StateActive),
		SessionName: sessionName, SessionNameMetadata: sessionName, InstanceToken: token,
	}}, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())

	if len(store.heartbeats) != 1 || len(store.reclaimCalls) != 0 {
		t.Fatalf("census-owned operations = heartbeats:%v reclaim:%v, want heartbeat only", store.heartbeats, store.reclaimCalls)
	}
	if got.Stores[0].Errors != 0 || got.LastSuccessfulAt.IsZero() {
		t.Fatalf("census-owned diagnostics = %#v, want success", got)
	}
}

func TestReconcileClaimLeasesReclaimsOnlyTerminallyClosedOwner(t *testing.T) {
	store := newClaimLeaseTestStore(t,
		beads.Bead{
			ID: "claim", Status: "in_progress", Assignee: "worker",
			Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "session"},
		},
		beads.Bead{
			ID: "session", Type: session.BeadType, Status: "closed", Labels: []string{session.LabelSession},
			Metadata: beads.StringMap{
				"session_name":   "worker",
				"instance_token": "token",
			},
		},
	)
	// Closed history is not part of the current-cycle open census. Reclaim is
	// authorized by the bounded exact-ID fallback against the active stores.
	got := reconcileClaimLeases(context.Background(), nil, nil, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())

	if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 1 {
		t.Fatalf("closed-owner operations = heartbeats:%v reclaim:%v, want reclaim only", store.heartbeats, store.reclaimCalls)
	}
	if got.Stores[0].Errors != 0 || got.LastSuccessfulAt.IsZero() {
		t.Fatalf("closed-owner diagnostics = %#v, want success", got)
	}
}

func TestReconcileClaimLeasesRequiresExactTerminalOwnerIdentity(t *testing.T) {
	tests := []struct {
		name     string
		metadata beads.StringMap
	}{
		{name: "missing persisted identity", metadata: beads.StringMap{}},
		{name: "assignee mismatch", metadata: beads.StringMap{"session_name": "other-worker", "instance_token": "token"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newClaimLeaseTestStore(t,
				beads.Bead{
					ID: "claim", Status: "in_progress", Assignee: "worker",
					Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "session"},
				},
				beads.Bead{
					ID: "session", Type: session.BeadType, Status: "closed", Labels: []string{session.LabelSession},
					Metadata: tc.metadata,
				},
			)

			got := reconcileClaimLeases(context.Background(), nil, nil, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())
			if len(store.reclaimCalls) != 0 || len(store.heartbeats) != 0 {
				t.Fatalf("fenced terminal owner operations = heartbeats:%v reclaim:%v, want none", store.heartbeats, store.reclaimCalls)
			}
			if got.Stores[0].Errors == 0 || !got.LastSuccessfulAt.IsZero() {
				t.Fatalf("fenced terminal owner diagnostics = %#v, want fail-closed error", got)
			}
		})
	}
}

func TestReconcileClaimLeasesReclaimsOnlyExactTerminalAssignee(t *testing.T) {
	store := newClaimLeaseTestStore(t,
		beads.Bead{
			ID: "active-claim", Status: "in_progress", Assignee: "active-worker",
			Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "active-session"},
		},
		beads.Bead{
			ID: "closed-claim", Status: "in_progress", Assignee: "closed-worker",
			Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "closed-session"},
		},
		beads.Bead{
			ID: "closed-session", Type: session.BeadType, Status: "closed", Labels: []string{session.LabelSession},
			Metadata: beads.StringMap{"session_name": "closed-worker", "instance_token": "closed-token"},
		},
	)
	active := session.Info{
		ID: "active-session", State: session.StateActive, MetadataState: string(session.StateActive),
		SessionName: "active-worker", SessionNameMetadata: "active-worker", InstanceToken: "active-token",
	}

	got := reconcileClaimLeases(context.Background(), nil, []session.Info{active}, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())
	if len(store.heartbeats) != 1 || store.heartbeats[0].id != "active-claim" {
		t.Fatalf("heartbeats = %#v, want active claim only", store.heartbeats)
	}
	if len(store.reclaimAssignees) != 1 || !reflect.DeepEqual(store.reclaimAssignees[0], []string{"closed-worker"}) {
		t.Fatalf("reclaim assignees = %#v, want exact terminal assignee only", store.reclaimAssignees)
	}
	if len(store.reclaimClaimIDs) != 1 || !reflect.DeepEqual(store.reclaimClaimIDs[0], []string{"closed-claim"}) {
		t.Fatalf("reclaim claim ids = %#v, want exact terminal claim only", store.reclaimClaimIDs)
	}
	if got.Stores[0].Errors != 0 || got.LastSuccessfulAt.IsZero() {
		t.Fatalf("mixed-owner diagnostics = %#v, want success", got)
	}
}

func TestReconcileClaimLeasesReclaimsEachTerminalTupleIndependently(t *testing.T) {
	store := newClaimLeaseTestStore(t,
		beads.Bead{ID: "claim-a", Status: "in_progress", Assignee: "worker-a", Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "session-a"}},
		beads.Bead{ID: "claim-b", Status: "in_progress", Assignee: "worker-b", Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "session-b"}},
		beads.Bead{ID: "session-a", Type: session.BeadType, Status: "closed", Labels: []string{session.LabelSession}, Metadata: beads.StringMap{"session_name": "worker-a", "instance_token": "token-a"}},
		beads.Bead{ID: "session-b", Type: session.BeadType, Status: "closed", Labels: []string{session.LabelSession}, Metadata: beads.StringMap{"session_name": "worker-b", "instance_token": "token-b"}},
	)

	got := reconcileClaimLeases(context.Background(), nil, nil, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())
	if got.Stores[0].Errors != 0 || len(store.reclaimCalls) != 2 {
		t.Fatalf("reclaim diagnostics = %#v calls=%d, want two exact calls", got.Stores[0], len(store.reclaimCalls))
	}
	want := map[string]string{"claim-a": "worker-a", "claim-b": "worker-b"}
	for i := range store.reclaimCalls {
		if len(store.reclaimClaimIDs[i]) != 1 || len(store.reclaimAssignees[i]) != 1 {
			t.Fatalf("reclaim call %d = ids:%v assignees:%v, want one tuple", i, store.reclaimClaimIDs[i], store.reclaimAssignees[i])
		}
		if want[store.reclaimClaimIDs[i][0]] != store.reclaimAssignees[i][0] {
			t.Fatalf("reclaim call %d crossed owner tuples: ids:%v assignees:%v", i, store.reclaimClaimIDs[i], store.reclaimAssignees[i])
		}
	}
}

func TestReconcileClaimLeasesRevalidatesClaimBeforeTerminalReclaim(t *testing.T) {
	store := newClaimLeaseTestStore(t,
		beads.Bead{ID: "claim", Status: "in_progress", Assignee: "old-worker", Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "old-session"}},
		beads.Bead{ID: "old-session", Type: session.BeadType, Status: "closed", Labels: []string{session.LabelSession}, Metadata: beads.StringMap{"session_name": "old-worker", "instance_token": "old-token"}},
	)
	changed := false
	store.beforeGetLeaseRow = func(id string) {
		if id != "claim" || changed {
			return
		}
		changed = true
		newAssignee := "new-worker"
		if err := store.Update("claim", beads.UpdateOpts{Assignee: &newAssignee, Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "new-session"}}); err != nil {
			t.Fatalf("transfer claim: %v", err)
		}
	}

	got := reconcileClaimLeases(context.Background(), nil, nil, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())
	if len(store.reclaimCalls) != 0 {
		t.Fatalf("transferred claim reclaim calls = %v, want none", store.reclaimCalls)
	}
	if got.Stores[0].Errors == 0 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("transferred claim diagnostics = %#v, want fail-closed error", got)
	}
}

func TestReconcileClaimLeasesUsesRelocatedSessionOwnerStore(t *testing.T) {
	const sessionID, sessionName = "relocated-session", "relocated-worker"
	claims := newClaimLeaseTestStore(t, beads.Bead{ID: "claim", Status: "in_progress", Assignee: sessionName, Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID}})
	owners := newClaimLeaseTestStore(t, beads.Bead{ID: sessionID, Type: session.BeadType, Labels: []string{session.LabelSession}, Metadata: beads.StringMap{"state": string(session.StateActive), "session_name": sessionName, "instance_token": "token"}})
	cr := &CityRuntime{storageRoutes: &storageRoutes{stores: map[coordclass.Class]beads.Store{coordclass.ClassSessions: owners}}}
	claimScopes := []claimLeaseScope{{Name: "city", Store: claims, Lease: claims}}
	ownerScopes, err := cr.claimLeaseOwnerScopes(claimScopes)
	if err != nil {
		t.Fatalf("claimLeaseOwnerScopes() error = %v", err)
	}
	census, complete, err := loadCurrentClaimLeaseOwners(context.Background(), ownerScopes)
	if err != nil {
		t.Fatalf("loadCurrentClaimLeaseOwners() error = %v", err)
	}
	if !complete {
		t.Fatal("loadCurrentClaimLeaseOwners() reported incomplete relocated census")
	}
	got := reconcileClaimLeasesWithOwnerScopes(context.Background(), nil, census, claimScopes, ownerScopes, complete, time.Now())
	if len(claims.heartbeats) != 1 || claims.heartbeats[0].id != "claim" || got.Stores[0].Errors != 0 {
		t.Fatalf("relocated-owner result = heartbeats:%v diagnostics:%#v", claims.heartbeats, got)
	}
}

func TestReconcileClaimLeasesRenewsKnownOwnersWhenAnotherOwnerStoreIsUnavailable(t *testing.T) {
	const sessionID, sessionName = "healthy-session", "healthy-worker"
	healthy := newClaimLeaseTestStore(t,
		beads.Bead{ID: "claim", Status: "in_progress", Assignee: sessionName, Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID}},
		beads.Bead{ID: sessionID, Type: session.BeadType, Labels: []string{session.LabelSession}, Metadata: beads.StringMap{"state": string(session.StateActive), "session_name": sessionName, "instance_token": "token"}},
	)
	claimScopes := []claimLeaseScope{{Name: "healthy", Store: healthy, Lease: healthy}, {Name: "unavailable"}}
	owners, complete, err := loadCurrentClaimLeaseOwners(context.Background(), claimScopes)
	if err != nil || complete {
		t.Fatalf("partial owner census = complete:%v err:%v, want incomplete without fatal error", complete, err)
	}
	got := reconcileClaimLeasesWithOwnerScopes(context.Background(), nil, owners, claimScopes, claimScopes, complete, time.Now())
	if len(healthy.heartbeats) != 1 || healthy.heartbeats[0].id != "claim" {
		t.Fatalf("healthy heartbeats = %#v, want exact known owner renewed", healthy.heartbeats)
	}
	if len(healthy.reclaimCalls) != 0 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("partial census result = %#v reclaim=%v, want no reclaim and incomplete pass", got, healthy.reclaimCalls)
	}
}

func TestLoadCurrentClaimLeaseOwnersAcceptsIdenticalMigrationResidency(t *testing.T) {
	row := beads.Bead{ID: "session", Type: session.BeadType, Labels: []string{session.LabelSession}, Metadata: beads.StringMap{"state": string(session.StateActive), "session_name": "worker", "instance_token": "token"}}
	legacy := newClaimLeaseTestStore(t, row)
	relocated := newClaimLeaseTestStore(t, row)
	owners, complete, err := loadCurrentClaimLeaseOwners(context.Background(), []claimLeaseScope{
		{Name: "legacy", Owner: legacy}, {Name: "sessions", Owner: relocated},
	})
	if err != nil || !complete || len(owners) != 1 {
		t.Fatalf("dual-resident census = owners:%#v complete:%v err:%v, want one authoritative identity", owners, complete, err)
	}
}

func TestReconcileClaimLeasesDoesNotReclaimAssigneeActiveInAnotherStore(t *testing.T) {
	active := newClaimLeaseTestStore(t, beads.Bead{ID: "active-claim", Status: "in_progress", Assignee: "worker", Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "active-session"}})
	terminal := newClaimLeaseTestStore(t,
		beads.Bead{ID: "old-claim", Status: "in_progress", Assignee: "worker", Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "closed-session"}},
		beads.Bead{ID: "closed-session", Type: session.BeadType, Status: "closed", Labels: []string{session.LabelSession}, Metadata: beads.StringMap{"session_name": "worker", "instance_token": "closed-token"}},
	)
	info := session.Info{ID: "active-session", State: session.StateActive, MetadataState: string(session.StateActive), SessionName: "worker", SessionNameMetadata: "worker", InstanceToken: "active-token"}
	got := reconcileClaimLeases(context.Background(), nil, []session.Info{info}, []claimLeaseScope{
		{Name: "active", Store: active, Lease: active}, {Name: "terminal", Store: terminal, Lease: terminal},
	}, time.Now())
	if len(active.heartbeats) != 1 || len(terminal.reclaimCalls) != 0 {
		t.Fatalf("cross-store operations = heartbeats:%v reclaim:%v diagnostics:%#v", active.heartbeats, terminal.reclaimCalls, got)
	}
}

func TestReconcileClaimLeasesSkipsSessionRowsInProgress(t *testing.T) {
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "session", Type: session.BeadType, Status: "in_progress", Assignee: "worker", Labels: []string{session.LabelSession},
		Metadata: beads.StringMap{"state": string(session.StateActive), "session_name": "worker", "instance_token": "token"},
	})

	got := reconcileClaimLeases(context.Background(), nil, nil, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())
	if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 0 {
		t.Fatalf("session-row operations = heartbeats:%v reclaim:%v, want none", store.heartbeats, store.reclaimCalls)
	}
	if got.Stores[0].Errors != 0 || got.LastSuccessfulAt.IsZero() {
		t.Fatalf("session-row diagnostics = %#v, want successful no-op", got)
	}
}

func TestReconcileClaimLeasesDoesNotReclaimWhenRecordedOwnerIsMissing(t *testing.T) {
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "claim", Status: "in_progress", Assignee: "worker",
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "missing-session"},
	})
	got := reconcileClaimLeases(context.Background(), nil, nil, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())

	if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 0 {
		t.Fatalf("missing-owner operations = heartbeats:%v reclaim:%v, want none", store.heartbeats, store.reclaimCalls)
	}
	if got.Stores[0].Errors == 0 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("missing-owner diagnostics = %#v, want fail-closed error", got)
	}
}

func TestReconcileClaimLeasesHeartbeatsFleetConcurrently(t *testing.T) {
	const claimCount = claimLeaseHeartbeatWorkers + 1
	rows := make([]beads.Bead, 0, claimCount)
	for i := range claimCount {
		rows = append(rows, beads.Bead{
			ID: fmt.Sprintf("claim-%d", i), Status: "in_progress", Assignee: "worker",
			Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "session"},
		})
	}
	store := &concurrentClaimLeaseTestStore{
		claimLeaseTestStore: newClaimLeaseTestStore(t, rows...),
		started:             make(chan struct{}, claimCount),
		release:             make(chan struct{}),
	}
	provider := liveLeaseProvider(t, "session", "worker", "token")
	done := make(chan claimLeaseReconcileResult, 1)
	go func() {
		done <- reconcileClaimLeases(context.Background(), provider, []session.Info{{
			ID: "session", State: session.StateActive, MetadataState: string(session.StateActive),
			SessionName: "worker", SessionNameMetadata: "worker", InstanceToken: "token",
		}}, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())
	}()

	for range 2 {
		select {
		case <-store.started:
		case <-time.After(time.Second):
			close(store.release)
			t.Fatal("heartbeats remained serial; two operations did not start together")
		}
	}
	close(store.release)
	got := <-done
	if store.maximum.Load() < 2 || got.Stores[0].Renewed != claimCount {
		t.Fatalf("heartbeat concurrency = %d, diagnostics = %#v", store.maximum.Load(), got.Stores[0])
	}
}

func TestReconcileClaimLeasesFailsClosedPerUnreadableStore(t *testing.T) {
	broken := newClaimLeaseTestStore(t)
	broken.listErr = errors.New("rig ledger unavailable")
	healthy := newClaimLeaseTestStore(t, beads.Bead{
		ID: "healthy-claim", Status: "in_progress", Assignee: "worker",
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: "session"},
	})
	healthy.reclaimCount = 1
	provider := liveLeaseProvider(t, "session", "worker", "token")

	got := reconcileClaimLeases(context.Background(), provider, []session.Info{{
		ID: "session", State: session.StateActive, MetadataState: string(session.StateActive),
		SessionName: "worker", SessionNameMetadata: "worker", InstanceToken: "token",
	}}, []claimLeaseScope{
		{Name: "broken", Store: broken, Lease: broken},
		{Name: "healthy", Store: healthy, Lease: healthy},
	}, time.Now())

	if len(broken.reclaimCalls) != 0 {
		t.Fatalf("broken store reclaim calls = %v, want none", broken.reclaimCalls)
	}
	if len(healthy.heartbeats) != 1 || len(healthy.reclaimCalls) != 0 || got.Stores[1].Renewed != 1 {
		t.Fatalf("healthy store was not independently renewed: heartbeats=%v calls=%v report=%#v", healthy.heartbeats, healthy.reclaimCalls, got.Stores)
	}
	if got.Stores[0].Errors == 0 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("partial pass diagnostics = %#v, want error and no successful instant", got)
	}
}

func TestReconcileClaimLeasesRetainsLastSuccessfulTimestampAcrossFailure(t *testing.T) {
	broken := newClaimLeaseTestStore(t)
	broken.listErr = errors.New("city ledger unavailable")
	previous := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	cr := &CityRuntime{
		cityPath:            t.TempDir(),
		cityName:            "city",
		standaloneCityStore: broken,
		claimLeaseResult: claimLeaseReconcileResult{
			LastAttemptAt:    previous.Add(-time.Minute),
			LastSuccessfulAt: previous,
		},
	}

	runClaimLeaseReconcileForTest(context.Background(), cr, previous.Add(time.Minute))
	got := cr.claimLeaseReconciliation()
	if !got.LastSuccessfulAt.Equal(previous) {
		t.Fatalf("last successful timestamp = %v, want preserved %v", got.LastSuccessfulAt, previous)
	}
	if got.Stores[0].Errors != 1 {
		t.Fatalf("failure diagnostics = %#v, want one store error", got.Stores[0])
	}
}

func TestReconcileClaimLeasesDueUsesCompleteCrossStoreOwnerCensus(t *testing.T) {
	const (
		sessionID   = "rig-session"
		sessionName = "rig-worker"
		token       = "rig-token"
	)
	store := newClaimLeaseTestStore(t,
		beads.Bead{
			ID: "rig-claim", Status: "in_progress", Assignee: sessionName,
			Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
		},
		beads.Bead{
			ID: sessionID, Type: session.BeadType, Labels: []string{session.LabelSession},
			Metadata: beads.StringMap{"state": string(session.StateActive), "session_name": sessionName, "instance_token": token},
		},
	)
	provider := liveLeaseProvider(t, sessionID, sessionName, token)
	cr := &CityRuntime{
		cityPath:            t.TempDir(),
		cityName:            "city",
		sp:                  provider,
		standaloneCityStore: store,
	}
	runClaimLeaseReconcileForTest(context.Background(), cr, time.Now())

	if len(store.heartbeats) != 1 || store.heartbeats[0].id != "rig-claim" {
		t.Fatalf("heartbeats = %#v, want the exact owner from the complete cross-store census", store.heartbeats)
	}
}

func TestReconcileClaimLeasesDueIgnoresStaleSnapshot(t *testing.T) {
	const sessionID, sessionName = "stale-session", "stale-worker"
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "claim", Status: "in_progress", Assignee: sessionName,
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
	})
	staleSnapshot := newSessionBeadSnapshot([]beads.Bead{{
		ID: sessionID, Type: session.BeadType, Labels: []string{session.LabelSession},
		Metadata: beads.StringMap{"state": string(session.StateActive), "session_name": sessionName, "instance_token": "stale-token"},
	}})
	cr := &CityRuntime{cityPath: t.TempDir(), cityName: "city", standaloneCityStore: store}

	_ = staleSnapshot
	runClaimLeaseReconcileForTest(context.Background(), cr, time.Now())

	if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 0 {
		t.Fatalf("stale-snapshot operations = heartbeats:%v reclaim:%v, want none", store.heartbeats, store.reclaimCalls)
	}
	got := cr.claimLeaseReconciliation()
	if len(got.Stores) != 1 || got.Stores[0].Errors == 0 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("stale-snapshot diagnostics = %#v, want fresh-census failure", got)
	}
}

func TestStartClaimLeaseReconcileDoesNotBlockControllerTick(t *testing.T) {
	const (
		sessionID   = "session"
		sessionName = "worker"
		token       = "token"
	)
	store := &concurrentClaimLeaseTestStore{
		claimLeaseTestStore: newClaimLeaseTestStore(t,
			beads.Bead{
				ID: "claim", Status: "in_progress", Assignee: sessionName,
				Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
			},
			beads.Bead{
				ID: sessionID, Type: session.BeadType, Labels: []string{session.LabelSession},
				Metadata: beads.StringMap{"state": string(session.StateActive), "session_name": sessionName, "instance_token": token},
			},
		),
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	cr := &CityRuntime{
		cityPath:            t.TempDir(),
		cityName:            "city",
		sp:                  liveLeaseProvider(t, sessionID, sessionName, token),
		standaloneCityStore: store,
	}
	returned := make(chan struct{})
	go func() {
		cr.startClaimLeaseReconcileIfDue(context.Background(), time.Now())
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		close(store.release)
		t.Fatal("lease reconciliation blocked the controller caller")
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		close(store.release)
		t.Fatal("background lease reconciliation did not start")
	}
	close(store.release)

	deadline := time.Now().Add(time.Second)
	for {
		cr.claimLeaseMu.RLock()
		inFlight := cr.claimLeaseInFlight
		cr.claimLeaseMu.RUnlock()
		if !inFlight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background lease reconciliation did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStartClaimLeaseReconcileCancellationReleasesPatrolLane(t *testing.T) {
	store := &blockingClaimLeaseCensusStore{
		claimLeaseTestStore: newClaimLeaseTestStore(t),
		started:             make(chan struct{}),
	}
	cr := &CityRuntime{cityPath: t.TempDir(), cityName: "city", standaloneCityStore: store}
	ctx, cancel := context.WithCancel(context.Background())
	cr.startClaimLeaseReconcileIfDue(ctx, time.Now())
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("background owner census did not start")
	}
	cancel()

	deadline := time.Now().Add(time.Second)
	for {
		cr.claimLeaseMu.RLock()
		inFlight := cr.claimLeaseInFlight
		cr.claimLeaseMu.RUnlock()
		if !inFlight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled owner census did not release the patrol lane")
		}
		time.Sleep(time.Millisecond)
	}
	got := cr.claimLeaseReconciliation()
	if len(got.Stores) != 1 || got.Stores[0].Errors != 1 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("canceled-census diagnostics = %#v, want one failed store", got)
	}
}

func TestReconcileClaimLeasesDueFailsClosedOnPartialOwnerCensus(t *testing.T) {
	const (
		sessionID   = "possibly-live-session"
		sessionName = "possibly-live-worker"
		token       = "possibly-live-token"
	)
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "possibly-live-claim", Status: "in_progress", Assignee: sessionName,
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
	})
	store.listErr = errors.New("owner census unavailable")
	provider := liveLeaseProvider(t, sessionID, sessionName, token)
	cr := &CityRuntime{
		cityPath:            t.TempDir(),
		cityName:            "city",
		sp:                  provider,
		standaloneCityStore: store,
	}
	runClaimLeaseReconcileForTest(context.Background(), cr, time.Now())

	if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 0 {
		t.Fatalf("partial-census lease operations = heartbeats:%v reclaim:%v, want no mutation", store.heartbeats, store.reclaimCalls)
	}
	got := cr.claimLeaseReconciliation()
	if len(got.Stores) != 1 || got.Stores[0].Errors != 1 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("partial-census diagnostics = %#v, want one error and no successful instant", got)
	}
}

func TestReconcileClaimLeasesFailsClosedWhenClaimSessionIdentityIsMissing(t *testing.T) {
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "identity-unstamped", Status: "in_progress", Assignee: "direct-owner",
	})
	store.reclaimRows = true
	store.reclaimCount = 1

	got := reconcileClaimLeases(context.Background(), runtime.NewFake(), nil, []claimLeaseScope{
		{Name: "rig-a", Store: store, Lease: store},
	}, time.Now())

	if len(store.heartbeats) != 0 {
		t.Fatalf("unstamped claim heartbeats = %#v, want none", store.heartbeats)
	}
	if len(store.reclaimCalls) != 0 {
		t.Fatalf("unstamped claim reclaim calls = %v, want none", store.reclaimCalls)
	}
	row, err := store.Get("identity-unstamped")
	if err != nil {
		t.Fatalf("read unstamped claim: %v", err)
	}
	if row.Status != "in_progress" || row.Assignee != "direct-owner" {
		t.Fatalf("unstamped claim = status %q assignee %q, want ownership preserved", row.Status, row.Assignee)
	}
	if got.Stores[0].Errors == 0 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("unstamped diagnostics = %#v, want fail-closed error", got)
	}
}

func TestReconcileClaimLeasesSkipsUnassignedCoordinationRows(t *testing.T) {
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "coordination", Status: "in_progress", Assignee: "",
	})

	got := reconcileClaimLeases(context.Background(), runtime.NewFake(), nil, []claimLeaseScope{
		{Name: "city", Store: store, Lease: store},
	}, time.Now())

	if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 0 {
		t.Fatalf("coordination row lease operations = heartbeats:%v reclaim:%v, want none", store.heartbeats, store.reclaimCalls)
	}
	if got.Stores[0].Errors != 0 || got.LastSuccessfulAt.IsZero() {
		t.Fatalf("coordination diagnostics = %#v, want a successful no-op", got)
	}
}

func TestClaimLeaseScopesIncludeOnlyConfiguredActiveRigs(t *testing.T) {
	cityPath := t.TempDir()
	activePath := t.TempDir()
	suspendedPath := t.TempDir()
	cr := &CityRuntime{
		cityPath:            cityPath,
		cityName:            "city",
		cfg:                 &config.City{Rigs: []config.Rig{{Name: "active", Path: activePath}, {Name: "suspended", Path: suspendedPath, SuspendedOnStart: true}}},
		standaloneCityStore: newClaimLeaseTestStore(t),
		standaloneRigStores: map[string]beads.Store{
			"active":       newClaimLeaseTestStore(t),
			"suspended":    newClaimLeaseTestStore(t),
			"unregistered": newClaimLeaseTestStore(t),
		},
	}

	scopes, err := cr.claimLeaseScopes()
	if err != nil {
		t.Fatalf("claimLeaseScopes() error = %v", err)
	}
	if len(scopes) != 2 || scopes[0].Name != "city" || scopes[1].Name != "active" {
		t.Fatalf("lease scopes = %#v, want city and active configured rig only", scopes)
	}
}

func TestClaimLeaseScopesFailClosedWhenSuspensionStateIsUnreadable(t *testing.T) {
	cityPath := t.TempDir()
	if err := os.MkdirAll(citylayout.SuspensionStateFile(cityPath), 0o755); err != nil {
		t.Fatalf("create unreadable suspension-state path: %v", err)
	}
	cr := &CityRuntime{
		cityPath:            cityPath,
		cityName:            "city",
		cfg:                 &config.City{Rigs: []config.Rig{{Name: "rig", Path: t.TempDir()}}},
		standaloneCityStore: newClaimLeaseTestStore(t),
	}

	if scopes, err := cr.claimLeaseScopes(); err == nil || scopes != nil {
		t.Fatalf("claimLeaseScopes() = (%#v, %v), want nil error-gated scopes", scopes, err)
	}
}

func TestClaimLeaseScopesSnapshotsOneStandaloneReloadGeneration(t *testing.T) {
	cityPath := t.TempDir()
	cityStore := newClaimLeaseTestStore(t)
	trigA, trigB := newClaimLeaseTestStore(t), newClaimLeaseTestStore(t)
	cfgA := &config.City{Rigs: []config.Rig{{Name: "a", Path: t.TempDir()}}}
	cfgB := &config.City{Rigs: []config.Rig{{Name: "b", Path: t.TempDir()}}}
	cr := &CityRuntime{cityPath: cityPath, cityName: "city"}

	setGeneration := func(cfg *config.City, name string, store beads.Store) {
		cr.serviceStateMu.Lock()
		cr.cfg = cfg
		cr.standaloneCityStore = cityStore
		cr.standaloneRigStores = map[string]beads.Store{name: store}
		cr.serviceStateMu.Unlock()
	}
	setGeneration(cfgA, "a", trigA)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 500 {
			setGeneration(cfgB, "b", trigB)
			setGeneration(cfgA, "a", trigA)
		}
	}()
	for range 500 {
		scopes, err := cr.claimLeaseScopes()
		if err != nil {
			t.Fatalf("claimLeaseScopes() error = %v", err)
		}
		if len(scopes) != 2 {
			t.Fatalf("lease scopes = %#v, want one complete city+rig generation", scopes)
		}
		switch scopes[1].Name {
		case "a":
			if scopes[1].Store != trigA {
				t.Fatalf("rig a paired with store %#v, want generation-a store", scopes[1].Store)
			}
		case "b":
			if scopes[1].Store != trigB {
				t.Fatalf("rig b paired with store %#v, want generation-b store", scopes[1].Store)
			}
		default:
			t.Fatalf("lease scopes = %#v, want generation a or b", scopes)
		}
	}
	<-done
}

func TestApplyBdLeaseHolderClearsAmbientIdentityForReclaim(t *testing.T) {
	env := map[string]string{"BEADS_ACTOR": "stale-owner"}
	applyBdLeaseHolder(env, "", true)
	if env["BEADS_ACTOR"] != "controller" {
		t.Fatalf("reclaim actor = %q, want explicit controller actor", env["BEADS_ACTOR"])
	}

	applyBdLeaseHolder(env, "owner-identity", true)
	if env["BEADS_ACTOR"] != "owner-identity" {
		t.Fatalf("holder actor = %q, want exact owner identity", env["BEADS_ACTOR"])
	}
}
