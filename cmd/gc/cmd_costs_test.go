package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/usage"
)

func TestAggregateRunCosts(t *testing.T) {
	facts := []usage.Fact{
		{RunID: "run-a", Kind: usage.KindModel, InputTokens: 100, OutputTokens: 50, CacheReadTokens: 5, CostUSDEstimate: 0.01},
		{RunID: "run-a", Kind: usage.KindModel, InputTokens: 10, Unpriced: true}, // excluded from cost
		{RunID: "run-a", Kind: usage.KindCompute, WallSeconds: 12.5},
		{RunID: "run-b", Kind: usage.KindCompute, WallSeconds: 3},
	}
	rows := aggregateRunCosts(facts)
	if len(rows) != 2 {
		t.Fatalf("want 2 runs, got %d", len(rows))
	}
	// Sorted by run id: run-a, run-b.
	a, b := rows[0], rows[1]
	if a.RunID != "run-a" || b.RunID != "run-b" {
		t.Fatalf("run order wrong: %q, %q", a.RunID, b.RunID)
	}
	if a.Invocations != 2 {
		t.Fatalf("run-a invocations = %d, want 2", a.Invocations)
	}
	if a.InputTokens != 110 || a.OutputTokens != 50 || a.CacheReadTokens != 5 {
		t.Fatalf("run-a tokens wrong: %+v", a)
	}
	if a.WallSeconds != 12.5 || a.ComputeFacts != 1 {
		t.Fatalf("run-a compute wrong: %+v", a)
	}
	if a.Unpriced != 1 {
		t.Fatalf("run-a unpriced = %d, want 1", a.Unpriced)
	}
	if a.CostUSDEstimate != 0.01 {
		t.Fatalf("run-a cost = %v, want 0.01 (unpriced excluded)", a.CostUSDEstimate)
	}
	if b.WallSeconds != 3 || b.ComputeFacts != 1 || b.Invocations != 0 {
		t.Fatalf("run-b wrong: %+v", b)
	}
}

func TestAggregateRunCostsEmpty(t *testing.T) {
	if rows := aggregateRunCosts(nil); len(rows) != 0 {
		t.Fatalf("nil facts must yield no rows, got %d", len(rows))
	}
}

func TestAggregateRunCostsNamesIdentityAndObservedWindow(t *testing.T) {
	t1 := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(5 * time.Minute)
	t3 := t2.Add(time.Minute)
	rows := aggregateRunCosts([]usage.Fact{
		{RunID: "wf-7", RunSource: "workflow_id", SessionID: "gc-session", Worker: "model-comparison--worker", AgentName: "model-comparison/worker", Template: "polecat", AwakeEpoch: "epoch-1", Kind: usage.KindModel, At: t1.UnixMilli(), InputTokens: 10},
		{RunID: "wf-7", RunSource: "workflow_id", SessionID: "gc-session", Worker: "model-comparison--worker", AgentName: "model-comparison/worker", Template: "polecat", AwakeEpoch: "epoch-1", Kind: usage.KindCompute, At: t2.UnixMilli(), WallSeconds: 5},
		{RunID: "wf-7", RunSource: "workflow_id", SessionID: "gc-session", Worker: "model-comparison--worker", AgentName: "model-comparison/worker", Template: "polecat", AwakeEpoch: "epoch-2", Kind: usage.KindCompute, At: t3.UnixMilli(), WallSeconds: 7},
	})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.EntityType != "execution_run" || row.EntityName != "wf-7" || row.RunSource != "workflow_id" {
		t.Fatalf("identity = type=%q name=%q source=%q", row.EntityType, row.EntityName, row.RunSource)
	}
	if row.AgentName != "model-comparison/worker" || row.Template != "polecat" {
		t.Fatalf("agent identity = %q/%q", row.AgentName, row.Template)
	}
	if row.FirstObservedAt != t1.Format(time.RFC3339) || row.LastObservedAt != t3.Format(time.RFC3339) {
		t.Fatalf("window = %q..%q", row.FirstObservedAt, row.LastObservedAt)
	}
	if row.ObservedFacts != 3 || row.AwakeIntervals != 2 {
		t.Fatalf("observation metadata = facts=%d awake=%d", row.ObservedFacts, row.AwakeIntervals)
	}
	if row.CacheReadTokens != 0 || row.InputTokens != 10 || row.WallSeconds != 12 {
		t.Fatalf("totals changed: %+v", row)
	}
}

func TestAggregateRunCostsLabelsPersistentSessionAndLegacyFacts(t *testing.T) {
	rows := aggregateRunCosts([]usage.Fact{
		{RunID: "gc-wisp-knf", RunSource: "self_bead_id", SessionID: "gc-wisp-knf", Worker: "gastown.mayor", Kind: usage.KindCompute, At: 3},
		{RunID: "gc-vef", SessionID: "gc-vef", Worker: "model-comparison/gastown.witness", Kind: usage.KindCompute, At: 1},
		{RunID: "old-run", Worker: "legacy-worker", Kind: usage.KindModel, At: 2},
	})
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	var explicitSession, session, legacy runCost
	for _, row := range rows {
		switch row.RunID {
		case "gc-wisp-knf":
			explicitSession = row
		case "gc-vef":
			session = row
		case "old-run":
			legacy = row
		}
	}
	if explicitSession.EntityType != "logical_session" || explicitSession.IdentityConfidence != "session_shape" {
		t.Fatalf("self-bead session identity = %+v", explicitSession)
	}
	if session.EntityType != "logical_session" || session.EntityName != "model-comparison/gastown.witness" || session.RunSource != "legacy_fact" {
		t.Fatalf("session identity = %+v", session)
	}
	if legacy.EntityType != "unknown" || legacy.IdentityConfidence != "unknown" {
		t.Fatalf("legacy identity = %+v", legacy)
	}
}

func TestCostsJSONIsVersionedAndExplicitlyFullHistory(t *testing.T) {
	rows := aggregateRunCosts([]usage.Fact{{RunID: "r", RunSource: "self_bead_id", Kind: usage.KindModel, At: 1000}})
	report := buildCostsReport(rows)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{"\"schema\":1", "\"window_kind\":\"all_recorded_history\"", "\"entity_type\":\"execution_run\"", "\"first_observed_at\"", "\"last_observed_at\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("JSON = %s, missing %s", text, want)
		}
	}
}
