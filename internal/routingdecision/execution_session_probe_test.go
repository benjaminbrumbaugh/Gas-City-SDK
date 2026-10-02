package routingdecision

import (
	"os"
	"path/filepath"
	"testing"

	bbolt "go.etcd.io/bbolt"
)

func TestExecutionSessionProbeUnknownLedgerFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, StoreRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if bound, err := ExecutionSessionBound(root, "session"); err == nil {
		t.Fatalf("unknown ledger classified as absent: bound=%v", bound)
	}
}
