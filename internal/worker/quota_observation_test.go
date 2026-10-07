package worker

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/session"
)

func TestSessionLogAdapterCodexQuotaObservationBindsIncarnationAndExpiry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T19-00-00-session-1.jsonl")
	fixture, err := os.ReadFile("../sessionlog/testdata/codex/quota-refresh.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	info := session.Info{
		Provider:      "codex",
		ProviderKind:  "codex",
		SessionKey:    "session-1",
		WorkDir:       "/work/dir",
		InstanceToken: "incarnation-a",
	}
	now := time.Date(2026, 10, 5, 19, 1, 0, 0, time.UTC)
	observation, err := (SessionLogAdapter{SearchPaths: []string{root}}).CodexQuotaObservation(info, path, now)
	if err != nil {
		t.Fatal(err)
	}
	if observation == nil {
		t.Fatal("CodexQuotaObservation() = nil, want verified observation")
	}
	if observation.Provider != "codex" || observation.SessionID != "session-1" || observation.Incarnation != "incarnation-a" {
		t.Fatalf("identity = %+v, want codex/session-1/incarnation-a", observation)
	}
	if observation.AccountRef != "codex" || observation.Scope != "account:codex" {
		t.Fatalf("scope = %q/%q, want account-derived provider scope", observation.AccountRef, observation.Scope)
	}
	if !observation.IsHardExhausted() {
		t.Fatalf("observation = %+v, want hard exhaustion from reached evidence", observation)
	}
	if observation.Primary == nil || observation.Primary.WindowMinutes == nil || *observation.Primary.WindowMinutes != 300 {
		t.Fatalf("primary = %+v, want 300-minute window", observation.Primary)
	}
	if !observation.ExpiresAt.After(observation.ObservedAt) || !observation.IsFresh(now) {
		t.Fatalf("lifecycle = observed %s expires %s, want fresh at %s", observation.ObservedAt, observation.ExpiresAt, now)
	}
}

func TestObserveCodexQuotaUsesExactSessionResolver(t *testing.T) {
	root := t.TempDir()
	dayDir := filepath.Join(root, "2026", "10", "05")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dayDir, "rollout-2026-10-05T19-00-00-session-1.jsonl")
	fixture, err := os.ReadFile("../sessionlog/testdata/codex/quota-refresh.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	info := session.Info{
		Provider: "codex", SessionKey: "session-1", WorkDir: "/work/dir",
		CreatedAt:     time.Date(2026, 10, 5, 18, 59, 0, 0, time.UTC),
		LastWokeAt:    time.Date(2026, 10, 5, 19, 1, 0, 0, time.UTC).Format(time.RFC3339),
		InstanceToken: "incarnation-a",
	}
	observation, err := ObserveCodexQuota(info, []string{root}, time.Date(2026, 10, 5, 19, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if observation == nil || observation.SessionID != "session-1" {
		t.Fatalf("observation = %+v, want exact session transcript", observation)
	}
}

func TestQuotaObservationHardExhaustionRequiresReachedEvidence(t *testing.T) {
	reached := false
	used := 100.0
	observation := QuotaObservation{
		ObservedAt: time.Unix(100, 0).UTC(),
		ExpiresAt:  time.Unix(200, 0).UTC(),
		Primary:    &QuotaWindow{UsedPercent: &used, Reached: &reached},
	}
	if observation.IsHardExhausted() {
		t.Fatal("100 percent usage without reached=true was treated as hard exhaustion")
	}
	if observation.IsFresh(time.Unix(200, 0).UTC()) {
		t.Fatal("observation at its expiry was treated as fresh")
	}
}

func TestDecodeCurrentQuotaObservationRejectsChangedIncarnation(t *testing.T) {
	reached := true
	now := time.Unix(150, 0).UTC()
	observation := QuotaObservation{
		SessionID:   "session-1",
		Incarnation: "incarnation-a",
		ObservedAt:  now.Add(-time.Minute),
		ExpiresAt:   now.Add(time.Minute),
		Primary:     &QuotaWindow{Reached: &reached},
	}
	encoded, err := marshalQuotaObservation(&observation)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeCurrentQuotaObservation(encoded, session.Info{SessionKey: "session-1", InstanceToken: "incarnation-b"}, now); got != nil {
		t.Fatalf("changed incarnation reused quota evidence: %+v", got)
	}
}

func TestOverlayQuotaObservationRequiresAccountScopeAndFreshReachedEvidence(t *testing.T) {
	packet := wayfinderEvaluateRequest{
		Candidates: []wayfinderCandidate{{
			ExecutionTarget: wayfinderExecutionTarget{TargetID: "target-a", AccountRef: "account-a"},
			Model:           wayfinderModelRecord{CanonicalModel: "model-a"},
			ObservationRef:  "observation-a",
		}},
		Observations: []wayfinderObservation{{
			RecordID: "observation-a",
			Throttle: wayfinderKnownString{Known: true, Value: stringPointer("none")},
		}},
	}
	now := time.Unix(150, 0).UTC()
	reached := true
	base := QuotaObservation{Model: "model-a", ObservedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), Primary: &QuotaWindow{Reached: &reached}}
	for _, tc := range []struct {
		name string
		edit func(*QuotaObservation)
	}{
		{name: "missing account scope", edit: func(*QuotaObservation) {}},
		{name: "wrong account", edit: func(o *QuotaObservation) { o.AccountRef = "account-b" }},
		{name: "stale", edit: func(o *QuotaObservation) { o.AccountRef = "account-a"; o.ExpiresAt = now }},
		{name: "reminder", edit: func(o *QuotaObservation) { o.AccountRef = "account-a"; reached := false; o.Primary.Reached = &reached }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := packet
			candidate.Observations = append([]wayfinderObservation(nil), packet.Observations...)
			observation := base
			tc.edit(&observation)
			overlayQuotaObservation(&candidate, &observation, now)
			if !candidate.Observations[0].Throttle.Known || candidate.Observations[0].Throttle.Value == nil || *candidate.Observations[0].Throttle.Value != "none" {
				t.Fatalf("observation = %+v, want unchanged non-throttled evidence", candidate.Observations[0])
			}
		})
	}
}
