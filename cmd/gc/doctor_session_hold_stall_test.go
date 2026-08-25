package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// holdStallNow is the fixed clock every case in this file reads, so the
// far-future threshold is evaluated against a stable "now" instead of wall time.
var holdStallNow = time.Date(2026, 8, 25, 11, 10, 51, 0, time.UTC)

func holdStallAt(d time.Duration) string {
	return holdStallNow.Add(d).UTC().Format(time.RFC3339)
}

// holdStallSessionBead builds a session bead the session front door will project.
func holdStallSessionBead(id, name string, meta map[string]string) beads.Bead {
	m := map[string]string{"session_name": name}
	for k, v := range meta {
		m[k] = v
	}
	return beads.Bead{
		ID:       id,
		Title:    "session " + name,
		Type:     sessionpkg.BeadType,
		Status:   "open",
		Labels:   []string{sessionpkg.LabelSession},
		Metadata: m,
	}
}

// TestSessionHoldStallCheck covers gc-yrc: a session parked under a hold no
// timer will retire, and work left in_progress against a session whose runtime
// is down, are both invisible today — the pool-demand query counts only
// UNASSIGNED routed beads, so an assigned bead is not demand, no demand means no
// wake reason, and the queue stalls with no error anywhere.
func TestSessionHoldStallCheck(t *testing.T) {
	cityDir := t.TempDir()
	rigDir := t.TempDir()
	cfg := &config.City{Rigs: []config.Rig{{Name: "repo", Path: rigDir}}}

	cityStore := beads.NewMemStoreFrom(0, []beads.Bead{
		// The gc-yrc fingerprint: suspend wrote the 100-year sentinel, the
		// session then drained, and nothing cleared either field.
		holdStallSessionBead("gc-wisp-uweq2q", "city-worker", map[string]string{
			"state":        "asleep",
			"sleep_reason": "drained",
			"sleep_intent": "user-hold",
			"held_until":   holdStallAt(100 * 365 * 24 * time.Hour),
		}),
		// Live worker holding work normally — must never be reported.
		holdStallSessionBead("gc-wisp-alive", "deacon", map[string]string{"state": "active"}),
		// A heartbeat hold is bounded (12h cap) and belongs to a LIVE runtime:
		// neither the far-future arm nor the stranded arm may fire on it.
		holdStallSessionBead("gc-wisp-beat", "mayor", map[string]string{
			"state":      "active",
			"held_until": holdStallAt(1 * time.Hour),
		}),
		// Stopped session with no hold at all: still strands work, so the
		// stranded arm must not be gated on held_until.
		holdStallSessionBead("gc-wisp-idle", "scribe", map[string]string{
			"state":        "asleep",
			"sleep_reason": "idle",
		}),

		{ID: "gc-stranded", Title: "routed work", Type: "task", Status: "in_progress", Assignee: "city-worker"},
		{ID: "gc-stranded-byid", Title: "routed work", Type: "task", Status: "in_progress", Assignee: "gc-wisp-uweq2q"},
		{ID: "gc-stranded-idle", Title: "routed work", Type: "task", Status: "in_progress", Assignee: "scribe"},
		// Assigned to a LIVE session — healthy, must not be reported.
		{ID: "gc-healthy", Title: "live work", Type: "task", Status: "in_progress", Assignee: "deacon"},
		// A human assignee resolves to no session bead. The check cannot tell
		// "stranded" from "not a session" here, so it must stay quiet.
		{ID: "gc-human", Title: "human work", Type: "task", Status: "in_progress", Assignee: "ben"},
		// Not in_progress — an open bead assigned to a dead session is already
		// visible to pool demand and is not this check's business.
		{ID: "gc-open", Title: "queued work", Type: "task", Status: "open", Assignee: "city-worker"},
	}, nil)

	rigStore := beads.NewMemStoreFrom(0, []beads.Bead{
		{ID: "wf-stranded", Title: "rig work", Type: "task", Status: "in_progress", Assignee: "city-worker"},
	}, nil)

	stores := map[string]beads.Store{cityDir: cityStore, rigDir: rigStore}
	factory := func(path string) (beads.Store, error) {
		store, ok := stores[path]
		if !ok {
			return nil, fmt.Errorf("unexpected store path %q", path)
		}
		return store, nil
	}

	check := newSessionHoldStallCheck(cfg, cityDir, factory)
	check.now = func() time.Time { return holdStallNow }

	res := check.Run(&doctor.CheckContext{})
	if res.Status != doctor.StatusWarning {
		t.Fatalf("Run status = %v, want warning: %#v", res.Status, res)
	}
	details := strings.Join(res.Details, "\n")

	// The far-future hold is reported, and named with the value that makes it
	// obvious no timer will retire it.
	if !strings.Contains(details, "gc-wisp-uweq2q") || !strings.Contains(details, "2126") {
		t.Errorf("details missing the far-future hold on gc-wisp-uweq2q:\n%s", details)
	}
	// Every stranded claim is reported, in both stores and under both the
	// session-name and session-ID forms of the assignee.
	for _, want := range []string{"gc-stranded", "gc-stranded-byid", "gc-stranded-idle", "wf-stranded"} {
		if !strings.Contains(details, want) {
			t.Errorf("details missing stranded bead %s:\n%s", want, details)
		}
	}
	if !strings.Contains(details, "rig repo") {
		t.Errorf("details missing the rig scope label:\n%s", details)
	}
	// Healthy state must stay out of the report entirely.
	for _, unwanted := range []string{"gc-healthy", "gc-human", "gc-open", "gc-wisp-alive", "gc-wisp-beat"} {
		if strings.Contains(details, unwanted) {
			t.Errorf("details wrongly reported %s:\n%s", unwanted, details)
		}
	}
	// The hint must name the repair that actually restores pool demand;
	// waking the session alone leaves the bead assigned to a dead worker.
	if !strings.Contains(res.FixHint, "gc session wake") || !strings.Contains(res.FixHint, "bd unclaim") {
		t.Errorf("FixHint = %q, want it to name both repair steps", res.FixHint)
	}
	if check.CanFix() {
		t.Error("CanFix() = true, want false: releasing a live claim is an operator call")
	}
	if err := check.Fix(&doctor.CheckContext{}); err != nil {
		t.Errorf("Fix() = %v, want nil no-op", err)
	}
}

