package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
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
// tests can pin the lazy ordering that keeps the check cheap: the free activity
// read gates the pane capture, and the pane match gates the attachment probe.
type usageLimitModalProbes struct {
	activityCalls int
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

func (p *usageLimitModalProbes) lastActivity() (time.Time, error) {
	p.activityCalls++
	return p.activity, p.activityErr
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
	_, detected := detectUsageLimitModalWedge(alive, recorded, now, p.lastActivity, p.capturePane, p.isAttached, p.onProbeError)
	return detected
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

func TestDetectUsageLimitModalWedge_UsesProviderResetDeadline(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	probes := wedgedProbes(now)
	probes.pane = strings.Replace(
		usageLimitModalPane,
		"Enter to confirm · Esc to cancel",
		"You've hit your usage limit · try again at 3:10pm (UTC)\nEnter to confirm · Esc to cancel",
		1,
	)

	resetAt, detected := detectUsageLimitModalWedge(true, false, now, probes.lastActivity, probes.capturePane, probes.isAttached, probes.onProbeError)
	if !detected {
		t.Fatal("a frozen, unattended provider fence with a reset deadline must be detected")
	}
	want := time.Date(2026, 8, 17, 15, 10, 0, 0, time.UTC)
	if !resetAt.Equal(want) {
		t.Fatalf("reset deadline = %s, want %s", resetAt, want)
	}
}

func TestDetectUsageLimitModalWedge_DoesNotTreatResetBannerAsInteractiveWedge(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	probes := wedgedProbes(now)
	probes.pane = "Usage limit reached · resets 3:10pm (UTC)"

	resetAt, detected := detectUsageLimitModalWedge(true, false, now, probes.lastActivity, probes.capturePane, probes.isAttached, probes.onProbeError)
	if detected || !resetAt.IsZero() {
		t.Fatalf("a non-interactive reset banner is not enough evidence for a destructive restart: detected=%v reset=%s", detected, resetAt)
	}
}

func TestDetectUsageLimitModalWedge_ChoiceModalMustBeCurrentFrame(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	probes := wedgedProbes(now)
	probes.pane += "\nold log: cache resets at 3:10pm (UTC)"

	resetAt, detected := detectUsageLimitModalWedge(true, false, now, probes.lastActivity, probes.capturePane, probes.isAttached, probes.onProbeError)
	if detected || !resetAt.IsZero() {
		t.Fatalf("a choice modal followed by newer output is not the current frame: detected=%v reset=%s", detected, resetAt)
	}
}

func TestDetectUsageLimitModalWedge_DoesNotAttributeUnrelatedResetToUsageMention(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	probes := wedgedProbes(now)
	probes.pane = "old log: cache resets at 3:10pm (UTC)\nYou've hit your usage limit"

	resetAt, detected := detectUsageLimitModalWedge(true, false, now, probes.lastActivity, probes.capturePane, probes.isAttached, probes.onProbeError)
	if detected || !resetAt.IsZero() {
		t.Fatalf("unrelated reset text must not create a provider fence: detected=%v reset=%s", detected, resetAt)
	}
}

func TestDetectUsageLimitModalWedge_DoesNotMatchDisplayedFixture(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	probes := wedgedProbes(now)
	probes.pane = usageLimitModalPane + "`\nfunc TestSomething"

	if probes.detect(true, false, now) {
		t.Fatal("source displaying the modal fixture must not restart a legitimate idle session")
	}
}

func TestSelectUsageFenceProbeTargets_RoundRobinBound(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f", "g"}
	dt := newDrainTracker()
	wants := [][]string{{"a", "b", "c"}, {"d", "e", "f"}, {"g", "a", "b"}}
	for i, want := range wants {
		got := selectUsageFenceProbeTargets(ids, dt)
		if len(got) != maxUsageFenceProbesPerTick {
			t.Fatalf("pass %d selected %d targets, want %d", i+1, len(got), maxUsageFenceProbesPerTick)
		}
		for _, id := range want {
			if !got[id] {
				t.Errorf("pass %d did not select %q: got %v", i+1, id, got)
			}
		}
	}
}

func TestUsageFencePaneStableRequiresAnUnchangedObservationWindow(t *testing.T) {
	dt := newDrainTracker()
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	if dt.usageFencePaneStable("s1", usageLimitModalPane, now) {
		t.Fatal("first matching capture must not be destructive evidence")
	}
	if dt.usageFencePaneStable("s1", usageLimitModalPane, now.Add(usageLimitModalFrozenPane-time.Second)) {
		t.Fatal("unchanged capture before the full window must not be destructive evidence")
	}
	if !dt.usageFencePaneStable("s1", usageLimitModalPane, now.Add(usageLimitModalFrozenPane)) {
		t.Fatal("unchanged capture across the full window should become stable evidence")
	}
	if dt.usageFencePaneStable("s1", "healthy output", now.Add(2*usageLimitModalFrozenPane)) {
		t.Fatal("a changed pane must restart the observation window")
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
			name:      "activity probe failed",
			alive:     true,
			mutate:    func(p *usageLimitModalProbes) { p.activityErr = errors.New("no server") },
			wantProbe: "last activity",
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
	patch := usageLimitModalWedgePatch(now, time.Time{})

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
	if got := patch["state"]; got != string(sessionpkg.StateAsleep) {
		t.Errorf("state = %q, want asleep", got)
	}
	if got := patch["sleep_reason"]; got != string(sessionpkg.SleepReasonRateLimit) {
		t.Errorf("sleep_reason = %q, want rate_limit", got)
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

func TestUsageLimitModalWedgePatch_UsesParsedResetDeadline(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	resetAt := now.Add(37 * time.Minute)
	patch := usageLimitModalWedgePatch(now, resetAt)
	if got := patch["quarantined_until"]; got != resetAt.Format(time.RFC3339) {
		t.Fatalf("quarantined_until = %q, want %q", got, resetAt.Format(time.RFC3339))
	}
}

func TestUsageLimitModalWedgePatch_RejectsImplausiblyDistantResetDeadline(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	resetAt := now.Add(300 * 24 * time.Hour)
	patch := usageLimitModalWedgePatch(now, resetAt)
	want := now.Add(defaultRateLimitQuarantineDuration).Format(time.RFC3339)
	if got := patch["quarantined_until"]; got != want {
		t.Fatalf("quarantined_until = %q, want bounded fallback %q", got, want)
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
			metadata: map[string]string(usageLimitModalWedgePatch(now.Add(-time.Minute), time.Time{})),
			want:     true,
		},
		{
			name:     "quarantine expired, the wedge may be re-reported",
			metadata: map[string]string(usageLimitModalWedgePatch(now.Add(-2*defaultRateLimitQuarantineDuration), time.Time{})),
			want:     false,
		},
		{
			name: "implausibly distant durable deadline is rejected",
			metadata: map[string]string{
				sessionHealthReasonMetadataKey: sessionHealthReasonUsageLimitModal,
				"quarantined_until":            now.Add(300 * 24 * time.Hour).Format(time.RFC3339),
			},
			want: false,
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
	info := seedSessionInfo(makeBead("b1", map[string]string(usageLimitModalWedgePatch(now, time.Time{}))))

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

// newUsageLimitModalScenario builds the failure this bug describes: a live,
// desired session whose pane has been frozen on the usage-limit dialog. tmux and
// the provider process are both healthy — that is the whole problem.
func newUsageLimitModalScenario(t *testing.T) (*restartRequestTestEnv, beads.Bead, string) {
	t.Helper()

	env := newRestartRequestTestEnv()
	env.cfg = &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "worker", StartCommand: "true", MaxActiveSessions: restartRequestTestIntPtr(1)}},
		NamedSessions: []config.NamedSession{{Template: "worker", Mode: "on_demand"}},
	}
	sessionName := config.NamedSessionRuntimeName(env.cfg.Workspace.Name, env.cfg.Workspace, "worker")
	env.desiredState[sessionName] = TemplateParams{
		Command:               "true",
		SessionName:           sessionName,
		TemplateName:          "worker",
		ProviderFenceIdentity: "account:test",
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
		"instance_token":             "wedged-instance",
		"provider_fence_identity":    "account:test",
	})
	if err := env.sp.Start(context.Background(), sessionName, runtime.Config{Command: "true"}); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := env.sp.SetMeta(sessionName, "GC_SESSION_ID", session.ID); err != nil {
		t.Fatalf("SetMeta(GC_SESSION_ID): %v", err)
	}
	if err := env.sp.SetMeta(sessionName, "GC_INSTANCE_TOKEN", "wedged-instance"); err != nil {
		t.Fatalf("SetMeta(GC_INSTANCE_TOKEN): %v", err)
	}
	env.sp.SetPeekOutput(sessionName, usageLimitModalPane)
	env.sp.SetActivity(sessionName, env.clk.Now().Add(-3*usageLimitModalFrozenPane))
	// The scenario represents a pane already observed unchanged for the full
	// stability window; individual detector tests cover the first observation.
	env.dt.usageFencePanes[session.ID] = usageFencePaneObservation{
		digest:    sha256.Sum256([]byte(usageLimitModalPane)),
		firstSeen: env.clk.Now().Add(-usageLimitModalFrozenPane),
	}

	return env, session, sessionName
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
	for i := len(env.sp.Calls) - 1; i >= 0; i-- {
		call := env.sp.Calls[i]
		if call.Method != "ObserveAttachment" || call.Name != sessionName {
			continue
		}
		if i+1 >= len(env.sp.Calls) || env.sp.Calls[i+1].Method != "Stop" || env.sp.Calls[i+1].Name != sessionName {
			t.Fatalf("final attachment observation was not immediately followed by stopping the same runtime: calls[%d:] = %+v", i, env.sp.Calls[i:])
		}
		return
	}
	t.Fatal("missing final attachment observation before usage-limit restart")
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

type usageFencePersistFailStore struct {
	beads.Store
}

func (s usageFencePersistFailStore) Update(id string, opts beads.UpdateOpts) error {
	if opts.Metadata[sessionHealthReasonMetadataKey] == sessionHealthReasonUsageLimitModal {
		return errors.New("injected usage-fence persistence failure")
	}
	return s.Store.Update(id, opts)
}

type restartHandoffPersistFailStore struct {
	beads.Store
}

func (s restartHandoffPersistFailStore) Update(id string, opts beads.UpdateOpts) error {
	if opts.Metadata["continuation_reset_pending"] == "true" {
		return errors.New("injected restart-handoff persistence failure")
	}
	return s.Store.Update(id, opts)
}

type providerFenceCreateFailStore struct {
	beads.Store
}

func (s providerFenceCreateFailStore) Create(b beads.Bead) (beads.Bead, error) {
	for _, label := range b.Labels {
		if label == sessionpkg.ProviderFenceBeadLabel {
			return beads.Bead{}, errors.New("injected durable provider-fence failure")
		}
	}
	return s.Store.Create(b)
}

func TestReconcileSessionBeads_UsageLimitModalDoesNotKillWhenDurableFenceWriteFails(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.store = providerFenceCreateFailStore{Store: env.store}

	env.reconcile([]beads.Bead{session})

	if !env.sp.IsRunning(sessionName) {
		t.Fatal("runtime was stopped even though the durable provider fence could not be written")
	}
}

func TestReconcileSessionBeads_UsageLimitModalDoesNotKillWhenFencePersistenceFails(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.store = usageFencePersistFailStore{Store: env.store}

	env.reconcile([]beads.Bead{session})

	if !env.sp.IsRunning(sessionName) {
		t.Fatal("runtime was stopped even though the usage-limit fence was not durable")
	}
	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading session bead: %v", err)
	}
	if got.Metadata[sessionHealthReasonMetadataKey] == sessionHealthReasonUsageLimitModal {
		t.Fatal("usage-limit fence unexpectedly persisted through the failing store")
	}
}

func TestReconcileSessionBeads_UsageLimitModalDoesNotKillBeforeRestartHandoffIsDurable(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.store = restartHandoffPersistFailStore{Store: env.store}

	env.reconcile([]beads.Bead{session})

	if !env.sp.IsRunning(sessionName) {
		t.Fatal("runtime was stopped before the fresh-conversation restart handoff was durable")
	}
}

func TestReconcileSessionBeads_UsageLimitModalRetriesFailedKill(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.sp.StopErrors[sessionName] = errors.New("injected stop failure")

	env.reconcile([]beads.Bead{session})
	if !env.sp.IsRunning(sessionName) {
		t.Fatal("runtime unexpectedly stopped while Stop was configured to fail")
	}

	delete(env.sp.StopErrors, sessionName)
	refreshed, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading durable usage-limit fence: %v", err)
	}
	env.reconcile([]beads.Bead{refreshed})
	if env.sp.IsRunning(sessionName) {
		t.Fatal("runtime remained alive after a later tick could retry the failed stop")
	}

	stopCalls := 0
	for _, call := range env.sp.Calls {
		if call.Method == "Stop" && call.Name == sessionName {
			stopCalls++
		}
	}
	if stopCalls < 2 {
		t.Fatalf("Stop calls = %d, want at least 2 across the failed and successful ticks", stopCalls)
	}
}

func TestReconcileSessionBeads_UsageLimitModalDoesNotStopReplacementIncarnation(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.sp.StopErrors[sessionName] = errors.New("injected stop failure")

	env.reconcile([]beads.Bead{session})
	delete(env.sp.StopErrors, sessionName)
	if err := env.sp.SetMeta(sessionName, "GC_INSTANCE_TOKEN", "replacement-instance"); err != nil {
		t.Fatalf("SetMeta replacement token: %v", err)
	}

	refreshed, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading durable usage-limit fence: %v", err)
	}
	env.reconcile([]beads.Bead{refreshed})

	if !env.sp.IsRunning(sessionName) {
		t.Fatal("replacement runtime was stopped using stale evidence from the prior incarnation")
	}
	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading disarmed restart request: %v", err)
	}
	if got.Metadata["restart_requested"] == "true" {
		t.Fatal("stale restart request remained armed after the runtime instance changed")
	}
}

