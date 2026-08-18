package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gcapi "github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

func TestRoutingCLICommandGroupIsPublicAndComplete(t *testing.T) {
	root := newRootCmd(&bytes.Buffer{}, &bytes.Buffer{})
	group, _, err := root.Find([]string{"routing"})
	if err != nil || group == nil || group.Hidden {
		t.Fatalf("routing group = (%v, %v), want visible command", group, err)
	}
	want := map[string]bool{"status": false, "targets": false, "eligible": false, "decisions": false, "ingest": false}
	for _, child := range group.Commands() {
		if _, ok := want[child.Name()]; ok {
			want[child.Name()] = !child.Hidden
		}
	}
	for name, visible := range want {
		if !visible {
			t.Errorf("routing %s missing or hidden", name)
		}
	}
}

func withRoutingCLIClient(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	prior := routingAPIClientHook
	routingAPIClientHook = func() (*gcapi.Client, error) {
		return gcapi.NewInProcessCityScopedClient("acme", transport)
	}
	t.Cleanup(func() { routingAPIClientHook = prior })
}

func routingCLIResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func executeRoutingCommand(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := newRoutingCmd(&stdout, &stderr)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

func TestRoutingCLIReadCommandsAreLiveAPIOnly(t *testing.T) {
	transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v0/city/acme/routing/status":
			return routingCLIResponse(t, http.StatusOK, routingdecision.LiveStatus{Schema: 1, Status: "ready", Reason: "ready"}), nil
		case "/v0/city/acme/routing/targets":
			return routingCLIResponse(t, http.StatusOK, struct {
				Items []routingdecision.TargetSnapshot `json:"items"`
			}{Items: []routingdecision.TargetSnapshot{{Target: "worker", Rig: "rig-a"}}}), nil
		case "/v0/city/acme/routing/eligible":
			return routingCLIResponse(t, http.StatusOK, routingdecision.SelectionSnapshot{ObservedAt: time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC), Work: []routingdecision.EligibleWorkSnapshot{}, Targets: []routingdecision.TargetSnapshot{}}), nil
		case "/v0/city/acme/routing/decisions":
			if r.URL.Query().Get("state") != "claimed" || r.URL.Query().Get("limit") != "7" || r.URL.Query().Get("cursor") != "next" {
				t.Fatalf("decision query = %q", r.URL.RawQuery)
			}
			return routingCLIResponse(t, http.StatusOK, struct {
				Items []routingdecision.DecisionWithAudits `json:"items"`
				Total int                                  `json:"total"`
			}{Items: []routingdecision.DecisionWithAudits{}}), nil
		default:
			return routingCLIResponse(t, http.StatusNotFound, struct{}{}), nil
		}
	})
	withRoutingCLIClient(t, transport)

	tests := []struct {
		args     []string
		wantKeys []string
	}{
		{args: []string{"status", "--json"}, wantKeys: []string{"status"}},
		{args: []string{"targets", "--json"}, wantKeys: []string{"items"}},
		{args: []string{"eligible", "--json"}, wantKeys: []string{"observed_at"}},
		{args: []string{"decisions", "--state", "claimed", "--limit", "7", "--cursor", "next", "--json"}, wantKeys: []string{"items", "total"}},
	}
	for _, test := range tests {
		stdout, stderr, err := executeRoutingCommand(t, test.args...)
		if err != nil {
			t.Fatalf("routing %v: %v stderr=%s", test.args, err, stderr)
		}
		var output map[string]json.RawMessage
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &output); err != nil {
			t.Fatalf("routing %v output is not JSON: %q: %v", test.args, stdout, err)
		}
		if string(output["ok"]) != "true" {
			t.Fatalf("routing %v output = %s, want ok", test.args, stdout)
		}
		for _, wantKey := range test.wantKeys {
			if output[wantKey] == nil {
				t.Fatalf("routing %v output = %s, want %q", test.args, stdout, wantKey)
			}
		}
	}
}

