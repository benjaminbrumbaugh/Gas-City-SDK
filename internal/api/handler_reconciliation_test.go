package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/reconcileobservation"
)

// observingState adds the optional capability to the shared fake. It is a
// wrapper rather than a field on fakeState so the plain fake still does NOT
// implement the capability — which is the case the 503 path exists for and
// which a fake that always implemented it could not exercise.
type observingState struct {
	*fakeState
	obs *reconcileobservation.Observation
}

func (o *observingState) ReconciliationObservation() *reconcileobservation.Observation {
	return o.obs
}

func sampleObservation(city string, templates int) *reconcileobservation.Observation {
	rows := make([]reconcileobservation.TemplateRow, 0, templates)
	for i := 0; i < templates; i++ {
		rows = append(rows, reconcileobservation.TemplateRow{
			Template:   fmt.Sprintf("gastown.t%03d", i),
			OpenCount:  i,
			Evaluation: reconcileobservation.EvaluationEligible,
		})
	}
	return &reconcileobservation.Observation{
		SchemaVersion: reconcileobservation.SchemaVersion,
		City:          city,
		Generation: reconcileobservation.Generation{
			InstanceID: "host:4242",
			PID:        4242,
			StartedAt:  time.Now().UTC().Add(-time.Hour),
			ConfigRev:  "rev-1",
			IsCurrent:  true,
		},
		Cycle: reconcileobservation.Cycle{
			TickID:     "city-4242-1-000001",
			Trigger:    reconcileobservation.TriggerPatrol,
			StartedAt:  time.Now().UTC().Add(-time.Second),
			EndedAt:    time.Now().UTC(),
			DurationMS: 12,
			Completion: reconcileobservation.CompletionCompleted,
			Reconciled: true,
		},
		Completeness:  reconcileobservation.Completeness{SessionSnapshotComplete: true},
		Totals:        reconcileobservation.Totals{OpenSessionCount: templates},
		Templates:     rows,
		TemplateCount: templates,
		Trace:         reconcileobservation.TraceState{Enabled: true},
	}
}

// inProcessClient serves h without a listener: requests are dispatched
// straight to the handler through a recorder. The tests below exercise
// routing, status codes, problem bodies, spec validation and the generated
// client, none of which depend on a real socket, and the repository's
// resource census ratchets untagged loopback servers downward.
func inProcessClient(h http.Handler) *http.Client {
	return &http.Client{Transport: handlerTransport{h: h}}
}

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// The Host guard and loopback perimeter see what a real loopback listener
	// would have handed them.
	req = req.Clone(req.Context())
	req.RemoteAddr = "127.0.0.1:65535"
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

const inProcessBase = "http://127.0.0.1:1"

// --- 1. the handler touches nothing but the capability --------------------
//
// hostileState embeds a NIL State, so every State method other than the one
// the handler is allowed to use panics. That is a stricter assertion than
// counting calls on a recording double: a double that returns empty results
// lets an accidental store query pass silently, whereas this fails loudly on
// the first one. The Server is built by hand rather than through newServer so
// nothing but the handler runs.
type hostileState struct {
	State // nil: any promoted method call panics
	obs   *reconcileobservation.Observation
	reads int
}

func (h *hostileState) ReconciliationObservation() *reconcileobservation.Observation {
	h.reads++
	return h.obs
}

func TestReconciliationHandlerTouchesNothingButTheCapability(t *testing.T) {
	st := &hostileState{obs: sampleObservation("test-city", 3)}
	srv := &Server{state: st}

	out, err := srv.humaHandleReconciliation(context.Background(), &ReconciliationInput{})
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	if st.reads != 1 {
		t.Fatalf("capability read %d times, want exactly 1", st.reads)
	}
	if out.Body.City != "test-city" || len(out.Body.Templates) != 3 {
		t.Fatalf("body did not come from the published observation: %+v", out.Body)
	}
}

