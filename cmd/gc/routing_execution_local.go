package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/agentutil"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/shellquote"
)

var routingEnvironmentKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func routingExecutionCommand(binding config.RoutingExecutionBinding) (string, error) {
	if binding.Transport != "subprocess" || !filepath.IsAbs(binding.Executable) || !filepath.IsAbs(binding.WorkDir) || filepath.Base(binding.Executable) != binding.Account {
		return "", errors.New("unsupported local execution surface")
	}
	if binding.ModelArgIndex < 0 || binding.ModelArgIndex >= len(binding.Args) || binding.EffortArgIndex < 0 || binding.EffortArgIndex >= len(binding.Args) || binding.ModelArgIndex == binding.EffortArgIndex || binding.Args[binding.ModelArgIndex] != binding.ServeAs || binding.Args[binding.EffortArgIndex] != binding.ReasoningEffort {
		return "", errors.New("literal serving or reasoning argument mismatch")
	}
	switch binding.ReasoningEffort {
	case "none", "low", "medium", "high":
	default:
		return "", errors.New("unsupported reasoning effort")
	}
	// env -i is part of the bound command, not mutable runtime.Env: no provider,
	// account, model, config-path, shell-init, HOME, or PATH ambient override can
	// enter this child. The caller explicitly supplies its entire environment.
	keys := make([]string, 0, len(binding.Environment))
	for key := range binding.Environment {
		if !routingEnvironmentKey.MatchString(key) {
			return "", errors.New("invalid isolated environment key")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := []string{"/usr/bin/env", "-i"}
	for _, key := range keys {
		args = append(args, key+"="+binding.Environment[key])
	}
	args = append(args, binding.Executable)
	args = append(args, binding.Args...)
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return "", errors.New("invalid invocation argument")
		}
		quoted[i] = shellquote.Quote(arg)
	}
	return strings.Join(quoted, " "), nil
}

// resolveLocalRoutingExecution resolves caller-owned registry/config and actual
// executable bytes independently of the recommendation or signed decision.
func resolveLocalRoutingExecution(agent config.Agent, city *config.City, final *runtime.Config) (routingdecision.ExecutionBinding, error) {
	if city == nil || city.RoutingExecution == nil || !city.RoutingExecution.Enabled {
		return routingdecision.ExecutionBinding{}, errors.New("routing execution disabled")
	}
	target := agentutil.RoutedToIdentity(&agent)
	binding, ok := city.RoutingExecution.Bindings[target]
	if !ok {
		return routingdecision.ExecutionBinding{}, errors.New("local execution binding absent")
	}
	command, err := routingExecutionCommand(binding)
	if err != nil {
		return routingdecision.ExecutionBinding{}, err
	}
	resolved, err := config.ResolveProvider(&agent, &city.Workspace, city.Providers, func(name string) (string, error) { return name, nil })
	if err != nil {
		return routingdecision.ExecutionBinding{}, err
	}
	if resolved.Name != binding.Provider || resolved.Command != command || config.ResolveSessionCreateTransport(agent.Session, resolved) != "subprocess" {
		return routingdecision.ExecutionBinding{}, errors.New("resolved provider invocation mismatch")
	}
	info, err := os.Lstat(binding.Executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return routingdecision.ExecutionBinding{}, errors.New("authorized executable unavailable")
	}
	contents, err := os.ReadFile(binding.Executable)
	if err != nil {
		return routingdecision.ExecutionBinding{}, errors.New("authorized executable unreadable")
	}
	executableHash := sha256.Sum256(contents)
	if hex.EncodeToString(executableHash[:]) != binding.ExecutableDigest {
		return routingdecision.ExecutionBinding{}, errors.New("authorized executable bytes changed")
	}
	if final != nil {
		if final.Command != command || final.WorkDir != binding.WorkDir || final.Upstream != "" || len(final.MCPServers) > 0 || len(final.StartupEnvelope) > 0 || len(final.PreStart) > 0 || len(final.SessionSetup) > 0 || final.SessionSetupScript != "" || len(final.SessionLive) > 0 || len(final.CopyFiles) > 0 || final.OverlayDir != "" || len(final.PackOverlayDirs) > 0 || len(final.InstallAgentHooks) > 0 || final.PromptSuffix != "" {
			return routingdecision.ExecutionBinding{}, errors.New("final runtime invocation changed")
		}
	}
	// Registry bytes (including wrapper hash, local account, and literal tuple)
	// bind adapter identity. Invocation commits to isolated argv and working dir.
	data, err := json.Marshal(binding)
	if err != nil {
		return routingdecision.ExecutionBinding{}, err
	}
	adapterHash := sha256.Sum256(append([]byte("gascity.local-execution-adapter.v1\x00"), data...))
	invocationHash := sha256.Sum256([]byte("gascity.local-execution-invocation.v1\x00" + command + "\x00" + binding.WorkDir))
	digest, err := routingDecisionTargetConfigDigest(agent, city)
	if err != nil {
		return routingdecision.ExecutionBinding{}, err
	}
	actual := routingdecision.ExecutionBinding{Schema: 1, CanonicalModel: binding.CanonicalModel, ServeAs: binding.ServeAs, ReasoningEffort: binding.ReasoningEffort, Account: binding.Account, Provider: binding.Provider, Target: target, ConfigDigest: digest, AdapterID: binding.AdapterID, AdapterDigest: hex.EncodeToString(adapterHash[:]), InvocationDigest: hex.EncodeToString(invocationHash[:])}
	return actual, actual.Validate()
}
