package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// usageLimitModalPane is the provider's usage-limit choice modal as it renders
// in a wedged pane. The dialog waits on a keypress no managed session sends and
// does not clear when the limit window closes, so the session behind it is dead
// while tmux and the provider process both stay alive.
const usageLimitModalPane = `● Bash(bd ready)
  ⎿  3 issues ready

What do you want to do?
❯ 1. Stop and wait for limit to reset
  2. Ask your admin for more usage
Enter to confirm · Esc to cancel`

// usageLimitModalProbes records which probes a detection actually ran, so the
// tests can pin the lazy ordering that keeps the check cheap: the caller's
// already-paid activity read gates the pane capture, and the pane match gates
// the attachment probe.
type usageLimitModalProbes struct {
	paneCalls     int
	attachedCalls int
	probeErrors   []string

	activity    time.Time
	activityErr error
	pane        string
	paneErr     error
	attached    bool
	attachedErr error
}

func (p *usageLimitModalProbes) capturePane() (string, error) {
	p.paneCalls++
	return p.pane, p.paneErr
}

func (p *usageLimitModalProbes) isAttached() (bool, error) {
	p.attachedCalls++
	return p.attached, p.attachedErr
}

func (p *usageLimitModalProbes) onProbeError(stage string, _ error) {
	p.probeErrors = append(p.probeErrors, stage)
}

func (p *usageLimitModalProbes) detect(alive, recorded bool, now time.Time) bool {
	return detectUsageLimitModalWedge(alive, recorded, now, p.activity, p.activityErr, p.capturePane, p.isAttached, p.onProbeError)
}

// wedgedProbes returns probes describing the failure this check exists to
// catch: an alive session whose pane has been frozen on the modal well past the
// staleness window, with nobody attached to answer it.
func wedgedProbes(now time.Time) *usageLimitModalProbes {
	return &usageLimitModalProbes{
		activity: now.Add(-3 * usageLimitModalFrozenPane),
		pane:     usageLimitModalPane,
	}
}

func TestDetectUsageLimitModalWedge_FrozenPaneOnModal(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	probes := wedgedProbes(now)

	if !probes.detect(true, false, now) {
		t.Fatal("a frozen, unattended pane parked on the usage-limit modal must be detected as wedged")
	}
	if probes.paneCalls != 1 || probes.attachedCalls != 1 {
		t.Errorf("probe calls = pane %d, attached %d; want 1 each", probes.paneCalls, probes.attachedCalls)
	}
	if len(probes.probeErrors) != 0 {
		t.Errorf("probe errors = %v, want none", probes.probeErrors)
	}
}

// TestDetectUsageLimitModalWedge_ActiveSessionIsNeverCaptured pins the
// corroboration that keeps a text match from condemning a healthy session. A
// pane that merely RENDERS this modal — an agent reading the bug report about
// it, or sweeping panes for its text — is actively drawing, so its activity
// timestamp is fresh and the pane is never even captured.
func TestDetectUsageLimitModalWedge_ActiveSessionIsNeverCaptured(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	probes := wedgedProbes(now)
	probes.activity = now.Add(-2 * time.Second)

	if probes.detect(true, false, now) {
		t.Fatal("a session that drew output seconds ago must never be scored wedged, whatever its pane says")
	}
	if probes.paneCalls != 0 {
		t.Errorf("pane captures = %d, want 0: the activity read must gate the capture", probes.paneCalls)
	}
}

