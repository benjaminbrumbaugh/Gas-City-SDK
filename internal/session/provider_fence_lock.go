package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
	"golang.org/x/sync/semaphore"
)

const (
	providerFenceLocksDir         = "provider-fence-locks"
	providerFenceGlobalLockWeight = int64(1 << 30)
	providerFenceFileLockRetry    = 10 * time.Millisecond
)

type providerFenceProcessLockSet struct {
	global   *semaphore.Weighted
	accounts sync.Map // map[string]*semaphore.Weighted
}

var providerFenceProcessLocks sync.Map // map[string]*providerFenceProcessLockSet

type providerFenceLockWaitObserverKey struct{}

func notifyProviderFenceLockWait(ctx context.Context) {
	if observer, ok := ctx.Value(providerFenceLockWaitObserverKey{}).(func()); ok && observer != nil {
		observer()
	}
}

func acquireProviderFenceSemaphore(ctx context.Context, lock *semaphore.Weighted, weight int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lock.TryAcquire(weight) {
		if err := ctx.Err(); err != nil {
			lock.Release(weight)
			return err
		}
		return nil
	}
	notifyProviderFenceLockWait(ctx)
	return lock.Acquire(ctx, weight)
}

func (s *Store) withProviderFenceStart(ctx context.Context, identity string, fn func() error) error {
	identity = strings.TrimSpace(identity)
	if err := ctx.Err(); err != nil {
		return err
	}
	if identity == "" {
		return fn()
	}
	unlock, err := acquireProviderFenceScopedLock(ctx, s.lockScope, s.cityPath, identity, false)
	if err != nil {
		return fmt.Errorf("locking provider account start: %w", err)
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

func (s *Store) withProviderFenceRecordContext(ctx context.Context, identity string, fn func() error) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return fn()
	}
	unlock, err := acquireProviderFenceScopedLock(ctx, s.lockScope, s.cityPath, identity, identity == LegacyGlobalProviderFenceIdentity)
	if err != nil {
		return fmt.Errorf("locking provider fence record: %w", err)
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

// acquireProviderFenceAccountLock is the city-scoped account lock used by
// identity-aware starts and exact-account fence records. It is kept separate
// from Store so tests can pin wait/re-read ordering at the durable boundary.
func acquireProviderFenceAccountLock(cityPath, identity string) (func(), error) {
	return acquireProviderFenceScopedLock(context.Background(), cityPath, cityPath, strings.TrimSpace(identity), false)
}

func acquireProviderFenceScopedLock(ctx context.Context, scope, cityPath, identity string, legacyExclusive bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if identity == "" {
		return func() {}, nil
	}
	loaded, _ := providerFenceProcessLocks.LoadOrStore(scope, &providerFenceProcessLockSet{
		global: semaphore.NewWeighted(providerFenceGlobalLockWeight),
	})
	set := loaded.(*providerFenceProcessLockSet)

	globalWeight := int64(1)
	if legacyExclusive {
		globalWeight = providerFenceGlobalLockWeight
	}
	if err := acquireProviderFenceSemaphore(ctx, set.global, globalWeight); err != nil {
		return nil, err
	}
	releaseProcess := func() { set.global.Release(globalWeight) }
	if !legacyExclusive {
		loaded, _ := set.accounts.LoadOrStore(identity, semaphore.NewWeighted(1))
		account := loaded.(*semaphore.Weighted)
		if err := acquireProviderFenceSemaphore(ctx, account, 1); err != nil {
			releaseProcess()
			return nil, err
		}
		releaseProcess = func() {
			account.Release(1)
			set.global.Release(globalWeight)
		}
	}
	if err := ctx.Err(); err != nil {
		releaseProcess()
		return nil, err
	}

	if strings.TrimSpace(cityPath) == "" {
		return releaseProcess, nil
	}
	lockDir := filepath.Join(cityPath, ".gc", providerFenceLocksDir)
	if err := runtime.EnsurePrivateDir(lockDir); err != nil {
		releaseProcess()
		return nil, fmt.Errorf("preparing provider fence lock directory: %w", err)
	}
	globalUnlock, err := acquireProviderFenceFileLock(ctx, filepath.Join(lockDir, "global.lock"), legacyExclusive)
	if err != nil {
		releaseProcess()
		return nil, err
	}
	if legacyExclusive {
		return func() {
			globalUnlock()
			releaseProcess()
		}, nil
	}
	digest := sha256.Sum256([]byte(identity))
	accountPath := filepath.Join(lockDir, "account-"+hex.EncodeToString(digest[:])+".lock")
	accountUnlock, err := acquireProviderFenceFileLock(ctx, accountPath, true)
	if err != nil {
		globalUnlock()
		releaseProcess()
		return nil, err
	}
	return func() {
		accountUnlock()
		globalUnlock()
		releaseProcess()
	}, nil
}

func waitForProviderFenceFileLock(ctx context.Context, tryAcquire func() (bool, error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		acquired, err := tryAcquire()
		if err != nil {
			return err
		}
		if acquired {
			return nil
		}
		notifyProviderFenceLockWait(ctx)
		timer := time.NewTimer(providerFenceFileLockRetry)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
