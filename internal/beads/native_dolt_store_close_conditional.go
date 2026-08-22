package beads

import (
	"context"
	"encoding/json"
	"fmt"

	beadslib "github.com/steveyegge/beads"
)

// NativeDoltStore exposes only the narrow revision-fenced terminal close. This
// does not promote the store to ConditionalWriter: RowVersion is sufficient for
// this single transaction, not for every mutation promised by that interface.
var _ ConditionalCloseWriter = (*NativeDoltStore)(nil)

// CloseWithMetadataIfMatch stamps terminal metadata and closes the issue in one
// native Dolt transaction iff the opaque RowVersion still equals
// expectedRevision and the issue remains open and unblocked.
func (s *NativeDoltStore) CloseWithMetadataIfMatch(id string, expectedRevision int64, metadata map[string]string) error {
	storage, release, err := s.acquireStorage()
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()

	return storage.RunInTransaction(ctx, fmt.Sprintf("gc: conditional close bead %s", id), func(tx beadslib.Transaction) error {
		issue, err := tx.GetIssue(ctx, id)
		if err != nil {
			return nativeStoreError(id, err)
		}
		if issue == nil {
			return fmt.Errorf("conditional close %s: %w", id, ErrNotFound)
		}
		if issue.RowVersion != expectedRevision {
			return &PreconditionFailedError{ID: id, Expected: expectedRevision, Current: issue.RowVersion}
		}
		if issue.Status == beadslib.StatusClosed {
			return ErrNotClosableForConditionalClose
		}
		blocked, _, err := tx.IsBlocked(ctx, id)
		if err != nil {
			return nativeStoreError(id, err)
		}
		if blocked {
			return ErrNotClosableForConditionalClose
		}

		closeReason := ""
		if len(metadata) > 0 {
			rawMetadata, err := metadataRawValuesFromNative(issue.Metadata)
			if err != nil {
				return fmt.Errorf("parsing metadata for conditional close %q: %w", id, err)
			}
			if rawMetadata == nil {
				rawMetadata = make(map[string]json.RawMessage, len(metadata))
			}
			for key, value := range metadata {
				raw, err := json.Marshal(value)
				if err != nil {
					return fmt.Errorf("marshaling conditional close metadata %q: %w", key, err)
				}
				rawMetadata[key] = raw
			}
			rawBytes, err := json.Marshal(rawMetadata)
			if err != nil {
				return fmt.Errorf("marshaling conditional close metadata: %w", err)
			}
			raw := json.RawMessage(rawBytes)
			if err := tx.UpdateIssue(ctx, id, map[string]interface{}{"metadata": raw}, s.actor); err != nil {
				return nativeStoreError(id, err)
			}
			closeReason = metadata["close_reason"]
		}
		if closeReason == "" {
			parsed, err := metadataMapFromNative(issue.Metadata)
			if err != nil {
				return fmt.Errorf("parsing close reason for bead %q: %w", id, err)
			}
			closeReason = parsed["close_reason"]
		}
		if err := tx.CloseIssue(ctx, id, closeReason, s.actor, ""); err != nil {
			return nativeStoreError(id, err)
		}
		return nil
	})
}
