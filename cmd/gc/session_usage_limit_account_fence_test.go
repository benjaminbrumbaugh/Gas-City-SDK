package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

type failProviderFenceClearOnceStore struct {
	beads.Store
	failed bool
}

func (s *failProviderFenceClearOnceStore) Update(id string, opts beads.UpdateOpts) error {
	value, clearsQuarantine := opts.Metadata["quarantined_until"]
	if clearsQuarantine && value == "" && !s.failed {
		s.failed = true
		return errors.New("injected provider-fence quarantine clear failure")
	}
	return s.Store.Update(id, opts)
}

// This regression traverses the reconciler's provider-fence map after the
// source row has been closed. The source's session metadata is no longer an
// available input, so a session-row-only fence incorrectly disappears.
func TestReconcileSessionBeads_UsageLimitFenceSurvivesClosedSourceRow(t *testing.T) {
	env, source, sourceName := newUsageLimitModalScenario(t)
	sourceParams := env.desiredState[sourceName]
	sourceParams.ResolvedProvider = &config.ResolvedProvider{Name: "provider-a"}
	sourceParams.ProviderFenceIdentity = "account:a"
	env.desiredState[sourceName] = sourceParams

	env.reconcile([]beads.Bead{source})
	if err := env.store.Close(source.ID); err != nil {
		t.Fatal(err)
	}

	env.cfg = &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "sibling", StartCommand: "true"}},
		NamedSessions: []config.NamedSession{{Template: "sibling", Mode: "always"}},
	}
	name := config.NamedSessionRuntimeName(env.cfg.Workspace.Name, env.cfg.Workspace, "sibling")
	env.desiredState = map[string]TemplateParams{name: {
		Command:               "true",
		SessionName:           name,
		TemplateName:          "sibling",
		ProviderFenceIdentity: "account:a",
		ResolvedProvider:      &config.ResolvedProvider{Name: "provider-a"},
	}}
	sibling := env.createSessionBead(name)
	env.setSessionMetadata(&sibling, map[string]string{
		namedSessionMetadataKey:      "true",
		namedSessionIdentityMetadata: "sibling",
		namedSessionModeMetadata:     "always",
	})

	env.reconcile([]beads.Bead{sibling})
	if env.sp.IsRunning(name) {
		t.Fatal("same-account sibling started after its source session row was closed")
	}
}

func TestProviderUsageFenceIdentityDoesNotUseStandaloneLaunchCommand(t *testing.T) {
	cityPath := t.TempDir()
	account := map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/a"}
	first, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{Name: "custom", Command: "claude --profile a"}, account)
	if err != nil {
		t.Fatal(err)
	}
	second, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{Name: "custom", Command: "claude --profile b"}, account)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("launch command changed account identity: got %q, want %q", second, first)
	}
}

func TestProviderUsageFenceIdentityIgnoresOperationalEnvAndDestinationNames(t *testing.T) {
	cityPath := t.TempDir()
	first, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{Name: "alias-a", BuiltinAncestor: "claude"}, map[string]string{
		"CLAUDE_CONFIG_DIR":  "/accounts/a",
		"ANTHROPIC_API_KEY":  "same-account-secret",
		"ANTHROPIC_BASE_URL": "https://gateway-a.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{Name: "alias-b", BuiltinAncestor: "claude"}, map[string]string{
		"HOME":               "/accounts/a",
		"CUSTOM_AUTH_TOKEN":  "same-account-secret",
		"ANTHROPIC_BASE_URL": "https://gateway-b.example",
		"ANTHROPIC_MODEL":    "different-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("equivalent account aliases split identity: got %q, want %q", second, first)
	}
}

func TestProviderUsageFenceIdentityKeepsDistinctLiteralAccountsSeparate(t *testing.T) {
	cityPath := t.TempDir()
	first, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{Name: "custom", BuiltinAncestor: "custom"}, map[string]string{"CUSTOM_API_KEY": "literal-account-a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{Name: "custom", BuiltinAncestor: "custom"}, map[string]string{"CUSTOM_API_KEY": "literal-account-b"})
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("distinct literal account identities collapsed to %q", second)
	}
}

