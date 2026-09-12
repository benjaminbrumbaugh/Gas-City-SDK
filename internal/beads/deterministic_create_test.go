package beads

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/fsys"
)

const deterministicCreateTestKey = "recovery/v1/incident-123/attempt/1"

func deterministicCreateTestBead(title string) Bead {
	priority := 1
	return Bead{
		Title:       title,
		Type:        "task",
		Priority:    &priority,
		Description: "recover the impaired session",
		Labels:      []string{"gc:recovery-responder"},
		Metadata: StringMap{
			"from":                    "recovery-responder",
			"gc.recovery.incident_id": "incident-123",
			"gc.recovery.attempt":     "1",
		},
	}
}

func assertDeterministicCreateContract(t *testing.T, creator DeterministicCreator, store Store) {
	t.Helper()
	want := deterministicCreateTestBead("recover session")
	first, inserted, err := creator.CreateDeterministic(deterministicCreateTestKey, want)
	if err != nil {
		t.Fatalf("first CreateDeterministic: %v", err)
	}
	if !inserted {
		t.Fatal("first CreateDeterministic reported adoption, want insertion")
	}
	if matched, err := regexp.MatchString(`^gc-[0-9a-z]{8}$`, first.ID); err != nil || !matched {
		t.Fatalf("created ID %q is not a native-shaped gc hash ID", first.ID)
	}
	if first.Title != want.Title || first.Type != want.Type || first.Description != want.Description ||
		first.Priority == nil || *first.Priority != *want.Priority || first.From != want.Metadata["from"] ||
		!slices.Equal(first.Labels, want.Labels) || !mapsEqual(first.Metadata, want.Metadata) {
		t.Fatalf("created bead does not preserve the requested tuple:\n got: %+v\nwant: %+v", first, want)
	}

	second, inserted, err := creator.CreateDeterministic(deterministicCreateTestKey, deterministicCreateTestBead("recover session"))
	if err != nil {
		t.Fatalf("duplicate CreateDeterministic: %v", err)
	}
	if inserted {
		t.Fatal("duplicate CreateDeterministic reported insertion, want adoption")
	}
	if second.ID != first.ID || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("duplicate was not adopted: first=%+v second=%+v", first, second)
	}

	if _, _, err := creator.CreateDeterministic(deterministicCreateTestKey, deterministicCreateTestBead("different request")); !errors.Is(err, ErrDeterministicCreateConflict) {
		t.Fatalf("conflicting CreateDeterministic error = %v, want ErrDeterministicCreateConflict", err)
	}
	got, err := store.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || got.Title != want.Title {
		t.Fatalf("conflict changed stored row: %+v", got)
	}
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func TestMemStoreDeterministicCreateContract(t *testing.T) {
	store := NewMemStore()
	assertDeterministicCreateContract(t, store, store)
}

func TestDeterministicCreateCanonicalizesMetadataFromIntoField(t *testing.T) {
	store := NewMemStore()
	metadataOnly := Bead{Title: "recover", Metadata: StringMap{"from": "recovery-responder"}}
	created, inserted, err := store.CreateDeterministic("canonical-from", metadataOnly)
	if err != nil || !inserted {
		t.Fatalf("metadata-only create = %+v, inserted=%v, err=%v", created, inserted, err)
	}
	if created.From != "recovery-responder" || created.Metadata["from"] != created.From {
		t.Fatalf("metadata-only create was not canonicalized: %+v", created)
	}

	fieldOnly := Bead{Title: "recover", From: "recovery-responder"}
	adopted, inserted, err := store.CreateDeterministic("canonical-from", fieldOnly)
	if err != nil || inserted || adopted.ID != created.ID {
		t.Fatalf("field-only retry = %+v, inserted=%v, err=%v; want adoption of %q", adopted, inserted, err, created.ID)
	}
}

func TestDeterministicBeadIDMatchesNativeShapeVector(t *testing.T) {
	if got, want := deterministicBeadID("GC-", deterministicCreateTestKey), "gc-b3agipoz"; got != want {
		t.Fatalf("deterministicBeadID = %q, want %q", got, want)
	}
}

