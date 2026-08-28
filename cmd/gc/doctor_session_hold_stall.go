package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
	"github.com/gastownhall/gascity/internal/fsys"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/suspensionstate"
)

// sessionHoldFarFutureThreshold is how far out a session's held_until has to
// sit before it stops reading as a deadline and starts reading as a sentinel.
// Every path that legitimately writes a hold stays well below it: gc runtime
// heartbeat is capped at maximumHeartbeatDuration (12h) by
// validateHeartbeatDuration, and gc session wait parks for an operator-supplied
// window. Only indefiniteHoldDuration — the 100-year "suspended indefinitely"
// value gc session suspend writes — crosses it.
//
// The sentinel is harmless while the runtime is up. It becomes load-bearing the
// moment the session stops: no timer will ever retire the hold, so the session
// is out of service until someone runs gc session wake by hand. gc-yrc is the
// incident where that combination silently removed a city's only implementation
// worker and stalled its queue with no error anywhere.
const sessionHoldFarFutureThreshold = 30 * 24 * time.Hour

// sessionHoldFarFutureLabel renders the threshold the way an operator reads a
// calendar. time.Duration.String would print it as "720h0m0s", which buries the
// one number the finding is about.
const sessionHoldFarFutureLabel = "30 days"

// sessionHoldStallCheck surfaces the two states that stall a work queue without
// producing an error: a session parked under a hold no timer will retire, and a
// work bead sitting in_progress against a session whose runtime is down.
//
// Both are invisible today. The reconciler's pool-demand query counts only
// UNASSIGNED routed beads, so a bead stamped with a dead session's assignee is
// not demand; no demand means no wake reason, no wake reason means the session
// stays down, and the bead stays in_progress against a worker that will never
// come back. Externally this presents as "workers are idle" with a non-empty
// backlog.
//
// Detection only. The repairs (gc session wake, bd unclaim --force) release a
// live claim and are operator calls, not something a --fix sweep should make.
type sessionHoldStallCheck struct {
	cfg      *config.City
	cityPath string
	newStore func(string) (beads.Store, error)
	now      func() time.Time
}

// newSessionHoldStallCheck constructs a sessionHoldStallCheck.
func newSessionHoldStallCheck(cfg *config.City, cityPath string, newStore func(string) (beads.Store, error)) *sessionHoldStallCheck {
	return &sessionHoldStallCheck{cfg: cfg, cityPath: cityPath, newStore: newStore}
}

// Name returns the check's identifier.
func (c *sessionHoldStallCheck) Name() string { return "session-hold-stall" }

// CanFix reports that this check is detection-only.
func (c *sessionHoldStallCheck) CanFix() bool { return false }

// WarmupEligible reports that this check reads live store state and must not be
// served from a warmup snapshot.
func (c *sessionHoldStallCheck) WarmupEligible() bool { return false }

// Fix is a no-op; releasing a claim or clearing a hold is an operator call.
func (c *sessionHoldStallCheck) Fix(_ *doctor.CheckContext) error { return nil }

// sessionRuntimeExpectedDown reports whether a session bead's advisory state
// says no runtime should be up for it. It is deliberately an allowlist of the
// states that DO expect a live process: an unrecognized or newly added state
// must not be silently classified as "down" and start reporting live workers as
// stranded.
func sessionRuntimeExpectedDown(info sessionpkg.Info) bool {
	switch sessionpkg.State(strings.TrimSpace(info.MetadataState)) {
	case sessionpkg.StateActive, sessionpkg.StateAwake,
		sessionpkg.StateCreating, sessionpkg.StateStartPending,
		sessionpkg.StateDraining:
		return false
	case sessionpkg.StateAsleep, sessionpkg.StateSuspended, sessionpkg.StateDrained,
		sessionpkg.StateFailedCreate, sessionpkg.StateArchived, sessionpkg.StateQuarantined:
		return true
	default:
		return false
	}
}

// sessionHoldStallScope is one bead store the check reads, with the label it
// uses when reporting findings from it.
type sessionHoldStallScope struct {
	label string
	store beads.Store
}

// heldSession is a session bead parked under a hold no timer will retire.
type heldSession struct {
	id        string
	name      string
	heldUntil string
	state     string
}

