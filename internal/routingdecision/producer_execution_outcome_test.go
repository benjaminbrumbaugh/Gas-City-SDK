package routingdecision

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProducerV3ProjectionRequiresDurableLaunchAndKeepsTerminalUnknown(t *testing.T) {
	p := testExecutionPayload(t)
	item := DecisionWithAudits{Record: Record{Payload: p, State: StateClaimed}, AdmissionReceiptID: "admission-real"}
	if _, ok, err := ProjectProducerExecutionOutcome(item, nil); err != nil || ok {
		t.Fatalf("invented actual execution: %v %v", ok, err)
	}
	auth := ExecutionSessionAuthorization{DecisionID: p.DecisionID, BindingID: p.BindingID, WorkID: p.WorkBeadID, ClaimFence: p.ClaimFence, SessionID: "s1", Generation: "1", InstanceToken: "local-instance", Execution: *p.Execution}
	receipt := ExecutionLaunchReceipt{ExecutionID: "exec-real", Authorization: auth, StartedAt: p.CreatedAt.Add(time.Second)}
	row, ok, err := ProjectProducerExecutionOutcome(item, []ExecutionLaunchReceipt{receipt})
	if err != nil || !ok {
		t.Fatalf("durable execution omitted: %v %v", ok, err)
	}
	if row.Status != "unknown" || row.Disposition != OutcomeDispositionUnknown || row.FailureClass != OutcomeFailureUnknown || row.ActualTargetID == nil || *row.ActualTargetID != p.Target || row.ObservedAtUnix != receipt.StartedAt.Unix() {
		t.Fatalf("false terminal/clock: %+v", row)
	}
	data, _ := json.Marshal(row)
	if strings.Contains(string(data), "local-instance") || strings.Contains(string(data), "coverage") {
		t.Fatalf("internal authority leaked: %s", data)
	}
	var wire map[string]json.RawMessage
	_ = json.Unmarshal(data, &wire)
	for _, key := range []string{"routing_decision_id", "admission_receipt_id", "session_id", "execution_id", "actual_target_id", "actual_config_digest"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("missing required nullable %s", key)
		}
	}
	receipt.Authorization.Execution.ServeAs = "other"
	if _, _, err := ProjectProducerExecutionOutcome(item, []ExecutionLaunchReceipt{receipt}); err == nil {
		t.Fatal("forged receipt accepted")
	}
}
