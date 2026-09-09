package dolt_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoltHealthOrderIsDiagnosticOnly(t *testing.T) {
	root := repoRoot(t)
	orderPath := filepath.Join(root, "orders", "dolt-health.toml")
	data, err := os.ReadFile(orderPath)
	if err != nil {
		t.Fatalf("read dolt-health order: %v", err)
	}

	text := string(data)
	if !strings.Contains(text, `exec = "gc dolt health --json | gc dolt health-check"`) {
		t.Fatalf("dolt-health order should run bounded health JSON, got:\n%s", text)
	}
	for _, forbidden := range []string{"gc dolt start", "gc dolt status"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("dolt-health order must not call %q directly:\n%s", forbidden, text)
		}
	}
}

func TestDoltHealthCheckFailsUnreachableReportWithUsefulMessage(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "commands", "health-check", "run.sh")
	input := `{
  "server": {
    "running": true,
    "reachable": false,
    "pid": 123,
    "port": 3311,
    "latency_ms": 0
  }
}`

	cmd := exec.Command("sh", script)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("health-check unexpectedly succeeded:\n%s", out)
	}
	for _, want := range []string{"Dolt server unreachable", "running=true", "pid=123", "port=3311"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("health-check output missing %q:\n%s", want, out)
		}
	}
}

// runHealthCheck feeds a health report to the health-check script and
// returns its exit code and combined output.
func runHealthCheck(t *testing.T, report string) (int, string) {
	t.Helper()

	script := filepath.Join(repoRoot(t), "commands", "health-check", "run.sh")
	cmd := exec.Command("sh", script)
	cmd.Stdin = strings.NewReader(report)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	exitErr := &exec.ExitError{}
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("running health-check: %v\n%s", err, out)
	return 0, ""
}

// TestDoltHealthCheckDoesNotScoreUnavailableProbeAsUnreachable is the
// order-consumer half of sdk-4is. `gc dolt health` reports
// server.probe=unavailable when the SQL ping could not be executed at
// all — reachability is then UNKNOWN, and the accompanying
// reachable=false is an absence of evidence, not evidence of an outage.
//
// health-check is what turns this report into an order outcome, so a
// consumer that reads only `reachable` re-creates the whole incident one
// layer down: it announces an unreachable server, the patrol's
// escalation table scores that CRITICAL, and a healthy Dolt gets
// restarted. The bead requires the exit path AND the message to differ
// from a real outage, so this pins both.
func TestDoltHealthCheckDoesNotScoreUnavailableProbeAsUnreachable(t *testing.T) {
	const unavailable = `{
  "server": {
    "running": true,
    "reachable": false,
    "probe": "unavailable",
    "pid": 123,
    "port": 3311,
    "latency_ms": 0
  }
}`
	const unreachable = `{
  "server": {
    "running": true,
    "reachable": false,
    "probe": "failed",
    "pid": 123,
    "port": 3311,
    "latency_ms": 0
  }
}`

	unavailableCode, unavailableOut := runHealthCheck(t, unavailable)
	unreachableCode, _ := runHealthCheck(t, unreachable)

	if strings.Contains(unavailableOut, "Dolt server unreachable") {
		t.Fatalf("a probe that never ran was reported as an unreachable server:\n%s", unavailableOut)
	}
	if lower := strings.ToLower(unavailableOut); !strings.Contains(lower, "diagnostic unavailable") {
		t.Fatalf("expected the unavailable report to say the diagnostic is unavailable:\n%s", unavailableOut)
	}
	if lower := strings.ToLower(unavailableOut); strings.Contains(lower, "timed out") {
		t.Fatalf("a probe that never ran must never be described as timed out:\n%s", unavailableOut)
	}
	if unavailableCode == unreachableCode {
		t.Fatalf("exit code %d is shared by 'the probe could not run' and 'the server is unreachable'; "+
			"the two must take different exit paths so a caller can tell them apart", unavailableCode)
	}
}

func TestDoltHealthCheckPassesReachableReport(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "commands", "health-check", "run.sh")
	input := `{
  "server": {
    "running": true,
    "reachable": true,
    "pid": 123,
    "port": 3311,
    "latency_ms": 12
  }
}`

	cmd := exec.Command("sh", script)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("health-check failed: %v\n%s", err, out)
	}
}
