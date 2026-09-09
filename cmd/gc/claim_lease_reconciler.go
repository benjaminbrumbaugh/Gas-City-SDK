package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

const (
	// claimLeaseReconcileInterval is deliberately below bd's five-minute claim
	// TTL. A controller that misses one patrol still has another bounded window
	// to refresh a live claim before native reclaim's grace begins.
	claimLeaseReconcileInterval = time.Minute
	// claimLeaseReclaimGrace is the native reclaim grace (approximately twice
	// the five-minute claim TTL). It is passed to bd rather than reimplemented
	// from wall-clock fields, because lease state is node-local to bd.
	claimLeaseReclaimGrace = 10 * time.Minute
)

type claimLeaseScope struct {
	Name  string
	Store beads.Store
	Lease beads.ClaimLeaseStore
}

type claimLeaseStoreReport struct {
	Name      string
	Renewed   int
	Reclaimed int
	Errors    int
}

type claimLeaseReconcileResult struct {
	LastAttemptAt    time.Time
	LastSuccessfulAt time.Time
	Stores           []claimLeaseStoreReport
}

// reconcileClaimLeases refreshes native claim leases for exact live session
// owners, then asks each readable scope's local replica to reclaim expired
// claims. The caller supplies scopes explicitly so suspended and unregistered
// rig stores cannot enter the operation through a broad filesystem scan.
//
// A store read is a safety gate: when it fails, neither heartbeat nor reclaim
// is attempted for that scope. An absent session id is treated as an abandoned
// claim and left for native reclaim; a present but fenced session is reported
// as an identity error and is not heartbeated.
func reconcileClaimLeases(ctx context.Context, provider runtime.Provider, infos []session.Info, scopes []claimLeaseScope, now time.Time) claimLeaseReconcileResult {
	result := claimLeaseReconcileResult{LastAttemptAt: now.UTC()}
	bySessionID := make(map[string]session.Info, len(infos))
	for _, info := range infos {
		if id := strings.TrimSpace(info.ID); id != "" {
			bySessionID[id] = info
		}
	}

	allComplete := true
	for _, scope := range scopes {
		report := claimLeaseStoreReport{Name: scope.Name}
		result.Stores = append(result.Stores, report)
		current := &result.Stores[len(result.Stores)-1]
		if err := ctx.Err(); err != nil {
			current.Errors++
			allComplete = false
			continue
		}
		if scope.Store == nil || scope.Lease == nil {
			current.Errors++
			allComplete = false
			continue
		}

		claims, err := scope.Store.List(beads.ListQuery{
			Status:   "in_progress",
			TierMode: beads.TierBoth,
			Live:     true,
		})
		if err != nil {
			current.Errors++
			allComplete = false
			continue
		}

		claimed := false
		assignees := make(map[string]struct{})
		reclaimAllowed := true
		for _, claim := range claims {
			assignee := strings.TrimSpace(claim.Assignee)
			if assignee == "" {
				// Native bd claims have an assignee. A coordination row that
				// is merely in_progress is not a controller lease candidate.
				continue
			}
			claimed = true
			assignees[assignee] = struct{}{}
			sessionID := strings.TrimSpace(claim.Metadata[beadmeta.SessionIDMetadataKey])
			if sessionID == "" {
				// There is no exact owner to renew; native reclaim may recover
				// this abandoned claim after the local grace period.
				continue
			}
			info, exists := bySessionID[sessionID]
			if !exists {
				continue
			}
			live, reason, safeToReclaim := claimLeaseOwnerStatus(provider, info)
			if !live {
				reclaimAllowed = reclaimAllowed && safeToReclaim
				if reason != "" {
					current.Errors++
					allComplete = false
				}
				continue
			}
			if !claimLeaseAssigneeMatches(info, assignee) {
				reclaimAllowed = false
				current.Errors++
				allComplete = false
				continue
			}
			if err := scope.Lease.HeartbeatClaim(claim.ID, assignee); err != nil {
				reclaimAllowed = false
				current.Errors++
				allComplete = false
				continue
			}
			current.Renewed++
		}
		if !claimed {
			continue
		}
		if !reclaimAllowed {
			continue
		}
		assigneeList := make([]string, 0, len(assignees))
		for assignee := range assignees {
			assigneeList = append(assigneeList, assignee)
		}
		sort.Strings(assigneeList)
		reclaimed, err := scope.Lease.ReclaimExpiredClaims(claimLeaseReclaimGrace, assigneeList...)
		if err != nil {
			current.Errors++
			allComplete = false
			continue
		}
		current.Reclaimed = reclaimed
	}
	if allComplete && len(result.Stores) > 0 {
		result.LastSuccessfulAt = now.UTC()
	}
	return result
}

