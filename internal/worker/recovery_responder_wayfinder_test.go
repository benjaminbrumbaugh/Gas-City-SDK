package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWayfinderFingerprintsMatchPublishedRoutingV3Golden(t *testing.T) {
	template, err := os.ReadFile("../../cmd/gc/testdata/recovery_wayfinder_request.json")
	if err != nil {
		t.Fatal(err)
	}
	request, err := decodeWayfinderEvaluateTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	canonicalizeWayfinderRequest(&request)
	want := wayfinderFingerprints{
		Request:         "sha256:c7d0999e640bb12d9cd0ea3e52592a149111f01702eab5447632f15a8d71e586",
		Inventory:       "sha256:0c30eb45f40581e221188df04c7d532961e5504623e4392a18fb7760fb2e2bb8",
		ModelAssessment: "sha256:f63fbfc6a1e858704e12a54ce386e0e9755118df62940e793a361801ebcbe14f",
		AccountScope:    "sha256:be2be160af1c6eaf3a7cd554fd4c58c9f4db61c3fbb0a5e23a0d7cc9465dafd4",
		Evidence:        "sha256:4689f322336a7e75d6c2e232585606f3882b1f82e9b20fb2e36a1bee8e647cc1",
		Policy:          "sha256:b6a4d9701edcb52c3f5a836d8c1f4216bc2409a0be4014d57909632923971f40",
		Clock:           "sha256:c1a1e119e56d6b9c025e9279dd1b243adacc69a2ea28bf75ee90c80d1e351b5b",
	}
	if got := wayfinderRequestFingerprints(request); !reflect.DeepEqual(got, want) {
		t.Fatalf("fingerprints = %+v, want published routing/v3 golden %+v", got, want)
	}
	if got := wayfinderDecisionID(want); got != "routing/v3:21d21557a54b6beeaa63b0cdd6772c601c760355950cd43cbece98bab6208de7" {
		t.Fatalf("decision id = %q", got)
	}
}

func TestWayfinderRecoveryAdvisorSubmitsCanonicalBoundRoutingV3Request(t *testing.T) {
	template := recoveryWayfinderTemplate(t, "rig/second", "rig/first")
	var gotBody []byte
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", template)
	if err != nil {
		t.Fatal(err)
	}
	advisor.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != wayfinderEvaluatePath || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("content negotiation = %q / %q", r.Header.Get("Content-Type"), r.Header.Get("Accept"))
		}
		if r.Header.Get("X-Wayfinder-Request") != wayfinderRequestHeaderValue {
			t.Errorf("X-Wayfinder-Request = %q", r.Header.Get("X-Wayfinder-Request"))
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("advisor forwarded authorization")
		}
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var got wayfinderEvaluateRequest
		if err := decodeSingleJSON(gotBody, &got); err != nil {
			t.Error(err)
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(validWayfinderResponse(t, got, "rig/second"))),
		}, nil
	})
	advisor.now = func() time.Time { return time.Unix(150, 0) }
	target, err := advisor.Recommend(context.Background(), RecoveryRequest{
		CorrelationID: "incident-2",
		Now:           time.Unix(150, 123).UTC(),
		Targets:       []string{"rig/second", "rig/first"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target != "rig/second" {
		t.Fatalf("target = %q", target)
	}

	want, err := decodeWayfinderEvaluateTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	want.CorrelationID = "incident-2"
	want.NowUnix = 150
	want.Constraints.Work.AllowedTargetIDs = []string{"rig/second", "rig/first"}
	canonicalizeWayfinderRequest(&want)
	wantBody, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBody, wantBody) {
		t.Fatalf("submitted request is not canonical\n got: %s\nwant: %s", gotBody, wantBody)
	}
	if gotBody[len(gotBody)-1] == '\n' {
		t.Fatal("canonical request unexpectedly has trailing whitespace")
	}
}

func TestWayfinderRecoveryAdvisorNarrowsCandidatesTargetsAndEvidenceTogether(t *testing.T) {
	var template wayfinderEvaluateRequest
	if err := json.Unmarshal(recoveryWayfinderTemplate(t, "rig/first", "rig/second"), &template); err != nil {
		t.Fatal(err)
	}
	secondAssessment := template.ModelAssessments[0]
	secondAssessment.RecordID = "assessment-2"
	template.ModelAssessments = append(template.ModelAssessments, secondAssessment)
	template.Candidates[1].ModelAssessmentRef = stringPointer(secondAssessment.RecordID)
	encodedTemplate, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", encodedTemplate)
	if err != nil {
		t.Fatal(err)
	}
	advisor.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var got wayfinderEvaluateRequest
		if err := decodeSingleJSON(body, &got); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(got.Constraints.Work.AllowedTargetIDs, []string{"rig/first"}) {
			t.Errorf("allowed targets = %v, want only surviving candidate target", got.Constraints.Work.AllowedTargetIDs)
		}
		if len(got.Candidates) != 1 || got.Candidates[0].ExecutionTarget.TargetID != "rig/first" {
			t.Errorf("candidates = %+v, want only rig/first", got.Candidates)
		}
		if len(got.ModelAssessments) != 1 || got.ModelAssessments[0].RecordID != "assessment-1" {
			t.Errorf("model assessments = %+v, want only selected candidate evidence", got.ModelAssessments)
		}
		if len(got.Entitlements) != 1 || got.Entitlements[0].RecordID != "entitlement-a" {
			t.Errorf("entitlements = %+v, want only selected candidate evidence", got.Entitlements)
		}
		if len(got.Observations) != 1 || got.Observations[0].RecordID != "observation-a" {
			t.Errorf("observations = %+v, want only selected candidate evidence", got.Observations)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(validWayfinderResponse(t, got, "rig/first"))),
		}, nil
	})
	advisor.now = func() time.Time { return time.Unix(151, 0) }
	if target, err := advisor.Recommend(context.Background(), RecoveryRequest{
		CorrelationID: "incident-1",
		Now:           time.Unix(150, 0),
		Targets:       []string{"rig/first", "rig/unconfigured"},
	}); err != nil || target != "rig/first" {
		t.Fatalf("Recommend() = %q, %v", target, err)
	}
}