// strandedClaim is a work bead sitting in_progress against a session whose
// runtime is down.
type strandedClaim struct {
	scope     string
	beadID    string
	assignee  string
	sessionID string
	state     string
	heldUntil string
}

// Run scans the city's session beads for far-future holds, then scans the city
// and each non-suspended, path-bearing rig for in_progress work assigned to a
// session whose runtime is down.
func (c *sessionHoldStallCheck) Run(_ *doctor.CheckContext) *doctor.CheckResult {
	now := time.Now().UTC()
	if c.now != nil {
		now = c.now().UTC()
	}

	if c.newStore == nil {
		return warnCheck(c.Name(), "session-hold-stall check skipped: no bead store factory",
			"rerun gc doctor from a city workspace", nil)
	}
	cityStore, err := c.newStore(c.cityPath)
	if err != nil {
		return warnCheck(c.Name(), "session-hold-stall check skipped: opening city bead store",
			"fix bead store access, then rerun gc doctor",
			[]string{fmt.Sprintf("city skipped: opening bead store: %v", err)})
	}

	sessions, err := sessionFrontDoor(cityStore).ListLabeledSessionInfosUnfiltered()
	if err != nil {
		return warnCheck(c.Name(), "session-hold-stall check skipped: listing session beads",
			"fix bead store access, then rerun gc doctor",
			[]string{fmt.Sprintf("city skipped: listing session beads: %v", err)})
	}

	held, downByAssignee := c.classifySessions(sessions, now)
	scopes, skipped := c.scopes(cityStore)
	stranded, scanSkipped := scanStrandedClaims(scopes, downByAssignee)
	skipped = append(skipped, scanSkipped...)

	return c.result(held, stranded, skipped)
}

// classifySessions splits the session list into the far-future holds to report
// and an assignee->session index of the sessions whose runtime is down, which is
// what the work scan resolves each in_progress assignee against.
func (c *sessionHoldStallCheck) classifySessions(sessions []sessionpkg.Info, now time.Time) ([]heldSession, map[string]heldSession) {
	var held []heldSession
	downByAssignee := make(map[string]heldSession)
	for _, info := range sessions {
		state := strings.TrimSpace(info.MetadataState)
		entry := heldSession{
			id:        info.ID,
			name:      strings.TrimSpace(info.SessionNameMetadata),
			heldUntil: strings.TrimSpace(info.HeldUntil),
			state:     state,
		}
		if entry.name == "" {
			entry.name = info.ID
		}
		if until, ok := parseRFC3339Metadata(info.HeldUntil); ok && until.After(now.Add(sessionHoldFarFutureThreshold)) {
			held = append(held, entry)
		}
		if !sessionRuntimeExpectedDown(info) {
			continue
		}
		// sessionAssignmentIdentifiersForConfigInfo is the same derivation the
		// reconciler's assigned-work gates use, so a bead this check calls
		// stranded is one those gates would agree is assigned to this session.
		for _, id := range sessionAssignmentIdentifiersForConfigInfo(info, c.cfg) {
			if id = strings.TrimSpace(id); id != "" {
				downByAssignee[id] = entry
			}
		}
	}
	return held, downByAssignee
}

// scopes opens the city store plus each non-suspended, path-bearing rig store.
// A rig that cannot be opened is reported as skipped rather than silently
// treated as holding nothing.
func (c *sessionHoldStallCheck) scopes(cityStore beads.Store) ([]sessionHoldStallScope, []string) {
	scopes := []sessionHoldStallScope{{label: "city", store: cityStore}}
	var skipped []string
	if c.cfg == nil {
		return scopes, skipped
	}
	suspState, _ := loadSuspensionState(fsys.OSFS{}, c.cityPath)
	for _, rig := range c.cfg.Rigs {
		if suspensionstate.EffectiveRigSuspended(suspState, rig.Name, rig.EffectiveSuspendedOnStart()) || strings.TrimSpace(rig.Path) == "" {
			continue
		}
		store, err := c.newStore(rig.Path)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("rig %s skipped: opening bead store: %v", rig.Name, err))
			continue
		}
		scopes = append(scopes, sessionHoldStallScope{label: "rig " + rig.Name, store: store})
	}
	return scopes, skipped
}

