package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gastownhall/gascity/internal/usage"
	"github.com/spf13/cobra"
)

func newCostsCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "costs",
		Short: "Show usage entities and estimated cost for this city",
		Long: `Aggregate recorded usage facts (model tokens and compute wall-seconds)
by an explicitly labeled usage entity for local cost insight.

Reads .gc/usage.jsonl (the local usage sink) and groups facts by run id. This
reflects facts only under the default "local" usage provider; with an "exec:"
or "discard" provider the facts are forwarded out of process or dropped, so
gc costs shows nothing local.

Cost is a list-price estimate for decision support, not an authoritative
charge; invocations with no pricing are flagged "unpriced" and excluded from
the cost total.

The default report is all recorded history in the append-only sink. It does not
reset or rotate usage. Rows expose identity evidence and first/last observation
timestamps so a persistent named session is not presented as a fresh current
run. Use --json for the versioned machine-readable projection.`,
		Example: "  gc costs --json",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if doCosts(stdout, stderr, jsonOut) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the versioned machine-readable projection")
	return cmd
}

type runCost struct {
	RunID               string  `json:"run_id"`
	RunSource           string  `json:"identity_source"`
	EntityType          string  `json:"entity_type"`
	EntityName          string  `json:"entity_name"`
	IdentityConfidence  string  `json:"identity_confidence"`
	AgentName           string  `json:"agent_name,omitempty"`
	Template            string  `json:"template,omitempty"`
	FirstObservedAt     string  `json:"first_observed_at,omitempty"`
	LastObservedAt      string  `json:"last_observed_at,omitempty"`
	ObservedFacts       int     `json:"observed_facts"`
	AwakeIntervals      int     `json:"awake_intervals_observed"`
	Invocations         int     `json:"invocations"`
	ComputeFacts        int     `json:"compute_facts"`
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	WallSeconds         float64 `json:"wall_seconds"`
	CostUSDEstimate     float64 `json:"cost_usd_estimate"`
	Unpriced            int     `json:"unpriced_invocations"`
}

type costAccumulator struct {
	runCost
	firstObserved time.Time
	lastObserved  time.Time
	awakeEpochs   map[string]struct{}
}

type costsReport struct {
	Schema          int       `json:"schema"`
	Source          string    `json:"source"`
	WindowKind      string    `json:"window_kind"`
	FirstObservedAt string    `json:"first_observed_at,omitempty"`
	LastObservedAt  string    `json:"last_observed_at,omitempty"`
	ObservedFacts   int       `json:"observed_facts"`
	Rows            []runCost `json:"rows"`
	Total           runCost   `json:"total"`
}

const (
	costsSchemaVersion = 1
	costsSource        = "local_usage_jsonl"
	costsWindowKind    = "all_recorded_history"
)

// aggregateRunCosts groups usage facts by resolved run id while retaining the
// identity and observation metadata needed to explain what that grouping
// means. Numeric totals and idempotency remain facts of the input reader.
func aggregateRunCosts(facts []usage.Fact) []runCost {
	byRun := map[string]*costAccumulator{}
	var order []string
	for _, f := range facts {
		rc := byRun[f.RunID]
		if rc == nil {
			rc = &costAccumulator{
				runCost:     runCost{RunID: f.RunID},
				awakeEpochs: map[string]struct{}{},
			}
			byRun[f.RunID] = rc
			order = append(order, f.RunID)
		}
		rc.ObservedFacts++
		observeIdentity(&rc.runCost, f)
		if observedAt, ok := factObservedAt(f); ok {
			if rc.firstObserved.IsZero() || observedAt.Before(rc.firstObserved) {
				rc.firstObserved = observedAt
				rc.FirstObservedAt = observedAt.UTC().Format(time.RFC3339)
			}
			if rc.lastObserved.IsZero() || observedAt.After(rc.lastObserved) {
				rc.lastObserved = observedAt
				rc.LastObservedAt = observedAt.UTC().Format(time.RFC3339)
			}
		}
		if epoch := factAwakeEpoch(f); epoch != "" {
			rc.awakeEpochs[epoch] = struct{}{}
			rc.AwakeIntervals = len(rc.awakeEpochs)
		}
		switch f.Kind {
		case usage.KindModel:
			rc.Invocations++
			rc.InputTokens += f.InputTokens
			rc.OutputTokens += f.OutputTokens
			rc.CacheReadTokens += f.CacheReadTokens
			rc.CacheCreationTokens += f.CacheCreationTokens
			if f.Unpriced {
				rc.Unpriced++
			} else {
				rc.CostUSDEstimate += f.CostUSDEstimate
			}
		case usage.KindCompute:
			rc.ComputeFacts++
			rc.WallSeconds += f.WallSeconds
		}
	}
	sort.Strings(order)
	rows := make([]runCost, 0, len(order))
	for _, id := range order {
		rows = append(rows, byRun[id].runCost)
	}
	return rows
}