func TestReconcileSessionBeads_UsageLimitModalDoesNotRestartPinnedSession(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.setSessionMetadata(&session, map[string]string{"pin_awake": "true"})

	env.reconcile([]beads.Bead{session})

	if !env.sp.IsRunning(sessionName) {
		t.Fatal("pinned named session was stopped by an autonomous provider-fence repair")
	}
	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading pinned session bead: %v", err)
	}
	if got.Metadata[sessionHealthReasonMetadataKey] != sessionHealthReasonUsageLimitModal {
		t.Fatal("pinned session must retain visible provider-fence health metadata")
	}
	if got.Metadata["restart_requested"] == "true" || got.Metadata["continuation_reset_pending"] == "true" {
		t.Fatal("autonomous provider-fence repair armed an abrupt restart for a pinned session")
	}
}

func TestReconcileSessionBeads_UsageLimitModalDoesNotFenceWithoutInstanceToken(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.setSessionMetadata(&session, map[string]string{"instance_token": ""})

	env.reconcile([]beads.Bead{session})

	if !env.sp.IsRunning(sessionName) {
		t.Fatal("runtime was stopped without an instance token binding the observation to it")
	}
	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading session bead: %v", err)
	}
	if got.Metadata[sessionHealthReasonMetadataKey] == sessionHealthReasonUsageLimitModal {
		t.Fatal("usage-limit fence was persisted without a durable instance token")
	}
}

