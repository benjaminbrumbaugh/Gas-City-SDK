package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/test/tmuxtest"
)

// TestMain owns a fresh root for every socket, staged command file and child
// temporary directory. Never sweep previous runs: those resources are not ours.
// macOS callers must provide a short TMPDIR; there is no shared /tmp fallback.
func TestMain(m *testing.M) {
	_ = os.Unsetenv(AgentSliceEnv)
	root, err := os.MkdirTemp("", "rt-")
	if err != nil {
		panic("tmux tests: creating owned root: " + err.Error())
	}
	if err := tmuxtest.ConfigureOwnedProcessEnv(root); err != nil {
		_ = os.RemoveAll(root)
		panic("tmux tests: configuring owned root: " + err.Error())
	}
	_, _ = fmt.Fprintln(os.Stderr, "tmux tests: owned root:", root)
	code := m.Run()
	if err := tmuxtest.CleanupOwnedSocketRoot(filepath.Join(root, "s")); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		code = 1
	} else if err := os.RemoveAll(root); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