func TestWayfinderRecoveryAdvisorRejectsMoreThanMaximumTargetsBeforeHTTP(t *testing.T) {
	var template wayfinderEvaluateRequest
	if err := json.Unmarshal(recoveryWayfinderTemplate(t, "rig/first"), &template); err != nil {
		t.Fatal(err)
	}
	template.Constraints.Work.AllowedTargetIDs = make([]string, maximumWayfinderRecords+1)
	for i := range template.Constraints.Work.AllowedTargetIDs {
		template.Constraints.Work.AllowedTargetIDs[i] = "rig/target-" + strconv.Itoa(i)
	}
	template.Constraints.Work.AllowedTargetIDs[0] = "rig/first"
	encoded, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", encoded); err == nil {
		t.Fatal("template with more than maximum allowed targets was accepted")
	}

	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", recoveryWayfinderTemplate(t, "rig/first"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	advisor.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected HTTP request")
	})
	targets := make([]string, maximumWayfinderRecords+1)
	for i := range targets {
		targets[i] = "rig/target-" + strconv.Itoa(i)
	}
	targets[0] = "rig/first"
	if _, err := advisor.Recommend(context.Background(), RecoveryRequest{CorrelationID: "incident-1", Now: time.Unix(150, 0), Targets: targets}); err == nil {
		t.Fatal("recovery request with more than maximum targets was accepted")
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls)
	}

	duplicateTargets := make([]string, maximumWayfinderRecords+1)
	for i := range duplicateTargets {
		duplicateTargets[i] = "rig/first"
	}
	if _, err := advisor.Recommend(context.Background(), RecoveryRequest{CorrelationID: "incident-1", Now: time.Unix(150, 0), Targets: duplicateTargets}); err == nil {
		t.Fatal("recovery request with more than maximum duplicate targets was accepted")
	}
	if calls != 0 {
		t.Fatalf("HTTP calls after duplicate targets = %d, want 0", calls)
	}
}

func TestWayfinderRecoveryAdvisorRejectsOversizedFinalRequestBeforeHTTP(t *testing.T) {
	template := nearLimitWayfinderTemplate(t)
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", template)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	advisor.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected HTTP request")
	})
	correlationID := "incident-" + strings.Repeat("x", 247)
	_, err = advisor.Recommend(context.Background(), RecoveryRequest{CorrelationID: correlationID, Now: time.Unix(150, 0), Targets: []string{"rig/first"}})
	if err == nil || !strings.Contains(err.Error(), "request exceeds "+strconv.Itoa(maximumWayfinderPacketBytes)+" bytes") {
		t.Fatalf("Recommend() error = %v, want final marshaled request size rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls)
	}
}

func nearLimitWayfinderTemplate(t *testing.T) []byte {
	t.Helper()
	var request wayfinderEvaluateRequest
	if err := json.Unmarshal(recoveryWayfinderTemplate(t, "rig/first"), &request); err != nil {
		t.Fatal(err)
	}
	candidate := request.Candidates[0]
	assessment := request.ModelAssessments[0]
	entitlement := request.Entitlements[0]
	observation := request.Observations[0]
	request.Candidates = make([]wayfinderCandidate, 0, maximumWayfinderRecords)
	request.ModelAssessments = make([]wayfinderModelAssessment, 0, maximumWayfinderRecords)
	request.Entitlements = make([]wayfinderEntitlement, 0, maximumWayfinderRecords)
	request.Observations = make([]wayfinderObservation, 0, maximumWayfinderRecords)
	for i := 0; i < maximumWayfinderRecords; i++ {
		suffix := strconv.Itoa(i)
		candidateCopy := candidate
		candidateCopy.CandidateID = "candidate-" + suffix
		candidateCopy.ExecutionTarget.AccountRef = "account-" + suffix
		assessmentCopy := assessment
		assessmentCopy.RecordID = "assessment-" + suffix
		candidateCopy.ModelAssessmentRef = stringPointer(assessmentCopy.RecordID)
		entitlementCopy := entitlement
		entitlementCopy.RecordID = "entitlement-" + suffix
		entitlementCopy.AccountRef = candidateCopy.ExecutionTarget.AccountRef
		candidateCopy.EntitlementRef = entitlementCopy.RecordID
		observationCopy := observation
		observationCopy.RecordID = "observation-" + suffix
		candidateCopy.ObservationRef = observationCopy.RecordID
		request.Candidates = append(request.Candidates, candidateCopy)
		request.ModelAssessments = append(request.ModelAssessments, assessmentCopy)
		request.Entitlements = append(request.Entitlements, entitlementCopy)
		request.Observations = append(request.Observations, observationCopy)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	remaining := maximumWayfinderPacketBytes - 100 - len(encoded)
	grow := func(value *string) {
		if remaining <= 0 {
			return
		}
		room := 256 - len(*value)
		if room > remaining {
			room = remaining
		}
		*value += strings.Repeat("x", room)
		remaining -= room
	}
	for i := range request.Candidates {
		grow(&request.Candidates[i].CandidateID)
		grow(&request.Candidates[i].Model.ServeAs)
		grow(&request.Candidates[i].ExecutionTarget.ProviderID)
		grow(&request.Candidates[i].ExecutionTarget.Residency)
		grow(&request.Candidates[i].ExecutionTarget.InvocationBinding.BindingRef)
		grow(&request.ModelAssessments[i].Provenance)
		grow(&request.Entitlements[i].Provenance)
		grow(&request.Observations[i].Provenance)
	}
	// The 128-record cap intentionally prevents record multiplication from
	// reaching the packet limit by itself. Fill a typed, allowlisted policy field
	// with distinct safe IDs so this fixture still exercises the post-mutation
	// marshal guard rather than failing record-count validation first.
	for deniedID := 0; remaining > 0; deniedID++ {
		const minimumIDBytes = 12
		overhead := 3 // comma plus JSON quotes; the fixture starts with an empty list
		if len(request.Constraints.Policy.Providers.DeniedProviderIDs) == 0 {
			overhead = 2 // JSON quotes; no comma for the first element
		}
		maximumGrowth := overhead + 256
		growth := remaining
		if growth > maximumGrowth {
			growth = maximumGrowth
			if left := remaining - growth; left > 0 && left < overhead+minimumIDBytes {
				growth -= overhead + minimumIDBytes - left
			}
		}
		valueBytes := growth - overhead
		prefix := "denied-" + strconv.Itoa(deniedID) + "-"
		if valueBytes < len(prefix) {
			t.Fatalf("cannot fill final %d bytes with a distinct safe provider id", remaining)
		}
		request.Constraints.Policy.Providers.DeniedProviderIDs = append(
			request.Constraints.Policy.Providers.DeniedProviderIDs,
			prefix+strings.Repeat("x", valueBytes-len(prefix)),
		)
		remaining -= growth
	}
	if remaining != 0 {
		t.Fatalf("could not construct near-limit request; %d bytes of padding remain", remaining)
	}
	encoded, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(encoded), maximumWayfinderPacketBytes-100; got != want {
		t.Fatalf("near-limit template size = %d, want %d", got, want)
	}
	return encoded
}

func TestWayfinderRecoveryAdvisorRejectsUnsafeTemplateBeforeHTTP(t *testing.T) {
	valid := recoveryWayfinderTemplate(t, "rig/first")
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "sensitive field", mutate: func(packet map[string]any) { packet["secret"] = "do-not-send" }},
		{name: "environment field", mutate: func(packet map[string]any) { packet["workload"].(map[string]any)["environment"] = "prod" }},
		{name: "prose field", mutate: func(packet map[string]any) {
			packet["workload"].(map[string]any)["instructions"] = "recover the production account"
		}},
		{name: "account identity field", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["account"] = "customer@example.com"
		}},
		{name: "embedded credential prefix in opaque id", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["provider_id"] = "provider/sk-not-a-real-secret"
		}},
		{name: "bare unprefixed credential shaped opaque id", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["provider_id"] = "Q7mR9vK2xP4tN8sL6dF3hJ5cB7wY1uE9aZ4rT8X6"
		}},
		{name: "namespaced unprefixed credential shaped opaque id", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["provider_id"] = "provider/Q7mR9vK2xP4tN8sL6dF3hJ5cB7wY1uE9aZ4rT8X6"
		}},
		{name: "google api key shaped provider id", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["provider_id"] = "provider/AIzaSyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		}},
		{name: "embedded google api key shaped provider id", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["provider_id"] = "provider/AIzaSyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/prod"
		}},
		{name: "google api key shaped account ref", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["account_ref"] = "account/AIzaSyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		}},
		{name: "google api key shaped adapter id", mutate: func(packet map[string]any) {
			target := packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)
			target["adapter_id"] = "adapter/AIzaSyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
			target["invocation_binding"].(map[string]any)["adapter_id"] = "adapter/AIzaSyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		}},
		{name: "account namespace used as provider id", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["provider_id"] = "account/operator"
		}},
		{name: "provider namespace used as account ref", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["account_ref"] = "provider/operator"
		}},
		{name: "provider namespace used as adapter id", mutate: func(packet map[string]any) {
			target := packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)
			target["adapter_id"] = "provider/operator"
			target["invocation_binding"].(map[string]any)["adapter_id"] = "provider/operator"
		}},
		{name: "url shaped opaque id", mutate: func(packet map[string]any) {
			packet["candidates"].([]any)[0].(map[string]any)["execution_target"].(map[string]any)["invocation_binding"].(map[string]any)["binding_ref"] = "https://private.example"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var packet map[string]any
			if err := json.Unmarshal(valid, &packet); err != nil {
				t.Fatal(err)
			}
			tt.mutate(packet)
			encoded, err := json.Marshal(packet)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", encoded); err == nil {
				t.Fatalf("unsafe template accepted: %s", encoded)
			}
		})
	}
}

