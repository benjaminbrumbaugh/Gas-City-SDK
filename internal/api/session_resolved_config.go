package api

import (
	"fmt"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/providerfence"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

func resolvedSessionConfigForProvider(
	cityPath string,
	workspaceEnv map[string]string,
	alias, explicitName, template, title, transport string,
	metadata map[string]string,
	resolved *config.ResolvedProvider,
	command, workDir string,
	mcpServers []runtime.MCPServerConfig,
	stores ...beads.Store,
) (worker.ResolvedSessionConfig, error) {
	if resolved == nil {
		return worker.ResolvedSessionConfig{}, fmt.Errorf("%w: resolved provider is required", worker.ErrHandleConfig)
	}
	if transport == "acp" {
		var err error
		metadata, err = session.WithStoredMCPMetadata(
			metadata,
			firstNonEmptyString(metadata[session.MCPIdentityMetadataKey], metadata["agent_name"]),
			mcpServers,
		)
		if err != nil {
			return worker.ResolvedSessionConfig{}, err
		}
	}
	// Use the ACP-specific command when the session uses ACP transport,
	// falling back to the default command for tmux sessions.
	resolvedCommand := resolved.CommandString()
	if transport == "acp" {
		resolvedCommand = resolved.ACPCommandString()
	}
	sessionEnv := cityAnchoredSessionEnv(cityPath, workspaceEnv, resolved.Env)
	var store beads.Store
	if len(stores) > 0 {
		store = stores[0]
	}
	return worker.NormalizeResolvedSessionConfig(worker.ResolvedSessionConfig{
		Alias:        alias,
		ExplicitName: explicitName,
		Template:     template,
		Title:        title,
		Transport:    transport,
		Metadata:     metadata,
		Runtime: worker.ResolvedRuntime{
			Command:    firstNonEmptyString(command, resolvedCommand, resolved.Name),
			WorkDir:    workDir,
			Provider:   resolved.Name,
			SessionEnv: sessionEnv,
			Resume: session.ProviderResume{
				ResumeFlag:    resolved.ResumeFlag,
				ResumeStyle:   resolved.ResumeStyle,
				ResumeCommand: resolved.ResumeCommand,
				SessionIDFlag: resolved.SessionIDFlag,
			},
			Hints:                        sessionCreateHints(resolved, sessionEnv, mcpServers),
			ResolveProviderFenceIdentity: providerFenceIdentityResolverForLaunch(cityPath, store, resolved, sessionEnv),
		},
	})
}

func providerFenceIdentityResolverForLaunch(cityPath string, store beads.Store, resolved *config.ResolvedProvider, sessionEnv map[string]string) func() (string, error) {
	if store == nil || resolved == nil {
		return nil
	}
	return func() (string, error) {
		identity, err := providerfence.IdentityForCityWithStore(cityPath, store, resolved, providerfence.AccountEnv(sessionEnv))
		if err != nil {
			return "", fmt.Errorf("provider account identity: %w", err)
		}
		return identity, nil
	}
}
