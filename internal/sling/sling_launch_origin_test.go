package sling

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
	"github.com/gastownhall/gascity/internal/runtime"
)

// slingWithOrigin routes one plain bead and returns the auto-convoy it minted.
func slingWithOrigin(t *testing.T, origin string) beads.Bead {
	t.Helper()
	runner := newFakeRunner()
	cfg := &config.City{Workspace: config.Workspace{Name: "test"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	deps := testDeps(cfg, runtime.NewFake(), runner.run)
	deps.LaunchOrigin = origin

	b, err := deps.Store.Create(beads.Bead{Title: "work", Type: "task"})
	if err != nil {
		t.Fatalf("seeding work bead: %v", err)
	}
	result, err := DoSling(SlingOpts{Target: a, BeadOrFormula: b.ID}, deps, deps.Store)
	if err != nil {
		t.Fatalf("DoSling: %v", err)
	}
	if result.ConvoyID == "" {
		t.Fatalf("expected an auto-convoy; MetadataErrors = %#v", result.MetadataErrors)
	}
	convoy, err := deps.Store.Get(result.ConvoyID)
	if err != nil {
		t.Fatalf("reading auto-convoy %s: %v", result.ConvoyID, err)
	}
	return convoy
}

// An ordinary sling captures its launching actor with no flag, no registration,
// and no instruction to the agent.
func TestAutoConvoyCapturesLaunchOriginByDefault(t *testing.T) {
	convoy := slingWithOrigin(t, "actor-token")

	if got := convoycore.GetConvoyFields(convoy).LaunchOrigin; got != "actor-token" {
		t.Fatalf("LaunchOrigin = %q, want %q", got, "actor-token")
	}
}

// Without a trustworthy actor route the convoy must be byte-identical to the
// pre-capture shape: the key is absent, not empty.
func TestAutoConvoyOmitsLaunchOriginWithoutATrustworthyRoute(t *testing.T) {
	convoy := slingWithOrigin(t, "")

	if _, ok := convoy.Metadata[convoycore.LaunchOriginMetadataKey]; ok {
		t.Fatalf("%s present on a launch with no actor route: %#v",
			convoycore.LaunchOriginMetadataKey, convoy.Metadata)
	}
}

// A caller that supplies an origin the callback wire would reject must not get
// it stamped: fan-out would emit records that fail Event.Validate.
func TestAutoConvoyRefusesAnOriginTheCallbackWireWouldReject(t *testing.T) {
	cases := map[string]string{
		"over the opaque bound": strings.Repeat("x", convoycallback.MaxOpaqueIdentityLength+1),
		"control character":     "actor\x00token",
		"whitespace only":       "   ",
	}
	for name, origin := range cases {
		t.Run(name, func(t *testing.T) {
			convoy := slingWithOrigin(t, origin)
			if got, ok := convoy.Metadata[convoycore.LaunchOriginMetadataKey]; ok {
				t.Fatalf("%s = %q, want the unusable origin refused",
					convoycore.LaunchOriginMetadataKey, got)
			}
		})
	}
}

// A stamped origin must survive onto a callback record unchanged.
func TestStampedLaunchOriginValidatesOnTheCallbackWire(t *testing.T) {
	convoy := slingWithOrigin(t, "  actor-token  ")

	origin := convoycore.GetConvoyFields(convoy).LaunchOrigin
	if origin != "actor-token" {
		t.Fatalf("LaunchOrigin = %q, want the trimmed token", origin)
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
		t.Fatalf("a stamped origin must validate on the callback wire: %v", err)
	}
}