func TestWayfinderSafeStringAcceptsNormalOpaqueIDs(t *testing.T) {
	for _, value := range []string{
		"opaque/01K4YZ6XR3N8Q2VM9T1A7BCDEF",
		"0123456789abcdef0123456789abcdef",
		"0123456789abcdef0123456789abcdef01234567",
		"550e8400-e29b-41d4-a716-446655440000",
		"Q7mR9vK2xP4tN8sL6dF3hJ5cB7wY1uE9aZ",
		"anthropic-personal-max-opus-5-high",
	} {
		if !wayfinderSafeString(value) {
			t.Errorf("normal opaque id %q rejected", value)
		}
	}
}

func TestWayfinderRecoveryAdvisorRejectsStaleUnauthorizedOrUnboundCandidateEvidence(t *testing.T) {
	valid := recoveryWayfinderTemplate(t, "rig/first")
	tests := []struct {
		name   string
		mutate func(*wayfinderEvaluateRequest)
	}{
		{name: "expired model assessment", mutate: func(request *wayfinderEvaluateRequest) { request.ModelAssessments[0].ExpiresAtUnix = request.NowUnix }},
		{name: "expired entitlement", mutate: func(request *wayfinderEvaluateRequest) { request.Entitlements[0].ExpiresAtUnix = request.NowUnix }},
		{name: "expired observation", mutate: func(request *wayfinderEvaluateRequest) { request.Observations[0].ExpiresAtUnix = request.NowUnix }},
		{name: "missing model assessment", mutate: func(request *wayfinderEvaluateRequest) {
			request.Candidates[0].ModelAssessmentRef = stringPointer("assessment-missing")
		}},
		{name: "model assessment model mismatch", mutate: func(request *wayfinderEvaluateRequest) { request.ModelAssessments[0].CanonicalModel = "model/other" }},
		{name: "model assessment variant mismatch", mutate: func(request *wayfinderEvaluateRequest) { request.ModelAssessments[0].VariantRef = "variant/other" }},
		{name: "missing entitlement", mutate: func(request *wayfinderEvaluateRequest) { request.Candidates[0].EntitlementRef = "entitlement-missing" }},
		{name: "entitlement account mismatch", mutate: func(request *wayfinderEvaluateRequest) { request.Entitlements[0].AccountRef = "account/other" }},
		{name: "disabled entitlement", mutate: func(request *wayfinderEvaluateRequest) { request.Entitlements[0].Enabled = false }},
		{name: "unauthorized entitlement", mutate: func(request *wayfinderEvaluateRequest) { request.Entitlements[0].AuthorizationState = "suspended" }},
		{name: "missing observation", mutate: func(request *wayfinderEvaluateRequest) { request.Candidates[0].ObservationRef = "observation-missing" }},
		{name: "invocation adapter mismatch", mutate: func(request *wayfinderEvaluateRequest) {
			request.Candidates[0].ExecutionTarget.InvocationBinding.AdapterID = "adapter/other"
		}},
		{name: "invocation adapter digest mismatch", mutate: func(request *wayfinderEvaluateRequest) {
			request.Candidates[0].ExecutionTarget.InvocationBinding.AdapterDigest = wayfinderDigest("adapter-other")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request wayfinderEvaluateRequest
			if err := json.Unmarshal(valid, &request); err != nil {
				t.Fatal(err)
			}
			tt.mutate(&request)
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", encoded); err == nil {
				t.Fatal("stale, unauthorized, or unbound evidence accepted")
			}
		})
	}
}

