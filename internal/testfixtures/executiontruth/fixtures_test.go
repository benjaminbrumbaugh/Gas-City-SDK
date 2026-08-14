package executiontruth

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/pkg/eventexport"
)

func TestLoadShadowReplayCorpusIsDeterministicAndRedacted(t *testing.T) {
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated loads differ:\nfirst=%#v\nsecond=%#v", first, second)
	}

	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"payload", "message", "transcript_path", "provider_session_id", "/Users/", "secret"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("fixture contains forbidden non-redacted field/value %q: %s", forbidden, raw)
		}
	}
	for _, scenario := range first.Scenarios {
		if err := scenario.Validate(); err != nil {
			t.Fatalf("scenario %q: %v", scenario.Name, err)
		}
		if err := eventexport.ValidateBatch(scenario.Batch()); err != nil {
			t.Fatalf("scenario %q batch: %v", scenario.Name, err)
		}
		for _, observation := range scenario.Observations {
			if observation.Envelope.Title != "" || observation.Envelope.Formula != "" {
				t.Fatalf("scenario %q seq %d contains free-form content: %#v", scenario.Name, observation.Envelope.Seq, observation.Envelope)
			}
		}
	}
}

func TestShadowReplayCorpusPreservesTopologyTriState(t *testing.T) {
	corpus := mustLoad(t)
	unknown := mustScenario(t, corpus, "unknown-topology")
	knownRoot := mustScenario(t, corpus, "fresh-authoritative")
	knownDeps := mustScenario(t, corpus, "stale-derived")

	if got := findObservation(t, unknown, 11).Envelope.DependsOnStepIDs; got != nil {
		t.Fatalf("unknown topology = %#v, want nil", got)
	}
	if got := findObservation(t, knownRoot, 2).Envelope.DependsOnStepIDs; got == nil || len(*got) != 0 {
		t.Fatalf("known root topology = %#v, want present empty slice", got)
	}
	if got := findObservation(t, knownDeps, 21).Envelope.DependsOnStepIDs; got == nil || !reflect.DeepEqual(*got, []string{"prepare"}) {
		t.Fatalf("known dependency topology = %#v, want [prepare]", got)
	}
}

func TestShadowReplayCorpusSeparatesProvenanceFromFreshness(t *testing.T) {
	corpus := mustLoad(t)
	cases := []struct {
		scenario    string
		provenance  Provenance
		freshness   Freshness
		observedAt  string
		wantUnknown bool
	}{
		{"fresh-authoritative", ProvenanceAuthoritative, FreshnessFresh, "2026-08-13T19:59:59Z", false},
		{"unknown-topology", ProvenanceAuthoritative, FreshnessUnknown, "", true},
		{"stale-derived", ProvenanceDerived, FreshnessStale, "2026-08-13T18:00:00Z", false},
		{"unknown-provenance", ProvenanceUnknown, FreshnessUnknown, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.scenario, func(t *testing.T) {
			scenario := mustScenario(t, corpus, tc.scenario)
			observation := scenario.Observations[0]
			if observation.Provenance != tc.provenance {
				t.Fatalf("provenance = %q, want %q", observation.Provenance, tc.provenance)
			}
			if observation.Freshness != tc.freshness {
				t.Fatalf("freshness = %q, want %q", observation.Freshness, tc.freshness)
			}
			if (observation.ObservedAt == nil) != tc.wantUnknown {
				t.Fatalf("observed_at = %v, want unknown=%v", observation.ObservedAt, tc.wantUnknown)
			}
			if observation.ObservedAt != nil && observation.ObservedAt.Format(time.RFC3339) != tc.observedAt {
				t.Fatalf("observed_at = %s, want %s", observation.ObservedAt.Format(time.RFC3339), tc.observedAt)
			}
		})
	}
}

func TestShadowReplayCorpusRoundTripPreservesUnknownAndStaleEvidence(t *testing.T) {
	corpus := mustLoad(t)
	for _, want := range corpus.Scenarios {
		raw, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got Scenario
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&got); err != nil {
			t.Fatalf("scenario %q decode: %v", want.Name, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("scenario %q changed after JSON round trip:\nwant=%#v\ngot=%#v", want.Name, want, got)
		}
	}
}

func TestShadowReplayCorpusLoadReturnsIndependentValues(t *testing.T) {
	first := mustLoad(t)
	first.Scenarios[0].Observations[0].Envelope.RunID = "mutated"
	if first.Scenarios[0].Observations[0].Envelope.DependsOnStepIDs != nil {
		*first.Scenarios[0].Observations[0].Envelope.DependsOnStepIDs = append(*first.Scenarios[0].Observations[0].Envelope.DependsOnStepIDs, "mutated")
	}
	second := mustLoad(t)
	if second.Scenarios[0].Observations[0].Envelope.RunID == "mutated" {
		t.Fatal("Load returned shared mutable envelope state")
	}
}

func TestScenarioValidationRejectsFreshnessAndRedactionDrift(t *testing.T) {
	corpus := mustLoad(t)
	scenario := mustScenario(t, corpus, "fresh-authoritative")

	staleWithoutTimestamp := scenario
	staleWithoutTimestamp.Observations = append([]Observation(nil), scenario.Observations...)
	staleWithoutTimestamp.Observations[0].Freshness = FreshnessStale
	staleWithoutTimestamp.Observations[0].ObservedAt = nil
	if err := staleWithoutTimestamp.Validate(); err == nil {
		t.Fatal("stale observation without observed_at unexpectedly validated")
	}

	unknownWithTimestamp := scenario
	unknownWithTimestamp.Observations = append([]Observation(nil), scenario.Observations...)
	unknownWithTimestamp.Observations[0].Freshness = FreshnessUnknown
	unknownWithTimestamp.Observations[0].ObservedAt = scenario.Observations[0].ObservedAt
	if err := unknownWithTimestamp.Validate(); err == nil {
		t.Fatal("unknown observation with observed_at unexpectedly validated")
	}

	content := scenario
	content.Observations = append([]Observation(nil), scenario.Observations...)
	content.Observations[0].Envelope.Title = "free form"
	if err := content.Validate(); err == nil {
		t.Fatal("free-form title unexpectedly validated in redacted fixture")
	}
}

func mustLoad(t *testing.T) Corpus {
	t.Helper()
	corpus, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

func mustScenario(t *testing.T, corpus Corpus, name string) Scenario {
	t.Helper()
	for _, scenario := range corpus.Scenarios {
		if scenario.Name == name {
			return scenario
		}
	}
	t.Fatalf("scenario %q not found", name)
	return Scenario{}
}

func findObservation(t *testing.T, scenario Scenario, seq uint64) Observation {
	t.Helper()
	for _, observation := range scenario.Observations {
		if observation.Envelope.Seq == seq {
			return observation
		}
	}
	t.Fatalf("scenario %q seq %d not found", scenario.Name, seq)
	return Observation{}
}
