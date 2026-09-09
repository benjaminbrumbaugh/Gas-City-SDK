package runtime

import "testing"

// paneUsageLimitChoiceModal is the pane the six wedged sessions were parked on,
// transcribed from gc-gqk. The CLI renders it when the account usage limit is
// hit and then blocks on a keypress; it never self-clears, not even after the
// limit resets, because clearing it requires input no one is there to send.
const paneUsageLimitChoiceModal = `⏵⏵ accept edits on

  What do you want to do?
  > 1. Stop and wait for limit to reset
    2. Ask your admin for more usage

  Enter to confirm · Esc to cancel
`

// TestContainsProviderRateLimitScreen_DetectsUsageLimitChoiceModal is the
// gc-gqk regression. tmux and the claude process both stay alive while this
// modal is up, so every liveness signal the supervisor has reports the session
// healthy — six sessions sat wedged for up to four hours, including
// gastown.deacon and the gastown.boot watchdog whose job was to notice exactly
// this. The pane was the only place the failure was visible, and no classifier
// looked at it: ContainsRateLimitDialog, ContainsProviderRateLimitScreen,
// ContainsModelSwitchModal and ProviderTerminalErrorReason all returned
// false/empty for this content.
func TestContainsProviderRateLimitScreen_DetectsUsageLimitChoiceModal(t *testing.T) {
	if !ContainsProviderRateLimitScreen(paneUsageLimitChoiceModal) {
		t.Error("ContainsProviderRateLimitScreen = false, want true — a pane parked on the usage-limit choice modal is a wedged session, not a healthy one")
	}
}

// TestUsageLimitChoiceModal_RequiresOneOnScreenBlock guards the direction that
// matters more than detection. Consumers peek with CapturePane, which reads
// scrollback (`-S -N`), so a matcher that ORs loose tokens across the whole
// buffer misclassifies a healthy session as rate-limited — and the existing
// quarantine path re-detects the same scrollback every cycle, so a false
// positive masks a real crash indefinitely with no self-heal. The anchors must
// therefore co-occur inside one on-screen block, exactly as the sibling
// spend-limit matcher requires.
func TestUsageLimitChoiceModal_RequiresOneOnScreenBlock(t *testing.T) {
	scattered := "What do you want to do?\n" +
		"line\nline\nline\nline\nline\nline\nline\nline\n" +
		"> 1. Stop and wait for limit to reset\n" +
		"line\nline\nline\nline\nline\nline\nline\nline\n" +
		"  2. Ask your admin for more usage\n"
	if ContainsUsageLimitChoiceModal(scattered) {
		t.Error("matched anchors smeared across unrelated scrollback; a false positive here quarantines a healthy session and masks a real crash")
	}
}

// TestUsageLimitChoiceModal_PartialAnchorsDoNotMatch pins that no single
// fragment is sufficient. An agent that merely prints or quotes one line of the
// modal — reading this very bead, for instance — must not be classified as
// wedged.
func TestUsageLimitChoiceModal_PartialAnchorsDoNotMatch(t *testing.T) {
	for name, content := range map[string]string{
		"only the stop option":  "  > 1. Stop and wait for limit to reset\n",
		"only the admin option": "    2. Ask your admin for more usage\n",
		"only the question":     "  What do you want to do?\n",
		"prose about it":        "The session wedged on a usage limit and nobody noticed.\n",
	} {
		if ContainsUsageLimitChoiceModal(content) {
			t.Errorf("%s: matched, want no match", name)
		}
	}
}

// TestUsageLimitChoiceModal_DoesNotDisturbSiblingClassifiers pins that adding
// this modal leaves the neighboring judgements alone: it is a rate-limit
// screen, not a terminal provider error (which would mark the session
// permanently failed) and not the model-switch modal (which gets keystrokes).
func TestUsageLimitChoiceModal_DoesNotDisturbSiblingClassifiers(t *testing.T) {
	if got := ProviderTerminalErrorReason(paneUsageLimitChoiceModal); got != "" {
		t.Errorf("ProviderTerminalErrorReason = %q, want empty — a usage limit is transient, not terminal", got)
	}
	if ContainsModelSwitchModal(paneUsageLimitChoiceModal) {
		t.Error("ContainsModelSwitchModal = true, want false — this modal offers no cheaper model")
	}
}

// paneUsageLimitChoiceSweepEcho is the false positive that a witness sweeping
// for this modal actually produced: grepping every pane for the modal's anchor
// phrases echoes all of them onto the observing pane's OWN command line. That
// pane is healthy — it is running a grep — so classifying it as rate-limited
// quarantines a working session, and because the quarantine re-detects the same
// scrollback every cycle it never self-heals.
const paneUsageLimitChoiceSweepEcho = "$ for p in $(tmux list-panes -aF '#{pane_id}'); do tmux capture-pane -pt \"$p\" | " +
	"grep -q 'What do you want to do?' && grep -q 'Stop and wait for limit to reset' && " +
	"grep -q 'Ask your admin for more usage' && echo \"$p wedged\"; done\n"

// TestUsageLimitChoiceModal_RejectsSingleLineAnchorEcho pins that the anchors
// must be distributed the way the real modal renders them — a question, then
// two option ROWS — instead of merely co-occurring inside the window. A window
// match alone is satisfied by one line carrying every phrase, which is exactly
// the shape a sweep command produces.
func TestUsageLimitChoiceModal_RejectsSingleLineAnchorEcho(t *testing.T) {
	if ContainsUsageLimitChoiceModal(paneUsageLimitChoiceSweepEcho) {
		t.Error("matched a single command line echoing every anchor; a healthy pane that merely greps for the modal must not be quarantined")
	}
}

