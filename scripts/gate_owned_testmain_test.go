package scripts_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// These entrypoints execute even for -list or an empty -run selector.
func TestGateTestMainDoesNotAcquireForeignCleanupAuthority(t *testing.T) {
	for _, file := range []string{"../examples/gastown/main_test.go", "../cmd/gc/tmux_leak_guard_test.go", "../test/integration/integration_test.go"} {
		t.Run(filepath.Base(filepath.Dir(file))+"/"+filepath.Base(file), func(t *testing.T) {
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
					if fn.Name == "sweepStaleTmuxTestServers" {
						t.Error("gate entrypoint invokes globally attributed stale tmux reaper")
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
