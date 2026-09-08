package convoy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/coordinatoroutcome"
	"github.com/gastownhall/gascity/internal/strictjson"
)

const (
	// AcceptanceStateInProgress means required work or review is still active.
	AcceptanceStateInProgress = "in-progress"
	// AcceptanceStateComplete means all required review evidence passed.
	AcceptanceStateComplete = "complete"
	// AcceptanceStateBlockedRemediation means explicit linked repair work is open.
	AcceptanceStateBlockedRemediation = "blocked-remediation"
	// AcceptanceStateStranded means terminal work lacks acceptable review evidence.
	AcceptanceStateStranded = "stranded"

	// AcceptanceContractVersion is the only supported convoy acceptance schema.
	AcceptanceContractVersion  = 1
	maxAcceptanceIdentityBytes = 256
	maxAcceptanceReviewGates   = 64
	maxAcceptanceContractBytes = 32 << 10
)

// AcceptanceContract is the opt-in convoy metadata contract. CandidateCommit
// is a full lowercase Git SHA-1 or SHA-256 object id; review gates must publish
// the same work id and immutable object id in their typed-close envelopes.
type AcceptanceContract struct {
	ContractVersion int      `json:"contract_version"`
	CandidateWorkID string   `json:"candidate_work_id"`
	CandidateCommit string   `json:"candidate_commit"`
	ReviewGateIDs   []string `json:"review_gate_ids"`
}

func evaluateAcceptance(convoy beads.Bead, children []beads.Bead, result *ConvoyProgressResult) {
	raw, optedIn := convoy.Metadata[beadmeta.ConvoyAcceptanceMetadataKey]
	if !optedIn {
		result.AcceptedComplete = result.Complete
		if result.Complete {
			result.AcceptanceState = AcceptanceStateComplete
		} else {
			result.AcceptanceState = AcceptanceStateInProgress
		}
		return
	}

	result.AcceptanceGated = true
	contract, err := ParseAcceptanceContract(raw)
	if err != nil {
		strandAcceptance(result, err.Error())
		return
	}

	members := make(map[string]beads.Bead, len(children))
	for _, child := range children {
		members[child.ID] = child
	}
	candidate, ok := members[contract.CandidateWorkID]
	if !ok || IsUnresolvedTrackedItem(candidate) {
		strandAcceptance(result, fmt.Sprintf("candidate %s is not a resolved tracked member", contract.CandidateWorkID))
		return
	}
	if got := candidate.Metadata[beadmeta.WorkCommitMetadataKey]; got != contract.CandidateCommit {
		strandAcceptance(result, fmt.Sprintf("candidate %s current bytes %q do not match anchored commit %q", candidate.ID, got, contract.CandidateCommit))
		return
	}

	allGatesTerminal := true
	blockedWithOpenRemediation := false
	seenRemediation := make(map[string]struct{})
	for _, gateID := range contract.ReviewGateIDs {
		gate, ok := members[gateID]
		if !ok || IsUnresolvedTrackedItem(gate) {
			strandAcceptance(result, fmt.Sprintf("required review gate %s is not a resolved tracked member", gateID))
			continue
		}
		if !IsTerminalStatus(gate.Status) {
			allGatesTerminal = false
			continue
		}

		envelope, err := coordinatoroutcome.ParseFor(gate)
		if err != nil {
			strandAcceptance(result, fmt.Sprintf("required review gate %s: %v", gateID, err))
			continue
		}
		if envelope.CandidateWorkID != contract.CandidateWorkID || envelope.CandidateCommit != contract.CandidateCommit {
			strandAcceptance(result, fmt.Sprintf("required review gate %s candidate identity does not match anchored candidate bytes", gateID))
			continue
		}
		verdict, err := coordinatoroutcome.PublishedVerdict(gate, envelope)
		if err != nil {
			strandAcceptance(result, fmt.Sprintf("required review gate %s: %v", gateID, err))
			continue
		}

		openRemediation := false
		for _, remediationID := range envelope.RemediationIDs {
			if _, duplicate := seenRemediation[remediationID]; !duplicate {
				result.RemediationIDs = append(result.RemediationIDs, remediationID)
				seenRemediation[remediationID] = struct{}{}
			}
			remediation, tracked := members[remediationID]
			if !tracked || IsUnresolvedTrackedItem(remediation) {
				strandAcceptance(result, fmt.Sprintf("review gate %s remediation %s is not a resolved tracked member", gateID, remediationID))
				continue
			}
			if !IsTerminalStatus(remediation.Status) {
				openRemediation = true
			}
		}

		switch verdict {
		case beadmeta.OutcomePass:
			if openRemediation {
				blockedWithOpenRemediation = true
				result.AcceptanceIssues = append(result.AcceptanceIssues, fmt.Sprintf("review gate %s has open linked remediation", gateID))
			}
		case "block":
			result.AcceptanceIssues = append(result.AcceptanceIssues, fmt.Sprintf("review gate %s verdict is block", gateID))
			if openRemediation {
				blockedWithOpenRemediation = true
			}
		default:
			strandAcceptance(result, fmt.Sprintf("required review gate %s published unsupported verdict %q", gateID, verdict))
		}
	}

	if blockedWithOpenRemediation {
		result.AcceptanceState = AcceptanceStateBlockedRemediation
		return
	}
	if len(result.AcceptanceIssues) > 0 {
		result.AcceptanceState = AcceptanceStateStranded
		return
	}
	if !allGatesTerminal || !result.Complete {
		result.AcceptanceState = AcceptanceStateInProgress
		return
	}
	result.AcceptedComplete = true
	result.AcceptanceState = AcceptanceStateComplete
}

