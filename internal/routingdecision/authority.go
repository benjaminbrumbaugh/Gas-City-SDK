package routingdecision

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

const (
	// AuthorityRelativePath is the separately managed controller trust input.
	AuthorityRelativePath = ".gc/routing-decision-authorities.json"
	maxAuthorityFileBytes = 256 << 10
	maxAuthorities        = 128
)

type authorityFile struct {
	Schema      int              `json:"schema"`
	Authorities []authorityEntry `json:"authorities"`
}

type authorityEntry struct {
	AuthorityID string `json:"authority_id"`
	PublicKey   string `json:"public_key"`
}

// LoadAuthorityFile loads the fixed, owner-only allowlist without following
// the city root, .gc directory, or final file through symlinks. Absence is an
// error and never creates trust material.
func LoadAuthorityFile(cityRoot string) (Verifier, error) {
	data, err := readCityStateFile(cityRoot, filepath.Base(AuthorityRelativePath), maxAuthorityFileBytes, true)
	if err != nil {
		return Verifier{}, fmt.Errorf("%w: authority input unavailable", ErrAuthorizationRequired)
	}
	if err := validateAuthorityJSON(data); err != nil {
		return Verifier{}, fmt.Errorf("%w: authority input shape", ErrInvalidDecision)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var input authorityFile
	if err := decoder.Decode(&input); err != nil {
		return Verifier{}, fmt.Errorf("%w: authority input decode", ErrInvalidDecision)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Verifier{}, err
	}
	if input.Schema != SchemaVersion || len(input.Authorities) == 0 || len(input.Authorities) > maxAuthorities {
		return Verifier{}, fmt.Errorf("%w: authority input bounds", ErrInvalidDecision)
	}
	keys := make(map[string]ed25519.PublicKey, len(input.Authorities))
	for _, entry := range input.Authorities {
		if err := validateText("authority_id", entry.AuthorityID, true); err != nil {
			return Verifier{}, err
		}
		if !canonicalAuthorityID(entry.AuthorityID) {
			return Verifier{}, fmt.Errorf("%w: noncanonical authority", ErrInvalidDecision)
		}
		if _, exists := keys[entry.AuthorityID]; exists {
			return Verifier{}, fmt.Errorf("%w: duplicate authority", ErrInvalidDecision)
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(entry.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return Verifier{}, fmt.Errorf("%w: malformed authority key", ErrInvalidDecision)
		}
		keys[entry.AuthorityID] = ed25519.PublicKey(decoded)
	}
	return NewVerifier(keys), nil
}

func canonicalAuthorityID(value string) bool {
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return value != ""
}

type jsonValueValidator func(*json.Decoder) error

func validateAuthorityJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	err := validateJSONObject(decoder, map[string]jsonValueValidator{
		"schema": consumeJSONValue,
		"authorities": func(decoder *json.Decoder) error {
			return validateJSONArray(decoder, func(decoder *json.Decoder) error {
				return validateJSONObject(decoder, map[string]jsonValueValidator{
					"authority_id": consumeJSONValue,
					"public_key":   consumeJSONValue,
				})
			})
		},
	})
	if err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func validateJSONObject(decoder *json.Decoder, members map[string]jsonValueValidator) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("expected object")
	}
	seen := make(map[string]struct{}, len(members))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		validator, allowed := members[key]
		if !ok || !allowed {
			return errors.New("unknown object member")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("duplicate object member")
		}
		seen[key] = struct{}{}
		if err := validator(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func validateJSONArray(decoder *json.Decoder, element jsonValueValidator) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return errors.New("expected array")
	}
	for decoder.More() {
		if err := element(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func consumeJSONValue(decoder *json.Decoder) error {
	var raw json.RawMessage
	return decoder.Decode(&raw)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON data", ErrInvalidDecision)
	}
	return nil
}
