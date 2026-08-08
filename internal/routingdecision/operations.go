package routingdecision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	bbolt "go.etcd.io/bbolt"
)

// VerifyReport summarizes the layer inspected by Store.Verify.
type VerifyReport struct {
	SchemaVersion  uint64 `json:"schema_version"`
	StoreRevision  uint64 `json:"store_revision"`
	Decisions      int    `json:"decisions"`
	Transitions    int    `json:"transitions"`
	Idempotency    int    `json:"idempotency_receipts"`
	ImportReceipts int    `json:"import_receipts"`
}

type exportDocument struct {
	SchemaVersion uint64            `json:"schema_version"`
	StoreRevision uint64            `json:"store_revision"`
	Records       []Record          `json:"records"`
	Transitions   []TransitionAudit `json:"transitions"`
}

// Backup writes a consistent live snapshot to a new 0600 regular file. It
// never overwrites a destination and does not reuse bbolt's database opener.
func (store *Store) Backup(destination string) (err error) {
	parent := filepath.Dir(destination)
	info, statErr := os.Lstat(parent)
	if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrStoreCorrupt
	}
	file, err := openExclusivePrivateFile(destination)
	if err != nil {
		return fmt.Errorf("backup destination unavailable")
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			removeExactNewStore(destination) //nolint:errcheck
		}
	}()
	err = store.db.View(func(tx *bbolt.Tx) error {
		_, writeErr := tx.WriteTo(file)
		return writeErr
	})
	if err != nil {
		return ErrStoreCorrupt
	}
	if err := file.Sync(); err != nil {
		return ErrStoreCorrupt
	}
	if err := file.Close(); err != nil {
		return ErrStoreCorrupt
	}
	keep = true
	return nil
}

// Export writes a deterministic typed audit document. Idempotency tokens and
// import source paths are deliberately excluded from the export surface.
func (store *Store) Export(writer io.Writer) error {
	document := exportDocument{Records: []Record{}, Transitions: []TransitionAudit{}}
	err := store.db.View(func(tx *bbolt.Tx) error {
		meta := tx.Bucket(bucketMeta)
		var ok bool
		document.SchemaVersion, ok = decodeUint64(meta.Get(keySchemaVersion))
		if !ok {
			return ErrStoreCorrupt
		}
		document.StoreRevision, ok = decodeUint64(meta.Get(keyStoreRevision))
		if !ok {
			return ErrStoreCorrupt
		}
		if err := tx.Bucket(bucketDecisions).ForEach(func(_, value []byte) error {
			var record Record
			if err := decodeRecord(value, &record); err != nil {
				return err
			}
			document.Records = append(document.Records, record)
			return nil
		}); err != nil {
			return err
		}
		return tx.Bucket(bucketTransitions).ForEach(func(_, value []byte) error {
			var audit TransitionAudit
			if err := strictUnmarshal(value, &audit); err != nil {
				return err
			}
			document.Transitions = append(document.Transitions, audit)
			return nil
		})
	})
	if err != nil {
		return classifyStoreError(err)
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document); err != nil {
		return ErrStoreCorrupt
	}
	return nil
}

// Verify checks bbolt pages plus every application record, index, transition,
// idempotency receipt, revision, canonical binding, and stored approval.
func (store *Store) Verify(verifier Verifier) (VerifyReport, error) {
	var report VerifyReport
	err := store.db.View(func(tx *bbolt.Tx) error {
		for pageErr := range tx.Check() {
			if pageErr != nil {
				return ErrStoreCorrupt
			}
		}
		for _, name := range requiredBucketNames {
			if tx.Bucket(name) == nil {
				return ErrStoreCorrupt
			}
		}
		var ok bool
		report.SchemaVersion, ok = decodeUint64(tx.Bucket(bucketMeta).Get(keySchemaVersion))
		if !ok || report.SchemaVersion != SchemaVersion {
			return ErrUnsupportedSchema
		}
		report.StoreRevision, ok = decodeUint64(tx.Bucket(bucketMeta).Get(keyStoreRevision))
		if !ok {
			return ErrStoreCorrupt
		}

		records := make(map[string]Record)
		if err := tx.Bucket(bucketDecisions).ForEach(func(key, value []byte) error {
			var record Record
			if err := decodeRecord(value, &record); err != nil || string(key) != record.Payload.DecisionID {
				return ErrStoreCorrupt
			}
			if indexed := tx.Bucket(bucketStateExpiry).Get(stateIndexKey(record)); string(indexed) != record.Payload.DecisionID {
				return ErrStoreCorrupt
			}
			requiresApproval := record.State == StateApproved || record.State == StateAdmitted || record.State == StateRefusedAfterRace || record.State == StateClaimed || record.State == StateOutcomeRecorded
			if requiresApproval && (record.Approval == nil || record.Signature == nil) {
				return ErrStoreCorrupt
			}
			if record.Approval != nil || record.Signature != nil {
				if record.Approval == nil || record.Signature == nil {
					return ErrStoreCorrupt
				}
				if err := verifier.Verify(record.Payload, *record.Approval, *record.Signature); err != nil {
					return err
				}
			}
			if record.StoreRevision > report.StoreRevision {
				return ErrStoreCorrupt
			}
			records[record.Payload.DecisionID] = record
			report.Decisions++
			return nil
		}); err != nil {
			return err
		}

		indexCount := 0
		if err := tx.Bucket(bucketStateExpiry).ForEach(func(key, value []byte) error {
			record, exists := records[string(value)]
			if !exists || !bytes.Equal(key, stateIndexKey(record)) {
				return ErrStoreCorrupt
			}
			indexCount++
			return nil
		}); err != nil || indexCount != len(records) {
			return ErrStoreCorrupt
		}

		auditCount := make(map[string]uint64, len(records))
		lastState := make(map[string]State, len(records))
		if err := tx.Bucket(bucketTransitions).ForEach(func(_, value []byte) error {
			var audit TransitionAudit
			if err := strictUnmarshal(value, &audit); err != nil || audit.DecisionID == "" || audit.RecordRevision == 0 || audit.StoreRevision == 0 || audit.StoreRevision > report.StoreRevision {
				return ErrStoreCorrupt
			}
			expectedRevision := auditCount[audit.DecisionID] + 1
			if audit.RecordRevision != expectedRevision || (expectedRevision == 1 && audit.From != "") || (expectedRevision > 1 && lastState[audit.DecisionID] != audit.From) {
				return ErrStoreCorrupt
			}
			auditCount[audit.DecisionID] = expectedRevision
			lastState[audit.DecisionID] = audit.To
			report.Transitions++
			return nil
		}); err != nil {
			return err
		}
		for id, record := range records {
			if auditCount[id] != record.RecordRevision || lastState[id] != record.State {
				return ErrStoreCorrupt
			}
		}

		if err := tx.Bucket(bucketIdempotency).ForEach(func(_, value []byte) error {
			var receipt idempotencyRecord
			if err := strictUnmarshal(value, &receipt); err != nil || !validDigest(receipt.Fingerprint) || receipt.Kind != "create" && receipt.Kind != "transition" && receipt.Kind != "admission" {
				return ErrStoreCorrupt
			}
			report.Idempotency++
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket(bucketImports).ForEach(func(_, value []byte) error {
			if len(value) == 0 {
				return ErrStoreCorrupt
			}
			report.ImportReceipts++
			return nil
		}); err != nil {
			return err
		}
		return nil
	})
	return report, classifyStoreError(err)
}

func strictUnmarshal(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrStoreCorrupt
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ErrStoreCorrupt
	}
	return nil
}
