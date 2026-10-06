package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

func TestCityRuntimeRecoveryResponderUsesOrdinaryWorkRoute(t *testing.T) {
	infrastructure := recoveryControllerStore()
	work := beads.NewMemStore()
	work.HonorExplicitIDs = true
	cr := &CityRuntime{
		cfg: &config.City{RecoveryResponder: &config.RecoveryResponderConfig{
			Targets: []string{"rig/responder"}, Hold: "10m", Cooldown: "1m", MaxAttempts: 1,
		}},
		standaloneCityStore: work,
		storageRoutes:       splitClassRoutes(infrastructure),
		stderr:              io.Discard,
	}

	fields := cr.reconcileRecoveryResponder(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if fields["created"] != 1 {
		t.Fatalf("fields = %#v", fields)
	}
	works, err := work.List(beads.ListQuery{Label: worker.RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || works[0].Metadata[beadmeta.RoutedToMetadataKey] != "rig/responder" || works[0].Type != "task" {
		t.Fatalf("ordinary recovery work = %+v", works)
	}
	wrongStore, err := infrastructure.List(beads.ListQuery{Label: worker.RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(wrongStore) != 0 {
		t.Fatalf("ordinary work leaked into infrastructure store: %+v", wrongStore)
	}
	info, err := session.NewStore(beads.SessionStore{Store: infrastructure}).Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryIncidentID == "" || info.RecoveryWorkID != works[0].ID {
		t.Fatalf("session state was not persisted in session store: %+v", info)
	}
}

func TestCityRuntimeRecoveryResponderNoConfigIsNoOp(t *testing.T) {
	store := beads.NewMemStore()
	cr := &CityRuntime{cfg: &config.City{}, standaloneCityStore: store, stderr: io.Discard}
	if fields := cr.reconcileRecoveryResponder(context.Background(), time.Now()); fields != nil {
		t.Fatalf("fields = %#v, want nil", fields)
	}
	works, err := store.List(beads.ListQuery{AllowScan: true, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 0 {
		t.Fatalf("disabled recovery created work: %+v", works)
	}
}

func TestInstalledRecoveryAccountRefsRejectsExecutableAliases(t *testing.T) {
	cfg := &config.City{Providers: map[string]config.ProviderSpec{
		"claude-personal": {Command: "true"},
		"opencode-go":     {Command: "true", PathCheck: "true"},
		"missing-account": {Command: "gc-ibou-command-that-is-not-installed"},
		"no-command":      {},
	}}
	if got := installedRecoveryAccountRefs(cfg); len(got) != 0 {
		t.Fatalf("arbitrary executable aliases admitted as accounts: %v", got)
	}
}

func TestInstalledRecoveryAccountRefsRejectsWorkspaceCommandOverride(t *testing.T) {
	cfg := &config.City{Workspace: config.Workspace{StartCommand: "not-installed"}, Providers: map[string]config.ProviderSpec{
		"true": {Command: "true"},
	}}
	if got := installedRecoveryAccountRefs(cfg); len(got) != 0 {
		t.Fatalf("workspace command override admitted as installed identity: %v", got)
	}
}

func TestInstalledRecoveryAccountRefsRejectsPathCheckOnlyInstallation(t *testing.T) {
	cfg := &config.City{Providers: map[string]config.ProviderSpec{
		"gc-ibou-not-installed": {Command: "gc-ibou-not-installed", PathCheck: "true"},
	}}
	if got := installedRecoveryAccountRefs(cfg); len(got) != 0 {
		t.Fatalf("path_check admitted a missing account executable: %v", got)
	}
}

func TestCityRuntimeRecoveryResponderMissingAccountsHolds(t *testing.T) {
	store := recoveryControllerStore()
	var stderr bytes.Buffer
	cr := &CityRuntime{
		cfg: &config.City{RecoveryResponder: &config.RecoveryResponderConfig{
			Targets: []string{"rig/responder"}, MaxAttempts: 1,
			WayfinderURL: "http://127.0.0.1:1",
		}},
		standaloneCityStore: store,
		stderr:              &stderr,
	}
	fields := cr.reconcileRecoveryResponder(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if fields["created"] != 0 || !strings.Contains(stderr.String(), "same-named installed executable") {
		t.Fatalf("unavailable-advisor fields = %#v, stderr = %q", fields, stderr.String())
	}
	works, err := store.List(beads.ListQuery{Label: worker.RecoveryWorkLabel, IncludeClosed: true})
	if err != nil || len(works) != 0 {
		t.Fatalf("unavailable-advisor work = %+v, error = %v", works, err)
	}
}

func TestCityRuntimeRecoveryResponderLoadsContainedWayfinderTemplate(t *testing.T) {
	cityPath := t.TempDir()
	templatePath := filepath.Join(cityPath, ".gc", "wayfinder-recovery.json")
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templatePath, recoveryControllerWayfinderTemplate(t, "rig/responder"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := recoveryControllerStore()
	var stderr bytes.Buffer
	cr := &CityRuntime{
		cityPath: cityPath,
		cfg: &config.City{Providers: recoveryControllerProviders(t), RecoveryResponder: &config.RecoveryResponderConfig{
			// The contained template is valid but has no candidate for the only
			// configured target. This proves the file-backed advisor holds instead
			// of guessing from configured order.
			Targets: []string{"rig/fallback"}, WayfinderURL: "http://127.0.0.1:1",
			WayfinderRequestFile: ".gc/wayfinder-recovery.json", MaxAttempts: 1,
		}},
		standaloneCityStore: store, stderr: &stderr,
	}
	fields := cr.reconcileRecoveryResponder(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if fields["created"] != 0 || stderr.Len() != 0 {
		t.Fatalf("fields=%#v stderr=%q", fields, stderr.String())
	}
	works, err := store.List(beads.ListQuery{Label: worker.RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 0 {
		t.Fatalf("malformed advisory guessed a target: %+v", works)
	}
}

func TestReadRecoveryWayfinderTemplateRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maximumRecoveryWayfinderTemplateBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecoveryWayfinderTemplate(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized template error = %v", err)
	}
}

func TestCityRuntimeRecoveryResponderRejectsTemplateTraversalAndHolds(t *testing.T) {
	cityPath := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cityPath, outside)
	if err != nil {
		t.Fatal(err)
	}
	store := recoveryControllerStore()
	var stderr bytes.Buffer
	cr := &CityRuntime{
		cityPath: cityPath,
		cfg: &config.City{Providers: recoveryControllerProviders(t), RecoveryResponder: &config.RecoveryResponderConfig{
			Targets: []string{"rig/responder"}, WayfinderURL: "http://127.0.0.1:1",
			WayfinderRequestFile: rel, MaxAttempts: 1,
		}},
		standaloneCityStore: store, stderr: &stderr,
	}
	fields := cr.reconcileRecoveryResponder(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	if fields["created"] != 0 {
		t.Fatalf("unavailable-advisor fields = %#v", fields)
	}
	if !strings.Contains(stderr.String(), "outside city root") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	works, err := store.List(beads.ListQuery{Label: worker.RecoveryWorkLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 0 {
		t.Fatalf("unavailable-advisor guessed a target: %+v", works)
	}
}

func TestRecoveryWayfinderRequestPathRejectsSymlinkEscape(t *testing.T) {
	cityPath := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cityPath, "request.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := recoveryWayfinderRequestPath(cityPath, "request.json"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func recoveryControllerStore() *beads.MemStore {
	store := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "session-1", Type: session.BeadType, Status: "open", Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"state": "asleep", "session_health": "unhealthy", "session_health_reason": "quota_exceeded",
			"session_drainable": "true", "provider_terminal_error": "quota_exceeded", "provider": "provider-a",
		},
	}}, nil)
	store.HonorExplicitIDs = true
	return store
}

func recoveryControllerProviders(t *testing.T) map[string]config.ProviderSpec {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "account executables with spaces")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "claude-personal")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return map[string]config.ProviderSpec{"claude-personal": {Command: command}}
}

func TestInstalledRecoveryAccountRefsAcceptsSameNamedExecutables(t *testing.T) {
	providers := recoveryControllerProviders(t)
	for _, name := range []string{"codex", "claude-gladstone"} {
		command := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		providers[name] = config.ProviderSpec{Command: command}
	}
	if got := strings.Join(installedRecoveryAccountRefs(&config.City{Providers: providers}), ","); got != "claude-gladstone,claude-personal,codex" {
		t.Fatalf("installed account refs = %q", got)
	}
}

func TestInstalledRecoveryAccountRefsLiteralPathAdmissionBoundary(t *testing.T) {
	providers := recoveryControllerProviders(t)
	installed := providers["claude-personal"].Command
	dir := filepath.Dir(installed)
	nonExecutable := filepath.Join(dir, "non-executable-account")
	if err := os.WriteFile(nonExecutable, []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	frontend := filepath.Join(dir, "generic-frontend")
	if err := os.WriteFile(frontend, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := "frontend-alias"
	for _, tt := range []struct {
		name      string
		providers map[string]config.ProviderSpec
		workspace config.Workspace
		want      string
	}{
		{"same-named literal", providers, config.Workspace{}, "claude-personal"},
		{"absolute executable alias", map[string]config.ProviderSpec{"alias": {Command: installed}}, config.Workspace{}, ""},
		{"inherited generic frontend", map[string]config.ProviderSpec{
			"frontend-alias": {Command: frontend}, "claude-personal": {Base: &base},
		}, config.Workspace{}, ""},
		{"workspace generic override", providers, config.Workspace{StartCommand: "true"}, ""},
		{"path check lies about missing literal", map[string]config.ProviderSpec{
			"missing-account": {Command: filepath.Join(dir, "missing-account"), PathCheck: installed},
		}, config.Workspace{}, ""},
		{"path check lies about nonexecutable literal", map[string]config.ProviderSpec{
			"non-executable-account": {Command: nonExecutable, PathCheck: installed},
		}, config.Workspace{}, ""},
		{"missing literal", map[string]config.ProviderSpec{
			"missing-account": {Command: filepath.Join(dir, "missing-account")},
		}, config.Workspace{}, ""},
		{"nonexecutable literal", map[string]config.ProviderSpec{
			"non-executable-account": {Command: nonExecutable},
		}, config.Workspace{}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(installedRecoveryAccountRefs(&config.City{Providers: tt.providers, Workspace: tt.workspace}), ",")
			if got != tt.want {
				t.Fatalf("admitted accounts = %q, want %q", got, tt.want)
			}
		})
	}
}

func recoveryControllerWayfinderTemplate(t *testing.T, target string) []byte {
	t.Helper()
	template, err := os.ReadFile("testdata/recovery_wayfinder_request.json")
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(template, []byte("anthropic-personal-max-opus-5-high"), []byte(target))
}
