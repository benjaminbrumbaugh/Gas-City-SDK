// Package reconcileobservation carries the controller's latest factual
// observation of one reconciliation cycle.
//
// It exists as its own package for one structural reason: the producer is the
// city runtime in cmd/gc, which is package main and therefore unimportable, so
// an optional API capability cannot name a type declared there. This is the
// same boundary internal/routingdecision solves for the live routing capability.
//
// WHAT THIS IS NOT. There is no healthy, degraded, idle or stuck field here,
// and there never should be. A city is stuck only relative to a declared
// progress obligation — a subject and scope, a factual trigger, an expected
// transition, a deadline, and a complete evidence boundary — and four of those
// five belong to the consumer, not to the city. The SDK transports and projects
// facts; the judgment stays with whoever declared the obligation.
//
// Two consequences of that rule are easy to erode later, so they are stated
// here rather than left to taste:
//
//   - No field is an AGE. Ages are published as instants and subtracted by the
//     consumer against its own clock. A server-computed age silently mixes the
//     server's clock into what is supposed to be a fact.
//   - A failed source is never a zero. Every count is paired with the
//     completeness flags that say whether the query behind it was whole, and a
//     partial read is reported as partial with the counters the controller
//     actually saw.
package reconcileobservation

import "time"

// SchemaVersion is the wire contract version of Observation. Bump it only for
// a change a consumer must notice; adding an optional field does not qualify.
const SchemaVersion = 1

// MaxTemplates bounds Templates so the response cannot grow without limit as
// city.toml grows. Exceeding it sets TemplatesTruncated; TemplateCount always
// reports the true number. Truncation is declared, never silent — the same
// discipline the completeness flags apply to partial reads.
const MaxTemplates = 256

// Observation is one immutable, generation-bound view of what the controller
// most recently reconciled. It is published whole and read whole: no consumer
// ever sees fields from two different cycles, because assembly happens once at
// cycle end rather than per-field at read time.
type Observation struct {
	SchemaVersion int    `json:"schema_version"`
	City          string `json:"city"`

	Generation   Generation    `json:"generation"`
	Cycle        Cycle         `json:"cycle"`
	Completeness Completeness  `json:"completeness"`
	Totals       Totals        `json:"totals"`
	Templates    []TemplateRow `json:"templates"`

	// TemplatesTruncated reports that Templates was cut at MaxTemplates.
	TemplatesTruncated bool `json:"templates_truncated"`
	// TemplateCount is the true number of templates the cycle observed, which
	// is larger than len(Templates) exactly when TemplatesTruncated is set.
	TemplateCount int `json:"template_count"`

	Trace       TraceState             `json:"trace"`
	ClaimLeases *ClaimLeaseObservation `json:"claim_leases,omitempty"`
}

// Deliberately absent, and each omission is load-bearing:
//
//	vcs_dirty            SessionReconcilerTraceRecord declares it and nothing in
//	                     the tree ever sets it. Publishing a field that is
//	                     always false states something the controller does not
//	                     actually know.
//	demand_summary       likewise declared and never written upstream.
//	*_age_seconds        an age is the consumer's subtraction, not the city's.
//	healthy/stuck/idle   a verdict; see the package comment.
//	expected_next_tick   overdue is relative to a declared deadline the city
//	                     does not hold.
//	per-bead rows        unbounded, and they would make read cost track
//	                     workload — the property this resource exists to avoid.

