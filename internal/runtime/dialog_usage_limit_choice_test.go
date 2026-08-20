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
	if containsClaudeUsageLimitChoiceModal(scattered) {
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
		if containsClaudeUsageLimitChoiceModal(content) {
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
