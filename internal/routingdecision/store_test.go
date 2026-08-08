package routingdecision

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func openTestStore(t *testing.T, now time.Time) *Store {
	t.Helper()
	cityRoot := t.TempDir()
	store, err := OpenStore(cityRoot, StoreOptions{Now: func() time.Time { return now }, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func TestOpenStoreCreatesExactPrivateSchemaAndRejectsSymlink(t *testing.T) {
	cityRoot := t.TempDir()
	store, err := OpenStore(cityRoot, StoreOptions{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	path := filepath.Join(cityRoot, StoreRelativePath)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("store mode/type = %v, want regular 0600", info.Mode())
	}
	if err := store.db.View(func(tx *boltTx) error {
		for _, name := range requiredBucketNames {
			if tx.Bucket(name) == nil {
				t.Fatalf("missing bucket %q", name)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	linkCity := t.TempDir()
	if err := os.Mkdir(filepath.Join(linkCity, ".gc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(linkCity, StoreRelativePath)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(linkCity, StoreOptions{Timeout: 50 * time.Millisecond}); err == nil {
		t.Fatal("OpenStore accepted symlinked database")
	}
}

func TestOpenStoreRefusesLockAndFutureSchema(t *testing.T) {
	cityRoot := t.TempDir()
	first, err := OpenStore(cityRoot, StoreOptions{Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(cityRoot, StoreOptions{Timeout: 20 * time.Millisecond}); !errors.Is(err, ErrStoreLocked) {
		t.Fatalf("second OpenStore error = %v, want ErrStoreLocked", err)
	}
	if err := first.db.Update(func(tx *boltTx) error {
		return tx.Bucket(bucketMeta).Put(keySchemaVersion, encodeUint64(SchemaVersion+1))
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(cityRoot, StoreOptions{Timeout: 20 * time.Millisecond}); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("future schema error = %v, want ErrUnsupportedSchema", err)
	}
}

func TestStoreLifecycleCASIdempotencyAndActiveIndex(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(map[string]ed25519.PublicKey{"board": publicKey})

	payload := testDecisionPayload(t)
	payload.CreatedAt = now.Add(-time.Minute)
	payload.ExpiresAt = now.Add(time.Minute)
	payload.BindingID = BindingID(payload)
	record, err := store.Create(payload, "create-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if record.State != StateProposed || record.RecordRevision != 1 || record.StoreRevision != 1 {
		t.Fatalf("created record = %+v", record)
	}
	replayed, err := store.Create(payload, "create-1")
	if err != nil || !reflect.DeepEqual(replayed, record) {
		t.Fatalf("idempotent Create = (%+v, %v), want original", replayed, err)
	}
	conflict := payload
	conflict.DecisionID = "decision-conflict"
	conflict.BindingID = BindingID(conflict)
	if _, err := store.Create(conflict, "create-1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("token conflict error = %v", err)
	}

	approval := ApprovalPayload{Schema: SchemaVersion, DecisionID: payload.DecisionID, BindingID: payload.BindingID, AuthorityID: "board", ApprovedAt: now}
	signing, err := SigningBytes(payload, approval)
	if err != nil {
		t.Fatal(err)
	}
	signature := Signature{Algorithm: SignatureAlgorithmEd25519, AuthorityID: "board", Value: ed25519.Sign(privateKey, signing)}
	receipt, err := store.Transition(TransitionRequest{
		DecisionID: payload.DecisionID, ExpectedRevision: 1, From: StateProposed, To: StateApproved,
		Approval: &approval, Signature: &signature, IdempotencyToken: "approve-1", Reason: "approved",
	}, verifier)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if receipt.State != StateApproved || receipt.RecordRevision != 2 || receipt.StoreRevision != 2 {
		t.Fatalf("approval receipt = %+v", receipt)
	}
	if _, err := store.Transition(TransitionRequest{
		DecisionID: payload.DecisionID, ExpectedRevision: 1, From: StateApproved, To: StateRevoked,
		IdempotencyToken: "stale", Reason: "stale",
	}, Verifier{}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale transition error = %v", err)
	}

	active, err := store.ActiveApproved(now, 10, verifier)
	if err != nil {
		t.Fatalf("ActiveApproved: %v", err)
	}
	if len(active) != 1 || active[0].Payload.DecisionID != payload.DecisionID {
		t.Fatalf("active records = %+v", active)
	}
	if _, err := store.Transition(TransitionRequest{
		DecisionID: payload.DecisionID, ExpectedRevision: 2, From: StateApproved, To: StateProposed,
		IdempotencyToken: "forbidden", Reason: "rewind",
	}, Verifier{}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("forbidden transition error = %v", err)
	}
}

func TestStoreAllowedTransitionGraphAndExpireDue(t *testing.T) {
	wantAllowed := map[[2]State]bool{
		{StateProposed, StateApproved}:         true,
		{StateProposed, StateRevoked}:          true,
		{StateProposed, StateExpired}:          true,
		{StateApproved, StateAdmitted}:         true,
		{StateApproved, StateRefusedAfterRace}: true,
		{StateApproved, StateRevoked}:          true,
		{StateApproved, StateExpired}:          true,
		{StateAdmitted, StateClaimed}:          true,
		{StateAdmitted, StateOutcomeRecorded}:  true,
	}
	for _, from := range AllStates() {
		for _, to := range AllStates() {
			if got := IsAllowedTransition(from, to); got != wantAllowed[[2]State{from, to}] {
				t.Fatalf("IsAllowedTransition(%q, %q) = %t", from, to, got)
			}
		}
	}

	now := time.Date(2026, 8, 7, 21, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload := testDecisionPayload(t)
	payload.CreatedAt = now.Add(-2 * time.Minute)
	payload.ExpiresAt = now.Add(-time.Minute)
	payload.BindingID = BindingID(payload)
	if _, err := store.Create(payload, "create-expired"); err != nil {
		t.Fatal(err)
	}
	expired, err := store.ExpireDue(now, 1, func(id string) string { return "expire-" + id })
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	record, err := store.Get(payload.DecisionID)
	if err != nil || record.State != StateExpired {
		t.Fatalf("expired record = (%+v, %v)", record, err)
	}
}