// Generation identifies the controller process that produced an Observation.
//
// It is carried on every response because an observation from a previous
// controller is still a true statement about what that controller did. Skew is
// reachable inside a single supervisor process — a city can be unregistered and
// re-registered, producing a second runtime with a different InstanceID — so
// the reader is told which generation it is holding rather than being handed a
// value that quietly changed meaning.
type Generation struct {
	// InstanceID is host:pid, matching the identity the reconciler trace
	// already stamps on its records.
	InstanceID string `json:"instance_id"`
	PID        int    `json:"pid"`
	// StartedAt is when this generation began reconciling, captured at its
	// first cycle. It is not process start: the honest thing to publish is the
	// instant from which these observations exist.
	StartedAt time.Time `json:"started_at"`
	Host      string    `json:"host,omitempty"`
	GCVersion string    `json:"gc_version,omitempty"`
	GCCommit  string    `json:"gc_commit,omitempty"`
	BuildDate string    `json:"build_date,omitempty"`
	// ConfigRev is the effective config revision the cycle ran under. It is
	// part of the generation identity, not decoration: a reload inside one
	// controller process changes the template set the counts below are keyed
	// by, so an observation taken under an earlier revision describes a
	// different city than the one now configured.
	ConfigRev string `json:"config_revision,omitempty"`
	// IsCurrent is false when this observation was produced under a generation
	// identity — controller instance or config revision — that is no longer the
	// live one. It is a comparison of two identities, not a health judgment: a
	// false value says the facts are real but describe an earlier generation,
	// and says nothing about whether that is a problem.
	IsCurrent bool `json:"is_current"`
}

// CycleTrigger names what caused a reconciliation cycle to run. The values
// mirror the controller's existing tick triggers.
type CycleTrigger string

// The trigger vocabulary. Anything the controller reports outside this set is
// published as TriggerUnknown rather than passed through, so the field stays a
// closed set consumers can switch on.
const (
	TriggerPatrol         CycleTrigger = "patrol"
	TriggerPoke           CycleTrigger = "poke"
	TriggerStartup        CycleTrigger = "startup"
	TriggerReloadFollowup CycleTrigger = "reload_followup"
	TriggerControl        CycleTrigger = "control"
	TriggerUnknown        CycleTrigger = "unknown"
)

// CycleCompletion is how a cycle ended. Aborted and PanicRecovered are
// published, not suppressed: a cycle that died partway is the case a consumer
// most needs to see, and hiding it would leave the previous cycle's facts
// standing as if they were current.
type CycleCompletion string

// How a cycle ended. All four are published; an aborted or panic-recovered
// cycle is the case a consumer most needs to see.
const (
	CompletionCompleted      CycleCompletion = "completed"
	CompletionAborted        CycleCompletion = "aborted"
	CompletionPanicRecovered CycleCompletion = "panic_recovered"
	CompletionTraceError     CycleCompletion = "trace_error"
)

// Cycle describes the reconciliation cycle this observation came from.
type Cycle struct {
	TickID string `json:"tick_id"`
	// TraceID is present only when reconciler tracing was enabled for the
	// cycle. Its absence means the trace was off, not that the cycle failed.
	TraceID       string          `json:"trace_id,omitempty"`
	Trigger       CycleTrigger    `json:"trigger"`
	TriggerDetail string          `json:"trigger_detail,omitempty"`
	StartedAt     time.Time       `json:"started_at"`
	EndedAt       time.Time       `json:"ended_at"`
	DurationMS    int64           `json:"duration_ms"`
	Completion    CycleCompletion `json:"completion"`
	// Reconciled is false when the cycle ended before the bead-reconcile pass
	// ran at all, so Totals, Completeness and Templates carry no observation
	// from this cycle. It separates "reconciled and found nothing" from "never
	// got that far", which a zero-valued Totals cannot do on its own.
	Reconciled bool `json:"reconciled"`
}

// Completeness reports whether the queries behind this cycle's counts were
// whole. These are the controller's own flags, copied verbatim; none is
// derived, and none is collapsed into a summary boolean, because collapsing
// them is what turns a failed query into an apparent zero.
type Completeness struct {
	// StoreQueryPartial is set when one or more bead store work queries failed.
	StoreQueryPartial bool `json:"store_query_partial"`
	// SessionQueryPartial is set when the session view for the cycle was
	// incomplete: a failed primary session-bead snapshot, or a partial
	// cross-store session census.
	SessionQueryPartial bool `json:"session_query_partial"`
	// SessionSnapshotComplete is the POSITIVE proof that the session census was
	// whole. It is not the negation of SessionQueryPartial: it is false for a
	// nil or degraded primary snapshot as well, so a consumer that needs
	// certainty reads this rather than inferring from the absence of a flag.
	SessionSnapshotComplete bool `json:"session_snapshot_complete"`
	// ContinuationClaimQueryPartial is set when the continuation-claim
	// candidate read was incomplete or internally contradictory.
	ContinuationClaimQueryPartial bool `json:"continuation_claim_query_partial"`

	// The templates whose demand probe failed, by the three scopes the
	// controller distinguishes. Sorted, so two observations of the same state
	// serialize identically.
	ScaleCheckPartialTemplates      []string `json:"scale_check_partial_templates,omitempty"`
	PoolScaleCheckPartialTemplates  []string `json:"pool_scale_check_partial_templates,omitempty"`
	NamedScaleCheckPartialTemplates []string `json:"named_scale_check_partial_templates,omitempty"`
}