func TestMemStoreDeterministicCreateIsAtomic(t *testing.T) {
	store := NewMemStore()
	const callers = 32
	start := make(chan struct{})
	type result struct {
		bead     Bead
		inserted bool
		err      error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			created, inserted, err := store.CreateDeterministic(deterministicCreateTestKey, deterministicCreateTestBead("recover session"))
			results <- result{bead: created, inserted: inserted, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var id string
	inserts := 0
	for result := range results {
		if result.err != nil {
			t.Errorf("CreateDeterministic: %v", result.err)
		}
		if result.inserted {
			inserts++
		}
		if id == "" {
			id = result.bead.ID
		} else if result.bead.ID != id {
			t.Errorf("created ID = %q, want %q", result.bead.ID, id)
		}
	}
	if inserts != 1 {
		t.Fatalf("reported %d insertions, want exactly 1", inserts)
	}
	all, err := store.List(ListQuery{IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("stored %d beads, want 1", len(all))
	}
}

func TestDeterministicCreateRejectsAmbiguousInputs(t *testing.T) {
	store := NewMemStore()
	tests := []struct {
		name string
		key  string
		bead Bead
	}{
		{name: "empty key", bead: deterministicCreateTestBead("recover")},
		{name: "padded key", key: " padded ", bead: deterministicCreateTestBead("recover")},
		{name: "explicit id", key: "key", bead: Bead{ID: "gc-owned", Title: "recover"}},
		{name: "blank explicit id", key: "key", bead: Bead{ID: " ", Title: "recover"}},
		{name: "non-open status", key: "key", bead: Bead{Title: "recover", Status: "closed"}},
		{name: "created timestamp", key: "key", bead: Bead{Title: "recover", CreatedAt: time.Now()}},
		{name: "parent", key: "key", bead: Bead{Title: "recover", ParentID: "gc-parent"}},
		{name: "unsorted labels", key: "key", bead: Bead{Title: "recover", Labels: []string{"z", "a"}}},
		{name: "subsecond defer", key: "key", bead: Bead{Title: "recover", DeferUntil: func() *time.Time { value := time.Unix(10, 1); return &value }()}},
		{name: "projection", key: "key", bead: Bead{Title: "recover", Dependencies: []Dep{{IssueID: "gc-1", DependsOnID: "gc-2"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := store.CreateDeterministic(test.key, test.bead); !errors.Is(err, ErrInvalidDeterministicCreate) {
				t.Fatalf("error = %v, want ErrInvalidDeterministicCreate", err)
			}
		})
	}
	all, err := store.List(ListQuery{IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("invalid calls wrote %+v", all)
	}
}

func TestCreateDeterministicallyFailsClosedWithoutCapability(t *testing.T) {
	plain := struct{ Store }{Store: NewMemStore()}
	if _, _, err := CreateDeterministically(plain, "key", Bead{Title: "recover"}); !errors.Is(err, ErrDeterministicCreateUnsupported) {
		t.Fatalf("error = %v, want ErrDeterministicCreateUnsupported", err)
	}
	all, err := plain.List(ListQuery{IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("unsupported fallback wrote %+v", all)
	}
}

func TestFileStoreDeterministicCreateUsesOneCrossProcessCriticalSection(t *testing.T) {
	path := t.TempDir() + "/beads.json"
	firstHandle, err := OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatal(err)
	}
	// Open the second handle before the first write. Its in-memory image is
	// intentionally stale, proving the retry reload happens under the file lock.
	secondHandle, err := OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatal(err)
	}

	first, inserted, err := firstHandle.CreateDeterministic(deterministicCreateTestKey, deterministicCreateTestBead("recover session"))
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("first handle reported adoption, want insertion")
	}
	adopted, inserted, err := secondHandle.CreateDeterministic(deterministicCreateTestKey, deterministicCreateTestBead("recover session"))
	if err != nil {
		t.Fatalf("stale second handle did not adopt first handle's write: %v", err)
	}
	if inserted {
		t.Fatal("stale second handle reported insertion, want adoption")
	}
	if adopted.ID != first.ID || !adopted.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("second handle returned a different row: first=%+v adopted=%+v", first, adopted)
	}
	if _, _, err := secondHandle.CreateDeterministic(deterministicCreateTestKey, deterministicCreateTestBead("conflicting request")); !errors.Is(err, ErrDeterministicCreateConflict) {
		t.Fatalf("second-handle conflict error = %v, want ErrDeterministicCreateConflict", err)
	}

	reopened, err := OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatal(err)
	}
	all, err := reopened.List(ListQuery{IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != first.ID || all[0].Title != "recover session" {
		t.Fatalf("on-disk result = %+v, want the first tuple only", all)
	}
}

func TestFileStoreConcurrentHandlesCreateOneRow(t *testing.T) {
	path := t.TempDir() + "/beads.json"
	left, err := OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatal(err)
	}
	right, err := OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan Bead, 2)
	insertions := make(chan bool, 2)
	errs := make(chan error, 2)
	for _, store := range []*FileStore{left, right} {
		go func(store *FileStore) {
			<-start
			created, inserted, createErr := store.CreateDeterministic(deterministicCreateTestKey, deterministicCreateTestBead("recover session"))
			results <- created
			insertions <- inserted
			errs <- createErr
		}(store)
	}
	close(start)
	inserted := 0
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("CreateDeterministic: %v", err)
		}
		if <-insertions {
			inserted++
		}
	}
	if inserted != 1 {
		t.Fatalf("reported %d insertions, want exactly 1", inserted)
	}
	first, second := <-results, <-results
	if first.ID != second.ID || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("concurrent handles did not converge: first=%+v second=%+v", first, second)
	}
	reopened, err := OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatal(err)
	}
	all, err := reopened.List(ListQuery{IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("stored %d rows, want 1", len(all))
	}
}

