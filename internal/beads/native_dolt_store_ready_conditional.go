package beads

import (
	"context"
	"errors"
	"fmt"

	beadslib "github.com/steveyegge/beads"
)

// NativeDoltStore exposes the narrow admission fence backed by beads'
// UpdateIssueChecked primitive. It intentionally remains distinct from the
// broader ConditionalWriter contract: this backend's RowVersion is suitable for
// this ready-and-update operation, but does not yet cover every mutation that
// ConditionalWriter promises to fence.
var _ ReadyConditionalWriter = (*NativeDoltStore)(nil)

// UpdateIfReadyAndMatch applies a field/metadata update only while the native
// issue is still ready and its RowVersion equals expectedRevision. Beads checks
// both predicates inside its mutation transaction; this method never falls back
// to a read-then-write admission path.
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

	updates, err := s.nativeUpdates(ctx, storage, id, opts)
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return fmt.Errorf("conditional ready update %s: %w", id, ErrEmptyConditionalUpdate)
	}
	if err := storage.UpdateIssueChecked(ctx, id, updates, s.actor, beadslib.UpdateIssueOptions{
		ExpectedVersion: &expectedRevision,
		RequireReady:    true,
	}); err != nil {
		switch {
		case errors.Is(err, beadslib.ErrNotReady):
			return ErrNotReadyForConditionalUpdate
		case errors.Is(err, beadslib.ErrVersionMismatch):
			current := int64(0)
			if issue, getErr := storage.GetIssue(ctx, id); getErr == nil && issue != nil {
				current = issue.RowVersion
			}
			return &PreconditionFailedError{ID: id, Expected: expectedRevision, Current: current}
		default:
			return nativeStoreError(id, err)
		}
	}
	return nil
}
