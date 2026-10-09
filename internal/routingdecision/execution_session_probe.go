package routingdecision

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	bbolt "go.etcd.io/bbolt"
)

// ExecutionSessionBound is a read-only offline safety probe for callers lacking
// the controller-owned launch adapter. No ledger is created or initialized.
// An existing locked/corrupt ledger is unknown, NOT absence of authorization;
// callers must deny rather than downgrade to legacy. The ordinary controller
// uses its already-open Store instead and never needs this fallback probe.
func ExecutionSessionBound(cityRoot, id string) (bool, error) {
	if cityRoot == "" {
		return false, nil
	}
	path := filepath.Join(cityRoot, StoreRelativePath)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		return false, ErrStoreCorrupt
	}
	stateInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil || !stateInfo.IsDir() || stateInfo.Mode()&os.ModeSymlink != 0 {
		return false, ErrStoreCorrupt
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{ReadOnly: true, Timeout: 10 * time.Millisecond, OpenFile: openBoltFile})
	if err != nil {
		return false, ErrStoreLocked
	}
	defer db.Close() //nolint:errcheck
	if err := db.View(func(tx *bbolt.Tx) error {
		for _, name := range legacyRequiredBucketNames {
			if tx.Bucket(name) == nil {
				return ErrStoreCorrupt
			}
		}
		meta := tx.Bucket(bucketMeta)
		schema, ok := decodeUint64(meta.Get(keySchemaVersion))
		if !ok {
			return ErrStoreCorrupt
		}
		if schema != SchemaVersion {
			return ErrUnsupportedSchema
		}
		if _, ok := decodeUint64(meta.Get(keyStoreRevision)); !ok {
			return ErrStoreCorrupt
		}
		return nil
	}); err != nil {
		return false, err
	}
	store := &Store{db: db}
	auth, err := store.ExecutionSession(id)
	return auth != nil, err
}
