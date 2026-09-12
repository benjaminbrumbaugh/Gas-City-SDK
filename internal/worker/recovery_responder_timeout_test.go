package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

func TestRecoveryResponderAdvisoryTimeoutFallsBackOnceInConfiguredOrder(t *testing.T) {
	store := recoveryTestStore(t, "quota_exceeded")
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var calls atomic.Int32
	advisor := recoveryAdvisorFunc(func(context.Context, RecoveryRequest) (string, error) {
		calls.Add(1)
		close(started)
		<-release
		return "rig/second", nil
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
		if got.report.Created != 1 {
			t.Fatalf("report = %+v, want one fallback work item", got.report)
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
	work := onlyRecoveryWork(t, store)
	if got := work.Metadata[beadmeta.RecoveryTargetMetadataKey]; got != "rig/first" {
		t.Fatalf("fallback target = %q, want first configured target", got)
	}
}
