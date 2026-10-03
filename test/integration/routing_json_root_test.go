//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/gastownhall/gascity/internal/clientcontext"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// Exercise the built executable, not Cobra's subtree or a replaced routing hook.
func TestRoutingJSONBuiltRootCapability(t *testing.T) {
	if os.Getenv(integrationGCBinaryEnv) != "" {
		t.Fatal("built-root proof requires this SDK's freshly built binary, not GC_INTEGRATION_GC_BINARY")
	}
	// TestMain builds this SDK once into its exclusive owned run root.
	binary := gcBinary
	home := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GC_HOME=" + home, "TMPDIR=" + os.TempDir()}
	for _, name := range []string{"status", "targets", "eligible", "decisions", "outcomes", "ingest"} {
		t.Run(name, func(t *testing.T) {
			out, err := runCommand("", env, 30*time.Second, binary, "routing", name, "--json-schema")
			var manifest struct {
				JSONSupported bool                       `json:"json_supported"`
				Schemas       map[string]json.RawMessage `json:"schemas"`
			}
			if err != nil || json.Unmarshal([]byte(out), &manifest) != nil || !manifest.JSONSupported || len(manifest.Schemas["result"]) == 0 || len(manifest.Schemas["failure"]) == 0 {
				t.Fatalf("root schema unavailable: %v %s", err, out)
			}
			out, _ = runCommand("", env, 30*time.Second, binary, "routing", name, "--json")
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
	rootEnv := append(append([]string(nil), env...), "GC_CITY_CONTEXT=fixture")
	out, err := runCommandStdout("", rootEnv, 30*time.Second, binary, "routing", "outcomes", "--json")
	if err != nil {
		t.Fatalf("real root read: %v %s", err, out)
	}
	want, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace([]byte(out)), want) {
		t.Fatalf("root changed typed producer page: got %s want %s", out, want)
	}
	// Validate against the schema emitted by this exact built executable, not
	// a duplicate fixture schema or a Cobra subtree.
	schemaOut, err := runCommandStdout("", env, 30*time.Second, binary, "routing", "outcomes", "--json-schema=result")
	if err != nil {
		t.Fatalf("root result schema: %v %s", err, schemaOut)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaOut))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("gc://schemas/routing/outcomes/result", schemaDoc); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("gc://schemas/routing/outcomes/result")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(instance); err != nil {
		t.Fatalf("built root payload violates its schema: %v", err)
	}
}
