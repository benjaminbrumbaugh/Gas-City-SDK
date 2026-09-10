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

	if _, err := providerUsageFenceIdentityForCityWithStore(cityPath, cache, resolved, account); err == nil || !strings.Contains(err.Error(), "active keyed fence metadata remains") {
		t.Fatalf("identity derivation after key-directory loss error = %v, want active-fence continuity failure", err)
	}
	if _, err := os.Stat(filepath.Join(cityPath, ".gc", providerFenceIdentityKeyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity key was regenerated despite active fence: %v", err)
	}
}

func TestNewWorkerStartedSessionPersistsLaunchProviderFenceIdentity(t *testing.T) {
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
	identity := strings.TrimSpace(row.Metadata["launch_provider_fence_identity"])
	if !strings.HasPrefix(identity, "account:hmac-sha256:") {
		t.Fatalf("launch_provider_fence_identity = %q, want opaque keyed account identity", identity)
	}
}