// --- 2. cost does not track workload --------------------------------------
//
// Templates is bounded by configured templates; nothing in the response is
// keyed by a bead, task or session. This benchmark varies the only axis that
// can grow the body and is the executable form of that claim. Run with
// -benchmem: allocs/op must not vary with the store/task/session axes at all,
// because none of them is read.
func BenchmarkReconciliationHandler(b *testing.B) {
	for _, templates := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("templates=%d", templates), func(b *testing.B) {
			st := &hostileState{obs: sampleObservation("test-city", templates)}
			srv := &Server{state: st}
			ctx := context.Background()
			in := &ReconciliationInput{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := srv.humaHandleReconciliation(ctx, in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- 3. 503 for "nothing published", never a zero-valued 200 --------------
func TestReconciliationUnavailableIsNotAnEmptyObservation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state State
		want  string
	}{
		{"capability absent", newFakeState(t), "no controller runtime"},
		{"published nothing yet", &observingState{fakeState: newFakeState(t)}, "has not completed a cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := inProcessClient(newTestCityHandler(t, tc.state))
			city := tc.state.CityName()

			resp, err := client.Get(inProcessBase + "/v0/city/" + city + "/reconciliation")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer resp.Body.Close() //nolint:errcheck
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want 503 — an unpublished observation must not read as an observation of an idle city", resp.StatusCode)
			}
			var problem map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&problem); err != nil {
				t.Fatalf("decode: %v", err)
			}
			detail, _ := problem["detail"].(string)
			if !strings.Contains(detail, tc.want) {
				t.Fatalf("detail %q does not say why it is unavailable (want %q)", detail, tc.want)
			}
		})
	}
}

// --- 4. 404 is the inherited one, not a second implementation -------------
//
// bindCity already refuses an unregistered city with a stable detail string
// that callers match on via IsCityNotFoundOrNotRunningDetail. A hand-written
// 404 here would fork that contract silently.
func TestReconciliationNotFoundUsesTheInheritedCityRefusal(t *testing.T) {
	client := inProcessClient(newTestCityHandler(t, newFakeState(t)))

	resp, err := client.Get(inProcessBase + "/v0/city/no-such-city/reconciliation")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
	var problem map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&problem); err != nil {
		t.Fatalf("decode: %v", err)
	}
	detail, _ := problem["detail"].(string)
	if !IsCityNotFoundOrNotRunningDetail(detail) {
		t.Fatalf("404 detail %q is not the shared city-not-found payload; the endpoint has forked a settled contract", detail)
	}
}

// --- 5. a served observation round-trips and matches the published spec ---
func TestReconciliationResponseMatchesSpec(t *testing.T) {
	specBytes, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	doc, err := libopenapi.NewDocument(specBytes)
	if err != nil {
		t.Fatalf("build document: %v", err)
	}
	v, errs := validator.NewValidator(doc)
	if len(errs) > 0 {
		t.Fatalf("construct validator: %v", errs)
	}

	base := newFakeState(t)
	state := &observingState{fakeState: base, obs: sampleObservation(base.CityName(), 2)}
	client := inProcessClient(newTestCityHandler(t, state))

	url := inProcessBase + "/v0/city/" + base.CityName() + "/reconciliation"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ok, verrs := v.ValidateHttpResponse(req, resp); !ok {
		for _, e := range verrs {
			t.Errorf("spec violation: %s", e.Error())
		}
		t.Fatal("the served body does not match the published schema")
	}
}