func TestWayfinderRecoveryAdvisorRejectsDuplicateAndCaseVariantKeys(t *testing.T) {
	valid := string(recoveryWayfinderTemplate(t, "rig/first"))
	templates := []string{
		strings.Replace(valid, `"schema_version":"routing/v3"`, `"schema_version":"routing/v3","schema_version":"routing/v3"`, 1),
		strings.Replace(valid, `"schema_version":"routing/v3"`, `"SchemaVersion":"routing/v3","schema_version":"routing/v3"`, 1),
		strings.Replace(valid, `"profile_source":"caller"`, `"profile_source":"caller","Profile_Source":"caller"`, 1),
	}
	for _, template := range templates {
		if _, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", []byte(template)); err == nil {
			t.Fatalf("ambiguous template accepted: %s", template)
		}
	}

	request := recoveryWayfinderRequest(t, valid)
	response := string(validWayfinderResponse(t, request, "rig/first"))
	responses := []string{
		strings.Replace(response, `"schema_version":"routing/v3"`, `"schema_version":"routing/v3","schema_version":"routing/v3"`, 1),
		strings.Replace(response, `"schema_version":"routing/v3"`, `"SchemaVersion":"routing/v3","schema_version":"routing/v3"`, 1),
	}
	for _, response := range responses {
		advisor := recoveryWayfinderAdvisorWithResponse(t, []byte(response))
		if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
			t.Fatalf("ambiguous response accepted: %s", response)
		}
	}
}

func TestWayfinderRecoveryAdvisorVerifiesRoutingV3Bindings(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	tests := []struct {
		name   string
		mutate func(*wayfinderEvaluateResult)
	}{
		{name: "echoed request", mutate: func(result *wayfinderEvaluateResult) { result.Request.Workload.Operation = "plan" }},
		{name: "policy version", mutate: func(result *wayfinderEvaluateResult) { result.PolicyVersion = "policy/other" }},
		{name: "request policy", mutate: func(result *wayfinderEvaluateResult) { result.Request.PolicyVersion = "policy/other" }},
		{name: "request fingerprint", mutate: func(result *wayfinderEvaluateResult) { result.Fingerprints.Request = wayfinderDigest("wrong-request") }},
		{name: "policy fingerprint", mutate: func(result *wayfinderEvaluateResult) { result.Fingerprints.Policy = wayfinderDigest("wrong-policy") }},
		{name: "decision id", mutate: func(result *wayfinderEvaluateResult) { result.DecisionID = "routing/v3:" + strings.Repeat("0", 64) }},
		{name: "candidate disposition id", mutate: func(result *wayfinderEvaluateResult) { result.Candidates[0].CandidateID = "candidate-other" }},
		{name: "candidate disposition model", mutate: func(result *wayfinderEvaluateResult) { result.Candidates[0].Model.CanonicalModel = "model/other" }},
		{name: "candidate disposition target", mutate: func(result *wayfinderEvaluateResult) {
			result.Candidates[0].ExecutionTarget.ConfigDigest = wayfinderDigest("other-config")
		}},
		{name: "selected failed gate", mutate: func(result *wayfinderEvaluateResult) { result.Candidates[0].GateResults[0].Result = "fail" }},
		{name: "selected empty gates", mutate: func(result *wayfinderEvaluateResult) {
			result.Candidates[0].GateResults = nil
		}},
		{name: "selected duplicate gate", mutate: func(result *wayfinderEvaluateResult) {
			result.Candidates[0].GateResults[len(result.Candidates[0].GateResults)-1] = result.Candidates[0].GateResults[0]
		}},
		{name: "recommendation candidate", mutate: func(result *wayfinderEvaluateResult) { result.Recommendation.CandidateID = "candidate-other" }},
		{name: "recommendation model", mutate: func(result *wayfinderEvaluateResult) { result.Recommendation.Model.ServeAs = "other-model" }},
		{name: "recommendation adapter", mutate: func(result *wayfinderEvaluateResult) {
			result.Recommendation.ExecutionTarget.AdapterDigest = wayfinderDigest("other-adapter")
		}},
		{name: "fabricated preferred provider match", mutate: func(result *wayfinderEvaluateResult) {
			result.Recommendation.MatchedPreferredProvider = true
			result.Recommendation.RankComponents.MatchedPreferredProvider = true
			result.Candidates[0].RankComponents.MatchedPreferredProvider = true
		}},
		{name: "fabricated preferred model match", mutate: func(result *wayfinderEvaluateResult) {
			result.Recommendation.MatchedPreferredModel = true
			result.Recommendation.RankComponents.MatchedPreferredModel = true
			result.Candidates[0].RankComponents.MatchedPreferredModel = true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validWayfinderResult(request, "rig/first")
			tt.mutate(&result)
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			advisor := recoveryWayfinderAdvisorWithResponse(t, encoded)
			if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
				t.Fatal("unbound response accepted")
			}
		})
	}
}

func TestWayfinderRecoveryAdvisorRejectsIncompleteSelectedGateSet(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	result := validWayfinderResult(request, "rig/first")
	result.Candidates[0].GateResults = []wayfinderGateResult{
		{Gate: "entitlement", Result: "pass"},
		{Gate: "reachable", Result: "pass"},
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	advisor := recoveryWayfinderAdvisorWithResponse(t, encoded)
	if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
		t.Fatal("incomplete selected gate set accepted")
	}
}

