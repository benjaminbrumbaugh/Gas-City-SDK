package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestRunFileStoreUsesProjectedScopeAndPrefix(t *testing.T) {
	cityDir := t.TempDir()
	rigDir := filepath.Join(t.TempDir(), "rig-0")
	t.Setenv("GC_STORE_ROOT", rigDir)
	t.Setenv("GC_BEADS_PREFIX", "r0")

	var stdout bytes.Buffer
	code, handled, err := runFileStore(fileStoreScopeRoot(cityDir), []string{"create", "work"}, &stdout)
	if err != nil {
		t.Fatalf("runFileStore: %v", err)
	}
	if !handled || code != 0 {
		t.Fatalf("runFileStore result = (%d, %t), want (0, true)", code, handled)
	}
	if got, want := stdout.String(), "Created bead: r0-1\n"; got != want {
		t.Fatalf("create output = %q, want %q", got, want)
	}

	store, recorder, err := openFileStore(rigDir)
	if err != nil {
		t.Fatalf("open rig store: %v", err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	if _, err := store.Get("r0-1"); err != nil {
		t.Fatalf("rig store missing created bead: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cityDir, ".gc", "beads.json")); !os.IsNotExist(err) {
		t.Fatalf("city store was used: stat error = %v", err)
	}
}

func TestFileStoreScopeRootFallsBackToCity(t *testing.T) {
	cityDir := t.TempDir()
	t.Setenv("GC_STORE_ROOT", "")
	if got := fileStoreScopeRoot(cityDir); got != cityDir {
		t.Fatalf("fileStoreScopeRoot = %q, want %q", got, cityDir)
	}

	store, recorder, err := openFileStore(cityDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	bead, err := store.Create(beads.Bead{Title: "city work"})
	if err != nil {
		t.Fatal(err)
	}
	if bead.ID != "gc-1" {
		t.Fatalf("fallback store ID = %q, want gc-1", bead.ID)
	}
}

func TestRunFileStoreReadyExcludesSessionBeads(t *testing.T) {
	cityDir := newShimTestCity(t)
	store, recorder, err := openFileStore(cityDir)
	if err != nil {
		t.Fatalf("openFileStore: %v", err)
	}
	defer recorder.Close() //nolint:errcheck

	if _, err := store.Create(beads.Bead{Title: "task", Type: "task"}); err != nil {
		t.Fatalf("Create(task): %v", err)
	}
	if _, err := store.Create(beads.Bead{Title: "session", Type: "session"}); err != nil {
		t.Fatalf("Create(session): %v", err)
	}

	var stdout bytes.Buffer
	code, handled, err := runFileStore(cityDir, []string{"ready", "--json"}, &stdout)
	if err != nil {
		t.Fatalf("runFileStore(ready): %v", err)
	}
	if !handled || code != 0 {
		t.Fatalf("runFileStore handled=%v code=%d, want handled=true code=0", handled, code)
	}

	var items []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput=%s", err, stdout.String())
	}
	if len(items) != 1 {
		t.Fatalf("ready returned %d items, want 1\noutput=%s", len(items), stdout.String())
	}
	if got := items[0]["title"]; got != "task" {
		t.Fatalf("ready title = %v, want task", got)
	}
}

func TestRunFileStoreReadyRespectsAssigneeFilter(t *testing.T) {
	cityDir := newShimTestCity(t)
	store, recorder, err := openFileStore(cityDir)
	if err != nil {
		t.Fatalf("openFileStore: %v", err)
	}
	defer recorder.Close() //nolint:errcheck

	task, err := store.Create(beads.Bead{Title: "claimed-task", Type: "task"})
	if err != nil {
		t.Fatalf("Create(task): %v", err)
	}
	if err := store.Update(task.ID, beads.UpdateOpts{Assignee: stringPtr("worker")}); err != nil {
		t.Fatalf("Update(task assignee): %v", err)
	}
	session, err := store.Create(beads.Bead{Title: "worker-session", Type: "session"})
	if err != nil {
		t.Fatalf("Create(session): %v", err)
	}
	if err := store.Update(session.ID, beads.UpdateOpts{Assignee: stringPtr("worker")}); err != nil {
		t.Fatalf("Update(session assignee): %v", err)
	}

	var stdout bytes.Buffer
	code, handled, err := runFileStore(cityDir, []string{"ready", "--assignee=worker", "--json"}, &stdout)
	if err != nil {
		t.Fatalf("runFileStore(ready --assignee): %v", err)
	}
	if !handled || code != 0 {
		t.Fatalf("runFileStore handled=%v code=%d, want handled=true code=0", handled, code)
	}

	var items []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput=%s", err, stdout.String())
	}
	if len(items) != 1 {
		t.Fatalf("ready returned %d items, want 1\noutput=%s", len(items), stdout.String())
	}
	if got := items[0]["title"]; got != "claimed-task" {
		t.Fatalf("ready title = %v, want claimed-task", got)
	}
}

func newShimTestCity(t *testing.T) string {
	t.Helper()
	cityDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityDir, ".gc"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.gc): %v", err)
	}
	store, recorder, err := openFileStore(cityDir)
	if err != nil {
		t.Fatalf("openFileStore(init): %v", err)
	}
	defer recorder.Close() //nolint:errcheck
	if _, err := store.List(beads.ListQuery{AllowScan: true}); err != nil {
		t.Fatalf("List(init): %v", err)
	}
	return cityDir
}

func stringPtr(s string) *string {
	return &s
}
