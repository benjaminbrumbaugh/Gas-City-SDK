// Command gen-client generates the typed Go API client from the live
// OpenAPI spec.
//
// Pipeline:
//  1. Fetch the 3.0-downgrade spec directly from a SupervisorMux built
//     against an empty resolver. Huma v2 emits the downgrade
//     automatically; oapi-codegen v2.6.0 consumes it cleanly where it
//     chokes on 3.1. The supervisor owns every operation, so one fetch
//     yields the entire API surface — no merge step.
//  2. Restore generator-only x-go-type extensions from the canonical 3.1
//     document, because Huma's 3.0 downgrade drops schema extensions. No
//     routes or wire schemas are rewritten: the extension only binds a
//     generated model to its validated in-tree Go contract.
//  3. Write the generated client to internal/api/genclient/client_gen.go.
//
// Usage:
//
//	go run ./cmd/gen-client > internal/api/genclient/client_gen.go
//
// Or via go:generate in internal/api/genclient/doc.go. A CI drift test
// regenerates the client and diffs against the committed file so the
// spec is the source of truth.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"time"

	"github.com/gastownhall/gascity/internal/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// Step 1: fetch the 3.0-downgraded spec from the supervisor.
	sm := api.NewSupervisorMux(emptyResolver{}, nil, false, "", "", time.Time{})
	req := httptest.NewRequest(http.MethodGet, "/openapi-3.0.json", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("GET /openapi-3.0.json returned %d: %s", rec.Code, rec.Body.String())
	}

	canonicalReq := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	canonicalRec := httptest.NewRecorder()
	sm.ServeHTTP(canonicalRec, canonicalReq)
	if canonicalRec.Code != http.StatusOK {
		return fmt.Errorf("GET /openapi.json returned %d: %s", canonicalRec.Code, canonicalRec.Body.String())
	}
	spec, err := restoreGoTypeExtensions(rec.Body.Bytes(), canonicalRec.Body.Bytes())
	if err != nil {
		return fmt.Errorf("restore Go type extensions: %w", err)
	}

	// Step 2: write the generator-compatible spec to a temp file for oapi-codegen.
	tmp, err := os.CreateTemp("", "gc-openapi-3.0-*.json")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(spec); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp spec: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp spec: %w", err)
	}

	// Step 3: invoke oapi-codegen. Output goes to stdout — the caller
	// redirects it to internal/api/genclient/client_gen.go.
	cmd := exec.Command("oapi-codegen", "-generate", "types,client,skip-prune", "-package", "genclient", tmp.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("oapi-codegen: %w", err)
	}
	return nil
}

func restoreGoTypeExtensions(downgraded, canonical []byte) ([]byte, error) {
	var target, source map[string]any
	if err := json.Unmarshal(downgraded, &target); err != nil {
		return nil, fmt.Errorf("decode OpenAPI 3.0 document: %w", err)
	}
	if err := json.Unmarshal(canonical, &source); err != nil {
		return nil, fmt.Errorf("decode canonical OpenAPI document: %w", err)
	}
	targetSchemas, err := componentSchemas(target)
	if err != nil {
		return nil, fmt.Errorf("OpenAPI 3.0: %w", err)
	}
	sourceSchemas, err := componentSchemas(source)
	if err != nil {
		return nil, fmt.Errorf("canonical OpenAPI: %w", err)
	}
	for name, sourceValue := range sourceSchemas {
		sourceSchema, ok := sourceValue.(map[string]any)
		if !ok {
			continue
		}
		targetSchema, ok := targetSchemas[name].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := sourceSchema["x-go-type"]; ok {
			// oapi-codegen resolves allOf before x-go-type. The canonical Go type
			// owns validation, so remove the downgraded composition and let the
			// generator bind the component directly to that type.
			delete(targetSchema, "allOf")
		}
		for _, extension := range []string{"x-go-type", "x-go-type-import"} {
			if value, ok := sourceSchema[extension]; ok {
				targetSchema[extension] = value
			}
		}
	}
	return json.Marshal(target)
}

func componentSchemas(document map[string]any) (map[string]any, error) {
	components, ok := document["components"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("components object missing")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("components.schemas object missing")
	}
	return schemas, nil
}

// emptyResolver implements api.CityResolver with no cities. Schema
// generation is reflection-based and never calls resolver methods.
type emptyResolver struct{}

func (emptyResolver) ListCities() []api.CityInfo   { return nil }
func (emptyResolver) CityState(_ string) api.State { return nil }