// usageLimitChoiceModalPane is the provider's usage-limit choice modal exactly
// as it renders in a wedged pane: a header, two options with the selection glyph
// on the first, and the confirm footer.
const usageLimitChoiceModalPane = `● Bash(go test ./...)
  ⎿  ok  github.com/gastownhall/gascity/internal/runtime

What do you want to do?
❯ 1. Stop and wait for limit to reset
  2. Ask your admin for more usage
Enter to confirm · Esc to cancel`

// claudeSpendLimitModalPane is the sibling spend-limit modal, which shares this
// modal's header and footer but offers different arms. containsClaudeSpendLimitModal
// already covers it, so the usage-limit matcher must not also claim it.
const claudeSpendLimitModalPane = `What do you want to do?
Usage credit balance: $573.37
❯ Adjust monthly spend limit: $1503.19
  Wait for limit to reset      Resets Jul 12 at 11pm (America/Los_Angeles)
Enter to confirm · Esc to cancel`

// usageLimitObserverGrepEcho is the false positive the witness that found this
// modal actually hit: sweeping every pane for the option text echoes BOTH option
// phrases onto the observing pane's own command line, so a naive whole-buffer
// match reports the healthy observer as wedged.
const usageLimitObserverGrepEcho = `$ for s in $(tmux list-sessions -F '#{session_name}'); do
>   tmux capture-pane -p -t "$s" | grep -qiE 'Stop and wait for limit to reset|Ask your admin for more usage' && echo "$s WEDGED"
> done
gastown__deacon WEDGED
gastown__boot WEDGED`

// usageLimitPhrasesScatteredScrollback holds every anchor, but spread far enough
// apart that no small window carries them together — a pane paging through
// source or notes that merely mention the modal.
const usageLimitPhrasesScatteredScrollback = `$ less notes.md
the dialog offers Stop and wait for limit to reset as its first arm
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
and Ask your admin for more usage as its second, which is paid spend
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
scrollback line unrelated to any modal
❯ every such dialog closes on Enter to confirm`

func TestContainsUsageLimitChoiceModal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "wedged pane", content: usageLimitChoiceModalPane, want: true},
		{
			// The capture recorded on the incident bead, whose glyph and footer
			// separator both rendered as ASCII.
			name:    "incident capture, ASCII glyph and separator",
			content: "What do you want to do?\n> 1. Stop and wait for limit to reset\n  2. Ask your admin for more usage\nEnter to confirm - Esc to cancel",
			want:    true,
		},
		{
			name:    "selection on the paid-spend arm still matches",
			content: "What do you want to do?\n  1. Stop and wait for limit to reset\n❯ 2. Ask your admin for more usage\nEnter to confirm · Esc to cancel",
			want:    true,
		},
		{
			name:    "unnumbered options inside a bordered modal",
			content: "│ What do you want to do?\n│ ❯ Stop and wait for limit to reset\n│   Ask your admin for more usage\n│ Enter to confirm · Esc to cancel",
			want:    true,
		},
		{name: "observer pane echoing its own grep sweep", content: usageLimitObserverGrepEcho, want: false},
		{name: "anchors scattered across unrelated scrollback", content: usageLimitPhrasesScatteredScrollback, want: false},
		{
			name:    "both options on one line cannot be a two-row menu",
			content: "❯ compare Stop and wait for limit to reset with Ask your admin for more usage\nEnter to confirm · Esc to cancel",
			want:    false,
		},
		{
			name:    "options without the confirm footer",
			content: "What do you want to do?\n❯ 1. Stop and wait for limit to reset\n  2. Ask your admin for more usage",
			want:    false,
		},
		{
			name:    "footer and options without a selection glyph",
			content: "What do you want to do?\n  1. Stop and wait for limit to reset\n  2. Ask your admin for more usage\nEnter to confirm · Esc to cancel",
			want:    false,
		},
		{name: "sibling spend-limit modal is a different dialog", content: claudeSpendLimitModalPane, want: false},
		{name: "normal output", content: "Hello world", want: false},
		{name: "empty", content: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsUsageLimitChoiceModal(tt.content); got != tt.want {
				t.Errorf("ContainsUsageLimitChoiceModal() = %v, want %v\ncontent:\n%s", got, tt.want, tt.content)
			}
		})
	}
}

// TestUsageLimitChoiceModalNeverDrivesKeystrokes pins the safety rule that makes
// this modal different from every other dialog gc handles: its second option
// asks the account owner for more usage, which is paid spend. The startup
// helpers answer whatever ContainsRateLimitDialog matches by pressing Down then
// Enter — on this modal that keystroke pair selects exactly that paid option, so
// the modal must stay out of the permissive startup matcher and be recognized
// only by the classification matcher.
func TestUsageLimitChoiceModalNeverDrivesKeystrokes(t *testing.T) {
	t.Parallel()
	if ContainsRateLimitDialog(usageLimitChoiceModalPane) {
		t.Error("ContainsRateLimitDialog must not match the usage-limit choice modal: the startup handler answers its matches with Down+Enter, which selects the paid-spend option")
	}
	if !ContainsProviderRateLimitScreen(usageLimitChoiceModalPane) {
		t.Error("ContainsProviderRateLimitScreen must classify the usage-limit choice modal as a rate-limit screen")
	}
}
