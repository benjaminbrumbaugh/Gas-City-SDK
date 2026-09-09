package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/launchorigin"
)

// The API server's own environment identifies the server process, not the
// actor that called it. A sling over the control plane therefore has no
// trustworthy actor route, and its auto-convoy must keep the legacy shape
// rather than being attributed to whoever started the server.
func TestSlingOverTheAPICapturesNoLaunchOriginFromTheServerEnvironment(t *testing.T) {
	for _, key := range launchorigin.RouteKeys() {
		t.Setenv(key, "server-process-"+key)
	}

	h, state := newSlingTestServer(t)
	store := state.stores["myrig"]
	b, err := store.Create(beads.Bead{Title: "test task", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}

	body := `{"target":"myrig/worker","bead":"` + b.ID + `"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newPostRequest(cityURL(state, "/sling"), strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	convoys, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(convoys) == 0 {
		t.Fatal("no auto-convoy was created; the test proves nothing about its launch origin")
	}
	for _, convoy := range convoys {
		if got, ok := convoy.Metadata[convoycore.LaunchOriginMetadataKey]; ok {
			t.Fatalf("convoy %s carries %s = %q; the server's environment is not the caller's identity",
				convoy.ID, convoycore.LaunchOriginMetadataKey, got)
		}
	}
}

// A caller that has already captured an opaque route identity may carry it over
// the typed API request. The server must persist that value exactly as a
// normalized origin while remaining independent of its own environment.
func TestSlingOverTheAPIPersistsCallerLaunchOrigin(t *testing.T) {
	for _, key := range launchorigin.RouteKeys() {
		t.Setenv(key, "")
	}

	h, state := newSlingTestServer(t)
	store := state.stores["myrig"]
	b, err := store.Create(beads.Bead{Title: "test task", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}

	body := `{"target":"myrig/worker","bead":"` + b.ID + `","launch_origin":"  remote-actor  "}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newPostRequest(cityURL(state, "/sling"), strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	convoys, err := store.List(beads.ListQuery{Type: "convoy"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(convoys) == 0 {
		t.Fatal("no auto-convoy was created")
	}
	for _, convoy := range convoys {
		if got := convoycore.GetConvoyFields(convoy).LaunchOrigin; got != "remote-actor" {
			t.Fatalf("convoy %s LaunchOrigin = %q, want normalized caller origin", convoy.ID, got)
		}
	}
}

// A guard for the rule above: capturing origin from the server process would
// stamp every API sling with the server's identity. The API must receive an
// actor identity from a caller-authenticated route or capture nothing at all.
func TestAPINonTestFilesDoNotCaptureLaunchOriginFromTheServerEnvironment(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(currentFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	forbidden := []string{"launchorigin.Capture(", "launchorigin.RouteKeys("}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", name, err)
		}
		for _, needle := range forbidden {
			if strings.Contains(string(data), needle) {
				t.Errorf("%s calls %s; the API server's environment is the server's identity, not the caller's", name, needle)
			}
		}
	}
}
