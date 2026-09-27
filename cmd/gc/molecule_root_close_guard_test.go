package main

import (
	"bytes"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/storebinding"
)

func TestMoleculeRootCloseRefusesNonTerminalHandoffStep(t *testing.T) {
	store := beads.NewMemStore()
	root, err := store.Create(beads.Bead{Title: "mol-polecat-work", Type: "molecule"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(beads.Bead{
		Title:    "submit-and-exit",
		Type:     "step",
		ParentID: root.ID,
		Assignee: "furiosa",
	}); err != nil {
		t.Fatal(err)
	}
	graph, err := storebinding.NewBeadsGraphStore(store)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := doBdByIDClose(graph, bdByIDOp{Verb: bdByIDClose, ID: root.ID}, "test", &stdout, &stderr)
	if code == 0 {
		t.Fatalf("close unexpectedly succeeded: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	closed, err := store.Get(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	step, err := store.Children(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "open" || len(step) != 1 || step[0].Status != "open" {
		t.Fatalf("refused close changed root/step state = root %q, steps %#v", closed.Status, step)
	}
}

func TestMoleculeRootUpdateClosedRefusesNonTerminalHandoffStep(t *testing.T) {
	store := beads.NewMemStore()
	root, err := store.Create(beads.Bead{Title: "mol-polecat-work", Type: "molecule"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(beads.Bead{Title: "workflow-finalize", Type: "step", ParentID: root.ID}); err != nil {
		t.Fatal(err)
	}
	graph, err := storebinding.NewBeadsGraphStore(store)
	if err != nil {
		t.Fatal(err)
	}
	closed := "closed"
	var stdout, stderr bytes.Buffer
	code := doBdByIDUpdate(graph, bdByIDOp{
		Verb: bdByIDUpdate,
		ID:   root.ID,
		Update: beads.UpdateOpts{
			Status: &closed,
		},
	}, "test", &stdout, &stderr)
	if code == 0 {
		t.Fatalf("closed update unexpectedly succeeded: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	got, err := store.Get(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "open" {
		t.Fatalf("root status = %q, want open after refused update; stderr=%q", got.Status, stderr.String())
	}
}

func TestMoleculeRootCloseAllowsAllTerminalSteps(t *testing.T) {
	store := beads.NewMemStore()
	root, err := store.Create(beads.Bead{Title: "mol-polecat-work", Type: "molecule"})
	if err != nil {
		t.Fatal(err)
	}
	step, err := store.Create(beads.Bead{Title: "implement", Type: "step", ParentID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(step.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := storebinding.NewBeadsGraphStore(store)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := doBdByIDClose(graph, bdByIDOp{Verb: bdByIDClose, ID: root.ID}, "test", &stdout, &stderr); code != 0 {
		t.Fatalf("all-terminal close code = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	got, err := store.Get(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "closed" {
		t.Fatalf("root status = %q, want closed", got.Status)
	}
}

func TestMoleculeRootCloseBatchAllowsStepsClosedWithRoot(t *testing.T) {
	store := beads.NewMemStore()
	root, err := store.Create(beads.Bead{Title: "mol-polecat-work", Type: "molecule"})
	if err != nil {
		t.Fatal(err)
	}
	step, err := store.Create(beads.Bead{Title: "submit-and-exit", Type: "step", ParentID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := storebinding.NewBeadsGraphStore(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.CloseAll([]string{root.ID, step.ID}, nil); err != nil {
		t.Fatalf("root-plus-step close batch failed: %v", err)
	}
	for _, id := range []string{root.ID, step.ID} {
		got, err := store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "closed" {
			t.Fatalf("%s status = %q, want closed", id, got.Status)
		}
	}
}

func TestGcBdCloseGuardRefusesNonTerminalMoleculeRootBeforePassthrough(t *testing.T) {
	store := beads.NewMemStore()
	root, err := store.Create(beads.Bead{Title: "mol-polecat-work", Type: "molecule"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(beads.Bead{Title: "submit-and-exit", Type: "step", ParentID: root.ID}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if !moleculeCloseMutationRefusal([]string{"close", root.ID}, store, map[string]beads.Bead{root.ID: root}, &stderr) {
		t.Fatalf("gc bd close guard accepted a non-terminal root: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	got, err := store.Get(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "open" {
		t.Fatalf("root status = %q, want open after refused passthrough", got.Status)
	}
}

func TestMoleculeCloseMutationUsesLastStatusValue(t *testing.T) {
	if moleculeCloseMutation([]string{"update", "gc-1", "--status=closed", "--status=open"}) {
		t.Fatal("guard treated an earlier closed status as the effective update")
	}
	if !moleculeCloseMutation([]string{"update", "gc-1", "--status=open", "--status", "closed"}) {
		t.Fatal("guard missed the effective closed status")
	}
}
