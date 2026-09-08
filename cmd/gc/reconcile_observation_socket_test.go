package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/reconcileobservation"
)

// snapshotOverSocket drives one reconciliation-snapshot request through the
// real controller connection handler.
func snapshotOverSocket(t *testing.T, observe func() *reconcileobservation.Observation) reconciliationSnapshotReply {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close() //nolint:errcheck

	cityDir := t.TempDir()
	done := make(chan struct{})
	go func() {
		handleControllerConn(server, cityDir, controllerHostingStandalone, func() {}, nil, nil, nil,
			make(chan convergenceRequest, 1), make(chan struct{}, 1), make(chan struct{}, 1), observe)
		close(done)
	}()

	if _, err := fmt.Fprintln(client, "reconciliation-snapshot"); err != nil {
		t.Fatalf("write command: %v", err)
	}
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	var reply reconciliationSnapshotReply
	if err := json.Unmarshal([]byte(line), &reply); err != nil {
		t.Fatalf("decode reply %q: %v", line, err)
	}
	client.Close() //nolint:errcheck
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("controller socket handler did not exit")
	}
	// The city dir must be untouched: this command answers from memory, and a
	// trace directory appearing here would mean it went to disk after all.
	if entries, err := os.ReadDir(cityDir); err == nil && len(entries) != 0 {
		t.Fatalf("the snapshot command wrote to the city directory: %v", entries)
	}
	return reply
}

// The whole point of `snapshot` as a subcommand distinct from `status`: it
// answers from the controller's memory and never opens a trace segment. A city
// with tracing switched off has no trace status worth printing and a perfectly
// good snapshot.
func TestReconciliationSnapshotSocketServesThePublishedObservation(t *testing.T) {
	cr := newObservationRuntime(t)
	runCycle(cr, "patrol", TraceCompletionCompleted, func() {
		cr.observeReconcileInputs(
			map[string]int{"gastown.mayor": 2}, map[string]int{"gastown.mayor": 1},
			map[string]int{}, map[string]bool{}, map[string]bool{}, map[string]bool{},
			map[string]struct{}{"gastown.mayor": {}}, 2, 1, DesiredStateResult{})
	})

	reply := snapshotOverSocket(t, cr.ReconciliationObservation)
	if !reply.OK || reply.Observation == nil {
		t.Fatalf("snapshot failed: ok=%v err=%q", reply.OK, reply.Error)
	}
	obs := reply.Observation
	if obs.City != "testcity" || obs.Totals.OpenSessionCount != 2 {
		t.Fatalf("the reply is not the published observation: %+v", obs)
	}
	if len(obs.Templates) != 1 || obs.Templates[0].Template != "gastown.mayor" {
		t.Fatalf("template rows lost across the socket: %+v", obs.Templates)
	}
	if obs.Trace.Enabled {
		t.Error("Trace.Enabled true for a cycle that ran without a trace")
	}
}

// "No observation" and "an observation of nothing" must stay distinguishable
// at the CLI too, and the reason must be specific enough to act on.
func TestReconciliationSnapshotSocketDistinguishesNoObservation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		observe func() *reconcileobservation.Observation
		want    string
	}{
		{"no runtime attached", nil, "no city runtime attached"},
		{"no cycle completed yet", newObservationRuntime(t).ReconciliationObservation, "has not completed a cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := snapshotOverSocket(t, tc.observe)
			if reply.OK {
				t.Fatal("an absent observation was reported as success")
			}
			if reply.Observation != nil {
				t.Fatalf("an empty observation was invented: %+v", reply.Observation)
			}
			if !strings.Contains(reply.Error, tc.want) {
				t.Fatalf("error %q does not say why (want %q)", reply.Error, tc.want)
			}
		})
	}
}

// `gc trace snapshot` must not acquire a local disk fallback. `status` has one
// — traceStatusLocal reads head/arm state off disk when the controller is
// unreachable — and copying that here would answer a different question with
// stale bytes while looking like the same command.
func TestReconciliationSnapshotHasNoDiskFallback(t *testing.T) {
	src, err := os.ReadFile("session_reconciler_trace_cmd.go")
	if err != nil {
		t.Fatalf("read trace cmd: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func cmdTraceSnapshot(")
	if start < 0 {
		t.Fatal("cmdTraceSnapshot is gone")
	}
	end := strings.Index(body[start:], "\nfunc ")
	if end < 0 {
		end = len(body) - start
	}
	fn := body[start : start+end]
	for _, banned := range []string{
		"traceStatusLocal", "traceStatusHeadSeq", "ReadTraceRecords",
		"newSessionReconcilerTraceStore", "os.ReadFile", "os.Open",
	} {
		if strings.Contains(fn, banned) {
			t.Errorf("cmdTraceSnapshot references %q; the snapshot must come from the controller's memory, not from disk", banned)
		}
	}
}

// The subcommand has to be reachable, or none of the above is.
func TestReconciliationSnapshotSubcommandIsRegistered(t *testing.T) {
	cmd := newTraceCmd(os.Stdout, os.Stderr)
	for _, sub := range cmd.Commands() {
		if sub.Name() == "snapshot" {
			return
		}
	}
	t.Fatal("gc trace has no snapshot subcommand")
}

func TestReconciliationSnapshotDeclaresAndEmitsJSON(t *testing.T) {
	var schemaOut, schemaErr bytes.Buffer
	root := newRootCmd(&schemaOut, &schemaErr)
	handled, code := handleJSONSchemaRequest(root, []string{"trace", "snapshot", "--json-schema=result"}, &schemaOut)
	if !handled || code != 0 {
		t.Fatalf("schema request handled=%v code=%d stderr=%q stdout=%q", handled, code, schemaErr.String(), schemaOut.String())
	}
	if schemaErr.Len() != 0 {
		t.Fatalf("schema request stderr=%q", schemaErr.String())
	}

	var out, stderr bytes.Buffer
	obs := &reconcileobservation.Observation{
		SchemaVersion: reconcileobservation.SchemaVersion,
		City:          "testcity",
		Templates:     []reconcileobservation.TemplateRow{},
	}
	if code := writeReconciliationSnapshotJSON(&out, &stderr, obs); code != 0 {
		t.Fatalf("write JSON code=%d stderr=%q", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if result["ok"] != true || result["city"] != "testcity" {
		t.Fatalf("output lost success discriminator or observation: %v", result)
	}
}
