package beads

import (
	"context"
	"fmt"
	"time"

	beadslib "github.com/steveyegge/beads"
)

// NativeDoltStore exposes the narrow admission fence backed by beads' public
// transaction primitives. It intentionally remains distinct from the broader
// ConditionalWriter contract: this backend's RowVersion is suitable for this
// ready-and-update operation, but does not yet cover every mutation that
// ConditionalWriter promises to fence.
var _ ReadyConditionalWriter = (*NativeDoltStore)(nil)

// UpdateIfReadyAndMatch applies a field/metadata update only while the native
// issue is still ready and its RowVersion equals expectedRevision. The pinned
// beads API does not expose the newer RequireReady update option, so this uses
// its public transaction surface to evaluate every predicate and apply the
// update in one transaction. It never falls back to read-then-write admission.
func (s *NativeDoltStore) UpdateIfReadyAndMatch(id string, expectedRevision int64, opts UpdateOpts) error {
	if isEmptyUpdateOpts(opts) {
		return fmt.Errorf("conditional ready update %s: %w", id, ErrEmptyConditionalUpdate)
	}
	// Labels and parent links require separate backend operations today. Refuse
	// rather than pretending their mutation is covered by the single guarded
	// issue update. Routing admission uses metadata only.
	if len(opts.Labels) > 0 || len(opts.RemoveLabels) > 0 || opts.ParentID != nil {
		return fmt.Errorf("conditional ready update %s: labels and parent links are unsupported", id)
	}

	storage, release, err := s.acquireStorage()
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()

	return storage.RunInTransaction(ctx, fmt.Sprintf("gc: conditional ready update bead %s", id), func(tx beadslib.Transaction) error {
		issue, err := tx.GetIssue(ctx, id)
		if err != nil {
			return nativeStoreError(id, err)
		}
		if issue == nil {
			return fmt.Errorf("conditional ready update %s: %w", id, ErrNotFound)
		}
		if issue.RowVersion != expectedRevision {
			return &PreconditionFailedError{ID: id, Expected: expectedRevision, Current: issue.RowVersion}
		}
		if issue.Assignee != "" {
			return ErrNotReadyForConditionalUpdate
		}
		ready, err := nativeIssueReadyForConditionalUpdate(issue)
		if err != nil {
			return err
		}
		if !ready {
			return ErrNotReadyForConditionalUpdate
		}
		blocked, _, err := tx.IsBlocked(ctx, id)
		if err != nil {
			return nativeStoreError(id, err)
		}
		if blocked {
			return ErrNotReadyForConditionalUpdate
		}
		updates, err := s.nativeUpdates(ctx, tx, id, opts)
		if err != nil {
			return err
		}
		if len(updates) == 0 {
			return fmt.Errorf("conditional ready update %s: %w", id, ErrEmptyConditionalUpdate)
		}
		if err := tx.UpdateIssue(ctx, id, updates, s.actor); err != nil {
			return nativeStoreError(id, err)
		}
		return nil
	})
}

func nativeIssueReadyForConditionalUpdate(issue *beadslib.Issue) (bool, error) {
	if issue == nil {
		return false, nil
	}
	// An indefinite deferred status is not ready. A time-bound deferred issue
	// becomes eligible once its deadline passes, matching NativeDoltStore.Ready.
	if issue.Status == beadslib.StatusDeferred && issue.DeferUntil == nil {
		return false, nil
	}
	bead, err := beadFromNativeIssue(issue)
	if err != nil {
		return false, err
	}
	return IsReadyCandidateForTier(bead, time.Now().UTC(), TierIssues), nil
}
