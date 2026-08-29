package doctor

// events-log-size threshold alignment (gc-dfwf5). The check used a hardcoded
// 100 MB while the runtime's events-rotation ceiling defaults to 256 MB, so a
// city between the two warned permanently while rotation behaved exactly as
// designed — and the hint told the operator to hand-truncate a file the runtime
// owns and is actively appending to.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// eventsCityWithLog writes a .gc/events.jsonl of the given size and returns the
// city path.
func eventsCityWithLog(t *testing.T, size int) string {
	t.Helper()
	cityPath := t.TempDir()
	gcDir := filepath.Join(cityPath, ".gc")
	if err := os.MkdirAll(gcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gcDir, "events.jsonl"), make([]byte, size), 0o644); err != nil {
		t.Fatalf("write events.jsonl: %v", err)
	}
	return cityPath
}

func rotationCity(maxSize int64, enabled *bool) *config.City {
	cfg := &config.City{}
	cfg.Events.Rotation.MaxSizeBytes = &maxSize
	cfg.Events.Rotation.Enabled = enabled
	return cfg
}

// TestEventLogSizeUnderRotationCeilingIsOK is the live 104.7 MB case: a log
// larger than the old 100 MB literal but well under the rotation ceiling is
// rotation working, not a fault.
func TestEventLogSizeUnderRotationCeilingIsOK(t *testing.T) {
	cityPath := eventsCityWithLog(t, 2048)
	c := NewEventLogSizeCheckForConfig(rotationCity(4096, nil), nil)
	r := c.Run(&CheckContext{CityPath: cityPath})
	if r.Status != StatusOK {
		t.Fatalf("status = %v, want OK; message %q", r.Status, r.Message)
	}
}

// TestEventLogSizePastRotationCeilingWarns keeps the genuinely actionable
// signal: a log that has outgrown its own rotation policy means rotation is
// disabled, misconfigured, or stuck.
func TestEventLogSizePastRotationCeilingWarns(t *testing.T) {
	cityPath := eventsCityWithLog(t, 4096)
	c := NewEventLogSizeCheckForConfig(rotationCity(1024, nil), nil)
	r := c.Run(&CheckContext{CityPath: cityPath})
	if r.Status != StatusWarning {
		t.Fatalf("status = %v, want Warning; message %q", r.Status, r.Message)
	}
	// The hint must not ADVISE truncation. It may mention truncation to warn
	// against it, which is the point — the runtime owns this file.
	if strings.Contains(strings.ToLower(r.FixHint), "consider truncating") {
		t.Fatalf("hint must not advise hand-truncating a runtime-managed file: %q", r.FixHint)
	}
	if !strings.Contains(strings.ToLower(r.FixHint), "do not truncate") {
		t.Fatalf("hint should warn against hand-truncation, got %q", r.FixHint)
	}
	if !strings.Contains(r.FixHint, "rotation") {
		t.Fatalf("hint should point at rotation config, got %q", r.FixHint)
	}
}

// TestEventLogSizeDefaultsToRotationDefault pins the drift fix: with no
// [events.rotation] table the check must use the runtime's own default ceiling
// rather than a second independent literal.
func TestEventLogSizeDefaultsToRotationDefault(t *testing.T) {
	c := NewEventLogSizeCheckForConfig(&config.City{}, nil)
	if c.MaxSize != config.DefaultEventsRotationMaxSizeBytes {
		t.Fatalf("MaxSize = %d, want the rotation default %d",
			c.MaxSize, config.DefaultEventsRotationMaxSizeBytes)
	}
}

// TestEventLogSizeHonoursEnvOverride matches how the runtime resolves the
// ceiling: GC_EVENTS_ROTATION_MAX_SIZE_BYTES overrides config, so the check has
// to read it too or it re-creates the drift on any city that sets it.
func TestEventLogSizeHonoursEnvOverride(t *testing.T) {
	t.Setenv("GC_EVENTS_ROTATION_MAX_SIZE_BYTES", "4096")
	cityPath := eventsCityWithLog(t, 2048)
	c := NewEventLogSizeCheckForConfig(rotationCity(1024, nil), nil)
	if r := c.Run(&CheckContext{CityPath: cityPath}); r.Status != StatusOK {
		t.Fatalf("env override ignored: status = %v, message %q", r.Status, r.Message)
	}
}

// TestEventLogSizeReportsRotationDisabled: when rotation is off, nothing will
// ever bound this file, so say that instead of quoting a ceiling that no longer
// means anything.
func TestEventLogSizeReportsRotationDisabled(t *testing.T) {
	disabled := false
	cityPath := eventsCityWithLog(t, 4096)
	c := NewEventLogSizeCheckForConfig(rotationCity(1024, &disabled), nil)
	r := c.Run(&CheckContext{CityPath: cityPath})
	if r.Status != StatusWarning {
		t.Fatalf("status = %v, want Warning", r.Status)
	}
	if !strings.Contains(strings.ToLower(r.Message+r.FixHint), "disabled") {
		t.Fatalf("a disabled rotation policy should be named, got message %q hint %q", r.Message, r.FixHint)
	}
}

// TestEventLogSizeUnreadableConfigUsesDefault keeps the check useful when
// city.toml cannot be parsed: fall back to the runtime default rather than
// reverting to the stale literal or crashing.
func TestEventLogSizeUnreadableConfigUsesDefault(t *testing.T) {
	c := NewEventLogSizeCheckForConfig(nil, os.ErrInvalid)
	if c.MaxSize != config.DefaultEventsRotationMaxSizeBytes {
		t.Fatalf("MaxSize = %d, want the rotation default %d",
			c.MaxSize, config.DefaultEventsRotationMaxSizeBytes)
	}
}

// TestEventLogSizeHonoursEnabledEnvOverride closes the gap the live run
// exposed: the runtime honors GC_EVENTS_ROTATION_ENABLED, so a check that
// reads only city.toml reports a ceiling nothing is enforcing. Caught against
// the real city, not by the config-only test above.
func TestEventLogSizeHonoursEnabledEnvOverride(t *testing.T) {
	t.Setenv("GC_EVENTS_ROTATION_ENABLED", "false")
	cityPath := eventsCityWithLog(t, 4096)
	c := NewEventLogSizeCheckForConfig(rotationCity(1024, nil), nil)
	r := c.Run(&CheckContext{CityPath: cityPath})
	if !strings.Contains(strings.ToLower(r.Message+r.FixHint), "disabled") {
		t.Fatalf("env-disabled rotation should be named, got message %q hint %q", r.Message, r.FixHint)
	}
}

// TestEventLogSizeIgnoresUnparseableEnabledEnv keeps an unrecognized value from
// silently flipping the verdict, matching the runtime's parse contract.
func TestEventLogSizeIgnoresUnparseableEnabledEnv(t *testing.T) {
	t.Setenv("GC_EVENTS_ROTATION_ENABLED", "perhaps")
	c := NewEventLogSizeCheckForConfig(rotationCity(1024, nil), nil)
	if !c.rotationEnabled {
		t.Fatal("an unparseable override must leave the configured value in place")
	}
}
