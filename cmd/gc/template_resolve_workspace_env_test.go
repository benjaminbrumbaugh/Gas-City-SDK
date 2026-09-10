package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/execenv"
	"github.com/gastownhall/gascity/internal/fsys"
)

func TestResolveTemplateMergesWorkspaceEnv(t *testing.T) {
	cityPath := t.TempDir()
	writeTemplateResolveCityConfig(t, cityPath, "file")

	params := &agentBuildParams{
		cityName: "city",
		cityPath: cityPath,
		workspace: &config.Workspace{
			Provider: "test",
			Env: map[string]string{
				"GC_TARGET_BRANCH": "boylec/develop",
				"FROM_WORKSPACE":   "ws",
			},
		},
		providers:  map[string]config.ProviderSpec{"test": {Command: "echo", PromptMode: "none"}},
		lookPath:   func(string) (string, error) { return "/bin/echo", nil },
		fs:         fsys.OSFS{},
		beaconTime: time.Unix(0, 0),
		beadNames:  make(map[string]string),
		stderr:     io.Discard,
	}
	agent := &config.Agent{Name: "mayor"}

	tp, err := resolveTemplate(params, agent, agent.QualifiedName(), nil)
	if err != nil {
		t.Fatalf("resolveTemplate: %v", err)
	}
	if got := tp.Env["GC_TARGET_BRANCH"]; got != "boylec/develop" {
		t.Errorf("GC_TARGET_BRANCH = %q, want %q", got, "boylec/develop")
	}
	if got := tp.Env["FROM_WORKSPACE"]; got != "ws" {
		t.Errorf("FROM_WORKSPACE = %q, want %q", got, "ws")
	}
}

func TestResolveTemplateAgentEnvWinsOverWorkspaceEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ambient-account")
	cityPath := t.TempDir()
	writeTemplateResolveCityConfig(t, cityPath, "file")

	params := &agentBuildParams{
		cityName: "city",
		cityPath: cityPath,
		workspace: &config.Workspace{
			Provider: "test",
			Env: map[string]string{
				"GC_TARGET_BRANCH":  "boylec/develop",
				"FROM_WORKSPACE":    "shared",
				"ANTHROPIC_API_KEY": "workspace-account",
			},
		},
		providers:  map[string]config.ProviderSpec{"test": {Command: "echo", PromptMode: "none"}},
		lookPath:   func(string) (string, error) { return "/bin/echo", nil },
		fs:         fsys.OSFS{},
		beaconTime: time.Unix(0, 0),
		beadNames:  make(map[string]string),
		stderr:     io.Discard,
	}
	agent := &config.Agent{
		Name: "mayor",
		Env: map[string]string{
			"GC_TARGET_BRANCH":  "boylec/special",
			"CLAUDE_CONFIG_DIR": "/accounts/mayor",
		},
	}

	tp, err := resolveTemplate(params, agent, agent.QualifiedName(), nil)
	if err != nil {
		t.Fatalf("resolveTemplate: %v", err)
	}
	if got := tp.Env["GC_TARGET_BRANCH"]; got != "boylec/special" {
		t.Errorf("GC_TARGET_BRANCH = %q, want %q (agent env must override workspace env)", got, "boylec/special")
	}
	if !strings.HasPrefix(tp.ProviderFenceIdentity, "account:hmac-sha256:") {
		t.Errorf("ProviderFenceIdentity = %q, want opaque keyed identity", tp.ProviderFenceIdentity)
	}
	if _, retained := reflect.TypeOf(tp).FieldByName("ProviderFenceEnv"); retained {
		t.Fatal("TemplateParams retains plaintext provider-account material after identity derivation")
	}
}

func TestResolveTemplateDisablesProductMetricsForManagedAgent(t *testing.T) {
	cityPath := t.TempDir()
	writeTemplateResolveCityConfig(t, cityPath, "file")

	params := &agentBuildParams{
		cityName:   "city",
		cityPath:   cityPath,
		workspace:  &config.Workspace{Provider: "test"},
		providers:  map[string]config.ProviderSpec{"test": {Command: "echo", PromptMode: "none"}},
		lookPath:   func(string) (string, error) { return "/bin/echo", nil },
		fs:         fsys.OSFS{},
		beaconTime: time.Unix(0, 0),
		beadNames:  make(map[string]string),
		stderr:     io.Discard,
	}
	agent := &config.Agent{Name: "worker", Env: map[string]string{
		execenv.UsageMetricsDisableEnv: "0",
		"BD_DISABLE_METRICS":           "leave-beads-alone",
	}}

	tp, err := resolveTemplate(params, agent, agent.QualifiedName(), nil)
	if err != nil {
		t.Fatalf("resolveTemplate: %v", err)
	}
	if got := tp.Env[execenv.UsageMetricsDisableEnv]; got != execenv.UsageMetricsDisableValue {
		t.Fatalf("%s = %q, want %q", execenv.UsageMetricsDisableEnv, got, execenv.UsageMetricsDisableValue)
	}
	if got := tp.Env["BD_DISABLE_METRICS"]; got != "leave-beads-alone" {
		t.Fatalf("BD_DISABLE_METRICS = %q, want unchanged", got)
	}
}
