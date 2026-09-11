package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gastownhall/gascity/internal/runtime"
)

const providerFenceLocksDir = "provider-fence-locks"

type providerFenceProcessLockSet struct {
	global   sync.RWMutex
	mu       sync.Mutex
	accounts map[string]*sync.Mutex
}

var providerFenceProcessLocks = struct {
	sync.Mutex
	byScope map[string]*providerFenceProcessLockSet
}{byScope: make(map[string]*providerFenceProcessLockSet)}

func (s *Store) withProviderFenceStart(identity string, fn func() error) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return fn()
	}
	unlock, err := acquireProviderFenceScopedLock(s.lockScope, s.cityPath, identity, false)
	if err != nil {
		return fmt.Errorf("locking provider account start: %w", err)
	}
	defer unlock()
	return fn()
}

func (s *Store) withProviderFenceRecord(identity string, fn func() error) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return fn()
	}
	unlock, err := acquireProviderFenceScopedLock(s.lockScope, s.cityPath, identity, identity == LegacyGlobalProviderFenceIdentity)
	if err != nil {
		return fmt.Errorf("locking provider fence record: %w", err)
	}
	defer unlock()
	return fn()
}

// acquireProviderFenceAccountLock is the city-scoped account lock used by
// identity-aware starts and exact-account fence records. It is kept separate
// from Store so tests can pin wait/re-read ordering at the durable boundary.
func acquireProviderFenceAccountLock(cityPath, identity string) (func(), error) {
	return acquireProviderFenceScopedLock(cityPath, cityPath, strings.TrimSpace(identity), false)
}

func acquireProviderFenceScopedLock(scope, cityPath, identity string, legacyExclusive bool) (func(), error) {
	if identity == "" {
		return func() {}, nil
	}
	providerFenceProcessLocks.Lock()
	set := providerFenceProcessLocks.byScope[scope]
	if set == nil {
		set = &providerFenceProcessLockSet{accounts: make(map[string]*sync.Mutex)}
		providerFenceProcessLocks.byScope[scope] = set
	}
	providerFenceProcessLocks.Unlock()

	var releaseProcess func()
	if legacyExclusive {
		set.global.Lock()
		releaseProcess = set.global.Unlock
	} else {
		set.global.RLock()
		set.mu.Lock()
		account := set.accounts[identity]
		if account == nil {
			account = &sync.Mutex{}
			set.accounts[identity] = account
		}
		set.mu.Unlock()
		account.Lock()
		releaseProcess = func() {
			account.Unlock()
			set.global.RUnlock()
		}
	}

	if strings.TrimSpace(cityPath) == "" {
		return releaseProcess, nil
	}
	lockDir := filepath.Join(cityPath, ".gc", providerFenceLocksDir)
	if err := runtime.EnsurePrivateDir(lockDir); err != nil {
		releaseProcess()
		return nil, fmt.Errorf("preparing provider fence lock directory: %w", err)
	}
	globalUnlock, err := acquireProviderFenceFileLock(filepath.Join(lockDir, "global.lock"), legacyExclusive)
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
	accountUnlock, err := acquireProviderFenceFileLock(accountPath, true)
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