// claimLeaseOwnerStatus proves whether the session is the exact live owner by
// checking the persisted session id, exact runtime session name, runtime
// session id, persisted instance token, live provider runtime, and provider
// token.
// reason is non-empty only when a session record exists but cannot be proven to
// be the current live owner; safeToReclaim says whether that conclusion is
// sufficiently fenced to permit native reclaim. A provider error is not safe
// to interpret as abandonment. A draining session with matching runtime
// identity is also not safe to reclaim: it remains the exact owner even though
// its lease is intentionally not renewed.
func claimLeaseOwnerStatus(provider runtime.Provider, info session.Info) (live bool, reason string, safeToReclaim bool) {
	if provider == nil {
		return false, "provider unavailable", false
	}
	if info.Closed {
		return false, "session is closed", true
	}
	name := strings.TrimSpace(info.SessionNameMetadata)
	if name == "" {
		return false, "session name is not persisted", false
	}
	if !provider.IsRunning(name) {
		return false, "provider runtime is not running", true
	}
	runtimeID, err := provider.GetMeta(name, "GC_SESSION_ID")
	if err != nil {
		return false, "provider session identity unavailable", false
	}
	runtimeID = strings.TrimSpace(runtimeID)
	if runtimeID == "" {
		return false, "provider session identity is fenced", false
	}
	if runtimeID != strings.TrimSpace(info.ID) {
		return false, "provider session identity is fenced", true
	}
	token, err := provider.GetMeta(name, "GC_INSTANCE_TOKEN")
	if err != nil {
		return false, "provider instance token unavailable", false
	}
	if strings.TrimSpace(token) == "" {
		return false, "provider instance token is fenced", false
	}
	token = strings.TrimSpace(token)
	if strings.TrimSpace(info.InstanceToken) == "" || token != strings.TrimSpace(info.InstanceToken) {
		return false, "persisted instance token was replaced", true
	}
	if info.State != session.StateActive && info.State != session.StateAwake {
		return false, fmt.Sprintf("session state %q is not renewable", info.State), false
	}
	if state := session.State(strings.TrimSpace(info.MetadataState)); state == session.StateDraining {
		return false, "session is draining", false
	}
	return true, "", true
}

func claimLeaseAssigneeMatches(info session.Info, assignee string) bool {
	assignee = strings.TrimSpace(assignee)
	for _, identity := range session.AssigneeIdentities(info) {
		if strings.TrimSpace(identity) == assignee {
			return true
		}
	}
	return false
}

func sortClaimLeaseReports(reports []claimLeaseStoreReport) {
	sort.Slice(reports, func(i, j int) bool { return reports[i].Name < reports[j].Name })
}

func (cr *CityRuntime) reconcileClaimLeasesIfDue(ctx context.Context, snapshot *sessionBeadSnapshot, now time.Time) {
	cr.claimLeaseMu.Lock()
	if !cr.claimLeaseLastAttempt.IsZero() && now.Before(cr.claimLeaseLastAttempt.Add(claimLeaseReconcileInterval)) {
		cr.claimLeaseMu.Unlock()
		return
	}
	cr.claimLeaseLastAttempt = now
	cr.claimLeaseMu.Unlock()

	scopes := cr.claimLeaseScopes()
	if snapshot == nil || snapshot.LoadError() != nil {
		result := claimLeaseReconcileResult{LastAttemptAt: now.UTC()}
		for _, scope := range scopes {
			result.Stores = append(result.Stores, claimLeaseStoreReport{Name: scope.Name, Errors: 1})
		}
		sortClaimLeaseReports(result.Stores)
		cr.publishClaimLeaseResult(result)
		return
	}

	result := reconcileClaimLeases(ctx, cr.sp, snapshot.OpenInfos(), scopes, now)
	sortClaimLeaseReports(result.Stores)
	cr.publishClaimLeaseResult(result)
}

