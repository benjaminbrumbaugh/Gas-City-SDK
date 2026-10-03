package routingdecision

import "time"

// ExecutionOutcomeObservation is an SDK authority projection, NOT the producer
// routing/outcome/v3 protocol. The producer transport adapter maps these facts
// using the reviewed plugin; this package does not duplicate that protocol.
// Actual remains unavailable until a durable execution receipt binds the
// launched invocation. Admission, closure, and metadata do not prove execution.
type ExecutionOutcomeObservation struct {
	DecisionID         string              `json:"decision_id"`
	RecommendationID   string              `json:"recommendation_id"`
	WorkID             string              `json:"work_id"`
	State              State               `json:"state"`
	AdmissionReceiptID *string             `json:"admission_receipt_id"`
	Requested          ExecutionBinding    `json:"requested"`
	Actual             *ExecutionBinding   `json:"actual"`
	SessionID          *string             `json:"session_id"`
	ExecutionID        *string             `json:"execution_id"`
	Disposition        OutcomeDisposition  `json:"disposition"`
	FailureClass       OutcomeFailureClass `json:"failure_class"`
	ObservedAt         time.Time           `json:"observed_at"`
}

// ProjectExecutionOutcome consumes immutable SDK ledger facts and the existing
// session/execution authority seam. It never stamps a read clock or upgrades
// work closure into a success disposition. A missing durable launch receipt is
// explicitly unavailable, including when terminal work records exist.
func ProjectExecutionOutcome(item DecisionWithAudits, authority OutcomeAuthoritySnapshot) (ExecutionOutcomeObservation, error) {
	p := item.Record.Payload
	if err := p.Validate(); err != nil {
		return ExecutionOutcomeObservation{}, err
	}
	if p.Schema != ExecutionSchemaVersion || p.Execution == nil {
		return ExecutionOutcomeObservation{}, invalidf("v3 execution selection required")
	}
	result := ExecutionOutcomeObservation{
		DecisionID: p.DecisionID, RecommendationID: p.RecommendationID, WorkID: p.WorkBeadID,
		State: item.Record.State, Requested: *p.Execution, AdmissionReceiptID: optionalOutcomeOpaque(item.AdmissionReceiptID),
		Disposition: OutcomeDispositionUnknown, FailureClass: OutcomeFailureUnknown,
		ObservedAt: latestOutcomeObservedAt(item, time.Time{}),
	}
	if item.Record.State == StateRefusedAfterRace || item.Record.State == StateExpired || item.Record.State == StateRevoked {
		result.Disposition = OutcomeDispositionNotAdmitted
		result.AdmissionReceiptID = nil
		return result, nil
	}
	// Causal identity comes only from the SDK's validated session/execution
	// records. They do not bind an invocation, so Actual remains nil.
	result.SessionID, result.ExecutionID = authority.authoritativeCausalIdentity()
	return result, nil
}
