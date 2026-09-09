package externalcoordination

// The delivery state machine is the shared vocabulary for one request's
// durable lifecycle. Parallel implementers consume this table unchanged; a
// sibling boundary must not invent a provider-specific interpretation of
// uncertain, reconciled, responded, or outcome_recorded.
//
// The load-bearing rule is that uncertain has exactly one successor. A
// submission whose receipt was lost may already have been observed by the
// recipient, so it must be reconciled against the same target and idempotency
// key before any retry, fallback, or terminal failure is permitted.
var allowedTransitions = map[DeliveryState][]DeliveryState{
	StateQueued: {StateRunning, StateUncertain, StateFailed, StateExpired, StateCancelled},
	// Running is the in-flight sub-state of queued: claimed by a dispatcher,
	// with no submission receipt yet. It can still reach a response state
	// directly, because a validated, correlated response is itself proof the
	// recipient observed the request and supersedes an unrecorded receipt.
	StateRunning:    {StateSubmitted, StateUncertain, StateResponded, StateOutcomeRecorded, StateFailed, StateExpired, StateCancelled},
	StateSubmitted:  {StateResponded, StateOutcomeRecorded, StateUncertain, StateFailed, StateExpired, StateCancelled},
	StateUncertain:  {StateReconciled},
	StateReconciled: {StateSubmitted, StateFailed, StateUncertain},
	// Responded is self-looping: a response that requires follow-up explicitly
	// invites another correlated response before the outcome is recorded.
	StateResponded:       {StateResponded, StateOutcomeRecorded, StateFailed, StateCancelled},
	StateAccepted:        {StateQueued, StateRunning, StateFailed},
	StateCompleted:       {},
	StateFailed:          {},
	StateExpired:         {},
	StateCancelled:       {},
	StateOutcomeRecorded: {},
}

// terminalStates are the states from which only an exact replay is permitted.
var terminalStates = map[DeliveryState]bool{
	StateOutcomeRecorded: true,
	StateCompleted:       true,
	StateFailed:          true,
	StateExpired:         true,
	StateCancelled:       true,
}

// CanTransition reports whether the delivery policy permits moving a request
// from one durable state to another. It is the single source of truth for
// transition legality; callers must not hand-roll state comparisons.
func CanTransition(from, to DeliveryState) bool {
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// IsTerminal reports whether a state permits no further transition. A terminal
// record still accepts an exact replay of the write that produced it, which is
// how a caller that lost a response safely retries.
func IsTerminal(state DeliveryState) bool { return terminalStates[state] }
