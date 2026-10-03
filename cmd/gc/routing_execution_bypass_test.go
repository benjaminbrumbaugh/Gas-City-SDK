package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// A persisted authorization must not become legacy merely because its trigger
// was lost during reconstruction.
func TestRoutingExecutionMissingTriggerDoesNotBecomeLegacy(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "missing-trigger")
	info := sessionpkg.Info{ID: "bound-session", Labels: []string{"gc:routing-execution:v3"}}
	if err := fixture.cr.checkRoutingExecutionLaunch(fixture.payload.Target, fixture.payload.Rig, info, runtime.Config{}); err == nil {
		t.Fatal("durably marked session without trigger was admitted as legacy")
	}
}
