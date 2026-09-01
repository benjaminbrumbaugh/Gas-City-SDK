package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// gc-p0hf4: excluding mail from the WORK release sweep (ra-59207) stopped gc
// from destroying a message's only route to an inbox, but left the message open
// forever, addressed to a closed session. `gc doctor session-model` then reports
// closed-bead-owner with no remedy and nothing ever clears it — two real
// peer-to-peer messages (gc-wisp-duwj, gc-wisp-89ag) sat that way for days, each
// carrying a finding its sender expected to be acted on.
//
// Mail needs the opposite move from work: re-ADDRESS, never unassign.

func newRetiringSessionBead(t *testing.T, store beads.Store, extra map[string]string) beads.Bead {
	t.Helper()
	md := map[string]string{"session_name": "worker-1", "state": "active"}
	for k, v := range extra {
		md[k] = v
	}
	b, err := store.Create(beads.Bead{
		Title:    "worker",
		Type:     sessionBeadType,
		Labels:   []string{sessionBeadLabel},
		Metadata: md,
	})
	if err != nil {
		t.Fatalf("create session bead: %v", err)
	}
	return b
}

func newMailTo(t *testing.T, store beads.Store, assignee, title string) beads.Bead {
	t.Helper()
	b, err := store.Create(beads.Bead{
		Title:       title,
		Description: "the finding the sender expected to be acted on",
		Type:        "message",
		Status:      "open",
		Assignee:    assignee,
	})
	if err != nil {
		t.Fatalf("create mail bead: %v", err)
	}
	return b
}

// The reported case: a session retires with a fallback route, so mail addressed
// to it is re-pointed at that mailbox instead of being orphaned.
func TestRetiredSessionMailIsReaddressedToTheFallbackRoute(t *testing.T) {
	store := beads.NewMemStore()
	sessionBead := newRetiringSessionBead(t, store, map[string]string{"agent_name": "rig/worker"})
	mailBead := newMailTo(t, store, sessionBead.ID, "gcd-ulh removes Models running UI card")

	var stderr bytes.Buffer
	unclaimWorkAssignedToRetiredSessionBead("", nil, store, nil, sessionBead, "rig/worker", &stderr)

	got, err := store.Get(mailBead.ID)
	if err != nil {
		t.Fatalf("get mail bead: %v", err)
	}
	if got.Assignee != "rig/worker" {
		t.Fatalf("mail Assignee = %q, want the fallback route %q (an orphaned message is what this fixes)", got.Assignee, "rig/worker")
	}
	if got.Status != "open" {
		t.Fatalf("mail Status = %q, want it still deliverable (%q)", got.Status, "open")
	}
	if got.Description == "" || got.Title == "" {
		t.Fatalf("rehoming must not touch message content: title=%q description=%q", got.Title, got.Description)
	}
	if got.Metadata[mailRehomeMetadataFrom] != sessionBead.ID {
		t.Fatalf("%s = %q, want the retired session id %q", mailRehomeMetadataFrom, got.Metadata[mailRehomeMetadataFrom], sessionBead.ID)
	}
	if got.Metadata[mailRehomeMetadataRoute] != "rig/worker" {
		t.Fatalf("%s = %q, want the route it moved to", mailRehomeMetadataRoute, got.Metadata[mailRehomeMetadataRoute])
	}
	if strings.TrimSpace(got.Metadata[mailRehomeMetadataAt]) == "" {
		t.Fatalf("%s not stamped; provenance must say when", mailRehomeMetadataAt)
	}
}

// Acceptance clause 3: with no successor route there is nowhere to deliver, so
// the message is closed with a reason that names the session. An explicit,
// searchable record beats an open bead nobody can act on — and the content
// survives on the closed bead, so nothing is deleted unreviewed.
func TestRetiredSessionMailWithNoRouteIsClosedWithAReason(t *testing.T) {
	store := beads.NewMemStore()
	sessionBead := newRetiringSessionBead(t, store, nil)
	mailBead := newMailTo(t, store, sessionBead.ID, "JSONL incident correlation")

	var stderr bytes.Buffer
	unclaimWorkAssignedToRetiredSessionBead("", nil, store, nil, sessionBead, "", &stderr)

	got, err := store.Get(mailBead.ID)
	if err != nil {
		t.Fatalf("get mail bead: %v", err)
	}
	if got.Status == "open" {
		t.Fatalf("mail left open with no route; that is the permanent doctor finding this fixes")
	}
	reason := got.Metadata["close_reason"]
	if !strings.Contains(reason, sessionBead.ID) {
		t.Fatalf("close_reason = %q, want it to name the retired session %s", reason, sessionBead.ID)
	}
	if got.Title == "" || got.Description == "" {
		t.Fatalf("closing must preserve content for review: title=%q description=%q", got.Title, got.Description)
	}
	if got.Metadata[mailRehomeMetadataFrom] != sessionBead.ID {
		t.Fatalf("%s = %q, want provenance stamped before the close", mailRehomeMetadataFrom, got.Metadata[mailRehomeMetadataFrom])
	}
}

