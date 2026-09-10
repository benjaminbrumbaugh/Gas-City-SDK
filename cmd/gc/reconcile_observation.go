package main

// Controller-side production of the reconciliation observation (gc-stb7g).
//
// The observation is a SIBLING of the reconciler trace, never a child of it.
// That separation is the whole point of the file, and it is not stylistic:
//
//   - BeginCycle returns nil whenever tracing is off — GC_SESSION_RECONCILER_TRACE=0,
//     or any trace-store construction failure, which degrades the tracer to
//     enabled:false. An observation hung off the trace cycle would disappear in
//     exactly the degraded conditions an operator most wants to observe.
//   - The trace is disk I/O behind a bounded flush budget that DROPS records
//     under slow storage. The observation must survive a dropped flush, because
//     the drop is itself a fact worth reporting (Trace.DroppedRecordCount).
//   - At cycle_result the trace record writes its rollup into an untyped
//     Fields map rather than into its own typed fields, so those fields cannot
//     be lifted into an API schema by renaming.
//
// So nothing here is guarded by `trace != nil`, and none of it reads or writes
// a trace file.
//
// Lifetime. One observation is built per tick and published once, at the end,
// as a single immutable value. Because assembly happens at cycle end rather
// than per-field at read time, a reader can never see fields from two cycles —
// which rules out by construction the failure of combining pre-restart demand
// with post-restart runtime state.

import (
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	sessionpkg "github.com/gastownhall/gascity/internal/session"

	"github.com/gastownhall/gascity/internal/reconcileobservation"
)

// reconcileObservationState holds the in-flight build and the published value.
//
// `published` and `liveGen` are atomic so a reader never takes a lock the tick
// could be holding: an API read must not be able to wait on the controller.
// `mu` guards only the in-flight build, which is touched by the tick alone.
type reconcileObservationState struct {
	mu       sync.Mutex
	inFlight *reconcileobservation.Observation

	genOnce   sync.Once
	gen       reconcileobservation.Generation
	tickSeq   atomic.Uint64
	liveGen   atomic.Pointer[reconcileobservation.Generation]
	published atomic.Pointer[reconcileobservation.Observation]
}

// generation returns this controller generation's stable identity, capturing it
// on first use. Identity is deliberately NOT read from the tracer: the tracer's
// disabled constructor returns a struct with a zero host, pid and start time,
// so sourcing identity from it would make the observation's provenance depend
// on whether tracing happened to be on.
func (s *reconcileObservationState) generation(configRev string) reconcileobservation.Generation {
	s.genOnce.Do(func() {
		host, _ := os.Hostname()
		pid := os.Getpid()
		s.gen = reconcileobservation.Generation{
			InstanceID: fmt.Sprintf("%s:%d", host, pid),
			PID:        pid,
			StartedAt:  time.Now().UTC(),
			Host:       host,
			GCVersion:  version,
			GCCommit:   commit,
			BuildDate:  date,
		}
	})
	g := s.gen
	g.ConfigRev = configRev
	return g
}

// beginReconcileObservation opens the tick's observation. It is called for
// every tick, including ticks that will not reach the bead-reconcile pass, so
// that a cycle which dies early is still reported rather than leaving the
// previous cycle's facts standing as though they were current.
//
// PRECONDITION: at most one tick is in flight at a time. There is a single
// in-flight slot, so two overlapping ticks would let the first one's defer
// publish the second one's half-filled value. That holds today — runTick is
// invoked synchronously from the one controller loop and is never spawned into
// a goroutine — and TestReconcileObservationTicksAreSerialized pins it, because
// the coherence guarantee this resource offers depends on it.
func (cr *CityRuntime) beginReconcileObservation(trigger, detail, traceID string, now time.Time) {
	gen := cr.reconcileObs.generation(cr.configRev)
	// Published for the accessor's is-current comparison. Updated at tick
	// start, so an observation is stale as soon as the next cycle runs under a
	// different config revision.
	cr.reconcileObs.liveGen.Store(&gen)

	seq := cr.reconcileObs.tickSeq.Add(1)
	obs := &reconcileobservation.Observation{
		SchemaVersion: reconcileobservation.SchemaVersion,
		City:          cr.cityName,
		Generation:    gen,
		Cycle: reconcileobservation.Cycle{
			// Same shape as the trace tick id, so the two are correlatable by
			// eye when both exist, without the observation depending on one.
			TickID:        fmt.Sprintf("%s-%d-%d-%06d", cr.cityName, gen.PID, gen.StartedAt.UnixNano(), seq),
			TraceID:       traceID,
			Trigger:       reconcileObservationTrigger(trigger),
			TriggerDetail: detail,
			StartedAt:     now.UTC(),
		},
	}
	cr.reconcileObs.mu.Lock()
	cr.reconcileObs.inFlight = obs
	cr.reconcileObs.mu.Unlock()
}

