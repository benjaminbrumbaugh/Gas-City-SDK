package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestLocalRoutingExecutionAdapterResolvesExactCallerOwnedBinding(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "caller wrappers with spaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "account-a")
	contents := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(executable, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(contents)
	binding := config.RoutingExecutionBinding{CanonicalModel: "canonical/model", ServeAs: "Literal/Model", ReasoningEffort: "high", Account: "account-a", Provider: "local-wrapper", AdapterID: "caller-wrapper-v1", Executable: executable, ExecutableDigest: hex.EncodeToString(hash[:]), Args: []string{"--model", "Literal/Model", "--effort", "high"}, ModelArgIndex: 1, EffortArgIndex: 3, WorkDir: root, Environment: map[string]string{"HOME": root, "PATH": "/usr/bin:/bin"}, Transport: "subprocess"}
	command, err := routingExecutionCommand(binding)
	if err != nil {
		t.Fatal(err)
	}
	agent := config.Agent{Name: "local-target", Provider: "local-wrapper", Session: "subprocess"}
	city := &config.City{Providers: map[string]config.ProviderSpec{"local-wrapper": {Command: command, PromptMode: "none"}}, RoutingExecution: &config.RoutingExecutionConfig{Enabled: true, Bindings: map[string]config.RoutingExecutionBinding{"local-target": binding}}}
	actual, err := resolveLocalRoutingExecution(agent, city, nil)
	if err != nil {
		t.Fatal(err)
	}
	if actual.ServeAs != binding.ServeAs || actual.Account != binding.Account || actual.ReasoningEffort != "high" {
		t.Fatalf("tuple substituted: %+v", actual)
	}
	final := runtime.Config{Command: command, WorkDir: root, Env: map[string]string{"ANTHROPIC_MODEL": "hostile-model", "HOME": "hostile-home"}}
	if _, err := resolveLocalRoutingExecution(agent, city, &final); err != nil {
		t.Fatalf("isolated invocation depends on hostile ambient env: %v", err)
	}
	final.Command += " --model Other"
	if _, err := resolveLocalRoutingExecution(agent, city, &final); err == nil {
		t.Fatal("final override admitted")
	}
	changed := binding
	changed.Account = "different-account"
	city.RoutingExecution.Bindings["local-target"] = changed
	if _, err := resolveLocalRoutingExecution(agent, city, nil); err == nil {
		t.Fatal("wrapper basename did not bind exact account")
	}
	city.RoutingExecution.Bindings["local-target"] = binding
	city.RoutingExecution.Enabled = false
	if _, err := resolveLocalRoutingExecution(agent, city, nil); err == nil {
		t.Fatal("disabled adapter admitted")
	}
}
