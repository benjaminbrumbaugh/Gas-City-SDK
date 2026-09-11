package config

import (
	"strings"
	"testing"
	"time"
)

func TestRecoveryResponderConfigProgressiveActivationAndDurations(t *testing.T) {
	var cfg City
	if _, err := tomlDecode("", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RecoveryResponder != nil {
		t.Fatalf("absent config activated responder: %+v", cfg.RecoveryResponder)
	}

	input := `[recovery_responder]
targets = ["rig/first", "rig/second"]
wayfinder_url = "http://127.0.0.1:9876"
wayfinder_request_file = ".gc/wayfinder-recovery.json"
hold = "20m"
advisory_timeout = "1500ms"
cooldown = "3m"
max_attempts = 2
`
	if _, err := tomlDecode(input, &cfg); err != nil {
		t.Fatal(err)
	}
	r := cfg.RecoveryResponder
	if r == nil || len(r.Targets) != 2 || r.HoldDuration() != 20*time.Minute || r.AdvisoryTimeoutDuration() != 1500*time.Millisecond || r.CooldownDuration() != 3*time.Minute {
		t.Fatalf("decoded recovery config = %+v", r)
	}
	if r.WayfinderRequestFile != ".gc/wayfinder-recovery.json" {
		t.Fatalf("wayfinder request file = %q", r.WayfinderRequestFile)
	}
}

func TestValidateRecoveryResponderRequiresConfiguredDistinctTargets(t *testing.T) {
	cfg := &City{
		Agents:            []Agent{{Name: "first", Dir: "rig"}, {Name: "second", Dir: "rig"}},
		RecoveryResponder: &RecoveryResponderConfig{Targets: []string{"rig/first", "rig/second"}, MaxAttempts: 2},
	}
	if err := ValidateRecoveryResponder(cfg, "city.toml"); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	tests := []struct {
		name    string
		targets []string
		want    string
	}{
		{name: "none", want: "at least one target"},
		{name: "unknown", targets: []string{"rig/missing"}, want: "not a configured agent"},
		{name: "duplicate", targets: []string{"rig/first", "rig/first"}, want: "duplicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.RecoveryResponder.Targets = tt.targets
			err := ValidateRecoveryResponder(cfg, "city.toml")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateRecoveryResponderRejectsInvalidBounds(t *testing.T) {
	base := City{Agents: []Agent{{Name: "first", Dir: "rig"}}}
	tests := []struct {
		name string
		cfg  RecoveryResponderConfig
		want string
	}{
		{name: "negative hold", cfg: RecoveryResponderConfig{Targets: []string{"rig/first"}, Hold: "-1s"}, want: "hold"},
		{name: "long advisory", cfg: RecoveryResponderConfig{Targets: []string{"rig/first"}, AdvisoryTimeout: "6s"}, want: "advisory_timeout"},
		{name: "attempts exceed targets", cfg: RecoveryResponderConfig{Targets: []string{"rig/first"}, MaxAttempts: 2}, want: "max_attempts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.RecoveryResponder = &tt.cfg
			err := ValidateRecoveryResponder(&cfg, "city.toml")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateRecoveryResponderRequiresLoopbackWayfinderBaseAndTemplatePair(t *testing.T) {
	base := City{
		Agents:            []Agent{{Name: "first", Dir: "rig"}},
		RecoveryResponder: &RecoveryResponderConfig{Targets: []string{"rig/first"}},
	}
	tests := []struct {
		name, url, file, want string
	}{
		{name: "valid ipv4", url: "http://127.0.0.1:9876", file: ".gc/request.json"},
		{name: "valid ipv6", url: "http://[::1]:9876", file: ".gc/request.json"},
		{name: "url without template", url: "http://127.0.0.1:9876", want: "wayfinder_request_file"},
		{name: "template without url", file: ".gc/request.json", want: "wayfinder_url"},
		{name: "remote host", url: "http://192.0.2.1:9876", file: ".gc/request.json", want: "loopback"},
		{name: "dns localhost", url: "http://localhost:9876", file: ".gc/request.json", want: "loopback"},
		{name: "https", url: "https://127.0.0.1:9876", file: ".gc/request.json", want: "http"},
		{name: "missing port", url: "http://127.0.0.1", file: ".gc/request.json", want: "port"},
		{name: "endpoint path", url: "http://127.0.0.1:9876/routing/v3/evaluate", file: ".gc/request.json", want: "base URL"},
		{name: "query", url: "http://127.0.0.1:9876?x=1", file: ".gc/request.json", want: "base URL"},
		{name: "userinfo", url: "http://user@127.0.0.1:9876", file: ".gc/request.json", want: "user info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			recoveryConfig := *base.RecoveryResponder
			recoveryConfig.WayfinderURL = tt.url
			recoveryConfig.WayfinderRequestFile = tt.file
			cfg.RecoveryResponder = &recoveryConfig
			err := ValidateRecoveryResponder(&cfg, "city.toml")
			if tt.want == "" {
				if err != nil {
					t.Fatalf("valid config: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
