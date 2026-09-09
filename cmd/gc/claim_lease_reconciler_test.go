package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

type claimLeaseHeartbeat struct {
	id     string
	holder string
}

type claimLeaseTestStore struct {
	*beads.MemStore
	listErr          error
	heartbeats       []claimLeaseHeartbeat
	heartbeatErr     error
	reclaimCalls     []time.Duration
	reclaimAssignees [][]string
	reclaimCount     int
	reclaimErr       error
	reclaimRows      bool
	leaseExpiresAt   time.Time
}

func (s *claimLeaseTestStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.MemStore.List(query)
}

func (s *claimLeaseTestStore) HeartbeatClaim(id, holder string) error {
	s.heartbeats = append(s.heartbeats, claimLeaseHeartbeat{id: id, holder: holder})
	if s.heartbeatErr == nil {
		s.leaseExpiresAt = time.Now().Add(5 * time.Minute)
	}
	return s.heartbeatErr
}

func (s *claimLeaseTestStore) ReclaimExpiredClaims(grace time.Duration, assignees ...string) (int, error) {
	s.reclaimCalls = append(s.reclaimCalls, grace)
	s.reclaimAssignees = append(s.reclaimAssignees, append([]string(nil), assignees...))
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
	if len(city.reclaimCalls) != 1 || len(rig.reclaimCalls) != 1 {
		t.Fatalf("reclaim calls = city:%v rig:%v, want one per scope", city.reclaimCalls, rig.reclaimCalls)
	}
	if len(city.reclaimAssignees) != 1 || len(city.reclaimAssignees[0]) != 1 || city.reclaimAssignees[0][0] != sessionName {
		t.Fatalf("city reclaim assignees = %v, want the observed owner only", city.reclaimAssignees)
	}
	if got.Stores[1].Reclaimed != 1 {
		t.Fatalf("rig diagnostics = %#v, want one reclaimed claim", got.Stores)
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
	if len(store.heartbeats) != 1 || len(store.reclaimCalls) != 1 || got.Stores[0].Renewed != 1 {
		t.Fatalf("active-rig lease result = heartbeats:%v reclaim:%v diagnostics:%#v", store.heartbeats, store.reclaimCalls, got)
	}
}

func TestReconcileClaimLeasesRejectsRecycledNamesTokensDrainingAndProviderFence(t *testing.T) {
	tests := []struct {
		name          string
		info          session.Info
		start         bool
		providerID    string
		providerToken string
		wantCalls     int
	}{
		{
			name:  "recycled runtime name is fenced by session identity",
			info:  session.Info{ID: "old-session", State: session.StateActive, MetadataState: string(session.StateActive), SessionName: "recycled", SessionNameMetadata: "recycled", InstanceToken: "old-token"},
			start: true, providerID: "replacement-session", providerToken: "new-token", wantCalls: 0,
		},
		{
			name:  "replaced token is fenced",
			info:  session.Info{ID: "session", State: session.StateActive, MetadataState: string(session.StateActive), SessionName: "worker", SessionNameMetadata: "worker", InstanceToken: "old-token"},
			start: true, providerToken: "new-token", wantCalls: 0,
		},
		{
			name:  "draining owner is not renewed",
			info:  session.Info{ID: "session", State: session.StateDraining, MetadataState: string(session.StateDraining), SessionName: "worker", SessionNameMetadata: "worker", InstanceToken: "token"},
			start: true, providerToken: "token", wantCalls: 0,
		},
		{
			name:  "provider fence without a live runtime",
			info:  session.Info{ID: "session", State: session.StateActive, MetadataState: string(session.StateActive), SessionName: "worker", SessionNameMetadata: "worker", InstanceToken: "token"},
			start: false, providerToken: "", wantCalls: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newClaimLeaseTestStore(t, beads.Bead{
				ID: "claim", Status: "in_progress", Assignee: tc.info.SessionName,
				Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: tc.info.ID},
			})
			provider := runtime.NewFake()
			if tc.start {
				if err := provider.Start(context.Background(), tc.info.SessionName, runtime.Config{}); err != nil {
					t.Fatal(err)
				}
				if err := provider.SetMeta(tc.info.SessionName, "GC_INSTANCE_TOKEN", tc.providerToken); err != nil {
					t.Fatal(err)
				}
				providerID := tc.providerID
				if providerID == "" {
					providerID = tc.info.ID
				}
				if err := provider.SetMeta(tc.info.SessionName, "GC_SESSION_ID", providerID); err != nil {
					t.Fatal(err)
				}
			}
			got := reconcileClaimLeases(context.Background(), provider, []session.Info{tc.info}, []claimLeaseScope{
				{Name: "city", Store: store, Lease: store},
			}, time.Now())
			if len(store.heartbeats) != tc.wantCalls {
				t.Fatalf("heartbeats = %#v, want %d", store.heartbeats, tc.wantCalls)
			}
			if got.Stores[0].Errors == 0 && tc.wantCalls == 0 {
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

func TestReconcileClaimLeasesDoesNotReclaimLiveProviderWithoutSessionIdentity(t *testing.T) {
	const (
		sessionID   = "persisted-session"
		sessionName = "worker"
		token       = "instance-token"
	)
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "identity-missing-claim", Status: "in_progress", Assignee: sessionName,
		Metadata: beads.StringMap{beadmeta.SessionIDMetadataKey: sessionID},
	})
	provider := runtime.NewFake()
	if err := provider.Start(context.Background(), sessionName, runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetMeta(sessionName, "GC_INSTANCE_TOKEN", token); err != nil {
		t.Fatal(err)
	}
	// A running provider with no session ID is fenced, not proven abandoned.
	got := reconcileClaimLeases(context.Background(), provider, []session.Info{{
		ID: sessionID, State: session.StateActive, MetadataState: string(session.StateActive),
		SessionName: sessionName, SessionNameMetadata: sessionName, InstanceToken: token,
	}}, []claimLeaseScope{{Name: "city", Store: store, Lease: store}}, time.Now())

	if len(store.heartbeats) != 0 || len(store.reclaimCalls) != 0 {
		t.Fatalf("missing provider identity operations = heartbeats:%v reclaim:%v, want none", store.heartbeats, store.reclaimCalls)
	}
	if got.Stores[0].Errors == 0 || !got.LastSuccessfulAt.IsZero() {
		t.Fatalf("missing provider identity diagnostics = %#v, want fenced error and no success", got)
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
	if len(healthy.reclaimCalls) != 1 || got.Stores[1].Reclaimed != 1 {
		t.Fatalf("healthy store was not independently reconciled: calls=%v report=%#v", healthy.reclaimCalls, got.Stores)
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

	cr.reconcileClaimLeasesIfDue(context.Background(), newSessionBeadSnapshotWithError(errors.New("session snapshot unavailable")), previous.Add(time.Minute))
	got := cr.claimLeaseReconciliation()
	if !got.LastSuccessfulAt.Equal(previous) {
		t.Fatalf("last successful timestamp = %v, want preserved %v", got.LastSuccessfulAt, previous)
	}
	if got.Stores[0].Errors != 1 {
		t.Fatalf("failure diagnostics = %#v, want one store error", got.Stores[0])
	}
}

func TestReconcileClaimLeasesReopensAbandonedClaimThroughNativeReclaim(t *testing.T) {
	store := newClaimLeaseTestStore(t, beads.Bead{
		ID: "abandoned", Status: "in_progress", Assignee: "direct-owner",
	})
	store.reclaimRows = true
	store.reclaimCount = 1

	got := reconcileClaimLeases(context.Background(), runtime.NewFake(), nil, []claimLeaseScope{
		{Name: "rig-a", Store: store, Lease: store},
	}, time.Now())

	if len(store.heartbeats) != 0 {
		t.Fatalf("abandoned claim heartbeats = %#v, want none", store.heartbeats)
	}
	if len(store.reclaimCalls) != 1 || store.reclaimCalls[0] != claimLeaseReclaimGrace {
		t.Fatalf("reclaim calls = %v, want one native grace call", store.reclaimCalls)
	}
	row, err := store.Get("abandoned")
	if err != nil {
		t.Fatalf("read reopened claim: %v", err)
	}
	if row.Status != "open" || row.Assignee != "" {
		t.Fatalf("abandoned claim = status %q assignee %q, want open/unassigned", row.Status, row.Assignee)
	}
	if got.Stores[0].Reclaimed != 1 || got.LastSuccessfulAt.IsZero() {
		t.Fatalf("reclaim diagnostics = %#v, want successful native reclaim", got)
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

	scopes := cr.claimLeaseScopes()
	if len(scopes) != 2 || scopes[0].Name != "city" || scopes[1].Name != "active" {
		t.Fatalf("lease scopes = %#v, want city and active configured rig only", scopes)
	}
}

func TestApplyBdLeaseHolderUsesControllerActorForReclaim(t *testing.T) {
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
