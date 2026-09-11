package session

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/testutil"
)

func awaitProviderFenceLockWait(t *testing.T, waiting <-chan struct{}, done <-chan error) {
	t.Helper()
	timer := time.NewTimer(testutil.GoroutineRaceTimeout)
	defer timer.Stop()
	select {
	case <-waiting:
	case err := <-done:
		t.Fatalf("lock operation returned before reporting contention: %v", err)
	case <-timer.C:
		t.Fatal("timed out waiting for lock contention signal")
	}
}

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
	waiting := make(chan struct{})
	var waitingOnce sync.Once
	recordCtx := context.WithValue(context.Background(), providerFenceLockWaitObserverKey{}, func() {
		waitingOnce.Do(func() { close(waiting) })
	})
	recordDone := make(chan error, 1)
	go func() {
		recordDone <- NewStoreForCity(beads.SessionStore{Store: mem}, cityPath).recordProviderFence(
			recordCtx,
			identity, now.Add(time.Hour), now, "usage_limit_modal",
		)
	}()
	awaitProviderFenceLockWait(t, waiting, recordDone)
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
			waiting := make(chan struct{})
			var waitingOnce sync.Once
			startCtx := context.WithValue(context.Background(), providerFenceLockWaitObserverKey{}, func() {
				waitingOnce.Do(func() { close(waiting) })
			})
			startDone := make(chan error, 1)
			go func() {
				hints := runtime.Config{ProviderFenceIdentity: identity}
				if mode == "runtime-only" {
					startDone <- mgr.StartRuntimeOnly(startCtx, info.ID, "true", hints)
					return
				}
				startDone <- mgr.Start(startCtx, info.ID, "true", hints)
			}()
			awaitProviderFenceLockWait(t, waiting, startDone)

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
		if _, err := os.Stdout.Write([]byte("ready\n")); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatal(err)
		}
		return
	}

	cityPath := t.TempDir()
	identity := "account:cross-process"
	cmd := exec.Command(os.Args[0], "-test.run=^TestProviderFenceAccountLockSerializesAcrossProcesses$")
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		"GC_TEST_PROVIDER_FENCE_LOCK_CITY="+cityPath,
		"GC_TEST_PROVIDER_FENCE_LOCK_IDENTITY="+identity,
	)
	childIn, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	childOut, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := make(chan error, 1)
	go func() { childDone <- cmd.Wait() }()
	childReaped := false
	t.Cleanup(func() {
		_ = childIn.Close()
		if !childReaped {
			_ = cmd.Process.Kill()
			<-childDone
		}
	})
	var ready [6]byte
	if _, err := io.ReadFull(childOut, ready[:]); err != nil {
		t.Fatalf("reading lock helper readiness: %v", err)
	}
	if string(ready[:]) != "ready\n" {
		t.Fatalf("lock helper readiness = %q, want %q", ready[:], "ready\\n")
	}

	mem := beads.NewMemStore()
	now := time.Now().UTC()
	canceledWaiting := make(chan struct{})
	var canceledWaitingOnce sync.Once
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), providerFenceLockWaitObserverKey{}, func() {
		canceledWaitingOnce.Do(func() { close(canceledWaiting) })
	}))
	canceledDone := make(chan error, 1)
	go func() {
		canceledDone <- NewStoreForCity(beads.SessionStore{Store: mem}, cityPath).recordProviderFence(
			ctx, identity, now.Add(time.Hour), now, "usage_limit_modal",
		)
	}()
	awaitProviderFenceLockWait(t, canceledWaiting, canceledDone)
	cancel()
	if err := <-canceledDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("record canceled during cross-process lock wait error = %v, want context.Canceled", err)
	}

	recordWaiting := make(chan struct{})
	var recordWaitingOnce sync.Once
	recordCtx := context.WithValue(context.Background(), providerFenceLockWaitObserverKey{}, func() {
		recordWaitingOnce.Do(func() { close(recordWaiting) })
	})
	recordDone := make(chan error, 1)
	go func() {
		recordDone <- NewStoreForCity(beads.SessionStore{Store: mem}, cityPath).recordProviderFence(
			recordCtx, identity, now.Add(time.Hour), now, "usage_limit_modal",
		)
	}()
	awaitProviderFenceLockWait(t, recordWaiting, recordDone)
	if err := childIn.Close(); err != nil {
		t.Fatal(err)
	}
	childErr := <-childDone
	childReaped = true
	if childErr != nil {
		t.Fatalf("lock helper: %v", childErr)
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

func TestProviderFenceStartCanceledWhileWaitingDoesNotLaunch(t *testing.T) {
	cityPath := t.TempDir()
	identity := "account:canceled-waiter"
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
	waiting := make(chan struct{})
	var waitingOnce sync.Once
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), providerFenceLockWaitObserverKey{}, func() {
		waitingOnce.Do(func() { close(waiting) })
	}))
	startDone := make(chan error, 1)
	go func() {
		startDone <- mgr.Start(ctx, info.ID, "true", runtime.Config{ProviderFenceIdentity: identity})
	}()
	awaitProviderFenceLockWait(t, waiting, startDone)
	cancel()
	unlock()

	if err := <-startDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("start after cancellation error = %v, want context.Canceled", err)
	}
	if provider.IsRunning(info.SessionName) {
		t.Fatal("provider start ran after its account-lock wait was canceled")
	}
	retryUnlock, err := acquireProviderFenceAccountLock(cityPath, identity)
	if err != nil {
		t.Fatalf("reacquiring account lock after canceled waiter: %v", err)
	}
	retryUnlock()
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
