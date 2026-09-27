package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func TestClaimDueQueuedNudgesForTargetRebindsMissingPinnedSessionButNotLiveSibling(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	now := time.Now().Add(-time.Minute)
	for _, item := range []queuedNudge{
		newQueuedNudgeWithOptions("worker", "rebind me", "session", now, queuedNudgeOptions{
			ID:        "n-replaced",
			SessionID: "gc-replaced",
		}),
		newQueuedNudgeWithOptions("worker", "keep for sibling", "session", now, queuedNudgeOptions{
			ID:        "n-sibling",
			SessionID: "gc-sibling",
		}),
	} {
		if err := enqueueQueuedNudge(dir, item); err != nil {
			t.Fatalf("enqueueQueuedNudge(%s): %v", item.ID, err)
		}
	}

	target := nudgeTarget{
		agent:                 config.Agent{Name: "worker"},
		sessionID:             "gc-current",
		rebindReplacedSession: true,
		replacementQueueAgentOwners: map[string]int{
			"worker": 1,
		},
		liveSessionIDs: map[string]struct{}{
			"gc-current": {},
			"gc-sibling": {},
		},
	}
	claimed, err := claimDueQueuedNudgesForTarget(dir, target, time.Now())
	if err != nil {
		t.Fatalf("claimDueQueuedNudgesForTarget: %v", err)
	}
	if got := queuedNudgeIDs(claimed); len(got) != 1 || got[0] != "n-replaced" {
		t.Fatalf("claimed IDs = %#v, want only n-replaced", got)
	}

	deliverable, rejected := splitQueuedNudgesForTarget(target, claimed)
	if len(deliverable) != 1 || deliverable[0].ID != "n-replaced" {
		t.Fatalf("deliverable = %#v, want n-replaced", deliverable)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected = %#v, want none", rejected)
	}

	pending, _, _, err := listQueuedNudges(dir, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "n-sibling" {
		t.Fatalf("pending = %#v, want only live sibling", pending)
	}
}

func TestDispatchAllQueuedNudgesRebindsReplacementSession(t *testing.T) {
	clearGCEnv(t)
	disableManagedDoltRecoveryForTest(t)
	clearInheritedCityRoutingEnv(t)
	t.Setenv("GC_BEADS", "file")

	dir := t.TempDir()
	store := openNudgeBeadStore(dir)
	if store.Store == nil {
		t.Fatal("openNudgeBeadStore returned nil")
	}
	current, err := store.Create(beads.Bead{
		Title:  "Session: worker",
		Type:   session.BeadType,
		Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"session_name":       "worker-session",
			"agent_name":         "worker",
			"template":           "worker",
			"transport":          "acp",
			"continuation_epoch": "2",
		},
	})
	if err != nil {
		t.Fatalf("create current session: %v", err)
	}
	sp := runtime.NewFake()
	if err := sp.Start(context.Background(), "worker-session", runtime.Config{}); err != nil {
		t.Fatalf("start current session: %v", err)
	}
	sp.SetActivity("worker-session", time.Now().Add(-time.Minute))
	item := newQueuedNudgeWithOptions("worker", "deliver after replacement", "session", time.Now().Add(-time.Minute), queuedNudgeOptions{
		ID:        "n-replaced-dispatch",
		SessionID: "gc-old-session",
	})
	if err := enqueueQueuedNudgeWithStore(dir, store, item); err != nil {
		t.Fatalf("enqueue replacement nudge: %v", err)
	}

	delivered, err := dispatchAllQueuedNudges(dir, supervisorCfg(), store, store, sp, newSessionBeadSnapshot([]beads.Bead{current}), nil)
	if err != nil {
		t.Fatalf("dispatchAllQueuedNudges: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("delivered = %d, want 1", delivered)
	}
	var messages []string
	for _, call := range sp.SnapshotCalls() {
		if call.Method == "Nudge" {
			messages = append(messages, call.Message)
		}
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "deliver after replacement") {
		t.Fatalf("nudge messages = %#v, want replacement message", messages)
	}
}