func TestBdStoreDeterministicCreateUsesOwnedNativeShapedIDWithoutForce(t *testing.T) {
	var mu sync.Mutex
	var persisted *Bead
	var createArgs []string
	runner := func(_ string, name string, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if name != "bd" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		switch args[0] {
		case "create":
			createArgs = slices.Clone(args)
			id := flagValue(args, "--id")
			if persisted != nil {
				return nil, errors.New("duplicate primary key")
			}
			priority := 1
			persisted = &Bead{
				ID: id, Title: "recover session", Status: "open", Type: "task", Priority: &priority,
				Description: "recover the impaired session", Labels: []string{"gc:recovery-responder"},
				Metadata:  StringMap{"from": "recovery-responder", "gc.recovery.incident_id": "incident-123", "gc.recovery.attempt": "1"},
				CreatedAt: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
			}
			return json.Marshal(persisted)
		case "show":
			if persisted == nil || args[len(args)-1] != persisted.ID {
				return nil, errors.New("no issue found matching")
			}
			return json.Marshal([]Bead{*persisted})
		default:
			return nil, fmt.Errorf("unexpected bd args: %v", args)
		}
	}
	store := NewBdStoreWithPrefix("/city", runner, "gc")
	assertDeterministicCreateContract(t, store, store)
	if !slices.Contains(createArgs, "--id") {
		t.Fatalf("bd create args = %v, want explicit --id", createArgs)
	}
	if slices.Contains(createArgs, "--force") {
		t.Fatalf("bd create args = %v, deterministic create must not use migration --force", createArgs)
	}
	id := flagValue(createArgs, "--id")
	if matched, _ := regexp.MatchString(`^gc-[0-9a-z]{8}$`, id); !matched {
		t.Fatalf("--id = %q, want native-shaped ID in the owned gc namespace", id)
	}
}

func TestBdStoreDeterministicCreateFailsClosedWithoutOwnedPrefix(t *testing.T) {
	calls := 0
	store := NewBdStore("/city", func(string, string, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("must not run")
	})
	if SupportsDeterministicCreate(store) {
		t.Fatal("prefixless BdStore advertised deterministic creation")
	}
	if _, _, err := store.CreateDeterministic("key", Bead{Title: "recover"}); !errors.Is(err, ErrInvalidDeterministicCreate) {
		t.Fatalf("error = %v, want ErrInvalidDeterministicCreate", err)
	}
	if calls != 0 {
		t.Fatalf("ran bd %d times without an owned prefix", calls)
	}
}

func TestBdStoreDeterministicCapabilityRequiresRunner(t *testing.T) {
	if SupportsDeterministicCreate(NewBdStoreWithPrefix("/city", nil, "gc")) {
		t.Fatal("runnerless BdStore advertised deterministic creation")
	}
	if !SupportsDeterministicCreate(NewBdStoreWithPrefix("/city", func(string, string, ...string) ([]byte, error) {
		return nil, nil
	}, "gc")) {
		t.Fatal("prefixed runnable BdStore hid deterministic creation")
	}
}

