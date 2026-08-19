package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubDashboardOpen replaces the browser-open hook with a recorder for the
// duration of the test and returns a pointer to the opened URL ("" if never
// called).
func stubDashboardOpen(t *testing.T) *string {
	t.Helper()
	var opened string
	old := openDashboardURLHook
	openDashboardURLHook = func(rawURL string) error {
		opened = rawURL
		return nil
	}
	t.Cleanup(func() { openDashboardURLHook = old })
	return &opened
}

func stubDashboardSPARootAvailable(t *testing.T) {
	t.Helper()
	old := dashboardSPARootAvailableHook
	dashboardSPARootAvailableHook = func(string) bool { return true }
	t.Cleanup(func() { dashboardSPARootAvailableHook = old })
}

func TestDashboardSPARootAvailableRequiresDirectHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/html/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
		case "/json/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
		case "/redirect/":
			http.Redirect(w, r, "/html/", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{name: "html", path: "/html", want: true},
		{name: "json", path: "/json", want: false},
		{name: "redirect", path: "/redirect", want: false},
		{name: "missing", path: "/missing", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dashboardSPARootAvailable(server.URL + tc.path); got != tc.want {
				t.Fatalf("dashboardSPARootAvailable(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestRunDashboardNoticeSPADisabledDoesNotAdvertiseURL(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_SUPERVISOR_DASHBOARD", "")
	t.Setenv("GC_SUPERVISOR_DASHBOARD_SPA", "0")
	t.Chdir(t.TempDir())
	opened := stubDashboardOpen(t)

	var stdout bytes.Buffer
	if err := runDashboardNotice("", false, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error: %v", err)
	}
	if *opened != "" {
		t.Fatalf("opened URL = %q, want no browser launch with SPA disabled", *opened)
	}
	if strings.Contains(stdout.String(), "http://") || strings.Contains(stdout.String(), "https://") {
		t.Fatalf("SPA-disabled notice advertised a dead URL: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "GC_SUPERVISOR_DASHBOARD_SPA=1") {
		t.Fatalf("SPA-disabled notice = %q, want manual-enable guidance", stdout.String())
	}
}

func TestRunDashboardNoticeRefusesLiveAPIWithoutSPARoot(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_SUPERVISOR_DASHBOARD", "")
	t.Setenv("GC_SUPERVISOR_DASHBOARD_SPA", "")
	t.Chdir(t.TempDir())
	opened := stubDashboardOpen(t)

	apiOnly := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(apiOnly.Close)

	var stdout bytes.Buffer
	if err := runDashboardNotice(apiOnly.URL, false, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error: %v", err)
	}
	if *opened != "" {
		t.Fatalf("opened URL = %q, want no browser launch when root is unavailable", *opened)
	}
	if strings.Contains(stdout.String(), apiOnly.URL) {
		t.Fatalf("notice advertised unavailable SPA root: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "GC_SUPERVISOR_DASHBOARD_SPA=1") {
		t.Fatalf("notice = %q, want SPA-unavailable guidance", stdout.String())
	}
}

func TestRunDashboardNoticePrintsSupervisorURL(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Chdir(t.TempDir())
	stubDashboardOpen(t)
	stubDashboardSPARootAvailable(t)

	oldAlive := supervisorAliveHook
	oldCityFlag := cityFlag
	oldRigFlag := rigFlag
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		cityFlag = oldCityFlag
		rigFlag = oldRigFlag
	})

	supervisorAliveHook = func() int { return 4242 }
	cityFlag = ""
	rigFlag = ""

	var stdout bytes.Buffer
	if err := runDashboardNotice("", true, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error: %v", err)
	}

	wantURL, err := supervisorAPIBaseURL()
	if err != nil {
		t.Fatalf("supervisorAPIBaseURL(): %v", err)
	}
	wantURL = strings.TrimRight(wantURL, "/")
	if !strings.Contains(stdout.String(), wantURL) {
		t.Fatalf("notice = %q, want it to include supervisor URL %q", stdout.String(), wantURL)
	}
}

// TestRunDashboardNoticeOpensBrowserWhenServed pins that, when the URL resolves
// and --no-open is not set, the command opens the resolved URL in the browser
// and still prints it.
func TestRunDashboardNoticeOpensBrowserWhenServed(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Chdir(t.TempDir())
	opened := stubDashboardOpen(t)
	stubDashboardSPARootAvailable(t)

	oldAlive := supervisorAliveHook
	oldCityFlag := cityFlag
	oldRigFlag := rigFlag
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		cityFlag = oldCityFlag
		rigFlag = oldRigFlag
	})

	supervisorAliveHook = func() int { return 4242 }
	cityFlag = ""
	rigFlag = ""

	var stdout bytes.Buffer
	if err := runDashboardNotice("", false, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error: %v", err)
	}

	wantURL, err := supervisorAPIBaseURL()
	if err != nil {
		t.Fatalf("supervisorAPIBaseURL(): %v", err)
	}
	wantURL = strings.TrimRight(wantURL, "/")
	if *opened != wantURL {
		t.Fatalf("opened URL = %q, want %q", *opened, wantURL)
	}
	if !strings.Contains(stdout.String(), wantURL) {
		t.Fatalf("notice = %q, want it to still print the URL %q", stdout.String(), wantURL)
	}
}