func TestWayfinderRecoveryAdvisorReconcilesSelectedEligibilityWithBoundRequest(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*wayfinderEvaluateRequest)
		mutateResult func(*wayfinderEvaluateResult)
	}{
		{name: "reachable false", mutate: func(request *wayfinderEvaluateRequest) {
			request.Observations[0].Reachable = wayfinderKnownBool{Known: true, Value: wayfinderBoolPointer(false)}
		}},
		{name: "reachable unknown", mutate: func(request *wayfinderEvaluateRequest) {
			request.Observations[0].Reachable = wayfinderKnownBool{}
		}},
		{name: "authentication false", mutate: func(request *wayfinderEvaluateRequest) {
			request.Observations[0].Authenticated = wayfinderKnownBool{Known: true, Value: wayfinderBoolPointer(false)}
		}},
		{name: "circuit open", mutate: func(request *wayfinderEvaluateRequest) {
			request.Observations[0].Circuit = wayfinderKnownString{Known: true, Value: stringPointer("open")}
		}},
		{name: "upstream throttled", mutate: func(request *wayfinderEvaluateRequest) {
			request.Observations[0].Throttle = wayfinderKnownString{Known: true, Value: stringPointer("upstream")}
		}},
		{name: "known exhausted capacity", mutate: func(request *wayfinderEvaluateRequest) {
			zero, eight := uint64(0), uint64(8)
			request.Candidates[0].ExecutionTarget.CapacityModel = "concurrency_slots"
			request.Observations[0].Capacity = wayfinderCapacity{
				Model:      "concurrency_slots",
				FreeSlots:  &wayfinderKnownUint64{Known: true, Value: &zero},
				TotalSlots: &wayfinderKnownUint64{Known: true, Value: &eight},
			}
		}},
		{name: "queue closed", mutate: func(request *wayfinderEvaluateRequest) {
			zero := uint64(0)
			request.Candidates[0].ExecutionTarget.AdmissionModel = "queued"
			request.Candidates[0].ExecutionTarget.ResourceProfile = &wayfinderResourceProfile{
				Scheduler: "managed_batch", QueueRef: "queue/recovery", NodesPerAllocation: 1,
				CPUCoresPerNode: 1, MemoryGiBPerNode: 1, Interconnect: "ethernet", MaxWalltimeSeconds: 60,
			}
			request.Observations[0].Queue = &wayfinderQueue{
				Depth: wayfinderKnownUint64{Known: true, Value: &zero}, ExpectedWaitSeconds: wayfinderKnownUint64{Known: true, Value: &zero},
				AcceptingSubmissions: wayfinderKnownBool{Known: true, Value: wayfinderBoolPointer(false)},
			}
		}},
		{name: "startup unknown", mutate: func(request *wayfinderEvaluateRequest) {
			request.Candidates[0].ExecutionTarget.ActivationModel = "on_demand"
			request.Observations[0].Startup = &wayfinderStartup{State: wayfinderKnownString{}, ExpectedReadySeconds: wayfinderKnownUint64{}}
		}},
		{name: "allocation exhausted", mutate: func(request *wayfinderEvaluateRequest) {
			zero := uint64(0)
			request.Entitlements[0].BillingMode = "allocation"
			request.Entitlements[0].AllocationBalance = &wayfinderAllocationBalance{
				Unit: "service_units", Remaining: wayfinderKnownUint64{Known: true, Value: &zero},
			}
		}, mutateResult: func(result *wayfinderEvaluateResult) {
			result.Recommendation.BillingMode = "allocation"
		}},
		{name: "provider denied by policy", mutate: func(request *wayfinderEvaluateRequest) {
			request.Constraints.Policy.Providers.DeniedProviderIDs = []string{request.Candidates[0].ExecutionTarget.ProviderID}
		}},
		{name: "data handling denied by policy", mutate: func(request *wayfinderEvaluateRequest) {
			request.Constraints.Policy.DataHandling.RemoteDisclosure = "forbidden"
		}},
		{name: "context below workload minimum", mutate: func(request *wayfinderEvaluateRequest) {
			request.Candidates[0].ExecutionTarget.DeploymentMaxContextTokens = 512
		}},
		{name: "unmetered capacity missing declared detail", mutate: func(*wayfinderEvaluateRequest) {}, mutateResult: func(result *wayfinderEvaluateResult) {
			for i := range result.Candidates[0].GateResults {
				if result.Candidates[0].GateResults[i].Gate == "capacity" {
					result.Candidates[0].GateResults[i].Detail = nil
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
			tt.mutate(&request)
			if err := validateWayfinderRequest(&request); err != nil {
				t.Fatalf("mutated request must remain routing/v3-valid: %v", err)
			}
			result := validWayfinderResult(request, "rig/first")
			for i := range result.Candidates[0].GateResults {
				if result.Candidates[0].GateResults[i].Result != "pass" {
					result.Candidates[0].GateResults[i].Result = "pass"
					result.Candidates[0].GateResults[i].Detail = nil
				}
			}
			if tt.mutateResult != nil {
				tt.mutateResult(&result)
			}
			if err := validateWayfinderResult(result, request, map[string]struct{}{"rig/first": {}}); err == nil {
				t.Fatal("selected recommendation contradicted bound eligibility but was accepted")
			}
		})
	}
}

func TestWayfinderRecoveryAdvisorAcceptsSelectedEligibilityConsistentWithBoundRequest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*wayfinderEvaluateRequest)
	}{
		{name: "known metered capacity", mutate: func(request *wayfinderEvaluateRequest) {
			free, total := uint64(1), uint64(8)
			request.Candidates[0].ExecutionTarget.CapacityModel = "concurrency_slots"
			request.Observations[0].Capacity = wayfinderCapacity{
				Model:      "concurrency_slots",
				FreeSlots:  &wayfinderKnownUint64{Known: true, Value: &free},
				TotalSlots: &wayfinderKnownUint64{Known: true, Value: &total},
			}
		}},
		{name: "open queue", mutate: func(request *wayfinderEvaluateRequest) {
			zero := uint64(0)
			request.Candidates[0].ExecutionTarget.AdmissionModel = "queued"
			request.Candidates[0].ExecutionTarget.ResourceProfile = &wayfinderResourceProfile{
				Scheduler: "managed_batch", QueueRef: "queue/recovery", NodesPerAllocation: 1,
				CPUCoresPerNode: 1, MemoryGiBPerNode: 1, Interconnect: "ethernet", MaxWalltimeSeconds: 60,
			}
			request.Observations[0].Queue = &wayfinderQueue{
				Depth: wayfinderKnownUint64{Known: true, Value: &zero}, ExpectedWaitSeconds: wayfinderKnownUint64{Known: true, Value: &zero},
				AcceptingSubmissions: wayfinderKnownBool{Known: true, Value: wayfinderBoolPointer(true)},
			}
		}},
		{name: "known cold startup", mutate: func(request *wayfinderEvaluateRequest) {
			zero := uint64(0)
			request.Candidates[0].ExecutionTarget.ActivationModel = "on_demand"
			request.Observations[0].Startup = &wayfinderStartup{
				State: wayfinderKnownString{Known: true, Value: stringPointer("cold")}, ExpectedReadySeconds: wayfinderKnownUint64{Known: true, Value: &zero},
			}
		}},
		{name: "positive allocation", mutate: func(request *wayfinderEvaluateRequest) {
			one := uint64(1)
			request.Entitlements[0].BillingMode = "allocation"
			request.Entitlements[0].AllocationBalance = &wayfinderAllocationBalance{
				Unit: "service_units", Remaining: wayfinderKnownUint64{Known: true, Value: &one},
			}
		}},
		{name: "strict matching policy", mutate: func(request *wayfinderEvaluateRequest) {
			provider := request.Candidates[0].ExecutionTarget.ProviderID
			request.Constraints.Policy.Providers.AllowedProviderIDs = &[]string{provider}
			request.Constraints.Policy.DataHandling.RemoteDisclosure = "forbidden"
			request.Candidates[0].ExecutionTarget.DataHandling = "local_only"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
			tt.mutate(&request)
			if err := validateWayfinderRequest(&request); err != nil {
				t.Fatalf("mutated request must remain routing/v3-valid: %v", err)
			}
			result := validWayfinderResult(request, "rig/first")
			if err := validateWayfinderResult(result, request, map[string]struct{}{"rig/first": {}}); err != nil {
				t.Fatalf("consistent selected eligibility rejected: %v", err)
			}
		})
	}
}