func TestReconcileSessionBeads_UsageLimitModalDoesNotRetryAfterWedgeClears(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.sp.StopErrors[sessionName] = errors.New("injected stop failure")

	env.reconcile([]beads.Bead{session})
	delete(env.sp.StopErrors, sessionName)
	env.sp.SetPeekOutput(sessionName, "healthy resumed conversation")
	env.sp.SetActivity(sessionName, env.clk.Now())

	refreshed, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading durable usage-limit fence: %v", err)
	}
	env.reconcile([]beads.Bead{refreshed})
	if !env.sp.IsRunning(sessionName) {
		t.Fatal("resolved conversation was killed solely because its old provider fence remained")
	}
	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading disarmed restart request: %v", err)
	}
	if got.Metadata["restart_requested"] == "true" {
		t.Fatal("restart request remained armed after current evidence no longer showed a wedge")
	}
}

func TestReconcileSessionBeads_UsageLimitModalDoesNotRetryOutsideProbeBudget(t *testing.T) {
	env, session, sessionName := newUsageLimitModalScenario(t)
	env.sp.StopErrors[sessionName] = errors.New("injected stop failure")
	env.reconcile([]beads.Bead{session})
	delete(env.sp.StopErrors, sessionName)
	env.sp.SetPeekOutput(sessionName, "healthy resumed conversation")
	env.sp.SetActivity(sessionName, env.clk.Now())

	refreshed, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("reading durable usage-limit fence: %v", err)
	}
	rows := []beads.Bead{refreshed}
	for i := 0; i < maxUsageFenceProbesPerTick; i++ {
		rows = append(rows, env.createSessionBead(fmt.Sprintf("probe-dummy-%d", i)))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	sourceIndex := -1
	for i := range rows {
		if rows[i].ID == session.ID {
			sourceIndex = i
			break
		}
	}
	if sourceIndex < 0 {
		t.Fatal("source session missing from probe rows")
	}
	// With max+1 rows, starting immediately after the source spends the whole
	// bounded probe budget on the other rows and leaves the source unselected.
	env.dt.usageFenceCursor = (sourceIndex + 1) % len(rows)
	env.reconcile(rows)
	if !env.sp.IsRunning(sessionName) {
		t.Fatal("recorded restart marker bypassed the bounded fresh-evidence retry path")
	}
}

