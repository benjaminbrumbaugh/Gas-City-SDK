//go:build integration

package integration

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func runOwnedIntegrationProcessHelper() bool {
	mode := os.Getenv("GC_INTEGRATION_OWNED_PROCESS_HELPER")
	if mode == "" || len(os.Args) < 3 || os.Args[1] != "supervisor" || os.Args[2] != "run" {
		return false
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	if mode == "child" {
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		<-signals
		return true
	}
	if mode != "root" {
		return false
	}
	child := buildCommand(context.Background(), "", append(os.Environ(), "GC_INTEGRATION_OWNED_PROCESS_HELPER=child"), os.Args[0], "supervisor", "run")
	out, err := child.StdoutPipe()
	if err != nil {
		panic(err)
	}
	if err := child.Start(); err != nil {
		panic(err)
	}
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "ready\n" {
		panic("child not ready")
	}
	fmt.Println(child.Process.Pid)
	<-signals
	return true
}

func TestOwnedIntegrationActualDescendantCleanup(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "gc")
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := buildCommand(ctx, "", append(os.Environ(), "GC_INTEGRATION_OWNED_PROCESS_HELPER=root"), binary, "supervisor", "run")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	// A direct child's unreaped PID cannot be recycled during failure teardown.
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-waited })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		go func() { waited <- cmd.Wait() }()
		t.Fatal(err)
	}
	child, err := strconv.Atoi(line[:len(line)-1])
	if err != nil {
		go func() { waited <- cmd.Wait() }()
		t.Fatal(err)
	}
	childStart, err := integrationProcessStartTime(child)
	if err != nil {
		go func() { waited <- cmd.Wait() }()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if start, err := integrationProcessStartTime(child); err == nil && start == childStart {
			_ = syscall.Kill(child, syscall.SIGKILL)
		}
	})
	// Start Wait after observing readiness; closing StdoutPipe before that races
	// the black-box handshake. Reap the root promptly during the sweep.
	go func() { waited <- cmd.Wait() }()
	oldRoot := integrationRunRoot
	integrationRunRoot = root
	t.Cleanup(func() { integrationRunRoot = oldRoot })
	snapshot := readProcessSnapshot()
	selected := subprocessTestKillSet(snapshot, root)
	if !selected[cmd.Process.Pid] || !selected[child] {
		t.Fatalf("owned root/descendant absent: %v", selected)
	}
	sweepSubprocessTestProcesses()
	if start, err := integrationProcessStartTime(child); err == nil && start == childStart {
		// A zombie has exited but may await launchd reaping on macOS.
		if err := syscall.Kill(child, 0); err == nil {
			t.Fatalf("owned TERM-resistant descendant %d survived identity-bound escalation", child)
		}
	}
	t.Logf("native owned root %d and TERM-resistant descendant %d cleaned", cmd.Process.Pid, child)
}

func TestOwnedIntegrationEscalationRejectsChangedIdentity(t *testing.T) {
	oldSnapshot, oldSignal, oldPause, oldStart := integrationCleanupSnapshot, integrationCleanupSignal, integrationCleanupPause, integrationCleanupStartTime
	oldRoot := integrationRunRoot
	t.Cleanup(func() {
		integrationCleanupSnapshot, integrationCleanupSignal, integrationCleanupPause, integrationCleanupStartTime = oldSnapshot, oldSignal, oldPause, oldStart
		integrationRunRoot = oldRoot
	})
	integrationRunRoot = t.TempDir()
	for _, change := range []string{"start", "ancestry", "unreadable"} {
		t.Run(change, func(t *testing.T) {
			changed := false
			integrationCleanupSnapshot = func() map[int]procSnapshot {
				ppid := 101
				if changed && change == "ancestry" {
					ppid = 999
				}
				return map[int]procSnapshot{
					101: {pid: 101, ppid: 1, cmd: integrationRunRoot + "/bin/gc supervisor run"},
					102: {pid: 102, ppid: ppid, cmd: "owned-child"},
				}
			}
			integrationCleanupStartTime = func(pid int) (string, error) {
				if pid == 102 && changed {
					if change == "start" {
						return "reused", nil
					}
					if change == "unreadable" {
						return "", fmt.Errorf("unreadable")
					}
				}
				return fmt.Sprint(pid), nil
			}
			integrationCleanupPause = func(time.Duration) { changed = true }
			var killed []int
			checks := 0
			integrationCleanupSignal = func(pid int, sig syscall.Signal) error {
				if sig == syscall.SIGKILL && pid == 102 {
					killed = append(killed, pid)
				}
				if sig == 0 {
					checks++
					if checks > 2 {
						return syscall.ESRCH
					}
				}
				return nil
			}
			sweepSubprocessTestProcesses()
			if len(killed) != 0 {
				t.Fatalf("escalation killed changed/unverified PID identity: %v", killed)
			}
		})
	}
}
