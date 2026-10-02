package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gcapi "github.com/gastownhall/gascity/internal/api"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/runtime/subprocess"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// This fixture uses the production resolver and authorizer, signed admission,
// worker factory, session store and real subprocess provider. No tuple callback.
type routingExecutionTestResolver struct {
	state gcapi.State
	name  string
}

func (r routingExecutionTestResolver) ListCities() []gcapi.CityInfo { return nil }
func (r routingExecutionTestResolver) CityState(name string) gcapi.State {
	if name == r.name {
		return r.state
	}
	return nil
}

func TestRoutingExecutionProductionCutoverInstallsOnlySupportedAdapter(t *testing.T) {
	root := t.TempDir()
	cr := &CityRuntime{cfg: &config.City{RoutingExecution: &config.RoutingExecutionConfig{Enabled: true}}, sp: subprocess.NewSeamBackedWithDir(root)}
	cr.configureRoutingExecutionAdapter()
	if cr.routingExecutionAdapter == nil {
		t.Fatal("production cutover has no local execution adapter")
	}
	cr.sp = runtime.NewFake()
	cr.configureRoutingExecutionAdapter()
	if cr.routingExecutionAdapter != nil {
		t.Fatal("unsupported runtime retained authority")
	}
	cr.sp = subprocess.NewSeamBackedWithDir(root)
	cr.cfg.RoutingExecution.Enabled = false
	cr.configureRoutingExecutionAdapter()
	if cr.routingExecutionAdapter != nil {
		t.Fatal("disabled configuration retained authority")
	}
}

