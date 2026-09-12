package worker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

type recoveryAdvisorFunc func(context.Context, RecoveryRequest) (string, error)

func (f recoveryAdvisorFunc) Recommend(ctx context.Context, req RecoveryRequest) (string, error) {
	return f(ctx, req)
}

func TestRecoveryResponderDisabledWithoutTargets(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{})

	report, err := responder.Reconcile(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report != (RecoveryReport{}) {
		t.Fatalf("report = %+v, want no-op", report)
	}
	assertRecoveryWorkCount(t, store, 0)
	info, err := session.NewStore(beads.SessionStore{Store: store}).Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryIncidentID != "" || info.HeldUntil != "" {
		t.Fatalf("disabled responder mutated session: %+v", info)
	}
}

type recoveryConditionalOnlyStore struct {
	beads.Store
	beads.ConditionalWriter
}

func TestRecoveryResponderUnsupportedDestinationFailsBeforeHoldMutation(t *testing.T) {
	base := recoveryTestStore(t, "quota_exceeded")
	store := &recoveryConditionalOnlyStore{Store: base, ConditionalWriter: base}
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour,
	})
	if _, err := responder.Reconcile(context.Background(), time.Now().UTC()); !errors.Is(err, beads.ErrDeterministicCreateUnsupported) {
		t.Fatalf("Reconcile error = %v, want unsupported deterministic destination", err)
	}
	persisted, err := base.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Metadata["held_until"] != "" || persisted.Metadata["recovery_incident_id"] != "" {
		t.Fatalf("unsupported destination mutated recovery state: %#v", persisted.Metadata)
	}
}

