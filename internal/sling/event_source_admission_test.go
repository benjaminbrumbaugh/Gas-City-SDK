package sling

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestDoSlingRejectsEventBeadForPoolBeforeMoleculeCreation(t *testing.T) {
	runner := newFakeRunner()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	target := config.Agent{
		Name:                "polecat",
		MaxActiveSessions:   intPtr(3),
		DefaultSlingFormula: stringPtr("mol-polecat-work"),
	}
	deps := testDeps(cfg, runtime.NewFake(), runner.run)
	event, err := deps.Store.Create(beads.Bead{Title: "witness record", Type: "event", Status: "open"})
	if err != nil {
		t.Fatalf("create event bead: %v", err)
	}
	before, err := deps.Store.ListOpen()
	if err != nil {
		t.Fatalf("list before sling: %v", err)
	}

	_, err = DoSling(SlingOpts{Target: target, BeadOrFormula: event.ID}, deps, nil)
	if err == nil {
		t.Fatal("DoSling(event -> pool) error = nil, want admission refusal")
	}
	if !strings.Contains(err.Error(), "event") || !strings.Contains(err.Error(), "polecat pool") {
		t.Fatalf("DoSling(event -> pool) error = %q, want event/polecat admission diagnostic", err)
	}
	after, err := deps.Store.ListOpen()
	if err != nil {
		t.Fatalf("list after sling: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("store bead count after refusal = %d, want unchanged %d; beads=%+v", len(after), len(before), after)
	}
	got, err := deps.Store.Get(event.ID)
	if err != nil {
		t.Fatalf("get event after refusal: %v", err)
	}
	if got.Metadata["gc.routed_to"] != "" || got.Metadata["molecule_id"] != "" {
		t.Fatalf("event metadata after refusal = %#v, want no routing or molecule metadata", got.Metadata)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls after refusal = %v, want none", runner.calls)
	}
}

func TestDoSlingAllowsEventBeadToSingleSessionAgent(t *testing.T) {
	runner := newFakeRunner()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	target := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	deps := testDeps(cfg, runtime.NewFake(), runner.run)
	event, err := deps.Store.Create(beads.Bead{Title: "operator record", Type: "event", Status: "open"})
	if err != nil {
		t.Fatalf("create event bead: %v", err)
	}

	if _, err := DoSling(SlingOpts{Target: target, BeadOrFormula: event.ID, NoFormula: true}, deps, nil); err != nil {
		t.Fatalf("DoSling(event -> single session) = %v, want compatibility success", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %v, want one direct route", runner.calls)
	}
}

func TestDoSlingBatchRejectsEventChildForPool(t *testing.T) {
	runner := newFakeRunner()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	target := config.Agent{Name: "polecat", MaxActiveSessions: intPtr(3)}
	deps := testDeps(cfg, runtime.NewFake(), runner.run)
	convoy, err := deps.Store.Create(beads.Bead{Title: "dispatch batch", Type: "convoy", Status: "open"})
	if err != nil {
		t.Fatalf("create convoy: %v", err)
	}
	event, err := deps.Store.Create(beads.Bead{Title: "event child", Type: "event", Status: "open", ParentID: convoy.ID})
	if err != nil {
		t.Fatalf("create event child: %v", err)
	}

	_, err = DoSlingBatch(SlingOpts{Target: target, BeadOrFormula: convoy.ID, NoFormula: true}, deps, deps.Store)
	if err == nil {
		t.Fatal("DoSlingBatch(event child -> pool) error = nil, want admission refusal")
	}
	if !strings.Contains(err.Error(), "event") || !strings.Contains(err.Error(), "polecat pool") {
		t.Fatalf("DoSlingBatch error = %q, want event/polecat admission diagnostic", err)
	}
	got, err := deps.Store.Get(event.ID)
	if err != nil {
		t.Fatalf("get event child after refusal: %v", err)
	}
	if got.Metadata["gc.routed_to"] != "" || got.Metadata["molecule_id"] != "" {
		t.Fatalf("event child metadata after refusal = %#v, want no routing or molecule metadata", got.Metadata)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls after refusal = %v, want none", runner.calls)
	}
}