func TestRoutingCLIReadCommandsRenderSelectionFacts(t *testing.T) {
	transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v0/city/acme/routing/status":
			return routingCLIResponse(t, http.StatusOK, routingdecision.LiveStatus{
				Schema: 1, Status: "ready", Reason: "ready", AuthorityReady: true,
				RetentionMonths: 6, TerminalStateBasis: "latest_transition_at",
				Store: routingdecision.StoreStatus{
					SchemaVersion: 1, StoreRevision: 12,
					StateCounts: []routingdecision.StateCount{{State: routingdecision.StateApproved, Count: 2}},
				},
			}), nil
		case "/v0/city/acme/routing/targets":
			return routingCLIResponse(t, http.StatusOK, struct {
				Items []routingdecision.TargetSnapshot `json:"items"`
			}{Items: []routingdecision.TargetSnapshot{{Rig: "rig-a", Target: "worker-a", ResolvedProvider: "provider-a", ConfigDigest: "cfg-a", Description: "Primary target"}}}), nil
		case "/v0/city/acme/routing/eligible":
			return routingCLIResponse(t, http.StatusOK, routingdecision.SelectionSnapshot{
				ObservedAt: time.Date(2026, 8, 7, 20, 0, 0, 123, time.UTC),
				Work:       []routingdecision.EligibleWorkSnapshot{{Rig: "rig-a", WorkBeadID: "work-a", WorkRevision: 3, ClaimFence: 4, WorkStateDigest: "work-digest"}},
				Targets:    []routingdecision.TargetSnapshot{{Rig: "rig-a", Target: "worker-a", ResolvedProvider: "provider-a", ConfigDigest: "cfg-a", Description: "Primary target"}},
			}), nil
		case "/v0/city/acme/routing/decisions":
			return routingCLIResponse(t, http.StatusOK, struct {
				Items      []routingdecision.DecisionWithAudits `json:"items"`
				Total      int                                  `json:"total"`
				NextCursor string                               `json:"next_cursor,omitempty"`
			}{Items: []routingdecision.DecisionWithAudits{{Record: routingdecision.Record{
				Payload: routingdecision.DecisionPayload{DecisionID: "decision-a", WorkBeadID: "work-a", Target: "worker-a"},
				State:   routingdecision.StateApproved, RecordRevision: 5,
			}}}, Total: 1, NextCursor: "decision-a"}), nil
		default:
			t.Fatalf("unexpected routing path %q", r.URL.Path)
			return nil, nil
		}
	})
	withRoutingCLIClient(t, transport)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "status", args: []string{"status"}, want: []string{"Routing schema: 1", "Status: ready", "Retention: 6 calendar months (latest_transition_at)", "Ledger schema: 1", "Store revision: 12", "approved", "2"}},
		{name: "targets", args: []string{"targets"}, want: []string{"RIG", "TARGET", "PROVIDER", "CONFIG DIGEST", "rig-a", "worker-a", "provider-a", "cfg-a", "Primary target"}},
		{name: "eligible", args: []string{"eligible"}, want: []string{"Observed: 2026-08-07T20:00:00.000000123Z", "work-a", "work-digest", "Targets:", "worker-a", "provider-a", "cfg-a"}},
		{name: "decisions", args: []string{"decisions"}, want: []string{"DECISION", "STATE", "decision-a", "approved", "work-a", "worker-a", "Total: 1", "Next cursor: decision-a"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := executeRoutingCommand(t, test.args...)
			if err != nil {
				t.Fatalf("routing %v: %v stderr=%s", test.args, err, stderr)
			}
			for _, want := range test.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout %q does not contain %q", stdout, want)
				}
			}
		})
	}
}

func TestRoutingCLIDecisionFiltersFailBeforeClientResolution(t *testing.T) {
	prior := routingAPIClientHook
	var resolutions int
	routingAPIClientHook = func() (*gcapi.Client, error) {
		resolutions++
		return nil, context.Canceled
	}
	t.Cleanup(func() { routingAPIClientHook = prior })

	for _, args := range [][]string{{"decisions", "--state", "unknown"}, {"decisions", "--limit", "0"}, {"decisions", "--limit", "257"}} {
		if _, _, err := executeRoutingCommand(t, args...); err == nil {
			t.Errorf("routing %v succeeded", args)
		}
	}
	if resolutions != 0 {
		t.Fatalf("invalid filters resolved live client %d times", resolutions)
	}
}