// TestRunDashboardNoticeNoOpenSkipsBrowser pins that --no-open (noOpen=true)
// prints the URL without launching a browser.
func TestRunDashboardNoticeNoOpenSkipsBrowser(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Chdir(t.TempDir())
	opened := stubDashboardOpen(t)
	stubDashboardSPARootAvailable(t)

	oldAlive := supervisorAliveHook
	oldCityFlag := cityFlag
	oldRigFlag := rigFlag
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		cityFlag = oldCityFlag
		rigFlag = oldRigFlag
	})

	supervisorAliveHook = func() int { return 4242 }
	cityFlag = ""
	rigFlag = ""

	var stdout bytes.Buffer
	if err := runDashboardNotice("", true, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error: %v", err)
	}
	if *opened != "" {
		t.Fatalf("opened URL = %q, want no browser launch under --no-open", *opened)
	}
}

// TestRunDashboardNoticeOpenFailureFallsBackToPrint pins that a browser-launch
// failure does not error the command — it falls back to printing the URL.
func TestRunDashboardNoticeOpenFailureFallsBackToPrint(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Chdir(t.TempDir())
	stubDashboardSPARootAvailable(t)

	old := openDashboardURLHook
	openDashboardURLHook = func(string) error { return io.ErrClosedPipe }
	t.Cleanup(func() { openDashboardURLHook = old })

	oldAlive := supervisorAliveHook
	oldCityFlag := cityFlag
	oldRigFlag := rigFlag
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		cityFlag = oldCityFlag
		rigFlag = oldRigFlag
	})

	supervisorAliveHook = func() int { return 4242 }
	cityFlag = ""
	rigFlag = ""

	var stdout bytes.Buffer
	if err := runDashboardNotice("", false, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error = %v, want nil (open failure must not error)", err)
	}

	wantURL, err := supervisorAPIBaseURL()
	if err != nil {
		t.Fatalf("supervisorAPIBaseURL(): %v", err)
	}
	if !strings.Contains(stdout.String(), strings.TrimRight(wantURL, "/")) {
		t.Fatalf("notice = %q, want it to fall back to printing the URL", stdout.String())
	}
}

func TestRunDashboardNoticeUsesAPIOverride(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Chdir(t.TempDir())
	stubDashboardSPARootAvailable(t)

	oldAlive := supervisorAliveHook
	oldCityFlag := cityFlag
	oldRigFlag := rigFlag
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		cityFlag = oldCityFlag
		rigFlag = oldRigFlag
	})

	supervisorAliveHook = func() int { return 0 }
	cityFlag = ""
	rigFlag = ""

	var stdout bytes.Buffer
	if err := runDashboardNotice("http://127.0.0.1:9999/", true, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error: %v", err)
	}
	if !strings.Contains(stdout.String(), "http://127.0.0.1:9999") {
		t.Fatalf("notice = %q, want trimmed override URL", stdout.String())
	}
	if strings.Contains(stdout.String(), "http://127.0.0.1:9999/") {
		t.Fatalf("notice = %q, want trailing slash trimmed", stdout.String())
	}
}

