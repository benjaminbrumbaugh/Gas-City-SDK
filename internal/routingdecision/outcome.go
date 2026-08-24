package routingdecision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

const (
	// OutcomeSchemaVersion identifies the strict redacted recommendation-outcome wire contract.
	OutcomeSchemaVersion = "routing/outcome/v2"

	// OutcomeProvenanceDecision identifies a projection backed only by the signed decision.
	OutcomeProvenanceDecision = "authoritative_routing_decision"
	// OutcomeProvenanceExactWork identifies a projection also backed by an exact work carrier.
	OutcomeProvenanceExactWork = "authoritative_routing_decision_exact_work"
)

// OutcomeDisposition is the bounded public disposition vocabulary.
type OutcomeDisposition string

const (
	// OutcomeDispositionShipped reports successful delivery by an authoritative terminal record.
	OutcomeDispositionShipped OutcomeDisposition = "shipped"
	// OutcomeDispositionNoOp reports an authoritative successful no-op.
	OutcomeDispositionNoOp OutcomeDisposition = "no_op"
	// OutcomeDispositionBlocked reports an authoritative blocked execution.
	OutcomeDispositionBlocked OutcomeDisposition = "blocked"
	// OutcomeDispositionAbandoned reports an authoritative abandoned execution.
	OutcomeDispositionAbandoned OutcomeDisposition = "abandoned"
	// OutcomeDispositionNotAdmitted reports that execution was never admitted.
	OutcomeDispositionNotAdmitted OutcomeDisposition = "not_admitted"
	// OutcomeDispositionUnknown reports unavailable terminal truth.
	OutcomeDispositionUnknown OutcomeDisposition = "unknown"
)

// OutcomeFailureClass is the bounded public failure vocabulary.
type OutcomeFailureClass string

const (
	// OutcomeFailureNone reports that an authoritative successful execution had no failure.
	OutcomeFailureNone OutcomeFailureClass = "none"
	// OutcomeFailureTransient reports an authoritative transient execution failure.
	OutcomeFailureTransient OutcomeFailureClass = "transient"
	// OutcomeFailureHard reports an authoritative hard execution failure.
	OutcomeFailureHard OutcomeFailureClass = "hard"
	// OutcomeFailureUnknown reports unavailable or unclassified failure truth.
	OutcomeFailureUnknown OutcomeFailureClass = "unknown"
)

// OutcomeCoverage reports whether the exact work-side carrier was available.
type OutcomeCoverage string

const (
	// OutcomeCoverageAvailable reports that all required authority records were available.
	OutcomeCoverageAvailable OutcomeCoverage = "available"
	// OutcomeCoveragePartial reports that some required authority records were unavailable.
	OutcomeCoveragePartial OutcomeCoverage = "partial"
	// OutcomeCoverageUnknown reports that exact carrier evidence was unavailable.
	OutcomeCoverageUnknown OutcomeCoverage = "unknown"
)

// OutcomeStatus is the portable execution result status.
type OutcomeStatus string

const (
	// OutcomeStatusClaimed reports admitted work whose execution is not terminal.
	OutcomeStatusClaimed OutcomeStatus = "claimed"
	// OutcomeStatusSucceeded reports an authoritative successful terminal execution.
	OutcomeStatusSucceeded OutcomeStatus = "succeeded"
	// OutcomeStatusFailed reports non-admission, unknown terminal truth, or authoritative failure.
	OutcomeStatusFailed OutcomeStatus = "failed"
)

