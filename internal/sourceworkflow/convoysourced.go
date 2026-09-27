package sourceworkflow

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
)

// ListConvoySourcedRoots returns graph.v2 workflow roots that belong to a
// source through an input convoy rather than gc.source_bead_id. Graph
// workflows deliberately clear gc.source_bead_id after launch, so the
// tracking edge is the durable recovery link.
//
// This is intentionally separate from ListLiveRoots. The singleton launch
// check asks the narrower source-metadata question; cleanup and recovery need
// the complete topology or they can report success while leaving a workflow
// alive.
func ListConvoySourcedRoots(store beads.Store, sourceBeadID string) ([]beads.Bead, error) {
	sourceBeadID = NormalizeSourceBeadID(sourceBeadID)
	if store == nil || sourceBeadID == "" {
		return nil, nil
	}

	convoyIDs := make(map[string]struct{})
	if source, err := store.Get(sourceBeadID); err == nil && source.Type == "convoy" {
		convoyIDs[source.ID] = struct{}{}
	} else if err != nil && !errors.Is(err, beads.ErrNotFound) {
		return nil, fmt.Errorf("getting source bead %s: %w", sourceBeadID, err)
	}
	convoys, err := convoycore.TrackingConvoysForItem(store, sourceBeadID)
	if err != nil {
		return nil, fmt.Errorf("listing tracking convoys for source bead %s: %w", sourceBeadID, err)
	}
	for _, convoy := range convoys {
		if id := strings.TrimSpace(convoy.ID); id != "" {
			convoyIDs[id] = struct{}{}
		}
	}

	seen := make(map[string]struct{})
	var roots []beads.Bead
	for convoyID := range convoyIDs {
		matches, err := store.ListByMetadata(
			map[string]string{beadmeta.InputConvoyIDMetadataKey: convoyID},
			0,
			beads.WithBothTiers,
		)
		if err != nil {
			return nil, fmt.Errorf("listing workflow roots for input convoy %s: %w", convoyID, err)
		}
		for _, root := range matches {
			if root.ID == "" || root.Status == "tombstone" || !IsWorkflowRoot(root) {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(root.Metadata[beadmeta.FormulaContractMetadataKey]), beadmeta.FormulaContractGraphV2) {
				continue
			}
			if _, ok := seen[root.ID]; ok {
				continue
			}
			seen[root.ID] = struct{}{}
			roots = append(roots, root)
		}
	}
	slices.SortFunc(roots, func(a, b beads.Bead) int { return strings.Compare(a.ID, b.ID) })
	return roots, nil
}

// ListRootsIncludingConvoySourced returns all source-linked workflow roots,
// including closed roots. Callers must inspect the returned subtree before
// acting: a closed root with open descendants is recoverable, while a fully
// closed subtree is already clean.
func ListRootsIncludingConvoySourced(store beads.Store, sourceBeadID, sourceStoreRef, rootStoreRef string) ([]beads.Bead, error) {
	roots, err := listRoots(store, sourceBeadID, sourceStoreRef, rootStoreRef)
	if err != nil {
		return nil, err
	}
	convoyRoots, err := ListConvoySourcedRoots(store, sourceBeadID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(roots)+len(convoyRoots))
	for _, root := range roots {
		seen[root.ID] = struct{}{}
	}
	for _, root := range convoyRoots {
		if _, ok := seen[root.ID]; ok {
			continue
		}
		seen[root.ID] = struct{}{}
		roots = append(roots, root)
	}
	slices.SortFunc(roots, func(a, b beads.Bead) int { return strings.Compare(a.ID, b.ID) })
	return roots, nil
}

func listRoots(store beads.Store, sourceBeadID, sourceStoreRef, rootStoreRef string) ([]beads.Bead, error) {
	sourceBeadID = NormalizeSourceBeadID(sourceBeadID)
	if store == nil || sourceBeadID == "" {
		return nil, nil
	}
	roots, err := store.List(beads.ListQuery{
		Metadata:      map[string]string{beadmeta.SourceBeadIDMetadataKey: sourceBeadID},
		IncludeClosed: true,
		TierMode:      beads.TierBoth,
	})
	if err != nil {
		return nil, fmt.Errorf("listing source workflows for %s: %w", sourceBeadID, err)
	}
	roots = slices.DeleteFunc(roots, func(root beads.Bead) bool {
		return !IsWorkflowRoot(root) || !WorkflowMatchesSource(root, sourceBeadID, sourceStoreRef, rootStoreRef)
	})
	slices.SortFunc(roots, func(a, b beads.Bead) int { return strings.Compare(a.ID, b.ID) })
	return roots, nil
}