// reconcileObservationTrigger maps the controller's trigger string onto the
// published vocabulary. An unrecognized trigger becomes "unknown" rather than
// being passed through, so the field stays a closed set for consumers.
func reconcileObservationTrigger(trigger string) reconcileobservation.CycleTrigger {
	switch reconcileobservation.CycleTrigger(trigger) {
	case reconcileobservation.TriggerPatrol,
		reconcileobservation.TriggerPoke,
		reconcileobservation.TriggerStartup,
		reconcileobservation.TriggerReloadFollowup,
		reconcileobservation.TriggerControl:
		return reconcileobservation.CycleTrigger(trigger)
	default:
		return reconcileobservation.TriggerUnknown
	}
}

// observeReconcileInputs records the cycle's factual inputs into the in-flight
// observation. It is called from the same place the trace input summary is
// built, over the values that pass has already computed, so it adds no query.
//
// Every count here is paired with the completeness flags that say whether the
// query behind it was whole. A failed source is never serialized as zero.
func (cr *CityRuntime) observeReconcileInputs(
	openCounts map[string]int,
	desiredCounts map[string]int,
	poolDesired map[string]int,
	workSet map[string]bool,
	workRequested map[string]bool,
	readyWaitSet map[string]bool,
	templateNames map[string]struct{},
	openInfoCount int,
	desiredStateCount int,
	result DesiredStateResult,
) {
	cr.reconcileObs.mu.Lock()
	defer cr.reconcileObs.mu.Unlock()
	obs := cr.reconcileObs.inFlight
	if obs == nil {
		// A direct beadReconcileTick call outside a tick (tests, boot
		// reconcile). There is no cycle to attribute these inputs to, and
		// inventing one would publish an observation no cycle produced.
		return
	}

	obs.Cycle.Reconciled = true
	obs.Totals = reconcileobservation.Totals{
		DesiredSessionCount: desiredStateCount,
		OpenSessionCount:    openInfoCount,
		ReadyWaitCount:      len(readyWaitSet),
		WorkSetCount:        len(workSet),
	}
	obs.Completeness = reconcileobservation.Completeness{
		StoreQueryPartial:               result.StoreQueryPartial,
		SessionQueryPartial:             result.SessionQueryPartial,
		SessionSnapshotComplete:         result.SessionSnapshotComplete,
		ContinuationClaimQueryPartial:   result.ContinuationClaimQueryPartial,
		ScaleCheckPartialTemplates:      sortedBoolMapKeys(result.ScaleCheckPartialTemplates),
		PoolScaleCheckPartialTemplates:  sortedBoolMapKeys(result.PoolScaleCheckPartialTemplates),
		NamedScaleCheckPartialTemplates: sortedBoolMapKeys(result.NamedScaleCheckPartialTemplates),
	}

	names := traceSetStrings(templateNames)
	obs.TemplateCount = len(names)
	if len(names) > reconcileobservation.MaxTemplates {
		names = names[:reconcileobservation.MaxTemplates]
		obs.TemplatesTruncated = true
	}
	rows := make([]reconcileobservation.TemplateRow, 0, len(names))
	for _, template := range names {
		partial := result.ScaleCheckPartialTemplates[template] ||
			result.PoolScaleCheckPartialTemplates[template] ||
			result.NamedScaleCheckPartialTemplates[template]
		row := reconcileobservation.TemplateRow{
			Template:        template,
			DesiredCount:    desiredCounts[template],
			OpenCount:       openCounts[template],
			PoolDesired:     poolDesired[template],
			WorkRequested:   workRequested[template],
			ScaleCheckCount: result.ScaleCheckCounts[template],
			DemandPartial:   partial,
		}
		// The evaluation mirrors the trace's per-template rule, with one
		// deliberate addition: a template whose demand probe failed reports
		// store_partial rather than skipped. The trace does not make that
		// distinction, and without it a template with a failed probe is
		// indistinguishable from one with genuinely no demand — which is the
		// exact "query failure serialized as zero" this resource must not do.
		switch {
		case partial:
			row.Evaluation = reconcileobservation.EvaluationStorePartial
			row.Reason = string(TraceReasonStoreQueryPartial)
		case row.DesiredCount == 0 && row.PoolDesired == 0 && row.OpenCount == 0:
			row.Evaluation = reconcileobservation.EvaluationSkipped
			row.Reason = string(TraceReasonNoDemand)
		default:
			row.Evaluation = reconcileobservation.EvaluationEligible
			row.Reason = string(TraceReasonRetained)
		}
		rows = append(rows, row)
	}
	obs.Templates = rows
}

