package scripts_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Inventory source reachable in normal and integration builds, including
// production and test package init. Include canonical Linux and the host's
// source scopes; inspecting Linux AST is not a claim of Linux execution.
func gateEntrypointFiles(t *testing.T) []string {
	t.Helper()
	contexts := []build.Context{build.Default, build.Default, build.Default, build.Default}
	contexts[1].BuildTags = []string{"integration"}
	contexts[2].GOOS, contexts[2].GOARCH = "linux", "amd64"
	contexts[3].GOOS, contexts[3].GOARCH, contexts[3].BuildTags = "linux", "amd64", []string{"integration"}
	files := []string{"../cmd/gc/tmux_leak_guard_test.go"} // cleanup helper called by cmd/gc TestMain
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != ".." && (strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), "_") || entry.Name() == "temp" || entry.Name() == "testdata" || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		included := false
		for _, ctx := range contexts {
			matched, err := ctx.MatchFile(filepath.Dir(path), entry.Name())
			if err != nil {
				return err
			}
			included = included || matched
		}
		if !included {
			return nil
		}
		tree, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range tree.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && (fn.Name.Name == "init" || fn.Name.Name == "TestMain") {
				files = append(files, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	return slices.Compact(files)
}

func TestGateEntrypointInventoryIncludesPackageInit(t *testing.T) {
	files := gateEntrypointFiles(t)
	for _, want := range []string{"../internal/sling/sling_test.go", "../internal/testenv/testenv.go", "../cmd/gc/dolt_cleanup_purge_test.go", "../cmd/gc/main_test.go", "../internal/runtime/tmux/main_test.go"} {
		if !slices.Contains(files, want) {
			t.Errorf("normal/integration inventory missed init/TestMain in %s", want)
		}
	}
}

// These entrypoints execute even for -list or an empty -run selector.
func TestGateTestMainDoesNotAcquireForeignCleanupAuthority(t *testing.T) {
	for _, file := range gateEntrypointFiles(t) {
		t.Run(file, func(t *testing.T) {
			tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(tree, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					if fn.Name == "sweepStaleTmuxTestServers" || fn.Name == "sweepOrphanSlingPIDPrefixedDirs" {
						t.Errorf("gate init/TestMain invokes foreign namespace reaper %s", fn.Name)
					}
				case *ast.SelectorExpr:
					if fn.Sel.Name == "KillAllTestSessions" || fn.Sel.Name == "NewSocketParentDir" {
						t.Errorf("gate invokes sibling-discovering %s", fn.Sel.Name)
					}
					if fn.Sel.Name == "SweepStale" || fn.Sel.Name == "SweepOrphanStoreDirsWithPrefix" {
						root, ok := call.Args[0].(*ast.Ident)
						if !ok || root.Name != "runRoot" {
							t.Errorf("%s requires exact fresh runRoot", fn.Sel.Name)
						}
					}
				}
				return true
			})
		})
	}
}