func TestHighConfidenceRecoveryImpairmentIsDeterministic(t *testing.T) {
	tests := []struct {
		name string
		info session.Info
		want string
	}{
		{name: "terminal model", info: session.Info{HealthState: "unhealthy", HealthReason: "model_not_found", ProviderTerminalError: "model_not_found", Drainable: true}, want: "model_not_found"},
		{name: "terminal quota", info: session.Info{HealthState: "unhealthy", HealthReason: "quota_exceeded", ProviderTerminalError: "quota_exceeded", Drainable: true}, want: "quota_exceeded"},
		{name: "strict modal", info: session.Info{HealthState: "unhealthy", HealthReason: "usage_limit_modal", QuarantinedUntil: "2026-09-11T13:00:00Z"}, want: "usage_limit_modal"},
		{name: "unknown unhealthy reason", info: session.Info{HealthState: "unhealthy", HealthReason: "looks_bad"}},
		{name: "terminal marker without unhealthy", info: session.Info{ProviderTerminalError: "quota_exceeded"}},
		{name: "terminal tuple without drainable", info: session.Info{HealthState: "unhealthy", HealthReason: "quota_exceeded", ProviderTerminalError: "quota_exceeded"}},
		{name: "stale terminal quota with unrelated health reason", info: session.Info{HealthState: "unhealthy", HealthReason: "heartbeat_timeout", ProviderTerminalError: "quota_exceeded", Drainable: true}},
		{name: "substring is not evidence", info: session.Info{HealthState: "unhealthy", ProviderTerminalError: "maybe_quota_exceeded_later"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HighConfidenceRecoveryImpairment(tt.info); got != tt.want {
				t.Fatalf("HighConfidenceRecoveryImpairment() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecoveryResponderUsesValidWayfinderRecommendationAndPersistsIncident(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	called := 0
	advisor := recoveryAdvisorFunc(func(ctx context.Context, req RecoveryRequest) (string, error) {
		called++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2100*time.Millisecond {
			t.Errorf("advisor deadline = %v, want bounded by 2s", deadline)
		}
		if len(req.Targets) != 2 || req.Targets[0] != "rig/first" || req.Targets[1] != "rig/second" {
			t.Fatalf("targets = %#v", req.Targets)
		}
		if !strings.HasPrefix(req.CorrelationID, "recovery-attempt-") {
			t.Fatalf("correlation id = %q, want deterministic attempt identity", req.CorrelationID)
		}
		return "rig/second", nil
	})
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: 20 * time.Minute,
		AdvisoryTimeout: 2 * time.Second, Cooldown: 5 * time.Minute, MaxAttempts: 2, Advisor: advisor,
	})

	report, err := responder.Reconcile(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 1 || called != 1 {
		t.Fatalf("report=%+v advisor calls=%d", report, called)
	}
	work := onlyRecoveryWork(t, store)
	if got := work.Metadata[beadmeta.RoutedToMetadataKey]; got != "rig/second" {
		t.Fatalf("routed_to = %q", got)
	}
	if got := work.Metadata[beadmeta.RecoveryAttemptMetadataKey]; got != "1" {
		t.Fatalf("attempt = %q", got)
	}
	if work.Type != "task" || work.Assignee != "" || work.Status != "open" {
		t.Fatalf("recovery work is not ordinary unassigned work: %+v", work)
	}
	info, err := session.NewStore(beads.SessionStore{Store: store}).Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryIncidentID == "" || info.RecoveryWorkID != work.ID || info.RecoveryAttempt != "1" || info.RecoveryOutcome != "active" {
		t.Fatalf("incident state not durable: %+v", info)
	}
	if info.HeldUntil != now.Add(20*time.Minute).Format(time.RFC3339) || info.RecoveryHoldUntil != info.HeldUntil {
		t.Fatalf("bounded hold = %q recovery hold = %q", info.HeldUntil, info.RecoveryHoldUntil)
	}

	// A fresh coordinator instance adopts the durable work rather than creating a duplicate.
	responder = NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, responder.Options())
	report, err = responder.Reconcile(context.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 0 || report.Active != 1 {
		t.Fatalf("second report = %+v", report)
	}
	assertRecoveryWorkCount(t, store, 1)
}

func TestRecoveryResponderRunsAtMostOneAdvisoryActionPerTick(t *testing.T) {
	metadata := func(name string) map[string]string {
		return map[string]string{
			"state": "asleep", "session_health": "unhealthy", "session_name": name, "provider": "provider-a",
			"provider_terminal_error": "quota_exceeded", "session_health_reason": "quota_exceeded", "session_drainable": "true",
		}
	}
	store := beads.NewMemStoreFrom(1, []beads.Bead{
		{ID: "session-1", Type: session.BeadType, Status: "open", Labels: []string{session.LabelSession}, Metadata: metadata("runtime-1")},
		{ID: "session-2", Type: session.BeadType, Status: "open", Labels: []string{session.LabelSession}, Metadata: metadata("runtime-2")},
	}, nil)
	calls := 0
	advisor := recoveryAdvisorFunc(func(context.Context, RecoveryRequest) (string, error) {
		calls++
		return "rig/first", nil
	})
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute, Advisor: advisor,
	})
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	report, err := responder.Reconcile(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || report.Created != 1 {
		t.Fatalf("first tick calls=%d report=%+v, want one advisory/create", calls, report)
	}
	report, err = responder.Reconcile(context.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	works := allRecoveryWork(t, store)
	if calls != 2 || report.Created != 1 || len(works) != 2 {
		t.Fatalf("second tick calls=%d report=%+v work=%d, want one more advisory/create", calls, report, len(works))
	}
}

func TestRecoveryResponderInvalidOrUnavailableAdviceFallsBackInOrder(t *testing.T) {
	tests := []struct {
		name    string
		advisor RecoveryAdvisor
	}{
		{name: "invalid", advisor: recoveryAdvisorFunc(func(context.Context, RecoveryRequest) (string, error) { return "rig/not-configured", nil })},
		{name: "unavailable", advisor: recoveryAdvisorFunc(func(context.Context, RecoveryRequest) (string, error) { return "", errors.New("down") })},
		{name: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := recoveryTestStore(t, "model_not_found")
			responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
				Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour,
				AdvisoryTimeout: time.Second, Cooldown: time.Minute, MaxAttempts: 2, Advisor: tt.advisor,
			})
			if _, err := responder.Reconcile(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			if got := onlyRecoveryWork(t, store).Metadata[beadmeta.RoutedToMetadataKey]; got != "rig/first" {
				t.Fatalf("fallback target = %q", got)
			}
		})
	}
}

func TestRecoveryResponderSerializesAttemptsCooldownAndOutcome(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour,
		AdvisoryTimeout: time.Second, Cooldown: 10 * time.Minute, MaxAttempts: 2,
	})
	if _, err := responder.Reconcile(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	first := onlyRecoveryWork(t, store)
	if err := store.Close(first.ID); err != nil {
		t.Fatal(err)
	}

	report, err := responder.Reconcile(context.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.CoolingDown != 1 || report.Created != 0 {
		t.Fatalf("cooldown report = %+v", report)
	}
	info, err := session.NewStore(beads.SessionStore{Store: store}).Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryOutcome != "attempt_closed" || info.RecoveryCooldownUntil != now.Add(11*time.Minute).Format(time.RFC3339) {
		t.Fatalf("closed attempt state = %+v", info)
	}

	// A fresh controller instance must adopt the durable cooldown and advance it.
	responder = NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, responder.Options())
	if _, err := responder.Reconcile(context.Background(), now.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	works := allRecoveryWork(t, store)
	if len(works) != 2 || works[1].Metadata[beadmeta.RoutedToMetadataKey] != "rig/second" {
		t.Fatalf("fallback attempts = %+v", works)
	}
	if err := store.Close(works[1].ID); err != nil {
		t.Fatal(err)
	}
	report, err = responder.Reconcile(context.Background(), now.Add(13*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Exhausted != 1 || report.Created != 0 {
		t.Fatalf("exhausted report = %+v", report)
	}
	info, err = session.NewStore(beads.SessionStore{Store: store}).Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryOutcome != "exhausted" || info.RecoveryAttempt != "2" {
		t.Fatalf("exhausted state = %+v", info)
	}
	assertRecoveryWorkCount(t, store, 2)
}

func TestRecoveryResponderVerifiesHealthyRunningSessionAndAdoptsOpenWorkOnRecurrence(t *testing.T) {
	for _, state := range []string{"active", "awake"} {
		t.Run(state, func(t *testing.T) {
			store := recoveryTestStore(t, "quota_exceeded")
			sessions := session.NewStore(beads.SessionStore{Store: store})
			now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
			responder := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
				Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour,
				Cooldown: time.Minute, MaxAttempts: 2,
			})
			if _, err := responder.Reconcile(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			firstInfo, err := sessions.Get("session-1")
			if err != nil {
				t.Fatal(err)
			}
			firstIncident := firstInfo.RecoveryIncidentID
			firstWork := onlyRecoveryWork(t, store)

			// The normal session reconciler is authoritative for recovery: a healthy
			// live state verifies even if a stale terminal-error marker remains.
			if err := store.SetMetadataBatch("session-1", map[string]string{
				"state": state, "session_health": "healthy",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := responder.Reconcile(context.Background(), now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			verified, err := sessions.Get("session-1")
			if err != nil {
				t.Fatal(err)
			}
			if verified.RecoveryOutcome != "verified" || verified.RecoveryHoldUntil != "" || verified.HeldUntil != "" ||
				verified.ProviderTerminalError != "" || verified.Drainable || verified.HealthReason != "" {
				t.Fatalf("verified state = %+v", verified)
			}
			if got, err := store.Get(firstWork.ID); err != nil || got.Status != "open" {
				t.Fatalf("ordinary work ownership changed: work=%+v err=%v", got, err)
			}

			if err := store.SetMetadataBatch("session-1", map[string]string{
				"state": "asleep", "session_health": "unhealthy", "session_health_reason": "quota_exceeded",
				"session_drainable": "true", "provider_terminal_error": "quota_exceeded",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := responder.Reconcile(context.Background(), now.Add(2*time.Minute)); err != nil {
				t.Fatal(err)
			}
			recurred, err := sessions.Get("session-1")
			if err != nil {
				t.Fatal(err)
			}
			if recurred.RecoveryIncidentID != firstIncident || recurred.RecoveryWorkID != firstWork.ID || recurred.RecoveryAttempt != "1" {
				t.Fatalf("recurrence did not adopt open work: first=%q work=%q state=%+v", firstIncident, firstWork.ID, recurred)
			}
			wantHold := now.Add(2*time.Minute + time.Hour).Format(time.RFC3339)
			if recurred.HeldUntil != wantHold || recurred.RecoveryHoldUntil != wantHold {
				t.Fatalf("recurrence hold = held %q recovery %q, want fresh bounded hold %q", recurred.HeldUntil, recurred.RecoveryHoldUntil, wantHold)
			}
			assertRecoveryWorkCount(t, store, 1)
		})
	}
}

type recoveryCASRaceStore struct {
	*beads.MemStore
	leaseArrivals chan struct{}
	startCAS      chan struct{}
	workCreated   chan struct{}
	createdOnce   sync.Once
}

func (s *recoveryCASRaceStore) UpdateIfMatch(id string, revision int64, opts beads.UpdateOpts) error {
	if next := opts.Metadata["recovery_responder_lease"]; next != "" && revision == 0 {
		s.leaseArrivals <- struct{}{}
		<-s.startCAS
	}
	err := s.MemStore.UpdateIfMatch(id, revision, opts)
	if opts.Metadata["recovery_responder_lease"] != "" && beads.IsPreconditionFailed(err) {
		<-s.workCreated
	}
	return err
}

func (s *recoveryCASRaceStore) CreateDeterministic(key string, bead beads.Bead) (beads.Bead, bool, error) {
	created, inserted, err := s.MemStore.CreateDeterministic(key, bead)
	if err == nil && hasRecoveryWorkLabel(bead.Labels) {
		s.createdOnce.Do(func() { close(s.workCreated) })
	}
	return created, inserted, err
}

type recoverySourceClosesAfterWorkCreateStore struct {
	*beads.MemStore
}

func (s *recoverySourceClosesAfterWorkCreateStore) CreateDeterministic(key string, candidate beads.Bead) (beads.Bead, bool, error) {
	work, created, err := s.MemStore.CreateDeterministic(key, candidate)
	if err == nil && created && hasRecoveryWorkLabel(candidate.Labels) {
		if closeErr := s.Close("session-1"); closeErr != nil {
			return beads.Bead{}, false, closeErr
		}
	}
	return work, created, err
}

func TestRecoveryResponderClosesNewWorkWhenSourceClosesBeforeActivation(t *testing.T) {
	store := &recoverySourceClosesAfterWorkCreateStore{MemStore: recoveryTestStore(t, "quota_exceeded")}
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour,
	})

	report, err := responder.Reconcile(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 0 || report.Active != 0 {
		t.Fatalf("report = %+v, want no executable recovery work", report)
	}
	works := allRecoveryWork(t, store)
	if len(works) != 1 || works[0].Status != "closed" {
		t.Fatalf("recovery work = %+v, want one compensated closed task", works)
	}
}

type recoverySourceAdoptsAfterWorkCreateStore struct {
	*beads.MemStore
}

func (s *recoverySourceAdoptsAfterWorkCreateStore) CreateDeterministic(key string, candidate beads.Bead) (beads.Bead, bool, error) {
	work, created, err := s.MemStore.CreateDeterministic(key, candidate)
	if err == nil && created && hasRecoveryWorkLabel(candidate.Labels) {
		if updateErr := s.SetMetadataBatch("session-1", map[string]string{
			"recovery_incident_id": candidate.Metadata[beadmeta.RecoveryIncidentMetadataKey],
			"recovery_outcome":     "active",
			"recovery_work_id":     work.ID,
		}); updateErr != nil {
			return beads.Bead{}, false, updateErr
		}
	}
	return work, created, err
}

func TestRecoveryResponderPreservesWorkAdoptedBeforeActivationCAS(t *testing.T) {
	store := &recoverySourceAdoptsAfterWorkCreateStore{MemStore: recoveryTestStore(t, "quota_exceeded")}
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour,
	})

	report, err := responder.Reconcile(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 0 || report.Active != 0 {
		t.Fatalf("report = %+v, want stale creator to report no activation", report)
	}
	work := onlyRecoveryWork(t, store)
	if work.Status == "closed" {
		t.Fatalf("adopted recovery work was closed: %+v", work)
	}
}

func TestRecoveryResponderConcurrentInstancesLeaseAndAdoptOneDeterministicAttempt(t *testing.T) {
	base := recoveryTestStore(t, "quota_exceeded")
	store := &recoveryCASRaceStore{
		MemStore:      base,
		leaseArrivals: make(chan struct{}, 2),
		startCAS:      make(chan struct{}),
		workCreated:   make(chan struct{}),
	}
	options := RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour,
		Cooldown: time.Minute, MaxAttempts: 2,
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	reports := make(chan RecoveryReport, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, options)
			report, err := responder.Reconcile(context.Background(), now)
			reports <- report
			errs <- err
		}()
	}
	<-store.leaseArrivals
	<-store.leaseArrivals
	close(store.startCAS)
	created, active := 0, 0
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		report := <-reports
		created += report.Created
		active += report.Active
	}
	if created != 1 || active != 1 {
		t.Fatalf("reports created=%d active=%d, want one winner and one adopter", created, active)
	}
	work := onlyRecoveryWork(t, store)
	incidentID := work.Metadata[beadmeta.RecoveryIncidentMetadataKey]
	if incidentID == "" || work.Metadata[beadmeta.RecoveryAttemptIDMetadataKey] != deterministicRecoveryAttemptID(incidentID, 1) {
		t.Fatalf("work lacks deterministic identity: %+v", work.Metadata)
	}
	info, err := session.NewStore(beads.SessionStore{Store: store}).Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryIncidentID != incidentID || info.RecoveryWorkID != work.ID {
		t.Fatalf("session did not converge on winner: %+v", info)
	}
}

type recoveryLeaseTakeoverStore struct {
	*beads.MemStore
	mu          sync.Mutex
	leaseClaims int
	expireNext  bool
}

func (s *recoveryLeaseTakeoverStore) Get(id string) (beads.Bead, error) {
	bead, err := s.MemStore.Get(id)
	if err != nil {
		return bead, err
	}
	s.mu.Lock()
	expire := s.expireNext
	if expire {
		s.expireNext = false
	}
	s.mu.Unlock()
	if token := strings.TrimSpace(bead.Metadata["recovery_responder_lease"]); expire && token != "" {
		bead.Metadata["recovery_responder_lease"] = "expired-owner\n1970-01-01T00:00:00Z"
	}
	return bead, nil
}

func (s *recoveryLeaseTakeoverStore) UpdateIfMatch(id string, revision int64, opts beads.UpdateOpts) error {
	if opts.Metadata["recovery_responder_lease"] == "" {
		return s.MemStore.UpdateIfMatch(id, revision, opts)
	}
	s.mu.Lock()
	s.leaseClaims++
	s.mu.Unlock()
	return s.MemStore.UpdateIfMatch(id, revision, opts)
}

func TestRecoveryResponderLeaseTakeoverDuringDecisionCreatesOneAttempt(t *testing.T) {
	base := recoveryTestStore(t, "quota_exceeded")
	base.HonorExplicitIDs = true
	store := &recoveryLeaseTakeoverStore{MemStore: base}
	firstAdvising := make(chan struct{})
	resumeFirst := make(chan struct{})
	var advisorCalls int
	var advisorMu sync.Mutex
	advisor := recoveryAdvisorFunc(func(context.Context, RecoveryRequest) (string, error) {
		advisorMu.Lock()
		advisorCalls++
		call := advisorCalls
		advisorMu.Unlock()
		if call == 1 {
			close(firstAdvising)
			<-resumeFirst
		}
		return "rig/first", nil
	})
	options := RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour,
		AdvisoryTimeout: time.Hour, Cooldown: time.Minute, MaxAttempts: 1, Advisor: advisor,
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	firstResult := make(chan error, 1)
	go func() {
		_, err := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, options).
			Reconcile(context.Background(), now)
		firstResult <- err
	}()
	<-firstAdvising
	store.mu.Lock()
	store.expireNext = true
	store.mu.Unlock()

	secondReport, err := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, options).
		Reconcile(context.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if secondReport.Created != 1 {
		t.Fatalf("takeover report = %+v, want one created attempt", secondReport)
	}
	close(resumeFirst)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}

	works := allRecoveryWork(t, store)
	if len(works) != 1 {
		t.Fatalf("lease takeover created %d attempts, want one: %+v", len(works), works)
	}
	attemptID := works[0].Metadata[beadmeta.RecoveryAttemptIDMetadataKey]
	if attemptID == "" || !strings.HasPrefix(works[0].ID, "gc-") || works[0].ID == attemptID {
		t.Fatalf("work id = %q, want store-valid id bound to deterministic attempt %q", works[0].ID, attemptID)
	}
}

type recoveryPlannedTakeoverStore struct {
	*recoveryLeaseTakeoverStore
	createMu    sync.Mutex
	createCalls int
	firstCreate chan struct{}
	resumeFirst chan struct{}
}

func (s *recoveryPlannedTakeoverStore) CreateDeterministic(key string, candidate beads.Bead) (beads.Bead, bool, error) {
	s.createMu.Lock()
	s.createCalls++
	call := s.createCalls
	s.createMu.Unlock()
	if call == 1 {
		close(s.firstCreate)
		<-s.resumeFirst
	}
	return s.MemStore.CreateDeterministic(key, candidate)
}

func TestRecoveryResponderLeaseTakeoverAfterPlanReusesBoundAttemptTuple(t *testing.T) {
	base := recoveryTestStore(t, "quota_exceeded")
	leaseStore := &recoveryLeaseTakeoverStore{MemStore: base}
	store := &recoveryPlannedTakeoverStore{
		recoveryLeaseTakeoverStore: leaseStore,
		firstCreate:                make(chan struct{}), resumeFirst: make(chan struct{}),
	}
	advisorCalls := 0
	advisor := recoveryAdvisorFunc(func(context.Context, RecoveryRequest) (string, error) {
		advisorCalls++
		if advisorCalls == 1 {
			return "rig/first", nil
		}
		return "rig/second", nil
	})
	options := RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour,
		AdvisoryTimeout: time.Hour, Cooldown: time.Minute, MaxAttempts: 2, Advisor: advisor,
	}
	now := time.Now().UTC()
	firstResult := make(chan error, 1)
	go func() {
		_, err := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, options).Reconcile(context.Background(), now)
		firstResult <- err
	}()
	<-store.firstCreate
	if err := base.SetMetadataBatch("session-1", map[string]string{
		"session_name": "operator-mutated-name",
		"provider":     "operator-mutated-provider",
	}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.expireNext = true
	store.mu.Unlock()

	report, err := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, options).Reconcile(context.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	close(store.resumeFirst)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	works := allRecoveryWork(t, store)
	if report.Created != 1 || len(works) != 1 || advisorCalls != 1 {
		t.Fatalf("report=%+v work=%d advisor calls=%d, want one bound planned attempt", report, len(works), advisorCalls)
	}
	if got := works[0].Metadata[beadmeta.RecoveryTargetMetadataKey]; got != "rig/first" {
		t.Fatalf("takeover changed planned target to %q", got)
	}
}

