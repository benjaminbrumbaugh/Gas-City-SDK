package routingdecision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ExecutionCandidateSnapshot is the SDK-resolved, redacted execution tuple
// exposed to an advisory selector. It contains no executable, argv,
// environment, credential, or workload fields.
type ExecutionCandidateSnapshot ExecutionBinding

// Validate checks that an advisory candidate is a complete SDK execution tuple.
func (candidate ExecutionCandidateSnapshot) Validate() error {
	return ExecutionBinding(candidate).Validate()
}

// ExecutionBinding converts an advisory candidate into the immutable execution
// binding used by the controller and runtime authorization paths.
func (candidate ExecutionCandidateSnapshot) ExecutionBinding() ExecutionBinding {
	return ExecutionBinding(candidate)
}

// LocalAdmissionRequest is the complete selector output accepted by the local
// SDK admission lane. The SDK re-resolves every field before mutation.
type LocalAdmissionRequest struct {
	RecommendationID string                     `json:"recommendation_id"`
	Work             EligibleWorkSnapshot       `json:"work"`
	Candidate        ExecutionCandidateSnapshot `json:"candidate"`
}

// Validate checks the selector output without inferring policy or workload.
func (request LocalAdmissionRequest) Validate() error {
	if !validV3RecommendationID(request.RecommendationID) {
		return invalidf("v3 recommendation identity required")
	}
	if err := request.Work.Validate(); err != nil {
		return err
	}
	if err := request.Candidate.Validate(); err != nil {
		return err
	}
	return nil
}

// Validate checks one exact ready-work observation.
func (work EligibleWorkSnapshot) Validate() error {
	for name, value := range map[string]string{
		"work.rig": work.Rig, "work.scope": work.Scope, "work.work_bead_id": work.WorkBeadID,
	} {
		if err := validateText(name, value, true); err != nil {
			return err
		}
	}
	if work.ClaimFence < 0 {
		return invalidf("work claim fence must be non-negative")
	}
	if !validDigest(work.WorkStateDigest) {
		return invalidf("work state digest must be a lowercase SHA-256 value")
	}
	return nil
}

// LocalDecisionID derives the stable SDK ledger identity for one exact
// advisory selection. A retry with the same selection therefore cannot create
// a second admitted record.
func LocalDecisionID(request LocalAdmissionRequest) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode local admission identity: %w", err)
	}
	sum := sha256.Sum256(append([]byte("gascity.local-admission.v3\x00"), encoded...))
	return "local/v3:" + hex.EncodeToString(sum[:]), nil
}

// LocalAdmissionPayload builds the schema-2 payload used by the local lane.
// Its policy digest is a fixed protocol marker and its observation digest
// commits to the exact typed selector output; neither claims external policy
// evaluation or provider execution.
func LocalAdmissionPayload(city string, request LocalAdmissionRequest, now time.Time) (DecisionPayload, error) {
	if err := request.Validate(); err != nil {
		return DecisionPayload{}, err
	}
	if err := validateText("city", city, true); err != nil {
		return DecisionPayload{}, err
	}
	decisionID, err := LocalDecisionID(request)
	if err != nil {
		return DecisionPayload{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return DecisionPayload{}, fmt.Errorf("encode local admission observation: %w", err)
	}
	observationDigest := sha256.Sum256(append([]byte("gascity.local-admission-observation.v3\x00"), encoded...))
	policyDigest := sha256.Sum256([]byte("gascity.local-admission-policy.v3\x00"))
	created := now.UTC().Round(0)
	if created.IsZero() {
		return DecisionPayload{}, invalidf("local admission time is required")
	}
	payload := DecisionPayload{
		Schema: ExecutionSchemaVersion, DecisionID: decisionID, RecommendationID: request.RecommendationID,
		Execution:  func() *ExecutionBinding { binding := request.Candidate.ExecutionBinding(); return &binding }(),
		WorkBeadID: request.Work.WorkBeadID, WorkRevision: request.Work.WorkRevision, ClaimFence: request.Work.ClaimFence,
		WorkStateDigest: request.Work.WorkStateDigest, City: city, Rig: request.Work.Rig,
		Target: request.Candidate.Target, TargetConfigDigest: request.Candidate.ConfigDigest,
		PolicyDigest: hex.EncodeToString(policyDigest[:]), ObservationDigest: hex.EncodeToString(observationDigest[:]),
		Model: request.Candidate.CanonicalModel, Source: "local-advisory", Account: request.Candidate.Account,
		ServeAs: request.Candidate.ServeAs, Provider: request.Candidate.Provider,
		Reason: "local advisory admission", Evidence: []string{}, Alternatives: []Alternative{}, Options: []AuditOption{},
		CreatedAt: created, ExpiresAt: created.Add(5 * time.Minute), NoMigration: true,
	}
	payload.BindingID = BindingID(payload)
	return payload, payload.Validate()
}

// LocalAdmissionResult contains the SDK-owned local record and admission
// receipt. Admission is not evidence of a provider launch or work completion.
type LocalAdmissionResult struct {
	Record  Record            `json:"record"`
	Receipt TransitionReceipt `json:"receipt"`
}

// CandidateMatches reports whether a selector candidate is byte-for-byte equal
// to the controller-resolved execution tuple.
func CandidateMatches(candidate ExecutionCandidateSnapshot, actual ExecutionBinding) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	if candidate.ExecutionBinding() != actual {
		return invalidf("execution candidate differs from live SDK resolution")
	}
	return nil
}

// IsLocalDecision reports whether a durable record was admitted by the local
// advisory lane rather than by a signed external authority.
func (record Record) IsLocalDecision() bool {
	return record.Local
}
