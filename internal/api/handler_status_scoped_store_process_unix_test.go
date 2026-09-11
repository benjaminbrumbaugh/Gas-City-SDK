//go:build !windows

package api

import (
	"context"
	"os/exec"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/processgroup"
)

// contextBoundScopedTestRunner avoids the process-wide bd execution slot so
// fixture startup is not canceled before it can publish its PID. It retains
// the production process-group contract for the descendant cleanup assertion.
func contextBoundScopedTestRunner(ctx context.Context) beads.CommandRunner {
	return func(dir, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.WaitDelay = 2 * time.Second
		processgroup.StartCommandInNewGroup(cmd)
		cmd.Cancel = func() error {
			return processgroup.TerminateCommand(cmd, 0, time.Second, processgroup.Options{})
		}
		cmd.Dir = dir
		return cmd.Output()
	}
}
