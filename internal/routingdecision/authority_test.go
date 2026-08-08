package routingdecision

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeAuthorityFixture(t *testing.T, payload string, mode os.FileMode) string {
	t.Helper()
	cityRoot := t.TempDir()
	stateDir := filepath.Join(cityRoot, ".gc")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cityRoot, AuthorityRelativePath)
	if err := os.WriteFile(path, []byte(payload), mode); err != nil {
		t.Fatal(err)
	}
	return cityRoot
}

func TestLoadAuthorityFileVerifiesStrictOwnerOnlyAllowlist(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"schema":1,"authorities":[{"authority_id":"board","public_key":"%s"}]}`, base64.StdEncoding.EncodeToString(publicKey))
	cityRoot := writeAuthorityFixture(t, payload, 0o600)
	verifier, err := LoadAuthorityFile(cityRoot)
	if err != nil {
		t.Fatalf("LoadAuthorityFile: %v", err)
	}
	if len(verifier.keys) != 1 {
		t.Fatalf("loaded keys = %d, want 1", len(verifier.keys))
	}
}

func TestLoadAuthorityFileRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(publicKey)
	tests := map[string]string{
		"unknown member":   fmt.Sprintf(`{"schema":1,"extra":true,"authorities":[{"authority_id":"board","public_key":"%s"}]}`, encoded),
		"case variant":     fmt.Sprintf(`{"Schema":1,"authorities":[{"authority_id":"board","public_key":"%s"}]}`, encoded),
		"duplicate member": fmt.Sprintf(`{"schema":1,"schema":1,"authorities":[{"authority_id":"board","public_key":"%s"}]}`, encoded),
		"duplicate key":    fmt.Sprintf(`{"schema":1,"authorities":[{"authority_id":"board","public_key":"%s"},{"authority_id":"board","public_key":"%s"}]}`, encoded, encoded),
		"noncanonical id":  fmt.Sprintf(`{"schema":1,"authorities":[{"authority_id":"Board","public_key":"%s"}]}`, encoded),
		"malformed key":    `{"schema":1,"authorities":[{"authority_id":"board","public_key":"AA=="}]}`,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadAuthorityFile(writeAuthorityFixture(t, payload, 0o600)); err == nil {
				t.Fatalf("LoadAuthorityFile accepted %s", name)
			}
		})
	}

	t.Run("insecure mode", func(t *testing.T) {
		payload := fmt.Sprintf(`{"schema":1,"authorities":[{"authority_id":"board","public_key":"%s"}]}`, encoded)
		cityRoot := writeAuthorityFixture(t, payload, 0o600)
		if err := os.Chmod(filepath.Join(cityRoot, AuthorityRelativePath), 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAuthorityFile(cityRoot); err == nil {
			t.Fatal("LoadAuthorityFile accepted group-readable authority file")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		cityRoot := t.TempDir()
		if err := os.Mkdir(filepath.Join(cityRoot, ".gc"), 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "authorities.json")
		payload := fmt.Sprintf(`{"schema":1,"authorities":[{"authority_id":"board","public_key":"%s"}]}`, encoded)
		if err := os.WriteFile(target, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(cityRoot, AuthorityRelativePath)); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAuthorityFile(cityRoot); err == nil {
			t.Fatal("LoadAuthorityFile accepted symlink")
		}
	})
}

func TestLoadAuthorityFileMissingDoesNotCreateTrust(t *testing.T) {
	cityRoot := t.TempDir()
	if _, err := LoadAuthorityFile(cityRoot); err == nil {
		t.Fatal("LoadAuthorityFile accepted missing authority input")
	}
	if _, err := os.Stat(filepath.Join(cityRoot, AuthorityRelativePath)); !os.IsNotExist(err) {
		t.Fatalf("missing load created authority file: %v", err)
	}
}
