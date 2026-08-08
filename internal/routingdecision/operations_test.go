package routingdecision

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreBackupExportAndVerify(t *testing.T) {
	now := time.Date(2026, 8, 7, 22, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload := testDecisionPayload(t)
	payload.CreatedAt = now.Add(-time.Minute)
	payload.ExpiresAt = now.Add(time.Minute)
	payload.BindingID = BindingID(payload)
	if _, err := store.Create(payload, "create-operations"); err != nil {
		t.Fatal(err)
	}

	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(map[string]ed25519.PublicKey{"unused": publicKey})
	report, err := store.Verify(verifier)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Decisions != 1 || report.Transitions != 1 || report.StoreRevision != 1 {
		t.Fatalf("verify report = %+v", report)
	}

	var first, second bytes.Buffer
	if err := store.Export(&first); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := store.Export(&second); err != nil {
		t.Fatalf("Export again: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("export is nondeterministic:\n%s\n%s", first.Bytes(), second.Bytes())
	}
	if bytes.Contains(first.Bytes(), []byte("private_key")) {
		t.Fatalf("export leaked key material: %s", first.Bytes())
	}

	backupPath := filepath.Join(t.TempDir(), "decision-backup.db")
	if err := store.Backup(backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	info, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("backup mode/type = %v", info.Mode())
	}
	if err := store.Backup(backupPath); err == nil {
		t.Fatal("Backup overwrote existing destination")
	}
}

func TestStoreVerifyRejectsIndexTamper(t *testing.T) {
	now := time.Date(2026, 8, 7, 22, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload := testDecisionPayload(t)
	payload.BindingID = BindingID(payload)
	record, err := store.Create(payload, "create-tamper")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Update(func(tx *boltTx) error {
		return tx.Bucket(bucketStateExpiry).Delete(stateIndexKey(record))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(Verifier{}); !errors.Is(err, ErrStoreCorrupt) {
		t.Fatalf("Verify tampered index error = %v, want ErrStoreCorrupt", err)
	}
}

func TestStoreVerifyRejectsStoredApprovalTamper(t *testing.T) {
	now := time.Date(2026, 8, 7, 22, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	record, verifier := approvedTestRecord(t, store, now)
	if _, err := store.Verify(verifier); err != nil {
		t.Fatalf("Verify authentic approval: %v", err)
	}
	if err := store.db.Update(func(tx *boltTx) error {
		value := tx.Bucket(bucketDecisions).Get([]byte(record.Payload.DecisionID))
		var tampered Record
		if err := strictUnmarshal(value, &tampered); err != nil {
			return err
		}
		tampered.Signature.Value[0] ^= 0xff
		return putJSON(tx.Bucket(bucketDecisions), []byte(record.Payload.DecisionID), tampered)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(verifier); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify tampered approval error = %v, want ErrInvalidSignature", err)
	}
}
