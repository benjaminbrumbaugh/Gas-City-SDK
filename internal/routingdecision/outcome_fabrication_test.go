package routingdecision

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// signedOutcomeFixture builds an outcome-recorded decision with a valid
// recommendation link and the durable admission receipt identity the ledger
// attaches at FinalAdmission time.
func signedOutcomeFixture(t *testing.T) DecisionWithAudits {
	t.Helper()
	payload := testDecisionPayload(t)
	payload.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
	payload.BindingID = BindingID(payload)
	return DecisionWithAudits{
		Record:             Record{Payload: payload, State: StateOutcomeRecorded},
		AdmissionReceiptID: "controller-admit:decision-1:2",
	}
}

// fabricatedExactCarrier returns a closed, exact-bound work carrier whose open
// metadata map carries everything a caller would need to fabricate a success:
// a typed work outcome, a session id, a current run id, and a failure class.
// None of these values were written by an SDK authority; they are ordinary
// open-world metadata any writer could stamp.
func fabricatedExactCarrier(payload DecisionPayload) OutcomeWorkSnapshot {
	return OutcomeWorkSnapshot{
		Found: true, WorkID: payload.WorkBeadID, Status: "closed", ClaimFence: payload.ClaimFence,
		Metadata: map[string]string{
			beadmeta.RoutingDecisionIDMetadataKey:         payload.DecisionID,
			beadmeta.RoutingDecisionClaimFenceMetadataKey: strconv.FormatInt(payload.ClaimFence, 10),
			beadmeta.RunTargetMetadataKey:                 payload.Target,
			beadmeta.WorkOutcomeMetadataKey:               "shipped",
			beadmeta.FailureClassMetadataKey:              "hard",
			beadmeta.SessionIDMetadataKey:                 "session-fabricated",
			beadmeta.CurrentRunIDMetadataKey:              "execution-fabricated",
		},
	}
}

// TestProjectOutcomeNeverDerivesTruthFromFabricatedWorkMetadata pins the
// causal-truth boundary: a closed exact work bead carrying fabricated
// gc.work_outcome, gc.session_id, gc.current_run_id, and gc.failure_class can
// NEVER produce a succeeded/failed-known disposition, session identity, or
// execution identity, because the projector consumes causal truth only from
// the typed authority-record seam — never from open-world work metadata.
func TestProjectOutcomeNeverDerivesTruthFromFabricatedWorkMetadata(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	item := signedOutcomeFixture(t)
	got := ProjectOutcome(item, fabricatedExactCarrier(item.Record.Payload), OutcomeAuthoritySnapshot{}, now)
	if err := got.Validate(); err != nil {
		t.Fatalf("fail-closed projection is schema-invalid: %v: %+v", err, got)
	}
	if got.Status == OutcomeStatusSucceeded || got.Disposition != OutcomeDispositionUnknown ||
		got.FailureClass != OutcomeFailureUnknown {
		t.Fatalf("fabricated metadata invented truth: %+v", got)
	}
	if got.SessionID != nil || got.ExecutionID != nil {
		t.Fatalf("fabricated metadata leaked identity: %+v", got)
	}
	if got.Provenance != OutcomeProvenanceExactWork || got.Coverage != OutcomeCoveragePartial {
		t.Fatalf("exact carrier evidence was discarded rather than fail-closed: %+v", got)
	}
}

// TestProjectOutcomeRejectsAmbiguousAuthorityBindings fails the projection
// closed when the authority records themselves disagree: a session record
// whose identity does not match the terminal execution record's session is
// ambiguous evidence, never a guessable success.
func TestProjectOutcomeRejectsAmbiguousAuthorityBindings(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	item := signedOutcomeFixture(t)
	sessionID, otherSession := "session-authority", "session-elsewhere"
	authority := OutcomeAuthoritySnapshot{
		Session:                  &OutcomeSessionRecord{SessionID: sessionID},
		Execution:                &OutcomeExecutionRecord{ExecutionID: "run-1", SessionID: otherSession, Completed: true},
		TerminalDispositionKnown: true,
		Disposition:              OutcomeDispositionShipped,
		FailureClass:             OutcomeFailureNone,
	}
	got := ProjectOutcome(item, fabricatedExactCarrier(item.Record.Payload), authority, now)
	if got.Status == OutcomeStatusSucceeded || got.Disposition != OutcomeDispositionUnknown ||
		got.FailureClass != OutcomeFailureUnknown || got.SessionID != nil || got.ExecutionID != nil {
		t.Fatalf("ambiguous authority invented truth: %+v", got)
	}
}

// TestProjectOutcomeProjectsSuccessOnlyFromAuthoritativeRecords is the
// positive control: when every layer binds exactly — signed decision, exact
// closed carrier, durable admission receipt, a real session record, a real
// terminal execution record bound to that same session, and an authoritative
// terminal disposition — the projection succeeds with the authoritative
// identities and no others.
func TestProjectOutcomeProjectsSuccessOnlyFromAuthoritativeRecords(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	item := signedOutcomeFixture(t)
	sessionID := "session-authority"
	authority := OutcomeAuthoritySnapshot{
		Session:                  &OutcomeSessionRecord{SessionID: sessionID},
		Execution:                &OutcomeExecutionRecord{ExecutionID: "run-authority", SessionID: sessionID, Completed: true},
		TerminalDispositionKnown: true,
		Disposition:              OutcomeDispositionShipped,
		FailureClass:             OutcomeFailureNone,
	}
	got := ProjectOutcome(item, fabricatedExactCarrier(item.Record.Payload), authority, now)
	if err := got.Validate(); err != nil {
		t.Fatalf("authoritative success is schema-invalid: %v: %+v", err, got)
	}
	if got.Status != OutcomeStatusSucceeded || got.Disposition != OutcomeDispositionShipped ||
		got.FailureClass != OutcomeFailureNone {
		t.Fatalf("authoritative records did not project success: %+v", got)
	}
	if got.SessionID == nil || *got.SessionID != sessionID || got.ExecutionID == nil || *got.ExecutionID != "run-authority" {
		t.Fatalf("authoritative identities missing: %+v", got)
	}
}

// TestProjectOutcomeProjectsKnownFailureOnlyFromAuthoritativeRecords proves a
// known failure class arrives only through the authoritative terminal record;
// the fabricated gc.failure_class=hard on the carrier is ignored.
func TestProjectOutcomeProjectsKnownFailureOnlyFromAuthoritativeRecords(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	item := signedOutcomeFixture(t)
	sessionID := "session-authority"
	authority := OutcomeAuthoritySnapshot{
		Session:                  &OutcomeSessionRecord{SessionID: sessionID},
		Execution:                &OutcomeExecutionRecord{ExecutionID: "run-authority", SessionID: sessionID, Completed: true},
		TerminalDispositionKnown: true,
		Disposition:              OutcomeDispositionBlocked,
		FailureClass:             OutcomeFailureTransient,
	}
	got := ProjectOutcome(item, fabricatedExactCarrier(item.Record.Payload), authority, now)
	if err := got.Validate(); err != nil {
		t.Fatalf("authoritative failure is schema-invalid: %v: %+v", err, got)
	}
	if got.Status != OutcomeStatusFailed || got.Disposition != OutcomeDispositionBlocked ||
		got.FailureClass != OutcomeFailureTransient {
		t.Fatalf("authoritative failure misprojected: %+v", got)
	}
	if got.SessionID == nil || *got.SessionID != sessionID {
		t.Fatalf("authoritative session identity missing: %+v", got)
	}
}
