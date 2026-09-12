package beads

import (
	"context"
	"errors"
	"fmt"

	beadslib "github.com/steveyegge/beads"
	beadsissueops "github.com/steveyegge/beads/issueops"
)

var _ DeterministicCreator = (*NativeDoltStore)(nil)

// SupportsDeterministicCreate reports whether the native store is currently
// open, owns an ID namespace, and can acquire upstream's strict create-only
// role. Acquiring the role does not mutate storage.
func (s *NativeDoltStore) SupportsDeterministicCreate() bool {
	if s == nil || s.idPrefix == "" {
		return false
	}
	storage, release, err := s.acquireStorage()
	if err != nil {
		return false
	}
	defer release()
	creator, err := storage.BatchCreator()
	return err == nil && creator != nil
}

// CreateDeterministic creates through native Beads' strict create-only role.
// That role serializes same-ID writers and returns ErrAlreadyExists to every
// loser without updating the winner. A loser then reads and adopts the durable
// row only when its deterministic tuple is identical.
func (s *NativeDoltStore) CreateDeterministic(key string, b Bead) (Bead, bool, error) {
	if s == nil {
		return Bead{}, false, fmt.Errorf("deterministic create: nil NativeDoltStore: %w", ErrDeterministicCreateUnsupported)
	}
	want, err := deterministicCreateRequest(s.idPrefix, key, b)
	if err != nil {
		return Bead{}, false, err
	}
	issue, err := nativeIssueFromBead(want)
	if err != nil {
		return Bead{}, false, err
	}
	storage, release, err := s.acquireStorage()
	if err != nil {
		return Bead{}, false, err
	}
	defer release()

	creator, err := storage.BatchCreator()
	if err != nil {
		return Bead{}, false, fmt.Errorf("deterministic create: native create-only role unavailable: %w: %w", err, ErrDeterministicCreateUnsupported)
	}
	if creator == nil {
		return Bead{}, false, fmt.Errorf("deterministic create: native create-only role is nil: %w", ErrDeterministicCreateUnsupported)
	}

	var createdIssue *beadslib.Issue
	err = retryOnNativeDoltSerializationConflict(func() error {
		createdIssue = nil
		ctx, cancel := nativeDoltOperationContext(context.TODO())
		defer cancel()
		result, createErr := creator.CreateBatch(ctx, beadsissueops.CreateBatchRequest{
			Actor: s.actor,
			Items: []beadsissueops.BatchCreateItem{{Issue: issue}},
			// The deterministic ID is already in this store's owned namespace.
			// ForceIDPrefix prevents upstream configuration aliases from rejecting
			// that exact ID; it does not enable upsert semantics.
			ForceIDPrefix: true,
		})
		if createErr != nil {
			return createErr
		}
		if len(result.Issues) != 1 || result.Issues[0] == nil {
			return fmt.Errorf("deterministic create bead %q: native create-only role returned %d issues", want.ID, len(result.Issues))
		}
		createdIssue = result.Issues[0]
		return nil
	})
	if err == nil {
		created, convertErr := beadFromNativeIssue(createdIssue)
		if convertErr != nil {
			return Bead{}, false, convertErr
		}
		if !sameDeterministicCreateTuple(created, want) {
			return Bead{}, false, fmt.Errorf("deterministic create bead %q: native create-only role returned a different tuple", want.ID)
		}
		return created, true, nil
	}
	if !errors.Is(err, beadsissueops.ErrAlreadyExists) {
		return Bead{}, false, nativeStoreError(want.ID, err)
	}

	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()
	existing, readErr := storage.GetIssue(ctx, want.ID)
	if readErr != nil {
		return Bead{}, false, nativeStoreError(want.ID, readErr)
	}
	if existing == nil {
		return Bead{}, false, fmt.Errorf("deterministic create bead %q: create-only conflict but row is absent: %w", want.ID, ErrNotFound)
	}
	adopted, err := beadFromNativeIssue(existing)
	if err != nil {
		return Bead{}, false, err
	}
	if !sameDeterministicCreateTuple(adopted, want) {
		return Bead{}, false, deterministicCreateConflict(want.ID)
	}
	return adopted, false, nil
}
