package session

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestMarkedRoutingSessionCannotStartWithoutAuthorization(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	b, err := store.Create(beads.Bead{Type: BeadType, Labels: []string{LabelSession, "gc:routing-execution:v3"}, Metadata: map[string]string{"state": "suspended", "session_name": "routing-marked", "command": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManagerWithOptions(store, sp)
	if err := m.StartRuntimeOnly(context.Background(), b.ID, "true", runtime.Config{}); err == nil {
		t.Fatal("marked worker started without authority")
	}
	if sp.IsRunning("routing-marked") {
		t.Fatal("unauthorized runtime started")
	}
}
