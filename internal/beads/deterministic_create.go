package beads

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrDeterministicCreateUnsupported reports that a store cannot atomically
	// create-or-adopt a request under a deterministic store-owned ID. Callers must
	// not fall back to Store.Create: doing so would reopen the duplicate window
	// this capability exists to close.
	ErrDeterministicCreateUnsupported = errors.New("deterministic create unsupported")
	// ErrInvalidDeterministicCreate reports an input that cannot be represented
	// faithfully by the narrow deterministic-create contract.
	ErrInvalidDeterministicCreate = errors.New("invalid deterministic create")
	// ErrDeterministicCreateConflict reports that the key's deterministic ID is
	// already occupied by a different create tuple.
	ErrDeterministicCreateConflict = errors.New("deterministic create conflict")
)

// DeterministicCreator is the narrow atomic create-or-adopt capability used by
// reconcilers that may retry after losing the result of a successful create.
//
// key is an opaque, stable operation identity. A store derives an ID in its own
// namespace from key, atomically creates b under that ID, and returns the
// persisted row. Repeating the exact create tuple adopts that row. inserted is
// true only for the call that committed the row. Reusing key for a different
// tuple returns ErrDeterministicCreateConflict and must not mutate the existing
// row. Implementations reject ambiguous or non-creatable Bead fields instead
// of silently dropping them.
//
// This is deliberately separate from ForeignIDCreator. Deterministic creates
// must stay in the destination store's own ID namespace and never use bd's
// migration-only --force path.
type DeterministicCreator interface {
	CreateDeterministic(key string, b Bead) (created Bead, inserted bool, err error)
}

// DeterministicCreateCapability lets a delegating wrapper report whether its
// effective backing store can actually honor deterministic creation.
type DeterministicCreateCapability interface {
	SupportsDeterministicCreate() bool
}

var (
	_ DeterministicCreator = (*MemStore)(nil)
	_ DeterministicCreator = (*FileStore)(nil)
	_ DeterministicCreator = (*BdStore)(nil)
	_ DeterministicCreator = (*CachingStore)(nil)
)

// CreateDeterministically invokes the optional deterministic-create capability.
// It fails closed when the store does not expose it; it never falls back to
// Store.Create.
func CreateDeterministically(store Store, key string, b Bead) (Bead, bool, error) {
	creator, ok := store.(DeterministicCreator)
	if !ok || isNilStoreCapability(creator) {
		return Bead{}, false, fmt.Errorf("creating bead for key %q: %w", key, ErrDeterministicCreateUnsupported)
	}
	return creator.CreateDeterministic(key, b)
}

// SupportsDeterministicCreate resolves wrapper capability before a controller
// mutates source state. Unsupported destinations therefore fail closed without
// applying a recovery hold.
func SupportsDeterministicCreate(store Store) bool {
	if store == nil {
		return false
	}
	if capability, ok := store.(DeterministicCreateCapability); ok {
		return capability.SupportsDeterministicCreate()
	}
	creator, ok := store.(DeterministicCreator)
	return ok && !isNilStoreCapability(creator)
}

