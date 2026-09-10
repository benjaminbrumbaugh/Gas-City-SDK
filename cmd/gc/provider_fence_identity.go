package main

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

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/session"
)

const (
	providerFenceIdentityKeyFile = "provider-fence-identity.key"
	providerFenceIdentityKeySize = 32
)

// providerUsageFenceIdentityForCity derives the opaque account identity used
// by the reconciler. The identity is keyed per city so persisted metadata does
// not expose credential material or permit offline guesses. Only canonical
// provider-family and account-material values participate; commands, env names,
// model/endpoint settings, and role-local operational values do not.
func providerUsageFenceIdentityForCity(cityPath string, resolved *config.ResolvedProvider, accountEnv map[string]string) (string, error) {
	if resolved == nil {
		return "", nil
	}
	family := strings.TrimSpace(resolved.BuiltinAncestor)
	if family == "" && len(resolved.Chain) > 0 {
		family = strings.TrimSpace(resolved.Chain[len(resolved.Chain)-1].Name)
	}
	if family == "" {
		family = strings.TrimSpace(resolved.Name)
	}
	// A standalone start_command has no stable provider/account boundary. It
	// must not turn its command line into durable identity material.
	if family == "" {
		return "", nil
	}
	key, err := loadOrCreateProviderFenceIdentityKey(cityPath)
	if err != nil {
		return "", err
	}

	values := make([]string, 0, len(accountEnv))
	seen := make(map[string]struct{}, len(accountEnv))
	for name, value := range accountEnv {
		if !isProviderFenceAccountEnv(name) {
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

// isProviderFenceAccountEnv is deliberately narrower than the provider env
// forwarding allowlist. Provider-prefixed operational settings (MODEL,
// BASE_URL, REGION, etc.) must not split one exhausted account. Abstract
// upstream credentials are represented by GC_PROVIDER_FENCE_ACCOUNT_MATERIAL_*
// entries before this filter runs, so arbitrary destination names are safe.
func isProviderFenceAccountEnv(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	if strings.HasPrefix(name, "GC_PROVIDER_FENCE_ACCOUNT_MATERIAL_") {
		return true
	}
	if providerAccountSelectorEnv[name] {
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

func loadOrCreateProviderFenceIdentityKey(cityPath string) ([]byte, error) {
	cityPath = strings.TrimSpace(cityPath)
	if cityPath == "" {
		return nil, errors.New("city path is required")
	}
	path := filepath.Join(cityPath, ".gc", providerFenceIdentityKeyFile)
	var key []byte
	err := session.WithCitySessionIdentifierLocks(cityPath, []string{"internal:provider-fence-identity-key"}, func() error {
		data, found, err := readProviderFenceIdentityKey(path)
		if err != nil {
			return err
		}
		if found {
			if len(data) != providerFenceIdentityKeySize {
				return fmt.Errorf("provider fence identity key %q has invalid length %d", path, len(data))
			}
			key = append([]byte(nil), data...)
			return nil
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
		data = make([]byte, providerFenceIdentityKeySize)
		if _, err := rand.Read(data); err != nil {
			return fmt.Errorf("generating provider fence identity key: %w", err)
		}
		if err := fsys.WriteFileAtomic(fsys.OSFS{}, path, data, 0o600); err != nil {
			return fmt.Errorf("writing provider fence identity key %q: %w", path, err)
		}
		key = append([]byte(nil), data...)
		return nil
	})
	return key, err
}

func readProviderFenceIdentityKey(path string) ([]byte, bool, error) {
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
