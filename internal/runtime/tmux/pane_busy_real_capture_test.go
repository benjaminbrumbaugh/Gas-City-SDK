package tmux

import "testing"

// gc-uaa56. TestPaneContainsBusyIndicator feeds synthesized single lines, so
// every one of its "esc to interrupt" rows stays green with claudeBusySpinnerRe
// deleted. On a real pane that literal is not there to fall back on: `tmux
// capture-pane` truncates at the pane width, and on current Claude Code the
// footer carrying it runs past 80 columns and arrives as "… · esc t…".
//
// So the spinner regex is the ONLY signal holding submit confirmation up for
// every Claude-backed agent in the city, and nothing said so. An unconfirmed
// submit requeues the nudge and spends one of its bounded attempts while the
// paste has usually already landed, which is gc-trild: the same reminder on
// three consecutive turns, filed as a false wake.
//
// These fixtures are verbatim `tmux capture-pane -p` output from two panes that
// were genuinely mid-turn when captured, 2026-08-31.

// busyMayorPane is gastown__mayor at 80 columns, five minutes into a turn.
var busyMayorPane = []string{
	"       4 #",
	"       5 # This is the second observer for the Mayor -> Hermes push path",
	"       6 # it exists because every naive probe of that path reported gree",
	"         n straight",
	"       7 # through a total outage.",
	"     … +388 lines",
	"",
	"  Checking how existing tests resolve script paths",
	"  ⎿  $ cd \"/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City\" && chmod +x",
	"     assets/scripts/external-coordination-liveness.sh && bash",
	"",
	"✢ Bloviating… (5m 14s · ↓ 21.3k tokens)",
	"  ⎿  Tip: Use /btw to ask a quick side question without interrupting Claude's",
	"     current work",
	"",
	"────────────────────────────────────────────────────────────────────────────────",
	"❯ ",
	"────────────────────────────────────────────────────────────────────────────────",
	// The truncation. This line is where "esc to interrupt" would be.
	"  ⏵⏵ bypass permissions on (shift+tab to cycle) · 3 memories recalled · esc t…",
}

// busyCityWorkerPane is a second agent's pane, captured the same way, so the
// assertion does not rest on one session's chrome.
var busyCityWorkerPane = []string{
	"✻ Marinating… (12m 1s · ↓ 47.2k tokens)",
	"  ⎿  Tip: Use /btw to ask a quick side question without interrupting Claude's",
	"     current work",
	"",
	"────────────────────────────────────────────────────────────────────────────────",
	"❯ ",
	"────────────────────────────────────────────────────────────────────────────────",
	"  ⏵⏵ bypass permissions on (shift+tab to cycle) · 3 memories recalled · esc t…",
}

func TestPaneContainsBusyIndicatorOnRealBusyClaudeCapture(t *testing.T) {
	for name, pane := range map[string][]string{
		"gastown__mayor": busyMayorPane,
		"city-worker":    busyCityWorkerPane,
	} {
		if !paneContainsBusyIndicator(pane) {
			t.Fatalf("%s: a genuinely busy Claude pane did not read as busy; every nudge to this agent would be requeued as an unconfirmed submit", name)
		}
	}
}

// The point of the bead: on this TUI the three literal matchers contribute
// nothing, so a change that keeps them and drops the regex looks safe and is
// not. Assert their inertness rather than leaving it to be rediscovered from an
// incident.
func TestRealBusyClaudeCaptureMatchesOnlyTheSpinner(t *testing.T) {
	literals := []string{
		"esc to interrupt",
		"Press Esc or Ctrl+C to cancel",
		"[current working directory ",
	}
	for name, pane := range map[string][]string{
		"gastown__mayor": busyMayorPane,
		"city-worker":    busyCityWorkerPane,
	} {
		for _, line := range pane {
			for _, lit := range literals {
				if contains(line, lit) {
					// Not a failure of the product — a signal that the fixture
					// no longer demonstrates what it was captured to
					// demonstrate, and that the load-bearing claim below needs
					// re-measuring against a fresh capture.
					t.Fatalf("%s: fixture line %q contains %q; recapture a busy pane and re-check which matcher is load-bearing", name, line, lit)
				}
			}
		}
		spinner := 0
		for _, line := range pane {
			if claudeBusySpinnerRe.MatchString(line) {
				spinner++
			}
		}
		if spinner != 1 {
			t.Fatalf("%s: claudeBusySpinnerRe matched %d line(s), want exactly 1 — it is the only signal this capture carries", name, spinner)
		}
	}
}

// contains avoids importing strings for one call in a file whose whole point is
// the matcher's behavior.
func contains(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
