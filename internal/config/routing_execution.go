package config

// RoutingExecutionConfig activates a caller-owned closed-world local execution
// registry. Absence or enabled=false denies v3 admission; legacy is unchanged.
// This is root city authorization, never recommendation-supplied configuration.
type RoutingExecutionConfig struct {
	Enabled  bool                               `toml:"enabled,omitempty"`
	Bindings map[string]RoutingExecutionBinding `toml:"bindings,omitempty"`
}

// RoutingExecutionBinding authorizes one exact local wrapper invocation.
// Canonical identity and literal serving argument are separate opaque values.
// The SDK supplies no accounts, aliases, reasoning defaults, or ranking policy.
type RoutingExecutionBinding struct {
	CanonicalModel   string            `toml:"canonical_model"`
	ServeAs          string            `toml:"serve_as"`
	ReasoningEffort  string            `toml:"reasoning_effort"`
	Account          string            `toml:"account"`
	Provider         string            `toml:"provider"`
	AdapterID        string            `toml:"adapter_id"`
	Executable       string            `toml:"executable"`
	ExecutableDigest string            `toml:"executable_digest"`
	Args             []string          `toml:"args"`
	ModelArgIndex    int               `toml:"model_arg_index"`
	EffortArgIndex   int               `toml:"effort_arg_index"`
	WorkDir          string            `toml:"work_dir"`
	Environment      map[string]string `toml:"environment"`
	Transport        string            `toml:"transport"`
}
