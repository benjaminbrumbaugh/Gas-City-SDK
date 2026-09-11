package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestWayfinderRecoveryAdvisorUsesRoutingV3TemplateWithoutInventingFacts(t *testing.T) {
	template := recoveryWayfinderTemplate(t, "rig/first", "rig/second")
	var got wayfinderEvaluateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/routing/v3/evaluate" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Wayfinder-Request") != "operator-v1" {
			t.Errorf("X-Wayfinder-Request = %q", r.Header.Get("X-Wayfinder-Request"))
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("advisor forwarded authorization")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = w.Write(validWayfinderResponse(t, "incident-2", "rig/second", 1700000000))
	}))
	defer server.Close()

	advisor, err := NewWayfinderRecoveryAdvisor(server.URL, template)
	if err != nil {
		t.Fatal(err)
	}
	target, err := advisor.Recommend(context.Background(), RecoveryRequest{
		CorrelationID: "incident-2",
		Now:           time.Unix(1700000000, 123).UTC(),
		Targets:       []string{"rig/second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target != "rig/second" {
		t.Fatalf("target = %q", target)
	}
	if got.CorrelationID != "incident-2" || got.NowUnix != 1700000000 {
		t.Fatalf("mutable request fields = correlation %q now %d", got.CorrelationID, got.NowUnix)
	}
	if !reflect.DeepEqual(got.Constraints.Work.AllowedTargetIDs, []string{"rig/second"}) {
		t.Fatalf("allowed_target_ids = %#v", got.Constraints.Work.AllowedTargetIDs)
	}
	if len(got.Candidates) != 1 || candidateTargetID(got.Candidates[0]) != "rig/second" {
		t.Fatalf("filtered candidates = %s", got.Candidates)
	}

	original, err := decodeWayfinderEvaluateTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Workload, original.Workload) ||
		!reflect.DeepEqual(got.Objectives, original.Objectives) ||
		!reflect.DeepEqual(got.Preferences, original.Preferences) ||
		!reflect.DeepEqual(got.ModelAssessments, original.ModelAssessments) ||
		!reflect.DeepEqual(got.Entitlements, original.Entitlements) ||
		!reflect.DeepEqual(got.Observations, original.Observations) ||
		!reflect.DeepEqual(got.Constraints.Policy, original.Constraints.Policy) ||
		!reflect.DeepEqual(got.Constraints.Work.ModelID, original.Constraints.Work.ModelID) ||
		!reflect.DeepEqual(got.Constraints.Work.TargetID, original.Constraints.Work.TargetID) ||
		got.PolicyVersion != original.PolicyVersion || got.SchemaVersion != original.SchemaVersion {
		t.Fatal("advisor changed operator-authored workload, policy, preference, evidence, or entitlement facts")
	}
}

func TestWayfinderRecoveryAdvisorFallsBackWhenNoTemplateCandidateRemains(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	advisor, err := NewWayfinderRecoveryAdvisor(server.URL, recoveryWayfinderTemplate(t, "rig/first"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = advisor.Recommend(context.Background(), RecoveryRequest{
		CorrelationID: "incident-1", Now: time.Unix(1700000000, 0), Targets: []string{"rig/second"},
	})
	if err == nil {
		t.Fatal("no matching template candidate error = nil")
	}
	if calls.Load() != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls.Load())
	}
}

