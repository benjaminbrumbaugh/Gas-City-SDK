package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// gc-nb5h9: a bead's owner is recorded in several places that were written
// independently, so they routinely disagreed. The operator found gcd-8oq
// carrying session_id furiosa and session_name slit and reasonably read it as
// corruption. It was not corruption — it was two writers and no unit of update.
//
// It matters beyond tidiness: liveness and orphan checks key off one field or
// the other, so a witness resolving owners by gc.session_name and a controller
// resolving by gc.session_id disagree about who holds a bead. That is exactly
// the condition under which one of them recovers work from a LIVE agent, or
// fails to recover it from a dead one.

const (
	furiosaSession = "gc-k2gh9"
	furiosaName    = "Gas-City-Dashboard--gastown__furiosa"
	slitSession    = "gc-hywwv"
	slitName       = "Gas-City-Dashboard--gastown__slit"
)

func beadOwnedBy(sessionID, sessionName string) beads.Bead {
	meta := map[string]string{}
	if sessionID != "" {
		meta[beadmeta.SessionIDMetadataKey] = sessionID
	}
	if sessionName != "" {
		meta[beadmeta.SessionNameMetadataKey] = sessionName
	}
	return beads.Bead{ID: "gcd-8oq", Status: "in_progress", Metadata: meta}
}

// The reported shape. A takeover with no GC_SESSION_NAME in the environment
// used to stamp the id and leave the previous owner's name in place.
func TestClaimIdentityTakeoverNeverLeavesThePriorOwnersName(t *testing.T) {
	bead := beadOwnedBy(slitSession, slitName)

	patch := hookClaimSessionIdentityPatch(bead, furiosaSession, "")

	if got := patch[beadmeta.SessionIDMetadataKey]; got != furiosaSession {
		t.Fatalf("session_id = %q, want %q", got, furiosaSession)
	}
	name, ok := patch[beadmeta.SessionNameMetadataKey]
	if !ok {
		t.Fatalf("session_name was not written at all; the bead keeps %q while session_id becomes %q — "+
			"the exact split the operator read as corruption", slitName, furiosaSession)
	}
	if name != "" {
		t.Fatalf("session_name = %q, want it cleared", name)
	}
}

// The ordinary takeover: both keys move to the new owner together.
func TestClaimIdentityTakeoverMovesBothKeys(t *testing.T) {
	bead := beadOwnedBy(slitSession, slitName)

	patch := hookClaimSessionIdentityPatch(bead, furiosaSession, furiosaName)

	if patch[beadmeta.SessionIDMetadataKey] != furiosaSession ||
		patch[beadmeta.SessionNameMetadataKey] != furiosaName {
		t.Fatalf("patch = %v, want both keys naming furiosa", patch)
	}
}

// A re-stamp by the SAME session with no name to offer must not wipe a name
// that is already correct: it belongs to this session and is simply absent from
// this environment. Clearing it here would be a new way to lose the owner.
func TestClaimIdentityReStampKeepsACorrectName(t *testing.T) {
	bead := beadOwnedBy(furiosaSession, furiosaName)

	patch := hookClaimSessionIdentityPatch(bead, furiosaSession, "")

	if _, ok := patch[beadmeta.SessionNameMetadataKey]; ok {
		t.Fatalf("a same-session re-stamp cleared a correct name: %v", patch)
	}
	if _, ok := patch[beadmeta.SessionIDMetadataKey]; ok {
		t.Fatalf("a same-session re-stamp rewrote an unchanged id: %v", patch)
	}
}

// The same session filling in a name it did not have before is still allowed.
func TestClaimIdentityReStampFillsAMissingName(t *testing.T) {
	bead := beadOwnedBy(furiosaSession, "")

	patch := hookClaimSessionIdentityPatch(bead, furiosaSession, furiosaName)

	if patch[beadmeta.SessionNameMetadataKey] != furiosaName {
		t.Fatalf("patch = %v, want the missing name filled in", patch)
	}
}

// Nothing to do when the bead already names this claimant exactly. An empty
// patch is what keeps the claim path from issuing a write on every tick.
func TestClaimIdentityIsANoOpWhenAlreadyCurrent(t *testing.T) {
	bead := beadOwnedBy(furiosaSession, furiosaName)

	if patch := hookClaimSessionIdentityPatch(bead, furiosaSession, furiosaName); len(patch) != 0 {
		t.Fatalf("patch = %v, want empty", patch)
	}
}