// scanStrandedClaims lists each scope's in_progress beads and reports the ones
// whose assignee resolves to a session with no live runtime.
//
// An assignee absent from downByAssignee is left alone: it is a human, a pool
// alias, or a session whose bead is already closed, and this check has no way to
// tell "definitely stranded" from "not a session" for those. Reporting them
// would trade a silent stall for a noisy one.
func scanStrandedClaims(scopes []sessionHoldStallScope, downByAssignee map[string]heldSession) ([]strandedClaim, []string) {
	var stranded []strandedClaim
	var skipped []string
	if len(downByAssignee) == 0 {
		return stranded, skipped
	}
	for _, sc := range scopes {
		if sc.store == nil {
			continue
		}
		// Status counts as a filter, so this needs no AllowScan. TierBoth
		// includes wisp-tier work, which is where routed pool work lands.
		items, err := sc.store.List(beads.ListQuery{Status: "in_progress", TierMode: beads.TierBoth, Live: true})
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s skipped: listing in_progress beads: %v", sc.label, err))
			continue
		}
		for _, item := range excludeMailMessageBeads(items) {
			if sessionpkg.IsSessionBeadOrRepairable(item) {
				continue
			}
			assignee := strings.TrimSpace(item.Assignee)
			owner, ok := downByAssignee[assignee]
			if !ok {
				continue
			}
			stranded = append(stranded, strandedClaim{
				scope:     sc.label,
				beadID:    item.ID,
				assignee:  assignee,
				sessionID: owner.id,
				state:     owner.state,
				heldUntil: owner.heldUntil,
			})
		}
	}
	return stranded, skipped
}

// result renders the check outcome. Stranded claims outrank held sessions in the
// message because a stranded claim is a queue that has already stalled, while a
// far-future hold is only the condition that lets one stall.
func (c *sessionHoldStallCheck) result(held []heldSession, stranded []strandedClaim, skipped []string) *doctor.CheckResult {
	if len(held) == 0 && len(stranded) == 0 && len(skipped) == 0 {
		return okCheck(c.Name(), "no far-future session holds and no in_progress work assigned to a stopped session")
	}

	details := make([]string, 0, len(held)+len(stranded)+len(skipped))
	for _, h := range held {
		details = append(details, fmt.Sprintf("session %s (%s) state=%s held_until=%s is more than %s out",
			h.id, h.name, h.state, h.heldUntil, sessionHoldFarFutureLabel))
	}
	for _, s := range stranded {
		detail := fmt.Sprintf("%s bead %s is in_progress against %s (session %s, state=%s)",
			s.scope, s.beadID, s.assignee, s.sessionID, s.state)
		if s.heldUntil != "" {
			detail += fmt.Sprintf(", held_until=%s", s.heldUntil)
		}
		details = append(details, detail)
	}
	details = append(details, skipped...)
	sort.Strings(details)

	if len(held) == 0 && len(stranded) == 0 {
		return warnCheck(c.Name(),
			fmt.Sprintf("session-hold-stall check skipped %d scope(s)", len(skipped)),
			"fix bead store access, then rerun gc doctor",
			details)
	}

	var parts []string
	if len(stranded) > 0 {
		parts = append(parts, fmt.Sprintf("%d bead(s) in_progress against a stopped session", len(stranded)))
	}
	if len(held) > 0 {
		parts = append(parts, fmt.Sprintf("%d session(s) held more than %s out", len(held), sessionHoldFarFutureLabel))
	}
	message := strings.Join(parts, ", ")
	if len(skipped) > 0 {
		message = fmt.Sprintf("%s (and skipped %d scope(s))", message, len(skipped))
	}

	hint := "wake the session with gc session wake <session-id>; if its runtime is provably gone, release the work with bd unclaim <bead-id> --force so pool demand is restored"
	if len(stranded) == 0 {
		hint = "a deliberate gc session suspend is expected here; if the session was not suspended on purpose, clear the hold with gc session wake <session-id>"
	}
	return warnCheck(c.Name(), message, hint, details)
}