func strandAcceptance(result *ConvoyProgressResult, issue string) {
	result.AcceptanceIssues = append(result.AcceptanceIssues, issue)
	result.AcceptanceState = AcceptanceStateStranded
}

// ParseAcceptanceContract strictly decodes and bounds an acceptance contract.
func ParseAcceptanceContract(raw string) (AcceptanceContract, error) {
	if strings.TrimSpace(raw) == "" {
		return AcceptanceContract{}, fmt.Errorf("empty convoy acceptance contract")
	}
	var contract AcceptanceContract
	if err := strictjson.DecodeObject(raw, maxAcceptanceContractBytes, &contract); err != nil {
		return AcceptanceContract{}, fmt.Errorf("malformed convoy acceptance contract: %w", err)
	}
	if contract.ContractVersion != AcceptanceContractVersion {
		return AcceptanceContract{}, fmt.Errorf("unsupported convoy acceptance contract version %d", contract.ContractVersion)
	}
	if strings.TrimSpace(contract.CandidateWorkID) == "" || strings.TrimSpace(contract.CandidateCommit) == "" ||
		len(contract.CandidateWorkID) > maxAcceptanceIdentityBytes || len(contract.CandidateCommit) > maxAcceptanceIdentityBytes {
		return AcceptanceContract{}, fmt.Errorf("convoy acceptance contract requires bounded candidate_work_id and candidate_commit")
	}
	if !isFullGitOID(contract.CandidateCommit) {
		return AcceptanceContract{}, fmt.Errorf("candidate_commit must be a full lowercase SHA-1 or SHA-256 Git object id")
	}
	if len(contract.ReviewGateIDs) == 0 || len(contract.ReviewGateIDs) > maxAcceptanceReviewGates {
		return AcceptanceContract{}, fmt.Errorf("convoy acceptance contract requires 1-%d review_gate_ids", maxAcceptanceReviewGates)
	}
	seen := make(map[string]struct{}, len(contract.ReviewGateIDs))
	for _, id := range contract.ReviewGateIDs {
		if strings.TrimSpace(id) == "" || len(id) > maxAcceptanceIdentityBytes {
			return AcceptanceContract{}, fmt.Errorf("convoy acceptance contract contains invalid review gate id")
		}
		if _, duplicate := seen[id]; duplicate {
			return AcceptanceContract{}, fmt.Errorf("convoy acceptance contract contains duplicate review gate id %q", id)
		}
		seen[id] = struct{}{}
	}
	return contract, nil
}

// MarshalAcceptanceContract validates and encodes a contract for storage under
// beadmeta.ConvoyAcceptanceMetadataKey. Linking candidate, gate, and remediation
// beads remains a separate Store operation; evaluators fail closed if that
// multi-write setup is only partially applied.
func MarshalAcceptanceContract(contract AcceptanceContract) (string, error) {
	raw, err := json.Marshal(contract)
	if err != nil {
		return "", err
	}
	if _, err := ParseAcceptanceContract(string(raw)); err != nil {
		return "", err
	}
	return string(raw), nil
}

func isFullGitOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