func TestWayfinderRecoveryAdvisorRejectsUnselectedOrOutsideRecommendation(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "no candidate", response: `{"schema_version":"routing/v3","correlation_id":"incident-1","disposition":"no_eligible_candidate","recommendation":null}`},
		{name: "outside remaining set", response: `{"schema_version":"routing/v3","correlation_id":"incident-1","disposition":"selected","recommendation":{"execution_target":{"target_id":"rig/other"}}}`},
		{name: "wrong correlation", response: `{"schema_version":"routing/v3","correlation_id":"other","disposition":"selected","recommendation":{"execution_target":{"target_id":"rig/first"}}}`},
		{name: "invented protocol", response: `{"target":"rig/first"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tt.response)) }))
			defer server.Close()
			advisor, err := NewWayfinderRecoveryAdvisor(server.URL, recoveryWayfinderTemplate(t, "rig/first"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := advisor.Recommend(context.Background(), RecoveryRequest{
				CorrelationID: "incident-1", Now: time.Unix(1700000000, 0), Targets: []string{"rig/first"},
			}); err == nil {
				t.Fatal("invalid recommendation error = nil")
			}
		})
	}
}

func TestWayfinderRecoveryAdvisorRejectsStaleOrUnboundRecommendation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "expired", mutate: func(result map[string]any) { result["expires_at_unix"] = int64(1699999999) }},
		{name: "invalid decision", mutate: func(result map[string]any) { result["decision_id"] = "routing/v3:not-a-digest" }},
		{name: "not advisory", mutate: func(result map[string]any) { result["advisory_only"] = false }},
		{name: "candidate mismatch", mutate: func(result map[string]any) {
			result["recommendation"].(map[string]any)["candidate_id"] = "candidate-rig/other"
		}},
		{name: "config digest mismatch", mutate: func(result map[string]any) {
			result["recommendation"].(map[string]any)["execution_target"].(map[string]any)["config_digest"] = "sha256:wrong"
		}},
		{name: "adapter mismatch", mutate: func(result map[string]any) {
			result["recommendation"].(map[string]any)["execution_target"].(map[string]any)["adapter_id"] = "wrong-adapter"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validWayfinderResult("incident-1", "rig/first", 1700000000)
			tt.mutate(result)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			advisor, err := NewWayfinderRecoveryAdvisor(server.URL, recoveryWayfinderTemplate(t, "rig/first"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := advisor.Recommend(context.Background(), RecoveryRequest{
				CorrelationID: "incident-1", Now: time.Unix(1700000000, 0), Targets: []string{"rig/first"},
			}); err == nil {
				t.Fatal("untrusted recommendation error = nil")
			}
		})
	}
}

func TestWayfinderRecoveryAdvisorHonorsContextAndRejectsInvalidTemplate(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()
		advisor, err := NewWayfinderRecoveryAdvisor(server.URL, recoveryWayfinderTemplate(t, "rig/first"))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if _, err := advisor.Recommend(ctx, RecoveryRequest{CorrelationID: "incident-1", Now: time.Now(), Targets: []string{"rig/first"}}); err == nil {
			t.Fatal("timeout error = nil")
		}
	})

	for _, template := range [][]byte{
		[]byte(`{"schema_version":"routing/v3"}`),
		[]byte(`{"schema_version":"routing/v2","correlation_id":"x","workload":{},"constraints":{},"objectives":{},"preferences":{},"candidates":[],"model_assessments":[],"entitlements":[],"observations":[],"now_unix":1,"policy_version":"p"}`),
		append(recoveryWayfinderTemplate(t, "rig/first"), []byte(` {}`)...),
	} {
		if _, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", template); err == nil {
			t.Fatalf("invalid template accepted: %s", template)
		}
	}
}

func recoveryWayfinderTemplate(t *testing.T, targets ...string) []byte {
	t.Helper()
	candidates := make([]map[string]any, 0, len(targets))
	observations := make([]map[string]any, 0, len(targets))
	for i, target := range targets {
		candidates = append(candidates, map[string]any{
			"candidate_id": "candidate-" + target,
			"model":        map[string]any{"canonical_model": "operator/model", "serve_as": "operator-model"},
			"execution_target": map[string]any{
				"target_id": target, "provider_id": "operator-provider", "account_ref": "operator-account",
				"config_digest": "sha256:config-" + target, "adapter_id": "operator-adapter", "adapter_digest": "sha256:adapter",
			},
			"economics":            map[string]any{"pricing_model": "operator-authored"},
			"model_assessment_ref": "assessment-1", "entitlement_ref": "entitlement-1", "observation_ref": "observation-" + target,
		})
		observations = append(observations, map[string]any{"record_id": "observation-" + target, "target_id": target, "provenance": "operator-observation-" + string(rune('a'+i))})
	}
	packet := map[string]any{
		"schema_version": "routing/v3", "correlation_id": "operator-placeholder",
		"workload": map[string]any{"profile_source": "caller", "operation": "recover", "operator_fact": "preserve"},
		"constraints": map[string]any{
			"policy": map[string]any{"data_handling": map[string]any{"remote_disclosure": "prohibited"}},
			"work":   map[string]any{"allowed_target_ids": targets, "model_id": nil, "target_id": nil},
		},
		"objectives":        map[string]any{"quality": 100, "latency": 0, "cost": 0},
		"preferences":       map[string]any{"preferred_provider_id": "operator-provider", "preferred_model_id": nil},
		"candidates":        candidates,
		"model_assessments": []map[string]any{{"record_id": "assessment-1", "provenance": "operator-assessment"}},
		"entitlements":      []map[string]any{{"record_id": "entitlement-1", "enabled": true, "provenance": "operator-entitlement"}},
		"observations":      observations,
		"now_unix":          int64(1), "policy_version": "operator-policy/v3",
	}
	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func validWayfinderResponse(t *testing.T, correlationID, target string, nowUnix int64) []byte {
	t.Helper()
	encoded, err := json.Marshal(validWayfinderResult(correlationID, target, nowUnix))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func validWayfinderResult(correlationID, target string, nowUnix int64) map[string]any {
	return map[string]any{
		"schema_version": "routing/v3", "correlation_id": correlationID,
		"decision_id": "routing/v3:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"request":     map[string]any{}, "disposition": "selected", "candidates": []any{}, "fingerprints": map[string]any{},
		"policy_version": "operator-policy/v3", "issued_at_unix": nowUnix, "expires_at_unix": nowUnix + 60,
		"advisory_only": true, "no_active_migration": true, "alternatives_advisory_only": true, "reevaluation": map[string]any{},
		"recommendation": map[string]any{
			"candidate_id": "candidate-" + target,
			"model":        map[string]any{"canonical_model": "operator/model", "serve_as": "operator-model", "reasoning_effort": "high"},
			"execution_target": map[string]any{
				"target_id": target, "config_digest": "sha256:config-" + target,
				"adapter_id": "operator-adapter", "adapter_digest": "sha256:adapter",
			},
			"billing_mode": "subscription", "provider_preference_matched": false, "model_preference_matched": false,
			"rank_components": map[string]any{},
		},
	}
}