// OutcomeRecord is a strict redacted projection. It intentionally has no fields
// for bead prose, audit reasons, credentials, or raw failures.
type OutcomeRecord struct {
	SchemaVersion         string              `json:"schema_version" enum:"routing/outcome/v2"`
	OutcomeID             string              `json:"outcome_id" pattern:"^outcome_[0-9a-f]{64}$"`
	CorrelationID         string              `json:"correlation_id" minLength:"1"`
	RecommendationID      string              `json:"recommendation_id" pattern:"^routing/v2:[0-9a-f]{64}$"`
	RoutingDecisionID     *string             `json:"routing_decision_id"`
	WorkID                string              `json:"work_id" minLength:"1"`
	AdmissionReceiptID    *string             `json:"admission_receipt_id"`
	SessionID             *string             `json:"session_id"`
	ExecutionID           *string             `json:"execution_id"`
	RequestedTargetID     string              `json:"requested_target_id" minLength:"1"`
	ActualTargetID        *string             `json:"actual_target_id"`
	RequestedConfigDigest string              `json:"requested_config_digest" pattern:"^sha256:[0-9a-f]{64}$"`
	ActualConfigDigest    *string             `json:"actual_config_digest" pattern:"^sha256:[0-9a-f]{64}$"`
	Status                OutcomeStatus       `json:"status" enum:"claimed,succeeded,failed"`
	Disposition           OutcomeDisposition  `json:"disposition" enum:"shipped,no_op,blocked,abandoned,not_admitted,unknown"`
	FailureClass          OutcomeFailureClass `json:"failure_class" enum:"none,transient,hard,unknown"`
	Coverage              OutcomeCoverage     `json:"coverage" enum:"available,partial,unknown"`
	Provenance            string              `json:"provenance" enum:"authoritative_routing_decision,authoritative_routing_decision_exact_work"`
	ObservedAtUnix        int64               `json:"observed_at_unix" minimum:"1"`
}

// OutcomePage is one bounded stable decision-ID page.
type OutcomePage struct {
	SchemaVersion string          `json:"schema_version" enum:"routing/outcome/v2"`
	Items         []OutcomeRecord `json:"items"`
	NextCursor    string          `json:"next_cursor,omitempty"`
	Partial       bool            `json:"partial"`
}

// OutcomeListOptions controls a bounded outcome page.
type OutcomeListOptions struct {
	Limit  int
	Cursor string
}

// OutcomeWorkSnapshot is the minimum work-side authority consumed by the
// projector. Callers must populate it from one exact live Bead read.
type OutcomeWorkSnapshot struct {
	Found      bool
	WorkID     string
	Status     string
	Assignee   string
	ClaimFence int64
	Metadata   map[string]string
}

