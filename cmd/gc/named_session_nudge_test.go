package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

func namedSessionNudgeFixture(t *testing.T, status string) (runtime.Provider, *config.City, beads.Store, beads.Bead, beads.Bead, time.Time) {
	t.Helper()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:  "witness",
			Nudge: "gc hook --claim --drain-ack --json",
		}},
		NamedSessions: []config.NamedSession{{
			Template: "witness",
			Mode:     "on_demand",
		}},
	}
	store := beads.NewMemStore()
	session, err := store.Create(beads.Bead{
		Type:   sessionBeadType,
		Status: "open",
		Metadata: map[string]string{
			"session_name":              "test-city--witness",
			"template":                  "witness",
			"state":                     "active",
			"configured_named_session":  "true",
			"configured_named_identity": "witness",
			"configured_named_mode":     "on_demand",
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	wisp, err := store.Create(beads.Bead{
		Title:     "patrol step",
		Type:      "task",
		Assignee:  "witness",
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("create wisp: %v", err)
	}
	if status != "open" {
		if err := store.Update(wisp.ID, beads.UpdateOpts{Status: &status}); err != nil {
			t.Fatalf("set wisp status: %v", err)
		}
		wisp, err = store.Get(wisp.ID)
		if err != nil {
			t.Fatalf("re-read wisp: %v", err)
		}
	}
	sp := runtime.NewFake()
	if err := sp.Start(context.Background(), session.Metadata["session_name"], runtime.Config{}); err != nil {
		t.Fatalf("start provider: %v", err)
	}
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	sp.SetActivity(session.Metadata["session_name"], now.Add(-10*time.Minute))
	return sp, cfg, store, session, wisp, now
}

func namedSessionNudgeWork(wisp beads.Bead, store beads.Store) ([]beads.Bead, []beads.Store, []string) {
	return []beads.Bead{wisp}, []beads.Store{store}, []string{""}
}

func TestNudgeStalledNamedSessionClaims_NudgesQuietAssignedOpenWisp(t *testing.T) {
	sp, cfg, store, session, wisp, now := namedSessionNudgeFixture(t, "open")
	work, stores, refs := namedSessionNudgeWork(wisp, store)
	var out bytes.Buffer

	nudgeStalledNamedSessionClaims(sp, cfg, store, []beads.Bead{session}, work, stores, refs, now, &out)
	if got := sp.(*runtime.Fake).CountCalls("Nudge", session.Metadata["session_name"]); got != 0 {
		t.Fatalf("first tick nudge calls = %d, want 0 during observe grace", got)
	}

	now = now.Add(idleClaimNudgeGrace + time.Second)
	nudgeStalledNamedSessionClaims(sp, cfg, store, []beads.Bead{session}, work, stores, refs, now, &out)
	if got := sp.(*runtime.Fake).CountCalls("Nudge", session.Metadata["session_name"]); got != 1 {
		t.Fatalf("post-grace nudge calls = %d, want 1; output=%s", got, out.String())
	}
}

func TestNudgeStalledNamedSessionExecution_NudgesQuietAssignedInProgressWisp(t *testing.T) {
	sp, cfg, store, session, wisp, now := namedSessionNudgeFixture(t, "in_progress")
	work, stores, refs := namedSessionNudgeWork(wisp, store)
	var out bytes.Buffer

	nudgeStalledNamedSessionExecution(sp, cfg, store, []beads.Bead{session}, work, stores, refs, now, &out)
	now = now.Add(idleClaimNudgeGrace + time.Second)
	nudgeStalledNamedSessionExecution(sp, cfg, store, []beads.Bead{session}, work, stores, refs, now, &out)
	if got := sp.(*runtime.Fake).CountCalls("Nudge", session.Metadata["session_name"]); got != 1 {
		t.Fatalf("post-grace nudge calls = %d, want 1; output=%s", got, out.String())
	}
}

func TestNudgeStalledNamedSessionWork_SkipsAmbiguousOrIneligibleSessions(t *testing.T) {
	sp, cfg, store, session, wisp, now := namedSessionNudgeFixture(t, "open")
	other, err := store.Create(beads.Bead{
		Title:     "second patrol step",
		Type:      "task",
		Status:    "open",
		Assignee:  "witness",
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("create second wisp: %v", err)
	}
	work, stores, refs := []beads.Bead{wisp, other}, []beads.Store{store, store}, []string{"", ""}
	var out bytes.Buffer

	for _, tick := range []time.Time{now, now.Add(time.Hour)} {
		nudgeStalledNamedSessionClaims(sp, cfg, store, []beads.Bead{session}, work, stores, refs, tick, &out)
	}
	if got := sp.(*runtime.Fake).CountCalls("Nudge", session.Metadata["session_name"]); got != 0 {
		t.Fatalf("ambiguous named-session nudge calls = %d, want 0", got)
	}

	session.Metadata["configured_named_mode"] = "always"
	for _, tick := range []time.Time{now, now.Add(time.Hour)} {
		nudgeStalledNamedSessionClaims(sp, cfg, store, []beads.Bead{session}, []beads.Bead{wisp}, []beads.Store{store}, []string{""}, tick, &out)
	}
	if got := sp.(*runtime.Fake).CountCalls("Nudge", session.Metadata["session_name"]); got != 0 {
		t.Fatalf("always-mode named-session nudge calls = %d, want 0", got)
	}
}
