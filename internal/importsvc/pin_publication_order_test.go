package importsvc

import (
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/packman"
)

// A pin must never become observable before the content it names is in the
// shared cache.
//
// gc-w4bpj: for ~90 seconds every config load in a live city failed with
// "remote import ... is locked but not cached at <sha256 path>", because the
// commit recorded in packs.lock had no cache directory. The loader is not at
// fault — resolveInstalledRemoteImport reads packs.lock and derives the cache
// path from the commit it finds there, which is exactly what a lockfile is
// for. The window can only exist if a writer publishes the new pin before
// materializing it.
//
// The import paths get this right today: SyncLock materializes every chosen
// commit (packman.syncState.walkImport -> EnsureRepoInCache) and only then does
// the caller write packs.lock. That ordering is load-bearing and invisible —
// nothing about `syncLock(...)` followed by `writeLockfile(...)` announces that
// swapping the two lines would take the city offline. These tests pin it.

func recordOrder(t *testing.T, order *[]string) Deps {
	t.Helper()
	return Deps{
		ResolveVersion: func(_, _, _ string) (packman.ResolvedVersion, error) {
			return packman.ResolvedVersion{Version: "1.4.2", Commit: "abc123"}, nil
		},
		DefaultConstraint: func(string) (string, error) { return "^1.4", nil },
		SyncLock: func(_ string, _ map[string]config.Import, _ packman.InstallMode) (*packman.Lockfile, error) {
			// SyncLock is where EnsureRepoInCache runs for every chosen commit.
			*order = append(*order, "materialize")
			return &packman.Lockfile{
				Schema: packman.LockfileSchema,
				Packs: map[string]packman.LockedPack{
					"https://github.com/example/tools.git": {Version: "1.4.2", Commit: "abc123"},
				},
			}, nil
		},
		WriteLockfile: func(fsys.FS, string, *packman.Lockfile) error {
			*order = append(*order, "publish-pin")
			return nil
		},
	}
}

func assertMaterializeBeforePublish(t *testing.T, order []string) {
	t.Helper()
	materialized, published := -1, -1
	for i, step := range order {
		switch step {
		case "materialize":
			if materialized < 0 {
				materialized = i
			}
		case "publish-pin":
			if published < 0 {
				published = i
			}
		}
	}
	if materialized < 0 {
		t.Fatalf("no cache materialization happened at all; order = %v", order)
	}
	if published < 0 {
		t.Fatalf("packs.lock was never written; order = %v", order)
	}
	if materialized > published {
		t.Fatalf("packs.lock was published before the cache was materialized; order = %v\n"+
			"Every config load in the city fails for the width of that window (gc-w4bpj).", order)
	}
}

func TestAddImportMaterializesCacheBeforePublishingThePin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "city.toml"), "[workspace]\nname = \"demo\"\n")

	var order []string
	if _, err := AddImportWith(
		fsys.OSFS{}, dir,
		"https://github.com/example/tools.git", "", "",
		recordOrder(t, &order),
	); err != nil {
		t.Fatalf("AddImportWith: %v", err)
	}
	assertMaterializeBeforePublish(t, order)
}

func TestRemoveImportMaterializesCacheBeforePublishingThePin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "city.toml"), "[workspace]\nname = \"demo\"\n")

	var addOrder []string
	if _, err := AddImportWith(
		fsys.OSFS{}, dir,
		"https://github.com/example/tools.git", "", "",
		recordOrder(t, &addOrder),
	); err != nil {
		t.Fatalf("AddImportWith: %v", err)
	}

	// Removal rewrites packs.lock to the remaining graph, so it publishes a new
	// pin set and is subject to the same invariant.
	var order []string
	if _, err := RemoveImportWith(fsys.OSFS{}, dir, "tools", recordOrder(t, &order)); err != nil {
		t.Fatalf("RemoveImportWith: %v", err)
	}
	assertMaterializeBeforePublish(t, order)
}
