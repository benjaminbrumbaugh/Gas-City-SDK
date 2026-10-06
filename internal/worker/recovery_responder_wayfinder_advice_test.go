package worker

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWayfinderRecoveryAdvisorPreservesValidNoEligibleResult(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	result := validWayfinderResult(request, "rig/first")
	result.Disposition = "no_eligible"
	result.Recommendation = nil
	for i := range result.Candidates {
		result.Candidates[i].Disposition = "rejected"
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	advisor := recoveryWayfinderAdvisorWithResponse(t, encoded)

	got, err := advisor.Recommend(context.Background(), recoveryRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != RecoveryAdviceNoEligible || got.Target != "" {
		t.Fatalf("advice = %+v, want typed no-eligible result without target", got)
	}
}
