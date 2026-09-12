package beads

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	beadslib "github.com/steveyegge/beads"
	beadsissueops "github.com/steveyegge/beads/issueops"
)

func testNativeDeterministicStore() *NativeDoltStore {
	storage := newNativeDoltDeterministicTestStorage()
	return newNativeDoltStoreWithStorageAndPrefix(storage, "native-test", "prod")
}

type nativeDoltDeterministicCapabilityStorage struct {
	*nativeDoltDeterministicTestStorage
	creator beadsissueops.BatchCreator
	err     error
	calls   int
}

func (s *nativeDoltDeterministicCapabilityStorage) BatchCreator() (beadsissueops.BatchCreator, error) {
	s.calls++
	return s.creator, s.err
}

func TestNativeDoltStoreDeterministicCapabilityRequiresPrefixOpenStorageAndCreateOnlyRole(t *testing.T) {
	validStorage := newNativeDoltDeterministicTestStorage()
	validStore := newNativeDoltStoreWithStorageAndPrefix(validStorage, "native-test", "prod")
	if !SupportsDeterministicCreate(validStore) {
		t.Fatal("ready native store hid deterministic creation")
	}

	missingPrefix := newNativeDoltStoreWithStorage(validStorage, "native-test")
	if SupportsDeterministicCreate(missingPrefix) {
		t.Fatal("prefixless native store advertised deterministic creation")
	}
	if SupportsDeterministicCreate(newNativeDoltStoreWithStorageAndPrefix(nil, "native-test", "prod")) {
		t.Fatal("native store without storage advertised deterministic creation")
	}
	closed := newNativeDoltStoreWithStorageAndPrefix(validStorage, "native-test", "prod")
	closed.closed = true
	if SupportsDeterministicCreate(closed) {
		t.Fatal("closed native store advertised deterministic creation")
	}

	roleErrStorage := &nativeDoltDeterministicCapabilityStorage{
		nativeDoltDeterministicTestStorage: newNativeDoltDeterministicTestStorage(),
		err:                                errors.New("create-only role unavailable"),
	}
	if SupportsDeterministicCreate(newNativeDoltStoreWithStorageAndPrefix(roleErrStorage, "native-test", "prod")) {
		t.Fatal("native store with role error advertised deterministic creation")
	}
	if roleErrStorage.calls != 1 {
		t.Fatalf("role error preflight calls = %d, want 1", roleErrStorage.calls)
	}

	nilRoleStorage := &nativeDoltDeterministicCapabilityStorage{
		nativeDoltDeterministicTestStorage: newNativeDoltDeterministicTestStorage(),
	}
	if SupportsDeterministicCreate(newNativeDoltStoreWithStorageAndPrefix(nilRoleStorage, "native-test", "prod")) {
		t.Fatal("native store with nil create-only role advertised deterministic creation")
	}
	if nilRoleStorage.calls != 1 {
		t.Fatalf("nil role preflight calls = %d, want 1", nilRoleStorage.calls)
	}
}

// nativeDoltDeterministicTestStorage exposes the same strict create-only role
// used by real Dolt. It deliberately does not emulate that role with
// RunInTransaction/CreateIssue: the latter is the import/upsert surface and
// cannot prove deterministic creation under a same-ID race.
type nativeDoltDeterministicTestStorage struct {
	*nativeDoltMemStorage
	createEntered chan<- struct{}
	createRelease <-chan struct{}
}

func newNativeDoltDeterministicTestStorage() *nativeDoltDeterministicTestStorage {
	storage := newNativeDoltMemStorage()
	storage.store.HonorExplicitIDs = true
	return &nativeDoltDeterministicTestStorage{nativeDoltMemStorage: storage}
}

func (s *nativeDoltDeterministicTestStorage) BatchCreator() (beadsissueops.BatchCreator, error) {
	return nativeDoltDeterministicTestBatchCreator{storage: s}, nil
}

type nativeDoltDeterministicTestBatchCreator struct {
	storage *nativeDoltDeterministicTestStorage
}

func (c nativeDoltDeterministicTestBatchCreator) CreateBatch(_ context.Context, req beadsissueops.CreateBatchRequest) (beadsissueops.CreateBatchResult, error) {
	if c.storage.createEntered != nil {
		c.storage.createEntered <- struct{}{}
	}
	if c.storage.createRelease != nil {
		<-c.storage.createRelease
	}
	if len(req.Items) != 1 || req.Items[0].Issue == nil {
		return beadsissueops.CreateBatchResult{}, errors.New("test batch creator requires exactly one issue")
	}
	requested, err := beadFromNativeIssue(req.Items[0].Issue)
	if err != nil {
		return beadsissueops.CreateBatchResult{}, err
	}
	created, err := c.storage.store.Create(requested)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate id") {
			return beadsissueops.CreateBatchResult{}, errors.Join(beadsissueops.ErrAlreadyExists, err)
		}
		return beadsissueops.CreateBatchResult{}, err
	}
	issue, err := nativeIssueFromBead(created)
	if err != nil {
		return beadsissueops.CreateBatchResult{}, err
	}
	return beadsissueops.CreateBatchResult{Issues: []*beadslib.Issue{issue}}, nil
}

