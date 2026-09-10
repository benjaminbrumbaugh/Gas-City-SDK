package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

func TestProviderFenceIdentityKeyDirectoryLossFailsClosedWithActiveFence(t *testing.T) {
	cityPath := t.TempDir()
	backing := beads.NewMemStore()
	cache := beads.NewCachingStore(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatal(err)
	}
	resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude"}
	account := map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}
	identity, err := providerUsageFenceIdentityForCityWithStore(cityPath, cache, resolved, account)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// Bypass the primed cache to model a durable external observation whose
	// event has not reached the controller cache yet.
	front := sessionpkg.NewStore(beads.SessionStore{Store: backing})
	if err := front.RecordProviderFence(identity, now.Add(time.Hour), now, "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cityPath, ".gc")); err != nil {
		t.Fatal(err)
	}

	if _, err := providerUsageFenceIdentityForCityWithStore(cityPath, cache, resolved, account); err == nil || !strings.Contains(err.Error(), "durable keyed fence or session attribution metadata remains") {
		t.Fatalf("identity derivation after key-directory loss error = %v, want durable-history continuity failure", err)
	}
	if _, err := os.Stat(filepath.Join(cityPath, ".gc", providerFenceIdentityKeyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity key was regenerated despite active fence: %v", err)
	}
}

func TestNewWorkerStartedSessionPromotesProviderFenceIdentity(t *testing.T) {
	cityPath := t.TempDir()
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	resolved := &config.ResolvedProvider{
		Name:            "claude",
		BuiltinAncestor: "claude",
		Command:         "true",
		Env:             map[string]string{"ANTHROPIC_API_KEY": "test-account-material"},
	}
	handle, err := newWorkerSessionHandleForResolvedRuntimeWithConfig(
		cityPath,
		mem,
		sp,
		nil,
		"worker",
		"worker-started",
		"worker",
		"Worker",
		"true",
		"claude",
		cityPath,
		"",
		resolved,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, ok := handle.(worker.LifecycleHandle)
	if !ok {
		t.Fatalf("handle %T does not expose lifecycle", handle)
	}
	info, err := lifecycle.Create(context.Background(), worker.CreateModeStarted)
	if err != nil {
		t.Fatal(err)
	}
	row, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	identity := strings.TrimSpace(row.Metadata["started_provider_fence_identity"])
	if !strings.HasPrefix(identity, "account:hmac-sha256:") {
		t.Fatalf("started_provider_fence_identity = %q, want opaque keyed account identity", identity)
	}
	if launch := strings.TrimSpace(row.Metadata["launch_provider_fence_identity"]); launch != "" {
		t.Fatalf("successful direct start retained launch identity %q", launch)
	}
}

func TestNewWorkerFailedDirectStartClearsLaunchProviderFenceIdentity(t *testing.T) {
	cityPath := t.TempDir()
	mem := beads.NewMemStore()
	resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude", Command: "false", Env: map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}}
	handle, err := newWorkerSessionHandleForResolvedRuntimeWithConfig(cityPath, mem, runtime.NewFailFake(), nil, "worker", "worker-failed", "worker", "Worker", "false", "claude", cityPath, "", resolved, nil)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := handle.(worker.LifecycleHandle)
	if _, err := lifecycle.Create(context.Background(), worker.CreateModeStarted); err == nil {
		t.Fatal("failed provider start returned nil")
	}
	rows, err := mem.List(beads.ListQuery{Type: sessionpkg.BeadType, IncludeClosed: true})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rolled-back session rows = %d, %v; want 1", len(rows), err)
	}
	if got := strings.TrimSpace(rows[0].Metadata["launch_provider_fence_identity"]); got != "" {
		t.Fatalf("failed direct start retained launch identity %q", got)
	}
}

