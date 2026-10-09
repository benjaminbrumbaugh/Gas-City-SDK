package routingdecision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ProducerExecutionOutcome is the redacted producer routing/outcome/v3 shape.
// This is serialization of SDK authority, not producer transport or ranking.
// All required nullable fields are present. No raw tuple/account/env is exposed.
type ProducerExecutionOutcome struct {
	SchemaVersion         string              `json:"schema_version"`
	OutcomeID             string              `json:"outcome_id"`
	CorrelationID         string              `json:"correlation_id"`
	RecommendationID      string              `json:"recommendation_id"`
	RoutingDecisionID     *string             `json:"routing_decision_id"`
	WorkID                string              `json:"work_id"`
	AdmissionReceiptID    *string             `json:"admission_receipt_id"`
	SessionID             *string             `json:"session_id"`
	ExecutionID           *string             `json:"execution_id"`
	RequestedTargetID     string              `json:"requested_target_id"`
	RequestedConfigDigest string              `json:"requested_config_digest"`
	ActualTargetID        *string             `json:"actual_target_id"`
	ActualConfigDigest    *string             `json:"actual_config_digest"`
	Status                string              `json:"status"`
	Disposition           OutcomeDisposition  `json:"disposition"`
	FailureClass          OutcomeFailureClass `json:"failure_class"`
	Provenance            string              `json:"provenance"`
	ObservedAtUnix        int64               `json:"observed_at_unix"`
}

// ProducerExecutionOutcomePage is a bounded page of persisted v3 authority facts.
type ProducerExecutionOutcomePage struct {
	SchemaVersion string                     `json:"schema_version"`
	Items         []ProducerExecutionOutcome `json:"items"`
	Total         int                        `json:"total"`
	NextCursor    string                     `json:"next_cursor,omitempty"`
	Partial       bool                       `json:"partial"`
}

// ProjectProducerExecutionOutcome requires an exact durable runtime-start receipt
// for actual identity. Missing execution is omitted, not called not-admitted.
// Unknown completion stays unknown even when work is closed. observed_at is
// persisted evidence time; outcome_id hashes the complete stable projection.
func ProjectProducerExecutionOutcome(item DecisionWithAudits, launches []ExecutionLaunchReceipt) (ProducerExecutionOutcome, bool, error) {
	p := item.Record.Payload
	if err := p.Validate(); err != nil {
		return ProducerExecutionOutcome{}, false, err
	}
	if p.Schema != ExecutionSchemaVersion {
		return ProducerExecutionOutcome{}, false, invalidf("v3 required")
	}
	row := ProducerExecutionOutcome{SchemaVersion: "routing/outcome/v3", CorrelationID: p.WorkBeadID, RecommendationID: p.RecommendationID, RoutingDecisionID: optionalOutcomeOpaque(p.DecisionID), WorkID: p.WorkBeadID, RequestedTargetID: p.Target, RequestedConfigDigest: "sha256:" + p.TargetConfigDigest, Status: "unknown", Disposition: OutcomeDispositionUnknown, FailureClass: OutcomeFailureUnknown, Provenance: "gas-city:runtime-start", ObservedAtUnix: latestOutcomeObservedAt(item, p.CreatedAt).Unix()}
	switch item.Record.State {
	case StateRefusedAfterRace, StateExpired, StateRevoked:
		// Revocation/expiry after an actual launch cannot erase prior execution.
		if len(launches) == 0 {
			row.Status = "failed"
			row.Disposition = OutcomeDispositionNotAdmitted
			row.Provenance = "gas-city:routing-admission"
		}
	}
	if row.Disposition != OutcomeDispositionNotAdmitted {
		if len(launches) == 0 {
			return row, false, nil
		}
		var latest *ExecutionLaunchReceipt
		for index := range launches {
			receipt := &launches[index]
			a := receipt.Authorization
			if receipt.ExecutionID == "" || receipt.StartedAt.IsZero() || a.DecisionID != p.DecisionID || a.BindingID != p.BindingID || a.WorkID != p.WorkBeadID || a.ClaimFence != p.ClaimFence || a.SessionID == "" || a.Generation == "" || a.InstanceToken == "" || p.MatchesExecution(a.Execution) != nil {
				return row, false, ErrStoreCorrupt
			}
			if latest == nil || receipt.StartedAt.After(latest.StartedAt) {
				latest = receipt
			}
		}
		row.AdmissionReceiptID = optionalOutcomeOpaque(item.AdmissionReceiptID)
		row.SessionID = optionalOutcomeOpaque(latest.Authorization.SessionID)
		row.ExecutionID = optionalOutcomeOpaque(latest.ExecutionID)
		row.ActualTargetID = optionalOutcomeOpaque(latest.Authorization.Execution.Target)
		actualDigest := "sha256:" + latest.Authorization.Execution.ConfigDigest
		row.ActualConfigDigest = &actualDigest
		// Launch evidence is immutable. Do not let later lifecycle audits change
		// the serialized outcome when a failed projection is retried.
		row.ObservedAtUnix = latest.StartedAt.Unix()
	}
	// Reuse the existing confined opaque/digest redaction rules for wire strings.
	for _, value := range []string{row.CorrelationID, row.RecommendationID, row.WorkID, row.RequestedTargetID, row.Provenance} {
		if optionalOutcomeOpaque(value) == nil {
			return row, false, invalidf("unsafe outcome identity")
		}
	}
	if row.RoutingDecisionID == nil || row.ObservedAtUnix <= 0 {
		return row, false, invalidf("outcome evidence unavailable")
	}
	outcomeID, err := producerExecutionOutcomeID(row)
	if err != nil {
		return row, false, err
	}
	row.OutcomeID = outcomeID
	return row, true, nil
}

func producerExecutionOutcomeID(outcome ProducerExecutionOutcome) (string, error) {
	canonical := outcome
	canonical.OutcomeID = ""
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("gascity.producer-outcome.v3\x00"), data...))
	return "outcome_" + hex.EncodeToString(digest[:]), nil
}