// ProjectOutcome produces one redacted record from the durable decision, an
// optional exact work carrier, and the typed causal-authority snapshot. No
// audit reason or open-world metadata value is copied except the explicitly
// allowlisted opaque IDs, target, and one-way digest fields.
//
// Causal-truth boundary (routing/outcome/v2): disposition, session identity,
// execution identity, and failure class derive ONLY from the authority
// snapshot — real session/execution records plus a demonstrated terminal
// disposition record. Work-bead metadata proves exact carrier/closure only;
// values like gc.work_outcome, gc.session_id, gc.current_run_id, and
// gc.failure_class are caller-authored and never trusted for truth. Missing,
// mismatched, or ambiguous authority fails closed to unknown.
func ProjectOutcome(item DecisionWithAudits, work OutcomeWorkSnapshot, authority OutcomeAuthoritySnapshot, observedAt time.Time) (result OutcomeRecord) {
	payload := item.Record.Payload
	observedAt = latestOutcomeObservedAt(item, observedAt)
	result = OutcomeRecord{
		SchemaVersion: OutcomeSchemaVersion, CorrelationID: payload.WorkBeadID,
		RecommendationID: payload.RecommendationID, RoutingDecisionID: stringPointer(payload.DecisionID),
		WorkID: payload.WorkBeadID, RequestedTargetID: payload.Target,
		RequestedConfigDigest: portableDigest(payload.TargetConfigDigest), Status: OutcomeStatusFailed,
		Disposition: OutcomeDispositionUnknown, FailureClass: OutcomeFailureUnknown,
		Coverage: OutcomeCoverageUnknown, Provenance: OutcomeProvenanceDecision,
		ObservedAtUnix: observedAt.UTC().Unix(),
	}
	defer func() { result.OutcomeID = outcomeID(result) }()

	if item.Record.State == StateRefusedAfterRace || item.Record.State == StateExpired || item.Record.State == StateRevoked {
		result.Disposition = OutcomeDispositionNotAdmitted
		return result
	}
	if item.Record.State == StateClaimed {
		result.Status = OutcomeStatusClaimed
	}
	if item.Record.State == StateClaimed || item.Record.State == StateOutcomeRecorded {
		actualTarget, actualDigest := payload.Target, portableDigest(payload.TargetConfigDigest)
		result.ActualTargetID, result.ActualConfigDigest = &actualTarget, &actualDigest
	}
	if !work.Found {
		return result
	}
	result.Coverage = OutcomeCoveragePartial
	if !exactOutcomeCarrier(payload, work) {
		return result
	}
	result.Provenance = OutcomeProvenanceExactWork
	actualTarget := strings.TrimSpace(work.Metadata[beadmeta.RunTargetMetadataKey])
	result.ActualTargetID = &actualTarget
	result.AdmissionReceiptID = optionalOutcomeOpaque(item.AdmissionReceiptID)
	// Causal identity and terminal truth come exclusively from the typed
	// authority records. Work metadata is never consulted here: a closed,
	// exact-bound carrier with fabricated gc.work_outcome / gc.session_id /
	// gc.current_run_id / gc.failure_class projects as available-coverage
	// unknown, never succeeded or failed-known.
	sessionID, executionID := authority.authoritativeCausalIdentity()
	result.SessionID = sessionID
	result.ExecutionID = executionID

	if item.Record.State != StateOutcomeRecorded {
		result.Coverage = OutcomeCoverageAvailable
		return result
	}
	if !authority.TerminalDispositionKnown {
		return result
	}
	disposition := authority.Disposition
	if disposition != OutcomeDispositionShipped && disposition != OutcomeDispositionNoOp &&
		disposition != OutcomeDispositionBlocked && disposition != OutcomeDispositionAbandoned {
		return result
	}
	if disposition == OutcomeDispositionShipped || disposition == OutcomeDispositionNoOp {
		if result.AdmissionReceiptID == nil || result.SessionID == nil || result.ExecutionID == nil ||
			authority.Execution == nil || !authority.Execution.Completed {
			result.Coverage = OutcomeCoveragePartial
			return result
		}
		result.Disposition = disposition
		result.Coverage = OutcomeCoverageAvailable
		result.Status = OutcomeStatusSucceeded
		result.FailureClass = OutcomeFailureNone
		return result
	}
	// Known failure dispositions require the demonstrated terminal execution
	// record too; without it the failure truth is unavailable.
	if authority.Execution == nil || !authority.Execution.Completed {
		result.Coverage = OutcomeCoveragePartial
		return result
	}
	result.Disposition = disposition
	result.Coverage = OutcomeCoverageAvailable
	switch authority.FailureClass {
	case OutcomeFailureTransient:
		result.FailureClass = OutcomeFailureTransient
	case OutcomeFailureHard:
		result.FailureClass = OutcomeFailureHard
	default:
		result.FailureClass = OutcomeFailureUnknown
	}
	return result
}