func TestRecoveryResponderRejectsPersistedPlanOutsideCurrentBounds(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	sessions := session.NewStore(beads.SessionStore{Store: store})
	info, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	incidentID := deterministicRecoveryIncidentID(info, "quota_exceeded")
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := store.SetMetadataBatch("session-1", map[string]string{
		"recovery_incident_id": incidentID, "recovery_impairment": "quota_exceeded",
		"recovery_detected_at": now.Format(time.RFC3339), "recovery_hold_until": now.Add(time.Hour).Format(time.RFC3339),
		"recovery_attempt": "1", "recovery_attempted_targets": "rig/removed", "recovery_outcome": "planned",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/current"}, HoldDuration: time.Hour, Cooldown: time.Minute, MaxAttempts: 1,
	}).Reconcile(context.Background(), now)
	if err == nil || !strings.Contains(err.Error(), "invalid persisted recovery plan") {
		t.Fatalf("Reconcile error = %v, want invalid persisted plan", err)
	}
	assertRecoveryWorkCount(t, store, 0)
}

func TestRecoveryResponderRejectsActiveWorkThatConflictsWithPersistedPlan(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	sessions := session.NewStore(beads.SessionStore{Store: store})
	info, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	plannedIncidentID := deterministicRecoveryIncidentID(info, "quota_exceeded")
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := store.SetMetadataBatch("session-1", map[string]string{
		"recovery_incident_id": plannedIncidentID, "recovery_impairment": "quota_exceeded",
		"recovery_detected_at": now.Format(time.RFC3339), "recovery_hold_until": now.Add(time.Hour).Format(time.RFC3339),
		"recovery_attempt": "1", "recovery_attempted_targets": "rig/first", "recovery_outcome": "planned",
	}); err != nil {
		t.Fatal(err)
	}
	conflictingIncidentID := "conflicting-incident"
	conflictingAttemptID := deterministicRecoveryAttemptID(conflictingIncidentID, 1)
	if _, err := store.Create(beads.Bead{
		ID: conflictingAttemptID, Title: "Recover impaired session session-1", Type: "task", Labels: []string{RecoveryWorkLabel},
		Description: recoveryWorkDescription("session-1", conflictingIncidentID, "quota_exceeded", "rig/second"),
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:              "rig/second",
			beadmeta.RecoverySourceSessionMetadataKey: "session-1",
			beadmeta.RecoveryIncidentMetadataKey:      conflictingIncidentID,
			beadmeta.RecoveryAttemptIDMetadataKey:     conflictingAttemptID,
			beadmeta.RecoveryAttemptMetadataKey:       "1",
			beadmeta.RecoveryImpairmentMetadataKey:    "quota_exceeded",
			beadmeta.RecoveryTargetMetadataKey:        "rig/second",
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour, Cooldown: time.Minute, MaxAttempts: 2,
	}).Reconcile(context.Background(), now)
	if err == nil || !strings.Contains(err.Error(), "conflicts with persisted recovery plan") {
		t.Fatalf("Reconcile error = %v, want persisted-plan conflict", err)
	}
	persisted, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RecoveryIncidentID != plannedIncidentID || persisted.RecoveryOutcome != "planned" || persisted.RecoveryAttemptedTargets != "rig/first" {
		t.Fatalf("conflicting adoption changed persisted plan: %+v", persisted)
	}
	assertRecoveryWorkCount(t, store, 1)
}

