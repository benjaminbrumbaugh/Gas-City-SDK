package extmsg

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// AdapterKey uniquely identifies a registered transport adapter.
type AdapterKey struct {
	Provider  string
	AccountID string
}

// AdapterRegistration is the ephemeral callback identity issued for one
// adapter registration. Credential is returned only at registration time and
// is stored as a hash by AdapterRegistry.
type AdapterRegistration struct {
	Credential string
	Generation uint64
	Instance   string
}

type adapterRegistration struct {
	adapter        TransportAdapter
	credentialHash [sha256.Size]byte
	generation     uint64
	instance       string
}

type registrationCredentialBinder interface {
	bindRegistrationCredential(string)
}

// AdapterRegistry is a concurrent-safe, ephemeral registry of transport
// adapters keyed by (Provider, AccountID). Created once per controller
// lifetime and not rebuilt on config hot-reload.
//
// Registrations are in-memory only and do not survive controller restarts.
// Replacing a registration revokes its prior credential, generation, and
// instance identity atomically.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[AdapterKey]adapterRegistration
}

// NewAdapterRegistry creates an empty adapter registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		adapters: make(map[AdapterKey]adapterRegistration),
	}
}

// Register adds or replaces an adapter for the given key and returns a new
// ephemeral callback credential bound to this exact registration.
func (r *AdapterRegistry) Register(key AdapterKey, adapter TransportAdapter) AdapterRegistration {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous := r.adapters[key]
	credential := uuid.NewString()
	if binder, ok := adapter.(registrationCredentialBinder); ok {
		binder.bindRegistrationCredential(credential)
	}
	registration := adapterRegistration{
		adapter:        adapter,
		credentialHash: sha256.Sum256([]byte(credential)),
		generation:     previous.generation + 1,
		instance:       uuid.NewString(),
	}
	r.adapters[key] = registration
	return AdapterRegistration{
		Credential: credential,
		Generation: registration.generation,
		Instance:   registration.instance,
	}
}

// Unregister removes an adapter only when generation and instance exactly match
// the current registration. A stale or unknown fence is a safe no-op.
func (r *AdapterRegistry) Unregister(key AdapterKey, generation uint64, instance string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	registration, ok := r.adapters[key]
	if !ok || registration.generation != generation || registration.instance != instance {
		return false
	}
	delete(r.adapters, key)
	return true
}

// Lookup returns the adapter for the given key, or nil if not registered.
func (r *AdapterRegistry) Lookup(key AdapterKey) TransportAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.adapters[key].adapter
}

// Authenticate verifies that a callback was made by the current registration
// for key. It rejects stale generations/instances and malformed credentials.
func (r *AdapterRegistry) Authenticate(key AdapterKey, adapterName string, generation uint64, instance, authorization string) (TransportAdapter, bool) {
	credential, ok := strings.CutPrefix(strings.TrimSpace(authorization), "Bearer ")
	if !ok || strings.TrimSpace(credential) == "" {
		return nil, false
	}
	candidate := sha256.Sum256([]byte(credential))
	r.mu.RLock()
	defer r.mu.RUnlock()
	registration, ok := r.adapters[key]
	if !ok || registration.adapter == nil || registration.adapter.Name() != strings.TrimSpace(adapterName) ||
		registration.generation != generation || registration.instance != strings.TrimSpace(instance) ||
		subtle.ConstantTimeCompare(candidate[:], registration.credentialHash[:]) != 1 {
		return nil, false
	}
	return registration.adapter, true
}

// LookupByConversation finds the adapter for a ConversationRef by deriving
// the key from ref.Provider and ref.AccountID.
func (r *AdapterRegistry) LookupByConversation(ref ConversationRef) TransportAdapter {
	return r.Lookup(AdapterKey{Provider: ref.Provider, AccountID: ref.AccountID})
}

// List returns all registered adapter keys.
func (r *AdapterRegistry) List() []AdapterKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]AdapterKey, 0, len(r.adapters))
	for k := range r.adapters {
		keys = append(keys, k)
	}
	return keys
}
