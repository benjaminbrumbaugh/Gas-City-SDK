package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestProviderFenceRecordWaitsForIdentityAwareCreateAttribution(t *testing.T) {
	cityPath := t.TempDir()
	identity := "account:serialized-create"
	mem := beads.NewMemStore()
	provider := &providerFenceBlockingStart{
		Fake:    runtime.NewFake(),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	mgr := NewManagerWithOptions(mem, provider, WithCityPath(cityPath))
	startDone := make(chan error, 1)
	go func() {
		_, err := mgr.CreateSession(context.Background(), CreateOptions{
			Template: "worker", Title: "worker", Command: "true", WorkDir: cityPath,
			Hints: runtime.Config{ProviderFenceIdentity: identity},
		})
		startDone <- err
	}()
	<-provider.entered

	now := time.Now().UTC()
	recordDone := make(chan error, 1)
	go func() {
		recordDone <- NewStoreForCity(beads.SessionStore{Store: mem}, cityPath).RecordProviderFence(
			identity, now.Add(time.Hour), now, "usage_limit_modal",
		)
	}()
	select {
	case err := <-recordDone:
		t.Fatalf("provider fence record completed before launch attribution: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(provider.release)
	if err := <-startDone; err != nil {
		t.Fatalf("identity-aware create: %v", err)
	}
	if err := <-recordDone; err != nil {
		t.Fatalf("recording provider fence after create attribution: %v", err)
	}
}

func TestIdentityAwareStartsRereadFenceAfterAccountLockWait(t *testing.T) {
	for _, mode := range []string{"resume", "runtime-only"} {
		t.Run(mode, func(t *testing.T) {
			cityPath := t.TempDir()
			identity := "account:waiter-" + mode
			mem := beads.NewMemStore()
			provider := runtime.NewFake()
			mgr := NewManagerWithOptions(mem, provider, WithCityPath(cityPath))
			info, err := mgr.CreateSession(context.Background(), CreateOptions{
				BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: cityPath,
			})
			if err != nil {
				t.Fatal(err)
			}

			unlock, err := acquireProviderFenceAccountLock(cityPath, identity)
			if err != nil {
				t.Fatal(err)
			}
			startDone := make(chan error, 1)
			go func() {
				hints := runtime.Config{ProviderFenceIdentity: identity}
				if mode == "runtime-only" {
					startDone <- mgr.StartRuntimeOnly(context.Background(), info.ID, "true", hints)
					return
				}
				startDone <- mgr.Start(context.Background(), info.ID, "true", hints)
			}()
			select {
			case err := <-startDone:
				unlock()
				t.Fatalf("identity-aware %s did not wait for account lock: %v", mode, err)
			case <-time.After(100 * time.Millisecond):
			}

			now := time.Now().UTC()
			if err := NewStore(beads.SessionStore{Store: mem}).RecordProviderFence(identity, now.Add(time.Hour), now, "usage_limit_modal"); err != nil {
				unlock()
				t.Fatal(err)
			}
			unlock()
			err = <-startDone
			if err == nil || !strings.Contains(err.Error(), "fenced until") {
				t.Fatalf("identity-aware %s after lock wait error = %v, want active fence refusal", mode, err)
			}
			if provider.IsRunning(info.SessionName) {
				t.Fatalf("identity-aware %s launched after a fence committed first", mode)
			}
		})
	}
}

type providerFenceBlockingStart struct {
	*runtime.Fake
	entered chan struct{}
	release chan struct{}
}

func (p *providerFenceBlockingStart) Start(ctx context.Context, name string, cfg runtime.Config) error {
	close(p.entered)
	<-p.release
	return p.Fake.Start(ctx, name, cfg)
}

func TestProviderFenceAccountLockSerializesAcrossProcesses(t *testing.T) {
	const helperEnv = "GC_TEST_PROVIDER_FENCE_LOCK_HELPER"
	if os.Getenv(helperEnv) == "1" {
		cityPath := os.Getenv("GC_TEST_PROVIDER_FENCE_LOCK_CITY")
		identity := os.Getenv("GC_TEST_PROVIDER_FENCE_LOCK_IDENTITY")
		unlock, err := acquireProviderFenceAccountLock(cityPath, identity)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		if err := os.WriteFile(filepath.Join(cityPath, "child-ready"), []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(cityPath, "child-release")); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for parent release")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	cityPath := t.TempDir()
	identity := "account:cross-process"
	cmd := exec.Command(os.Args[0], "-test.run=^TestProviderFenceAccountLockSerializesAcrossProcesses$")
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		"GC_TEST_PROVIDER_FENCE_LOCK_CITY="+cityPath,
		"GC_TEST_PROVIDER_FENCE_LOCK_IDENTITY="+identity,
	)
	childDone := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { childDone <- cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(cityPath, "child-ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for child lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mem := beads.NewMemStore()
	now := time.Now().UTC()
	recordDone := make(chan error, 1)
	go func() {
		recordDone <- NewStoreForCity(beads.SessionStore{Store: mem}, cityPath).RecordProviderFence(
			identity, now.Add(time.Hour), now, "usage_limit_modal",
		)
	}()
	select {
	case err := <-recordDone:
		t.Fatalf("record crossed a provider fence account lock held by another process: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(cityPath, "child-release"), []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-childDone; err != nil {
		t.Fatalf("lock helper: %v", err)
	}
	if err := <-recordDone; err != nil {
		t.Fatal(err)
	}
}

func TestProviderFenceAccountLocksDoNotSerializeDifferentAccounts(t *testing.T) {
	cityPath := t.TempDir()
	unlockA, err := acquireProviderFenceAccountLock(cityPath, "account:a")
	if err != nil {
		t.Fatal(err)
	}
	defer unlockA()
	acquiredB := make(chan error, 1)
	go func() {
		unlockB, err := acquireProviderFenceAccountLock(cityPath, "account:b")
		if err == nil {
			unlockB()
		}
		acquiredB <- err
	}()
	select {
	case err := <-acquiredB:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("different provider accounts were serialized by a global exclusive lock")
	}
}

func TestExpiredProviderFenceLaunchClaimCannotBeStolenFromLiveOwner(t *testing.T) {
	mem := beads.NewMemStore()
	mgr := NewManagerWithOptions(mem, runtime.NewFake())
	info, err := mgr.CreateSession(context.Background(), CreateOptions{
		BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerStart, err := pidutil.StartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	claim, err := encodeProviderFenceLaunchClaim(providerFenceLaunchClaim{
		Version:    1,
		ClaimedAt:  time.Now().UTC().Add(-providerFenceLaunchClaimTTL - time.Minute),
		OwnerPID:   os.Getpid(),
		OwnerStart: ownerStart,
		Token:      "live-old-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.SetMetadata(info.ID, providerFenceLaunchClaimMetadataKey, claim); err != nil {
		t.Fatal(err)
	}
	providerFenceActiveLaunchClaims.Store("live-old-owner", struct{}{})
	defer providerFenceActiveLaunchClaims.Delete("live-old-owner")
	row, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.acquireProviderFenceLaunchClaim(info.ID, &row); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("stealing expired live-owner claim error = %v, want refusal", err)
	}
}

func TestInactiveSameProcessProviderFenceClaimIsRecoverable(t *testing.T) {
	mem := beads.NewMemStore()
	mgr := NewManagerWithOptions(mem, runtime.NewFake())
	info, err := mgr.CreateSession(context.Background(), CreateOptions{
		BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerStart, err := pidutil.StartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	abandoned, err := encodeProviderFenceLaunchClaim(providerFenceLaunchClaim{
		Version: 1, ClaimedAt: time.Now().UTC(), OwnerPID: os.Getpid(), OwnerStart: ownerStart, Token: "release-failed-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.SetMetadata(info.ID, providerFenceLaunchClaimMetadataKey, abandoned); err != nil {
		t.Fatal(err)
	}
	row, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := mgr.acquireProviderFenceLaunchClaim(info.ID, &row)
	if err != nil {
		t.Fatalf("recovering inactive same-process claim: %v", err)
	}
	mgr.releaseProviderFenceLaunchClaim(info.ID, claim)
}
