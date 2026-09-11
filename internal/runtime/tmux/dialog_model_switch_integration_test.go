//go:build integration

package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// buildModelSwitchModalAgent compiles a fake agent that prints the Codex/GPT
// "approaching rate limits — switch model?" modal, then treats the next Enter as
// confirming "Keep current model" by printing a MODEL_KEPT marker. It stays
// alive so the tmux pane persists for capture.
func buildModelSwitchModalAgent(t *testing.T, dir, name string) string {
	t.Helper()
	bin := dir + "/" + name
	src := dir + "/" + name + ".go"
	prog := `package main
import ("bufio";"fmt";"os")
func main(){
	fmt.Println("Approaching rate limits")
	fmt.Println("Switch to gpt-5.4-mini for lower credit usage?")
	fmt.Println("  1. Switch to gpt-5.4-mini")
	fmt.Println("  2. Keep current model")
	fmt.Println("  3. Keep current model (never show again)")
	fmt.Println("Press enter to confirm or esc to go back")
	r:=bufio.NewReader(os.Stdin)
	for{
		b,err:=r.ReadByte()
		if err!=nil{ return }
		if b=='\r'||b=='\n'{
			fmt.Println("MODEL_KEPT modal dismissed keeping current model")
			continue
		}
	}
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", src, err)
	}
	build := exec.Command("go", "build", "-o", bin, src)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", name, err, string(out))
	}
	return bin
}

// waitForPaneText polls a real pane until it contains want. Waiting on the
// pane's observable state avoids making startup readiness depend on a fixed
// wall-clock sleep, while preserving the test's real-tmux boundary.
func waitForPaneText(t *testing.T, capture func() (string, error), want, what string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		content, err := capture()
		if err != nil {
			t.Fatalf("capture while waiting for %s: %v", what, err)
		}
		if strings.Contains(content, want) {
			return content
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s:\n%s", what, content)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestDismissModelSwitchModalIfPresentClearsModalOnRealTmux proves ga-3syh's fix
// end-to-end on real tmux: a session showing the mid-session model-switch modal
// is dismissed to "Keep current model" (Down+Enter) so it stops hanging.
func TestDismissModelSwitchModalIfPresentClearsModalOnRealTmux(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not installed")
	}
	tm := testTmux()
	dir := t.TempDir()
	fake := buildModelSwitchModalAgent(t, dir, "fakecodex")
	sessionName := fmt.Sprintf("gt-test-modal-%d", time.Now().UnixNano()%100000)

	_ = tm.KillSession(sessionName)
	if err := tm.NewSessionWithCommandAndEnv(sessionName, dir, fake, map[string]string{
		"GC_PROVIDER": "codex",
	}); err != nil {
		t.Fatalf("NewSessionWithCommandAndEnv: %v", err)
	}
	defer func() { _ = tm.KillSession(sessionName) }()

	capture := func() (string, error) { return tm.CapturePaneAll(sessionName) }
	// Precondition: the pane shows the model-switch modal.
	pre := waitForPaneText(t, capture, "Keep current model", "the model-switch modal")
	if !strings.Contains(pre, "Switch to ") {
		t.Fatalf("precondition: model-switch modal not shown:\n%s", pre)
	}

	tm.DismissModelSwitchModalIfPresent(sessionName)
	waitForPaneText(t, capture, "MODEL_KEPT", "the modal to be dismissed")
}