func flagValue(args []string, name string) string {
	for index := range args {
		if args[index] == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func TestCachingStoreForwardsDeterministicCreateAndRefreshesCache(t *testing.T) {
	backing := NewMemStore()
	events := 0
	cache := NewCachingStoreForTest(backing, func(eventType, _ string, _ json.RawMessage) {
		if eventType == "bead.created" {
			events++
		}
	})
	created, inserted, err := cache.CreateDeterministic("cache-key", Bead{Title: "cached"})
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("first cache call reported adoption, want insertion")
	}
	adopted, inserted, err := cache.CreateDeterministic("cache-key", Bead{Title: "cached"})
	if err != nil {
		t.Fatal(err)
	}
	if inserted || adopted.ID != created.ID {
		t.Fatalf("cache retry = (%+v, inserted=%t), want adoption of %q", adopted, inserted, created.ID)
	}
	if events != 1 {
		t.Fatalf("cache emitted %d created events, want only the insertion event", events)
	}
	got, err := cache.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Title != "cached" {
		t.Fatalf("cached bead = %+v, want %+v", got, created)
	}
}

type conflictingReadAfterDeterministicStore struct {
	*MemStore
	getCalls int
}

func (s *conflictingReadAfterDeterministicStore) Get(id string) (Bead, error) {
	s.getCalls++
	bead, err := s.MemStore.Get(id)
	bead.Title = "conflicting later read"
	return bead, err
}

type blockingDeterministicReturnStore struct {
	*MemStore
	created chan Bead
	resume  chan struct{}
}

func (s *blockingDeterministicReturnStore) CreateDeterministic(key string, bead Bead) (Bead, bool, error) {
	created, inserted, err := s.MemStore.CreateDeterministic(key, bead)
	if err == nil {
		s.created <- created
		<-s.resume
	}
	return created, inserted, err
}

func TestCachingStoreDeterministicSnapshotCannotOverwriteLaterMutation(t *testing.T) {
	backing := &blockingDeterministicReturnStore{
		MemStore: NewMemStore(),
		created:  make(chan Bead, 1),
		resume:   make(chan struct{}),
	}
	cache := NewCachingStoreForTest(backing, nil)
	result := make(chan error, 1)
	go func() {
		_, _, err := cache.CreateDeterministic("ordered-cache-key", Bead{Title: "created"})
		result <- err
	}()
	created := <-backing.created
	updatedTitle := "updated after deterministic commit"
	if err := cache.Update(created.ID, UpdateOpts{Title: &updatedTitle}); err != nil {
		t.Fatal(err)
	}
	fresh, err := backing.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	startSeq := cache.mutationSeq
	cache.mergeSnapshotLocked(map[string]Bead{created.ID: fresh}, nil, nil, false, startSeq, time.Now().Add(10*time.Second))
	cache.mu.Unlock()
	close(backing.resume)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	cached, err := cache.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Title != updatedTitle {
		t.Fatalf("late deterministic absorption overwrote newer mutation: %+v", cached)
	}
}

func TestCachingStoreKeepsAuthoritativeDeterministicSnapshot(t *testing.T) {
	backing := &conflictingReadAfterDeterministicStore{MemStore: NewMemStore()}
	cache := NewCachingStoreForTest(backing, nil)
	authoritative, inserted, err := cache.CreateDeterministic("authoritative-cache-key", Bead{Title: "authoritative create result"})
	if err != nil || !inserted {
		t.Fatalf("CreateDeterministic = %+v, inserted=%v, err=%v", authoritative, inserted, err)
	}
	if authoritative.Title != "authoritative create result" {
		t.Fatalf("returned snapshot = %+v, want atomic create result", authoritative)
	}
	if backing.getCalls != 0 {
		t.Fatalf("deterministic absorption issued %d later Get calls", backing.getCalls)
	}
	cached, err := cache.Get(authoritative.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Title != authoritative.Title {
		t.Fatalf("cached snapshot = %+v, want authoritative %+v", cached, authoritative)
	}
	if backing.getCalls != 0 {
		t.Fatalf("fresh cache lookup issued %d backing Get calls", backing.getCalls)
	}
}

func TestClassStoresReportBackingDeterministicCapability(t *testing.T) {
	plain := struct{ Store }{Store: NewMemStore()}
	stores := []Store{
		WorkStore{Store: plain},
		GraphStore{Store: plain},
		SessionStore{Store: plain},
		MailStore{Store: plain},
		OrdersStore{Store: plain},
		NudgesStore{Store: plain},
	}
	for index, store := range stores {
		if SupportsDeterministicCreate(store) {
			t.Fatalf("wrapper %d falsely advertised unsupported backing capability", index)
		}
	}
}

func TestClassStoresForwardDeterministicCreate(t *testing.T) {
	base := NewMemStore()
	stores := []DeterministicCreator{
		WorkStore{Store: base},
		GraphStore{Store: base},
		SessionStore{Store: base},
		MailStore{Store: base},
		OrdersStore{Store: base},
		NudgesStore{Store: base},
	}
	for index, store := range stores {
		created, inserted, err := store.CreateDeterministic(fmt.Sprintf("class-%d", index), Bead{Title: fmt.Sprintf("class %d", index)})
		if err != nil {
			t.Fatalf("store %d: %v", index, err)
		}
		if !inserted {
			t.Fatalf("store %d reported adoption, want insertion", index)
		}
		if !strings.HasPrefix(created.ID, "gc-") {
			t.Fatalf("store %d returned ID %q", index, created.ID)
		}
	}
}
