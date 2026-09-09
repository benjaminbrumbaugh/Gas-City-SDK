package beadmail

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/mail"
)

// newSubscriptionShapedBead creates the durable-record shape that adopts
// Type="message" purely for the beads.readyExcludeTypes exclusion: an owner in
// From, a class label, record metadata, and NO recipient. It mirrors the
// callback-subscription bead of the harness-callbacks epic (sdk-e7v) so these
// tests pin the namespace rule against a real producer rather than a
// hypothetical one.
func newSubscriptionShapedBead(t *testing.T, store beads.Store) beads.Bead {
	t.Helper()
	b, err := store.Create(beads.Bead{
		Title:       "Convoy callback subscription",
		Description: "Durable callback subscription",
		Type:        "message",
		From:        "harness-owner",
		Labels:      []string{"gc:convoy-callback-subscription"},
		Metadata: beads.StringMap{
			"convoy.callback_subscription.owner": "harness-owner",
			"convoy.callback_subscription.state": "active",
		},
	})
	if err != nil {
		t.Fatalf("creating subscription-shaped bead: %v", err)
	}
	if b.Assignee != "" {
		t.Fatalf("subscription-shaped bead must stay unaddressed, got assignee %q", b.Assignee)
	}
	return b
}

func TestUnaddressedMessageBeadIsNotMailInAllRoutesListings(t *testing.T) {
	store := beads.NewMemStore()
	p := New(store)

	sub := newSubscriptionShapedBead(t, store)
	addressed, err := p.Send("human", "mayor", "real mail", "body")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Empty recipients mean "all routes" — the unfiltered path where an
	// unaddressed record used to be rendered as mail.
	candidates, err := p.ArchiveCandidates(ArchiveFilter{})
	if err != nil {
		t.Fatalf("ArchiveCandidates: %v", err)
	}
	if hasMailMessageID(candidates, sub.ID) {
		t.Errorf("ArchiveCandidates returned unaddressed record %s as an archive candidate", sub.ID)
	}
	if !hasMailMessageID(candidates, addressed.ID) {
		t.Errorf("ArchiveCandidates dropped addressed mail %s", addressed.ID)
	}

	inbox, err := p.InboxRecipients(nil)
	if err != nil {
		t.Fatalf("InboxRecipients: %v", err)
	}
	if hasMailMessageID(inbox, sub.ID) {
		t.Errorf("InboxRecipients(nil) returned unaddressed record %s as mail", sub.ID)
	}
	if !hasMailMessageID(inbox, addressed.ID) {
		t.Errorf("InboxRecipients(nil) dropped addressed mail %s", addressed.ID)
	}
}

func TestArchiveMatchingLeavesUnaddressedMessageBeadOpen(t *testing.T) {
	store := beads.NewMemStore()
	p := New(store)

	sub := newSubscriptionShapedBead(t, store)

	// The bulk sweep with no recipient filter is the destructive path: closing
	// a live subscription bead out of band corrupts the record's own
	// Status-backed lifecycle.
	if _, _, err := p.ArchiveMatching(ArchiveFilter{}); err != nil {
		t.Fatalf("ArchiveMatching: %v", err)
	}

	after, err := store.Get(sub.ID)
	if err != nil {
		t.Fatalf("Get after sweep: %v", err)
	}
	if after.Status != "open" {
		t.Fatalf("ArchiveMatching closed unaddressed record %s (status %q); it must stay open", sub.ID, after.Status)
	}
}

