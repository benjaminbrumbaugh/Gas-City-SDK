package worker

import (
	"context"
	"errors"
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

func TestHighConfidenceRecoveryImpairmentIsDeterministic(t *testing.T) {
	tests := []struct {
		name string
		info session.Info
		want string
	}{
		{name: "terminal model", info: session.Info{HealthState: "unhealthy", ProviderTerminalError: "model_not_found"}, want: "model_not_found"},
		{name: "terminal quota", info: session.Info{HealthState: "unhealthy", ProviderTerminalError: "quota_exceeded"}, want: "quota_exceeded"},
		{name: "strict modal", info: session.Info{HealthState: "unhealthy", HealthReason: "usage_limit_modal", QuarantinedUntil: "2026-09-11T13:00:00Z"}, want: "usage_limit_modal"},
		{name: "unknown unhealthy reason", info: session.Info{HealthState: "unhealthy", HealthReason: "looks_bad"}},
		{name: "terminal marker without unhealthy", info: session.Info{ProviderTerminalError: "quota_exceeded"}},
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

func TestRecoveryResponderVerifiesHealthyRunningSessionAndStartsUniqueRecurrence(t *testing.T) {
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
			if verified.RecoveryOutcome != "verified" || verified.RecoveryHoldUntil != "" || verified.HeldUntil != "" {
				t.Fatalf("verified state = %+v", verified)
			}
			if got, err := store.Get(firstWork.ID); err != nil || got.Status != "open" {
				t.Fatalf("ordinary work ownership changed: work=%+v err=%v", got, err)
			}

			if err := store.SetMetadataBatch("session-1", map[string]string{
				"state": "asleep", "session_health": "unhealthy", "provider_terminal_error": "quota_exceeded",
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
			if recurred.RecoveryIncidentID == firstIncident || recurred.RecoveryAttempt != "1" {
				t.Fatalf("recurrence reused prior incident: first=%q state=%+v", firstIncident, recurred)
			}
			assertRecoveryWorkCount(t, store, 2)
		})
	}
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
	}
	return beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "session-1", Type: session.BeadType, Status: "open", Title: "source", Labels: []string{session.LabelSession}, Metadata: metadata,
	}}, nil)
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