func TestWayfinderRecoveryAdvisorRejectsUnboundResponseEvidence(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	tests := []struct {
		name   string
		mutate func(*wayfinderEvaluateResult)
	}{
		{name: "missing evidence ref", mutate: func(result *wayfinderEvaluateResult) {
			result.Candidates[0].EvidenceRefs = result.Candidates[0].EvidenceRefs[1:]
		}},
		{name: "unknown evidence ref", mutate: func(result *wayfinderEvaluateResult) {
			result.Candidates[0].EvidenceRefs[0].RecordID = "evidence-missing"
		}},
		{name: "wrong evidence observation time", mutate: func(result *wayfinderEvaluateResult) { result.Candidates[0].EvidenceRefs[0].ObservedAtUnix-- }},
		{name: "wrong evidence expiry", mutate: func(result *wayfinderEvaluateResult) { result.Candidates[0].EvidenceRefs[0].ExpiresAtUnix-- }},
		{name: "duplicate evidence ref", mutate: func(result *wayfinderEvaluateResult) {
			result.Candidates[0].EvidenceRefs[1] = result.Candidates[0].EvidenceRefs[0]
		}},
		{name: "advisory outlives selected evidence", mutate: func(result *wayfinderEvaluateResult) { result.ExpiresAtUnix++ }},
		{name: "reevaluation outlives selected evidence", mutate: func(result *wayfinderEvaluateResult) { result.Reevaluation.ReevaluateAtUnix++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validWayfinderResult(request, "rig/first")
			tt.mutate(&result)
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			advisor := recoveryWayfinderAdvisorWithResponse(t, encoded)
			if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
				t.Fatal("unbound response evidence accepted")
			}
		})
	}
}

func TestWayfinderRecoveryAdvisorAcceptsCrossDomainEvidenceIDCollision(t *testing.T) {
	template, err := decodeWayfinderEvaluateTemplate(recoveryWayfinderTemplate(t, "rig/first"))
	if err != nil {
		t.Fatal(err)
	}
	template.ModelAssessments[0].RecordID = "shared-record"
	template.ModelAssessments[0].AssessedAtUnix = 91
	template.ModelAssessments[0].ExpiresAtUnix = 291
	template.Entitlements[0].RecordID = "shared-record"
	template.Entitlements[0].ObservedAtUnix = 92
	template.Entitlements[0].ExpiresAtUnix = 292
	template.Observations[0].RecordID = "shared-record"
	template.Observations[0].ObservedAtUnix = 93
	template.Observations[0].ExpiresAtUnix = 293
	template.Candidates[0].ModelAssessmentRef = stringPointer("shared-record")
	template.Candidates[0].EntitlementRef = "shared-record"
	template.Candidates[0].ObservationRef = "shared-record"
	encodedTemplate, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	request := recoveryWayfinderRequest(t, string(encodedTemplate))
	result := validWayfinderResult(request, "rig/first")
	result.ExpiresAtUnix = 291
	result.Reevaluation.ReevaluateAtUnix = 291
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", encodedTemplate)
	if err != nil {
		t.Fatal(err)
	}
	advisor.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(encodedResult))}, nil
	})
	advisor.now = func() time.Time { return time.Unix(151, 0) }
	if got, err := advisor.Recommend(context.Background(), recoveryRequest()); err != nil || got != "rig/first" {
		t.Fatalf("Recommend() = %q, %v; want rig/first with colliding cross-domain IDs", got, err)
	}
}

func TestWayfinderRecoveryAdvisorRejectsElapsedReevaluationDeadline(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	result := validWayfinderResult(request, "rig/first")
	result.Reevaluation.ReevaluateAtUnix = 151
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	advisor := recoveryWayfinderAdvisorWithResponse(t, encoded)
	advisor.now = func() time.Time { return time.Unix(151, 0) }
	if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
		t.Fatal("response at mandatory reevaluation deadline was accepted")
	}
}

func TestWayfinderRecoveryAdvisorChecksExpiryAgainstFreshPostResponseClock(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	result := validWayfinderResult(request, "rig/first")
	result.ExpiresAtUnix = 151
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	advisor := recoveryWayfinderAdvisorWithResponse(t, encoded)
	advisor.now = func() time.Time { return time.Unix(151, 0) }
	if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
		t.Fatal("response expiring after request clock but at post-response clock was accepted")
	}
}

func TestWayfinderRecoveryAdvisorRejectsResponseExpiredWhileBodyCloses(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	result := validWayfinderResult(request, "rig/first")
	result.ExpiresAtUnix = 152
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	currentTime := time.Unix(151, 0)
	body := &wayfinderResponseBody{
		Reader: bytes.NewReader(encoded),
		close: func() error {
			currentTime = time.Unix(152, 0)
			return nil
		},
	}
	advisor := recoveryWayfinderAdvisorWithBody(t, body)
	advisor.now = func() time.Time { return currentTime }
	if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
		t.Fatal("response that expired while its body closed was accepted")
	}
}

func TestWayfinderRecoveryAdvisorReturnsBodyCloseError(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	encoded := validWayfinderResponse(t, request, "rig/first")
	closeErr := errors.New("close response body")
	advisor := recoveryWayfinderAdvisorWithBody(t, &wayfinderResponseBody{
		Reader: bytes.NewReader(encoded),
		close:  func() error { return closeErr },
	})
	if _, err := advisor.Recommend(context.Background(), recoveryRequest()); !errors.Is(err, closeErr) {
		t.Fatalf("error = %v, want body close error", err)
	}
}

