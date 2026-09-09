package main

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/reconcileobservation"
)

// newObservationRuntime is the smallest runtime that can observe: the
// observation deliberately depends on no store, no provider and no trace.
func newObservationRuntime(t *testing.T) *CityRuntime {
	t.Helper()
	return &CityRuntime{cityName: "testcity", configRev: "rev-1"}
}

// runCycle drives exactly the sequence the tick performs: open the
// observation, record the cycle's inputs, publish from the defer.
func runCycle(cr *CityRuntime, trigger string, completion TraceCompletionStatus, fill func()) {
	cr.beginReconcileObservation(trigger, "controller_tick", "", time.Now())
	if fill != nil {
		fill()
	}
	cr.publishReconcileObservation(completion, nil, time.Now())
}

// --- 1. every completion publishes, including the ones that went wrong ------
//
// A cycle that aborted or panicked is the case a consumer most needs to see.
// If it published nothing, the PREVIOUS cycle's facts would keep standing as
// though they were current, which is a silent false-green.
func TestReconcileObservationPublishesEveryCompletion(t *testing.T) {
	for _, tc := range []struct {
		in   TraceCompletionStatus
		want reconcileobservation.CycleCompletion
	}{
		{TraceCompletionCompleted, reconcileobservation.CompletionCompleted},
		{TraceCompletionAborted, reconcileobservation.CompletionAborted},
		{TraceCompletionPanicRecovered, reconcileobservation.CompletionPanicRecovered},
		{TraceCompletionTraceError, reconcileobservation.CompletionTraceError},
	} {
		cr := newObservationRuntime(t)
		runCycle(cr, "patrol", tc.in, nil)
		got := cr.ReconciliationObservation()
		if got == nil {
			t.Fatalf("completion %q published nothing", tc.in)
		}
		if got.Cycle.Completion != tc.want {
			t.Fatalf("completion %q: got %q, want %q", tc.in, got.Cycle.Completion, tc.want)
		}
		if got.Cycle.TickID == "" {
			t.Fatalf("completion %q: no tick id", tc.in)
		}
	}
}

// An aborted cycle that never reached the reconcile pass must say so rather
// than presenting a zero-valued Totals as an observation of an empty city.
func TestReconcileObservationUnreconciledCycleSaysSo(t *testing.T) {
	cr := newObservationRuntime(t)
	runCycle(cr, "patrol", TraceCompletionAborted, nil)
	got := cr.ReconciliationObservation()
	if got.Cycle.Reconciled {
		t.Fatal("a cycle that never reached the reconcile pass reported Reconciled")
	}
	if got.Templates == nil {
		t.Fatal("Templates is nil; it must serialize as [] so a consumer cannot read null as an error")
	}
}

// --- 2. the observation does not depend on the trace ------------------------
//
// This is the whole reason the observation is not built on the trace cycle:
// BeginCycle returns nil whenever GC_SESSION_RECONCILER_TRACE=0 or the trace
// store fails to open, and those are exactly the degraded conditions worth
// observing.
func TestReconcileObservationPublishedWithoutTrace(t *testing.T) {
	cr := newObservationRuntime(t)
	runCycle(cr, "patrol", TraceCompletionCompleted, func() {
		cr.observeReconcileInputs(
			map[string]int{"a": 1}, map[string]int{"a": 1}, map[string]int{},
			map[string]bool{}, map[string]bool{}, map[string]bool{},
			map[string]struct{}{"a": {}}, 1, 1, DesiredStateResult{})
	})
	got := cr.ReconciliationObservation()
	if got == nil {
		t.Fatal("no observation was published with tracing off")
	}
	if got.Trace.Enabled {
		t.Fatal("Trace.Enabled is true for a cycle with no trace")
	}
	if got.Cycle.TraceID != "" {
		t.Fatalf("TraceID %q set for a cycle with no trace", got.Cycle.TraceID)
	}
	if !got.Cycle.Reconciled || got.Totals.OpenSessionCount != 1 {
		t.Fatalf("inputs were not observed without a trace: %+v", got)
	}
}

