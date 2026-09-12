package beads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var _ DeterministicCreator = (*SQLiteStore)(nil)

// CreateDeterministic uses the deterministic ID as SQLite's unique key and
// performs the create-or-adopt decision inside one transaction.
func (s *SQLiteStore) CreateDeterministic(key string, b Bead) (Bead, bool, error) {
	if err := s.ensureOpen(); err != nil {
		return Bead{}, false, err
	}
	want, err := deterministicCreateRequest(s.prefix, key, b)
	if err != nil {
		return Bead{}, false, err
	}
	if err := s.checkPinnedIDNamespace(want.ID); err != nil {
		return Bead{}, false, err
	}

	var out Bead
	var inserted bool
	err = retryOnBusy(func() error {
		ctx := context.Background()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("sqlite deterministic create: begin tx: %w", err)
		}
		defer tx.Rollback() //nolint:errcheck

		row := tx.QueryRowContext(ctx, `SELECT `+s.sqliteBeadProjection()+` FROM beads b WHERE b.id=?`, want.ID)
		existing, err := scanSQLiteBead(row)
		switch {
		case err == nil:
			if !sameDeterministicCreateTuple(existing, want) {
				return deterministicCreateConflict(want.ID)
			}
			out = existing
			inserted = false
		case errors.Is(err, sql.ErrNoRows):
			out = s.normalizeCreate(want)
			if err := s.clearClaimFenceTx(ctx, tx, out.ID); err != nil {
				return err
			}
			if err := s.upsertBeadTx(ctx, tx, out); err != nil {
				return err
			}
			inserted = true
		default:
			return fmt.Errorf("sqlite deterministic create %s: %w", want.ID, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite deterministic create: commit: %w", err)
		}
		return nil
	})
	if err != nil {
		return Bead{}, false, err
	}
	return cloneBead(out), inserted, nil
}
