package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrClaimLeaseUnsupported reports that a store does not expose the native
// claim lease primitives required by the controller lease reconciler.
var ErrClaimLeaseUnsupported = errors.New("native claim lease primitives unsupported")

// LeaseCommandRunner runs a native lease command with the holder identity that
// owns the claim. The holder is supplied separately from argv so it cannot be
// mistaken for a bead id or accidentally appear in operator-facing output.
type LeaseCommandRunner func(ctx context.Context, dir, holder string, args ...string) ([]byte, error)

// WithBdStoreLeaseRunner installs the native lease command runner for a
// BdStore. Ordinary BdStore commands continue to use the CommandRunner passed
// to NewBdStore; this option exists because BEADS_ACTOR is owner-specific and
// must be projected per heartbeat.
func WithBdStoreLeaseRunner(runner LeaseCommandRunner) BdStoreOption {
	return func(s *BdStore) {
		s.leaseRunner = runner
	}
}

// ClaimLeaseOwnerStore provides the bounded reads required to establish current
// and terminal session ownership. It is separate from ClaimLeaseStore because a
// relocated session-class store carries durable owner rows but no native lease
// table of its own.
type ClaimLeaseOwnerStore interface {
	// ListOpenSessionRows returns the current non-closed rows from which the
	// controller builds its session-owner census. The beads package deliberately
	// does not classify session rows, avoiding a package cycle with session.
	ListOpenSessionRows(ctx context.Context) ([]Bead, error)
	// GetLeaseRow resolves an exact persisted row, including closed history.
	// Reconciliation uses it for both terminal-owner and final claim fencing.
	GetLeaseRow(ctx context.Context, id string) (Bead, error)
}

// ClaimLeaseStore exposes the native, node-local claim lease operations. The
// interface is deliberately optional: backends that do not implement bd's
// ephemeral lease table must fail closed rather than emulate it with metadata.
type ClaimLeaseStore interface {
	ClaimLeaseOwnerStore
	// ListInProgressClaims returns the current durable and ephemeral rows whose
	// stored status is in_progress. The caller applies claim identity checks;
	// this read exists here so the same bounded native runner owns the complete
	// lease patrol rather than falling back to context-free Store.List.
	ListInProgressClaims(ctx context.Context) ([]Bead, error)
	HeartbeatClaim(ctx context.Context, id, holder string) error
	ReclaimExpiredClaims(ctx context.Context, olderThan time.Duration, scope ClaimLeaseReclaimScope) (int, error)
}

// ClaimLeaseReclaimScope is an AND-combined destructive boundary. Callers must
// provide both exact claim IDs and their fully validated assignees; an empty
// side is rejected rather than widening reclaim to every local stale lease.
type ClaimLeaseReclaimScope struct {
	ClaimIDs  []string
	Assignees []string
}

var _ ClaimLeaseStore = (*BdStore)(nil)

// ListInProgressClaims returns the current in-progress rows through the
// context-bound native lease runner.
func (s *BdStore) ListInProgressClaims(ctx context.Context) ([]Bead, error) {
	return s.listLeaseRows(ctx, "--status", "in_progress")
}

// ListOpenSessionRows returns current open and in-progress rows for session
// ownership classification by the controller.
func (s *BdStore) ListOpenSessionRows(ctx context.Context) ([]Bead, error) {
	return s.listLeaseRows(ctx, "--status", "open,in_progress")
}

func (s *BdStore) listLeaseRows(ctx context.Context, filters ...string) ([]Bead, error) {
	if s == nil || s.leaseRunner == nil {
		return nil, ErrClaimLeaseUnsupported
	}
	args := []string{"list", "--json", "--include-infra", "--include-gates", "--include-templates", "--limit", "0"}
	args = append(args, filters...)
	out, err := s.leaseRunner(ctx, s.dir, "", s.bdTransientWriteArgs(args)...)
	if err != nil {
		return nil, fmt.Errorf("listing claim lease rows: %w", err)
	}
	issues, err := parseIssuesTolerant(extractJSON(out))
	if err != nil {
		return nil, fmt.Errorf("listing claim lease rows: %w", err)
	}
	rows := make([]Bead, 0, len(issues))
	for i := range issues {
		rows = append(rows, issues[i].toBead())
	}
	return rows, nil
}