func TestDetectUsageLimitModalWedge_Guards(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		alive      bool
		recorded   bool
		mutate     func(*usageLimitModalProbes)
		wantProbe  string
		wantPanes  int
		wantAttach int
	}{
		{
			name:  "dead session is the crash path's business",
			alive: false,
		},
		{
			name:     "wedge already recorded this quarantine window",
			alive:    true,
			recorded: true,
		},
		{
			name:      "unknown activity",
			alive:     true,
			mutate:    func(p *usageLimitModalProbes) { p.activity = time.Time{} },
			wantPanes: 0,
		},
		{
			// The caller logs its own failed activity read; this check declines
			// silently rather than reporting the same failure twice.
			name:   "activity read failed",
			alive:  true,
			mutate: func(p *usageLimitModalProbes) { p.activityErr = errors.New("no server") },
		},
		{
			name:      "pane holds no modal",
			alive:     true,
			mutate:    func(p *usageLimitModalProbes) { p.pane = "❯ waiting for work" },
			wantPanes: 1,
		},
		{
			name:      "pane capture failed",
			alive:     true,
			mutate:    func(p *usageLimitModalProbes) { p.paneErr = errors.New("pane gone") },
			wantProbe: "pane capture",
			wantPanes: 1,
		},
		{
			name:       "a human is attached and can answer it",
			alive:      true,
			mutate:     func(p *usageLimitModalProbes) { p.attached = true },
			wantPanes:  1,
			wantAttach: 1,
		},
		{
			name:       "attachment probe failed",
			alive:      true,
			mutate:     func(p *usageLimitModalProbes) { p.attachedErr = errors.New("list-clients failed") },
			wantProbe:  "attachment",
			wantPanes:  1,
			wantAttach: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probes := wedgedProbes(now)
			if tt.mutate != nil {
				tt.mutate(probes)
			}
			if probes.detect(tt.alive, tt.recorded, now) {
				t.Fatal("detectUsageLimitModalWedge must not report a wedge here")
			}
			if probes.paneCalls != tt.wantPanes {
				t.Errorf("pane captures = %d, want %d", probes.paneCalls, tt.wantPanes)
			}
			if probes.attachedCalls != tt.wantAttach {
				t.Errorf("attachment probes = %d, want %d", probes.attachedCalls, tt.wantAttach)
			}
			switch {
			case tt.wantProbe == "" && len(probes.probeErrors) != 0:
				t.Errorf("probe errors = %v, want none", probes.probeErrors)
			case tt.wantProbe != "":
				if len(probes.probeErrors) != 1 || probes.probeErrors[0] != tt.wantProbe {
					t.Errorf("probe errors = %v, want [%s]", probes.probeErrors, tt.wantProbe)
				}
			}
		})
	}
}