type recoveryDelayedSourceVisibilityStore struct {
	*beads.MemStore
	closeOnLiveIncidentRead bool
}

func (s *recoveryDelayedSourceVisibilityStore) ListByMetadata(filters map[string]string, limit int, opts ...beads.QueryOpt) ([]beads.Bead, error) {
	if _, sourceScoped := filters[beadmeta.RecoverySourceSessionMetadataKey]; sourceScoped {
		return nil, nil
	}
	return s.MemStore.ListByMetadata(filters, limit, opts...)
}

func (s *recoveryDelayedSourceVisibilityStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	works, err := s.MemStore.List(query)
	if err != nil || !s.closeOnLiveIncidentRead || !query.Live || query.Metadata[beadmeta.RecoveryIncidentMetadataKey] == "" {
		return works, err
	}
	s.closeOnLiveIncidentRead = false
	status := "closed"
	for _, work := range works {
		if work.Status != "closed" {
			if err := s.Update(work.ID, beads.UpdateOpts{Status: &status}); err != nil {
				return nil, err
			}
		}
	}
	return s.MemStore.List(query)
}

func TestRecoveryResponderAdoptsOpenWorkFromPersistedPlan(t *testing.T) {
	store := &recoveryDelayedSourceVisibilityStore{MemStore: recoveryTestStore(t, "quota_exceeded")}
	sessions := session.NewStore(beads.SessionStore{Store: store})
	info, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	incidentID := deterministicRecoveryIncidentID(info, "quota_exceeded")
	attemptID := deterministicRecoveryAttemptID(incidentID, 1)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := store.SetMetadataBatch("session-1", map[string]string{
		"recovery_incident_id": incidentID, "recovery_impairment": "quota_exceeded",
		"recovery_detected_at": now.Format(time.RFC3339), "recovery_hold_until": now.Add(time.Hour).Format(time.RFC3339),
		"recovery_attempt": "1", "recovery_attempted_targets": "rig/first", "recovery_outcome": "planned",
	}); err != nil {
		t.Fatal(err)
	}
	work, err := store.Create(beads.Bead{
		ID: attemptID, Title: "Recover impaired session session-1", Type: "task", Labels: []string{RecoveryWorkLabel},
		Description: recoveryWorkDescription("session-1", incidentID, "quota_exceeded", "rig/first"),
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:              "rig/first",
			beadmeta.RecoverySourceSessionMetadataKey: "session-1",
			beadmeta.RecoveryIncidentMetadataKey:      incidentID,
			beadmeta.RecoveryAttemptIDMetadataKey:     attemptID,
			beadmeta.RecoveryAttemptMetadataKey:       "1",
			beadmeta.RecoveryImpairmentMetadataKey:    "quota_exceeded",
			beadmeta.RecoveryTargetMetadataKey:        "rig/first",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour, Cooldown: time.Minute, MaxAttempts: 2,
	}).Reconcile(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Active != 1 || report.Created != 0 || report.CoolingDown != 0 {
		t.Fatalf("planned open adoption report = %+v", report)
	}
	persisted, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RecoveryOutcome != "active" || persisted.RecoveryWorkID != work.ID || persisted.RecoveryCooldownUntil != "" {
		t.Fatalf("planned open attempt was not adopted: %+v", persisted)
	}
	assertRecoveryWorkCount(t, store, 1)
}

func TestRecoveryResponderMarksPlannedAttemptClosedWhenItClosesBeforeAuthoritativeAdoption(t *testing.T) {
	store := &recoveryDelayedSourceVisibilityStore{
		MemStore:                recoveryTestStore(t, "quota_exceeded"),
		closeOnLiveIncidentRead: true,
	}
	sessions := session.NewStore(beads.SessionStore{Store: store})
	info, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	incidentID := deterministicRecoveryIncidentID(info, "quota_exceeded")
	attemptID := deterministicRecoveryAttemptID(incidentID, 1)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := store.SetMetadataBatch("session-1", map[string]string{
		"recovery_incident_id": incidentID, "recovery_impairment": "quota_exceeded",
		"recovery_detected_at": now.Format(time.RFC3339), "recovery_hold_until": now.Add(time.Hour).Format(time.RFC3339),
		"recovery_attempt": "1", "recovery_attempted_targets": "rig/first", "recovery_outcome": "planned",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(beads.Bead{
		ID: attemptID, Title: "Recover impaired session session-1", Type: "task", Labels: []string{RecoveryWorkLabel},
		Description: recoveryWorkDescription("session-1", incidentID, "quota_exceeded", "rig/first"),
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:              "rig/first",
			beadmeta.RecoverySourceSessionMetadataKey: "session-1",
			beadmeta.RecoveryIncidentMetadataKey:      incidentID,
			beadmeta.RecoveryAttemptIDMetadataKey:     attemptID,
			beadmeta.RecoveryAttemptMetadataKey:       "1",
			beadmeta.RecoveryImpairmentMetadataKey:    "quota_exceeded",
			beadmeta.RecoveryTargetMetadataKey:        "rig/first",
		},
	}); err != nil {
		t.Fatal(err)
	}

	report, err := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour, Cooldown: time.Minute, MaxAttempts: 2,
	}).Reconcile(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.CoolingDown != 1 || report.Active != 0 || report.Created != 0 {
		t.Fatalf("planned close-during-adoption report = %+v", report)
	}
	persisted, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RecoveryOutcome != "attempt_closed" || persisted.RecoveryWorkID != "" || persisted.RecoveryCooldownUntil != now.Add(time.Minute).Format(time.RFC3339) {
		t.Fatalf("planned attempt closed during adoption = %+v", persisted)
	}
}

func TestRecoveryResponderAdvancesClosedWorkFromPersistedPlan(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	sessions := session.NewStore(beads.SessionStore{Store: store})
	info, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	incidentID := deterministicRecoveryIncidentID(info, "quota_exceeded")
	attemptID := deterministicRecoveryAttemptID(incidentID, 1)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := store.SetMetadataBatch("session-1", map[string]string{
		"recovery_incident_id": incidentID, "recovery_impairment": "quota_exceeded",
		"recovery_detected_at": now.Format(time.RFC3339), "recovery_hold_until": now.Add(time.Hour).Format(time.RFC3339),
		"recovery_attempt": "1", "recovery_attempted_targets": "rig/first", "recovery_outcome": "planned",
	}); err != nil {
		t.Fatal(err)
	}
	work, err := store.Create(beads.Bead{
		ID: attemptID, Title: "Recover impaired session session-1", Type: "task", Labels: []string{RecoveryWorkLabel},
		Description: recoveryWorkDescription("session-1", incidentID, "quota_exceeded", "rig/first"),
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:              "rig/first",
			beadmeta.RecoverySourceSessionMetadataKey: "session-1",
			beadmeta.RecoveryIncidentMetadataKey:      incidentID,
			beadmeta.RecoveryAttemptIDMetadataKey:     attemptID,
			beadmeta.RecoveryAttemptMetadataKey:       "1",
			beadmeta.RecoveryImpairmentMetadataKey:    "quota_exceeded",
			beadmeta.RecoveryTargetMetadataKey:        "rig/first",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(work.ID); err != nil {
		t.Fatal(err)
	}

	report, err := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour, Cooldown: 7 * time.Minute, MaxAttempts: 2,
	}).Reconcile(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.CoolingDown != 1 || report.Created != 0 || report.Active != 0 {
		t.Fatalf("report = %+v, want closed planned attempt in cooldown", report)
	}
	persisted, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RecoveryOutcome != "attempt_closed" || persisted.RecoveryAttempt != "1" ||
		persisted.RecoveryAttemptedTargets != "rig/first" || persisted.RecoveryWorkID != "" ||
		persisted.RecoveryCooldownUntil != now.Add(7*time.Minute).Format(time.RFC3339) {
		t.Fatalf("closed planned attempt state = %+v", persisted)
	}
	assertRecoveryWorkCount(t, store, 1)
}

func TestRecoveryResponderRejectsMalformedSourceWorkInsteadOfAdopting(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	if _, err := store.Create(beads.Bead{
		Title: "operator work", Type: "task", Labels: []string{RecoveryWorkLabel},
		Metadata: map[string]string{beadmeta.RecoverySourceSessionMetadataKey: "session-1"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
	}).Reconcile(context.Background(), time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "conflicts with its deterministic attempt tuple") {
		t.Fatalf("Reconcile error = %v, want deterministic tuple conflict", err)
	}
	assertRecoveryWorkCount(t, store, 1)
}

func TestRecoveryResponderVerificationPreservesOperatorModifiedHold(t *testing.T) {
	store := recoveryTestStore(t, "model_not_found")
	sessions := session.NewStore(beads.SessionStore{Store: store})
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	responder := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
	})
	if _, err := responder.Reconcile(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	operatorHold := now.Add(6 * time.Hour).Format(time.RFC3339)
	if err := store.SetMetadataBatch("session-1", map[string]string{
		"held_until": operatorHold, "state": "active", "session_health": "healthy",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Reconcile(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	info, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryOutcome != "verified" || info.RecoveryHoldUntil != "" || info.HeldUntil != operatorHold {
		t.Fatalf("operator hold was not preserved: %+v", info)
	}
	persisted, err := store.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Metadata["recovery_hold_owned"]; got != "" {
		t.Fatalf("stale recovery hold ownership = %q, want retired", got)
	}

	if err := store.SetMetadataBatch("session-1", map[string]string{
		"held_until": "", "state": "asleep", "session_health": "unhealthy",
		"session_health_reason": "model_not_found", "session_drainable": "true",
		"provider_terminal_error": "model_not_found",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Reconcile(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recurred, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	wantHold := now.Add(2*time.Minute + time.Hour).Format(time.RFC3339)
	if recurred.HeldUntil != wantHold || recurred.RecoveryHoldUntil != wantHold {
		t.Fatalf("recurrence did not reacquire hold: %+v", recurred)
	}
	persisted, err = store.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Metadata["recovery_hold_owned"]; got != wantHold {
		t.Fatalf("recurrence ownership = %q, want %q", got, wantHold)
	}
	assertRecoveryWorkCount(t, store, 1)
}

func TestRecoveryResponderVerificationPreservesUnrelatedOperatorHealthReason(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	sessions := session.NewStore(beads.SessionStore{Store: store})
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	responder := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
	})
	if _, err := responder.Reconcile(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMetadataBatch("session-1", map[string]string{
		"state": "active", "session_health": "healthy", "session_health_reason": "operator-maintenance",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Reconcile(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	info, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryOutcome != "verified" || info.HealthReason != "operator-maintenance" {
		t.Fatalf("verification erased unrelated operator health: %+v", info)
	}
}

type recoveryVerificationRaceStore struct {
	*beads.MemStore
	armed bool
	reads int
}

func (s *recoveryVerificationRaceStore) Get(id string) (beads.Bead, error) {
	if s.armed {
		s.reads++
	}
	if s.armed && s.reads == 2 {
		s.armed = false
		if err := s.SetMetadataBatch(id, map[string]string{
			"state": "asleep", "session_health": "unhealthy", "session_health_reason": "quota_exceeded",
			"session_drainable": "true", "provider_terminal_error": "quota_exceeded",
			"provider_terminal_error_at": "2026-09-11T12:02:00Z",
		}); err != nil {
			return beads.Bead{}, err
		}
	}
	return s.MemStore.Get(id)
}

func TestRecoveryResponderVerificationCannotEraseConcurrentTerminalRecurrence(t *testing.T) {
	base := recoveryTestStore(t, "quota_exceeded")
	store := &recoveryVerificationRaceStore{MemStore: base}
	sessions := session.NewStore(beads.SessionStore{Store: store})
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	responder := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
	})
	if _, err := responder.Reconcile(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	work := onlyRecoveryWork(t, store)
	if err := store.SetMetadataBatch("session-1", map[string]string{
		"state": "active", "session_health": "healthy",
	}); err != nil {
		t.Fatal(err)
	}
	store.armed = true
	if _, err := responder.Reconcile(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	traced, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if traced.RecoveryOutcome == "verified" || traced.ProviderTerminalError != "quota_exceeded" ||
		traced.HealthState != "unhealthy" || traced.HealthReason != "quota_exceeded" || !traced.Drainable {
		t.Fatalf("stale verification erased terminal recurrence: %+v", traced)
	}

	report, err := responder.Reconcile(context.Background(), now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Active != 1 || report.Created != 0 {
		t.Fatalf("recurrence report = %+v, want adoption of existing work", report)
	}
	recurred, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if recurred.RecoveryWorkID != work.ID || recurred.ProviderTerminalError != "quota_exceeded" {
		t.Fatalf("recurrence did not survive and adopt existing work: %+v", recurred)
	}
	assertRecoveryWorkCount(t, store, 1)
}

type recoveryPostDecisionRaceStore struct {
	*beads.MemStore
	armed bool
}

func (s *recoveryPostDecisionRaceStore) CreateDeterministic(key string, candidate beads.Bead) (beads.Bead, bool, error) {
	if s.armed && hasRecoveryWorkLabel(candidate.Labels) {
		s.armed = false
		if err := s.SetMetadataBatch("session-1", map[string]string{
			"state": "active", "session_health": "healthy", "session_health_reason": "",
			"session_drainable": "", "provider_terminal_error": "", "provider_terminal_error_at": "",
			"recovery_outcome": "verified", "recovery_work_id": "", "recovery_hold_until": "", "held_until": "",
		}); err != nil {
			return beads.Bead{}, false, err
		}
	}
	return s.MemStore.CreateDeterministic(key, candidate)
}

func TestRecoveryResponderCreationCannotOverwriteConcurrentVerification(t *testing.T) {
	store := &recoveryPostDecisionRaceStore{MemStore: recoveryTestStore(t, "quota_exceeded"), armed: true}
	sessions := session.NewStore(beads.SessionStore{Store: store})
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	responder := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
	})
	report, err := responder.Reconcile(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 0 || report.Active != 0 {
		t.Fatalf("report = %+v, want stale creator to report no activation", report)
	}
	info, err := sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryOutcome != "verified" || info.RecoveryWorkID != "" || info.HealthState != "healthy" ||
		info.ProviderTerminalError != "" || info.Drainable {
		t.Fatalf("stale creator overwrote concurrent verification: %+v", info)
	}
	work := onlyRecoveryWork(t, store)
	if work.Status != "closed" {
		t.Fatalf("unbound recovery work remained executable: %+v", work)
	}
}

type recoveryAdoptionRaceStore struct {
	*beads.MemStore
	armed bool
	reads int
}

func (s *recoveryAdoptionRaceStore) Get(id string) (beads.Bead, error) {
	if s.armed && id == "session-1" {
		s.reads++
	}
	if s.armed && s.reads == 3 {
		s.armed = false
		if err := s.SetMetadataBatch(id, map[string]string{
			"state": "active", "session_health": "healthy", "session_health_reason": "",
			"session_drainable": "", "provider_terminal_error": "", "provider_terminal_error_at": "",
			"recovery_outcome": "verified", "recovery_work_id": "", "recovery_hold_until": "", "held_until": "",
		}); err != nil {
			return beads.Bead{}, err
		}
	}
	return s.MemStore.Get(id)
}

func TestRecoveryResponderAdoptionCannotOverwriteConcurrentVerification(t *testing.T) {
	base := recoveryTestStore(t, "quota_exceeded")
	info, err := session.NewStore(beads.SessionStore{Store: base}).Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	incidentID := deterministicRecoveryIncidentID(info, "quota_exceeded")
	attemptID := deterministicRecoveryAttemptID(incidentID, 1)
	work, err := base.Create(beads.Bead{
		ID: "recovery-attempt", Title: "Recover impaired session session-1", Type: "task", Status: "open", Labels: []string{RecoveryWorkLabel},
		Description: recoveryWorkDescription("session-1", incidentID, "quota_exceeded", "rig/first"),
		Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:              "rig/first",
			beadmeta.RecoverySourceSessionMetadataKey: "session-1",
			beadmeta.RecoveryIncidentMetadataKey:      incidentID,
			beadmeta.RecoveryAttemptIDMetadataKey:     attemptID,
			beadmeta.RecoveryAttemptMetadataKey:       "1",
			beadmeta.RecoveryImpairmentMetadataKey:    "quota_exceeded",
			beadmeta.RecoveryTargetMetadataKey:        "rig/first",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &recoveryAdoptionRaceStore{MemStore: base, armed: true}
	sessions := session.NewStore(beads.SessionStore{Store: store})
	responder := NewRecoveryResponder(sessions, store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
	})
	report, err := responder.Reconcile(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Active != 1 || report.Created != 0 {
		t.Fatalf("report = %+v, want existing-work adoption attempt", report)
	}
	info, err = sessions.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryOutcome != "verified" || info.RecoveryWorkID != "" || info.HealthState != "healthy" {
		t.Fatalf("stale adoption overwrote concurrent verification with work %s: %+v", work.ID, info)
	}
	assertRecoveryWorkCount(t, store, 1)
}

func recoveryTestStore(t *testing.T, impairment string) *beads.MemStore {
	t.Helper()
	metadata := map[string]string{
		"state": "asleep", "session_health": "unhealthy", "session_name": "runtime-1", "provider": "provider-a",
	}
	if impairment == "usage_limit_modal" {
		metadata["session_health_reason"] = impairment
		metadata["quarantined_until"] = "2026-09-12T12:00:00Z"
	} else {
		metadata["provider_terminal_error"] = impairment
		metadata["session_health_reason"] = impairment
		metadata["session_drainable"] = "true"
	}
	store := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "session-1", Type: session.BeadType, Status: "open", Title: "source", Labels: []string{session.LabelSession}, Metadata: metadata,
	}}, nil)
	store.HonorExplicitIDs = true
	return store
}

func allRecoveryWork(t *testing.T, store beads.Store) []beads.Bead {
	t.Helper()
	works, err := store.List(beads.ListQuery{Label: RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	return works
}

func onlyRecoveryWork(t *testing.T, store beads.Store) beads.Bead {
	t.Helper()
	works, err := store.List(beads.ListQuery{Label: RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 {
		t.Fatalf("recovery work count = %d, want 1", len(works))
	}
	return works[0]
}

func assertRecoveryWorkCount(t *testing.T, store beads.Store, want int) {
	t.Helper()
	works, err := store.List(beads.ListQuery{Label: RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != want {
		t.Fatalf("recovery work count = %d, want %d", len(works), want)
	}
}