func TestNewWorkerDirectStartHonorsDurableProviderFence(t *testing.T) {
	cityPath := t.TempDir()
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude", Command: "true", Env: map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}}
	sessionCfg, err := resolvedWorkerSessionConfigWithConfig(cityPath, "true", "claude", cityPath, "worker", "worker-fenced", "worker", "Worker", "", resolved, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, providerFenceAccountEnv(sessionCfg.Runtime.SessionEnv))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := sessionpkg.NewStore(beads.SessionStore{Store: mem}).RecordProviderFence(identity, now.Add(time.Hour), now, "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}
	handle, err := newWorkerSessionHandleForResolvedRuntimeWithConfig(cityPath, mem, sp, nil, "worker", "worker-fenced", "worker", "Worker", "true", "claude", cityPath, "", resolved, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.(worker.LifecycleHandle).Create(context.Background(), worker.CreateModeStarted); err == nil || !strings.Contains(err.Error(), "fenced until") {
		t.Fatalf("direct start under active account fence error = %v, want refusal", err)
	}
	if sp.IsRunning("worker-fenced") {
		t.Fatal("direct start bypassed durable provider account fence")
	}
}

func TestExistingWorkerStartHonorsDurableProviderFence(t *testing.T) {
	cityPath := t.TempDir()
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude", Command: "true", Env: map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}}
	handle, err := newWorkerSessionHandleForResolvedRuntimeWithConfig(cityPath, mem, sp, nil, "worker", "worker-existing-fenced", "worker", "Worker", "true", "claude", cityPath, "", resolved, nil)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := handle.(worker.LifecycleHandle)
	info, err := lifecycle.Create(context.Background(), worker.CreateModeDeferred)
	if err != nil {
		t.Fatal(err)
	}
	row, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	identity := strings.TrimSpace(row.Metadata["launch_provider_fence_identity"])
	if !strings.HasPrefix(identity, "account:hmac-sha256:") {
		t.Fatalf("deferred launch identity = %q, want opaque keyed account identity", identity)
	}
	now := time.Now().UTC()
	if err := sessionpkg.NewStore(beads.SessionStore{Store: mem}).RecordProviderFence(identity, now.Add(time.Hour), now, "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "fenced until") {
		t.Fatalf("existing start under active account fence error = %v, want refusal", err)
	}
	if sp.IsRunning("worker-existing-fenced") {
		t.Fatal("existing worker start bypassed durable provider account fence")
	}
}

func TestExistingWorkerStartPromotesProviderFenceIdentity(t *testing.T) {
	cityPath := t.TempDir()
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude", Command: "true", Env: map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}}
	handle, err := newWorkerSessionHandleForResolvedRuntimeWithConfig(cityPath, mem, sp, nil, "worker", "worker-existing-start", "worker", "Worker", "true", "claude", cityPath, "", resolved, nil)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := handle.(worker.LifecycleHandle)
	info, err := lifecycle.Create(context.Background(), worker.CreateModeDeferred)
	if err != nil {
		t.Fatal(err)
	}
	before, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	staged := strings.TrimSpace(before.Metadata["launch_provider_fence_identity"])
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(after.Metadata["started_provider_fence_identity"]); got == "" || got != staged {
		t.Fatalf("started provider identity = %q, want staged %q", got, staged)
	}
	if got := strings.TrimSpace(after.Metadata["launch_provider_fence_identity"]); got != "" {
		t.Fatalf("successful existing start retained launch identity %q", got)
	}
}

func TestProviderFenceIdentityMissingDigestRefusesAttestationWithHistory(t *testing.T) {
	cityPath := t.TempDir()
	mem := beads.NewMemStore()
	resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude"}
	account := map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}
	identity, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Create(beads.Bead{Type: sessionpkg.BeadType, Labels: []string{sessionpkg.LabelSession}, Metadata: map[string]string{"started_provider_fence_identity": identity}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cityPath, ".gc", providerFenceIdentityKeyDigestFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, account); err == nil || !strings.Contains(err.Error(), "refusing to attest") {
		t.Fatalf("identity derivation with missing digest and history = %v, want continuity refusal", err)
	}
	if _, err := os.Stat(filepath.Join(cityPath, ".gc", providerFenceIdentityKeyDigestFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing continuity digest was silently recreated: %v", err)
	}
}

func TestProviderFenceIdentityKeyLossFailsClosedAfterFenceExpires(t *testing.T) {
	cityPath := t.TempDir()
	mem := beads.NewMemStore()
	resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude"}
	account := map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}
	identity, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, account)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := sessionpkg.NewStore(beads.SessionStore{Store: mem}).RecordProviderFence(identity, now.Add(-time.Minute), now.Add(-2*time.Minute), "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cityPath, ".gc")); err != nil {
		t.Fatal(err)
	}
	if _, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, account); err == nil || !strings.Contains(err.Error(), "durable keyed") {
		t.Fatalf("identity derivation after expired fence key loss = %v, want continuity failure", err)
	}
}

func TestProviderFenceIdentityKeyLossFailsClosedOnSessionAttributionHistory(t *testing.T) {
	for _, field := range []string{"provider_fence_identity", "started_provider_fence_identity", "launch_provider_fence_identity"} {
		t.Run(field, func(t *testing.T) {
			cityPath := t.TempDir()
			mem := beads.NewMemStore()
			resolved := &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude"}
			account := map[string]string{"ANTHROPIC_API_KEY": "test-account-material"}
			identity, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, account)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mem.Create(beads.Bead{Type: sessionpkg.BeadType, Labels: []string{sessionpkg.LabelSession}, Metadata: map[string]string{field: identity}}); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(cityPath, ".gc")); err != nil {
				t.Fatal(err)
			}
			if _, err := providerUsageFenceIdentityForCityWithStore(cityPath, mem, resolved, account); err == nil || !strings.Contains(err.Error(), "durable keyed") {
				t.Fatalf("identity derivation after %s history key loss = %v, want continuity failure", field, err)
			}
		})
	}
}

func TestProviderFenceIdentityDeclinesWithoutCityPath(t *testing.T) {
	identity, err := providerUsageFenceIdentityForCityWithStore("", nil, &config.ResolvedProvider{Name: "claude", BuiltinAncestor: "claude"}, map[string]string{"ANTHROPIC_API_KEY": "test-account-material"})
	if err != nil || identity != "" {
		t.Fatalf("empty-city identity = %q, %v; want empty non-error", identity, err)
	}
}
