package routingdecision

// Outcome authority records: the typed seam through which causal truth
// (session identity, execution identity, terminal disposition) enters the
// outcome projection. Every field is populated exclusively from a real
// SDK-owned record read — never from open-world work-bead metadata.
//
// # Authoritative sources (exact identities)
//
//   - Session identity: internal/session.Store.Get over the session-class
//     bead store (internal/session/info_store.go). Get validates that the
//     referenced bead IS a session bead, so an arbitrary gc.session_id value
//     copied onto a work bead cannot satisfy this seam.
//   - Execution identity: the execution event journal
//     (internal/events, internal/executionevent). execution.step_completed /
//     execution.step_started facts are minted solely by
//     executionevent.EmitLifecycle, which loads the workflow root from the
//     graph store and rejects any bead whose gc.root_bead_id does not name an
//     authoritative graph.v2 workflow root — metadata resemblance alone can
//     never produce a lifecycle fact.
//
// # Known gap (documented, deliberate)
//
// No durable SDK record currently carries a TERMINAL DISPOSITION for routed
// work: gc.work_outcome is caller-authored open-world metadata, not an
// authority record, and the event journal records step lifecycle but not a
// typed close disposition bound to decision/work/target/config. Until such a
// record exists, TerminalDispositionKnown stays false in every production
// projection, succeeded/failed-known outcomes are unreachable, and the
// projector fails closed to disposition=unknown. This is the explicit typed
// unavailable seam required by the routing/outcome/v2 causal-truth boundary;
// the seam is intentionally NOT manufactured from metadata to make tests
// pass. Positive-path dispositions are exercised in tests by constructing
// the snapshot directly, which is exactly the trust a future authoritative
// writer would have to earn.

// OutcomeSessionRecord is the minimum session-side causal identity consumed
// by the projector, read from one exact session.Store.Get.
type OutcomeSessionRecord struct {
	SessionID string
}

// OutcomeExecutionRecord is the minimum execution-side causal identity
// consumed by the projector, read from validated execution event facts plus
// the referenced session record.
type OutcomeExecutionRecord struct {
	ExecutionID string
	// SessionID is the session the execution event bound itself to at the
	// record site. It must equal OutcomeSessionRecord.SessionID or the
	// evidence is ambiguous and the projection fails closed.
	SessionID string
	// Completed reports that a terminal execution lifecycle fact
	// (execution.step_completed) was observed for this run. A started-but-
	// never-completed run yields unknown, never guessed.
	Completed bool
}

// OutcomeAuthoritySnapshot is the bounded causal-evidence bundle handed to
// ProjectOutcome alongside the signed decision and the exact work carrier.
// A nil pointer means that record was unavailable; unavailable records are
// fail-closed evidence gaps, not licenses to infer.
type OutcomeAuthoritySnapshot struct {
	Session   *OutcomeSessionRecord
	Execution *OutcomeExecutionRecord

	// TerminalDispositionKnown reports that an authoritative record carrying
	// a typed terminal disposition was demonstrated. It is false in every
	// production projection today (see package gap note above).
	TerminalDispositionKnown bool
	// Disposition and FailureClass are consumed only when
	// TerminalDispositionKnown is true.
	Disposition  OutcomeDisposition
	FailureClass OutcomeFailureClass
}

// authoritativeCausalIdentity resolves the session/execution identity pair
// from the authority snapshot. Identity is published only when BOTH records
// are present and bind to the same session; anything missing or ambiguous is
// dropped (fail closed) rather than partially reported.
func (snapshot OutcomeAuthoritySnapshot) authoritativeCausalIdentity() (*string, *string) {
	if snapshot.Session == nil || snapshot.Execution == nil {
		return nil, nil
	}
	if snapshot.Session.SessionID == "" || snapshot.Execution.ExecutionID == "" ||
		snapshot.Session.SessionID != snapshot.Execution.SessionID {
		return nil, nil
	}
	sessionID := snapshot.Session.SessionID
	executionID := snapshot.Execution.ExecutionID
	return &sessionID, &executionID
}
