package main

import (
	"os"
	"strings"
)

// supervisorServiceManagerEnv opts the supervisor lifecycle out of the
// platform service manager.
//
// gc normally hands supervisor availability to launchd (macOS) or systemd
// (Linux): `gc start`/`gc init` install a service file and let the manager
// own the process. On macOS that ownership is enforced — a failed install
// is terminal and `gc supervisor start` refuses to fork, because a detached
// child alongside a registered launchd job produces split ownership and a
// crash loop against the occupied supervisor port.
//
// That invariant is right for a real machine and wrong for a hermetic test
// environment, which must never register a job in the host's launchd. Set
// GC_SUPERVISOR_SERVICE_MANAGER=none to say so explicitly: gc then skips
// the install entirely and runs the supervisor as a bare child of the
// calling process, on every platform.
//
// The bypass must be requested, not inferred. Before this existed the
// acceptance harness got the same effect by shimming `launchctl` and
// `systemctl` to exit 1 and relying on gc falling back after the install
// failed — an accident of error handling rather than a contract, and one
// the macOS ownership rule removed, which is what left Mac acceptance red
// on every run (gc-2rglt).
const supervisorServiceManagerEnv = "GC_SUPERVISOR_SERVICE_MANAGER"

// supervisorServiceManagerBypassed reports whether the caller asked gc to
// run the supervisor as a bare child instead of delegating it to launchd
// or systemd. Only the exact opt-out value counts; anything else (empty,
// "launchd", a typo) keeps the platform manager in charge, so a mistyped
// variable cannot silently strip lifecycle ownership on a real machine.
func supervisorServiceManagerBypassed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(supervisorServiceManagerEnv)), "none")
}
