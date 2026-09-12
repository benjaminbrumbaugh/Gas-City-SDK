package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func hookStoreAffinityTestOptions() hookClaimOptions {
	return hookClaimOptions{
		Assignee:             "target",
		IdentityCandidates:   []string{"target"},
		RouteTargets:         []string{"target"},
		DrainAck:             true,
		JSON:                 true,
		EnforceStoreAffinity: true,
	}
}

func TestHookClaimSkipsCandidateFromForeignStoreBeforeMutation(t *testing.T) {
	stores := []hookStore{{
		dir: "rig-a",
		env: []string{"GC_STORE_SCOPE=rig", "GC_RIG=rig-a"},
	}}
	row := fmt.Sprintf(`[{"id":"foreign-1","status":"open","metadata":{"%s":"city","gc.routed_to":"target"}}]`, hookClaimStoreMetadataKey)

	var claimCalls []string
	ops := hookFanoutBaseOps(func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
		claimCalls = append(claimCalls, beadID+":"+assignee)
		return beads.Bead{ID: beadID, Status: "in_progress", Assignee: assignee}, true, nil
	})
	var stdout, stderr bytes.Buffer
	code := claimHookWorkWithRunner("gc ready --json", stores[0].dir, stores[0].env, stores,
		hookStoreAffinityTestOptions(), ops,
		func(string, string, []string) (string, error) { return row, nil },
		func(string, error) {}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("claimHookWorkWithRunner = %d, want 0 after structured drain; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(claimCalls) != 0 {
		t.Fatalf("claim calls = %v, want none for a foreign-store candidate", claimCalls)
	}
	if !strings.Contains(stderr.String(), "store affinity mismatch") ||
		!strings.Contains(stderr.String(), "foreign-1") ||
		!strings.Contains(stderr.String(), "city") ||
		!strings.Contains(stderr.String(), "rig rig-a") {
		t.Fatalf("stderr = %q, want candidate id and source/destination store-affinity diagnostic", stderr.String())
	}
	var result hookClaimJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nraw: %q", err, stdout.String())
	}
	if result.Action != "drain" || result.Reason != hookClaimReasonClaimsErrored {
		t.Fatalf("drain result = %+v, want claims_errored for skipped unclaimable work", result)
	}
}

func TestHookClaimAllowsCandidateFromClaimStore(t *testing.T) {
	stores := []hookStore{{
		dir: "rig-a",
		env: []string{"GC_STORE_SCOPE=rig", "GC_RIG=rig-a"},
	}}
	row := fmt.Sprintf(`[{"id":"local-1","status":"open","metadata":{"%s":"rig rig-a","gc.routed_to":"target"}}]`, hookClaimStoreMetadataKey)

	ops := hookFanoutBaseOps(func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
		return beads.Bead{ID: beadID, Status: "in_progress", Assignee: assignee}, true, nil
	})
	var stdout, stderr bytes.Buffer
	code := claimHookWorkWithRunner("gc ready --json", stores[0].dir, stores[0].env, stores,
		hookStoreAffinityTestOptions(), ops,
		func(string, string, []string) (string, error) { return row, nil },
		func(string, error) {}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("claimHookWorkWithRunner = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var result hookClaimJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nraw: %q", err, stdout.String())
	}
	if result.Action != "work" || result.BeadID != "local-1" {
		t.Fatalf("claim result = %+v, want local-1 work", result)
	}
}

func TestHookClaimRouteMetadataTreatsEmptyAndAbsentAsUnrouted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata map[string]string
	}{
		{name: "absent", metadata: map[string]string{
			"gc.kind":       "workflow",
			"gc.run_target": "target",
		}},
		{name: "empty", metadata: map[string]string{
			"gc.routed_to":  "",
			"gc.kind":       "workflow",
			"gc.run_target": "target",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := beads.Bead{ID: tc.name, Metadata: tc.metadata}
			if got := hookClaimRouteState(candidate); got != hookRouteUnrouted {
				t.Fatalf("hookClaimRouteState = %v, want unrouted", got)
			}
			if got := hookClaimRoute(candidate); got != "target" {
				t.Fatalf("hookClaimRoute = %q, want workflow fallback target", got)
			}
			if !hookClaimMatchesRoute(candidate, []string{"target"}) {
				t.Fatal("unrouted workflow candidate did not match its run_target fallback")
			}
		})
	}
}

func TestReadyHookSourceAnnotationFollowsFederationOwner(t *testing.T) {
	created := time.Unix(10, 0)
	city := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID:        "city-1",
		Title:     "city work",
		Status:    "open",
		Type:      "task",
		CreatedAt: created,
	}}, nil)
	rig := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID:        "rig-1",
		Title:     "rig work",
		Status:    "open",
		Type:      "task",
		CreatedAt: created.Add(time.Second),
	}, {
		ID:        "city-1",
		Title:     "co-resident duplicate",
		Status:    "open",
		Type:      "task",
		CreatedAt: created.Add(2 * time.Second),
	}}, nil)

	rows, err := readyBeadsForOpts([]readyLeg{
		{label: "city", store: city},
		{label: "rig rig-a", store: rig},
	}, readyOpts{includeStoreRef: true})
	if err != nil {
		t.Fatalf("readyBeadsForOpts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ready rows = %d, want 2", len(rows))
	}
	want := map[string]string{"city-1": "city", "rig-1": "rig rig-a"}
	for _, row := range rows {
		if got := row.Metadata[hookClaimStoreMetadataKey]; got != want[row.ID] {
			t.Fatalf("row %s source = %q, want %q", row.ID, got, want[row.ID])
		}
	}

	rows, err = readyBeadsForOpts([]readyLeg{{label: "city", store: city}}, readyOpts{})
	if err != nil {
		t.Fatalf("readyBeadsForOpts without source annotation: %v", err)
	}
	if _, annotated := rows[0].Metadata[hookClaimStoreMetadataKey]; annotated {
		t.Fatal("direct ready output carried the private hook source annotation")
	}
}
