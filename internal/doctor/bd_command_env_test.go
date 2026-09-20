package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorBdCommandsDisableMetrics pins the direct doctor bd subprocess
// paths, which bypass the shared beads runner: each must still opt out of
// bd's anonymous telemetry (its unbounded ~/.beads/eventsData spool broke
// Time Machine on one operator machine), replacing any inherited opt-in.
func TestDoctorBdCommandsDisableMetrics(t *testing.T) {
	binDir := t.TempDir()
	envFile := filepath.Join(binDir, "env.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$BD_DISABLE_METRICS\" >> \"" + envFile + "\"\n" +
		"case \"$1\" in config) [ \"$2\" = get ] && printf '{\"types.custom\":\"\"}\\n';; types) printf '{\"custom_types\":[]}\\n';; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("BD_DISABLE_METRICS", "false")
	dir := t.TempDir()

	if _, err := getCustomTypes(dir); err != nil {
		t.Fatalf("getCustomTypes: %v", err)
	}
	if _, err := getRegisteredTypes(dir); err != nil {
		t.Fatalf("getRegisteredTypes: %v", err)
	}
	if err := setCustomTypes(dir, "step"); err != nil {
		t.Fatalf("setCustomTypes: %v", err)
	}
	if err := removeDoltRemote(dir, "backup_export"); err != nil {
		t.Fatalf("removeDoltRemote: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 {
		t.Fatalf("recorded %d bd invocations, want 4: %q", len(lines), lines)
	}
	for i, line := range lines {
		if line != "1" {
			t.Errorf("bd invocation %d saw BD_DISABLE_METRICS=%q, want 1", i, line)
		}
	}
}