func observeIdentity(row *runCost, fact usage.Fact) {
	source := strings.TrimSpace(fact.RunSource)
	if source == "" {
		source = "legacy_fact"
	}
	if row.RunSource == "" {
		row.RunSource = source
	} else if row.RunSource != source {
		row.RunSource = "mixed"
	}

	typeName := "unknown"
	confidence := "unknown"
	switch source {
	case "workflow_id", "molecule_id", "root_bead_id":
		typeName, confidence = "execution_run", "explicit"
	case "session_fallback":
		typeName, confidence = "logical_session", "explicit"
	case "self_bead_id":
		if strings.TrimSpace(fact.RunID) == strings.TrimSpace(fact.SessionID) && strings.TrimSpace(fact.Worker) != "" {
			typeName, confidence = "logical_session", "session_shape"
		} else {
			typeName, confidence = "execution_run", "explicit"
		}
	case "legacy_fact":
		// Old facts predate RunSource. Equality of RunID and SessionID plus a
		// worker name identifies the persistent-session shape, but does not
		// prove a fresh execution run.
		if strings.TrimSpace(fact.RunID) != "" && fact.RunID == fact.SessionID && strings.TrimSpace(fact.Worker) != "" {
			typeName, confidence = "logical_session", "inferred_legacy"
		}
	}
	if row.EntityType == "" {
		row.EntityType = typeName
		row.IdentityConfidence = confidence
	} else if row.EntityType != typeName || row.IdentityConfidence != confidence {
		row.EntityType = "unknown"
		row.IdentityConfidence = "mixed"
	}
	if row.EntityName == "" {
		if typeName == "logical_session" {
			row.EntityName = firstNonBlank(fact.Worker, fact.SessionID, fact.RunID)
		} else {
			row.EntityName = firstNonBlank(fact.RunID, fact.Worker, fact.SessionID)
		}
	}
	row.AgentName = mergeLabel(row.AgentName, fact.AgentName)
	row.Template = mergeLabel(row.Template, fact.Template)
}

func mergeLabel(current, incoming string) string {
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return current
	}
	if current == "" || current == incoming {
		return incoming
	}
	return "mixed"
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func factObservedAt(fact usage.Fact) (time.Time, bool) {
	if fact.At <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(fact.At), true
}

func factAwakeEpoch(fact usage.Fact) string {
	if epoch := strings.TrimSpace(fact.AwakeEpoch); epoch != "" {
		return epoch
	}
	// Compute facts written before AwakeEpoch was added encoded the same
	// immutable interval after SessionID in UpstreamReqID.
	prefix := strings.TrimSpace(fact.SessionID) + ":"
	if fact.Kind == usage.KindCompute && strings.HasPrefix(fact.UpstreamReqID, prefix) {
		return strings.TrimPrefix(fact.UpstreamReqID, prefix)
	}
	return ""
}

