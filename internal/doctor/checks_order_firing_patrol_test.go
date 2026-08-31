package doctor

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/orders"
)

// gc-pdyfp.5: a live doctor run warned that gate-sweep was overdue while order
// history showed uninterrupted successful dispatches every 30-80 seconds and
// order-outcome-healthy stayed green.
//
// The order's cooldown is 30s and daemon.patrol_interval is also 30s. Nothing
// can be dispatched between patrol ticks, so an order whose cooldown expires
// just after a tick waits most of a further period for the next one — a healthy
// gap of 60s and more. The check warned at 1.5x the order's own interval, 45s,
// with no knowledge of how often the scheduler looks. It was measuring the
// order and ignoring the clock that drives it.
//
// A false overdue warning is not only noise: order-firing-current is the signal
// that a scheduler gap is real, and one that cries wolf every cycle is one
// nobody reads when the gap is real.

const (
	testCooldown = 30 * time.Second
	testPatrol   = 30 * time.Second
)

func cooldownOrder() orders.Order {
	return orders.Order{Name: "gate-sweep", Trigger: "cooldown"}
}

// classifyAge is the reported scenario: the gate-sweep cooldown order last
// fired age ago, on a city whose patrol interval is patrol.
func classifyAge(age, patrol time.Duration) (CheckStatus, string) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	status, _, detail := classifyOrderFiring(
		cooldownOrder(), now, testCooldown, patrol,
		now.Add(-age),          // last fired
		now.Add(-24*time.Hour), // controller long since started
	)
	return status, detail
}

// The reported case. Every one of these gaps is the scheduler working
// correctly, and every one of them warned before the fix.
func TestOrderFiringHealthySubMinuteCooldownDoesNotWarn(t *testing.T) {
	for _, age := range []time.Duration{
		30 * time.Second, // fired on the very next tick
		45 * time.Second, // the OLD warning threshold, exactly
		50 * time.Second, // the middle of the observed 30-80s band
		60 * time.Second, // cooldown expired just after a tick, waited a whole period
		75 * time.Second, // the maximum legitimate window: expected + patrol + jitter
	} {
		status, detail := classifyAge(age, testPatrol)
		if status != StatusOK {
			t.Errorf("a %s gap on a %s cooldown with a %s patrol reported %v, want OK.\n"+
				"Nothing can be dispatched between ticks, so this gap is the scheduler working:\n  %s",
				age, testCooldown, testPatrol, status, detail)
		}
	}
}

// The check must still fire when a tick was available and the order did not.
func TestOrderFiringStillWarnsOnceAWindowIsMissed(t *testing.T) {
	warnAt, errorAt := orderFiringThresholds(testCooldown, testPatrol)

	if status, detail := classifyAge(warnAt, testPatrol); status != StatusWarning {
		t.Fatalf("at the warn threshold %s status = %v, want warning: %s", warnAt, status, detail)
	}
	if status, detail := classifyAge(warnAt-time.Second, testPatrol); status != StatusOK {
		t.Fatalf("one second below the warn threshold status = %v, want OK: %s", status, detail)
	}
	if status, detail := classifyAge(errorAt, testPatrol); status != StatusError {
		t.Fatalf("at the error threshold %s status = %v, want error: %s", errorAt, status, detail)
	}
	// A genuinely dead order is still caught, loudly.
	if status, _ := classifyAge(time.Hour, testPatrol); status != StatusError {
		t.Fatalf("an hour with no dispatch on a 30s cooldown reported %v, want error", status)
	}
}

// The floor must not relax orders whose interval already dwarfs the patrol
// period. Those keep exactly the thresholds they have today, which is what
// keeps "genuinely stale stays covered" true rather than merely asserted.
func TestOrderFiringLongIntervalsKeepTheirExistingThresholds(t *testing.T) {
	for _, expected := range []time.Duration{
		30 * time.Minute,
		time.Hour,
		4 * time.Hour,
		24 * time.Hour,
	} {
		warnAt, errorAt := orderFiringThresholds(expected, testPatrol)
		if want := expected + expected/2; warnAt != want {
			t.Errorf("expected=%s: warn threshold moved to %s, want the original %s", expected, warnAt, want)
		}
		if want := expected * 3; errorAt != want {
			t.Errorf("expected=%s: error threshold moved to %s, want the original %s", expected, errorAt, want)
		}
	}
}

// Where the patrol period is the dominant term, the thresholds must come from
// it rather than from the order's interval.
func TestOrderFiringShortIntervalsGetAPatrolAwareFloor(t *testing.T) {
	warnAt, errorAt := orderFiringThresholds(testCooldown, testPatrol)
	if oldWarn := testCooldown + testCooldown/2; warnAt <= oldWarn {
		t.Fatalf("warn threshold %s is not above the old %s; the flapping case is unchanged", warnAt, oldWarn)
	}
	// The maximum legitimate window must sit strictly below the warn threshold,
	// or a healthy order sits exactly on the boundary and flaps again.
	healthyMax := testCooldown + testPatrol + orderFiringSchedulerJitter
	if warnAt <= healthyMax {
		t.Fatalf("warn threshold %s is not above the maximum legitimate window %s", warnAt, healthyMax)
	}
	if errorAt <= warnAt {
		t.Fatalf("error threshold %s is not above the warn threshold %s", errorAt, warnAt)
	}
}

