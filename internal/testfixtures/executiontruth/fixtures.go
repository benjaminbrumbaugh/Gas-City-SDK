// Package executiontruth provides deterministic, redacted execution evidence
// for shadow-replay tests. It is test support, not a production event source,
// receiver, lifecycle projector, or routing authority.
//
// The embedded corpus stores only eventexport envelopes and fixture-local
// evidence metadata. The eventexport validator remains the wire authority;
// this package adds no fields to that wire. Provenance describes where a fact
// came from, while freshness describes the quality of the observation. They
// are intentionally separate so a derived or unknown fact cannot be silently
// treated as authoritative, and stale or unknown observation state cannot be
// silently treated as fresh.
package executiontruth

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/gastownhall/gascity/pkg/eventexport"
)

const fixtureVersion = 1

// Provenance identifies the evidence owner for one redacted execution fact.
type Provenance string

const (
	// ProvenanceAuthoritative identifies a fact projected from the owning
	// durable execution store/event contract.
	ProvenanceAuthoritative Provenance = "authoritative"
	// ProvenanceObserved identifies an observation supplied by a runtime or
	// provider boundary without claiming durable execution authority.
	ProvenanceObserved Provenance = "observed"
	// ProvenanceDerived identifies a fact synthesized by a local projection or
	// replay adapter from other evidence.
	ProvenanceDerived Provenance = "derived"
	// ProvenanceUnknown identifies evidence whose source cannot be established.
	ProvenanceUnknown Provenance = "unknown"
)

// Freshness identifies the replay-time quality of an observation. It is not a
// provider state and does not change the event timestamp in the redacted wire.
type Freshness string

const (
	// FreshnessFresh identifies an observation available at or before the
	// scenario's replay clock with no fixture-declared staleness.
	FreshnessFresh Freshness = "fresh"
	// FreshnessStale identifies an observation known to predate the replay
	// window. It remains evidence, but is not a current fact by itself.
	FreshnessStale Freshness = "stale"
	// FreshnessUnknown identifies an observation with no trustworthy observation
	// time. It must not be interpreted as fresh or absent.
	FreshnessUnknown Freshness = "unknown"
)

// Corpus is the versioned set of deterministic shadow-replay scenarios.
type Corpus struct {
	FixtureVersion int        `json:"fixture_version"`
	Scenarios      []Scenario `json:"scenarios"`
}

// Scenario is one fixed-time, single-city replay input. Observations are
// ordered by their redacted envelope sequence and carry no raw payload or
// provider transcript content.
type Scenario struct {
	Name         string        `json:"name"`
	AsOf         time.Time     `json:"as_of"`
	CityHash     string        `json:"city_hash"`
	Observations []Observation `json:"observations"`
}

// Observation pairs one receiver-valid redacted execution envelope with
// fixture-local provenance and freshness metadata. ObservedAt is absent only
// when Freshness is FreshnessUnknown.
type Observation struct {
	Envelope   eventexport.Envelope `json:"envelope"`
	Provenance Provenance           `json:"provenance"`
	Freshness  Freshness            `json:"freshness"`
	ObservedAt *time.Time           `json:"observed_at,omitempty"`
}

// shadowReplayFS keeps the corpus owner-local and deterministic. Load decodes
// it for each call, so callers receive independent mutable values.
//
//go:embed testdata/shadow_replay.json
var shadowReplayFS embed.FS

// Load returns a freshly decoded and validated copy of the shadow-replay
// corpus. It performs no filesystem, provider, database, or network I/O.
func Load() (Corpus, error) {
	file, err := shadowReplayFS.Open("testdata/shadow_replay.json")
	if err != nil {
		return Corpus{}, fmt.Errorf("open execution-truth fixture: %w", err)
	}
	defer func() { _ = file.Close() }()

	var corpus Corpus
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("decode execution-truth fixture: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Corpus{}, fmt.Errorf("decode execution-truth fixture: trailing JSON")
		}
		return Corpus{}, fmt.Errorf("decode execution-truth fixture trailer: %w", err)
	}
	if err := corpus.Validate(); err != nil {
		return Corpus{}, fmt.Errorf("validate execution-truth fixture: %w", err)
	}
	return corpus, nil
}

