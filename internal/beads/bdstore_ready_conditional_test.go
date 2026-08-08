package beads

import (
	"errors"
	"slices"
	"testing"
)

func TestBdStoreReadyConditionalWriterUsesBothBackendGuards(t *testing.T) {
	var writeArgs []string
	store := NewBdStore(t.TempDir(), func(_, _ string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "--help" {
			return []byte("Flags:\n --if-revision int\n --if-ready\n"), nil
		}
		writeArgs = append([]string(nil), args...)
		return []byte(`{"id":"work-1"}`), nil
	})
	title := "routed"
	if err := store.UpdateIfReadyAndMatch("work-1", 7, UpdateOpts{Title: &title}); err != nil {
		t.Fatalf("UpdateIfReadyAndMatch: %v", err)
	}
	if !slices.Contains(writeArgs, "--if-revision") || !slices.Contains(writeArgs, "--if-ready") {
		t.Fatalf("write args = %q, want revision and readiness guards", writeArgs)
	}
}

func TestBdStoreReadyConditionalWriterFailsClosedWithoutReadyFlag(t *testing.T) {
	store := NewBdStore(t.TempDir(), func(_, _ string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "--help" {
			return []byte("Flags:\n --if-revision int\n"), nil
		}
		t.Fatalf("unexpected write without --if-ready support: %q", args)
		return nil, nil
	})
	title := "routed"
	if err := store.UpdateIfReadyAndMatch("work-1", 7, UpdateOpts{Title: &title}); !errors.Is(err, ErrConditionalWriteUnsupported) {
		t.Fatalf("error = %v, want ErrConditionalWriteUnsupported", err)
	}
}

func TestConditionalClassifierMapsNotReadyCode(t *testing.T) {
	err := classifyConditionalWriteResult([]byte(`{"error":"blocked","code":"not_ready"}`), errors.New("exit status 1"))
	if !errors.Is(err, ErrNotReadyForConditionalUpdate) {
		t.Fatalf("error = %v, want ErrNotReadyForConditionalUpdate", err)
	}
}