// A slower patrol widens the window further: the same order on a city that
// looks every five minutes legitimately fires much later.
func TestOrderFiringWindowScalesWithTheConfiguredPatrolInterval(t *testing.T) {
	fast, _ := orderFiringThresholds(testCooldown, 30*time.Second)
	slow, _ := orderFiringThresholds(testCooldown, 5*time.Minute)
	if slow <= fast {
		t.Fatalf("a 5m patrol produced warn threshold %s, not greater than the 30s patrol's %s;\n"+
			"the check would warn on a city that simply looks less often", slow, fast)
	}
	// And it must read the value from config rather than assume 30s.
	if status, detail := classifyAge(4*time.Minute, 5*time.Minute); status != StatusOK {
		t.Fatalf("a 4m gap on a city with a 5m patrol reported %v, want OK: %s", status, detail)
	}
}

// Never-fired uses the same window. A cooldown order on a controller that has
// only just started must not be called dead before a tick could have run it.
func TestOrderFiringNeverFiredRespectsTheSchedulerWindow(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	warnAt, _ := orderFiringThresholds(testCooldown, testPatrol)

	status, _, detail := classifyOrderFiring(
		cooldownOrder(), now, testCooldown, testPatrol,
		time.Time{}, now.Add(-50*time.Second),
	)
	if status != StatusOK {
		t.Fatalf("50s of uptime on a 30s cooldown with a 30s patrol reported %v, want OK: %s", status, detail)
	}

	status, _, detail = classifyOrderFiring(
		cooldownOrder(), now, testCooldown, testPatrol,
		time.Time{}, now.Add(-(warnAt + time.Second)),
	)
	if status != StatusError {
		t.Fatalf("uptime past the window with no dispatch reported %v, want error: %s", status, detail)
	}
}

// A missing or nonsensical patrol interval must degrade to the original
// behavior rather than panic or silently widen the window without bound.
func TestOrderFiringThresholdsTolerateAnAbsentPatrolInterval(t *testing.T) {
	for _, patrol := range []time.Duration{0, -time.Minute} {
		warnAt, errorAt := orderFiringThresholds(testCooldown, patrol)
		if warnAt < testCooldown+testCooldown/2 {
			t.Errorf("patrol=%s: warn threshold %s fell below the original rule", patrol, warnAt)
		}
		if errorAt < testCooldown*3 {
			t.Errorf("patrol=%s: error threshold %s fell below the original rule", patrol, errorAt)
		}
	}
}

// The end-to-end shape, through the whole check rather than through
// classifyOrderFiring alone.
//
// The unit tests above hand the patrol interval in directly, so none of them
// exercises the plumbing that reads it from config — a check that resolved the
// wrong field, or silently used a zero, would pass every one of them and still
// flap in production. This one drives the real Run() against a real city
// directory with a real order file and a real event log, so the value has to
// arrive from cfg.Daemon to make the assertion hold.
func TestOrderFiringCurrent_HealthySubMinuteCooldownIsNotOverdue(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	cfg.Daemon.PatrolInterval = "30s" // the live city's value
	writeOrderFiringTestOrder(t, cityPath, "gate-sweep", "cooldown", "30s")
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-8 * time.Hour)},
		// Fired 50s ago: squarely inside the 30-80s band the live incident
		// showed, and 5s past the old 45s warning threshold.
		events.Event{Type: events.OrderFired, Subject: "gate-sweep", Ts: now.Add(-50 * time.Second)},
	)

	result := runOrderFiringCurrentTest(t, cfg, cityPath, now)
	if result.Status != StatusOK {
		t.Fatalf("status = %v, want OK.\nA 50s gap on a 30s cooldown with a 30s patrol is the scheduler working:\n  msg = %s\n  details = %v",
			result.Status, result.Message, result.Details)
	}
}

// The same city, with a genuinely stale order, must still fail. Widening the
// window for sub-minute cooldowns must not blunt the check that a scheduler gap
// is real.
func TestOrderFiringCurrent_StaleSubMinuteCooldownStillReported(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	cityPath, cfg := orderFiringTestCity(t)
	cfg.Daemon.PatrolInterval = "30s"
	writeOrderFiringTestOrder(t, cityPath, "gate-sweep", "cooldown", "30s")
	writeOrderFiringTestEvents(t, cityPath,
		events.Event{Type: events.ControllerStarted, Ts: now.Add(-8 * time.Hour)},
		events.Event{Type: events.OrderFired, Subject: "gate-sweep", Ts: now.Add(-30 * time.Minute)},
	)

	result := runOrderFiringCurrentTest(t, cfg, cityPath, now)
	if result.Status == StatusOK {
		t.Fatalf("a 30-minute gap on a 30s cooldown reported OK; the check has been blunted.\n  details = %v",
			result.Details)
	}
}

// A city that configures a slower patrol must widen the window through config,
// not through a constant. Same order, same 4-minute gap: overdue at a 30s
// patrol, healthy at a 5m one.
func TestOrderFiringCurrent_WindowFollowsConfiguredPatrolInterval(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	run := func(patrol string) *CheckResult {
		cityPath, cfg := orderFiringTestCity(t)
		cfg.Daemon.PatrolInterval = patrol
		writeOrderFiringTestOrder(t, cityPath, "gate-sweep", "cooldown", "30s")
		writeOrderFiringTestEvents(t, cityPath,
			events.Event{Type: events.ControllerStarted, Ts: now.Add(-8 * time.Hour)},
			events.Event{Type: events.OrderFired, Subject: "gate-sweep", Ts: now.Add(-4 * time.Minute)},
		)
		return runOrderFiringCurrentTest(t, cfg, cityPath, now)
	}

	if got := run("30s"); got.Status == StatusOK {
		t.Fatalf("a 4m gap with a 30s patrol reported OK; that IS a missed window: %v", got.Details)
	}
	if got := run("5m"); got.Status != StatusOK {
		t.Fatalf("a 4m gap with a 5m patrol reported %v, want OK — the check is not reading daemon.patrol_interval: %v",
			got.Status, got.Details)
	}
}
