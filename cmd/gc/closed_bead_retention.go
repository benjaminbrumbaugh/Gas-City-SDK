package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
)

// closedBeadRetentionPolicyName is the [beads.policies.<name>] entry that ages
// out closed durable beads. It is opt-in: with no delete_after_close the
// sweep never runs. Wisp GC owns ephemeral beads and the order-tracking
// watchdog owns tracking rows; this policy covers everything else that closes
// and is never referenced again — session beads, finished tasks, closed bugs.
const closedBeadRetentionPolicyName = "closed_durable"

// closedBeadRetentionEnforceEnv is the environment override that switches
// the sweep from dry-run to enforcement, mirroring GC_WISP_GC_REAP_ORPHANS.
// The durable switch is the policy's enforce field; either one deletes.
const closedBeadRetentionEnforceEnv = "GC_CLOSED_BEAD_RETENTION_ENFORCE"

const (
	// closedBeadRetentionWatchdogInterval bounds how often the sweep runs.
	closedBeadRetentionWatchdogInterval = time.Hour
	// closedBeadRetentionWatchdogDeleteBudget caps deletions per run so a
	// first-deploy backlog of thousands of closed session beads drains across
	// successive hours instead of one long tick.
	closedBeadRetentionWatchdogDeleteBudget = 500
)

// closedBeadRetentionEnforced reports whether the process environment opts
// the sweep into deletion. Package var so tests flip it without touching the
// process environment.
var closedBeadRetentionEnforced = func() bool {
	return parseBoolEnv(os.Getenv(closedBeadRetentionEnforceEnv))
}

// closedBeadRetentionEnforcedFor reports whether the sweep deletes (true) or
// runs dry (false): the closed_durable policy's enforce field or the
// environment override, either one. A nil config never enforces because the
// policy that scopes the deletion is unknown.
func closedBeadRetentionEnforcedFor(cfg *config.City) bool {
	if cfg == nil {
		return false
	}
	if policy, ok := cfg.Beads.Policies[closedBeadRetentionPolicyName]; ok && policy.Enforce {
		return true
	}
	return closedBeadRetentionEnforced()
}

// closedBeadRetentionTTLForConfig returns the configured delete_after_close
// for the closed_durable policy, or 0 when the policy is absent or invalid
// (disabled).
func closedBeadRetentionTTLForConfig(cfg *config.City) time.Duration {
	if cfg == nil {
		return 0
	}
	policy, ok := cfg.Beads.Policies[closedBeadRetentionPolicyName]
	if !ok {
		return 0
	}
	return policy.DeleteAfterCloseDuration()
}

// closedBeadRetentionResult summarizes one sweep of one store.
type closedBeadRetentionResult struct {
	// eligible counts closed durable beads past the TTL with no link to
	// non-closed work — what enforcement would delete, budget permitting.
	eligible int
	// protected counts aged closed beads kept because a dependency edge or
	// parent link ties them to a bead that is not closed.
	protected int
	// deleted counts beads actually removed (0 in dry-run).
	deleted int
}

// sweepClosedBeadRetention deletes (or, when enforce is false, counts) closed
// durable beads whose last update is older than ttl and which have no
// dependency edge in either direction to a non-closed bead and no non-closed
// parent. Ephemeral beads are excluded (wisp GC owns them). Deletion is
// bounded by budget and ordered oldest-first so repeated runs converge.
//
// The link check is the whole of the policy: a closed bead that open work
// still points at (or that still points at open work) is context that work
// may read, so it stays until that work closes too.
func sweepClosedBeadRetention(store beads.Store, now time.Time, ttl time.Duration, budget int, enforce bool) (closedBeadRetentionResult, error) {
	var result closedBeadRetentionResult
	if store == nil {
		return result, fmt.Errorf("bead store unavailable")
	}
	if ttl <= 0 || budget <= 0 {
		return result, nil
	}
	cutoff := now.Add(-ttl)
	live := beads.HandlesFor(store).Live

	candidates, err := live.List(beads.ListQuery{
		Status:        "closed",
		UpdatedBefore: cutoff,
		TierMode:      beads.TierIssues,
		Sort:          beads.SortCreatedAsc,
	})
	if err != nil {
		return result, fmt.Errorf("listing closed beads for retention: %w", err)
	}
	if len(candidates) == 0 {
		return result, nil
	}

	notClosed, err := live.List(beads.ListQuery{AllowScan: true})
	if err != nil {
		return result, fmt.Errorf("listing open beads for retention protection: %w", err)
	}
	openIDs := make(map[string]struct{}, len(notClosed))
	linkedFromOpen := make(map[string]struct{})
	for _, b := range notClosed {
		openIDs[b.ID] = struct{}{}
		for _, dep := range b.Dependencies {
			linkedFromOpen[dep.DependsOnID] = struct{}{}
		}
	}

	eligible := make([]beads.Bead, 0, len(candidates))
	for _, b := range candidates {
		if b.Ephemeral {
			continue
		}
		if closedBeadLinkedToOpenWork(b, openIDs, linkedFromOpen) {
			result.protected++
			continue
		}
		eligible = append(eligible, b)
	}
	result.eligible = len(eligible)
	if !enforce || len(eligible) == 0 {
		return result, nil
	}

	sort.SliceStable(eligible, func(i, j int) bool {
		return beadUpdatedReferenceTimeForRetention(eligible[i]).Before(beadUpdatedReferenceTimeForRetention(eligible[j]))
	})
	if len(eligible) > budget {
		eligible = eligible[:budget]
	}
	ids := make([]string, 0, len(eligible))
	for _, b := range eligible {
		ids = append(ids, b.ID)
	}
	if err := deleteWorkflowBeadsBatch(store, ids); err != nil {
		var batchErr *beads.BatchDeleteError
		if errors.As(err, &batchErr) {
			result.deleted = len(batchErr.Committed)
		}
		return result, fmt.Errorf("deleting closed beads for retention: %w", err)
	}
	result.deleted = len(ids)
	return result, nil
}

