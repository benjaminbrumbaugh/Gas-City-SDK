package main

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestBeadPolicyStoreForwardsDeterministicCreate(t *testing.T) {
	base := beads.NewMemStore()
	wrapped := wrapStoreWithBeadPolicies(base, nil)
	creator, ok := wrapped.(beads.DeterministicCreator)
	if !ok {
		t.Fatal("policy store does not expose DeterministicCreator")
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
	writer, err := beads.PreflightConditionalWriter(wrapped)
	if err != nil {
		t.Fatalf("policy store does not expose the recovery work fence: %v", err)
	}
	if err := writer.UpdateIfMatch(first.ID, first.Revision, beads.UpdateOpts{Metadata: map[string]string{"gc.recovery_adoption_fence": "test-adopter"}}); err != nil {
		t.Fatalf("reserve through policy store: %v", err)
	}
	reserved, err := wrapped.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CloseIfMatch(reserved.ID, reserved.Revision); err != nil {
		t.Fatalf("compensate through policy store: %v", err)
	}
	closed, err := wrapped.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" {
		t.Fatalf("conditional close through policy store left status %q", closed.Status)
	}
}

func TestBeadPolicyStoreDeterministicCreateFailsClosed(t *testing.T) {
	plain := struct{ beads.Store }{Store: beads.NewMemStore()}
	wrapped := wrapStoreWithBeadPolicies(plain, nil)
	creator, ok := wrapped.(beads.DeterministicCreator)
	if !ok {
		t.Fatal("policy store does not expose DeterministicCreator")
	}
	if _, _, err := creator.CreateDeterministic("recovery/incident/1", beads.Bead{Title: "recover"}); !errors.Is(err, beads.ErrDeterministicCreateUnsupported) {
		t.Fatalf("error = %v, want ErrDeterministicCreateUnsupported", err)
	}
	all, err := plain.List(beads.ListQuery{IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("unsupported fallback wrote %+v", all)
	}
}
