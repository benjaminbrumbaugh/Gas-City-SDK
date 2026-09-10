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

// ClaimLeaseStore exposes the native, node-local claim lease operations. The
// interface is deliberately optional: backends that do not implement bd's
// ephemeral lease table must fail closed rather than emulate it with metadata.
type ClaimLeaseStore interface {
	HeartbeatClaim(ctx context.Context, id, holder string) error
	ReclaimExpiredClaims(ctx context.Context, olderThan time.Duration, assignees ...string) (int, error)
}

var _ ClaimLeaseStore = (*BdStore)(nil)

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
func (s *BdStore) ReclaimExpiredClaims(ctx context.Context, olderThan time.Duration, assignees ...string) (int, error) {
	if s == nil || s.leaseRunner == nil {
		return 0, ErrClaimLeaseUnsupported
	}
	if olderThan <= 0 {
		return 0, errors.New("reclaim expired claims: grace must be positive")
	}
	args := []string{"reclaim", "--older-than", olderThan.String()}
	for _, assignee := range normalizedAssignees(assignees) {
		args = append(args, "--assignee", assignee)
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

func normalizedAssignees(assignees []string) []string {
	seen := make(map[string]struct{}, len(assignees))
	result := make([]string, 0, len(assignees))
	for _, assignee := range assignees {
		assignee = strings.TrimSpace(assignee)
		if assignee == "" {
			continue
		}
		if _, exists := seen[assignee]; exists {
			continue
		}
		seen[assignee] = struct{}{}
		result = append(result, assignee)
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
