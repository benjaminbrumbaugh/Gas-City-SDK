package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

func TestCityRuntimeRecoveryResponderUsesOrdinaryWorkRoute(t *testing.T) {
	store := recoveryControllerStore()
	cr := &CityRuntime{
		cfg: &config.City{RecoveryResponder: &config.RecoveryResponderConfig{
			Targets: []string{"rig/responder"}, Hold: "10m", Cooldown: "1m", MaxAttempts: 1,
		}},
		standaloneCityStore: store,
		stderr:              io.Discard,
	}

	fields := cr.reconcileRecoveryResponder(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if fields["created"] != 1 {
		t.Fatalf("fields = %#v", fields)
	}
	works, err := store.List(beads.ListQuery{Label: worker.RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || works[0].Metadata[beadmeta.RoutedToMetadataKey] != "rig/responder" || works[0].Type != "task" {
		t.Fatalf("ordinary recovery work = %+v", works)
	}
}

func TestCityRuntimeRecoveryResponderNoConfigIsNoOp(t *testing.T) {
	store := beads.NewMemStore()
	cr := &CityRuntime{cfg: &config.City{}, standaloneCityStore: store, stderr: io.Discard}
	if fields := cr.reconcileRecoveryResponder(context.Background(), time.Now()); fields != nil {
		t.Fatalf("fields = %#v, want nil", fields)
	}
	works, err := store.List(beads.ListQuery{AllowScan: true, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 0 {
		t.Fatalf("disabled recovery created work: %+v", works)
	}
}

func TestCityRuntimeRecoveryResponderLoadsContainedWayfinderTemplate(t *testing.T) {
	cityPath := t.TempDir()
	templatePath := filepath.Join(cityPath, ".gc", "wayfinder-recovery.json")
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	template := `{"schema_version":"routing/v3","correlation_id":"placeholder","workload":{"operator":"fact"},"constraints":{"policy":{"operator":"policy"},"work":{"allowed_target_ids":["rig/responder"],"model_id":null,"target_id":null}},"objectives":{"quality":100,"latency":0,"cost":0},"preferences":{"preferred_provider_id":null,"preferred_model_id":null},"candidates":[{"candidate_id":"candidate","model":{"operator":"model"},"execution_target":{"target_id":"rig/responder","config_digest":"sha256:config","adapter_id":"adapter","adapter_digest":"sha256:adapter"},"economics":{"operator":"economics"},"model_assessment_ref":"assessment","entitlement_ref":"entitlement","observation_ref":"observation"}],"model_assessments":[{"record_id":"assessment","provenance":"operator"}],"entitlements":[{"record_id":"entitlement","provenance":"operator"}],"observations":[{"record_id":"observation","target_id":"rig/responder","provenance":"operator"}],"now_unix":1,"policy_version":"operator/v3"}`
	if err := os.WriteFile(templatePath, []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/routing/v3/evaluate" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var request struct {
			CorrelationID string `json:"correlation_id"`
			NowUnix       int64  `json:"now_unix"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		_, _ = fmt.Fprintf(w, `{"schema_version":"routing/v3","correlation_id":%q,"decision_id":"routing/v3:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","request":{},"disposition":"selected","recommendation":{"candidate_id":"candidate","model":{},"execution_target":{"target_id":"rig/responder","config_digest":"sha256:config","adapter_id":"adapter","adapter_digest":"sha256:adapter"},"billing_mode":"subscription","provider_preference_matched":false,"model_preference_matched":false,"rank_components":{}},"candidates":[],"fingerprints":{},"policy_version":"operator/v3","issued_at_unix":%d,"expires_at_unix":%d,"advisory_only":true,"no_active_migration":true,"alternatives_advisory_only":true,"reevaluation":{}}`, request.CorrelationID, request.NowUnix, request.NowUnix+60)
	}))
	defer server.Close()
	store := recoveryControllerStore()
	var stderr bytes.Buffer
	cr := &CityRuntime{
		cityPath: cityPath,
		cfg: &config.City{RecoveryResponder: &config.RecoveryResponderConfig{
			Targets: []string{"rig/fallback", "rig/responder"}, WayfinderURL: server.URL,
			WayfinderRequestFile: ".gc/wayfinder-recovery.json", MaxAttempts: 1,
		}},
		standaloneCityStore: store, stderr: &stderr,
	}
	fields := cr.reconcileRecoveryResponder(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if fields["created"] != 1 || stderr.Len() != 0 || calls.Load() != 1 {
		t.Fatalf("fields=%#v calls=%d stderr=%q", fields, calls.Load(), stderr.String())
	}
	works, err := store.List(beads.ListQuery{Label: worker.RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || works[0].Metadata[beadmeta.RoutedToMetadataKey] != "rig/responder" {
		t.Fatalf("Wayfinder recommendation was not used: %+v", works)
	}
}

func TestCityRuntimeRecoveryResponderRejectsTemplateTraversalAndFallsBack(t *testing.T) {
	cityPath := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cityPath, outside)
	if err != nil {
		t.Fatal(err)
	}
	store := recoveryControllerStore()
	var stderr bytes.Buffer
	cr := &CityRuntime{
		cityPath: cityPath,
		cfg: &config.City{RecoveryResponder: &config.RecoveryResponderConfig{
			Targets: []string{"rig/responder"}, WayfinderURL: "http://127.0.0.1:1",
			WayfinderRequestFile: rel, MaxAttempts: 1,
		}},
		standaloneCityStore: store, stderr: &stderr,
	}
	fields := cr.reconcileRecoveryResponder(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if fields["created"] != 1 {
		t.Fatalf("fallback fields = %#v", fields)
	}
	if !strings.Contains(stderr.String(), "outside city root") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	works, err := store.List(beads.ListQuery{Label: worker.RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || works[0].Metadata[beadmeta.RoutedToMetadataKey] != "rig/responder" {
		t.Fatalf("fallback work = %+v", works)
	}
}

func TestRecoveryWayfinderRequestPathRejectsSymlinkEscape(t *testing.T) {
	cityPath := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cityPath, "request.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := recoveryWayfinderRequestPath(cityPath, "request.json"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func recoveryControllerStore() *beads.MemStore {
	return beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "session-1", Type: session.BeadType, Status: "open", Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"state": "asleep", "session_health": "unhealthy", "session_health_reason": "quota_exceeded",
			"provider_terminal_error": "quota_exceeded", "provider": "provider-a",
		},
	}}, nil)
}
