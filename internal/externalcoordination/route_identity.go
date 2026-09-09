package externalcoordination

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Route identity bounds. Route data is opaque correlation data carried on
// behalf of a recipient, so it is deliberately small: a durable record must
// never become a place to smuggle bulk content, a URL, or a credential.
const (
	maxRouteIdentityEntries  = 16
	maxRouteIdentityKeyLen   = 64
	maxRouteIdentityValueLen = 256
)

// credentialKeyFragments name route keys that would imply the value is a
// secret. Route identity is correlation data only; authority comes from the
// recipient's authorization snapshot and the configured target, never from
// possession of a route.
var credentialKeyFragments = []string{
	"apikey",
	"api_key",
	"authorization",
	"auth_token",
	"access_key",
	"bearer",
	"cookie",
	"credential",
	"passphrase",
	"passwd",
	"password",
	"private_key",
	"secret",
	"signature",
	"token",
}

// credentialValuePrefixes name value forms that carry an HTTP credential.
var credentialValuePrefixes = []string{"bearer ", "basic ", "digest ", "negotiate "}

// sanitizeRouteIdentity validates and copies opaque route identity data.
//
// Route identity travels with a request so a recipient can correlate it with
// its own conversation. It is never authority, never a target selector, and
// never a callback URL: the configured External Coordination target is the
// only transport target. The returned map is a fresh copy, so a caller cannot
// mutate a durable record after it is enqueued.
func sanitizeRouteIdentity(input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > maxRouteIdentityEntries {
		return nil, fmt.Errorf("%w: route_identity has %d entries, limit is %d", ErrInvalidInput, len(input), maxRouteIdentityEntries)
	}
	// Validate in a stable order so a rejected map always names the same key,
	// rather than whichever one Go's map iteration happened to reach first.
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	sanitized := make(map[string]string, len(input))
	for _, key := range keys {
		if err := validateRouteIdentityKey(key); err != nil {
			return nil, err
		}
		if err := validateRouteIdentityValue(key, input[key]); err != nil {
			return nil, err
		}
		sanitized[key] = input[key]
	}
	return sanitized, nil
}

// validateRouteIdentityKey requires a bounded, lowercase, non-credential key.
func validateRouteIdentityKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: route_identity key must not be empty", ErrInvalidInput)
	}
	if len(key) > maxRouteIdentityKeyLen {
		return fmt.Errorf("%w: route_identity key %q exceeds %d bytes", ErrInvalidInput, key, maxRouteIdentityKeyLen)
	}
	for _, char := range key {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
		case char == '_', char == '-', char == '.':
		default:
			return fmt.Errorf("%w: route_identity key %q must be lowercase [a-z0-9._-]", ErrInvalidInput, key)
		}
	}
	for _, fragment := range credentialKeyFragments {
		if strings.Contains(key, fragment) {
			return fmt.Errorf("%w: route_identity key %q names a credential; credentials are never durable route data", ErrInvalidInput, key)
		}
	}
	return nil
}

// validateRouteIdentityValue requires a bounded, printable, non-URL,
// non-credential value.
func validateRouteIdentityValue(key, value string) error {
	if len(value) > maxRouteIdentityValueLen {
		return fmt.Errorf("%w: route_identity value for %q exceeds %d bytes", ErrInvalidInput, key, maxRouteIdentityValueLen)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return fmt.Errorf("%w: route_identity value for %q contains a control character", ErrInvalidInput, key)
		}
	}
	if looksLikeURL(value) {
		return fmt.Errorf("%w: route_identity value for %q looks like a URL; a route never selects a target", ErrInvalidInput, key)
	}
	lowered := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range credentialValuePrefixes {
		if strings.HasPrefix(lowered, prefix) {
			return fmt.Errorf("%w: route_identity value for %q looks like a credential", ErrInvalidInput, key)
		}
	}
	return nil
}

// looksLikeURL reports whether a value carries a URL, in absolute,
// scheme-relative, or embedded form.
func looksLikeURL(value string) bool {
	lowered := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(lowered, "//") {
		return true
	}
	return strings.Contains(lowered, "://")
}