func TestReconcileObservationPublishesClaimLeaseDiagnostics(t *testing.T) {
	cr := newObservationRuntime(t)
	attempt := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	success := attempt.Add(time.Second)
	cr.claimLeaseMu.Lock()
	cr.claimLeaseResult = claimLeaseReconcileResult{
		LastAttemptAt:    attempt,
		LastSuccessfulAt: success,
		Stores:           []claimLeaseStoreReport{{Name: "city", Renewed: 2, Reclaimed: 1, Errors: 0}},
	}
	cr.claimLeaseMu.Unlock()

	runCycle(cr, "patrol", TraceCompletionCompleted, nil)
	got := cr.ReconciliationObservation()
	if !got.ClaimLeases.LastAttemptAt.Equal(attempt) || !got.ClaimLeases.LastSuccessfulAt.Equal(success) {
		t.Fatalf("claim lease timestamps = %#v, want controller result", got.ClaimLeases)
	}
	if len(got.ClaimLeases.Stores) != 1 || got.ClaimLeases.Stores[0].Store != "city" ||
		got.ClaimLeases.Stores[0].Renewed != 2 || got.ClaimLeases.Stores[0].Reclaimed != 1 {
		t.Fatalf("claim lease store diagnostics = %#v, want typed counters", got.ClaimLeases.Stores)
	}
}

// --- 3. a failed query is never serialized as a zero ------------------------
func TestReconcileObservationPartialIsNotZero(t *testing.T) {
	cr := newObservationRuntime(t)
	result := DesiredStateResult{
		StoreQueryPartial:          true,
		SessionQueryPartial:        true,
		SessionSnapshotComplete:    false,
		ScaleCheckPartialTemplates: map[string]bool{"broken": true},
		ScaleCheckCounts:           map[string]int{"broken": 3},
	}
	runCycle(cr, "patrol", TraceCompletionCompleted, func() {
		cr.observeReconcileInputs(
			map[string]int{"broken": 2}, map[string]int{}, map[string]int{},
			map[string]bool{}, map[string]bool{}, map[string]bool{},
			map[string]struct{}{"broken": {}, "fine": {}}, 2, 0, result)
	})
	got := cr.ReconciliationObservation()
	if !got.Completeness.StoreQueryPartial || !got.Completeness.SessionQueryPartial {
		t.Fatalf("partial flags lost: %+v", got.Completeness)
	}
	if got.Completeness.SessionSnapshotComplete {
		t.Fatal("SessionSnapshotComplete must not be inferred from the absence of a partial flag")
	}
	if len(got.Completeness.ScaleCheckPartialTemplates) != 1 ||
		got.Completeness.ScaleCheckPartialTemplates[0] != "broken" {
		t.Fatalf("partial template not named: %+v", got.Completeness)
	}
	var broken, fine *reconcileobservation.TemplateRow
	for i := range got.Templates {
		switch got.Templates[i].Template {
		case "broken":
			broken = &got.Templates[i]
		case "fine":
			fine = &got.Templates[i]
		}
	}
	if broken == nil || fine == nil {
		t.Fatalf("expected both templates, got %+v", got.Templates)
	}
	// The load-bearing assertion: a template whose demand probe FAILED must not
	// be indistinguishable from one with genuinely no demand.
	if broken.Evaluation != reconcileobservation.EvaluationStorePartial {
		t.Fatalf("a failed demand probe reported %q, not store_partial", broken.Evaluation)
	}
	if !broken.DemandPartial {
		t.Fatal("DemandPartial not set on a template with a failed probe")
	}
	if broken.OpenCount != 2 || broken.ScaleCheckCount != 3 {
		t.Fatalf("the controller's own counters were dropped for a partial template: %+v", broken)
	}
	if fine.Evaluation != reconcileobservation.EvaluationSkipped {
		t.Fatalf("a template with no demand reported %q, not skipped", fine.Evaluation)
	}
	if fine.DemandPartial {
		t.Fatal("DemandPartial leaked onto a template whose probe succeeded")
	}
}