// GetLeaseRow resolves one exact persisted row, including closed rows, through
// the context-bound native lease runner.
func (s *BdStore) GetLeaseRow(ctx context.Context, id string) (Bead, error) {
	if s == nil || s.leaseRunner == nil {
		return Bead{}, ErrClaimLeaseUnsupported
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Bead{}, errors.New("getting claim lease row: empty bead id")
	}
	out, err := s.leaseRunner(ctx, s.dir, "", s.bdTransientWriteArgs([]string{"show", "--json", id})...)
	if err != nil {
		if isBdNotFound(err) {
			return Bead{}, fmt.Errorf("getting claim lease row %q: %w", id, ErrNotFound)
		}
		return Bead{}, fmt.Errorf("getting claim lease row %q: %w", id, err)
	}
	issues, err := parseIssuesTolerant(extractJSON(out))
	if err != nil {
		return Bead{}, fmt.Errorf("getting claim lease row %q: %w", id, err)
	}
	if len(issues) == 0 {
		return Bead{}, fmt.Errorf("getting claim lease row %q: %w", id, ErrNotFound)
	}
	bead := issues[0].toBead()
	if bead.ID != id {
		return Bead{}, fmt.Errorf("getting claim owner %q (resolved to %q): %w", id, bead.ID, ErrIDCollision)
	}
	return bead, nil
}

// HeartbeatClaim refreshes a claim through bd's native heartbeat primitive.
// The holder must be the exact assignee that acquired the claim; the native
// command rejects a different or expired owner.
func (s *BdStore) HeartbeatClaim(ctx context.Context, id, holder string) error {
	if s == nil || s.leaseRunner == nil {
		return ErrClaimLeaseUnsupported
	}
	id = strings.TrimSpace(id)
	holder = strings.TrimSpace(holder)
	if id == "" {
		return errors.New("heartbeat claim: empty bead id")
	}
	if holder == "" {
		return errors.New("heartbeat claim: empty holder")
	}
	_, err := s.leaseRunner(ctx, s.dir, holder, s.bdTransientWriteArgs([]string{"heartbeat", id, "--json"})...)
	if err != nil {
		return fmt.Errorf("heartbeat claim %q: %w", id, err)
	}
	return nil
}

// ReclaimExpiredClaims invokes bd's native local-replica reclaim operation.
// It never passes --any-replica: a controller may only reclaim leases granted
// by this store's local replica. olderThan is the native grace threshold, not
// a controller-side timestamp heuristic.
func (s *BdStore) ReclaimExpiredClaims(ctx context.Context, olderThan time.Duration, scope ClaimLeaseReclaimScope) (int, error) {
	if s == nil || s.leaseRunner == nil {
		return 0, ErrClaimLeaseUnsupported
	}
	if olderThan <= 0 {
		return 0, errors.New("reclaim expired claims: grace must be positive")
	}
	claimIDs := normalizedStrings(scope.ClaimIDs)
	assignees := normalizedStrings(scope.Assignees)
	if len(claimIDs) == 0 || len(assignees) == 0 {
		return 0, errors.New("reclaim expired claims: exact claim ids and assignees are required")
	}
	args := []string{"reclaim", "--older-than", olderThan.String()}
	for _, assignee := range assignees {
		args = append(args, "--assignee", assignee)
	}
	for _, id := range claimIDs {
		args = append(args, "--id", id)
	}
	args = append(args, "--json")
	out, err := s.leaseRunner(ctx, s.dir, "", s.bdTransientWriteArgs(args)...)
	if err != nil {
		return 0, fmt.Errorf("reclaim expired claims: %w", err)
	}
	count, err := reclaimCount(out)
	if err != nil {
		return 0, fmt.Errorf("reclaim expired claims: %w", err)
	}
	return count, nil
}

func normalizedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// reclaimCount accepts the JSON shapes used by bd releases for reclaim: a
// result array, a named array, or a numeric count. Unknown successful output
// is an error rather than an invented zero, so diagnostics never claim that a
// reclaim pass was complete when its result could not be decoded.
func reclaimCount(out []byte) (int, error) {
	data := bytes.TrimSpace(extractJSON(out))
	if len(data) == 0 {
		return 0, nil
	}
	if data[0] == '[' {
		var rows []json.RawMessage
		if err := json.Unmarshal(data, &rows); err != nil {
			return 0, err
		}
		return len(rows), nil
	}
	if data[0] != '{' {
		return 0, errors.New("unexpected JSON result")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, err
	}
	for _, key := range []string{"reclaimed", "reclaimed_count", "count"} {
		raw, ok := result[key]
		if !ok {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			// bd emits reclaimed:null when a scoped reclaim finds no rows;
			// continue to the count field instead of treating valid output as
			// an undecodable result.
			continue
		}
		var count int
		if err := json.Unmarshal(raw, &count); err == nil {
			return count, nil
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err == nil {
			return len(rows), nil
		}
		return 0, fmt.Errorf("field %q is neither a count nor an array", key)
	}
	for _, key := range []string{"issues", "beads", "data"} {
		raw, ok := result[key]
		if !ok {
			continue
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return 0, fmt.Errorf("field %q is not an array: %w", key, err)
		}
		return len(rows), nil
	}
	return 0, errors.New("JSON result has no reclaim count")
}