// publishReconcileObservation finalizes the in-flight observation and makes it
// the latest one. It must be called from the tick's own defer so that a cycle
// which aborted or panicked still publishes, carrying the completion status
// that says so.
func (cr *CityRuntime) publishReconcileObservation(completion TraceCompletionStatus, traceCycle *sessionReconcilerTraceCycle, now time.Time) {
	cr.reconcileObs.mu.Lock()
	obs := cr.reconcileObs.inFlight
	cr.reconcileObs.inFlight = nil
	cr.reconcileObs.mu.Unlock()
	if obs == nil {
		return
	}

	obs.Cycle.EndedAt = now.UTC()
	obs.Cycle.DurationMS = obs.Cycle.EndedAt.Sub(obs.Cycle.StartedAt).Milliseconds()
	obs.Cycle.Completion = reconcileObservationCompletion(completion)
	obs.Trace = reconcileObservationTraceState(traceCycle)
	claimLeases := cr.claimLeaseObservationWire()
	obs.ClaimLeases = &claimLeases
	if obs.Templates == nil {
		// A nil slice serializes as JSON null and an empty one as []. The
		// difference is visible to every consumer, and "no rows" is the honest
		// reading of a cycle that never reached the reconcile pass — which
		// Cycle.Reconciled already states.
		obs.Templates = []reconcileobservation.TemplateRow{}
	}
	cr.reconcileObs.published.Store(obs)
}

func (cr *CityRuntime) claimLeaseObservationWire() reconcileobservation.ClaimLeaseObservation {
	result := cr.claimLeaseReconciliation()
	observation := reconcileobservation.ClaimLeaseObservation{
		LastAttemptAt:    result.LastAttemptAt,
		LastSuccessfulAt: result.LastSuccessfulAt,
		Stores:           make([]reconcileobservation.ClaimLeaseStoreObservation, 0, len(result.Stores)),
	}
	for _, store := range result.Stores {
		observation.Stores = append(observation.Stores, reconcileobservation.ClaimLeaseStoreObservation{
			Store:     store.Name,
			Renewed:   store.Renewed,
			Reclaimed: store.Reclaimed,
			Errors:    store.Errors,
		})
	}
	return observation
}

func reconcileObservationCompletion(completion TraceCompletionStatus) reconcileobservation.CycleCompletion {
	switch completion {
	case TraceCompletionCompleted:
		return reconcileobservation.CompletionCompleted
	case TraceCompletionPanicRecovered:
		return reconcileobservation.CompletionPanicRecovered
	case TraceCompletionTraceError:
		return reconcileobservation.CompletionTraceError
	default:
		return reconcileobservation.CompletionAborted
	}
}

// traceCycleID returns the trace id of a cycle, or "" when tracing is off. The
// observation carries it purely so a reader holding both can line them up; the
// observation never depends on the trace existing.
func traceCycleID(c *sessionReconcilerTraceCycle) string {
	if c == nil {
		return ""
	}
	return c.traceID
}

// reconcileObservationTraceState reports what the trace did, without depending
// on it having done anything. A nil cycle means tracing was off for this tick.
func reconcileObservationTraceState(c *sessionReconcilerTraceCycle) reconcileobservation.TraceState {
	if c == nil {
		return reconcileobservation.TraceState{Enabled: false}
	}
	dropped, batches := c.droppedCounts()
	return reconcileobservation.TraceState{
		Enabled:            true,
		DroppedRecordCount: dropped,
		DroppedBatchCount:  batches,
	}
}