func TestRoutingCLIIngestUsesBindingOnStdinAndNeverDisclosesToken(t *testing.T) {
	var requests int
	transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("X-GC-City-Write") != "secret-grant-token" || r.Header.Get("Idempotency-Key") != "idem-1" {
			t.Fatalf("headers = %#v", r.Header)
		}
		return routingCLIResponse(t, http.StatusCreated, routingdecision.IngestApprovedResult{
			Record:  routingdecision.Record{Payload: routingdecision.DecisionPayload{DecisionID: "decision-cli"}, State: routingdecision.StateApproved},
			Receipt: routingdecision.TransitionReceipt{DecisionID: "decision-cli", State: routingdecision.StateApproved},
		}), nil
	})
	withRoutingCLIClient(t, transport)

	path := filepath.Join(t.TempDir(), "signed.json")
	if err := os.WriteFile(path, []byte(`{"payload":{"decision_id":"decision-cli"},"approval":{},"signature":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	priorRunner := routingGrantCommandRun
	var gotCommand string
	var gotBinding gcapi.GrantBinding
	routingGrantCommandRun = func(_ context.Context, command string, binding gcapi.GrantBinding) (string, error) {
		gotCommand, gotBinding = command, binding
		return "secret-grant-token", nil
	}
	t.Cleanup(func() { routingGrantCommandRun = priorRunner })

	stdout, stderr, err := executeRoutingCommand(t, "ingest", "--file", path, "--idempotency-key", "idem-1", "--write-grant-command", "signer --key external", "--json")
	if err != nil {
		t.Fatalf("ingest: %v stderr=%s", err, stderr)
	}
	if requests != 1 || gotCommand != "signer --key external" || gotBinding.Method != http.MethodPost || gotBinding.BodySHA256 == "" || gotBinding.ReqDigest == "" {
		t.Fatalf("requests=%d command=%q binding=%+v", requests, gotCommand, gotBinding)
	}
	if strings.Contains(stdout, "secret-grant-token") || strings.Contains(stderr, "secret-grant-token") {
		t.Fatalf("grant token disclosed: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestRoutingCLIIngestFailsBeforeSendWithoutGrantCommandOrTypedFile(t *testing.T) {
	var requests int
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return routingCLIResponse(t, http.StatusInternalServerError, struct{}{}), nil
	})
	withRoutingCLIClient(t, transport)
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(valid, []byte(`{"payload":{},"approval":{},"signature":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte(`{"payload":{},"approval":{},"signature":{},"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeRoutingCommand(t, "ingest", "--file", valid, "--idempotency-key", "idem"); err == nil {
		t.Fatal("missing --write-grant-command must fail")
	}
	if _, _, err := executeRoutingCommand(t, "ingest", "--file", invalid, "--idempotency-key", "idem", "--write-grant-command", "signer"); err == nil {
		t.Fatal("unknown typed file field must fail")
	}
	if requests != 0 {
		t.Fatalf("requests before validation = %d", requests)
	}
}

func TestRunRoutingGrantCommandSendsExactBindingOnStdin(t *testing.T) {
	dir := t.TempDir()
	capturedPath := filepath.Join(dir, "binding.json")
	scriptPath := filepath.Join(dir, "grant-source")
	signature := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	token := base64.RawURLEncoding.EncodeToString([]byte(`{"aud":"city-write"}`)) + "." + signature
	script := "#!/bin/sh\nset -eu\ncat >\"$1\"\nprintf '%s\\n' '" + token + "'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	binding := gcapi.GrantBinding{
		Method: http.MethodPost, Path: "/v0/city/acme/routing/decisions",
		CanonicalQuery: "", BodySHA256: "body-digest", ReqDigest: "request-digest",
	}
	gotToken, err := runRoutingGrantCommand(context.Background(), scriptPath+" "+capturedPath, binding)
	if err != nil {
		t.Fatalf("runRoutingGrantCommand: %v", err)
	}
	if gotToken != token {
		t.Fatalf("token = %q, want exact command output", gotToken)
	}
	captured, err := os.ReadFile(capturedPath)
	if err != nil {
		t.Fatal(err)
	}
	var gotBinding gcapi.GrantBinding
	if err := json.Unmarshal(captured, &gotBinding); err != nil {
		t.Fatalf("binding stdin = %q: %v", captured, err)
	}
	if gotBinding != binding {
		t.Fatalf("binding stdin = %+v, want %+v", gotBinding, binding)
	}
}
