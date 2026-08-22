package main

import (
	"bytes"
	"testing"
)

func TestSessionCloseStaleCommandSurfaceRequiresExactGuards(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newSessionCloseStaleCmd(&stdout, &stderr)
	if got, want := cmd.Use, "close-stale <session-id>"; got != want {
		t.Fatalf("Use = %q, want %q", got, want)
	}
	for _, name := range []string{"if-revision", "if-state", "if-held-until", "reason", "check", "json"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s flag", name)
		}
	}
	for _, name := range []string{"if-revision", "if-state", "if-held-until", "reason"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || len(flag.Annotations) == 0 {
			t.Fatalf("--%s is not marked required", name)
		}
	}
	if err := cmd.Args(cmd, []string{"gc-ha00", "extra"}); err == nil {
		t.Fatal("close-stale accepted more than one target")
	}
}
