package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/fsys"
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
	// Native heartbeat/reclaim calls are patrol work, not general bd commands.
	// Keep each call well below the patrol interval so a slow backend cannot
	// consume the next renewal window.
	claimLeaseOperationTimeout = 20 * time.Second
	claimLeaseHeartbeatWorkers = 8
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
// is attempted for that scope. Missing or fenced ownership evidence is also a
// safety failure: the pass does not guess that a worker is gone.
func reconcileClaimLeases(ctx context.Context, provider runtime.Provider, infos []session.Info, scopes []claimLeaseScope, now time.Time) claimLeaseReconcileResult {
	result := claimLeaseReconcileResult{LastAttemptAt: now.UTC()}
	bySessionID := make(map[string]session.Info, len(infos))
	for _, info := range infos {
		if id := strings.TrimSpace(info.ID); id != "" {
			bySessionID[id] = info
		}
	}
	runningNames := make(map[string]struct{})
	var runtimeInventoryErr error
	if provider == nil {
		runtimeInventoryErr = fmt.Errorf("provider unavailable")
	} else {
		running, err := provider.ListRunning("")
		runtimeInventoryErr = err
		for _, name := range running {
			if name = strings.TrimSpace(name); name != "" {
				runningNames[name] = struct{}{}
			}
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
		type heartbeatCandidate struct {
			id       string
			assignee string
		}
		heartbeats := make([]heartbeatCandidate, 0, len(claims))
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
				// Claim-time identity stamping is best-effort. Its absence cannot
				// distinguish an abandoned claim from a live worker whose stamp
				// failed, so destructive reclaim must fail closed for this store.
				reclaimAllowed = false
				current.Errors++
				allComplete = false
				continue
			}
			info, exists := bySessionID[sessionID]
			if !exists {
				continue
			}
			live, reason, safeToReclaim := claimLeaseOwnerStatus(provider, info, runningNames, runtimeInventoryErr)
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
			heartbeats = append(heartbeats, heartbeatCandidate{id: claim.ID, assignee: assignee})
		}
		heartbeatResults := make(chan error, len(heartbeats))
		heartbeatSlots := make(chan struct{}, claimLeaseHeartbeatWorkers)
		for _, heartbeat := range heartbeats {
			heartbeat := heartbeat
			go func() {
				select {
				case heartbeatSlots <- struct{}{}:
					defer func() { <-heartbeatSlots }()
				case <-ctx.Done():
					heartbeatResults <- ctx.Err()
					return
				}
				opCtx, cancel := context.WithTimeout(ctx, claimLeaseOperationTimeout)
				err := scope.Lease.HeartbeatClaim(opCtx, heartbeat.id, heartbeat.assignee)
				cancel()
				heartbeatResults <- err
			}()
		}
		for range heartbeats {
			if err := <-heartbeatResults; err != nil {
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
		opCtx, cancel := context.WithTimeout(ctx, claimLeaseOperationTimeout)
		reclaimed, err := scope.Lease.ReclaimExpiredClaims(opCtx, claimLeaseReclaimGrace, assigneeList...)
		cancel()
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
func claimLeaseOwnerStatus(provider runtime.Provider, info session.Info, runningNames map[string]struct{}, runtimeInventoryErr error) (live bool, reason string, safeToReclaim bool) {
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
	if runtimeInventoryErr != nil {
		return false, "provider runtime inventory unavailable", false
	}
	if _, runtimePresent := runningNames[name]; !runtimePresent {
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
	ownerInfos, ownerSnapshotComplete := claimLeaseOwnerInfos(DesiredStateResult{}, snapshot)
	cr.reconcileClaimLeasesIfDueWithOwnerInfos(ctx, ownerInfos, ownerSnapshotComplete, now)
}

// reconcileClaimLeasesIfDueWithOwnerInfos runs the bounded lease pass using the
// owner census from the same desired-state build as the reconciliation tick.
// The full census includes session beads in the sessions binding, city store,
// and active rig stores; an incomplete census is a safety boundary and cannot
// authorize lease mutation.
func (cr *CityRuntime) reconcileClaimLeasesIfDueWithOwnerInfos(
	ctx context.Context,
	ownerInfos []session.Info,
	ownerSnapshotComplete bool,
	now time.Time,
) {
	cr.claimLeaseMu.Lock()
	if !cr.claimLeaseLastAttempt.IsZero() && now.Before(cr.claimLeaseLastAttempt.Add(claimLeaseReconcileInterval)) {
		cr.claimLeaseMu.Unlock()
		return
	}
	cr.claimLeaseLastAttempt = now
	cr.claimLeaseMu.Unlock()
	cr.runClaimLeaseReconcile(ctx, ownerInfos, ownerSnapshotComplete, now)
}

// startClaimLeaseReconcileIfDueWithOwnerInfos keeps native lease I/O off the
// controller tick. A single in-flight pass is allowed; each native command is
// independently context-bounded, so this lane cannot serialize later tick
// phases or accumulate overlapping reclaim attempts.
func (cr *CityRuntime) startClaimLeaseReconcileIfDueWithOwnerInfos(
	ctx context.Context,
	ownerInfos []session.Info,
	ownerSnapshotComplete bool,
	now time.Time,
) {
	cr.claimLeaseMu.Lock()
	if cr.claimLeaseInFlight || (!cr.claimLeaseLastAttempt.IsZero() && now.Before(cr.claimLeaseLastAttempt.Add(claimLeaseReconcileInterval))) {
		cr.claimLeaseMu.Unlock()
		return
	}
	cr.claimLeaseLastAttempt = now
	cr.claimLeaseInFlight = true
	cr.claimLeaseMu.Unlock()

	owners := append([]session.Info(nil), ownerInfos...)
	go func() {
		defer func() {
			cr.claimLeaseMu.Lock()
			cr.claimLeaseInFlight = false
			cr.claimLeaseMu.Unlock()
		}()
		cr.runClaimLeaseReconcile(ctx, owners, ownerSnapshotComplete, now)
	}()
}

func (cr *CityRuntime) runClaimLeaseReconcile(ctx context.Context, ownerInfos []session.Info, ownerSnapshotComplete bool, now time.Time) {
	scopes, scopeErr := cr.claimLeaseScopes()
	if scopeErr != nil {
		cr.publishClaimLeaseResult(claimLeaseReconcileResult{
			LastAttemptAt: now.UTC(),
			Stores:        []claimLeaseStoreReport{{Name: cr.claimLeaseCityScopeName(), Errors: 1}},
		})
		return
	}

	if !ownerSnapshotComplete {
		result := claimLeaseReconcileResult{LastAttemptAt: now.UTC()}
		for _, scope := range scopes {
			result.Stores = append(result.Stores, claimLeaseStoreReport{Name: scope.Name, Errors: 1})
		}
		sortClaimLeaseReports(result.Stores)
		cr.publishClaimLeaseResult(result)
		return
	}

	result := reconcileClaimLeases(ctx, cr.sp, ownerInfos, scopes, now)
	sortClaimLeaseReports(result.Stores)
	cr.publishClaimLeaseResult(result)
}

// claimLeaseOwnerInfos selects the strongest owner evidence available for a
// controller tick. DesiredStateResult.SessionOccupancyInfos is the complete
// cross-store census when present; the primary snapshot is only a fallback for
// focused callers that do not provide a build result. Partial evidence never
// becomes an empty owner set, because that would make live claims look
// abandoned and permit unsafe reclaim.
func claimLeaseOwnerInfos(result DesiredStateResult, snapshot *sessionBeadSnapshot) ([]session.Info, bool) {
	if result.SessionQueryPartial {
		return nil, false
	}
	if result.SessionOccupancyInfos != nil || result.SessionSnapshotComplete {
		infos := make([]session.Info, len(result.SessionOccupancyInfos))
		copy(infos, result.SessionOccupancyInfos)
		return infos, result.SessionSnapshotComplete
	}
	if result.SessionQueryPartial || snapshot == nil || snapshot.LoadError() != nil {
		return nil, false
	}
	return snapshot.OpenInfos(), true
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

func (cr *CityRuntime) claimLeaseCityScopeName() string {
	if cityName := strings.TrimSpace(cr.cityName); cityName != "" {
		return cityName
	}
	return "city"
}

func (cr *CityRuntime) claimLeaseScopes() ([]claimLeaseScope, error) {
	cityName := cr.claimLeaseCityScopeName()
	cityStore := cr.cityBeadStore()
	scopes := []claimLeaseScope{{
		Name:  cityName,
		Store: cityStore,
		Lease: cr.claimLeaseBackend(cr.cityPath, cityStore),
	}}

	if cr.cfg == nil {
		return scopes, nil
	}
	suspension, err := loadSuspensionState(fsys.OSFS{}, cr.cityPath)
	if err != nil {
		return nil, fmt.Errorf("loading suspension state for claim lease scopes: %w", err)
	}
	rigStores := cr.rigBeadStores() // residency:allow lease reconciliation scopes only active configured rigs
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
	return scopes, nil
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
