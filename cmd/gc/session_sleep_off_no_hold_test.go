package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	runtimepkg "github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// TestSleepAfterIdleOffNeverWritesHeldUntil pins the invariant gc-yrc was filed
// against: sleep_after_idle=off must stay a flag and must never be encoded as a
// far-future held_until.
//
// The incident report inferred that the legacy_off policy path encoded "off" as
// held_until = now + ~100 years with sleep_intent=user-hold. It does not — the
// century sentinel is indefiniteHoldDuration, written only by gc session
// suspend, and the sleep policy path writes a fixed seven-key metadata batch
// that has no timestamp in it at all. That is the property worth holding still:
// a timestamp used as a boolean becomes a real deadline the moment another code
// path reads it literally, and here that deadline outlives the runtime and
// strands whatever the session was holding.
//
// The test walks the whole idle-sleep lifecycle for an off/legacy_off policy —
// resolve, persist the policy metadata, mark the idle-stop intent, complete the
// sleep — and asserts held_until is still absent at every step.
func TestSleepAfterIdleOffNeverWritesHeldUntil(t *testing.T) {
	const sessionID = "gc-wisp-off"

	// No agent matches the template, which is the configuration that resolves
	// through legacy_off — the exact sleep_policy_source the incident recorded.
	cfg := &config.City{}
	resolved := config.ResolveSessionSleepPolicy(cfg, nil)
	if resolved.Value != config.SessionSleepOff {
		t.Fatalf("ResolveSessionSleepPolicy value = %q, want %q", resolved.Value, config.SessionSleepOff)
	}
	if resolved.Source != config.SessionSleepSourceLegacyOff {
		t.Fatalf("ResolveSessionSleepPolicy source = %q, want %q", resolved.Source, config.SessionSleepSourceLegacyOff)
	}

	store := beads.NewMemStoreFrom(0, []beads.Bead{
		holdStallSessionBead(sessionID, "city-worker", map[string]string{
			"state":            "active",
			"session_template": "city-worker",
		}),
	}, nil)
	sessFront := sessionFrontDoor(store)

	heldUntil := func(t *testing.T, step string) string {
		t.Helper()
		bead, err := store.Get(sessionID)
		if err != nil {
			t.Fatalf("%s: reading session bead: %v", step, err)
		}
		return bead.Metadata["held_until"]
	}
	requireNoHold := func(t *testing.T, step string) {
		t.Helper()
		if got := heldUntil(t, step); got != "" {
			t.Fatalf("%s: held_until = %q, want empty — the idle-sleep policy path must never write a hold", step, got)
		}
	}

	requireNoHold(t, "before any policy write")

	info, err := sessFront.Get(sessionID)
	if err != nil {
		t.Fatalf("projecting session info: %v", err)
	}

	policy := resolvedSessionSleepPolicy{
		Class:      resolved.Class,
		Requested:  resolved.Value,
		Effective:  resolved.Value,
		Source:     resolved.Source,
		Capability: runtimepkg.SessionSleepCapabilityFull,
	}
	policy.Fingerprint = sessionSleepFingerprint(nil, policy)
	if policy.enabled() {
		t.Fatalf("policy.enabled() = true for effective=%q, want false", policy.Effective)
	}

	info = persistSleepPolicyMetadataInfo(info, sessFront, policy, false)
	requireNoHold(t, "after persisting the off/legacy_off policy metadata")
	if got := info.EffectiveSleepAfterIdle; got != config.SessionSleepOff {
		t.Fatalf("effective_sleep_after_idle = %q, want %q", got, config.SessionSleepOff)
	}
	if got := info.SleepPolicySource; got != config.SessionSleepSourceLegacyOff {
		t.Fatalf("sleep_policy_source = %q, want %q", got, config.SessionSleepSourceLegacyOff)
	}

	// The idle-stop marker is the only thing the idle path writes ahead of the
	// drain, and it is an intent, not a deadline.
	patch := markIdleSleepPendingInfo(info, sessFront)
	if len(patch) != 1 || patch["sleep_intent"] != "idle-stop-pending" {
		t.Fatalf("markIdleSleepPendingInfo patch = %#v, want exactly {sleep_intent: idle-stop-pending}", patch)
	}
	requireNoHold(t, "after marking the idle-stop intent")

	info, err = sessFront.Get(sessionID)
	if err != nil {
		t.Fatalf("re-projecting session info: %v", err)
	}
	clk := &clock.Fake{Time: time.Date(2026, 8, 25, 11, 10, 51, 0, time.UTC)}
	if !recoverPendingIdleSleepInfo(info, sessFront, false, clk) {
		t.Fatal("recoverPendingIdleSleepInfo = false, want the pending idle sleep to complete")
	}
	requireNoHold(t, "after completing the idle sleep")

	bead, err := store.Get(sessionID)
	if err != nil {
		t.Fatalf("reading session bead: %v", err)
	}
	if got := bead.Metadata["state"]; got != string(sessionpkg.StateAsleep) {
		t.Fatalf("state = %q, want %q", got, sessionpkg.StateAsleep)
	}
	// SleepPatch clears sleep_intent, so a session that slept via the idle path
	// carries neither a hold nor a hold intent — it is reclaimable by demand.
	if got := bead.Metadata["sleep_intent"]; got != "" {
		t.Fatalf("sleep_intent = %q, want empty after the idle sleep completes", got)
	}
}