func TestCountRecipientsIgnoresUnaddressedMessageBead(t *testing.T) {
	store := beads.NewMemStore()
	p := New(store)

	newSubscriptionShapedBead(t, store)
	if _, err := p.Send("human", "mayor", "real mail", "body"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	total, unread, err := p.CountRecipients([]string{"mayor"})
	if err != nil {
		t.Fatalf("CountRecipients: %v", err)
	}
	if total != 1 || unread != 1 {
		t.Fatalf("CountRecipients = (%d, %d), want (1, 1)", total, unread)
	}
}

func TestDirectIDMailOperationsRejectUnaddressedMessageBead(t *testing.T) {
	store := beads.NewMemStore()
	p := New(store)
	sub := newSubscriptionShapedBead(t, store)

	if _, err := p.Get(sub.ID); !errors.Is(err, mail.ErrNotFound) {
		t.Errorf("Get(%s) error = %v, want mail.ErrNotFound", sub.ID, err)
	}
	if _, err := p.Read(sub.ID); !errors.Is(err, mail.ErrNotFound) {
		t.Errorf("Read(%s) error = %v, want mail.ErrNotFound", sub.ID, err)
	}
	if err := p.MarkRead(sub.ID); !errors.Is(err, mail.ErrNotFound) {
		t.Errorf("MarkRead(%s) error = %v, want mail.ErrNotFound", sub.ID, err)
	}
	if err := p.MarkUnread(sub.ID); !errors.Is(err, mail.ErrNotFound) {
		t.Errorf("MarkUnread(%s) error = %v, want mail.ErrNotFound", sub.ID, err)
	}
	if _, err := p.Thread(sub.ID); !errors.Is(err, mail.ErrNotFound) {
		t.Errorf("Thread(%s) error = %v, want mail.ErrNotFound", sub.ID, err)
	}
	if _, err := p.Reply(sub.ID, "mayor", "re", "body"); !errors.Is(err, mail.ErrNotFound) {
		t.Errorf("Reply(%s) error = %v, want mail.ErrNotFound", sub.ID, err)
	}
	if err := p.Archive(sub.ID); !errors.Is(err, mail.ErrNotFound) {
		t.Errorf("Archive(%s) error = %v, want mail.ErrNotFound", sub.ID, err)
	}

	after, err := store.Get(sub.ID)
	if err != nil {
		t.Fatalf("Get after direct-ID attempts: %v", err)
	}
	if after.Status != "open" {
		t.Fatalf("direct-ID mail operations closed record %s (status %q)", sub.ID, after.Status)
	}
	if hasLabel(after.Labels, "read") {
		t.Fatalf("direct-ID mail operations mutated record %s labels: %v", sub.ID, after.Labels)
	}
}

func TestUnaddressedMessageBeadStaysReadyExcluded(t *testing.T) {
	store := beads.NewMemStore()
	sub := newSubscriptionShapedBead(t, store)

	// The whole reason the record keeps Type="message" is the Ready()
	// exclusion it inherits. Narrowing the mail namespace must not cost it.
	if !beads.IsReadyExcludedType(sub.Type) {
		t.Fatalf("bead type %q is no longer Ready-excluded", sub.Type)
	}
	if !IsMessageBead(sub) {
		t.Fatalf("IsMessageBead(%s) = false; the messaging-class predicate must stay a bare type check", sub.ID)
	}
}

// TestSendRejectsBlankRecipient pins the premise the namespace rule rests on:
// every mail bead carries a non-blank Assignee by construction. Send and
// SendHandoff previously rejected only the empty string, so a whitespace-only
// recipient minted a bead that isUnaddressedMessageBead then classifies as
// non-mail — silently swallowing the message from every view that would
// otherwise show it. Reject it at the door instead.
func TestSendRejectsBlankRecipient(t *testing.T) {
	for _, to := range []string{"", " ", "\t", "\n", "   "} {
		store := beads.NewMemStore()
		p := New(store)

		if _, err := p.Send("human", to, "subject", "body"); err == nil {
			t.Errorf("Send(to=%q) succeeded; want a recipient-required error", to)
		}
		if _, err := p.SendHandoff(mail.HandoffIntent{From: "human", To: to, Subject: "s", Body: "b"}); err == nil {
			t.Errorf("SendHandoff(To=%q) succeeded; want a recipient-required error", to)
		}

		// Nothing was persisted, so no hidden bead is left behind.
		all, err := store.List(beads.ListQuery{Type: "message", AllowScan: true})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 0 {
			t.Errorf("to=%q created %d message bead(s); want none", to, len(all))
		}
	}
}

// TestSendKeepsPaddedRecipientAddressed guards the other side of the trim: a
// padded-but-real route is still mail, so trimming must reject only the blank
// case, never narrow a legitimate recipient out of the namespace.
func TestSendKeepsPaddedRecipientAddressed(t *testing.T) {
	store := beads.NewMemStore()
	p := New(store)

	sent, err := p.Send("human", " mayor ", "subject", "body")
	if err != nil {
		t.Fatalf("Send with padded recipient: %v", err)
	}
	got, err := p.Get(sent.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", sent.ID, err)
	}
	if got.ID != sent.ID {
		t.Fatalf("Get returned %s, want %s", got.ID, sent.ID)
	}
}
