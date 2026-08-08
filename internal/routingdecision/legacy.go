package routingdecision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	bbolt "go.etcd.io/bbolt"
)

const (
	// LegacyRelativePath is read only by the explicit offline importer.
	LegacyRelativePath = ".gc/routing-intents.json"
	maxLegacyFileBytes = 1 << 20
	maxLegacyIntents   = 256
)

// LegacyEnvelope is the retired flat-file schema accepted only for import.
type LegacyEnvelope struct {
	Schema  int            `json:"schema"`
	Intents []LegacyIntent `json:"intents"`
}

// LegacyIntent preserves every old audit field. ApprovalID is untrusted audit
// text and is never converted into an approval or signature.
type LegacyIntent struct {
	Schema              int                 `json:"schema"`
	ID                  string              `json:"intent_id"`
	WorkBeadID          string              `json:"work_bead_id"`
	City                string              `json:"city"`
	Rig                 string              `json:"rig,omitempty"`
	Target              string              `json:"target"`
	ExpectedStatus      string              `json:"expected_status"`
	ExpectedAssignee    string              `json:"expected_assignee"`
	ExpectedStateDigest string              `json:"expected_state_digest"`
	PolicyDigest        string              `json:"policy_digest"`
	ObservationDigest   string              `json:"observation_digest"`
	Model               string              `json:"model"`
	Source              string              `json:"source"`
	Account             string              `json:"account"`
	ServeAs             string              `json:"serve_as,omitempty"`
	Provider            string              `json:"provider,omitempty"`
	Endpoint            string              `json:"endpoint,omitempty"`
	Fallbacks           []LegacyAlternative `json:"fallbacks,omitempty"`
	Reason              string              `json:"reason,omitempty"`
	ApprovalID          string              `json:"approval_id"`
	CreatedAt           time.Time           `json:"created_at"`
	ExpiresAt           time.Time           `json:"expires_at"`
	NoMigration         bool                `json:"no_migration"`
}

// IsActiveAt reports whether the legacy candidate is currently fresh enough
// for explicit enrichment. It does not imply approval.
func (intent LegacyIntent) IsActiveAt(now time.Time) bool {
	return !now.Before(intent.CreatedAt) && now.Before(intent.ExpiresAt)
}

