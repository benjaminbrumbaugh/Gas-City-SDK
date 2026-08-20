package runtime

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestTerminateManagedProcessBoundsPostKillWait(t *testing.T) {
	cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		_ = SignalProcessGroup(cmd, syscall.SIGKILL)
		_ = cmd.Wait()
	}()

	done := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- TerminateManagedProcess(cmd, done, 10*time.Millisecond)
	}()

	select {
	case err := <-finished:
		if !errors.Is(err, ErrManagedProcessReapTimeout) {
			t.Fatalf("TerminateManagedProcess error = %v, want ErrManagedProcessReapTimeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TerminateManagedProcess waited indefinitely after SIGKILL")
	}
}

func TestTerminateManagedProcessReturnsWhenProcessDoneCloses(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	if err := TerminateManagedProcess(cmd, done, 10*time.Millisecond); err != nil {
		t.Fatalf("TerminateManagedProcess: %v", err)
	}
}