func isNilStoreCapability(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// deterministicCreateRequest validates and canonicalizes the subset of Bead
// that Store.Create can faithfully persist across MemStore, FileStore, and bd.
func deterministicCreateRequest(prefix, key string, b Bead) (Bead, error) {
	if key == "" || strings.TrimSpace(key) != key {
		return Bead{}, fmt.Errorf("%w: key must be non-empty and unpadded", ErrInvalidDeterministicCreate)
	}
	prefix = normalizeIDPrefix(prefix)
	if prefix == "" {
		return Bead{}, fmt.Errorf("%w: store has no owned ID prefix", ErrInvalidDeterministicCreate)
	}
	if b.ID != "" {
		return Bead{}, fmt.Errorf("%w: explicit bead ID is not allowed", ErrInvalidDeterministicCreate)
	}
	if b.Status != "" && b.Status != "open" {
		return Bead{}, fmt.Errorf("%w: create status %q is not open", ErrInvalidDeterministicCreate, b.Status)
	}
	if !b.CreatedAt.IsZero() || !b.UpdatedAt.IsZero() || b.Revision != 0 || b.ClaimFence != 0 || b.IsBlocked != nil {
		return Bead{}, fmt.Errorf("%w: store-owned timestamps, revisions, fences, and projections must be unset", ErrInvalidDeterministicCreate)
	}
	if b.Ref != "" || b.ParentID != "" || len(b.Needs) != 0 || len(b.Dependencies) != 0 {
		return Bead{}, fmt.Errorf("%w: ref, parent, and dependency projections are outside the standalone create contract", ErrInvalidDeterministicCreate)
	}
	if labels := normalizedStrings(b.Labels); !slices.Equal(labels, b.Labels) {
		return Bead{}, fmt.Errorf("%w: labels must be sorted, unique, non-empty, and unpadded", ErrInvalidDeterministicCreate)
	}
	if b.DeferUntil != nil && !b.DeferUntil.Equal(b.DeferUntil.UTC().Truncate(time.Second)) {
		return Bead{}, fmt.Errorf("%w: defer time must have whole-second precision", ErrInvalidDeterministicCreate)
	}
	if b.Ephemeral && b.NoHistory {
		return Bead{}, fmt.Errorf("%w: ephemeral and no-history storage are mutually exclusive", ErrInvalidDeterministicCreate)
	}
	if b.From != "" && b.Metadata["from"] != "" && b.Metadata["from"] != b.From {
		return Bead{}, fmt.Errorf("%w: From conflicts with metadata[\"from\"]", ErrInvalidDeterministicCreate)
	}

	b = cloneBead(b)
	b.ID = deterministicBeadID(prefix, key)
	b.Status = "open"
	if b.Type == "" {
		b.Type = "task"
	}
	if b.Priority == nil {
		priority := 2 // bd create's production default
		b.Priority = &priority
	}
	if b.From == "" {
		b.From = b.Metadata["from"]
	}
	if b.From != "" {
		if b.Metadata == nil {
			b.Metadata = make(StringMap, 1)
		}
		b.Metadata["from"] = b.From
	}
	return b, nil
}

// deterministicBeadID mirrors bd's native root-ID shape: an owned prefix and an
// eight-character lowercase base36 hash suffix. Eight is bd's longest native
// progressive hash length and uses 40 digest bits, matching its ID generator.
func deterministicBeadID(prefix, key string) string {
	sum := sha256.Sum256([]byte(key))
	var value uint64
	for _, octet := range sum[:5] {
		value = value<<8 | uint64(octet)
	}
	suffix := strconv.FormatUint(value, 36)
	if len(suffix) < 8 {
		suffix = strings.Repeat("0", 8-len(suffix)) + suffix
	} else if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return normalizeIDPrefix(prefix) + "-" + suffix
}

func sameDeterministicCreateTuple(got, want Bead) bool {
	return got.ID == want.ID &&
		got.Title == want.Title &&
		got.Status == want.Status &&
		got.Type == want.Type &&
		deterministicPriority(got.Priority) == deterministicPriority(want.Priority) &&
		got.Assignee == want.Assignee &&
		got.From == want.From &&
		got.ParentID == want.ParentID &&
		got.Description == want.Description &&
		slices.Equal(got.Labels, want.Labels) &&
		maps.Equal(got.Metadata, want.Metadata) &&
		got.Ephemeral == want.Ephemeral &&
		got.NoHistory == want.NoHistory &&
		equalTimePtr(got.DeferUntil, want.DeferUntil)
}

func deterministicPriority(priority *int) int {
	if priority == nil {
		return 2
	}
	return *priority
}

func equalTimePtr(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func deterministicCreateConflict(id string) error {
	return fmt.Errorf("%w: bead %q is occupied by a different create tuple", ErrDeterministicCreateConflict, id)
}

// CreateDeterministic atomically creates or adopts a deterministic row in this
// in-memory store.
func (m *MemStore) CreateDeterministic(key string, b Bead) (Bead, bool, error) {
	return m.createDeterministic(key, b)
}

// createDeterministic also reports whether it inserted a row. FileStore uses
// that bit to avoid rewriting the file for an adopted retry.
func (m *MemStore) createDeterministic(key string, b Bead) (Bead, bool, error) {
	if m == nil {
		return Bead{}, false, fmt.Errorf("deterministic create: nil MemStore: %w", ErrDeterministicCreateUnsupported)
	}
	prefix := m.IDPrefix
	if prefix == "" {
		prefix = "gc"
	}
	want, err := deterministicCreateRequest(prefix, key, b)
	if err != nil {
		return Bead{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if index := m.indexOfLocked(want.ID); index >= 0 {
		existing := cloneBead(m.beads[index])
		if sameDeterministicCreateTuple(existing, want) {
			return existing, false, nil
		}
		return Bead{}, false, deterministicCreateConflict(want.ID)
	}
	created, err := m.createLocked(want, true)
	if err != nil {
		return Bead{}, false, err
	}
	return created, true, nil
}