// Validate checks the fixture version, scenario identity/order, redacted wire
// envelopes, and the separate provenance/freshness metadata contract.
func (c Corpus) Validate() error {
	if c.FixtureVersion != fixtureVersion {
		return fmt.Errorf("fixture_version %d != %d", c.FixtureVersion, fixtureVersion)
	}
	if len(c.Scenarios) == 0 {
		return fmt.Errorf("scenarios must not be empty")
	}
	seen := make(map[string]struct{}, len(c.Scenarios))
	for i, scenario := range c.Scenarios {
		if scenario.Name == "" {
			return fmt.Errorf("scenario %d has empty name", i)
		}
		if _, exists := seen[scenario.Name]; exists {
			return fmt.Errorf("scenario %q is duplicated", scenario.Name)
		}
		seen[scenario.Name] = struct{}{}
		if err := scenario.Validate(); err != nil {
			return fmt.Errorf("scenario %q: %w", scenario.Name, err)
		}
	}
	return nil
}

// Validate checks one scenario and ensures its observation sequence is a
// deterministic, strictly increasing execution-fact stream.
func (s Scenario) Validate() error {
	if s.AsOf.IsZero() {
		return fmt.Errorf("as_of must be non-zero")
	}
	if len(s.Observations) == 0 {
		return fmt.Errorf("observations must not be empty")
	}
	batch := s.Batch()
	if err := eventexport.ValidateBatch(batch); err != nil {
		return fmt.Errorf("redacted batch: %w", err)
	}
	previousSeq := uint64(0)
	for i, observation := range s.Observations {
		if observation.Envelope.Seq <= previousSeq {
			return fmt.Errorf("observation %d seq %d is not strictly after %d", i, observation.Envelope.Seq, previousSeq)
		}
		previousSeq = observation.Envelope.Seq
		if !isExecutionFact(observation.Envelope.Type) {
			return fmt.Errorf("observation %d type %q is not an execution fact", i, observation.Envelope.Type)
		}
		if observation.Envelope.Title != "" || observation.Envelope.Formula != "" {
			return fmt.Errorf("observation %d contains free-form content", i)
		}
		if err := validateObservation(observation, s.AsOf); err != nil {
			return fmt.Errorf("observation %d: %w", i, err)
		}
	}
	return nil
}

// Batch returns a fresh receiver-valid redacted event batch for the scenario.
// It clones dependency slices so replay consumers cannot mutate the fixture's
// in-memory topology through the returned batch.
func (s Scenario) Batch() eventexport.Batch {
	events := make([]eventexport.Envelope, 0, len(s.Observations))
	for _, observation := range s.Observations {
		envelope := observation.Envelope
		if observation.Envelope.DependsOnStepIDs != nil {
			dependencies := append([]string(nil), (*observation.Envelope.DependsOnStepIDs)...)
			envelope.DependsOnStepIDs = &dependencies
		}
		events = append(events, envelope)
	}
	return eventexport.Batch{
		CityHash:      s.CityHash,
		SchemaVersion: eventexport.SchemaVersion,
		Events:        events,
	}
}

func validateObservation(observation Observation, asOf time.Time) error {
	switch observation.Provenance {
	case ProvenanceAuthoritative, ProvenanceObserved, ProvenanceDerived, ProvenanceUnknown:
	default:
		return fmt.Errorf("unknown provenance %q", observation.Provenance)
	}
	switch observation.Freshness {
	case FreshnessFresh:
		if observation.ObservedAt == nil {
			return fmt.Errorf("fresh observation requires observed_at")
		}
		if observation.ObservedAt.After(asOf) {
			return fmt.Errorf("fresh observation is after as_of")
		}
	case FreshnessStale:
		if observation.ObservedAt == nil {
			return fmt.Errorf("stale observation requires observed_at")
		}
		if !observation.ObservedAt.Before(asOf) {
			return fmt.Errorf("stale observation must predate as_of")
		}
	case FreshnessUnknown:
		if observation.ObservedAt != nil {
			return fmt.Errorf("unknown freshness must omit observed_at")
		}
	default:
		return fmt.Errorf("unknown freshness %q", observation.Freshness)
	}
	return nil
}

func isExecutionFact(typ string) bool {
	switch typ {
	case "execution.work_associated", "execution.step_defined", "execution.step_started", "execution.step_completed":
		return true
	default:
		return false
	}
}