// Validate enforces the strict routing/outcome/v2 wire and causal invariants.
func (record OutcomeRecord) Validate() error {
	if record.SchemaVersion != OutcomeSchemaVersion || record.ObservedAtUnix <= 0 {
		return invalidf("outcome schema or observed time is invalid")
	}
	for name, value := range map[string]string{
		"outcome_id": record.OutcomeID, "correlation_id": record.CorrelationID,
		"recommendation_id": record.RecommendationID, "work_id": record.WorkID,
		"requested_target_id": record.RequestedTargetID,
	} {
		if err := validateOutcomeOpaque(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string]*string{
		"routing_decision_id":  record.RoutingDecisionID,
		"admission_receipt_id": record.AdmissionReceiptID,
		"session_id":           record.SessionID,
		"execution_id":         record.ExecutionID,
	} {
		if value != nil {
			if err := validateOutcomeOpaque(name, *value); err != nil {
				return err
			}
		}
	}
	if !validRecommendationID(record.RecommendationID) || !validPortableDigest(record.RequestedConfigDigest) {
		return invalidf("outcome recommendation identity or requested config digest is invalid")
	}
	if (record.ActualTargetID == nil) != (record.ActualConfigDigest == nil) {
		return invalidf("actual target and config digest must be a nullable pair")
	}
	if record.ActualTargetID != nil {
		if err := validateOutcomeOpaque("actual_target_id", *record.ActualTargetID); err != nil {
			return err
		}
		if !validPortableDigest(*record.ActualConfigDigest) {
			return invalidf("actual config digest is invalid")
		}
	}
	knownDisposition := record.Disposition == OutcomeDispositionShipped || record.Disposition == OutcomeDispositionNoOp ||
		record.Disposition == OutcomeDispositionBlocked || record.Disposition == OutcomeDispositionAbandoned ||
		record.Disposition == OutcomeDispositionNotAdmitted || record.Disposition == OutcomeDispositionUnknown
	knownFailure := record.FailureClass == OutcomeFailureNone || record.FailureClass == OutcomeFailureTransient ||
		record.FailureClass == OutcomeFailureHard || record.FailureClass == OutcomeFailureUnknown
	knownCoverage := record.Coverage == OutcomeCoverageAvailable || record.Coverage == OutcomeCoveragePartial || record.Coverage == OutcomeCoverageUnknown
	knownProvenance := record.Provenance == OutcomeProvenanceDecision || record.Provenance == OutcomeProvenanceExactWork
	if !knownDisposition || !knownFailure || !knownCoverage || !knownProvenance {
		return invalidf("outcome vocabulary is invalid")
	}
	switch record.Status {
	case OutcomeStatusClaimed:
		if record.ActualTargetID == nil || record.Disposition != OutcomeDispositionUnknown || record.FailureClass != OutcomeFailureUnknown {
			return invalidf("claimed outcome consistency is invalid")
		}
	case OutcomeStatusSucceeded:
		if record.ActualTargetID == nil || record.Disposition != OutcomeDispositionShipped && record.Disposition != OutcomeDispositionNoOp || record.FailureClass != OutcomeFailureNone || record.AdmissionReceiptID == nil || record.SessionID == nil || record.ExecutionID == nil {
			return invalidf("succeeded outcome consistency is invalid")
		}
	case OutcomeStatusFailed:
		if record.Disposition == OutcomeDispositionNotAdmitted {
			if record.ActualTargetID != nil || record.FailureClass == OutcomeFailureNone || record.AdmissionReceiptID != nil || record.SessionID != nil || record.ExecutionID != nil {
				return invalidf("not-admitted outcome consistency is invalid")
			}
		} else if record.ActualTargetID == nil || record.FailureClass == OutcomeFailureNone || record.Disposition == OutcomeDispositionShipped || record.Disposition == OutcomeDispositionNoOp {
			return invalidf("failed execution outcome consistency is invalid")
		}
	default:
		return invalidf("outcome status is invalid")
	}
	if outcomeID(record) != record.OutcomeID {
		return invalidf("outcome id does not match canonical immutable fields")
	}
	return nil
}

func latestOutcomeObservedAt(item DecisionWithAudits, fallback time.Time) time.Time {
	latest := time.Time{}
	for _, audit := range item.Audits {
		if audit.At.After(latest) {
			latest = audit.At
		}
	}
	if latest.IsZero() {
		return fallback.UTC()
	}
	return latest.UTC()
}

func outcomeID(record OutcomeRecord) string {
	canonical := record
	canonical.OutcomeID = ""
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(append([]byte("gascity.routing-outcome.v2\x00"), encoded...))
	return "outcome_" + hex.EncodeToString(sum[:])
}

func exactOutcomeCarrier(payload DecisionPayload, work OutcomeWorkSnapshot) bool {
	if work.WorkID != payload.WorkBeadID || strings.TrimSpace(work.Metadata[beadmeta.RoutingDecisionIDMetadataKey]) != payload.DecisionID ||
		strings.TrimSpace(work.Metadata[beadmeta.RunTargetMetadataKey]) != payload.Target {
		return false
	}
	fence, err := strconv.ParseInt(strings.TrimSpace(work.Metadata[beadmeta.RoutingDecisionClaimFenceMetadataKey]), 10, 64)
	return err == nil && fence == payload.ClaimFence
}

func stringPointer(value string) *string { return &value }

func optionalOutcomeOpaque(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || validateOutcomeOpaque("optional outcome identity", value) != nil {
		return nil
	}
	return stringPointer(value)
}

func portableDigest(value string) string {
	if validPortableDigest(value) {
		return value
	}
	return "sha256:" + value
}

func validPortableDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validDigest(strings.TrimPrefix(value, "sha256:"))
}