func TestProviderUsageFenceIdentityKeyDeletionFailsClosed(t *testing.T) {
	cityPath := t.TempDir()
	provider := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude"}
	if _, err := providerUsageFenceIdentityForCity(cityPath, provider, map[string]string{"ANTHROPIC_API_KEY": "account-a"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cityPath, ".gc", providerFenceIdentityKeyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := providerUsageFenceIdentityForCity(cityPath, provider, map[string]string{"ANTHROPIC_API_KEY": "account-a"}); err == nil {
		t.Fatal("deleted identity key was silently regenerated")
	}
}

func TestProviderUsageFenceIdentityKeyReplacementFailsClosed(t *testing.T) {
	cityPath := t.TempDir()
	provider := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude"}
	if _, err := providerUsageFenceIdentityForCity(cityPath, provider, map[string]string{"ANTHROPIC_API_KEY": "account-a"}); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(cityPath, ".gc", providerFenceIdentityKeyFile)
	if err := os.WriteFile(keyPath, []byte("replacement-key-material-0000000"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := providerUsageFenceIdentityForCity(cityPath, provider, map[string]string{"ANTHROPIC_API_KEY": "account-a"}); err == nil {
		t.Fatal("replaced identity key was silently accepted")
	}
}

func TestProviderUsageFenceIdentityForRuntimePrefersLaunchedAccount(t *testing.T) {
	tp := TemplateParams{ProviderFenceIdentity: "account:desired"}
	info := sessionpkg.Info{StartedProviderFenceIdentity: "account:running"}
	if got := providerUsageFenceIdentityForRuntime(info, tp, true); got != "account:running" {
		t.Fatalf("live runtime identity = %q, want launched account", got)
	}
	info = sessionpkg.Info{LaunchProviderFenceIdentity: "account:launching"}
	if got := providerUsageFenceIdentityForRuntime(info, tp, true); got != "account:launching" {
		t.Fatalf("launching runtime identity = %q, want write-ahead account", got)
	}
	if got := providerUsageFenceIdentityForRuntime(info, tp, false); got != "account:desired" {
		t.Fatalf("dead runtime identity = %q, want desired account", got)
	}
}

func TestStartedProviderFenceIdentityForCommitPreservesRunningAccount(t *testing.T) {
	tp := TemplateParams{ProviderFenceIdentity: "account:desired"}
	tests := []struct {
		name         string
		info         sessionpkg.Info
		startedFresh bool
		want         string
	}{
		{
			name:         "fresh start uses desired account",
			info:         sessionpkg.Info{StartedProviderFenceIdentity: "account:old", LaunchProviderFenceIdentity: "account:launch"},
			startedFresh: true,
			want:         "account:desired",
		},
		{
			name: "warm reuse preserves running account",
			info: sessionpkg.Info{StartedProviderFenceIdentity: "account:running"},
			want: "account:running",
		},
		{
			name: "recovery promotes write-ahead account",
			info: sessionpkg.Info{StartedProviderFenceIdentity: "account:old", LaunchProviderFenceIdentity: "account:launch"},
			want: "account:launch",
		},
		{
			name: "legacy row falls back to desired account",
			want: "account:desired",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := startedProviderFenceIdentityForCommit(test.info, tp, test.startedFresh); got != test.want {
				t.Fatalf("started identity = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReconcileSessionBeads_RepointedHealthyAccountClearsOldFence(t *testing.T) {
	env := newRestartRequestTestEnv()
	env.cfg = &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "worker", StartCommand: "true"}},
		NamedSessions: []config.NamedSession{{Template: "worker", Mode: "always"}},
	}
	name := config.NamedSessionRuntimeName(env.cfg.Workspace.Name, env.cfg.Workspace, "worker")
	env.desiredState[name] = TemplateParams{
		Command:               "true",
		SessionName:           name,
		TemplateName:          "worker",
		ProviderFenceIdentity: "account:b",
		ResolvedProvider:      &config.ResolvedProvider{Name: "provider-b"},
	}
	bead := env.createSessionBead(name)
	const instanceToken = "0123456789abcdef0123456789abcdef"
	if err := env.sp.Start(context.Background(), name, runtime.Config{Command: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := env.sp.SetMeta(name, "GC_SESSION_ID", bead.ID); err != nil {
		t.Fatal(err)
	}
	if err := env.sp.SetMeta(name, "GC_INSTANCE_TOKEN", instanceToken); err != nil {
		t.Fatal(err)
	}
	patch := map[string]string(usageLimitModalWedgePatch(env.clk.Now(), env.clk.Now().Add(time.Hour)))
	patch["provider_fence_identity"] = "account:a"
	patch["started_provider_fence_identity"] = "account:a"
	patch["restart_requested"] = "true"
	patch["instance_token"] = instanceToken
	env.setSessionMetadata(&bead, patch)

	env.reconcile([]beads.Bead{bead})
	// The old runtime must actually be replaced; clearing quarantine alone would
	// leave an account-a process running under account-b desired configuration.
	if calls := env.sp.CountCalls("StopIfDetached", name); calls != 1 {
		t.Fatalf("conditional stops = %d, want old-account runtime stopped once", calls)
	}
	if env.sp.IsRunning(name) {
		t.Fatal("old-account runtime remained running after instance-bound stop")
	}
	got, err := env.store.Get(bead.ID)
	if err != nil {
		t.Fatal(err)
	}
	env.reconcile([]beads.Bead{got})
	if !env.sp.IsRunning(name) {
		t.Fatal("session repointed to a healthy account was not restarted on the next pass")
	}
	got, err = env.store.Get(bead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if identity := got.Metadata["started_provider_fence_identity"]; identity != "account:b" {
		t.Fatalf("repointed runtime attribution = %q, want account:b; calls=%#v metadata=%#v", identity, env.sp.Calls, got.Metadata)
	}
}

func TestReconcileSessionBeads_RepointRetriesQuarantineClearAfterSuccessfulStop(t *testing.T) {
	env := newRestartRequestTestEnv()
	env.cfg = &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "worker", StartCommand: "true"}},
		NamedSessions: []config.NamedSession{{Template: "worker", Mode: "always"}},
	}
	name := config.NamedSessionRuntimeName(env.cfg.Workspace.Name, env.cfg.Workspace, "worker")
	env.desiredState[name] = TemplateParams{
		Command:               "true",
		SessionName:           name,
		TemplateName:          "worker",
		ProviderFenceIdentity: "account:b",
		ResolvedProvider:      &config.ResolvedProvider{Name: "provider-b"},
	}
	bead := env.createSessionBead(name)
	const instanceToken = "0123456789abcdef0123456789abcdef"
	if err := env.sp.Start(context.Background(), name, runtime.Config{Command: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := env.sp.SetMeta(name, "GC_SESSION_ID", bead.ID); err != nil {
		t.Fatal(err)
	}
	if err := env.sp.SetMeta(name, "GC_INSTANCE_TOKEN", instanceToken); err != nil {
		t.Fatal(err)
	}
	patch := map[string]string(usageLimitModalWedgePatch(env.clk.Now(), env.clk.Now().Add(time.Hour)))
	patch["provider_fence_identity"] = "account:a"
	patch["started_provider_fence_identity"] = "account:a"
	patch["restart_requested"] = "true"
	patch["instance_token"] = instanceToken
	env.setSessionMetadata(&bead, patch)

	failing := &failProviderFenceClearOnceStore{Store: env.store}
	env.store = failing
	env.reconcile([]beads.Bead{bead})
	if calls := env.sp.CountCalls("StopIfDetached", name); calls != 1 {
		t.Fatalf("conditional stops = %d, want one successful instance-bound stop", calls)
	}
	if env.sp.IsRunning(name) {
		t.Fatal("old-account runtime remained running after conditional stop")
	}
	stopped, err := env.store.Get(bead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Metadata["quarantined_until"] == "" || stopped.Metadata["restart_requested"] != "true" {
		t.Fatalf("failed clear did not preserve retryable state: %#v", stopped.Metadata)
	}

	env.reconcile([]beads.Bead{stopped})
	if calls := env.sp.CountCalls("StopIfDetached", name); calls != 1 {
		t.Fatalf("retry issued another conditional stop after runtime was already absent: %d", calls)
	}
	if !env.sp.IsRunning(name) {
		t.Fatal("repointed session did not restart after quarantine-clear retry")
	}
	restarted, err := env.store.Get(bead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Metadata["quarantined_until"] != "" || restarted.Metadata["restart_requested"] != "" {
		t.Fatalf("retry did not consume provider-fence handoff: %#v", restarted.Metadata)
	}
	if identity := restarted.Metadata["started_provider_fence_identity"]; identity != "account:b" {
		t.Fatalf("retried repoint attribution = %q, want account:b", identity)
	}
}