// The camelCase variants are a READ FALLBACK — routingdecision resolves
// identity as firstWorkStateValue(snake, camel) — so a stale camel value is not
// inert. It becomes the answer the moment the snake key is cleared, which this
// code now deliberately does.
func TestClaimIdentityClearsAContradictingCamelVariant(t *testing.T) {
	bead := beadOwnedBy(slitSession, slitName)
	bead.Metadata[beadmeta.SessionIDCamelMetadataKey] = slitSession
	bead.Metadata[beadmeta.SessionNameCamelMetadataKey] = slitName

	patch := hookClaimSessionIdentityPatch(bead, furiosaSession, "")

	for _, key := range []string{beadmeta.SessionIDCamelMetadataKey, beadmeta.SessionNameCamelMetadataKey} {
		got, ok := patch[key]
		if !ok || got != "" {
			t.Errorf("%s = %q (present=%v), want cleared; a stale camel value is read as the owner "+
				"once the snake key is empty", key, got, ok)
		}
	}
}

// ... but an agreeing camel variant is left alone, so this does not churn a
// write on every claim of a bead some other writer populated correctly.
func TestClaimIdentityLeavesAnAgreeingCamelVariantAlone(t *testing.T) {
	bead := beadOwnedBy(furiosaSession, furiosaName)
	bead.Metadata[beadmeta.SessionIDCamelMetadataKey] = furiosaSession
	bead.Metadata[beadmeta.SessionNameCamelMetadataKey] = furiosaName

	if patch := hookClaimSessionIdentityPatch(bead, furiosaSession, furiosaName); len(patch) != 0 {
		t.Fatalf("patch = %v, want empty", patch)
	}
}

// An unowned bead being claimed for the first time gets both keys.
func TestClaimIdentityStampsAnUnownedBead(t *testing.T) {
	patch := hookClaimSessionIdentityPatch(beadOwnedBy("", ""), furiosaSession, furiosaName)

	if patch[beadmeta.SessionIDMetadataKey] != furiosaSession ||
		patch[beadmeta.SessionNameMetadataKey] != furiosaName {
		t.Fatalf("patch = %v, want both keys stamped", patch)
	}
}

// Whatever the inputs, the result must never describe two different sessions.
// This is the invariant the bead is actually about, asserted over the shapes
// that produced the incident.
func TestClaimIdentityNeverProducesASplitOwner(t *testing.T) {
	cases := []struct {
		name             string
		bead             beads.Bead
		wantID, wantName string
	}{
		{"takeover, name unavailable", beadOwnedBy(slitSession, slitName), furiosaSession, ""},
		{"takeover, name available", beadOwnedBy(slitSession, slitName), furiosaSession, furiosaName},
		{"stale name only", beadOwnedBy("", slitName), furiosaSession, ""},
		{"stale id only", beadOwnedBy(slitSession, ""), furiosaSession, furiosaName},
		{"unowned", beadOwnedBy("", ""), furiosaSession, furiosaName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patch := hookClaimSessionIdentityPatch(tc.bead, tc.wantID, tc.wantName)

			// Apply the patch and read the result the way a consumer would.
			final := map[string]string{}
			for k, v := range tc.bead.Metadata {
				final[k] = v
			}
			for k, v := range patch {
				final[k] = v
			}
			if got := final[beadmeta.SessionIDMetadataKey]; got != tc.wantID {
				t.Fatalf("session_id = %q, want %q", got, tc.wantID)
			}
			if got := final[beadmeta.SessionNameMetadataKey]; got != tc.wantName {
				t.Fatalf("session_name = %q, want %q — the bead names two different sessions", got, tc.wantName)
			}
			if got := final[beadmeta.SessionIDCamelMetadataKey]; got != "" && got != tc.wantID {
				t.Fatalf("gc.sessionId = %q contradicts the owner %q", got, tc.wantID)
			}
			if got := final[beadmeta.SessionNameCamelMetadataKey]; got != "" && got != tc.wantName {
				t.Fatalf("gc.sessionName = %q contradicts the owner %q", got, tc.wantName)
			}
		})
	}
}