func TestNativeDoltStoreCreateDeterministicUsesOwnedPrefixAndAdoptsExactRetry(t *testing.T) {
	store := testNativeDeterministicStore()
	request := deterministicCreateTestBead("recovery")
	created, inserted, err := store.CreateDeterministic("recovery-attempt-1", request)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || !strings.HasPrefix(created.ID, "prod-") {
		t.Fatalf("first create = %#v, inserted=%v", created, inserted)
	}
	adopted, inserted, err := store.CreateDeterministic("recovery-attempt-1", request)
	if err != nil || inserted || adopted.ID != created.ID {
		t.Fatalf("retry = %#v, inserted=%v, err=%v; want adoption of %q", adopted, inserted, err, created.ID)
	}
}

func TestNativeDoltStoreCreateDeterministicAdoptsDefaultPriorityRetry(t *testing.T) {
	store := testNativeDeterministicStore()
	request := Bead{Title: "recovery", Type: "task", Labels: []string{"gc:recovery-work"}, Metadata: map[string]string{"attempt": "one"}}
	created, inserted, err := store.CreateDeterministic("recovery-attempt-default-priority", request)
	if err != nil || !inserted {
		t.Fatalf("first create = %#v, inserted=%v, err=%v", created, inserted, err)
	}
	adopted, inserted, err := store.CreateDeterministic("recovery-attempt-default-priority", request)
	if err != nil || inserted || adopted.ID != created.ID {
		t.Fatalf("default-priority retry = %#v, inserted=%v, err=%v", adopted, inserted, err)
	}
}

func TestNativeDoltStoreCreateDeterministicRejectsConflictingTuple(t *testing.T) {
	store := testNativeDeterministicStore()
	request := deterministicCreateTestBead("recovery")
	if _, _, err := store.CreateDeterministic("recovery-attempt-1", request); err != nil {
		t.Fatal(err)
	}
	request.Title = "different"
	if _, _, err := store.CreateDeterministic("recovery-attempt-1", request); !errors.Is(err, ErrDeterministicCreateConflict) {
		t.Fatalf("conflicting retry error = %v", err)
	}
}

// TestNativeDoltStoreCreateDeterministicConcurrentConflictUsesCreateOnlyRole
// is causal rather than scheduler-probabilistic: the test role holds the first
// create until both callers have entered it. Exactly one strict insert can then
// win, and the loser must adopt the winner after ErrAlreadyExists. Calling the
// legacy transaction CreateIssue path is an immediate failure because that
// path has import/upsert semantics in native Dolt.
func TestNativeDoltStoreCreateDeterministicConcurrentConflictUsesCreateOnlyRole(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	storage := newNativeDoltDeterministicTestStorage()
	storage.createEntered = entered
	storage.createRelease = release
	trap := &nativeDoltDeterministicUpsertTrap{
		nativeDoltDeterministicTestStorage: storage,
		called:                             make(chan struct{}, 2),
	}
	store := newNativeDoltStoreWithStorageAndPrefix(trap, "native-test", "prod")

	type result struct {
		bead     Bead
		inserted bool
		err      error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			bead, inserted, err := store.CreateDeterministic("same-create", deterministicCreateTestBead("recovery"))
			results <- result{bead: bead, inserted: inserted, err: err}
		}()
	}

	for range 2 {
		select {
		case <-entered:
		case <-trap.called:
			close(release)
			t.Fatal("CreateDeterministic called RunInTransaction/CreateIssue instead of the atomic create-only role")
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("concurrent callers did not both enter the create-only role")
		}
	}
	close(release)

	insertions := 0
	var first Bead
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatalf("CreateDeterministic: %v", got.err)
		}
		if got.inserted {
			insertions++
		}
		if first.ID == "" {
			first = got.bead
		} else if got.bead.ID != first.ID || !got.bead.CreatedAt.Equal(first.CreatedAt) {
			t.Fatalf("concurrent results diverged: first=%+v second=%+v", first, got.bead)
		}
	}
	if insertions != 1 {
		t.Fatalf("reported %d insertions, want exactly one", insertions)
	}
	all, err := storage.store.List(ListQuery{AllowScan: true, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != first.ID {
		t.Fatalf("stored rows = %+v, want only %q", all, first.ID)
	}
}

type nativeDoltDeterministicUpsertTrap struct {
	*nativeDoltDeterministicTestStorage
	called chan struct{}
}

func (s *nativeDoltDeterministicUpsertTrap) RunInTransaction(_ context.Context, _ string, _ func(beadslib.Transaction) error) error {
	s.called <- struct{}{}
	return errors.New("upsert transaction path is forbidden")
}