func TestReconcileSessionBeads_UsageLimitFenceBlocksProviderSiblingsOnly(t *testing.T) {
	env := newRestartRequestTestEnv()
	env.cfg = &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{
			{Name: "limited-source", StartCommand: "true"},
			{Name: "limited-sibling", StartCommand: "true"},
			{Name: "healthy-sibling", StartCommand: "true"},
		},
		NamedSessions: []config.NamedSession{
			{Template: "limited-source", Mode: "always"},
			{Template: "limited-sibling", Mode: "always"},
			{Template: "healthy-sibling", Mode: "always"},
		},
	}

	makeNamed := func(template, provider string, metadata map[string]string) (beads.Bead, string) {
		name := config.NamedSessionRuntimeName(env.cfg.Workspace.Name, env.cfg.Workspace, template)
		env.desiredState[name] = TemplateParams{
			Command:               "true",
			SessionName:           name,
			TemplateName:          template,
			ProviderFenceIdentity: "account:" + provider,
			ResolvedProvider:      &config.ResolvedProvider{Name: provider},
		}
		bead := env.createSessionBead(name)
		base := map[string]string{
			"template":                   template,
			namedSessionMetadataKey:      "true",
			namedSessionIdentityMetadata: template,
			namedSessionModeMetadata:     "always",
		}
		for key, value := range metadata {
			base[key] = value
		}
		env.setSessionMetadata(&bead, base)
		return bead, name
	}

	providerFence := map[string]string(usageLimitModalWedgePatch(env.clk.Now(), time.Time{}))
	providerFence["provider_fence_identity"] = "account:provider-preset-a"
	limitedSource, limitedSourceName := makeNamed("limited-source", "provider-preset-a", providerFence)
	limitedSibling, limitedName := makeNamed("limited-sibling", "provider-preset-a", nil)
	healthySibling, healthyName := makeNamed("healthy-sibling", "provider-preset-b", nil)
	// The durable fence must remain authoritative after the source role leaves
	// desired state; otherwise siblings can restart into the same exhausted
	// provider preset during role migration.
	delete(env.desiredState, limitedSourceName)

	env.reconcile([]beads.Bead{limitedSource, limitedSibling, healthySibling})

	if env.sp.IsRunning(limitedName) {
		t.Fatal("same-provider-preset sibling started while the usage fence was active")
	}
	if !env.sp.IsRunning(healthyName) {
		t.Fatal("different-provider sibling did not start; the usage fence escaped its provider boundary")
	}
}

