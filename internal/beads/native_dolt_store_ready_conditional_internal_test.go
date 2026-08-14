package beads

import (
	"testing"

	"github.com/gastownhall/gascity/internal/rollout/gate"
)

// TestNativeDoltStoreDeclaresReadyConditionalWriter pins the narrow capability
// needed by routing admission: an atomic readiness-and-row-version update. It
// remains deliberately distinct from ConditionalWriter because the upstream
// row version has a narrower mutation coverage than that broader contract.
func TestNativeDoltStoreDeclaresReadyConditionalWriter(t *testing.T) {
	store := newNativeDoltStoreForTest(newNativeDoltMemStorage())
	store.stampConditionalWritesMode(gate.Require, false)
	if _, ok := ReadyConditionalWriterFor(store); !ok {
		t.Fatal("NativeDoltStore does not resolve ReadyConditionalWriter")
	}
	if _, ok := ConditionalWriterFor(store); ok {
		t.Fatal("NativeDoltStore unexpectedly resolves broader ConditionalWriter")
	}
}
