package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/launchorigin"
)

// createConvoyWithRoute runs `gc convoy create` with route set to the given
// value and returns the convoy bead it minted.
func createConvoyWithRoute(t *testing.T, route string) beads.Bead {
	t.Helper()
	clearActorRoutes(t)
	if route != "" {
		t.Setenv(launchorigin.RouteKeys()[0], route)
	}
	store := beads.NewMemStore()
	var stdout, stderr bytes.Buffer
	if code := doConvoyCreate(store, events.Discard, []string{"sprint-42"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doConvoyCreate = %d; stderr: %s", code, stderr.String())
	}
	convoys, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(convoys) != 1 {
		t.Fatalf("created %d convoys, want exactly 1", len(convoys))
	}
	return convoys[0]
}

// An explicitly created convoy carries its launch origin too. A convoy with no
// origin can never produce a valid callback record, because the contract
// requires launch_origin on convoy.created and convoy.closed alike.
func TestConvoyCreateCapturesLaunchOrigin(t *testing.T) {
	convoy := createConvoyWithRoute(t, "actor-token")

	origin := convoycore.GetConvoyFields(convoy).LaunchOrigin
	if origin != "actor-token" {
		t.Fatalf("LaunchOrigin = %q, want %q", origin, "actor-token")
	}
	event := convoycallback.Event{
		SchemaVersion: convoycallback.ContractVersion,
		EventID:       "event-1",
		Type:          convoycallback.EventConvoyCreated,
		ConvoyID:      convoy.ID,
		LaunchOrigin:  origin,
		CorrelationID: "correlation-1",
		OccurredAt:    convoy.CreatedAt,
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("a captured origin must validate on the callback wire: %v", err)
	}
}

// With no trustworthy actor route the convoy keeps its legacy shape.
func TestConvoyCreateOmitsLaunchOriginWithoutATrustworthyRoute(t *testing.T) {
	convoy := createConvoyWithRoute(t, "")

	if got, ok := convoy.Metadata[convoycore.LaunchOriginMetadataKey]; ok {
		t.Fatalf("%s = %q, want the key absent on a launch with no actor route",
			convoycore.LaunchOriginMetadataKey, got)
	}
}

// An unusable route value must be refused, not stamped: a truncated or
// control-bearing identity would attribute the launch to an actor that does
// not exist and would fail validation downstream.
func TestConvoyCreateRefusesAnUnusableRoute(t *testing.T) {
	convoy := createConvoyWithRoute(t, strings.Repeat("x", convoycallback.MaxOpaqueIdentityLength+1))

	if got, ok := convoy.Metadata[convoycore.LaunchOriginMetadataKey]; ok {
		t.Fatalf("%s = %q, want an unusable route refused", convoycore.LaunchOriginMetadataKey, got)
	}
}

// resolveLaunchOrigin is the shared seam both the sling and convoy-create
// paths use, so an origin is resolved the same way wherever work is launched.
func TestResolveLaunchOriginPrefersAUsableExplicitValue(t *testing.T) {
	clearActorRoutes(t)
	t.Setenv(launchorigin.RouteKeys()[0], "env-token")

	if got := resolveLaunchOrigin("explicit-token"); got != "explicit-token" {
		t.Fatalf("resolveLaunchOrigin = %q, want the explicit value", got)
	}
}

func TestResolveLaunchOriginFallsBackWhenTheExplicitValueIsUnusable(t *testing.T) {
	clearActorRoutes(t)
	t.Setenv(launchorigin.RouteKeys()[0], "env-token")

	if got := resolveLaunchOrigin("bad\x00token"); got != "env-token" {
		t.Fatalf("resolveLaunchOrigin = %q, want the captured route value", got)
	}
}