// TestSessionHoldStallCheckHealthy pins the quiet case: a city with only live
// sessions and bounded holds must report OK, or the check is noise operators
// learn to ignore.
func TestSessionHoldStallCheckHealthy(t *testing.T) {
	cityDir := t.TempDir()
	cityStore := beads.NewMemStoreFrom(0, []beads.Bead{
		holdStallSessionBead("gc-wisp-alive", "deacon", map[string]string{"state": "active"}),
		holdStallSessionBead("gc-wisp-beat", "mayor", map[string]string{
			"state":      "active",
			"held_until": holdStallAt(12 * time.Hour),
		}),
		// A stopped session with NO assigned work is an ordinary idle recycle.
		holdStallSessionBead("gc-wisp-idle", "scribe", map[string]string{
			"state":        "asleep",
			"sleep_reason": "idle",
		}),
		{ID: "gc-healthy", Title: "live work", Type: "task", Status: "in_progress", Assignee: "deacon"},
	}, nil)
	factory := func(path string) (beads.Store, error) {
		if path != cityDir {
			return nil, fmt.Errorf("unexpected store path %q", path)
		}
		return cityStore, nil
	}

	check := newSessionHoldStallCheck(&config.City{}, cityDir, factory)
	check.now = func() time.Time { return holdStallNow }

	res := check.Run(&doctor.CheckContext{})
	if res.Status != doctor.StatusOK {
		t.Fatalf("Run status = %v, want ok: %#v", res.Status, res)
	}
}

// TestSessionRuntimeExpectedDown pins the allowlist direction of the liveness
// classifier. It must answer "down" only for states it recognizes as stopped: a
// state added later, or a bead with no state metadata at all, has to fall
// through to "not down", or the check starts reporting live workers as stranded
// and the operator's first action is to unclaim work out from under one.
func TestSessionRuntimeExpectedDown(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{"active", false},
		{"awake", false},
		{"creating", false},
		{"start-pending", false},
		{"draining", false},
		{"asleep", true},
		{"suspended", true},
		{"drained", true},
		{"failed-create", true},
		{"archived", true},
		{"quarantined", true},
		{" asleep ", true},
		{"", false},
		{"some-future-state", false},
	}
	for _, tc := range cases {
		got := sessionRuntimeExpectedDown(sessionpkg.Info{MetadataState: tc.state})
		if got != tc.want {
			t.Errorf("sessionRuntimeExpectedDown(state=%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}