// LegacyAlternative is an ordered audit-only fallback from the retired schema.
type LegacyAlternative struct {
	Target   string `json:"target"`
	Model    string `json:"model"`
	Source   string `json:"source"`
	Account  string `json:"account"`
	ServeAs  string `json:"serve_as,omitempty"`
	Provider string `json:"provider,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// LegacySource binds parsed input to the exact file identity read.
type LegacySource struct {
	Envelope LegacyEnvelope
	Digest   string
	cityRoot string
	path     string
	info     os.FileInfo
}

// LegacyImportResult identifies the committed receipt and digest-named archive.
type LegacyImportResult struct {
	SourceDigest string   `json:"source_digest"`
	ArchiveName  string   `json:"archive_name"`
	DecisionIDs  []string `json:"decision_ids"`
}

type legacyImportReceipt struct {
	SourceDigest string    `json:"source_digest"`
	ArchiveName  string    `json:"archive_name"`
	DecisionIDs  []string  `json:"decision_ids"`
	ImportedAt   time.Time `json:"imported_at"`
}

// LoadLegacySource strictly parses the fixed retired file without following
// symlinks. No controller startup or tick calls this function.
func LoadLegacySource(cityRoot string) (LegacySource, error) {
	data, err := readCityStateFile(cityRoot, filepath.Base(LegacyRelativePath), maxLegacyFileBytes, true)
	if err != nil {
		return LegacySource{}, fmt.Errorf("legacy source unavailable")
	}
	if err := validateLegacyJSON(data); err != nil {
		return LegacySource{}, fmt.Errorf("legacy source invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope LegacyEnvelope
	if err := decoder.Decode(&envelope); err != nil || requireJSONEOF(decoder) != nil || validateLegacyEnvelope(envelope) != nil {
		return LegacySource{}, fmt.Errorf("legacy source invalid")
	}
	path := filepath.Join(cityRoot, LegacyRelativePath)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return LegacySource{}, fmt.Errorf("legacy source identity unavailable")
	}
	sum := sha256.Sum256(data)
	return LegacySource{Envelope: envelope, Digest: hex.EncodeToString(sum[:]), cityRoot: cityRoot, path: path, info: info}, nil
}

// LegacyArchiveName returns the collision-resistant post-import archive name.
func LegacyArchiveName(digest string) string {
	return "routing-intents.imported-" + digest + ".json"
}

// ImportLegacy atomically creates unsigned proposed decisions plus a source
// receipt, then archives the exact source inode. A committed receipt makes a
// retry perform only the missing archive step.
func (store *Store) ImportLegacy(source LegacySource, payloads []DecisionPayload) (LegacyImportResult, error) {
	if !validDigest(source.Digest) || source.path == "" || source.info == nil {
		return LegacyImportResult{}, ErrInvalidDecision
	}
	result := LegacyImportResult{SourceDigest: source.Digest, ArchiveName: LegacyArchiveName(source.Digest), DecisionIDs: []string{}}
	err := store.db.Update(func(tx *bbolt.Tx) error {
		if raw := tx.Bucket(bucketImports).Get([]byte(source.Digest)); raw != nil {
			var receipt legacyImportReceipt
			if err := strictUnmarshal(raw, &receipt); err != nil || receipt.SourceDigest != source.Digest {
				return ErrStoreCorrupt
			}
			result.ArchiveName = receipt.ArchiveName
			result.DecisionIDs = append([]string(nil), receipt.DecisionIDs...)
			return nil
		}
		if len(payloads) != len(source.Envelope.Intents) || len(payloads) == 0 || len(payloads) > maxLegacyIntents {
			return fmt.Errorf("%w: legacy enrichment count", ErrInvalidDecision)
		}
		seenWork := make(map[string]struct{}, len(payloads))
		for index, payload := range payloads {
			if err := payload.Validate(); err != nil || payload.WorkBeadID != source.Envelope.Intents[index].WorkBeadID {
				return fmt.Errorf("%w: legacy enrichment mismatch", ErrInvalidDecision)
			}
			if _, duplicate := seenWork[payload.WorkBeadID]; duplicate {
				return fmt.Errorf("%w: duplicate imported work", ErrInvalidDecision)
			}
			seenWork[payload.WorkBeadID] = struct{}{}
			if tx.Bucket(bucketDecisions).Get([]byte(payload.DecisionID)) != nil {
				return ErrDecisionExists
			}
			storeRevision, err := nextStoreRevision(tx)
			if err != nil {
				return err
			}
			record := Record{Payload: payload, State: StateProposed, RecordRevision: 1, StoreRevision: storeRevision}
			if err := putRecord(tx, record); err != nil {
				return err
			}
			if err := adjustStateCount(tx, StateProposed, 1); err != nil {
				return err
			}
			audit := TransitionAudit{DecisionID: payload.DecisionID, To: StateProposed, At: store.now().UTC(), Reason: "imported from unsigned legacy routing intent", RecordRevision: 1, StoreRevision: storeRevision}
			if err := putAudit(tx, audit); err != nil {
				return err
			}
			result.DecisionIDs = append(result.DecisionIDs, payload.DecisionID)
		}
		receipt := legacyImportReceipt{SourceDigest: source.Digest, ArchiveName: result.ArchiveName, DecisionIDs: append([]string(nil), result.DecisionIDs...), ImportedAt: store.now().UTC()}
		return putJSON(tx.Bucket(bucketImports), []byte(source.Digest), receipt)
	})
	if err != nil {
		return LegacyImportResult{}, classifyStoreError(err)
	}
	if err := archiveLegacySource(source, result.ArchiveName); err != nil {
		return LegacyImportResult{}, fmt.Errorf("legacy import committed but source archive failed")
	}
	return result, nil
}

func archiveLegacySource(source LegacySource, archiveName string) error {
	current, err := os.Lstat(source.path)
	if err != nil || !os.SameFile(source.info, current) || !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 {
		return errors.New("legacy source identity changed")
	}
	destination := filepath.Join(source.cityRoot, ".gc", archiveName)
	if err := renameNoReplace(source.path, destination); err != nil {
		return err
	}
	archived, err := os.Lstat(destination)
	if err != nil || !os.SameFile(source.info, archived) {
		return errors.New("legacy archive identity mismatch")
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err == nil {
		err = directory.Sync()
		_ = directory.Close()
	}
	return err
}

func validateLegacyEnvelope(envelope LegacyEnvelope) error {
	if envelope.Schema != SchemaVersion || len(envelope.Intents) > maxLegacyIntents {
		return ErrInvalidDecision
	}
	seenID := make(map[string]struct{}, len(envelope.Intents))
	seenWork := make(map[string]struct{}, len(envelope.Intents))
	for _, intent := range envelope.Intents {
		if validateLegacyIntent(intent) != nil {
			return ErrInvalidDecision
		}
		if _, exists := seenID[intent.ID]; exists {
			return ErrInvalidDecision
		}
		if _, exists := seenWork[intent.WorkBeadID]; exists {
			return ErrInvalidDecision
		}
		seenID[intent.ID] = struct{}{}
		seenWork[intent.WorkBeadID] = struct{}{}
	}
	return nil
}

func validateLegacyIntent(intent LegacyIntent) error {
	if intent.Schema != SchemaVersion || intent.ExpectedStatus != "open" || intent.ExpectedAssignee != "" || !intent.NoMigration || intent.CreatedAt.IsZero() || !intent.ExpiresAt.After(intent.CreatedAt) || len(intent.Fallbacks) > maxAlternatives {
		return ErrInvalidDecision
	}
	for name, value := range map[string]string{
		"intent_id": intent.ID, "work_bead_id": intent.WorkBeadID, "city": intent.City,
		"target": intent.Target, "model": intent.Model, "source": intent.Source,
		"account": intent.Account, "approval_id": intent.ApprovalID,
	} {
		if validateText(name, value, true) != nil {
			return ErrInvalidDecision
		}
	}
	for name, value := range map[string]string{
		"rig": intent.Rig, "serve_as": intent.ServeAs, "provider": intent.Provider,
		"endpoint": intent.Endpoint, "reason": intent.Reason,
	} {
		if validateText(name, value, false) != nil {
			return ErrInvalidDecision
		}
	}
	for _, digest := range []string{intent.ExpectedStateDigest, intent.PolicyDigest, intent.ObservationDigest} {
		if !strings.HasPrefix(digest, "sha256:") || !validDigest(strings.TrimPrefix(digest, "sha256:")) {
			return ErrInvalidDecision
		}
	}
	for _, fallback := range intent.Fallbacks {
		for name, value := range map[string]string{"target": fallback.Target, "model": fallback.Model, "source": fallback.Source, "account": fallback.Account} {
			if validateText(name, value, true) != nil {
				return ErrInvalidDecision
			}
		}
		for name, value := range map[string]string{"serve_as": fallback.ServeAs, "provider": fallback.Provider, "endpoint": fallback.Endpoint, "reason": fallback.Reason} {
			if validateText(name, value, false) != nil {
				return ErrInvalidDecision
			}
		}
	}
	return nil
}

func validateLegacyJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	err := validateJSONObject(decoder, map[string]jsonValueValidator{
		"schema": consumeJSONValue,
		"intents": func(decoder *json.Decoder) error {
			return validateJSONArray(decoder, validateLegacyIntentJSON)
		},
	})
	if err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func validateLegacyIntentJSON(decoder *json.Decoder) error {
	members := make(map[string]jsonValueValidator)
	for _, name := range []string{
		"schema", "intent_id", "work_bead_id", "city", "rig", "target", "expected_status", "expected_assignee",
		"expected_state_digest", "policy_digest", "observation_digest", "model", "source", "account", "serve_as",
		"provider", "endpoint", "reason", "approval_id", "created_at", "expires_at", "no_migration",
	} {
		members[name] = consumeJSONValue
	}
	members["fallbacks"] = func(decoder *json.Decoder) error {
		return validateJSONArray(decoder, func(decoder *json.Decoder) error {
			fallbackMembers := make(map[string]jsonValueValidator)
			for _, name := range []string{"target", "model", "source", "account", "serve_as", "provider", "endpoint", "reason"} {
				fallbackMembers[name] = consumeJSONValue
			}
			return validateJSONObject(decoder, fallbackMembers)
		})
	}
	return validateJSONObject(decoder, members)
}
