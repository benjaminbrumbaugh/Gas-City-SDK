package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// inProgressReadCounter counts every in_progress store read the active-bead
// lookup performs, in either the per-identity (Assignee) or batched
// (Assignees) form, so the count is a fan-out measurement rather than a
// query-shape assertion.
type inProgressReadCounter struct {
	beads.Store
	reads int
}

func (s *inProgressReadCounter) List(query beads.ListQuery) ([]beads.Bead, error) {
	if query.Status == "in_progress" {
		s.reads++
	}
	return s.Store.List(query)
}

// TestAgentListActiveBeadReadsDoNotScaleWithAgentCount pins the fan-out that
// made gc supervisor run burn >2 cores steady-state (gc-68qc).
//
// The active-bead lookup is per-identity, and the agent list handler runs it
// once per row with three identities (session id, session name, qualified
// name). Resolving each identity with its own targeted store read costs
// O(agents x identities x stores) reads. CachingStore cannot absorb that:
// CachedList serves from a whole-snapshot cache that reports a miss whenever
// any bead is dirty, so one dirty bead turns every read into a bd subprocess.
//
// The read count must therefore be bounded by the number of STORES, not by the
// number of agents. Without the index this test observes one read per agent
// per identity.
func TestAgentListActiveBeadReadsDoNotScaleWithAgentCount(t *testing.T) {
	const agentCount = 8

	state := newFakeState(t)
	state.cfg.Agents = []config.Agent{
		{
			Name:              "polecat",
			Dir:               "myrig",
			MinActiveSessions: intPtr(1), MaxActiveSessions: intPtr(agentCount), ScaleCheck: fmt.Sprintf("echo %d", agentCount),
		},
	}

	store := &inProgressReadCounter{Store: beads.NewMemStore()}
	state.stores["myrig"] = store

	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)

	req := httptest.NewRequest("GET", cityURL(state, "/agents"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Items []agentResponse `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != agentCount {
		t.Fatalf("Total = %d, want %d — the fan-out assertion below is only meaningful with every agent listed", resp.Total, agentCount)
	}

	// One store in scope, so one read. The guard is deliberately expressed
	// against agentCount rather than a bare literal: what must not happen is
	// the read count tracking the row count.
	if store.reads > 1 {
		t.Fatalf("in_progress store reads = %d for %d agents, want 1 (one read per store, reused across rows)", store.reads, agentCount)
	}
}

// TestActiveBeadIndexPreservesIdentityMajorPrecedence pins the resolution order
// the batched index has to reproduce: identity-major, then store order, then
// newest-created. Getting this wrong is silent — the wrong bead id is still a
// real bead id — so it is asserted directly rather than through the handler.
//
// Two stores each hold in_progress work, for DIFFERENT identities. The first
// identity argument must win even though its bead lives in the
// later-sorted store; a store-major walk would return the other one.
func TestActiveBeadIndexPreservesIdentityMajorPrecedence(t *testing.T) {
	state := newFakeState(t)

	early := beads.NewMemStore()
	late := beads.NewMemStore()
	state.stores["aaa-rig"] = early
	state.stores["zzz-rig"] = late

	mkActive := func(t *testing.T, store beads.Store, title, assignee string) beads.Bead {
		t.Helper()
		b, err := store.Create(beads.Bead{Title: title})
		if err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
		status := "in_progress"
		if err := store.Update(b.ID, beads.UpdateOpts{Status: &status, Assignee: &assignee}); err != nil {
			t.Fatalf("Update(%s): %v", title, err)
		}
		return b
	}

	// "second-identity" work sits in the FIRST store by sort order.
	secondIdentityWork := mkActive(t, early, "work for the fallback identity", "session-name")
	// "first-identity" work sits in the LAST store by sort order.
	firstIdentityWork := mkActive(t, late, "work for the preferred identity", "session-id")

	srv := New(state)

	// Identity order is (session id, session name): the session id must win
	// wherever its bead lives.
	if got := srv.findActiveBeadForAssignees("", "session-id", "session-name"); got != firstIdentityWork.ID {
		t.Fatalf("active bead = %q, want %q (identity-major precedence: the first identity wins across stores)", got, firstIdentityWork.ID)
	}

	// Reversing the identity order must reverse the answer, which proves the
	// assertion above is about precedence and not about store order.
	if got := srv.findActiveBeadForAssignees("", "session-name", "session-id"); got != secondIdentityWork.ID {
		t.Fatalf("active bead = %q, want %q (identity order drives the result)", got, secondIdentityWork.ID)
	}

	// An explicit, known rig narrows the search to that store only.
	if got := srv.findActiveBeadForAssignees("zzz-rig", "session-name"); got != "" {
		t.Fatalf("active bead = %q, want \"\" (rig scope excludes the other store)", got)
	}

	// An unknown rig falls back to searching every store, matching the
	// behavior of the per-identity lookup this replaced.
	if got := srv.findActiveBeadForAssignees("no-such-rig", "session-name"); got != secondIdentityWork.ID {
		t.Fatalf("active bead = %q, want %q (unknown rig searches all stores)", got, secondIdentityWork.ID)
	}
}

// TestActiveBeadIndexPicksNewestPerIdentity pins the third precedence rule.
// The replaced query carried Limit: 1 with SortCreatedDesc, so when one
// identity owns several in_progress beads the newest is the answer.
func TestActiveBeadIndexPicksNewestPerIdentity(t *testing.T) {
	state := newFakeState(t)
	store := beads.NewMemStore()
	state.stores["myrig"] = store

	status := "in_progress"
	assignee := "worker"
	var newest beads.Bead
	for i := 0; i < 3; i++ {
		b, err := store.Create(beads.Bead{Title: fmt.Sprintf("work %d", i)})
		if err != nil {
			t.Fatalf("Create(%d): %v", i, err)
		}
		if err := store.Update(b.ID, beads.UpdateOpts{Status: &status, Assignee: &assignee}); err != nil {
			t.Fatalf("Update(%d): %v", i, err)
		}
		newest = b
	}

	srv := New(state)
	if got := srv.findActiveBeadForAssignees("myrig", assignee); got != newest.ID {
		t.Fatalf("active bead = %q, want %q (newest in_progress bead for the identity)", got, newest.ID)
	}
}

// TestAgentListActiveBeadStaysCached guards the freshness boundary while the
// fan-out is collapsed: the list view must not start reading live. A live read
// bypasses the read-model cache, so turning the list path live would restore
// the subprocess-per-read cost this change exists to remove.
func TestAgentListActiveBeadStaysCached(t *testing.T) {
	state := newFakeState(t)
	sessionName := "myrig--worker"
	if err := state.sp.Start(context.Background(), sessionName, runtime.Config{}); err != nil {
		t.Fatalf("Start(%s): %v", sessionName, err)
	}

	store := &liveFlagRecorder{Store: beads.NewMemStore()}
	state.stores["myrig"] = store

	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	req := httptest.NewRequest("GET", cityURL(state, "/agents"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.sawLive {
		t.Fatal("agent list active-bead lookup issued a live read; it must stay on the cached path")
	}
}

type liveFlagRecorder struct {
	beads.Store
	sawLive bool
}

func (s *liveFlagRecorder) List(query beads.ListQuery) ([]beads.Bead, error) {
	if query.Status == "in_progress" && query.Live {
		s.sawLive = true
	}
	return s.Store.List(query)
}

// TestSessionListActiveBeadReadsDoNotScaleWithSessionCount is the /sessions
// half of the same fan-out. The session list resolves FOUR identities per row
// (session id, session name, alias, template) against every store, so it was
// the more expensive of the two endpoints per row — and factory-monitor polls
// both on every tick, which is what kept the cost continuous (gc-68qc).
func TestSessionListActiveBeadReadsDoNotScaleWithSessionCount(t *testing.T) {
	const sessionCount = 4

	fs := newSessionFakeState(t)
	store := &inProgressReadCounter{Store: beads.NewMemStore()}
	fs.stores["myrig"] = store
	srv := New(fs)
	h := newTestCityHandlerWith(t, fs, srv)

	for i := 0; i < sessionCount; i++ {
		createTestSession(t, fs.cityBeadStore, fs.sp, fmt.Sprintf("Session %d", i))
	}

	req := httptest.NewRequest("GET", cityURL(fs, "/sessions"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Items []sessionResponse `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != sessionCount {
		t.Fatalf("Total = %d, want %d — the fan-out assertion below is only meaningful with every session listed", resp.Total, sessionCount)
	}

	if store.reads > 1 {
		t.Fatalf("in_progress store reads = %d for %d sessions, want 1 (one read per store, reused across the page)", store.reads, sessionCount)
	}
}
