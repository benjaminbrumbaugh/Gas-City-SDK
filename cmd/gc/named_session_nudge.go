package main

import (
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// Named-session backstop state is deliberately separate from the pool state.
// A named session has no pool slot to drain when delivery is exhausted; its
// bounded retry record is therefore only a pacing latch for the configured
// session's own claim nudge.
const (
	namedClaimNudgeWorkKey     = "named_claim_nudge_work"
	namedClaimNudgeStoreRefKey = "named_claim_nudge_store_ref"
	namedClaimNudgeCountKey    = "named_claim_nudge_count"
	namedClaimNudgeAtKey       = "named_claim_nudge_at"

	namedExecutionNudgeWorkKey     = "named_execution_nudge_work"
	namedExecutionNudgeStoreRefKey = "named_execution_nudge_store_ref"
	namedExecutionNudgeCountKey    = "named_execution_nudge_count"
	namedExecutionNudgeAtKey       = "named_execution_nudge_at"
)

// nudgeStalledNamedSessionClaims covers the awake on_demand gap: a running
// named session owns exactly one wisp that is still open, but its previous
// turn ended before the configured claim hook was delivered. It shares the
// observe/grace/backoff engine with pool slots, while keeping eligibility and
// pacing state isolated from pool claims.
func nudgeStalledNamedSessionClaims(
	sp runtime.Provider,
	cfg *config.City,
	store beads.Store,
	sessionBeads []beads.Bead,
	work []beads.Bead,
	workStores []beads.Store,
	workStoreRefs []string,
	now time.Time,
	stdout io.Writer,
) {
	if namedSessionNudgeInputsInvalid(sp, cfg, store) {
		return
	}
	runNudgeBackstop(
		sp,
		store,
		sessionBeads,
		nil,
		now,
		stdout,
		"named-claim-nudge",
		namedSessionClaimBackstop{
			cfg:    cfg,
			sp:     sp,
			now:    now,
			claims: newNamedSessionClaimSnapshot(work, workStores, workStoreRefs, "open"),
			kind:   namedSessionClaimOpen,
		},
	)
}

// nudgeStalledNamedSessionExecution covers the second delivery edge: a
// running named session owns exactly one wisp in_progress, but has gone quiet
// without starting its work. Unlike the pool execution backstop, exhaustion
// is a no-op because named sessions are not disposable pool seats.
func nudgeStalledNamedSessionExecution(
	sp runtime.Provider,
	cfg *config.City,
	store beads.Store,
	sessionBeads []beads.Bead,
	work []beads.Bead,
	workStores []beads.Store,
	workStoreRefs []string,
	now time.Time,
	stdout io.Writer,
) {
	if namedSessionNudgeInputsInvalid(sp, cfg, store) {
		return
	}
	runNudgeBackstop(
		sp,
		store,
		sessionBeads,
		nil,
		now,
		stdout,
		"named-execution-nudge",
		namedSessionClaimBackstop{
			cfg:    cfg,
			sp:     sp,
			now:    now,
			claims: newNamedSessionClaimSnapshot(work, workStores, workStoreRefs, "in_progress"),
			kind:   namedSessionClaimExecution,
		},
	)
}

func namedSessionNudgeInputsInvalid(sp runtime.Provider, cfg *config.City, store beads.Store) bool {
	if sp == nil || cfg == nil || store == nil {
		return true
	}
	if sess, ok := store.(beads.SessionStore); ok && sess.Store == nil {
		return true
	}
	return false
}

type namedSessionClaimKind uint8

const (
	namedSessionClaimOpen namedSessionClaimKind = iota
	namedSessionClaimExecution
)

type namedSessionClaim struct {
	BeadID   string
	StoreRef string
	Assignee string
	Store    beads.Store
}

type namedSessionClaimSnapshot struct {
	byAssignee map[string][]namedSessionClaim
}

func newNamedSessionClaimSnapshot(
	work []beads.Bead,
	stores []beads.Store,
	storeRefs []string,
	status string,
) namedSessionClaimSnapshot {
	snapshot := namedSessionClaimSnapshot{byAssignee: make(map[string][]namedSessionClaim)}
	for i, wb := range work {
		if !strings.EqualFold(strings.TrimSpace(wb.Status), status) ||
			(!wb.Ephemeral && !wb.NoHistory) ||
			strings.TrimSpace(wb.ID) == "" ||
			strings.TrimSpace(wb.Assignee) == "" ||
			i >= len(stores) || stores[i] == nil {
			continue
		}
		storeRef := ""
		if i < len(storeRefs) {
			storeRef = normalizeIdleClaimStoreRef(storeRefs[i])
		}
		claim := namedSessionClaim{
			BeadID:   wb.ID,
			StoreRef: storeRef,
			Assignee: strings.TrimSpace(wb.Assignee),
			Store:    stores[i],
		}
		seen := false
		for _, existing := range snapshot.byAssignee[claim.Assignee] {
			if existing.BeadID == claim.BeadID && existing.StoreRef == claim.StoreRef {
				seen = true
				break
			}
		}
		if !seen {
			snapshot.byAssignee[claim.Assignee] = append(snapshot.byAssignee[claim.Assignee], claim)
		}
	}
	for assignee := range snapshot.byAssignee {
		sort.Slice(snapshot.byAssignee[assignee], func(i, j int) bool {
			left, right := snapshot.byAssignee[assignee][i], snapshot.byAssignee[assignee][j]
			if left.StoreRef != right.StoreRef {
				return left.StoreRef < right.StoreRef
			}
			return left.BeadID < right.BeadID
		})
	}
	return snapshot
}

func (s namedSessionClaimSnapshot) forIdentities(identities []string) []namedSessionClaim {
	seen := make(map[string]struct{})
	var claims []namedSessionClaim
	for _, identity := range identities {
		for _, claim := range s.byAssignee[identity] {
			key := claim.StoreRef + "\x00" + claim.BeadID
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			claims = append(claims, claim)
		}
	}
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].StoreRef != claims[j].StoreRef {
			return claims[i].StoreRef < claims[j].StoreRef
		}
		return claims[i].BeadID < claims[j].BeadID
	})
	return claims
}