// closedBeadLinkedToOpenWork reports whether closed bead b has a parent or a
// dependency edge (either direction) to a bead that is not closed.
func closedBeadLinkedToOpenWork(b beads.Bead, openIDs, linkedFromOpen map[string]struct{}) bool {
	if b.ParentID != "" {
		if _, open := openIDs[b.ParentID]; open {
			return true
		}
	}
	if _, cited := linkedFromOpen[b.ID]; cited {
		return true
	}
	for _, dep := range b.Dependencies {
		if _, open := openIDs[dep.DependsOnID]; open {
			return true
		}
	}
	return false
}

func beadUpdatedReferenceTimeForRetention(b beads.Bead) time.Time {
	if !b.UpdatedAt.IsZero() {
		return b.UpdatedAt
	}
	return b.CreatedAt
}

// runClosedBeadRetentionWatchdog ages out closed durable beads per the
// closed_durable policy at most once per closedBeadRetentionWatchdogInterval.
// It is silent when no policy is configured, reports a dry-run advisory
// unless closedBeadRetentionEnforceEnv is truthy, and — like the
// order-tracking watchdog — refuses bulk deletion while the managed backup is
// stale so a bad sweep is always recoverable.
func (cr *CityRuntime) runClosedBeadRetentionWatchdog(now time.Time) {
	ttl := closedBeadRetentionTTLForConfig(cr.cfg)
	if ttl <= 0 {
		return
	}
	if !cr.closedBeadRetentionWatchdogLast.IsZero() &&
		now.Sub(cr.closedBeadRetentionWatchdogLast) < closedBeadRetentionWatchdogInterval {
		return
	}
	cr.closedBeadRetentionWatchdogLast = now

	enforce := closedBeadRetentionEnforcedFor(cr.cfg)
	if enforce && cr.cityPath != "" {
		if safe, reason := doctor.BulkDeleteSafe(cr.cityPath, cr.cfg, bulkDeleteMaxAge(cr.cfg), now); !safe {
			cr.logClosedBeadRetention("skipping bulk delete — %s", reason)
			return
		}
	}

	stores, _, closeOpened, storeErr := cr.orderTrackingSweepStores()
	defer closeOpened()
	if len(stores) == 0 {
		if storeErr != nil {
			cr.logClosedBeadRetention("%v", storeErr)
		}
		return
	}

	var total closedBeadRetentionResult
	var errs error
	for i, store := range stores {
		result, err := sweepClosedBeadRetention(store, now, ttl, closedBeadRetentionWatchdogDeleteBudget-total.deleted, enforce)
		total.eligible += result.eligible
		total.protected += result.protected
		total.deleted += result.deleted
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("%s: %w", orderTrackingSweepStoreLabel(store, i), err))
		}
		if total.deleted >= closedBeadRetentionWatchdogDeleteBudget {
			break
		}
	}
	if err := errors.Join(storeErr, errs); err != nil {
		cr.logClosedBeadRetention("%v", err)
	}
	switch {
	case !enforce && total.eligible > 0:
		cr.logClosedBeadRetention("dry-run: %d closed bead(s) past %s with no open links would be deleted (%d protected); set [beads.policies.%s] enforce = true (or %s=1) to enforce",
			total.eligible, cr.cfg.Beads.Policies[closedBeadRetentionPolicyName].DeleteAfterClose, total.protected, closedBeadRetentionPolicyName, closedBeadRetentionEnforceEnv)
	case total.deleted > 0:
		cr.logClosedBeadRetention("pruned %d closed bead(s) (%d protected by open links)", total.deleted, total.protected)
	}
}

func (cr *CityRuntime) logClosedBeadRetention(format string, args ...any) {
	if cr.stderr == nil {
		return
	}
	fmt.Fprintf(cr.stderr, "%s: closed-bead retention: %s\n", cr.logPrefix, fmt.Sprintf(format, args...)) //nolint:errcheck // best-effort stderr
}