func validRecommendationID(value string) bool {
	digest := strings.TrimPrefix(value, "routing/v2:")
	return digest != value && len(digest) == sha256.Size*2 && strings.ToLower(digest) == digest && isHex(digest)
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateOutcomeOpaque(name, value string) error {
	if err := validateText(name, value, true); err != nil || value == "" {
		return err
	}
	if len(value) > 256 || !isOutcomeAlphaNumeric(value[0]) {
		return invalidf("%s is not a safe opaque identifier", name)
	}
	for index := 0; index < len(value); index++ {
		if !isOutcomeOpaqueChar(value[index]) {
			return invalidf("%s is not a safe opaque identifier", name)
		}
	}
	if resemblesOutcomeSecret(value) {
		return invalidf("%s resembles secret material", name)
	}
	return nil
}

func resemblesOutcomeSecret(value string) bool {
	lowered := strings.ToLower(value)
	if strings.HasPrefix(lowered, "sk-") {
		// Frozen Wayfinder safeString rule: every sk- prefixed value is
		// rejected unconditionally, with no length-scoped exception.
		return true
	}
	for _, shape := range []struct {
		prefix                 string
		minimum, maximumSuffix int
	}{
		{prefix: "sk-", minimum: 16, maximumSuffix: 253},
		{prefix: "rk-", minimum: 16, maximumSuffix: 253},
		{prefix: "ghp_", minimum: 36, maximumSuffix: 36},
		{prefix: "gho_", minimum: 36, maximumSuffix: 36},
		{prefix: "github_pat_", minimum: 82, maximumSuffix: 82},
		{prefix: "xoxb-", minimum: 20, maximumSuffix: 251},
		{prefix: "xoxp-", minimum: 20, maximumSuffix: 251},
		{prefix: "bearer-", minimum: 16, maximumSuffix: 249},
		{prefix: "basic-", minimum: 16, maximumSuffix: 250},
	} {
		if strings.HasPrefix(lowered, shape.prefix) && outcomeCredentialSuffix(value[len(shape.prefix):], shape.minimum, shape.maximumSuffix) {
			return true
		}
	}
	if len(value) == 20 && (strings.HasPrefix(value, "AKIA") || strings.HasPrefix(value, "ASIA")) && outcomeUpperAlphaNumeric(value[4:]) {
		return true
	}
	if len(value) == 39 && strings.HasPrefix(value, "AIza") && outcomeCredentialSuffix(value[4:], 35, 35) {
		return true
	}
	segments := strings.Split(value, ".")
	if len(segments) == 3 && len(segments[0]) >= 8 && len(segments[1]) >= 8 && len(segments[2]) >= 8 &&
		strings.HasPrefix(segments[0], "eyJ") && strings.HasPrefix(segments[1], "eyJ") &&
		outcomeCredentialSuffix(segments[0], 8, 256) && outcomeCredentialSuffix(segments[1], 8, 256) && outcomeCredentialSuffix(segments[2], 8, 256) {
		return true
	}
	return strings.Contains(value, "://") || strings.ContainsAny(value, "?#=")
}

func outcomeCredentialSuffix(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for index := 0; index < len(value); index++ {
		if !isOutcomeAlphaNumeric(value[index]) && value[index] != '_' && value[index] != '-' {
			return false
		}
	}
	return true
}

func outcomeUpperAlphaNumeric(value string) bool {
	for index := 0; index < len(value); index++ {
		if (value[index] < 'A' || value[index] > 'Z') && (value[index] < '0' || value[index] > '9') {
			return false
		}
	}
	return true
}

func isOutcomeAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func isOutcomeOpaqueChar(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune(".:_/-", rune(value))
}