// TestUsageLimitModalWedgePatch pins the metadata a detected wedge records: the
// session is scored unhealthy with an attributable reason, and its slot is
// quarantined so a still-limited account is not respawned straight back into the
// same wall.
func TestUsageLimitModalWedgePatch(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	patch := usageLimitModalWedgePatch(now)

	if got := patch[sessionHealthStateMetadataKey]; got != "unhealthy" {
		t.Errorf("%s = %q, want unhealthy", sessionHealthStateMetadataKey, got)
	}
	if got := patch[sessionHealthReasonMetadataKey]; got != sessionHealthReasonUsageLimitModal {
		t.Errorf("%s = %q, want %q", sessionHealthReasonMetadataKey, got, sessionHealthReasonUsageLimitModal)
	}
	until, err := time.Parse(time.RFC3339, patch["quarantined_until"])
	if err != nil {
		t.Fatalf("quarantined_until parse: %v", err)
	}
	if want := now.Add(defaultRateLimitQuarantineDuration); !until.Equal(want) {
		t.Errorf("quarantined_until = %s, want %s", until.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// A modal wedge clears on a fresh session, so it must not be mistaken for a
	// terminal provider error — that classification is reserved for failures an
	// operator has to repair, and sessionHasProviderTerminalErrorInfo keys on
	// the drainable flag to tell them apart.
	if _, ok := patch[sessionDrainableMetadataKey]; ok {
		t.Errorf("%s must not be set: a usage-limit wedge is retryable, not a terminal provider error", sessionDrainableMetadataKey)
	}
	info := seedSessionInfo(makeBead("b1", map[string]string(patch)))
	if sessionHasProviderTerminalErrorInfo(info) {
		t.Error("a usage-limit modal wedge must not read as a terminal provider error")
	}
}

func TestUsageLimitModalWedgeRecorded(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		metadata map[string]string
		want     bool
	}{
		{
			name:     "quarantine still running",
			metadata: map[string]string(usageLimitModalWedgePatch(now.Add(-time.Minute))),
			want:     true,
		},
		{
			name:     "quarantine expired, the wedge may be re-reported",
			metadata: map[string]string(usageLimitModalWedgePatch(now.Add(-2 * defaultRateLimitQuarantineDuration))),
			want:     false,
		},
		{
			name: "a different unhealthy reason is not this wedge",
			metadata: map[string]string{
				sessionHealthStateMetadataKey:  "unhealthy",
				sessionHealthReasonMetadataKey: "model_not_found",
				"quarantined_until":            now.Add(time.Hour).Format(time.RFC3339),
			},
			want: false,
		},
		{name: "clean session", metadata: map[string]string{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := seedSessionInfo(makeBead("b1", tt.metadata))
			if got := usageLimitModalWedgeRecorded(info, now); got != tt.want {
				t.Errorf("usageLimitModalWedgeRecorded() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestUsageLimitModalWedgeQuarantineSuppressesWake pins the pacing that keeps a
// detected wedge from becoming a respawn loop: the quarantine the patch writes
// is the same one the awake set already honors, so a session restarted out of
// the modal is not immediately rebuilt into an account that is still limited.
func TestUsageLimitModalWedgeQuarantineSuppressesWake(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	info := seedSessionInfo(makeBead("b1", map[string]string(usageLimitModalWedgePatch(now))))

	view := sessionpkg.ProjectLifecycle(sessionpkg.LifecycleInput{
		QuarantinedUntil: info.QuarantinedUntil,
		Now:              now.Add(time.Minute),
	})
	if !view.HasBlocker(sessionpkg.BlockerQuarantined) {
		t.Error("the wedge quarantine must block the session from being woken while it is running")
	}

	expired := sessionpkg.ProjectLifecycle(sessionpkg.LifecycleInput{
		QuarantinedUntil: info.QuarantinedUntil,
		Now:              now.Add(defaultRateLimitQuarantineDuration + time.Minute),
	})
	if expired.HasBlocker(sessionpkg.BlockerQuarantined) {
		t.Error("the wedge quarantine must lapse so the session recovers without an operator")
	}
}

// usageLimitModalStallTimeout activates the reconciler's activity-based liveness
// gate, which the modal check rides so it costs no probe of its own. It is
// deliberately far longer than usageLimitModalFrozenPane: the scenarios below
// must show the modal caught on its own short window, long before any
// progress-stall threshold is reached.
const usageLimitModalStallTimeout = "1h"

// newUsageLimitModalScenario builds the failure this bug describes: a live,
// desired session whose pane has been frozen on the usage-limit dialog. tmux and
// the provider process are both healthy — that is the whole problem.
func newUsageLimitModalScenario(t *testing.T) (*restartRequestTestEnv, beads.Bead, string) {
	t.Helper()

	env := newUsageLimitModalScenarioEnv(t)
	env.cfg.Session.ProgressStallTimeout = usageLimitModalStallTimeout
	return env.env, env.session, env.sessionName
}

// usageLimitModalScenario is the assembled scenario, kept separate from
// newUsageLimitModalScenario so one test can build it WITHOUT the activity gate.
type usageLimitModalScenario struct {
	env         *restartRequestTestEnv
	cfg         *config.City
	session     beads.Bead
	sessionName string
}

func newUsageLimitModalScenarioEnv(t *testing.T) usageLimitModalScenario {
	t.Helper()

	env := newRestartRequestTestEnv()
	env.cfg = &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "worker", StartCommand: "true", MaxActiveSessions: restartRequestTestIntPtr(1)}},
		NamedSessions: []config.NamedSession{{Template: "worker", Mode: "on_demand"}},
	}
	sessionName := config.NamedSessionRuntimeName(env.cfg.Workspace.Name, env.cfg.Workspace, "worker")
	env.desiredState[sessionName] = TemplateParams{
		Command:      "true",
		SessionName:  sessionName,
		TemplateName: "worker",
		ResolvedProvider: &config.ResolvedProvider{
			SessionIDFlag: "--session-id",
		},
	}

	session := env.createSessionBead(sessionName)
	env.setSessionMetadata(&session, map[string]string{
		namedSessionMetadataKey:      "true",
		namedSessionIdentityMetadata: "worker",
		namedSessionModeMetadata:     "on_demand",
		"state":                      "active",
		"session_key":                "wedged-key",
		"started_config_hash":        "hash-before-wedge",
	})
	if err := env.sp.Start(context.Background(), sessionName, runtime.Config{Command: "true"}); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := env.sp.SetMeta(sessionName, "GC_SESSION_ID", session.ID); err != nil {
		t.Fatalf("SetMeta(GC_SESSION_ID): %v", err)
	}
	env.sp.SetPeekOutput(sessionName, usageLimitModalPane)
	env.sp.SetActivity(sessionName, env.clk.Now().Add(-3*usageLimitModalFrozenPane))

	return usageLimitModalScenario{env: env, cfg: env.cfg, session: session, sessionName: sessionName}
}

// TestReconcileSessionBeads_UsageLimitModalScoredUnhealthy is the acceptance
// case: one reconcile pass over a session sitting on the usage-limit dialog must
// stop reporting it healthy. Before this check the pass saw a live tmux session
// wrapping a live process and left the bead awake, which is how six sessions
// stayed registered as healthy for hours with no agent behind them.
func TestReconcileSessionBeads_UsageLimitModalScoredUnhealthy(t *testing.T) {
	env, session, _ := newUsageLimitModalScenario(t)

	env.reconcile([]beads.Bead{session})

	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading session bead: %v", err)
	}
	if state := got.Metadata[sessionHealthStateMetadataKey]; state != "unhealthy" {
		t.Errorf("%s = %q, want unhealthy", sessionHealthStateMetadataKey, state)
	}
	if reason := got.Metadata[sessionHealthReasonMetadataKey]; reason != sessionHealthReasonUsageLimitModal {
		t.Errorf("%s = %q, want %q", sessionHealthReasonMetadataKey, reason, sessionHealthReasonUsageLimitModal)
	}
	until, parseErr := time.Parse(time.RFC3339, got.Metadata["quarantined_until"])
	if parseErr != nil {
		t.Fatalf("quarantined_until = %q: %v", got.Metadata["quarantined_until"], parseErr)
	}
	if !until.After(env.clk.Now()) {
		t.Errorf("quarantined_until = %s, want a live quarantine so the slot is not respawned into the same limit", until)
	}
}

// TestReconcileSessionBeads_UsageLimitModalRestartsWedgedSession pins the
// repair. Draining is useless here — a modal-frozen agent never polls
// drain-check, so it can never drain-ack — so the reconciler stops the runtime
// and hands off to a fresh session, the same recovery an operator performs by
// hand with `gc session reset`.
func TestReconcileSessionBeads_UsageLimitModalRestartsWedgedSession(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)

	env.reconcile([]beads.Bead{session})

	if env.sp.IsRunning(sessionName) {
		t.Error("the wedged runtime must be stopped: the dialog never clears itself, so only a fresh session recovers the slot")
	}
	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading session bead: %v", err)
	}
	if got.Metadata["started_config_hash"] != "" {
		t.Errorf("started_config_hash = %q, want cleared so the next wake starts a fresh conversation", got.Metadata["started_config_hash"])
	}
}

// TestReconcileSessionBeads_UsageLimitModalRidesTheActivityGate pins the cost
// contract. The desired-session fast path is allowed only the cached
// running/alive bits (#2442,
// TestReconcileSessionBeads_DesiredFastPathSkipsAttachmentActivityObservation),
// so this check probes nothing of its own: it reuses the activity read the
// progress-stall gate already pays for, and a city that opts into neither
// [session] progress_stall_timeout nor claim_holder_stall_timeout gets no probe
// at all — the wedge is then caught on the exit path instead, where the pane is
// already captured. Without this test the check could quietly go back to reading
// activity for every alive session on every tick.
func TestReconcileSessionBeads_UsageLimitModalRidesTheActivityGate(t *testing.T) {
	scenario := newUsageLimitModalScenarioEnv(t)

	scenario.env.reconcile([]beads.Bead{scenario.session})

	if got := scenario.env.sp.CountCalls("GetLastActivity", scenario.sessionName); got != 0 {
		t.Errorf("GetLastActivity calls = %d, want 0: the modal check must not probe on the desired fast path", got)
	}
	got, err := scenario.env.store.Get(scenario.session.ID)
	if err != nil {
		t.Fatalf("reading session bead: %v", err)
	}
	if reason := got.Metadata[sessionHealthReasonMetadataKey]; reason == sessionHealthReasonUsageLimitModal {
		t.Error("with no activity gate configured the modal check has no corroborating read, so it must not score the session")
	}
}

// TestReconcileSessionBeads_UsageLimitModalLeavesWorkingSessionAlone is the
// self-false-positive guard, stated in reconciler terms. A session that is
// actively drawing output — including one rendering this very dialog's text
// while investigating it — must survive the pass untouched.
func TestReconcileSessionBeads_UsageLimitModalLeavesWorkingSessionAlone(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.sp.SetActivity(sessionName, env.clk.Now().Add(-2*time.Second))

	env.reconcile([]beads.Bead{session})

	if !env.sp.IsRunning(sessionName) {
		t.Fatal("a session that drew output seconds ago must not be stopped, whatever its pane happens to render")
	}
	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading session bead: %v", err)
	}
	if reason := got.Metadata[sessionHealthReasonMetadataKey]; reason == sessionHealthReasonUsageLimitModal {
		t.Error("an actively working session must not be scored unhealthy for displaying the dialog's text")
	}
}