// TestRunDashboardNoticeHintsStartWhenUnresolvable pins that, when neither a
// supervisor nor a standalone-controller API can be resolved, the command
// prints how to start the supervisor and still exits 0 (returns nil).
func TestRunDashboardNoticeHintsStartWhenUnresolvable(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Chdir(t.TempDir())

	oldAlive := supervisorAliveHook
	oldCityFlag := cityFlag
	oldRigFlag := rigFlag
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		cityFlag = oldCityFlag
		rigFlag = oldRigFlag
	})

	supervisorAliveHook = func() int { return 0 }
	cityFlag = ""
	rigFlag = ""

	var stdout bytes.Buffer
	if err := runDashboardNotice("", true, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error = %v, want nil (informational command exits 0)", err)
	}
	if !strings.Contains(stdout.String(), "gc supervisor start") {
		t.Fatalf("notice = %q, want it to include the start hint %q", stdout.String(), "gc supervisor start")
	}
}

// TestRunDashboardNoticeResilientToBadCityConfig pins that a city/config
// resolution failure (here: a city dir with no readable city.toml) does NOT
// abort the informational command — it degrades to supervisor discovery and
// still prints the supervisor URL. Regression guard for the shim hard-failing
// with "city.toml: no such file" instead of reporting where the SPA is served.
func TestRunDashboardNoticeResilientToBadCityConfig(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	stubDashboardSPARootAvailable(t)

	badCity := filepath.Join(t.TempDir(), "broken")
	if err := os.MkdirAll(badCity, 0o755); err != nil {
		t.Fatalf("mkdir city dir: %v", err)
	}
	// Intentionally no city.toml: loadCityConfig fails for this dir.
	t.Chdir(t.TempDir())

	oldAlive := supervisorAliveHook
	oldCityFlag := cityFlag
	oldRigFlag := rigFlag
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		cityFlag = oldCityFlag
		rigFlag = oldRigFlag
	})

	supervisorAliveHook = func() int { return 4242 }
	cityFlag = badCity
	rigFlag = ""

	var stdout bytes.Buffer
	if err := runDashboardNotice("", true, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error = %v, want nil (must degrade past a bad city config)", err)
	}
	wantURL, err := supervisorAPIBaseURL()
	if err != nil {
		t.Fatalf("supervisorAPIBaseURL(): %v", err)
	}
	if !strings.Contains(stdout.String(), strings.TrimRight(wantURL, "/")) {
		t.Fatalf("notice = %q, want supervisor URL despite the unreadable city config", stdout.String())
	}
}

// TestRunDashboardNoticeUsesStandaloneControllerAPI pins that the standalone
// controller's API (cfg.API.Port) is reported as the dashboard URL when no
// machine-wide supervisor is running.
func TestRunDashboardNoticeUsesStandaloneControllerAPI(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	stubDashboardSPARootAvailable(t)

	cityDir := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(cityDir, 0o755); err != nil {
		t.Fatalf("mkdir city dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte(`
[workspace]
name = "alpha"
provider = "claude"

[providers.claude]
base = "builtin:claude"

[api]
port = 9123
`), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}

	t.Chdir(t.TempDir())

	oldAlive := supervisorAliveHook
	oldCityFlag := cityFlag
	oldRigFlag := rigFlag
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		cityFlag = oldCityFlag
		rigFlag = oldRigFlag
	})

	supervisorAliveHook = func() int { return 0 }
	cityFlag = cityDir
	rigFlag = ""

	var stdout bytes.Buffer
	if err := runDashboardNotice("", true, &stdout, io.Discard); err != nil {
		t.Fatalf("runDashboardNotice() error = %v, want nil (standalone-controller API is supported)", err)
	}
	if !strings.Contains(stdout.String(), ":9123") {
		t.Fatalf("notice = %q, want it to include the configured standalone port :9123", stdout.String())
	}
}
