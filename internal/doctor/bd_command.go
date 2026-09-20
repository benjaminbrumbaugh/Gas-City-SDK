package doctor

import (
	"os"
	"os/exec"

	"github.com/gastownhall/gascity/internal/execenv"
)

// bdCommand builds a direct bd invocation for a doctor check. Doctor checks
// bypass the shared beads runner, so the gc-wide bd telemetry opt-out is
// applied here: bd's unbounded ~/.beads/eventsData spool must never be fed by
// a gc-spawned child, whatever the inherited environment says.
func bdCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("bd", args...)
	cmd.Dir = dir
	cmd.Env = execenv.WithBdMetricsDisabled(os.Environ())
	return cmd
}
