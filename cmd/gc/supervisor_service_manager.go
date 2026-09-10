package main

import (
	"os"
	"strings"
)

// supervisorServiceManagerEnv names the explicit opt-out from launchd or
// systemd ownership of the supervisor.
const supervisorServiceManagerEnv = "GC_SUPERVISOR_SERVICE_MANAGER"

// supervisorServiceManagerBypassed reports whether the caller explicitly
// requested a bare supervisor child instead of platform service-manager
// ownership. Only the exact value "none" opts out; a typo must not silently
// remove lifecycle ownership on a real machine.
func supervisorServiceManagerBypassed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(supervisorServiceManagerEnv)), "none")
}
