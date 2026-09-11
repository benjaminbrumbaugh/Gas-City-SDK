// Package providerfence derives opaque, city-scoped provider account identities.
package providerfence

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/session"
)

const (
	// IdentityKeyFile is the city-local secret key used to derive opaque identities.
	IdentityKeyFile = "provider-fence-identity.key"
	// IdentityKeyDigestFile is the city-local non-secret continuity digest.
	IdentityKeyDigestFile = "provider-fence-identity.key.sha256"
	identityKeySize       = 32
)

var accountSelectorEnv = map[string]bool{
	"CLAUDE_CODE_OAUTH_TOKEN": true,
	"CLAUDE_CONFIG_DIR":       true,
	"CODEX_HOME":              true,
	"HOME":                    true,
	"XDG_CONFIG_HOME":         true,
}

// IdentityForCity derives the opaque account identity used by provider fences.
func IdentityForCity(cityPath string, resolved *config.ResolvedProvider, accountEnv map[string]string) (string, error) {
	return IdentityForCityWithStore(cityPath, nil, resolved, accountEnv)
}

// IdentityForCityWithStore derives a keyed identity and verifies durable key
// continuity when a store is available. Storeless city discovery may pass nil.
func IdentityForCityWithStore(cityPath string, store beads.Store, resolved *config.ResolvedProvider, accountEnv map[string]string) (string, error) {
	if resolved == nil {
		return "", nil
	}
	// Storeless callers used by city-path discovery can legitimately have no
	// city yet. There is nowhere safe to persist a continuity key in that state.
	if strings.TrimSpace(cityPath) == "" {
		return "", nil
	}
	family := strings.TrimSpace(resolved.BuiltinAncestor)
	if family == "" && len(resolved.Chain) > 0 {
		family = strings.TrimSpace(resolved.Chain[len(resolved.Chain)-1].Name)
	}
	if family == "" {
		family = strings.TrimSpace(resolved.Name)
	}
	// A standalone start_command has no stable provider/account boundary.
	if family == "" {
		return "", nil
	}
	key, err := loadOrCreateIdentityKeyWithStore(cityPath, store)
	if err != nil {
		return "", err
	}

	values := make([]string, 0, len(accountEnv))
	seen := make(map[string]struct{}, len(accountEnv))
	for name, value := range accountEnv {
		if !IsAccountEnv(name) {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	sort.Strings(values)
	payload, err := json.Marshal(struct {
		Version int      `json:"version"`
		Family  string   `json:"family"`
		Account []string `json:"account,omitempty"`
	}{Version: 1, Family: family, Account: values})
	if err != nil {
		return "", fmt.Errorf("encoding provider account identity: %w", err)
	}
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(payload)
	return "account:hmac-sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

// IsAccountEnv reports whether an environment name carries provider account
// material rather than an operational provider setting.
func IsAccountEnv(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	if strings.HasPrefix(name, "GC_PROVIDER_FENCE_ACCOUNT_MATERIAL_") {
		return true
	}
	if accountSelectorEnv[name] {
		return true
	}
	for _, suffix := range []string{
		"API_KEY", "AUTH_TOKEN", "ACCESS_TOKEN", "OAUTH_TOKEN", "BEARER_TOKEN",
		"SECRET_ACCESS_KEY", "SESSION_TOKEN", "CREDENTIAL", "CREDENTIALS",
		"CREDENTIALS_FILE", "CONFIG_FILE", "TOKEN_FILE", "PROFILE", "ROLE_ARN",
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// AccountEnv returns only effective environment values used for account
// identity derivation. The returned map must remain launch-local.
func AccountEnv(env map[string]string) map[string]string {
	accountEnv := make(map[string]string)
	for name, value := range env {
		if IsAccountEnv(name) {
			accountEnv[name] = value
		}
	}
	return accountEnv
}

func loadOrCreateIdentityKeyWithStore(cityPath string, store beads.Store) ([]byte, error) {
	cityPath = strings.TrimSpace(cityPath)
	if cityPath == "" {
		return nil, errors.New("city path is required")
	}
	path := filepath.Join(cityPath, ".gc", IdentityKeyFile)
	digestPath := filepath.Join(cityPath, ".gc", IdentityKeyDigestFile)
	var key []byte
	err := session.WithCitySessionIdentifierLocks(cityPath, []string{"internal:provider-fence-identity-key"}, func() error {
		data, found, err := readIdentityKey(path)
		if err != nil {
			return err
		}
		digestData, digestFound, err := readIdentityKey(digestPath)
		if err != nil {
			return err
		}
		if found {
			if len(data) != identityKeySize {
				return fmt.Errorf("provider fence identity key %q has invalid length %d", path, len(data))
			}
			digest := sha256.Sum256(data)
			encodedDigest := []byte(hex.EncodeToString(digest[:]))
			if digestFound && !hmac.Equal([]byte(strings.TrimSpace(string(digestData))), encodedDigest) {
				return fmt.Errorf("provider fence identity key %q does not match its continuity digest", path)
			}
			if !digestFound {
				if store == nil {
					key = append([]byte(nil), data...)
					return nil
				}
				hasHistory, historyErr := session.NewStore(beads.SessionStore{Store: store}).HasKeyedProviderFenceIdentityHistory()
				if historyErr != nil {
					return fmt.Errorf("checking durable provider fence identity continuity: %w", historyErr)
				}
				if hasHistory {
					return fmt.Errorf("provider fence identity continuity digest %q is missing while durable keyed fence or session attribution metadata remains; refusing to attest the current key", digestPath)
				}
				if err := fsys.WriteFileAtomic(fsys.OSFS{}, digestPath, encodedDigest, 0o600); err != nil {
					return fmt.Errorf("writing provider fence identity continuity digest %q: %w", digestPath, err)
				}
			}
			if store != nil {
				if err := session.NewStore(beads.SessionStore{Store: store}).AttestProviderFenceIdentityKeyDigest(string(encodedDigest)); err != nil {
					return fmt.Errorf("checking provider fence identity durable key digest: %w", err)
				}
			}
			key = append([]byte(nil), data...)
			return nil
		}
		if digestFound {
			return fmt.Errorf("provider fence identity key %q is missing while continuity digest %q exists", path, digestPath)
		}
		if store != nil {
			hasHistory, err := session.NewStore(beads.SessionStore{Store: store}).HasKeyedProviderFenceIdentityHistory()
			if err != nil {
				return fmt.Errorf("checking durable provider fence identity continuity: %w", err)
			}
			if hasHistory {
				return fmt.Errorf("provider fence identity key %q is missing while durable keyed fence or session attribution metadata remains", path)
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("creating provider fence identity directory: %w", err)
		}
		info, err := os.Lstat(filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("inspecting provider fence identity directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("provider fence identity directory must be a real directory")
		}
		if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("securing provider fence identity directory: %w", err)
		}
		data = make([]byte, identityKeySize)
		if _, err := rand.Read(data); err != nil {
			return fmt.Errorf("generating provider fence identity key: %w", err)
		}
		if err := fsys.WriteFileAtomic(fsys.OSFS{}, path, data, 0o600); err != nil {
			return fmt.Errorf("writing provider fence identity key %q: %w", path, err)
		}
		digest := sha256.Sum256(data)
		if err := fsys.WriteFileAtomic(fsys.OSFS{}, digestPath, []byte(hex.EncodeToString(digest[:])), 0o600); err != nil {
			return fmt.Errorf("writing provider fence identity continuity digest %q: %w", digestPath, err)
		}
		if store != nil {
			if err := session.NewStore(beads.SessionStore{Store: store}).AttestProviderFenceIdentityKeyDigest(hex.EncodeToString(digest[:])); err != nil {
				return fmt.Errorf("recording provider fence identity durable key digest: %w", err)
			}
		}
		key = append([]byte(nil), data...)
		return nil
	})
	return key, err
}

func readIdentityKey(path string) ([]byte, bool, error) {
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspecting provider fence identity key %q: %w", path, err)
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("provider fence identity key %q must be a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("opening provider fence identity key %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	openedInfo, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("inspecting opened provider fence identity key %q: %w", path, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return nil, false, fmt.Errorf("provider fence identity key %q changed while opening", path)
	}
	if err := f.Chmod(0o600); err != nil {
		return nil, false, fmt.Errorf("securing provider fence identity key %q: %w", path, err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, fmt.Errorf("reading provider fence identity key %q: %w", path, err)
	}
	return data, true, nil
}
