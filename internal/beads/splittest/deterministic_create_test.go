package splittest

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestStrictStoreReportsBackingDeterministicCapability(t *testing.T) {
	plain := struct{ beads.Store }{Store: beads.NewMemStore()}
	unsupported, err := newStrict(plain, "gc", BdSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if beads.SupportsDeterministicCreate(unsupported) {
		t.Fatal("strict wrapper advertised deterministic creation absent from its leaf")
	}

	supported, err := newStrict(beads.NewMemStore(), "gc", BdSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if !beads.SupportsDeterministicCreate(supported) {
		t.Fatal("strict wrapper hid its leaf's deterministic creation")
	}
}

func TestStrictStoreForwardsDeterministicCreate(t *testing.T) {
	work, _ := NewSplitStores(t)
	creator, ok := work.(beads.DeterministicCreator)
	if !ok {
		t.Fatal("strict store does not expose DeterministicCreator")
	}
	first, inserted, err := creator.CreateDeterministic("recovery/incident/1", beads.Bead{Title: "recover"})
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("first call reported adoption")
	}
	second, inserted, err := creator.CreateDeterministic("recovery/incident/1", beads.Bead{Title: "recover"})
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("duplicate call reported insertion")
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate IDs = %q and %q", first.ID, second.ID)
	}
	if _, _, err := creator.CreateDeterministic("recovery/incident/1", beads.Bead{Title: "different"}); !errors.Is(err, beads.ErrDeterministicCreateConflict) {
		t.Fatalf("conflict error = %v", err)
	}
}
