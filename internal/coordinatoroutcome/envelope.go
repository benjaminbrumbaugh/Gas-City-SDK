// Package coordinatoroutcome parses the typed close envelope produced by
// gc-outcome-close. It owns syntax and provenance validation; consumers retain
// authority over what a valid envelope means for their domain transition.
package coordinatoroutcome

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/strictjson"
)

const (
	maxIdentityBytes    = 256
	maxRemediationIDs   = 64
	maxRemediationBytes = 256
	maxEnvelopeBytes    = 32 << 10
	maxReasonBytes      = 4 << 10
)

// Envelope is the strict gc-outcome-close record. Candidate identity and
// remediation links are optional for legacy producers, but acceptance-gated
// convoys require and validate them.
type Envelope struct {
	ContractVersion int      `json:"contract_version"`
	Disposition     string   `json:"disposition"`
	WorkID          string   `json:"work_id"`
	RecordedBy      string   `json:"recorded_by"`
	Reason          string   `json:"reason"`
	Producer        *string  `json:"producer"`
	PassingVerdict  string   `json:"passing_verdict"`
	CandidateWorkID string   `json:"candidate_work_id,omitempty"`
	CandidateCommit string   `json:"candidate_commit,omitempty"`
	RemediationIDs  []string `json:"remediation_ids,omitempty"`
}

// ParseFor decodes and validates the typed-close envelope for subject. It does
// not require a passing review verdict; callers can therefore distinguish a
// well-formed BLOCK from absent or forged evidence.
func ParseFor(subject beads.Bead) (Envelope, error) {
	raw := strings.TrimSpace(subject.Metadata[beadmeta.CoordinatorOutcomeProducerDispositionMetadataKey])
	if raw == "" {
		return Envelope{}, fmt.Errorf("missing typed close envelope")
	}
	var envelope Envelope
	if err := strictjson.DecodeObject(raw, maxEnvelopeBytes, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("malformed typed close envelope: %w", err)
	}
	if envelope.ContractVersion != beadmeta.CoordinatorOutcomeContractVersion {
		return Envelope{}, fmt.Errorf("unsupported typed close contract version %d", envelope.ContractVersion)
	}
	if envelope.Disposition != beadmeta.CoordinatorDispositionDeliverable {
		return Envelope{}, fmt.Errorf("typed close disposition %q is not deliverable", envelope.Disposition)
	}
	if envelope.WorkID != subject.ID {
		return Envelope{}, fmt.Errorf("typed close work_id %q does not match %q", envelope.WorkID, subject.ID)
	}
	if strings.TrimSpace(envelope.RecordedBy) == "" || strings.TrimSpace(envelope.Reason) == "" {
		return Envelope{}, fmt.Errorf("typed close requires recorded_by and reason")
	}
	if envelope.Producer == nil || strings.TrimSpace(*envelope.Producer) == "" {
		return Envelope{}, fmt.Errorf("typed close requires producer")
	}
	if len(envelope.WorkID) > maxIdentityBytes || len(envelope.RecordedBy) > maxIdentityBytes || len(*envelope.Producer) > maxIdentityBytes || len(envelope.CandidateWorkID) > maxIdentityBytes || len(envelope.CandidateCommit) > maxIdentityBytes {
		return Envelope{}, fmt.Errorf("typed close identity fields exceed %d bytes", maxIdentityBytes)
	}
	if len(envelope.Reason) > maxReasonBytes {
		return Envelope{}, fmt.Errorf("typed close reason exceeds %d bytes", maxReasonBytes)
	}
	if len(envelope.RemediationIDs) > maxRemediationIDs {
		return Envelope{}, fmt.Errorf("typed close has %d remediation ids; maximum is %d", len(envelope.RemediationIDs), maxRemediationIDs)
	}
	seen := make(map[string]struct{}, len(envelope.RemediationIDs))
	for _, id := range envelope.RemediationIDs {
		if strings.TrimSpace(id) == "" || len(id) > maxRemediationBytes {
			return Envelope{}, fmt.Errorf("typed close contains invalid remediation id")
		}
		if _, ok := seen[id]; ok {
			return Envelope{}, fmt.Errorf("typed close contains duplicate remediation id %q", id)
		}
		seen[id] = struct{}{}
	}
	return envelope, nil
}

// PublishedVerdict returns the typed close's bounded review-verdict value.
func PublishedVerdict(subject beads.Bead, envelope Envelope) (string, error) {
	switch envelope.PassingVerdict {
	case beadmeta.CoordinatorPassingVerdictReview, beadmeta.CoordinatorPassingVerdictEvidence:
	default:
		return "", fmt.Errorf("unsupported passing_verdict %q", envelope.PassingVerdict)
	}
	if subject.Metadata[beadmeta.ReviewGateMetadataKey] != "consumed" {
		return "", fmt.Errorf("review gate is not consumed")
	}
	verdict := subject.Metadata[envelope.PassingVerdict]
	if strings.TrimSpace(verdict) == "" {
		return "", fmt.Errorf("missing published verdict %q", envelope.PassingVerdict)
	}
	if len(verdict) > 32 {
		return "", fmt.Errorf("published verdict exceeds 32 bytes")
	}
	return verdict, nil
}

// IsDeliverablePass preserves the formula/retry typed-close acceptance rule.
func IsDeliverablePass(subject beads.Bead) bool {
	envelope, err := ParseFor(subject)
	if err != nil {
		return false
	}
	if envelope.PassingVerdict == "" {
		return true
	}
	verdict, err := PublishedVerdict(subject, envelope)
	return err == nil && verdict == beadmeta.OutcomePass
}
