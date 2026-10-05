package routingdecision

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func localAdmissionRequestFixture() LocalAdmissionRequest {
	return LocalAdmissionRequest{
		RecommendationID: "routing/v3:" + strings.Repeat("a", 64),
		Work: EligibleWorkSnapshot{
			Rig: "rig-a", Scope: "rig", WorkBeadID: "work-1", WorkRevision: 7, ClaimFence: 3,
			WorkStateDigest: strings.Repeat("b", 64),
		},
		Candidate: ExecutionCandidateSnapshot{
			Schema: 1, CanonicalModel: "model-a", ServeAs: "model-a", ReasoningEffort: "high",
			Account: "account-a", Provider: "provider-a", Target: "rig-a/worker-a",
			ConfigDigest: strings.Repeat("c", 64), AdapterID: "adapter-a",
			AdapterDigest: strings.Repeat("d", 64), InvocationDigest: strings.Repeat("e", 64),
		},
	}
}

func TestLocalAdmissionRequestRejectsUntrustedShape(t *testing.T) {
	request := localAdmissionRequestFixture()
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*LocalAdmissionRequest){
		"recommendation": func(value *LocalAdmissionRequest) { value.RecommendationID = "routing/v3:not-a-digest" },
		"work-digest":    func(value *LocalAdmissionRequest) { value.Work.WorkStateDigest = "caller-work-state" },
		"candidate":      func(value *LocalAdmissionRequest) { value.Candidate.InvocationDigest = "caller-invocation" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			mutate(&changed)
			if err := changed.Validate(); err == nil {
				t.Fatal("invalid local advisory selection accepted")
			}
		})
	}
}

func TestLocalAdmissionPayloadIsStableAndRedacted(t *testing.T) {
	request := localAdmissionRequestFixture()
	now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	payload, err := LocalAdmissionPayload("city-a", request, now)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Schema != ExecutionSchemaVersion || payload.Execution == nil {
		t.Fatalf("payload = %+v", payload)
	}
	other, err := LocalAdmissionPayload("city-a", request, now)
	if err != nil || payload.BindingID != other.BindingID || payload.DecisionID != other.DecisionID {
		t.Fatalf("local payload identity not stable: first=%+v second=%+v err=%v", payload, other, err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "executable") || strings.Contains(string(encoded), "environment") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("local request leaked execution config: %s", encoded)
	}
}

func TestStoreAdmitLocalIsExplicitIdempotentAndVerifierFree(t *testing.T) {
	now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	request := localAdmissionRequestFixture()
	payload, err := LocalAdmissionPayload("city-a", request, now)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	first, err := store.AdmitLocal(payload, "local-attempt-1", func(record Record) (AdmissionCallbackResult, error) {
		called++
		if !record.Local || record.State != StateProposed || record.Approval != nil || record.Signature != nil {
			t.Fatalf("callback record = %+v", record)
		}
		return AdmissionCallbackResult{State: StateAdmitted, Reason: "exact local work CAS committed"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.State != StateAdmitted || !first.Record.Local || first.Record.RecordRevision != 2 || called != 1 {
		t.Fatalf("first local admission = %+v, callback count=%d", first, called)
	}
	replay, err := store.AdmitLocal(payload, "local-attempt-1", func(Record) (AdmissionCallbackResult, error) {
		called++
		return AdmissionCallbackResult{}, nil
	})
	if err != nil || replay.Receipt != first.Receipt || called != 1 {
		t.Fatalf("local replay = %+v err=%v callback count=%d", replay, err, called)
	}
	if _, err := store.Verify(Verifier{}); err != nil {
		t.Fatalf("local-only ledger required an external verifier: %v", err)
	}
}

func TestStoreAdmitLocalRollsBackWhenAdmissionCallbackFails(t *testing.T) {
	now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload, err := LocalAdmissionPayload("city-a", localAdmissionRequestFixture(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdmitLocal(payload, "local-attempt-fails", func(Record) (AdmissionCallbackResult, error) {
		return AdmissionCallbackResult{}, errors.New("callback failed")
	}); err == nil {
		t.Fatal("callback failure admitted local record")
	}
	if _, err := store.Get(payload.DecisionID); err == nil {
		t.Fatal("callback failure left a local record behind")
	}
}
