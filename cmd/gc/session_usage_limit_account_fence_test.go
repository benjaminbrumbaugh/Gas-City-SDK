package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

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
		ProviderFenceEnv:      map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/a"},
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
		ProviderFenceEnv:      map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/b"},
		ResolvedProvider:      &config.ResolvedProvider{Name: "provider-b"},
	}
	bead := env.createSessionBead(name)
	patch := map[string]string(usageLimitModalWedgePatch(env.clk.Now(), env.clk.Now().Add(time.Hour)))
	patch["provider_fence_identity"] = "account:a"
	env.setSessionMetadata(&bead, patch)

	env.reconcile([]beads.Bead{bead})
	if !env.sp.IsRunning(name) {
		t.Fatal("session repointed to a healthy account remained blocked by the old session quarantine")
	}
}