func TestRoutingExecutionRecordingChildRecoveryAndNonmigration(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "legacy-child")
	root := t.TempDir()
	wrapperDir := filepath.Join(root, "owned wrappers with spaces")
	if err := os.MkdirAll(wrapperDir, 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "observed")
	poison := filepath.Join(root, "forbidden-shell")
	if err := os.WriteFile(filepath.Join(wrapperDir, "sh"), []byte("#!/bin/sh\nprintf poison > '"+poison+"'\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapperDir+":/usr/bin:/bin")
	t.Setenv("BASH_ENV", filepath.Join(wrapperDir, "sh"))
	t.Setenv("ENV", filepath.Join(wrapperDir, "sh"))
	executable := filepath.Join(wrapperDir, "account-a")
	contents := []byte("#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\" \"HOME=$HOME\" \"ACCOUNT=$ACCOUNT\" \"ANTHROPIC_MODEL=${ANTHROPIC_MODEL-unset}\" \"OPENAI_API_KEY=${OPENAI_API_KEY-unset}\" >> \"$RECORD\"\nexec /bin/sleep 30\n")
	if err := os.WriteFile(executable, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	binding := config.RoutingExecutionBinding{CanonicalModel: "canonical/model", ServeAs: "Literal/Model", ReasoningEffort: "high", Account: "account-a", Provider: "local-wrapper", AdapterID: "caller-wrapper-v1", Executable: executable, ExecutableDigest: hex.EncodeToString(sum[:]), Args: []string{"--model", "Literal/Model", "--effort", "high"}, ModelArgIndex: 1, EffortArgIndex: 3, WorkDir: root, Environment: map[string]string{"HOME": root, "PATH": "/usr/bin:/bin", "ACCOUNT": "account-a", "RECORD": output}, Transport: "subprocess"}
	command, err := routingExecutionCommand(binding)
	if err != nil {
		t.Fatal(err)
	}
	agent := fixture.cr.cfg.Agents[0]
	agent.Provider = binding.Provider
	agent.Session = "subprocess"
	agent.StartCommand = ""
	agent.WorkDir = root
	fixture.cr.cfg.Agents[0] = agent
	fixture.cr.cfg.Providers = map[string]config.ProviderSpec{binding.Provider: {Command: command, PromptMode: "none"}}
	fixture.cr.cfg.RoutingExecution = &config.RoutingExecutionConfig{Enabled: true, Bindings: map[string]config.RoutingExecutionBinding{fixture.payload.Target: binding}}
	sp := subprocess.NewSeamBackedWithDir(filepath.Join(root, "runtime"))
	fixture.cr.sp = sp
	fixture.cr.configureRoutingExecutionAdapter()
	fixture.cr.standaloneCityStore = beads.NewMemStore()
	actual, err := resolveLocalRoutingExecution(agent, fixture.cr.cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := fixture.payload
	p.DecisionID = "child-v3"
	p.Schema = routingdecision.ExecutionSchemaVersion
	p.RecommendationID = "routing/v3:" + strings.Repeat("e", 64)
	p.Model = binding.CanonicalModel
	p.ServeAs = binding.ServeAs
	p.Provider = binding.Provider
	p.Account = binding.Account
	p.TargetConfigDigest = actual.ConfigDigest
	p.Execution = &actual
	p.BindingID = routingdecision.BindingID(p)
	fixture.cr.routingDecisionVerifier = approveRoutingDecision(t, fixture.ledger, p, fixture.cr.routingDecisionNow())
	old, _ := fixture.ledger.Get(fixture.payload.DecisionID)
	if _, err := fixture.ledger.Transition(routingdecision.TransitionRequest{DecisionID: old.Payload.DecisionID, ExpectedRevision: old.RecordRevision, From: old.State, To: routingdecision.StateRevoked, IdempotencyToken: "revoke-legacy", Reason: "fixture"}, routingdecision.Verifier{}); err != nil {
		t.Fatal(err)
	}
	if n, err := fixture.cr.applyApprovedRoutingDecisions(); err != nil || n != 1 {
		t.Fatalf("production admission: %d %v", n, err)
	}
	manager := sessionpkg.NewManagerWithOptions(fixture.cr.standaloneCityStore, sp)
	info, err := manager.CreateSession(context.Background(), sessionpkg.CreateOptions{BeadOnly: true, Template: p.Target, Command: command, WorkDir: root, Provider: binding.Provider, Transport: "subprocess", ExtraMeta: map[string]string{beadmeta.TriggerBeadIDMetadataKey: p.WorkBeadID}})
	if err != nil {
		t.Fatal(err)
	}
	factory, err := workerFactoryWithLaunchAuthorization(root, fixture.cr.standaloneCityStore, sp, fixture.cr.cfg, nil, fixture.cr.authorizeRoutingExecutionLaunch)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := factory.SessionByID(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	final := runtime.Config{Command: command, WorkDir: root, Env: map[string]string{"HOME": "hostile-home", "ANTHROPIC_MODEL": "hostile-model", "OPENAI_API_KEY": "hostile-not-a-key"}}
	if err := handle.StartResolved(context.Background(), command, final); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sp.Stop(info.SessionName) })
	awaitRecord := func(count int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(output)
			if strings.Count(string(data), "Literal/Model") >= count {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("recording child did not execute")
	}
	awaitRecord(1)
	data, _ := os.ReadFile(output)
	want := executable + "\n--model\nLiteral/Model\n--effort\nhigh\nHOME=" + root + "\nACCOUNT=account-a\nANTHROPIC_MODEL=unset\nOPENAI_API_KEY=unset\n"
	if string(data) != want {
		t.Fatalf("literal tuple/environment changed: %q", data)
	}
	receipts, err := fixture.ledger.ExecutionLaunches(p.DecisionID)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("successful launch receipt: %+v %v", receipts, err)
	}
	if err := sp.Stop(info.SessionName); err != nil {
		t.Fatal(err)
	}
	// Re-resolved tuple/config/adapter/fallback mutations fail at the REAL
	// worker boundary, without a child or a successful-start receipt.
	for _, tc := range []struct {
		name   string
		mutate func(*config.RoutingExecutionBinding)
	}{
		{"serve-as", func(b *config.RoutingExecutionBinding) { b.ServeAs = "Other/Model" }},
		{"canonical", func(b *config.RoutingExecutionBinding) { b.CanonicalModel = "other/model" }},
		{"account", func(b *config.RoutingExecutionBinding) { b.Account = "other-account" }},
		{"max", func(b *config.RoutingExecutionBinding) { b.ReasoningEffort = "max" }},
		{"xhigh", func(b *config.RoutingExecutionBinding) { b.ReasoningEffort = "xhigh" }},
		{"provider", func(b *config.RoutingExecutionBinding) { b.Provider = "other-wrapper" }},
		{"adapter", func(b *config.RoutingExecutionBinding) { b.AdapterID = "other-adapter" }},
		{"bytes", func(b *config.RoutingExecutionBinding) { b.ExecutableDigest = strings.Repeat("a", 64) }},
		{"config", func(b *config.RoutingExecutionBinding) { b.Environment = map[string]string{"HOME": "other-home"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := binding
			tc.mutate(&changed)
			fixture.cr.cfg.RoutingExecution.Bindings[p.Target] = changed
			if err := handle.StartResolved(context.Background(), command, final); err == nil {
				t.Fatal("altered authority launched")
			}
			fixture.cr.cfg.RoutingExecution.Bindings[p.Target] = binding
		})
	}
	if err := handle.StartResolved(context.Background(), command+" --resume different-account", runtime.Config{Command: command + " --resume different-account", WorkDir: root}); err == nil {
		t.Fatal("fallback command substituted signed invocation")
	}
	receipts, err = fixture.ledger.ExecutionLaunches(p.DecisionID)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("denied launch minted receipt: %+v %v", receipts, err)
	}
	if _, err := os.Stat(poison); !os.IsNotExist(err) {
		t.Fatal("hostile outer shell executed")
	}
	// Recovery retains the ORIGINAL authorization even after expiry and claim.
	work, _ := fixture.base.Get(p.WorkBeadID)
	if err := fixture.base.Update(work.ID, beads.UpdateOpts{Status: func() *string { s := "in_progress"; return &s }(), Assignee: &info.ID, Metadata: map[string]string{beadmeta.RoutedToMetadataKey: ""}}); err != nil {
		t.Fatal(err)
	}
	record, _ := fixture.ledger.Get(p.DecisionID)
	if _, err := fixture.ledger.Transition(routingdecision.TransitionRequest{DecisionID: p.DecisionID, ExpectedRevision: record.RecordRevision, From: record.State, To: routingdecision.StateClaimed, IdempotencyToken: "claimed", Reason: "exact claim"}, routingdecision.Verifier{}); err != nil {
		t.Fatal(err)
	}
	fixture.cr.routingDecisionNowFn = func() time.Time { return p.ExpiresAt.Add(time.Hour) }
	if err := handle.StartResolved(context.Background(), command, final); err != nil {
		t.Fatalf("original active recovery refused: %v", err)
	}
	awaitRecord(2)
	receipts, err = fixture.ledger.ExecutionLaunches(p.DecisionID)
	if err != nil || len(receipts) != 2 || receipts[0].ExecutionID == receipts[1].ExecutionID {
		t.Fatalf("recovery did not mint a distinct successful-start receipt: %+v %v", receipts, err)
	}
	_ = sp.Stop(info.SessionName)
	// Real controller state -> typed Huma route -> generated HTTP client -> CLI.
	service := &cityRoutingDecisionService{store: fixture.ledger, executionEnabled: true, status: routingdecision.AvailabilityReady, launchAuthorization: fixture.cr.authorizeRoutingExecutionLaunch}
	state := &controllerState{cfg: fixture.cr.cfg, cityName: fixture.cr.cityName, cityPath: root, cityBeadStore: fixture.cr.standaloneCityStore, sp: sp, routingDecisionService: service}
	mux := gcapi.NewSupervisorMux(routingExecutionTestResolver{state: state, name: fixture.cr.cityName}, nil, false, "test", "", time.Time{})
	prior := routingAPIClientHook
	routingAPIClientHook = func() (*gcapi.Client, error) {
		return gcapi.NewInProcessCityScopedClient(fixture.cr.cityName, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			req.Host = "localhost"
			rec := httptest.NewRecorder()
			mux.Handler().ServeHTTP(rec, req)
			return rec.Result(), nil
		}))
	}
	t.Cleanup(func() { routingAPIClientHook = prior })
	out, stderr, err := executeRoutingCommand(t, "outcomes", "--json", "--limit", "100")
	if err != nil {
		t.Fatalf("real producer CLI/HTTP: %s %v", stderr, err)
	}
	var page routingdecision.ProducerExecutionOutcomePage
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if page.SchemaVersion != "routing/outcome/v3" || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("producer page: %s", out)
	}
	row := page.Items[0]
	if row.RoutingDecisionID == nil || *row.RoutingDecisionID != p.DecisionID || row.RequestedConfigDigest != "sha256:"+p.TargetConfigDigest || row.ActualTargetID == nil || *row.ActualTargetID != p.Target || row.Status != "unknown" || row.Disposition != routingdecision.OutcomeDispositionUnknown {
		t.Fatalf("producer causal facts fabricated: %s", out)
	}
	// Ordinary read handler is exercised above; invalid pagination stays typed 4xx.
	req := httptest.NewRequest(http.MethodGet, "http://localhost/v0/city/"+fixture.cr.cityName+"/routing/outcomes-v3?cursor=bad", nil)
	rec := httptest.NewRecorder()
	mux.Handler().ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor: %d %s", rec.Code, body)
	}
	t.Logf("authoritative producer outcome page: %s", out)
	// Cleared trigger + both markers must not become legacy; independent ledger wins.
	if err := fixture.cr.standaloneCityStore.Update(info.ID, beads.UpdateOpts{Labels: []string{sessionpkg.LabelSession}, Metadata: map[string]string{beadmeta.TriggerBeadIDMetadataKey: "", sessionpkg.RoutingExecutionDecisionMetadataKey: ""}}); err != nil {
		t.Fatal(err)
	}
	if err := handle.StartResolved(context.Background(), command, final); err == nil {
		t.Fatal("durable authority lost through mutable-marker bypass")
	}
	unguarded := sessionpkg.NewManagerWithOptions(fixture.cr.standaloneCityStore, sp, sessionpkg.WithCityPath(fixture.cr.cityPath))
	if err := unguarded.StartRuntimeOnly(context.Background(), info.ID, command, final); err == nil {
		t.Fatal("missing caller adapter plus cleared markers became a legacy launch")
	}
	data, _ = os.ReadFile(output)
	if strings.Count(string(data), "Literal/Model") != 2 {
		t.Fatal("forbidden third launch")
	}
}
