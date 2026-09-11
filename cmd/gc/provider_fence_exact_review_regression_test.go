package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

func TestWorkerHandleForSessionWithConfigRecomputesProviderFenceIdentityOnResume(t *testing.T) {
	cityDir := t.TempDir()
	t.Setenv("CURRENT_STUB_ACCOUNT", "current-account-secret")
	writePhase0InterfaceCity(t, cityDir, `[workspace]
name = "test-city"

[beads]
provider = "file"

[[agent]]
name = "worker"
provider = "stub"

[providers.stub]
command = "/bin/echo"
resume_flag = "--resume"
resume_style = "flag"
session_id_flag = "--session-id"

[providers.stub.env]
STUB_API_KEY = "$CURRENT_STUB_ACCOUNT"
`)
	cfg, err := loadCityConfig(cityDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openCityStoreAt(cityDir)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.ResolveProvider(&config.Agent{Provider: "stub"}, &cfg.Workspace, cfg.Providers, func(name string) (string, error) { return name, nil })
	if err != nil {
		t.Fatal(err)
	}
	stale, err := providerUsageFenceIdentityForCityWithStore(cityDir, store, resolved, map[string]string{"STUB_API_KEY": "previous-account-secret"})
	if err != nil {
		t.Fatal(err)
	}
	sp := runtime.NewFake()
	mgr := newSessionManagerWithConfig(cityDir, store, sp, cfg)
	info, err := mgr.CreateSession(context.Background(), sessionpkg.CreateOptions{
		BeadOnly: true,
		Template: "worker",
		Title:    "Resume identity",
		Provider: "stub",
		WorkDir:  cityDir,
		ExtraMeta: map[string]string{
			"session_key":                     "resume-session-key",
			"started_config_hash":             "prior-launch",
			"started_provider_fence_identity": stale,
			"state":                           "suspended",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handle, err := workerHandleForSessionWithConfig(cityDir, store, sp, cfg, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	start := sp.LastStartConfig(info.SessionName)
	if start == nil {
		t.Fatal("resume did not launch runtime")
	}
	want, err := providerUsageFenceIdentityForCityWithStore(cityDir, store, resolved, providerFenceAccountEnv(start.Env))
	if err != nil {
		t.Fatal(err)
	}
	if got := start.ProviderFenceIdentity; got != want {
		t.Fatalf("launched provider fence identity = %q, want current effective-env identity %q", got, want)
	}
	row, err := store.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := row.Metadata["started_provider_fence_identity"]; got != want {
		t.Fatalf("persisted started identity = %q, want current launch identity %q", got, want)
	}
	for key, value := range row.Metadata {
		if strings.Contains(value, "current-account-secret") {
			t.Fatalf("secret credential persisted in metadata %s", key)
		}
	}
}

func TestStartedProviderFenceIdentityForCommitUsesActualRuntimeWinner(t *testing.T) {
	tp := TemplateParams{ProviderFenceIdentity: "account:losing-request"}
	for _, test := range []struct {
		name string
		info sessionpkg.Info
		want string
	}{
		{
			name: "winner still staged",
			info: sessionpkg.Info{StartedProviderFenceIdentity: "account:old", LaunchProviderFenceIdentity: "account:winner"},
			want: "account:winner",
		},
		{
			name: "winner already committed",
			info: sessionpkg.Info{StartedProviderFenceIdentity: "account:winner"},
			want: "account:winner",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := startedProviderFenceIdentityForCommit(test.info, tp, true); got != test.want {
				t.Fatalf("fresh losing caller committed identity %q, want runtime winner %q", got, test.want)
			}
		})
	}
}

func TestStartPreparedCandidateRefusesMatchingUnattributedLiveRuntime(t *testing.T) {
	const (
		name          = "test-city--worker"
		handle        = "gc-session"
		instanceToken = "0123456789abcdef0123456789abcdef"
	)
	sp := runtime.NewFake()
	if err := sp.Start(context.Background(), name, runtime.Config{Command: "foreign-runtime"}); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetMeta(name, "GC_SESSION_ID", handle); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetMeta(name, "GC_INSTANCE_TOKEN", instanceToken); err != nil {
		t.Fatal(err)
	}

	startedFresh, err := startPreparedStartCandidate(
		context.Background(),
		preparedStart{
			candidate: startCandidate{
				info: sessionpkg.Info{
					ID:                  handle,
					SessionNameMetadata: name,
					MetadataState:       string(sessionpkg.StateStartPending),
					InstanceToken:       instanceToken,
				},
				tp: TemplateParams{ProviderFenceIdentity: "account:caller"},
			},
			cfg: runtime.Config{},
		},
		"", nil, sp, nil, nil, nil, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "live runtime has no authoritative provider account attribution") {
		t.Fatalf("start error = %v, want unattributed-live-runtime refusal", err)
	}
	if startedFresh {
		t.Fatal("unattributed live runtime was reported as freshly started")
	}
	if !sp.IsRunning(name) {
		t.Fatal("unattributed live runtime was stopped")
	}
}

type concurrentStateMutationStopProvider struct {
	runtime.Provider
	store beads.Store
	id    string
}

type concurrentSuccessfulStopMutationProvider struct {
	runtime.Provider
	store beads.Store
	id    string
}

func (p concurrentSuccessfulStopMutationProvider) StopIfDetached(name, token string) error {
	if err := p.store.SetMetadata(p.id, "started_config_hash", "concurrent-after-handoff"); err != nil {
		return err
	}
	return p.Provider.(runtime.ConditionalStopProvider).StopIfDetached(name, token)
}

type concurrentPreStopHandoffMutationStore struct {
	*beads.MemStore
	id       string
	key      string
	value    string
	injected bool
}

type concurrentPreStopSnapshotMutationStore struct {
	*beads.MemStore
	id       string
	injected bool
}

func (s *concurrentPreStopSnapshotMutationStore) Get(id string) (beads.Bead, error) {
	row, err := s.MemStore.Get(id)
	if err != nil || id != s.id || s.injected {
		return row, err
	}
	if row.Metadata[sessionHealthReasonMetadataKey] != sessionHealthReasonUsageLimitModal ||
		row.Metadata["restart_requested"] != "true" ||
		row.Metadata["continuation_reset_pending"] == "true" {
		return row, nil
	}
	s.injected = true
	_ = s.SetMetadata(id, "restart_requested", "")
	return s.MemStore.Get(id)
}

func (s *concurrentPreStopHandoffMutationStore) injectBeforeHandoff(opts beads.UpdateOpts) {
	if s.injected || opts.Metadata["continuation_reset_pending"] != "true" {
		return
	}
	s.injected = true
	_ = s.SetMetadata(s.id, s.key, s.value)
}

func (s *concurrentPreStopHandoffMutationStore) Update(id string, opts beads.UpdateOpts) error {
	s.injectBeforeHandoff(opts)
	return s.MemStore.Update(id, opts)
}

func (s *concurrentPreStopHandoffMutationStore) UpdateIfMatch(id string, expectedRevision int64, opts beads.UpdateOpts) error {
	s.injectBeforeHandoff(opts)
	return s.MemStore.UpdateIfMatch(id, expectedRevision, opts)
}

func (p concurrentStateMutationStopProvider) StopIfDetached(string, string) error {
	if err := p.store.SetMetadata(p.id, "started_config_hash", "concurrent-newer-state"); err != nil {
		return err
	}
	return runtime.ErrConditionalStopRefused
}

func TestUsageLimitPreStopHandoffDoesNotRearmConcurrentRestartCancellation(t *testing.T) {
	env, source, sessionName := newUsageLimitModalScenario(t)
	mem := env.store.(*beads.MemStore)
	tracingStore := &concurrentPreStopSnapshotMutationStore{MemStore: mem, id: source.ID}
	env.store = tracingStore

	env.reconcile([]beads.Bead{source})

	if !tracingStore.injected {
		t.Fatal("test did not clear restart_requested after the tick snapshot")
	}
	if !env.sp.IsRunning(sessionName) {
		t.Fatal("runtime was stopped after a concurrent writer canceled restart")
	}
	got, err := env.store.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value := got.Metadata["restart_requested"]; value != "" {
		t.Fatalf("restart_requested = %q, want concurrent cancellation preserved", value)
	}
}

func TestUsageLimitPreStopHandoffRejectsConcurrentDestructiveMetadataWrite(t *testing.T) {
	env, source, sessionName := newUsageLimitModalScenario(t)
	mem, ok := env.store.(*beads.MemStore)
	if !ok {
		t.Fatalf("scenario store = %T, want *beads.MemStore", env.store)
	}
	const concurrentValue = "concurrent-newer-state"
	tracingStore := &concurrentPreStopHandoffMutationStore{
		MemStore: mem,
		id:       source.ID,
		key:      "started_config_hash",
		value:    concurrentValue,
	}
	env.store = tracingStore

	env.reconcile([]beads.Bead{source})

	if !tracingStore.injected {
		t.Fatal("test did not inject the concurrent write at the pre-stop handoff")
	}
	if !env.sp.IsRunning(sessionName) {
		t.Fatal("runtime was stopped after the pre-stop handoff lost its metadata precondition")
	}
	if calls := env.sp.CountCalls("StopIfDetached", sessionName); calls != 0 {
		t.Fatalf("StopIfDetached calls = %d, want 0 after handoff precondition loss", calls)
	}
	got, err := env.store.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value := got.Metadata["started_config_hash"]; value != concurrentValue {
		t.Fatalf("started_config_hash = %q, want concurrent value %q preserved", value, concurrentValue)
	}
}

func TestUsageLimitSuccessfulStopDoesNotRewriteConcurrentPostHandoffState(t *testing.T) {
	env, source, sessionName := newUsageLimitModalScenario(t)
	env.provider = concurrentSuccessfulStopMutationProvider{Provider: env.sp, store: env.store, id: source.ID}

	env.reconcile([]beads.Bead{source})

	if env.sp.IsRunning(sessionName) {
		t.Fatal("runtime remained running after the guarded stop succeeded")
	}
	got, err := env.store.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value := got.Metadata["started_config_hash"]; value != "concurrent-after-handoff" {
		t.Fatalf("started_config_hash = %q, want concurrent post-handoff value preserved", value)
	}
}

func TestUsageLimitRollbackPreservesConcurrentNewerSessionState(t *testing.T) {
	env, source, sessionName := newUsageLimitModalScenario(t)
	env.provider = concurrentStateMutationStopProvider{Provider: env.sp, store: env.store, id: source.ID}

	env.reconcile([]beads.Bead{source})

	if !env.sp.IsRunning(sessionName) {
		t.Fatal("refused conditional stop destroyed the runtime")
	}
	got, err := env.store.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata["started_config_hash"] != "concurrent-newer-state" {
		t.Fatalf("conditional-stop rollback overwrote concurrent state with %q", got.Metadata["started_config_hash"])
	}
}

func TestProviderFenceIdentityRejectsCoherentKeyAndDigestReplacementWithHistory(t *testing.T) {
	cityPath := t.TempDir()
	mem := beads.NewMemStore()
	resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude"}
	account := map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}
	identity, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Create(beads.Bead{
		Type: sessionpkg.BeadType, Labels: []string{sessionpkg.LabelSession},
		Metadata: map[string]string{"started_provider_fence_identity": identity},
	}); err != nil {
		t.Fatal(err)
	}

	replacement := []byte("0123456789abcdef0123456789abcdef")
	replacementDigest := sha256.Sum256(replacement)
	keyPath := filepath.Join(cityPath, ".gc", providerFenceIdentityKeyFile)
	digestPath := filepath.Join(cityPath, ".gc", providerFenceIdentityKeyDigestFile)
	if err := os.WriteFile(keyPath, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(digestPath, []byte(hex.EncodeToString(replacementDigest[:])), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, account); err == nil || !strings.Contains(err.Error(), "durable key digest") {
		t.Fatalf("coherent key+digest replacement error = %v, want durable continuity refusal", err)
	}
	for _, beadType := range []string{sessionpkg.BeadType, sessionpkg.WaitBeadType} {
		rows, err := mem.List(beads.ListQuery{Type: beadType, IncludeClosed: true, Live: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			for key, value := range row.Metadata {
				if value == string(replacement) {
					t.Fatalf("secret key material persisted in bead %s metadata %s", row.ID, key)
				}
			}
		}
	}
}
