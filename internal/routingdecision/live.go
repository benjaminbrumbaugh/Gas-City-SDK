package routingdecision

import "time"

const (
	// AvailabilityReady means the boot-latched authority and ledger are usable.
	AvailabilityReady = "ready"
	// AvailabilityDenied means routing ingest and admission are fail-closed.
	AvailabilityDenied = "denied"

	// ReasonReady identifies a fully initialized routing service.
	ReasonReady = "ready"
	// ReasonAuthorityUnavailable identifies an absent or unreadable authority input.
	ReasonAuthorityUnavailable = "authority_unavailable"
	// ReasonAuthorityInvalid identifies malformed or unsafe authority input.
	ReasonAuthorityInvalid = "authority_invalid"
	// ReasonLedgerUnavailable identifies a ledger that could not be opened.
	ReasonLedgerUnavailable = "ledger_unavailable"
	// ReasonLedgerInvalid identifies a ledger that failed durable verification.
	ReasonLedgerInvalid = "ledger_invalid"
	// ReasonServiceClosed identifies a city runtime that has begun shutdown.
	ReasonServiceClosed = "service_closed"

	// TerminalRetentionMonths is the calendar retention age for terminal records.
	TerminalRetentionMonths = retentionMonths
)

// LiveStatus is the boot-latched routing capability and exact ledger summary.
type LiveStatus struct {
	Schema             int         `json:"schema"`
	Status             string      `json:"status"`
	Reason             string      `json:"reason"`
	AuthorityReady     bool        `json:"authority_ready"`
	RetentionMonths    int         `json:"retention_months"`
	TerminalStateBasis string      `json:"terminal_state_basis"`
	Store              StoreStatus `json:"store"`
}

// TargetSnapshot is the only selection-safe public projection of a configured
// static target. Credential-bearing configuration contributes only to Digest.
type TargetSnapshot struct {
	Target           string `json:"target"`
	Rig              string `json:"rig"`
	Description      string `json:"description"`
	ResolvedProvider string `json:"resolved_provider"`
	ConfigDigest     string `json:"config_digest"`
}

// EligibleWorkSnapshot is one exact, ready, unowned work observation.
type EligibleWorkSnapshot struct {
	Rig             string `json:"rig"`
	Scope           string `json:"scope"`
	WorkBeadID      string `json:"work_bead_id"`
	WorkRevision    int64  `json:"work_revision"`
	ClaimFence      int64  `json:"claim_fence"`
	WorkStateDigest string `json:"work_state_digest"`
}

// SelectionSnapshot is one deterministic external-selector input boundary.
type SelectionSnapshot struct {
	ObservedAt time.Time              `json:"observed_at"`
	Work       []EligibleWorkSnapshot `json:"work"`
	Targets    []TargetSnapshot       `json:"targets"`
}