type namedSessionClaimBackstop struct {
	cfg    *config.City
	sp     runtime.Provider
	now    time.Time
	claims namedSessionClaimSnapshot
	kind   namedSessionClaimKind
}

func (p namedSessionClaimBackstop) governs(s beads.Bead) bool {
	return configuredOnDemandNamedSession(s, p.cfg)
}

func (p namedSessionClaimBackstop) resolve(s beads.Bead, _ map[string]beads.Bead, sessName string) (backstopTarget, backstopResolution) {
	claims := p.claims.forIdentities(currentSessionAssigneeIdentities(s))
	if len(claims) == 0 {
		return backstopTarget{}, backstopResolutionClear
	}
	if len(claims) != 1 {
		return backstopTarget{}, backstopResolutionHold
	}
	if !p.sessionIsQuiet(sessName) {
		return backstopTarget{}, backstopResolutionHold
	}
	claim := claims[0]
	return backstopTarget{
		ID:       claim.BeadID,
		StoreRef: claim.StoreRef,
		Assignee: claim.Assignee,
		Store:    claim.Store,
	}, backstopResolutionOutstanding
}

func (p namedSessionClaimBackstop) sessionIsQuiet(sessName string) bool {
	last, err := p.sp.GetLastActivity(sessName)
	if err != nil || last.IsZero() {
		return false
	}
	return p.now.Sub(last) >= idleClaimNudgeGrace
}

func (p namedSessionClaimBackstop) state(s beads.Bead, target backstopTarget) (bool, int, time.Time) {
	workKey, storeKey, countKey, atKey := p.markerKeys()
	same := strings.TrimSpace(s.Metadata[workKey]) == target.ID &&
		strings.TrimSpace(s.Metadata[storeKey]) == target.StoreRef
	return same, atoiOr0(s.Metadata[countKey]), parseRFC3339OrZero(s.Metadata[atKey])
}

