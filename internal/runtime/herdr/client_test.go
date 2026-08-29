package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// socketPath must resolve the SAME directory herdr itself uses for its
// config/socket state, or this client dials a path no herdr server ever
// binds: serverAlive() then always reads false against a healthy server
// ("did not become ready"), and every retry launches a redundant herdr
// server contending for the same pane ("agent_pane_busy") — ga-nqlb8q.
// Herdr's config::io::config_dir uses presence-sensitive std::env::var
// lookups. Empty and whitespace values remain paths; only absent variables
// advance to the next fallback.

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}

func TestSocketPathHonorsXDGConfigHomeOverHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir()) // deliberately different; must be ignored

	c := newClient("xdgtest", "")
	if got, want := c.socketPath(), filepath.Join(xdg, "herdr", "sessions", "xdgtest", "herdr.sock"); got != want {
		t.Errorf("socketPath() = %q; want %q", got, want)
	}

	c.session = "default"
	if got, want := c.socketPath(), filepath.Join(xdg, "herdr", "herdr.sock"); got != want {
		t.Errorf("socketPath() (default session) = %q; want %q", got, want)
	}
}

func TestSocketPathPreservesPresentEmptyXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", t.TempDir())

	c := newClient("emptyxdg", "")
	if got, want := c.socketPath(), filepath.Join("herdr", "sessions", "emptyxdg", "herdr.sock"); got != want {
		t.Errorf("socketPath() = %q; want %q", got, want)
	}
}

func TestSocketPathPreservesPresentWhitespaceXDGConfigHome(t *testing.T) {
	const xdg = " 	 "
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir())

	c := newClient("whitespacexdg", "")
	if got, want := c.socketPath(), filepath.Join(xdg, "herdr", "sessions", "whitespacexdg", "herdr.sock"); got != want {
		t.Errorf("socketPath() = %q; want %q", got, want)
	}
}

func TestSocketPathFallsBackToPresentHomeWhenXDGAbsent(t *testing.T) {
	unsetEnv(t, "XDG_CONFIG_HOME")
	unsetEnv(t, "APPDATA")
	unsetEnv(t, "USERPROFILE")
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := newClient("hometest", "")
	if got, want := c.socketPath(), filepath.Join(home, ".config", "herdr", "sessions", "hometest", "herdr.sock"); got != want {
		t.Errorf("socketPath() = %q; want %q", got, want)
	}
}

func TestSocketPathFallsBackToTempWhenConfigEnvironmentAbsent(t *testing.T) {
	for _, key := range []string{"XDG_CONFIG_HOME", "APPDATA", "USERPROFILE", "HOME"} {
		unsetEnv(t, key)
	}

	c := newClient("temptest", "")
	if got, want := c.socketPath(), filepath.Join(os.TempDir(), "herdr", "sessions", "temptest", "herdr.sock"); got != want {
		t.Errorf("socketPath() = %q; want %q", got, want)
	}
}

func TestServerEnvironmentAnchorsRelativeConfigBeforeChangingWorkingDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	c := newClient("emptyxdg", t.TempDir())

	env, err := c.serverEnvironment()
	if err != nil {
		t.Fatalf("serverEnvironment: %v", err)
	}
	want, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for _, entry := range env {
		if got, ok := strings.CutPrefix(entry, "XDG_CONFIG_HOME="); ok {
			if got != want {
				t.Fatalf("server XDG_CONFIG_HOME = %q; want %q", got, want)
			}
			return
		}
	}
	t.Fatal("server environment does not normalize relative XDG_CONFIG_HOME")
}