// --- 6. facts survive the wire ---------------------------------------------
//
// The body is the domain model, so a field that stops serializing is a field a
// consumer silently loses. These are the ones whose absence would change an
// answer: the completeness flags and the generation identity.
func TestReconciliationBodyCarriesCompletenessAndGeneration(t *testing.T) {
	base := newFakeState(t)
	obs := sampleObservation(base.CityName(), 1)
	obs.Completeness.StoreQueryPartial = true
	obs.Completeness.SessionSnapshotComplete = false
	obs.Completeness.ScaleCheckPartialTemplates = []string{"gastown.broken"}
	obs.Generation.IsCurrent = false
	obs.TemplatesTruncated = true
	obs.TemplateCount = 999

	state := &observingState{fakeState: base, obs: obs}
	client := inProcessClient(newTestCityHandler(t, state))

	resp, err := client.Get(inProcessBase + "/v0/city/" + base.CityName() + "/reconciliation")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var got reconcileobservation.Observation
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Completeness.StoreQueryPartial {
		t.Error("store_query_partial did not survive the wire; a failed query would read as a clean one")
	}
	if got.Completeness.SessionSnapshotComplete {
		t.Error("session_snapshot_complete was rewritten to true")
	}
	if len(got.Completeness.ScaleCheckPartialTemplates) != 1 {
		t.Error("the partial template list was dropped")
	}
	if got.Generation.IsCurrent {
		t.Error("is_current was rewritten to true; a superseded generation would read as the live one")
	}
	if !got.TemplatesTruncated || got.TemplateCount != 999 {
		t.Errorf("truncation was not reported: truncated=%v count=%d", got.TemplatesTruncated, got.TemplateCount)
	}
}

// --- 7. the resource is registered where the 404 comes for free -----------
func TestReconciliationIsRegisteredUnderTheCityScope(t *testing.T) {
	specBytes, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]any `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	op, ok := spec.Paths["/v0/city/{cityName}/reconciliation"]["get"]
	if !ok {
		t.Fatal("the reconciliation resource is not in the published spec")
	}
	for _, code := range []string{"200", "404", "503"} {
		if _, ok := op.Responses[code]; !ok {
			t.Errorf("spec does not declare a %s response; a caller cannot know the contract", code)
		}
	}
}

// --- 8. the GENERATED client is what most consumers actually hold ---------
//
// The spec-validation test above proves the body matches the published schema.
// This proves the schema is usable: that the generated client has a typed
// method for the resource and decodes both the observation and the 503 into
// typed fields rather than leaving a caller to parse bytes. A resource that
// only works when hand-rolled is not really exposed.
func TestReconciliationGeneratedClientDecodesTypedResponses(t *testing.T) {
	base := newFakeState(t)
	obs := sampleObservation(base.CityName(), 2)
	obs.Completeness.StoreQueryPartial = true
	state := &observingState{fakeState: base, obs: obs}
	client, err := genclient.NewClientWithResponses(inProcessBase,
		genclient.WithHTTPClient(inProcessClient(newTestCityHandler(t, state))))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx := context.Background()

	got, err := client.GetV0CityByCityNameReconciliationWithResponse(ctx, base.CityName())
	if err != nil {
		t.Fatalf("typed get: %v", err)
	}
	if got.JSON200 == nil {
		t.Fatalf("200 body did not decode into the typed field (status %s, body %s)", got.Status(), got.Body)
	}
	if got.JSON200.City != base.CityName() {
		t.Errorf("city = %q, want %q", got.JSON200.City, base.CityName())
	}
	if got.JSON200.Completeness.StoreQueryPartial != true {
		t.Error("store_query_partial did not survive the generated client; a failed query would read as a clean one")
	}
	// oapi-codegen renders array members as pointers even when the schema marks
	// them required, so a nil here means the field did not arrive at all.
	if got.JSON200.Templates == nil {
		t.Fatal("templates did not decode")
	}
	if n := len(*got.JSON200.Templates); n != 2 {
		t.Errorf("templates = %d, want 2", n)
	}

	// And the unavailable case is typed too, so a caller can tell "nothing
	// published" from a transport failure.
	empty := &observingState{fakeState: newFakeState(t)}
	client2, err := genclient.NewClientWithResponses(inProcessBase,
		genclient.WithHTTPClient(inProcessClient(newTestCityHandler(t, empty))))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	unavailable, err := client2.GetV0CityByCityNameReconciliationWithResponse(ctx, empty.CityName())
	if err != nil {
		t.Fatalf("typed get: %v", err)
	}
	if unavailable.JSON200 != nil {
		t.Fatal("an unpublished observation decoded as a 200 body")
	}
	if unavailable.ApplicationproblemJSON503 == nil {
		t.Fatalf("503 did not decode into the typed problem field (status %s)", unavailable.Status())
	}
}
