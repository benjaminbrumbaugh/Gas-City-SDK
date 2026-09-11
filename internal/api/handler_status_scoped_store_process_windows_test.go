//go:build windows

package api

import (
	"context"
	"os/exec"

	"github.com/gastownhall/gascity/internal/beads"
)

// contextBoundScopedTestRunner keeps the fixture bound to the request context
// on platforms without the Unix process-group implementation.
func contextBoundScopedTestRunner(ctx context.Context) beads.CommandRunner {
	return func(dir, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		return cmd.Output()
	}
}