// Totals are whole-city counts for the cycle. They summarize cardinality
// without enumerating it, which is what keeps a read independent of how many
// beads, tasks or sessions the city holds.
type Totals struct {
	DesiredSessionCount int `json:"desired_session_count"`
	OpenSessionCount    int `json:"open_session_count"`
	ReadyWaitCount      int `json:"ready_wait_count"`
	WorkSetCount        int `json:"work_set_count"`
}

// EvaluationStatus is what the cycle concluded about a template's INPUTS. It
// describes the demand read, not the template's health: Skipped means no demand
// was observed, which is the normal state of an idle template.
type EvaluationStatus string

// What the cycle concluded about a template's demand inputs.
const (
	EvaluationEligible          EvaluationStatus = "eligible"
	EvaluationDependencyBlocked EvaluationStatus = "dependency_blocked"
	EvaluationStorePartial      EvaluationStatus = "store_partial"
	EvaluationSkipped           EvaluationStatus = "skipped"
)

// TemplateRow is one configured template's facts for the cycle. There is one
// row per template, never one per session or per bead.
type TemplateRow struct {
	Template        string           `json:"template"`
	DesiredCount    int              `json:"desired_count"`
	OpenCount       int              `json:"open_count"`
	PoolDesired     int              `json:"pool_desired"`
	WorkRequested   bool             `json:"work_requested"`
	ScaleCheckCount int              `json:"scale_check_count"`
	Evaluation      EvaluationStatus `json:"evaluation"`
	// Reason carries the controller's own reason vocabulary for the evaluation
	// above. It is an opaque stable string to the consumer.
	Reason string `json:"reason,omitempty"`
	// DemandPartial marks a template whose demand probe failed in any scope, so
	// its counts must not be read as a complete picture.
	DemandPartial bool `json:"demand_partial,omitempty"`
}

// TraceState says what the reconciler trace did during the cycle. The
// observation does not depend on the trace and is published whether or not it
// ran; these fields exist so a consumer can tell "the trace has no record of
// this cycle" from "this cycle did not happen".
type TraceState struct {
	Enabled            bool `json:"enabled"`
	DroppedRecordCount int  `json:"dropped_record_count,omitempty"`
	DroppedBatchCount  int  `json:"dropped_batch_count,omitempty"`
}

// ClaimLeaseObservation reports the controller's native claim-lease work for
// the most recent attempt. Timestamps are instants rather than computed ages;
// a zero LastSuccessfulAt means this process has not completed a fully
// successful pass yet. Store rows are bounded by the configured city and rig
// set, and counts describe only operations this controller observed.
type ClaimLeaseObservation struct {
	LastAttemptAt    time.Time                    `json:"last_attempt_at,omitempty"`
	LastSuccessfulAt time.Time                    `json:"last_successful_at,omitempty"`
	Stores           []ClaimLeaseStoreObservation `json:"stores"`
}

// ClaimLeaseStoreObservation reports native lease operations for one named
// store. Errors count failed reads or native operations; a fenced, draining,
// or otherwise non-renewable owner contributes no renewal count.
type ClaimLeaseStoreObservation struct {
	Store     string `json:"store"`
	Renewed   int    `json:"renewed"`
	Reclaimed int    `json:"reclaimed"`
	Errors    int    `json:"errors"`
}