func buildCostsReport(rows []runCost) costsReport {
	report := costsReport{
		Schema:     costsSchemaVersion,
		Source:     costsSource,
		WindowKind: costsWindowKind,
		Rows:       rows,
	}
	var total runCost
	total.EntityType = "all_entities"
	total.EntityName = "TOTAL"
	total.IdentityConfidence = "aggregate"
	for _, row := range rows {
		total.ObservedFacts += row.ObservedFacts
		total.Invocations += row.Invocations
		total.ComputeFacts += row.ComputeFacts
		total.InputTokens += row.InputTokens
		total.OutputTokens += row.OutputTokens
		total.CacheReadTokens += row.CacheReadTokens
		total.CacheCreationTokens += row.CacheCreationTokens
		total.WallSeconds += row.WallSeconds
		total.CostUSDEstimate += row.CostUSDEstimate
		total.Unpriced += row.Unpriced
		if row.FirstObservedAt != "" && (report.FirstObservedAt == "" || row.FirstObservedAt < report.FirstObservedAt) {
			report.FirstObservedAt = row.FirstObservedAt
		}
		if row.LastObservedAt != "" && row.LastObservedAt > report.LastObservedAt {
			report.LastObservedAt = row.LastObservedAt
		}
	}
	total.FirstObservedAt = report.FirstObservedAt
	total.LastObservedAt = report.LastObservedAt
	report.ObservedFacts = total.ObservedFacts
	report.Total = total
	return report
}

func doCosts(stdout, stderr io.Writer, jsonOut bool) int {
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc costs: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	usagePath := filepath.Join(cityPath, ".gc", "usage.jsonl")
	facts, warnings, err := usage.ReadFacts(usagePath)
	if err != nil {
		fmt.Fprintf(stderr, "gc costs: reading %s: %v\n", usagePath, err) //nolint:errcheck // best-effort stderr
		return 1
	}
	for _, w := range warnings {
		fmt.Fprintf(stderr, "gc costs: %s\n", w) //nolint:errcheck // best-effort stderr
	}
	rows := aggregateRunCosts(facts)
	report := buildCostsReport(rows)
	if jsonOut {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			fmt.Fprintf(stderr, "gc costs: writing JSON: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
		return 0
	}

	if len(rows) == 0 {
		fmt.Fprintf(stdout, "No usage facts recorded yet (%s).\n", usagePath) //nolint:errcheck
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ENTITY_TYPE\tENTITY\tRUN_ID\tIDENTITY_SOURCE\tAGENT\tTEMPLATE\tOBSERVED_FROM\tOBSERVED_TO\tINVOCATIONS\tIN\tOUT\tCACHE_HITS_TOKENS\tCACHE_C\tWALL_S\tEST_USD\tUNPRICED") //nolint:errcheck
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%.1f\t%.4f\t%d\n", //nolint:errcheck
			nonBlank(r.EntityType), nonBlank(r.EntityName), truncRunID(r.RunID), nonBlank(r.RunSource), nonBlank(r.AgentName), nonBlank(r.Template), nonBlank(r.FirstObservedAt), nonBlank(r.LastObservedAt), r.Invocations, r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheCreationTokens, r.WallSeconds, r.CostUSDEstimate, r.Unpriced)
	}
	tot := report.Total
	fmt.Fprintf(tw, "TOTAL\tTOTAL\t-\t-\t-\t-\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%.1f\t%.4f\t%d\n", //nolint:errcheck
		nonBlank(report.FirstObservedAt), nonBlank(report.LastObservedAt), tot.Invocations, tot.InputTokens, tot.OutputTokens, tot.CacheReadTokens, tot.CacheCreationTokens, tot.WallSeconds, tot.CostUSDEstimate, tot.Unpriced)
	tw.Flush() //nolint:errcheck
	if tot.Unpriced > 0 {
		fmt.Fprintf(stdout, "\nNote: %d invocation(s) had no pricing and are excluded from EST_USD.\n", tot.Unpriced) //nolint:errcheck
	}
	fmt.Fprintf(stdout, "Aggregation: all recorded history; timestamps are observation bounds, not a current-run window.\n")                                //nolint:errcheck
	fmt.Fprintf(stdout, "Cache hits are prompt-cache read tokens, not hit events. Estimates are list-price decision-support, not authoritative charges.\n") //nolint:errcheck
	return 0
}

func nonBlank(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func truncRunID(s string) string {
	if len(s) > 28 {
		return s[:25] + "..."
	}
	return s
}