// --- 4. concurrent reads never see a half-published or mixed cycle ----------
//
// Run with -race. Every read must be internally consistent: the totals and the
// template rows must belong to the same cycle as the tick id.
func TestReconcileObservationConcurrentReadsAreCoherent(t *testing.T) {
	cr := newObservationRuntime(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			n := i + 1
			runCycle(cr, "patrol", TraceCompletionCompleted, func() {
				names := map[string]struct{}{}
				opens := map[string]int{}
				for j := 0; j < n%5+1; j++ {
					k := fmt.Sprintf("t%d", j)
					names[k] = struct{}{}
					opens[k] = n
				}
				cr.observeReconcileInputs(opens, map[string]int{}, map[string]int{},
					map[string]bool{}, map[string]bool{}, map[string]bool{},
					names, n, 0, DesiredStateResult{})
			})
		}
		close(stop)
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				obs := cr.ReconciliationObservation()
				if obs == nil {
					continue
				}
				if !obs.Cycle.Reconciled {
					continue
				}
				// Every row of a coherent observation carries the same
				// OpenSessionCount the cycle's totals reported. A value
				// assembled from two cycles would not.
				for _, row := range obs.Templates {
					if row.OpenCount != obs.Totals.OpenSessionCount {
						t.Errorf("mixed cycle: tick %s totals=%d row %s open=%d",
							obs.Cycle.TickID, obs.Totals.OpenSessionCount, row.Template, row.OpenCount)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// --- 5. before the first cycle there is no observation, not an empty one ----
func TestReconcileObservationAbsentBeforeFirstCycle(t *testing.T) {
	cr := newObservationRuntime(t)
	if got := cr.ReconciliationObservation(); got != nil {
		t.Fatalf("an observation existed before any cycle ran: %+v", got)
	}
	// Opening a cycle is not publishing one: a cycle in flight must not be
	// readable, or a consumer could see a half-filled value.
	cr.beginReconcileObservation("patrol", "controller_tick", "", time.Now())
	if got := cr.ReconciliationObservation(); got != nil {
		t.Fatalf("an in-flight cycle was readable: %+v", got)
	}
}

// --- 6. generation skew is reported, not hidden -----------------------------
//
// A config reload inside one controller process changes the template set the
// counts are keyed by, so an observation taken under the earlier revision is
// describing a differently-configured city.
func TestReconcileObservationIsCurrentTracksConfigRevision(t *testing.T) {
	cr := newObservationRuntime(t)
	runCycle(cr, "patrol", TraceCompletionCompleted, nil)
	if got := cr.ReconciliationObservation(); !got.Generation.IsCurrent {
		t.Fatal("a fresh observation reported IsCurrent=false")
	}
	if got := cr.ReconciliationObservation(); got.Generation.ConfigRev != "rev-1" {
		t.Fatalf("config revision not carried: %q", got.Generation.ConfigRev)
	}

	// A reload, then a new cycle opens under the new revision.
	cr.configRev = "rev-2"
	cr.beginReconcileObservation("reload_followup", "manual_reload", "", time.Now())
	got := cr.ReconciliationObservation()
	if got == nil {
		t.Fatal("the previously published observation disappeared")
	}
	if got.Generation.IsCurrent {
		t.Fatal("an observation from a superseded config revision reported IsCurrent=true")
	}
	if got.Generation.ConfigRev != "rev-1" {
		t.Fatalf("the published observation's revision was rewritten to %q", got.Generation.ConfigRev)
	}
}

// --- 7. truncation is declared, never silent -------------------------------
func TestReconcileObservationTemplateTruncationIsDeclared(t *testing.T) {
	cr := newObservationRuntime(t)
	total := reconcileobservation.MaxTemplates + 17
	names := make(map[string]struct{}, total)
	for i := 0; i < total; i++ {
		names[fmt.Sprintf("t%04d", i)] = struct{}{}
	}
	runCycle(cr, "patrol", TraceCompletionCompleted, func() {
		cr.observeReconcileInputs(map[string]int{}, map[string]int{}, map[string]int{},
			map[string]bool{}, map[string]bool{}, map[string]bool{}, names, 0, 0, DesiredStateResult{})
	})
	got := cr.ReconciliationObservation()
	if !got.TemplatesTruncated {
		t.Fatal("truncation was silent")
	}
	if got.TemplateCount != total {
		t.Fatalf("TemplateCount reported %d, want the true %d", got.TemplateCount, total)
	}
	if len(got.Templates) != reconcileobservation.MaxTemplates {
		t.Fatalf("Templates length %d, want the cap %d", len(got.Templates), reconcileobservation.MaxTemplates)
	}
}

// --- 8. the trigger vocabulary is closed ------------------------------------
func TestReconcileObservationTriggerIsAClosedSet(t *testing.T) {
	cr := newObservationRuntime(t)
	runCycle(cr, "something-new", TraceCompletionCompleted, nil)
	if got := cr.ReconciliationObservation(); got.Cycle.Trigger != reconcileobservation.TriggerUnknown {
		t.Fatalf("an unrecognized trigger was passed through as %q", got.Cycle.Trigger)
	}
	cr2 := newObservationRuntime(t)
	runCycle(cr2, "poke", TraceCompletionCompleted, nil)
	if got := cr2.ReconciliationObservation(); got.Cycle.Trigger != reconcileobservation.TriggerPoke {
		t.Fatalf("a known trigger was mangled to %q", got.Cycle.Trigger)
	}
}

// --- 9. the model carries facts only --------------------------------------
//
// A structural test, because this is the property most likely to erode: the
// next person with a dashboard to fill wants one `status` field, and one is all
// it takes to move judgment from the consumer into the SDK.
func TestReconcileObservationModelCarriesNoVerdictOrAge(t *testing.T) {
	banned := regexp.MustCompile(`(?i)health|degraded|stuck|idle|severity|recommend|action|verdict|ok\b|age|elapsed|since_|_ago|overdue|expected_next`)
	// "started_at"/"ended_at" are instants, not ages, and must stay allowed.
	allowed := map[string]bool{"started_at": true, "ended_at": true}

	var walk func(reflect.Type, string)
	seen := map[reflect.Type]bool{}
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] || rt == reflect.TypeOf(time.Time{}) {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "" {
				tag = f.Name
			}
			where := path + "." + tag
			if !allowed[tag] && banned.MatchString(tag) {
				t.Errorf("%s: field %q reads as a verdict or a server-computed age; publish facts and let the consumer judge", where, tag)
			}
			if f.Type == reflect.TypeOf(time.Duration(0)) {
				t.Errorf("%s: time.Duration field %q — publish instants, not spans the server measured", where, tag)
			}
			// Nothing whose size tracks the workload, and nothing that could
			// carry a live handle out of the controller.
			if f.Type.Kind() == reflect.Interface {
				t.Errorf("%s: interface field %q could carry a live store or provider handle across the boundary", where, tag)
			}
			walk(f.Type, where)
		}
	}
	walk(reflect.TypeOf(reconcileobservation.Observation{}), "Observation")
}

// The model must also stay free of the controller's internal types. A
// beads.Store or session.Info reaching the wire is both a leak and an unbounded
// payload.
func TestReconcileObservationModelHasNoControllerTypes(t *testing.T) {
	rt := reflect.TypeOf(reconcileobservation.Observation{})
	pkg := rt.PkgPath()
	var walk func(reflect.Type, string)
	seen := map[reflect.Type]bool{}
	walk = func(ft reflect.Type, path string) {
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map {
			ft = ft.Elem()
		}
		if ft.PkgPath() != "" && ft.PkgPath() != pkg && ft.PkgPath() != "time" {
			t.Errorf("%s: type %s.%s is imported into the wire model", path, ft.PkgPath(), ft.Name())
			return
		}
		if ft.Kind() != reflect.Struct || seen[ft] || ft == reflect.TypeOf(time.Time{}) {
			return
		}
		seen[ft] = true
		for i := 0; i < ft.NumField(); i++ {
			walk(ft.Field(i).Type, path+"."+ft.Field(i).Name)
		}
	}
	walk(rt, "Observation")
}

// --- 10. the publish is wired where it cannot be skipped -------------------
//
// The three properties below cannot be reached by calling the function: they
// are about WHERE it is called from. Each one, if it drifted, would restore a
// failure the observation exists to prevent — a cycle that aborted publishing
// nothing, or the whole resource vanishing when tracing is switched off.
func TestReconcileObservationPublishIsWiredIntoTheTickDefer(t *testing.T) {
	src, err := os.ReadFile("city_runtime.go")
	if err != nil {
		t.Fatalf("read city_runtime.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "trace := cr.beginTraceCycle(traceTrigger, traceDetail, nil)")
	if start < 0 {
		t.Fatal("could not find the tick's trace-cycle opening")
	}
	end := strings.Index(body[start:], "cr.reconcilePoolDeaths(")
	if end < 0 {
		t.Fatal("could not find the end of the tick's cycle preamble")
	}
	preamble := body[start : start+end]

	if !strings.Contains(preamble, "cr.beginReconcileObservation(") {
		t.Error("the tick does not open a reconciliation observation")
	}
	if !strings.Contains(preamble, "cr.publishReconcileObservation(completion, trace,") {
		t.Error("the tick's defer does not publish the observation; an aborted or panicked cycle would publish nothing and the previous cycle's facts would stand as current")
	}
	// The publish must sit OUTSIDE the `if trace != nil` block inside the
	// defer. If it moved inside, the entire resource would disappear whenever
	// tracing is off — which is the defect this design exists to avoid.
	guard := strings.Index(preamble, "if trace != nil {")
	pub := strings.Index(preamble, "cr.publishReconcileObservation(")
	if guard >= 0 && pub >= 0 {
		between := preamble[guard:pub]
		if !strings.Contains(between, "\n\t\t}\n") {
			t.Error("the observation publish is inside `if trace != nil`; it would vanish whenever tracing is disabled")
		}
	}
}

// The single in-flight slot is only safe while ticks are serialized. If a tick
// were ever spawned into its own goroutine, two cycles could overlap and the
// first one's defer would publish the second one's half-filled observation —
// exactly the mixed-cycle read the resource promises is impossible.
func TestReconcileObservationTicksAreSerialized(t *testing.T) {
	src, err := os.ReadFile("city_runtime.go")
	if err != nil {
		t.Fatalf("read city_runtime.go: %v", err)
	}
	for _, spawn := range []string{"go runTick(", "go cr.tick(", "go func() { runTick("} {
		if strings.Contains(string(src), spawn) {
			t.Errorf("%q: ticks are no longer serialized, so two cycles can share the single in-flight observation slot; give each tick its own handle before allowing this", spawn)
		}
	}
}

// The input pass must fill the observation unconditionally. Before this change
// it returned early on a nil trace, which is why the observation could not be
// built from it.
func TestReconcileObservationInputPassIsNotTraceGated(t *testing.T) {
	src, err := os.ReadFile("city_runtime.go")
	if err != nil {
		t.Fatalf("read city_runtime.go: %v", err)
	}
	body := string(src)
	fn := strings.Index(body, "func (cr *CityRuntime) observeAndTraceReconcileInputs(")
	if fn < 0 {
		t.Fatal("observeAndTraceReconcileInputs is gone; the input pass may have been re-gated on the trace")
	}
	obs := strings.Index(body[fn:], "cr.observeReconcileInputs(")
	gate := strings.Index(body[fn:], "if trace == nil {")
	if obs < 0 {
		t.Fatal("the input pass no longer fills the observation")
	}
	if gate >= 0 && gate < obs {
		t.Error("the input pass returns on a nil trace before observing; the observation would be empty whenever tracing is off")
	}
}
