package main

import (
	"bytes"
	"testing"
)

func TestSessionClearCityStopCommandSurface(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newSessionClearCityStopCmd(&stdout, &stderr)
	if cmd.Use != "clear-city-stop <session-id>" {
		t.Fatalf("Use=%q", cmd.Use)
	}
	for _, name := range []string{"if-held-until", "check", "json"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s", name)
		}
	}
	flag := cmd.Flags().Lookup("if-held-until")
	if flag == nil || len(flag.Annotations) == 0 {
		t.Fatal("--if-held-until not required")
	}
	if err := cmd.Args(cmd, []string{"gc-xahi", "extra"}); err == nil {
		t.Fatal("accepted multiple targets")
	}
}