func TestProviderUsageFenceIdentityUsesEffectiveAccountBoundary(t *testing.T) {
	t.Parallel()
	cityPath := t.TempDir()
	base, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{
		Name:            "claude-personal-max",
		BuiltinAncestor: "claude",
	}, map[string]string{"ANTHROPIC_API_KEY": "personal-secret"})
	if err != nil {
		t.Fatal(err)
	}
	alias, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{
		Name:            "claude-personal-sonnet",
		BuiltinAncestor: "claude",
	}, map[string]string{"CUSTOM_AUTH_TOKEN": "personal-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if alias != base {
		t.Fatalf("same account through provider aliases produced different identities: got %q, want %q", alias, base)
	}
	differentAccount, err := providerUsageFenceIdentityForCity(cityPath, &config.ResolvedProvider{
		Name:            "claude-personal-max",
		BuiltinAncestor: "claude",
	}, map[string]string{"ANTHROPIC_API_KEY": "enterprise-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if differentAccount == base {
		t.Fatalf("same provider preset with different account environment shared identity %q", base)
	}
}

func TestRecordedProviderUsageFenceIdentitySurvivesConfigChange(t *testing.T) {
	t.Parallel()

	info := sessionpkg.Info{ProviderFenceIdentity: "account:old"}
	if got := recordedProviderUsageFenceIdentity(info, "account:new"); got != "account:old" {
		t.Fatalf("recorded identity = %q, want account:old", got)
	}
	if got := recordedProviderUsageFenceIdentity(sessionpkg.Info{}, "account:current"); got != "account:current" {
		t.Fatalf("legacy fallback identity = %q, want account:current", got)
	}
}