func TestWayfinderRecoveryAdvisorReturnsContextErrorFromBodyClose(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	encoded := validWayfinderResponse(t, request, "rig/first")
	ctx, cancel := context.WithCancel(context.Background())
	closeErr := errors.New("close response body")
	advisor := recoveryWayfinderAdvisorWithBody(t, &wayfinderResponseBody{
		Reader: bytes.NewReader(encoded),
		close: func() error {
			cancel()
			return closeErr
		},
	})
	if _, err := advisor.Recommend(ctx, recoveryRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestWayfinderRecoveryAdvisorContextTimeoutWinsDeterministically(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	result := validWayfinderResult(request, "rig/first")
	result.ExpiresAtUnix = 150
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", recoveryWayfinderTemplate(t, "rig/first"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	advisor.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(encoded)),
		}, nil
	})
	advisor.now = func() time.Time { return time.Unix(151, 0) }
	_, err = advisor.Recommend(ctx, recoveryRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestWayfinderRecoveryAdvisorFallsBackWithoutMatchingCandidate(t *testing.T) {
	calls := 0
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", recoveryWayfinderTemplate(t, "rig/first"))
	if err != nil {
		t.Fatal(err)
	}
	advisor.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected HTTP request")
	})
	_, err = advisor.Recommend(context.Background(), RecoveryRequest{CorrelationID: "incident-1", Now: time.Unix(150, 0), Targets: []string{"rig/second"}})
	if err == nil {
		t.Fatal("no matching template candidate error = nil")
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls)
	}
}

func TestWayfinderRecoveryAdvisorRejectsMalformedTemplateAndResponse(t *testing.T) {
	valid := recoveryWayfinderTemplate(t, "rig/first")
	for _, template := range [][]byte{
		nil,
		[]byte(`{"schema_version":"routing/v3"}`),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
	} {
		if _, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", template); err == nil {
			t.Fatalf("invalid template accepted: %s", template)
		}
	}
	advisor := recoveryWayfinderAdvisorWithResponse(t, []byte(`{"schema_version":"routing/v3"}`))
	if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
		t.Fatal("incomplete response accepted")
	}
}

