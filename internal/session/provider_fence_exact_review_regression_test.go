package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestProviderFenceStoreObservesCorruptNonOpenRowsButSkipsValidClosedHistory(t *testing.T) {
	mem := beads.NewMemStore()
	store := NewStore(beads.SessionStore{Store: mem})
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	validMetadata := map[string]string{
		"kind":                    providerFenceBeadKind,
		"provider_fence_identity": "account:closed-history",
		"fenced_until":            now.Add(time.Hour).Format(time.RFC3339),
		"observed_at":             now.Format(time.RFC3339),
	}
	closed, err := mem.Create(beads.Bead{
		Title: "valid closed history", Type: WaitBeadType,
		Labels: []string{ProviderFenceBeadLabel}, Metadata: validMetadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(closed.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ActiveProviderFences(now); err != nil || len(got) != 0 {
		t.Fatalf("valid closed history = %#v, %v; want inactive without error", got, err)
	}
	corrupt, err := mem.Create(beads.Bead{
		Title: "corrupt non-open fence", Type: WaitBeadType,
		Labels: []string{ProviderFenceBeadLabel}, Metadata: validMetadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	corruptStatus := "in_progress"
	if err := mem.Update(corrupt.ID, beads.UpdateOpts{Status: &corruptStatus}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveProviderFences(now); err == nil || !strings.Contains(err.Error(), corrupt.ID) {
		t.Fatalf("corrupt non-open fence error = %v, want operator-visible row %s", err, corrupt.ID)
	}
}

func TestProviderFenceLaunchClaimPreventsCrossManagerIdentityTheft(t *testing.T) {
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	mgrA := NewManagerWithOptions(mem, sp)
	mgrB := NewManagerWithOptions(mem, sp)
	info, err := mgrA.CreateSession(context.Background(), CreateOptions{
		BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	rowA, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := mgrA.prepareProviderFenceStart(info.ID, &rowA, "account:winner")
	if err != nil {
		t.Fatal(err)
	}
	if claim == "" {
		t.Fatal("winner did not acquire durable launch ownership")
	}
	defer mgrA.releaseProviderFenceLaunchClaim(info.ID, claim)

	rowB, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgrB.prepareProviderFenceStart(info.ID, &rowB, "account:winner"); err == nil {
		t.Fatal("second manager bypassed durable ownership for the same identity")
	}
	rowB, err = mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgrB.prepareProviderFenceStart(info.ID, &rowB, "account:loser"); err == nil {
		t.Fatal("losing manager stole in-flight provider account launch ownership")
	}
	current, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := current.Metadata["launch_provider_fence_identity"]; got != "account:winner" {
		t.Fatalf("launch identity = %q, want winner", got)
	}
}
