package beads

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/rollout/gate"
)

type readyConditionalWriterFunc func(string, int64, UpdateOpts) error

func (f readyConditionalWriterFunc) UpdateIfReadyAndMatch(id string, revision int64, opts UpdateOpts) error {
	return f(id, revision, opts)
}

type readyConditionalHandleWrapper struct {
	Store
	handle ReadyConditionalWriter
}

func (w *readyConditionalHandleWrapper) ConditionalWritesResolveTarget() Store { return w.Store }

func (w *readyConditionalHandleWrapper) ReadyConditionalWriterHandle() (ReadyConditionalWriter, bool) {
	return w.handle, w.handle != nil
}

func TestMemStoreUpdateIfReadyAndMatchRejectsUnreadyWithoutWorkRevisionChange(t *testing.T) {
	const (
		blockerID = "GC-BLOCKER"
		workID    = "GC-WORK"
	)
	store := NewMemStoreFrom(0, []Bead{
		{ID: blockerID, Status: "open"},
		{ID: workID, Status: "open"},
	}, nil)
	work, err := store.Get(workID)
	if err != nil {
		t.Fatalf("get work: %v", err)
	}
	beforeRevision := work.Revision
	if err := store.DepAdd(workID, blockerID, "blocks"); err != nil {
		t.Fatalf("add blocking dependency: %v", err)
	}
	afterDependency, err := store.Get(workID)
	if err != nil {
		t.Fatalf("get work after dependency: %v", err)
	}
	if afterDependency.Revision != beforeRevision {
		t.Fatalf("derived dependency change revised work: got %d, want %d", afterDependency.Revision, beforeRevision)
	}
	title := "must not route while blocked"
	if err := store.UpdateIfReadyAndMatch(workID, beforeRevision, UpdateOpts{Title: &title}); !errors.Is(err, ErrNotReadyForConditionalUpdate) {
		t.Fatalf("UpdateIfReadyAndMatch() error = %v, want ErrNotReadyForConditionalUpdate", err)
	}
	after, err := store.Get(workID)
	if err != nil {
		t.Fatalf("get work after refused update: %v", err)
	}
	if after.Revision != beforeRevision || after.Title == title {
		t.Fatalf("refused update mutated work: before=%+v after=%+v", work, after)
	}

	if err := store.Close(blockerID); err != nil {
		t.Fatalf("close blocker: %v", err)
	}
	if err := store.UpdateIfReadyAndMatch(workID, beforeRevision, UpdateOpts{Title: &title}); err != nil {
		t.Fatalf("ready conditional update after blocker close: %v", err)
	}
}

func TestReadyConditionalWriterForHonorsStampedConditionalWritesMode(t *testing.T) {
	store := NewMemStore()
	if _, ok := ReadyConditionalWriterFor(store); ok {
		t.Fatal("unset conditional-writes mode exposed a ready conditional writer")
	}
	store.stampConditionalWritesMode(gate.Off, false)
	if _, ok := ReadyConditionalWriterFor(store); ok {
		t.Fatal("explicit conditional_writes=off exposed a ready conditional writer")
	}
	store.stampConditionalWritesMode(gate.Require, false)
	if _, ok := ReadyConditionalWriterFor(store); !ok {
		t.Fatal("conditional_writes=require hid a capable ready conditional writer")
	}
}

func TestReadyConditionalWriterForReturnsWrapperHandleAfterBackingPolicyValidation(t *testing.T) {
	backing := NewMemStore()
	backing.stampConditionalWritesMode(gate.Require, false)
	var called bool
	handle := readyConditionalWriterFunc(func(string, int64, UpdateOpts) error {
		called = true
		return nil
	})
	wrapper := &readyConditionalHandleWrapper{Store: backing, handle: handle}

	writer, ok := ReadyConditionalWriterFor(wrapper)
	if !ok {
		t.Fatal("ReadyConditionalWriterFor(wrapper) = unavailable")
	}
	if err := writer.UpdateIfReadyAndMatch("GC-WORK", 1, UpdateOpts{Metadata: map[string]string{"route": "safe"}}); err != nil {
		t.Fatalf("wrapper ready handle: %v", err)
	}
	if !called {
		t.Fatal("ReadyConditionalWriterFor returned the raw backing instead of the wrapper handle")
	}

	backing.stampConditionalWritesMode(gate.Off, false)
	if _, ok := ReadyConditionalWriterFor(wrapper); ok {
		t.Fatal("wrapper handle bypassed the backing's conditional_writes=off policy")
	}
}