// When a successor session exists the mail follows the work to it, not to the
// pool route: the successor is a live mailbox and the pool route is a detour.
func TestRetiredSessionMailFollowsTheSuccessorSession(t *testing.T) {
	store := beads.NewMemStore()
	retiring := newRetiringSessionBead(t, store, map[string]string{"agent_name": "rig/worker"})
	successor := newRetiringSessionBead(t, store, map[string]string{"session_name": "worker-2"})
	mailBead := newMailTo(t, store, retiring.ID, "peer finding")

	var stderr bytes.Buffer
	reassignWorkAssignedToRetiredSessionBead("", nil, store, nil, retiring, successor.ID, &stderr)

	got, err := store.Get(mailBead.ID)
	if err != nil {
		t.Fatalf("get mail bead: %v", err)
	}
	if got.Assignee != successor.ID {
		t.Fatalf("mail Assignee = %q, want the successor session %q", got.Assignee, successor.ID)
	}
	if got.Status != "open" {
		t.Fatalf("mail Status = %q, want it still deliverable", got.Status)
	}
}

// The ra-59207 invariant must survive this change: mail is never released like
// work. Assert both halves in one retirement — the work bead is detached, the
// mail bead is re-addressed and never left with an empty assignee.
func TestRetirementReleasesWorkAndReaddressesMail(t *testing.T) {
	store := beads.NewMemStore()
	sessionBead := newRetiringSessionBead(t, store, map[string]string{"agent_name": "rig/worker"})
	mailBead := newMailTo(t, store, sessionBead.ID, "peer finding")
	workBead, err := store.Create(beads.Bead{
		Title:    "real work",
		Type:     "task",
		Status:   "in_progress",
		Assignee: sessionBead.ID,
	})
	if err != nil {
		t.Fatalf("create work bead: %v", err)
	}

	var stderr bytes.Buffer
	unclaimWorkAssignedToRetiredSessionBead("", nil, store, nil, sessionBead, "rig/worker", &stderr)

	gotWork, err := store.Get(workBead.ID)
	if err != nil {
		t.Fatalf("get work bead: %v", err)
	}
	if gotWork.Assignee != "" {
		t.Fatalf("work Assignee = %q, want cleared: the mail sweep must not disable the work release", gotWork.Assignee)
	}
	gotMail, err := store.Get(mailBead.ID)
	if err != nil {
		t.Fatalf("get mail bead: %v", err)
	}
	if gotMail.Assignee == "" {
		t.Fatalf("mail Assignee was cleared; that is ra-59207 reintroduced — a mail bead's assignee is its only route to an inbox")
	}
	if gotMail.Assignee != "rig/worker" {
		t.Fatalf("mail Assignee = %q, want the fallback route", gotMail.Assignee)
	}
}

// Only message beads are rehomed. A task assigned to the same session must not
// acquire rehome provenance, or the sweep would be quietly rewriting work.
func TestMailRehomeTouchesOnlyMessageBeads(t *testing.T) {
	store := beads.NewMemStore()
	sessionBead := newRetiringSessionBead(t, store, nil)
	workBead, err := store.Create(beads.Bead{
		Title:    "real work",
		Type:     "task",
		Status:   "open",
		Assignee: sessionBead.ID,
	})
	if err != nil {
		t.Fatalf("create work bead: %v", err)
	}

	var stderr bytes.Buffer
	res := rehomeMailAssignedToRetiredSession("", nil, store, nil,
		sessionAssignmentIdentifiers(sessionBead), sessionBead.ID, "rig/worker", time.Now(), &stderr)

	if res.Rehomed != 0 || res.Expired != 0 {
		t.Fatalf("rehome touched a non-message bead: %+v", res)
	}
	got, err := store.Get(workBead.ID)
	if err != nil {
		t.Fatalf("get work bead: %v", err)
	}
	if got.Metadata[mailRehomeMetadataFrom] != "" {
		t.Fatalf("work bead acquired rehome provenance %q", got.Metadata[mailRehomeMetadataFrom])
	}
	if got.Status != "open" || got.Assignee != sessionBead.ID {
		t.Fatalf("work bead was altered: status=%q assignee=%q", got.Status, got.Assignee)
	}
}