func recoveryWayfinderTemplate(t *testing.T, targets ...string) []byte {
	t.Helper()
	packet := wayfinderEvaluateRequest{
		SchemaVersion: "routing/v3",
		CorrelationID: "operator-placeholder",
		Workload: wayfinderWorkloadProfile{
			ProfileSource: "caller", Operation: "execute", Artifact: "code", Complexity: "high", Consequence: "high",
			ExecutionShape: "iterative", ReasoningRequirement: "high", MinimumContextTokens: 1024,
			ExpectedInputTokens: 512, ExpectedOutputTokens: 512, RequiredTools: []string{"shell"},
			StructuredOutput: "none", RequiredInputModalities: []string{"text"}, RequiredOutputModalities: []string{"text"},
		},
		Constraints: wayfinderConstraints{
			Policy: wayfinderPolicyConstraints{
				DataHandling: wayfinderDataHandlingConstraint{RemoteDisclosure: "permitted"},
				Residency:    wayfinderResidencyConstraint{AllowedRegions: &[]string{"us-west"}},
				Providers:    wayfinderProviderConstraint{AllowedProviderIDs: nil, DeniedProviderIDs: []string{}},
				Spend:        wayfinderSpendConstraint{MaxCostMicros: nil, Currency: nil},
			},
			Work: wayfinderWorkConstraints{AllowedTargetIDs: append([]string(nil), targets...)},
		},
		Objectives:    wayfinderObjectives{Quality: 100},
		Preferences:   wayfinderPreferences{},
		NowUnix:       100,
		PolicyVersion: "policy/recovery-v3",
	}
	packet.ModelAssessments = []wayfinderModelAssessment{{
		RecordID: "assessment-1", CanonicalModel: "model/operator", VariantRef: "variant/full",
		OperationFitness: []wayfinderOperationFitness{{Operation: "execute", Fitness: 90}}, AssessedAtUnix: 90, ExpiresAtUnix: 300, Provenance: "catalog/v1",
	}}
	for i, target := range targets {
		suffix := string(rune('a' + i))
		candidate := recoveryWayfinderCandidate(target, suffix)
		packet.Candidates = append(packet.Candidates, candidate)
		packet.Entitlements = append(packet.Entitlements, wayfinderEntitlement{
			RecordID: candidate.EntitlementRef, AccountRef: candidate.ExecutionTarget.AccountRef, Enabled: true,
			AuthorizationState: "authorized", BillingMode: "subscription", ObservedAtUnix: 90, ExpiresAtUnix: 300, Provenance: "entitlement/v1",
		})
		packet.Observations = append(packet.Observations, wayfinderObservation{
			RecordID: candidate.ObservationRef, TargetID: target,
			Reachable: wayfinderKnownBool{Known: true, Value: wayfinderBoolPointer(true)}, Authenticated: wayfinderKnownBool{Known: true, Value: wayfinderBoolPointer(true)},
			Circuit: wayfinderKnownString{Known: true, Value: stringPointer("closed")}, Throttle: wayfinderKnownString{Known: true, Value: stringPointer("none")},
			Capacity: wayfinderCapacity{Model: "unmetered"}, ObservedAtUnix: 90, ExpiresAtUnix: 300, Provenance: "observer/" + suffix,
		})
	}
	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func recoveryWayfinderCandidate(target, suffix string) wayfinderCandidate {
	return wayfinderCandidate{
		CandidateID: "candidate-" + suffix,
		Model: wayfinderModelRecord{
			CanonicalModel: "model/operator", ServeAs: "operator-model", VariantRef: "variant/full", ReasoningEffort: "high",
			InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxContextTokens: 8192, MaxOutputTokens: 2048,
			SupportsTools: true, SupportsStructuredOutput: true, SupportsStreaming: true,
		},
		ExecutionTarget: wayfinderExecutionTarget{
			TargetID: target, FabricKind: "cloud_endpoint", ProviderID: "provider/operator", AccountRef: "account/opaque-" + suffix,
			AdmissionModel: "immediate", ActivationModel: "always_on", CapacityModel: "unmetered",
			DeploymentMaxContextTokens: 8192, DeploymentMaxOutputTokens: 2048, Residency: "us-west",
			DataHandling: "remote_provider", Isolation: "shared_tenant", ConfigDigest: wayfinderDigest("config-" + target),
			AdapterID: "adapter/operator", AdapterDigest: wayfinderDigest("adapter-" + target),
			InvocationBinding: wayfinderInvocationBinding{BindingKind: "adapter_handle", BindingRef: "binding/" + suffix, AdapterID: "adapter/operator", AdapterDigest: wayfinderDigest("adapter-" + target)},
		},
		Economics:          wayfinderEconomics{PricingModel: "included_in_subscription", PriceBasis: "contracted", Volatility: "fixed"},
		ModelAssessmentRef: stringPointer("assessment-1"), EntitlementRef: "entitlement-" + suffix, ObservationRef: "observation-" + suffix,
	}
}

func validWayfinderResponse(t *testing.T, request wayfinderEvaluateRequest, target string) []byte {
	t.Helper()
	encoded, err := json.Marshal(validWayfinderResult(request, target))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func validWayfinderResult(request wayfinderEvaluateRequest, target string) wayfinderEvaluateResult {
	canonicalizeWayfinderRequest(&request)
	fingerprints := wayfinderRequestFingerprints(request)
	result := wayfinderEvaluateResult{
		SchemaVersion: "routing/v3", CorrelationID: request.CorrelationID, DecisionID: wayfinderDecisionID(fingerprints), Request: request,
		Disposition: "selected", Fingerprints: fingerprints, PolicyVersion: request.PolicyVersion, IssuedAtUnix: request.NowUnix,
		ExpiresAtUnix: 300, AdvisoryOnly: true, NoActiveMigration: true, AlternativesAdvisoryOnly: true,
		Reevaluation: wayfinderReevaluation{ReevaluateAtUnix: 300, Reason: "evidence_expiry", RequiresNewRequest: true, NoPaidProbe: true, NoActiveMigration: true},
	}
	for _, candidate := range request.Candidates {
		rank := &wayfinderRankComponents{QualityScore: 90, LatencyScore: 100, CostScore: 100, WeightedScore: 9000, CostBasis: "subscription", UnknownInputs: []string{}}
		disposition := "eligible_not_selected"
		gateResults := []wayfinderGateResult{{Gate: "entitlement", Result: "pass"}}
		if candidate.ExecutionTarget.TargetID == target {
			disposition = "selected"
			var err error
			gateResults, err = wayfinderExpectedGateResults(request, candidate)
			if err != nil {
				panic(err)
			}
			entitlement, ok := wayfinderEntitlementFor(request.Entitlements, candidate.EntitlementRef)
			if !ok {
				panic("missing candidate entitlement")
			}
			result.Recommendation = &wayfinderRecommendation{
				CandidateID:     candidate.CandidateID,
				Model:           wayfinderSelectedModel{CanonicalModel: candidate.Model.CanonicalModel, ServeAs: candidate.Model.ServeAs, ReasoningEffort: candidate.Model.ReasoningEffort},
				ExecutionTarget: wayfinderSelectedTarget{TargetID: candidate.ExecutionTarget.TargetID, ConfigDigest: candidate.ExecutionTarget.ConfigDigest, AdapterID: candidate.ExecutionTarget.AdapterID, AdapterDigest: candidate.ExecutionTarget.AdapterDigest},
				BillingMode:     entitlement.BillingMode, RankComponents: *rank,
			}
		}
		result.Candidates = append(result.Candidates, wayfinderCandidateDisposition{
			CandidateID: candidate.CandidateID, Disposition: disposition, Model: candidate.Model, ExecutionTarget: candidate.ExecutionTarget,
			GateResults: gateResults, RankComponents: rank,
			EvidenceRefs: validWayfinderEvidenceRefs(request, candidate),
		})
	}
	return result
}

func validWayfinderEvidenceRefs(request wayfinderEvaluateRequest, candidate wayfinderCandidate) []wayfinderEvidenceRef {
	refs := make([]wayfinderEvidenceRef, 0, 3)
	if candidate.ModelAssessmentRef != nil {
		for _, assessment := range request.ModelAssessments {
			if assessment.RecordID == *candidate.ModelAssessmentRef {
				refs = append(refs, wayfinderEvidenceRef{RecordID: assessment.RecordID, ObservedAtUnix: assessment.AssessedAtUnix, ExpiresAtUnix: assessment.ExpiresAtUnix})
				break
			}
		}
	}
	for _, entitlement := range request.Entitlements {
		if entitlement.RecordID == candidate.EntitlementRef {
			refs = append(refs, wayfinderEvidenceRef{RecordID: entitlement.RecordID, ObservedAtUnix: entitlement.ObservedAtUnix, ExpiresAtUnix: entitlement.ExpiresAtUnix})
			break
		}
	}
	for _, observation := range request.Observations {
		if observation.RecordID == candidate.ObservationRef {
			refs = append(refs, wayfinderEvidenceRef{RecordID: observation.RecordID, ObservedAtUnix: observation.ObservedAtUnix, ExpiresAtUnix: observation.ExpiresAtUnix})
			break
		}
	}
	return refs
}

func recoveryWayfinderRequest(t *testing.T, template string) wayfinderEvaluateRequest {
	t.Helper()
	packet, err := decodeWayfinderEvaluateTemplate([]byte(template))
	if err != nil {
		t.Fatal(err)
	}
	packet.CorrelationID = "incident-1"
	packet.NowUnix = 150
	packet.Constraints.Work.AllowedTargetIDs = []string{"rig/first"}
	canonicalizeWayfinderRequest(&packet)
	return packet
}

func recoveryWayfinderAdvisorWithResponse(t *testing.T, response []byte) *WayfinderRecoveryAdvisor {
	t.Helper()
	return recoveryWayfinderAdvisorWithBody(t, io.NopCloser(bytes.NewReader(response)))
}

func recoveryWayfinderAdvisorWithBody(t *testing.T, body io.ReadCloser) *WayfinderRecoveryAdvisor {
	t.Helper()
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", recoveryWayfinderTemplate(t, "rig/first"))
	if err != nil {
		t.Fatal(err)
	}
	advisor.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: body}, nil
	})
	advisor.now = func() time.Time { return time.Unix(151, 0) }
	return advisor
}

type wayfinderResponseBody struct {
	io.Reader
	close func() error
}

func (b *wayfinderResponseBody) Close() error { return b.close() }

func recoveryRequest() RecoveryRequest {
	return RecoveryRequest{CorrelationID: "incident-1", Now: time.Unix(150, 0), Targets: []string{"rig/first"}}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func wayfinderBoolPointer(value bool) *bool { return &value }
func stringPointer(value string) *string    { return &value }

func TestWayfinderCanonicalizationDoesNotMutateTemplate(t *testing.T) {
	template := recoveryWayfinderTemplate(t, "rig/second", "rig/first")
	before, err := decodeWayfinderEvaluateTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", template)
	if err != nil {
		t.Fatal(err)
	}
	after := advisor.template
	if !reflect.DeepEqual(before, after) {
		t.Fatal("constructor mutated operator template")
	}
}
