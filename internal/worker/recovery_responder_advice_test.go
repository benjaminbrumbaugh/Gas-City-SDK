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

type typedRecoveryAdvisorFunc func(context.Context, RecoveryRequest) (RecoveryAdvice, error)

func (f typedRecoveryAdvisorFunc) Recommend(ctx context.Context, req RecoveryRequest) (RecoveryAdvice, error) {
	return f(ctx, req)
}

func TestRecoveryResponderNoEligibleAdviceWaitsWithoutCreatingFallback(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	var calls int
	advisor := typedRecoveryAdvisorFunc(func(context.Context, RecoveryRequest) (RecoveryAdvice, error) {
		calls++
		return RecoveryAdvice{Outcome: RecoveryAdviceNoEligible}, nil
	})
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour,
		Cooldown: time.Minute, MaxAttempts: 2, Advisor: advisor,
	})

	report, err := responder.Reconcile(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report != (RecoveryReport{}) {
		t.Fatalf("report = %+v, want no launch for valid no-eligible advice", report)
	}
	if calls != 1 {
		t.Fatalf("advisor calls = %d, want one bounded advisory call", calls)
	}
	if works := allRecoveryWork(t, store); len(works) != 0 {
		t.Fatalf("recovery work = %+v, want no guessed fallback target", works)
	}
	info, err := session.NewStore(beads.SessionStore{Store: store}).Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryAttempt != "0" || info.RecoveryAttemptedTargets != "" || info.RecoveryWorkID != "" {
		t.Fatalf("no-eligible advice advanced recovery state: %+v", info)
	}
}

func TestRecoveryResponderUnavailableAdviceHoldsWithoutConfiguredFallback(t *testing.T) {
	store := recoveryTestStore(t, "model_not_found")
	advisor := typedRecoveryAdvisorFunc(func(context.Context, RecoveryRequest) (RecoveryAdvice, error) {
		return RecoveryAdvice{Outcome: RecoveryAdviceUnavailable}, errors.New("wayfinder unavailable")
	})
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
		MaxAttempts: 1, Advisor: advisor,
	})

	report, err := responder.Reconcile(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report != (RecoveryReport{}) {
		t.Fatalf("report = %+v, want hold on unavailable advice", report)
	}
	if works := allRecoveryWork(t, store); len(works) != 0 {
		t.Fatalf("recovery work = %+v, want no configured-order fallback", works)
	}
}

func TestRecoveryResponderSelectedAdviceMustNameRemainingTarget(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	advisor := typedRecoveryAdvisorFunc(func(context.Context, RecoveryRequest) (RecoveryAdvice, error) {
		return RecoveryAdvice{Outcome: RecoveryAdviceSelected, Target: "rig/not-configured"}, nil
	})
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
		MaxAttempts: 1, Advisor: advisor,
	})

	if _, err := responder.Reconcile(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("invalid selected target accepted")
	}
	if works := allRecoveryWork(t, store); len(works) != 0 {
		t.Fatalf("recovery work = %+v, want no launch from invalid selection", works)
	}
}

func TestRecoveryResponderNoAdvisorUsesExplicitConfiguredOrderPolicy(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first"}, HoldDuration: time.Hour, Cooldown: time.Minute,
		MaxAttempts: 1,
	})

	if _, err := responder.Reconcile(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	works := allRecoveryWork(t, store)
	if len(works) != 1 || works[0].Metadata[beadmeta.RecoveryTargetMetadataKey] != "rig/first" {
		t.Fatalf("configured-only recovery work = %+v, want explicit configured-order target", works)
	}
}
