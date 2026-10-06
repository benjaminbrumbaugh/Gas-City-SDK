package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

func TestRecoveryResponderAdvisoryTimeoutHoldsWithoutConfiguredFallback(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var calls atomic.Int32
	advisor := recoveryAdvisorFunc(func(context.Context, RecoveryRequest) (RecoveryAdvice, error) {
		calls.Add(1)
		close(started)
		<-release
		return RecoveryAdvice{Outcome: RecoveryAdviceSelected, Target: "rig/second"}, nil
	})
	const advisoryTimeout = 25 * time.Millisecond
	responder := NewRecoveryResponder(session.NewStore(beads.SessionStore{Store: store}), store, RecoveryResponderOptions{
		Targets: []string{"rig/first", "rig/second"}, HoldDuration: time.Hour,
		AdvisoryTimeout: advisoryTimeout, Cooldown: time.Minute, MaxAttempts: 2, Advisor: advisor,
	})

	type reconcileResult struct {
		report RecoveryReport
		err    error
	}
	result := make(chan reconcileResult, 1)
	startedAt := time.Now()
	go func() {
		report, err := responder.Reconcile(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
		result <- reconcileResult{report: report, err: err}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("advisor was not called")
	}
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.report != (RecoveryReport{}) {
			t.Fatalf("report = %+v, want no work while advice is unavailable", got.report)
		}
	case <-time.After(time.Second):
		t.Fatal("Reconcile did not return within the bounded advisory timeout")
	}
	if elapsed := time.Since(startedAt); elapsed < advisoryTimeout || elapsed >= time.Second {
		t.Fatalf("Reconcile duration = %v, want advisory timeout through bounded return", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("advisor calls = %d, want 1", got)
	}
	if works := allRecoveryWork(t, store); len(works) != 0 {
		t.Fatalf("recovery work = %+v, want no configured-order fallback", works)
	}
}
