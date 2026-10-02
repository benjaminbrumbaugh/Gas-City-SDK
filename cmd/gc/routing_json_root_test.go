package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/clientcontext"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// Exercise the built executable, not Cobra's subtree or a replaced routing hook.
func TestRoutingJSONBuiltRootCapability(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "gc")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	home := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GC_HOME=" + home, "TMPDIR=" + os.TempDir()}
	for _, name := range []string{"status", "targets", "eligible", "decisions", "outcomes", "ingest"} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(binary, "routing", name, "--json-schema")
			command.Env = env
			out, err := command.CombinedOutput()
			var manifest jsonSchemaManifest
			if err != nil || json.Unmarshal(out, &manifest) != nil || !manifest.JSONSupported || len(manifest.Schemas["result"]) == 0 || len(manifest.Schemas["failure"]) == 0 {
				t.Fatalf("root schema unavailable: %v %s", err, out)
			}
			command = exec.Command(binary, "routing", name, "--json")
			command.Env = env
			out, _ = command.CombinedOutput()
			if strings.Contains(string(out), "json_unsupported") {
				t.Fatalf("root rejects JSON: %s", out)
			}
		})
	}
	// The City consumes this typed API page verbatim; a CLI-added ok field is
	// enough to break its exact-byte outbox/replay contract.
	page := routingdecision.ProducerExecutionOutcomePage{SchemaVersion: "routing/outcome/v3", Items: []routingdecision.ProducerExecutionOutcome{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/city/fixture/routing/status":
			if err := json.NewEncoder(w).Encode(routingdecision.LiveStatus{Schema: 1, ExecutionEnabled: true}); err != nil {
				t.Error(err)
			}
		case "/v0/city/fixture/routing/outcomes-v3":
			if err := json.NewEncoder(w).Encode(page); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	contexts := clientcontext.File{Contexts: []clientcontext.Context{{Name: "fixture", URL: server.URL, City: "fixture"}}}
	if err := contexts.Save(filepath.Join(home, "contexts.toml")); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "routing", "outcomes", "--json")
	command.Env = append(append([]string(nil), env...), "GC_CITY_CONTEXT=fixture")
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	out, err := command.Output()
	if err != nil {
		t.Fatalf("real root read: %v %s %s", err, out, diagnostic.String())
	}
	want, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(out), want) {
		t.Fatalf("root changed typed producer page: got %s want %s", out, want)
	}
	validateJSONAgainstResultSchema(t, []string{"routing", "outcomes"}, out)
}
