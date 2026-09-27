package molecule

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
)

// ValidateRootClosure refuses to close a live molecule while an executable
// descendant remains non-terminal. closingIDs contains descendants that will
// be closed in the same batch, so a legitimate root-plus-steps batch remains
// valid while a root-only close is refused before it can strand work.
//
// The guard is intentionally ownership-neutral. Molecule steps can move
// between sessions, but they remain descendants of the same root and must be
// accounted for regardless of which session last touched them.
func ValidateRootClosure(store beads.Store, rootID string, closingIDs map[string]struct{}) error {
	root, err := store.Get(rootID)
	if err != nil {
		return err
	}
	if root.Type != "molecule" || convoycore.IsTerminalStatus(root.Status) {
		return nil
	}
	descendants, err := ListSubtree(store, rootID)
	if err != nil {
		return fmt.Errorf("checking molecule root %q descendants: %w", rootID, err)
	}
	remaining := make([]string, 0)
	for _, bead := range descendants {
		if bead.ID == rootID || isGeneratedSpecSidecar(bead) || convoycore.IsTerminalStatus(bead.Status) {
			continue
		}
		if _, closesWithRoot := closingIDs[bead.ID]; closesWithRoot {
			continue
		}
		remaining = append(remaining, bead.ID)
	}
	if len(remaining) == 0 {
		return nil
	}
	sort.Strings(remaining)
	return fmt.Errorf("molecule root %q has non-terminal step(s): %s", rootID, strings.Join(remaining, ", "))
}

func isGeneratedSpecSidecar(bead beads.Bead) bool {
	return strings.EqualFold(strings.TrimSpace(bead.Metadata[beadmeta.KindMetadataKey]), beadmeta.KindSpec) ||
		strings.EqualFold(strings.TrimSpace(bead.Type), "spec")
}
