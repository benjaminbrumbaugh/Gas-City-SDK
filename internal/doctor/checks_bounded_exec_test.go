package doctor

import (
	"errors"
	"strings"
	"testing"
)

// lookPathOnly returns a LookPathFunc that resolves exactly the named
// binaries and reports every other name as absent.
func lookPathOnly(present ...string) LookPathFunc {
	set := make(map[string]struct{}, len(present))
	for _, p := range present {
		set[p] = struct{}{}
	}
	return func(name string) (string, error) {
		if _, ok := set[name]; ok {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func TestBoundedExecCheckPrefersGtimeout(t *testing.T) {
	// Both present: gtimeout wins, matching bounded.sh's resolution order.
	r := NewBoundedExecCheck(lookPathOnly("gtimeout", "timeout", "python3")).Run(nil)
	if r.Status != StatusOK {
		t.Fatalf("status = %v, want StatusOK", r.Status)
	}
	if !strings.Contains(r.Message, "gtimeout") {
		t.Errorf("message = %q, want it to name gtimeout", r.Message)
	}
	if strings.Contains(r.Message, "python3") {
		t.Errorf("message = %q, should not mention a fallback when gtimeout exists", r.Message)
	}
}

func TestBoundedExecCheckAcceptsTimeout(t *testing.T) {
	r := NewBoundedExecCheck(lookPathOnly("timeout")).Run(nil)
	if r.Status != StatusOK {
		t.Fatalf("status = %v, want StatusOK", r.Status)
	}
	if !strings.Contains(r.Message, "timeout") {
		t.Errorf("message = %q, want it to name timeout", r.Message)
	}
}

// TestBoundedExecCheckFlagsMissingTimeout is the regression fence for
// gc-z8b / sdk-4is. A stock macOS host ships neither timeout nor gtimeout,
// and that absence used to be invisible to doctor — which is why three
// separate patrols rediscovered it by misreading a health payload as a
// dead data plane. Doctor must say so out loud.
func TestBoundedExecCheckFlagsMissingTimeout(t *testing.T) {
	r := NewBoundedExecCheck(lookPathOnly("python3")).Run(nil)
	if r.Status != StatusWarning {
		t.Fatalf("status = %v, want StatusWarning when timeout/gtimeout are absent", r.Status)
	}
	if !strings.Contains(r.Message, "no timeout/gtimeout on PATH") {
		t.Errorf("message = %q, want it to state the absence explicitly", r.Message)
	}
	if !strings.Contains(r.Message, "python3") {
		t.Errorf("message = %q, want it to name the python3 fallback actually in use", r.Message)
	}
	if r.FixHint == "" {
		t.Error("want a FixHint telling the operator how to clear the warning")
	}
}

func TestBoundedExecCheckReportsShellWatchdog(t *testing.T) {
	// Nothing at all: bounded.sh still bounds the child with its shell
	// watchdog, so this is a warning, never an error.
	r := NewBoundedExecCheck(lookPathOnly()).Run(nil)
	if r.Status != StatusWarning {
		t.Fatalf("status = %v, want StatusWarning", r.Status)
	}
	if !strings.Contains(r.Message, "shell watchdog") {
		t.Errorf("message = %q, want it to name the shell watchdog fallback", r.Message)
	}
}

// TestBoundedExecCheckNeverErrors pins the core judgement: a missing
// timeout binary is the normal state on macOS, not a fault. Reporting
// StatusError would fabricate the same false alarm this check prevents,
// and SeverityAdvisory keeps a bare-host warning from gating dispatch.
func TestBoundedExecCheckNeverErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present []string
	}{
		{"all", []string{"gtimeout", "timeout", "python3"}},
		{"timeout only", []string{"timeout"}},
		{"python3 only", []string{"python3"}},
		{"nothing", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewBoundedExecCheck(lookPathOnly(tc.present...)).Run(nil)
			if r.Status == StatusError {
				t.Errorf("status = StatusError for %v; a missing bound helper must never be an error", tc.present)
			}
			if r.Severity != SeverityAdvisory {
				t.Errorf("severity = %v, want SeverityAdvisory so this never gates dispatch", r.Severity)
			}
		})
	}
}
