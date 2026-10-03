package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/runtime/subprocess"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	bbolt "go.etcd.io/bbolt"
)

func adversarialExecutionFixture(t *testing.T, script string, mutate ...func(*config.RoutingExecutionBinding)) (routingDecisionFixture, runtime.Provider, sessionpkg.Info, routingdecision.DecisionPayload, string, runtime.Config, string, *fsnotify.Watcher, func() error) {
	t.Helper()
	fixture := newApprovedRoutingDecisionFixture(t, "legacy-child")
	root := t.TempDir()
	wrapperDir := filepath.Join(root, "owned wrappers with spaces")
	if err := os.MkdirAll(wrapperDir, 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "observed")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Close() })
	if err := watcher.Add(root); err != nil {
		t.Fatal(err)
	}
	poison := filepath.Join(root, "forbidden-shell")
	if err := os.WriteFile(filepath.Join(wrapperDir, "sh"), []byte("#!/bin/sh\nprintf poison > '"+poison+"'\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapperDir+":/usr/bin:/bin")
	t.Setenv("BASH_ENV", filepath.Join(wrapperDir, "sh"))
	t.Setenv("ENV", filepath.Join(wrapperDir, "sh"))
	executable := filepath.Join(wrapperDir, "account-a")
	contents := []byte(script)
	if err := os.WriteFile(executable, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	binding := config.RoutingExecutionBinding{CanonicalModel: "canonical/model", ServeAs: "Literal/Model", ReasoningEffort: "high", Account: "account-a", Provider: "local-wrapper", AdapterID: "caller-wrapper-v1", Executable: executable, ExecutableDigest: hex.EncodeToString(sum[:]), Args: []string{"--model", "Literal/Model", "--effort", "high"}, ModelArgIndex: 1, EffortArgIndex: 3, WorkDir: root, Environment: map[string]string{"HOME": root, "PATH": "/usr/bin:/bin", "ACCOUNT": "account-a", "RECORD": output}, Transport: "subprocess"}
	for _, change := range mutate {
		change(&binding)
	}
	command, err := routingExecutionCommand(binding)
	if err != nil {
		t.Fatal(err)
	}
	agent := fixture.cr.cfg.Agents[0]
	agent.Provider = binding.Provider
	agent.Session = "subprocess"
	agent.StartCommand = ""
	agent.WorkDir = binding.WorkDir
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
	info, err := manager.CreateSession(context.Background(), sessionpkg.CreateOptions{BeadOnly: true, Template: p.Target, Command: command, WorkDir: binding.WorkDir, Provider: binding.Provider, Transport: "subprocess", ExtraMeta: map[string]string{beadmeta.TriggerBeadIDMetadataKey: p.WorkBeadID}})
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
	final := runtime.Config{Command: command, WorkDir: binding.WorkDir, Env: map[string]string{"HOME": "hostile-home", "ANTHROPIC_MODEL": "hostile-model", "OPENAI_API_KEY": "hostile-not-a-key"}}
	t.Cleanup(func() { _ = sp.Stop(info.SessionName) })
	return fixture, sp, info, p, command, final, output, watcher, func() error { return handle.StartResolved(context.Background(), command, final) }
}

func TestAdversarialUnavailableAuthorityDoesNotDowngrade(t *testing.T) {
	fixture, sp, info, p, _, _, output, watcher, start := adversarialExecutionFixture(t, "#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\" \"HOME=$HOME\" \"ACCOUNT=$ACCOUNT\" \"ANTHROPIC_MODEL=${ANTHROPIC_MODEL-unset}\" \"OPENAI_API_KEY=${OPENAI_API_KEY-unset}\" >> \"$RECORD\"\nexec /bin/sleep 30\n")
	if err := start(); err != nil {
		t.Fatal(err)
	}
	binding := fixture.cr.cfg.RoutingExecution.Bindings[p.Target]
	executable, root := binding.Executable, binding.WorkDir
	want := executable + "\n--model\nLiteral/Model\n--effort\nhigh\nHOME=" + root + "\nACCOUNT=account-a\nANTHROPIC_MODEL=unset\nOPENAI_API_KEY=unset\n"
	awaitRecord := func(count int) { awaitRoutingExecutionWitness(t, watcher, output, want, count) }
	awaitRecord(1)
	data, _ := os.ReadFile(output)
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
	if err := start(); err != nil {
		t.Fatalf("original active recovery refused: %v", err)
	}
	awaitRecord(2)
	receipts, err = fixture.ledger.ExecutionLaunches(p.DecisionID)
	if err != nil || len(receipts) != 2 || receipts[0].ExecutionID == receipts[1].ExecutionID {
		t.Fatalf("recovery did not mint a distinct successful-start receipt: %+v %v", receipts, err)
	}
	_ = sp.Stop(info.SessionName)

	if err := fixture.cr.standaloneCityStore.Update(info.ID, beads.UpdateOpts{Labels: []string{sessionpkg.LabelSession}, Metadata: map[string]string{beadmeta.TriggerBeadIDMetadataKey: "", sessionpkg.RoutingExecutionDecisionMetadataKey: ""}}); err != nil {
		t.Fatal(err)
	}
	// Model controller initialization returning without an open routing store.
	fixture.cr.routingDecisionStore = nil
	fixture.cr.routingDecisionVerifier = nil
	t.Setenv("PATH", "/usr/bin:/bin")
	err = start()
	if err == nil {
		awaitRecord(3)
		t.Fatal("SECURITY: cleared-marker bound session launched real child with unavailable controller authority")
	}
}

func TestAdversarialMissingInterpreterDoesNotMintActualExecution(t *testing.T) {
	fixture, sp, info, p, _, _, output, _, start := adversarialExecutionFixture(t, "#!/nonexistent-owned-fixture-interpreter\nprintf executed > \"$RECORD\"\n")
	if err := start(); err == nil {
		t.Fatal("failed executable exec accepted as successful Start")
	}
	if sp.IsRunning(info.SessionName) {
		t.Fatal("failed exec running")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("unexpected executable witness")
	}
	receipts, err := fixture.ledger.ExecutionLaunches(p.DecisionID)
	if err != nil || len(receipts) != 0 {
		t.Fatalf("successful launch receipt: %+v %v", receipts, err)
	}

	item := routingdecision.DecisionWithAudits{Record: func() routingdecision.Record { r, _ := fixture.ledger.Get(p.DecisionID); return r }()}
	row, ok, err := routingdecision.ProjectProducerExecutionOutcome(item, receipts)
	if err != nil {
		t.Fatal(err)
	}
	if ok && row.ActualTargetID != nil {
		t.Fatalf("CAUSAL: authorized executable never ran, but actual tuple published: %+v", row)
	}
}

func TestAdversarialRealPreWakePreservesOriginalWorkRecovery(t *testing.T) {
	fixture, sp, info, p, _, _, output, watcher, start := adversarialExecutionFixture(t, "#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\" \"HOME=$HOME\" \"ACCOUNT=$ACCOUNT\" \"ANTHROPIC_MODEL=${ANTHROPIC_MODEL-unset}\" \"OPENAI_API_KEY=${OPENAI_API_KEY-unset}\" >> \"$RECORD\"\nexec /bin/sleep 30\n")
	if err := start(); err != nil {
		t.Fatal(err)
	}
	binding := fixture.cr.cfg.RoutingExecution.Bindings[p.Target]
	executable, root := binding.Executable, binding.WorkDir
	want := executable + "\n--model\nLiteral/Model\n--effort\nhigh\nHOME=" + root + "\nACCOUNT=account-a\nANTHROPIC_MODEL=unset\nOPENAI_API_KEY=unset\n"
	awaitRecord := func(count int) { awaitRoutingExecutionWitness(t, watcher, output, want, count) }
	awaitRecord(1)
	data, _ := os.ReadFile(output)
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
	if err := start(); err != nil {
		t.Fatalf("original active recovery refused: %v", err)
	}
	awaitRecord(2)
	receipts, err = fixture.ledger.ExecutionLaunches(p.DecisionID)
	if err != nil || len(receipts) != 2 || receipts[0].ExecutionID == receipts[1].ExecutionID {
		t.Fatalf("recovery did not mint a distinct successful-start receipt: %+v %v", receipts, err)
	}
	_ = sp.Stop(info.SessionName)

	current, err := sessionpkg.NewStore(beads.SessionStore{Store: fixture.cr.standaloneCityStore}).Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	newGen, _, _, err := preWakeCommit(current, sessionpkg.NewStore(beads.SessionStore{Store: fixture.cr.standaloneCityStore}), clock.Real{}, fixture.cr.authorizeRoutingExecutionWake)
	if err != nil {
		t.Fatal(err)
	}
	if err := start(); err != nil {
		t.Fatalf("LIFECYCLE: real preWakeCommit generation %d makes original claimed work unrecoverable: %v", newGen, err)
	}
	awaitRecord(3)
	current, err = sessionpkg.NewStore(beads.SessionStore{Store: fixture.cr.standaloneCityStore}).Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	receipts, err = fixture.ledger.ExecutionLaunches(p.DecisionID)
	if err != nil || len(receipts) != 3 {
		t.Fatalf("successor receipts: %+v %v", receipts, err)
	}
	found := false
	for _, receipt := range receipts {
		if receipt.Authorization.Generation == current.Generation && receipt.Authorization.InstanceToken == current.InstanceToken {
			found = true
		}
	}
	if !found {
		t.Fatal("receipt did not bind actual successor incarnation")
	}
	original, _ := fixture.ledger.ExecutionSession(info.ID)
	if original.Generation != info.Generation || original.InstanceToken != info.InstanceToken {
		t.Fatal("original immutable authority overwritten")
	}
	if err := sp.Stop(info.SessionName); err != nil {
		t.Fatal(err)
	}
	witness, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	front := sessionpkg.NewStore(beads.SessionStore{Store: fixture.cr.standaloneCityStore})
	for _, changed := range []map[string]string{
		{"generation": info.Generation, "instance_token": info.InstanceToken},
		{"generation": "999"},
		{"instance_token": "forged-token"},
		{beadmeta.TriggerBeadIDMetadataKey: "other-work"},
		{sessionpkg.RoutingExecutionDecisionMetadataKey: "other-decision"},
	} {
		if err := fixture.cr.standaloneCityStore.Update(info.ID, beads.UpdateOpts{Metadata: changed}); err != nil {
			t.Fatal(err)
		}
		mutated, err := front.Get(info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.cr.authorizeRoutingExecutionWake(mutated, "3", "forged-successor"); err == nil {
			t.Fatal("arbitrary authority obtained successor")
		}
		if err := start(); err == nil {
			t.Fatal("arbitrary incarnation/work/decision launched")
		}
		if err := fixture.cr.standaloneCityStore.Update(info.ID, beads.UpdateOpts{Metadata: map[string]string{"generation": current.Generation, "instance_token": current.InstanceToken, beadmeta.TriggerBeadIDMetadataKey: p.WorkBeadID, sessionpkg.RoutingExecutionDecisionMetadataKey: p.DecisionID}}); err != nil {
			t.Fatal(err)
		}
	}
	binding = fixture.cr.cfg.RoutingExecution.Bindings[p.Target]
	changed := binding
	changed.ServeAs = "Other/Model"
	fixture.cr.cfg.RoutingExecution.Bindings[p.Target] = changed
	if err := fixture.cr.authorizeRoutingExecutionWake(current, "3", "changed-tuple"); err == nil {
		t.Fatal("changed tuple obtained successor")
	}
	if err := start(); err == nil {
		t.Fatal("changed tuple launched")
	}
	fixture.cr.cfg.RoutingExecution.Bindings[p.Target] = binding
	after, err := fixture.ledger.ExecutionLaunches(p.DecisionID)
	if err != nil || !reflect.DeepEqual(receipts, after) {
		t.Fatalf("denied recovery changed receipts: %+v %v", after, err)
	}
	got, _ := os.ReadFile(output)
	if string(got) != string(witness) {
		t.Fatal("denied recovery launched child")
	}
	// Final prepared check uses the controller-issued actual successor, too.
	if err := fixture.cr.checkRoutingExecutionLaunch(p.Target, p.Rig, current, runtime.Config{Command: func() string { s, _ := routingExecutionCommand(binding); return s }(), WorkDir: binding.WorkDir}); err != nil {
		t.Fatalf("prepared successor refused: %v", err)
	}
}

func TestRoutingExecutionUnavailableAuthorityMatrix(t *testing.T) {
	for _, state := range []string{"locked", "bound", "corrupt", "unknown", "absent"} {
		t.Run(state, func(t *testing.T) {
			fixture, sp, info, payload, command, final, output, watcher, start := adversarialExecutionFixture(t, "#!/bin/sh\nprintf executed >> \"$RECORD\"\nexec /bin/sleep 30\n")
			if err := start(); err != nil {
				t.Fatal(err)
			}
			awaitRoutingExecutionWitness(t, watcher, output, "executed", 1)
			if err := sp.Stop(info.SessionName); err != nil {
				t.Fatal(err)
			}
			before, err := fixture.ledger.ExecutionLaunches(payload.DecisionID)
			if err != nil || len(before) != 1 {
				t.Fatalf("initial receipt: %+v %v", before, err)
			}
			witness, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.cr.standaloneCityStore.Update(info.ID, beads.UpdateOpts{Labels: []string{sessionpkg.LabelSession}, Metadata: map[string]string{beadmeta.TriggerBeadIDMetadataKey: "", sessionpkg.RoutingExecutionDecisionMetadataKey: ""}}); err != nil {
				t.Fatal(err)
			}
			ledgerRoot := fixture.cr.cityPath
			fixture.cr.routingDecisionStore, fixture.cr.routingDecisionVerifier = nil, nil
			if state == "bound" {
				if err := fixture.ledger.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if state == "corrupt" || state == "unknown" || state == "absent" {
				fixture.cr.cityPath = t.TempDir()
				path := filepath.Join(fixture.cr.cityPath, routingdecision.StoreRelativePath)
				if state != "absent" {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if state == "corrupt" {
						if err := os.WriteFile(path, []byte("corrupt ledger"), 0o600); err != nil {
							t.Fatal(err)
						}
					} else {
						db, err := bbolt.Open(path, 0o600, nil)
						if err != nil {
							t.Fatal(err)
						}
						if err := db.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			for _, installed := range []bool{false, true} {
				var authorize sessionpkg.LaunchAuthorization
				if installed {
					authorize = fixture.cr.authorizeRoutingExecutionLaunch
				}
				manager := sessionpkg.NewManagerWithOptions(fixture.cr.standaloneCityStore, sp, sessionpkg.WithCityPath(fixture.cr.cityPath), sessionpkg.WithLaunchAuthorization(authorize))
				err := manager.StartRuntimeOnly(context.Background(), info.ID, command, final)
				if state == "absent" {
					if err != nil {
						t.Fatalf("genuinely absent legacy (hook=%v): %v", installed, err)
					}
					if err := sp.Stop(info.SessionName); err != nil {
						t.Fatal(err)
					}
				} else {
					if err == nil {
						t.Fatalf("%s authority bypass (hook=%v)", state, installed)
					}
					if sp.IsRunning(info.SessionName) {
						t.Fatal("denied child running")
					}
					got, _ := os.ReadFile(output)
					if string(got) != string(witness) {
						t.Fatal("denied child changed recorder")
					}
					current, err := sessionpkg.NewStore(beads.SessionStore{Store: fixture.cr.standaloneCityStore}).Get(info.ID)
					if err != nil {
						t.Fatal(err)
					}
					if err := fixture.cr.checkRoutingExecutionLaunch(payload.Target, payload.Rig, current, final); err == nil {
						t.Fatal("prepared boundary accepted unavailable authority")
					}
				}
			}
			ledger := fixture.ledger
			if state == "bound" {
				ledger, err = routingdecision.OpenStore(ledgerRoot, routingdecision.StoreOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer ledger.Close() //nolint:errcheck
			}
			after, err := ledger.ExecutionLaunches(payload.DecisionID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("denied/legacy launch changed receipts: %+v %v", after, err)
			}
		})
	}
}

func TestRoutingExecutionExecFailureAndFastExit(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		success      bool
	}{
		{"missing-interpreter", "#!/nonexistent-owned-fixture-interpreter\nprintf executed >> \"$RECORD\"\n", false},
		{"exec-format", "not an executable image", false},
		{"start-failure", "#!/bin/sh\nprintf executed >> \"$RECORD\"\n", false},
		{"fast-exit", "#!/bin/sh\nprintf executed >> \"$RECORD\"\nexit 7\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, sp, info, payload, _, _, output, watcher, start := adversarialExecutionFixture(t, tc.script, func(binding *config.RoutingExecutionBinding) {
				if tc.name == "start-failure" {
					binding.WorkDir = filepath.Join(binding.WorkDir, "missing-workdir")
				}
			})
			err := start()
			if (err == nil) != tc.success {
				t.Fatalf("exec result success=%v: %v", tc.success, err)
			}
			if tc.success {
				// Wait for the recorder only, never as a proxy authorizing the receipt.
				awaitRoutingExecutionWitness(t, watcher, output, "executed", 1)
			} else {
				if sp.IsRunning(info.SessionName) {
					t.Fatal("failed exec running")
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("failed exec produced witness")
				}
			}
			receipts, err := fixture.ledger.ExecutionLaunches(payload.DecisionID)
			expected := 0
			if tc.success {
				expected = 1
			}
			if err != nil || len(receipts) != expected {
				t.Fatalf("actual receipts: %+v %v", receipts, err)
			}
		})
	}
}
