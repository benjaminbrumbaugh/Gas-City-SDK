package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/launchorigin"
)

// clearActorRoutes blanks every actor route key so a test starts from a
// launch with no trustworthy actor.
func clearActorRoutes(t *testing.T) {
	t.Helper()
	for _, key := range launchorigin.RouteKeys() {
		t.Setenv(key, "")
	}
}

// An ordinary `gc sling` captures its launching actor with no flag and no
// registration step: the CLI's own dependency wiring does it.
func TestPopulateSlingDepsCapturesLaunchOrigin(t *testing.T) {
	clearActorRoutes(t)
	t.Setenv(launchorigin.RouteKeys()[0], "actor-token")

	var deps slingDeps
	populateSlingDepsCallbacks(&deps)

	if deps.LaunchOrigin != "actor-token" {
		t.Fatalf("LaunchOrigin = %q, want %q", deps.LaunchOrigin, "actor-token")
	}
}

// With no actor route the CLI must supply no origin, so the sling keeps its
// legacy behavior instead of inventing an identity.
func TestPopulateSlingDepsLeavesLaunchOriginEmptyWithoutARoute(t *testing.T) {
	clearActorRoutes(t)

	var deps slingDeps
	populateSlingDepsCallbacks(&deps)

	if deps.LaunchOrigin != "" {
		t.Fatalf("LaunchOrigin = %q, want empty when no actor route exists", deps.LaunchOrigin)
	}
}

// A caller that already resolved the launching actor keeps its value; capture
// fills a gap, it does not overwrite a decision already made.
func TestPopulateSlingDepsKeepsAnExplicitLaunchOrigin(t *testing.T) {
	clearActorRoutes(t)
	t.Setenv(launchorigin.RouteKeys()[0], "env-token")

	deps := slingDeps{LaunchOrigin: "explicit-token"}
	populateSlingDepsCallbacks(&deps)

	if deps.LaunchOrigin != "explicit-token" {
		t.Fatalf("LaunchOrigin = %q, want the caller's explicit value", deps.LaunchOrigin)
	}
}

// The CLI must read the same route keys the capture package owns, so the
// launching identity matches the identity that claims work.
func TestCLICaptureUsesTheOwnedRouteKeys(t *testing.T) {
	for _, key := range launchorigin.RouteKeys() {
		t.Run(key, func(t *testing.T) {
			clearActorRoutes(t)
			t.Setenv(key, "actor-"+key)

			var deps slingDeps
			populateSlingDepsCallbacks(&deps)

			if deps.LaunchOrigin != "actor-"+key {
				t.Fatalf("LaunchOrigin = %q, want %q", deps.LaunchOrigin, "actor-"+key)
			}
		})
	}
}