// ReconciliationObservation returns the latest published observation, or nil
// when the controller has not completed a cycle yet. Nil is the caller's 503:
// "no observation" and "an observation of nothing" are different answers and
// must not be collapsed.
//
// This is the read path. It loads two atomic pointers, copies one struct and
// returns — no lock, no store, no runtime provider, no file. A read can never
// block on the tick, and its cost does not vary with how many beads, tasks or
// sessions the city holds.
func (cr *CityRuntime) ReconciliationObservation() *reconcileobservation.Observation {
	obs := cr.reconcileObs.published.Load()
	if obs == nil {
		return nil
	}
	out := *obs
	if live := cr.reconcileObs.liveGen.Load(); live != nil {
		out.Generation.IsCurrent = live.InstanceID == out.Generation.InstanceID &&
			live.ConfigRev == out.Generation.ConfigRev
	}
	return &out
}

// countReconcileTemplates builds the per-template tallies for one cycle. It is
// shared by the trace input summary and the observation so the two can never
// drift into reporting different numbers for the same tick.
// visit, when non-nil, is called once per open session whose template
// resolved, with the normalized template. It exists so a traced tick emits its
// per-session baseline from inside this pass instead of resolving every
// session's template a second time.
func (cr *CityRuntime) countReconcileTemplates(
	openInfos []sessionpkg.Info,
	desiredState map[string]TemplateParams,
	poolDesired map[string]int,
	workSet map[string]bool,
	workRequested map[string]bool,
	visit func(info sessionpkg.Info, template string),
) (templateNames map[string]struct{}, openCounts, desiredCounts map[string]int) {
	templateNames = make(map[string]struct{})
	openCounts = make(map[string]int)
	desiredCounts = make(map[string]int)
	for _, info := range openInfos {
		template := normalizedSessionTemplateInfo(info, cr.cfg)
		if template == "" {
			continue
		}
		templateNames[template] = struct{}{}
		openCounts[template]++
		if visit != nil {
			visit(info, template)
		}
	}
	for _, tp := range desiredState {
		if tp.TemplateName == "" {
			continue
		}
		templateNames[tp.TemplateName] = struct{}{}
		desiredCounts[tp.TemplateName]++
	}
	for template := range poolDesired {
		templateNames[template] = struct{}{}
	}
	for template := range workSet {
		templateNames[template] = struct{}{}
	}
	for template := range workRequested {
		templateNames[template] = struct{}{}
	}
	return templateNames, openCounts, desiredCounts
}

// reconciliationSnapshotReply is the controller-socket reply for
// `gc trace snapshot`. Observation is nil when nothing has been published.
type reconciliationSnapshotReply struct {
	OK          bool                              `json:"ok"`
	Error       string                            `json:"error,omitempty"`
	Observation *reconcileobservation.Observation `json:"observation,omitempty"`
}

// handleReconciliationSnapshotSocketCmd serves the latest observation over the
// controller socket.
//
// The socket rather than the HTTP API, because every other `gc trace`
// subcommand already speaks it and because a diagnostic must still answer when
// the API is disabled. Either way the value is the same in-memory one: this
// path reads no trace segment and opens no store, which is what separates
// `gc trace snapshot` from `gc trace status`.
func handleReconciliationSnapshotSocketCmd(conn net.Conn, observe func() *reconcileobservation.Observation) {
	if observe == nil {
		writeJSONLine(conn, reconciliationSnapshotReply{
			OK:    false,
			Error: "no reconciliation observation is available: this controller has no city runtime attached",
		})
		return
	}
	obs := observe()
	if obs == nil {
		// Not an empty observation. A controller that has not finished its
		// first cycle has published nothing, and reporting that as an
		// observation of an idle city is the false-green the resource exists
		// to avoid.
		writeJSONLine(conn, reconciliationSnapshotReply{
			OK:    false,
			Error: "no reconciliation observation is available: the controller has not completed a cycle yet",
		})
		return
	}
	writeJSONLine(conn, reconciliationSnapshotReply{OK: true, Observation: obs})
}