func (cr *CityRuntime) publishClaimLeaseResult(result claimLeaseReconcileResult) {
	cr.claimLeaseMu.Lock()
	if result.LastSuccessfulAt.IsZero() {
		// Last successful means the most recent fully successful pass, not
		// merely a field from the latest attempt. Preserve that evidence while
		// surfacing current per-store failures in the same observation.
		result.LastSuccessfulAt = cr.claimLeaseResult.LastSuccessfulAt
	}
	cr.claimLeaseResult = result
	cr.claimLeaseMu.Unlock()
}

func (cr *CityRuntime) claimLeaseScopes() []claimLeaseScope {
	cityName := strings.TrimSpace(cr.cityName)
	if cityName == "" {
		cityName = "city"
	}
	cityStore := cr.cityBeadStore()
	scopes := []claimLeaseScope{{
		Name:  cityName,
		Store: cityStore,
		Lease: cr.claimLeaseBackend(cr.cityPath, cityStore),
	}}

	if cr.cfg == nil {
		return scopes
	}
	suspension := loadSuspensionStateBestEffort(cr.cityPath)
	rigStores := cr.rigBeadStores()
	for _, rig := range cr.cfg.Rigs {
		if strings.TrimSpace(rig.Path) == "" || !rigStoreBackgroundRefresh(suspension, rig) {
			continue
		}
		scopeRoot := resolveStoreScopeRoot(cr.cityPath, rig.Path)
		store := rigStores[rig.Name]
		scopes = append(scopes, claimLeaseScope{
			Name:  rig.Name,
			Store: store,
			Lease: cr.claimLeaseBackend(scopeRoot, store),
		})
	}
	return scopes
}

func (cr *CityRuntime) claimLeaseBackend(scopeRoot string, store beads.Store) beads.ClaimLeaseStore {
	if store == nil || claimLeaseStoreUnavailable(store) {
		return nil
	}
	if lease, ok := store.(beads.ClaimLeaseStore); ok && lease != nil {
		return lease
	}
	// Native Dolt stores are the controller's normal read/write backend, but
	// bd still owns the ephemeral node-local lease table. Open a bd command
	// façade only for scopes whose on-disk identity proves the bd contract.
	// File and custom non-bd stores have no safe lease emulation and remain
	// unsupported.
	if strings.TrimSpace(scopeRoot) == "" || !scopeUsesBdStoreContract(scopeRoot) {
		return nil
	}
	if samePath(scopeRoot, cr.cityPath) {
		return bdStoreForCity(scopeRoot, cr.cityPath)
	}
	return bdStoreForRig(scopeRoot, cr.cityPath, cr.cfg)
}

func claimLeaseStoreUnavailable(store beads.Store) bool {
	for range 8 {
		switch value := store.(type) {
		case unavailableStore:
			return true
		case *beads.CachingStore:
			if value == nil || value.Backing() == nil {
				return true
			}
			store = value.Backing()
			continue
		}
		if inner, _, ok := unwrapBeadPolicyStore(store); ok {
			store = inner
			continue
		}
		return false
	}
	return true
}

func (cr *CityRuntime) claimLeaseReconciliation() claimLeaseReconcileResult {
	cr.claimLeaseMu.RLock()
	defer cr.claimLeaseMu.RUnlock()
	result := cr.claimLeaseResult
	result.Stores = append([]claimLeaseStoreReport(nil), result.Stores...)
	return result
}