func (p namedSessionClaimBackstop) content(s beads.Bead) string {
	return claimNudgeFor(p.cfg, s)
}

func (p namedSessionClaimBackstop) revalidate(target backstopTarget) backstopResolution {
	if target.Store == nil {
		return backstopResolutionHold
	}
	live := beads.HandlesFor(target.Store).Live
	if live == nil {
		return backstopResolutionHold
	}
	current, err := live.Get(target.ID)
	if err != nil || current.ID != target.ID {
		return backstopResolutionHold
	}
	wantStatus := "open"
	if p.kind == namedSessionClaimExecution {
		wantStatus = "in_progress"
	}
	if !strings.EqualFold(strings.TrimSpace(current.Status), wantStatus) ||
		strings.TrimSpace(current.Assignee) != target.Assignee ||
		(!current.Ephemeral && !current.NoHistory) {
		return backstopResolutionClear
	}
	return backstopResolutionOutstanding
}

func (p namedSessionClaimBackstop) observe(store beads.Store, s *beads.Bead, target backstopTarget, now time.Time, stdout io.Writer) {
	p.writeMarker(store, s, target, 0, now, stdout)
}

func (p namedSessionClaimBackstop) reserve(store beads.Store, s *beads.Bead, target backstopTarget, attempts int, now time.Time, stdout io.Writer) bool {
	return p.writeMarker(store, s, target, attempts, now, stdout)
}

func (p namedSessionClaimBackstop) exhausted(_ beads.Store, _ *beads.Bead, _ io.Writer) {
}

func (p namedSessionClaimBackstop) clear(store beads.Store, s *beads.Bead, stdout io.Writer) {
	workKey, storeKey, countKey, atKey := p.markerKeys()
	keys := []string{workKey, storeKey, countKey, atKey}
	dirty := false
	for _, key := range keys {
		if strings.TrimSpace(s.Metadata[key]) != "" {
			dirty = true
			break
		}
	}
	if !dirty {
		return
	}
	kvs := make(map[string]string, len(keys))
	for _, key := range keys {
		kvs[key] = ""
	}
	if !writeSessionMetadata(store, s, kvs, p.label(), stdout) {
		return
	}
	for _, key := range keys {
		delete(s.Metadata, key)
	}
}

func (p namedSessionClaimBackstop) markerKeys() (string, string, string, string) {
	if p.kind == namedSessionClaimExecution {
		return namedExecutionNudgeWorkKey, namedExecutionNudgeStoreRefKey, namedExecutionNudgeCountKey, namedExecutionNudgeAtKey
	}
	return namedClaimNudgeWorkKey, namedClaimNudgeStoreRefKey, namedClaimNudgeCountKey, namedClaimNudgeAtKey
}

func (p namedSessionClaimBackstop) label() string {
	if p.kind == namedSessionClaimExecution {
		return "named-execution-nudge"
	}
	return "named-claim-nudge"
}

func (p namedSessionClaimBackstop) writeMarker(store beads.Store, s *beads.Bead, target backstopTarget, attempts int, now time.Time, stdout io.Writer) bool {
	workKey, storeKey, countKey, atKey := p.markerKeys()
	return writeSessionMetadata(store, s, map[string]string{
		workKey:  target.ID,
		storeKey: target.StoreRef,
		countKey: strconv.Itoa(attempts),
		atKey:    now.UTC().Format(time.RFC3339),
	}, p.label(), stdout)
}

func configuredOnDemandNamedSession(s beads.Bead, cfg *config.City) bool {
	if cfg == nil || strings.TrimSpace(s.Metadata["pool_managed"]) == "true" || !isNamedSessionBead(s) {
		return false
	}
	if mode := namedSessionMode(s); mode != "" && mode != "on_demand" {
		return false
	}
	identity := namedSessionIdentity(s)
	if identity == "" {
		return false
	}
	spec, ok := findNamedSessionSpec(cfg, cfg.EffectiveCityName(), identity)
	return ok && spec.Mode == "on_demand" && spec.Agent != nil && strings.TrimSpace(spec.Agent.Nudge) != ""
}
