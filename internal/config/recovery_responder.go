package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRecoveryHold            = 30 * time.Minute
	defaultRecoveryAdvisoryTimeout = 2 * time.Second
	defaultRecoveryCooldown        = 5 * time.Minute
	maximumRecoveryHold            = 24 * time.Hour
	maximumRecoveryAdvisoryTimeout = 5 * time.Second
	maximumRecoveryCooldown        = 24 * time.Hour
)

// RecoveryResponderConfig enables one-at-a-time ordinary recovery work for
// deterministic provider/session impairments. An absent table is disabled.
type RecoveryResponderConfig struct {
	Targets              []string `toml:"targets" jsonschema:"required,minItems=1"`
	WayfinderURL         string   `toml:"wayfinder_url,omitempty"`
	WayfinderRequestFile string   `toml:"wayfinder_request_file,omitempty"`
	Hold                 string   `toml:"hold,omitempty"`
	AdvisoryTimeout      string   `toml:"advisory_timeout,omitempty"`
	Cooldown             string   `toml:"cooldown,omitempty"`
	MaxAttempts          int      `toml:"max_attempts,omitempty" jsonschema:"minimum=0"`
}

// HoldDuration returns the configured source-session hold or its bounded default.
func (c *RecoveryResponderConfig) HoldDuration() time.Duration {
	return recoveryDuration(c, c.Hold, defaultRecoveryHold)
}

// AdvisoryTimeoutDuration returns the configured Wayfinder timeout or its default.
func (c *RecoveryResponderConfig) AdvisoryTimeoutDuration() time.Duration {
	return recoveryDuration(c, c.AdvisoryTimeout, defaultRecoveryAdvisoryTimeout)
}

// CooldownDuration returns the configured between-attempt delay or its default.
func (c *RecoveryResponderConfig) CooldownDuration() time.Duration {
	return recoveryDuration(c, c.Cooldown, defaultRecoveryCooldown)
}

func recoveryDuration(c *RecoveryResponderConfig, raw string, fallback time.Duration) time.Duration {
	if c == nil || strings.TrimSpace(raw) == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

// ValidateRecoveryResponder verifies that every fallback target names an
// explicitly configured agent and all timing knobs remain bounded.
func ValidateRecoveryResponder(cfg *City, source string) error {
	if cfg == nil || cfg.RecoveryResponder == nil {
		return nil
	}
	r := cfg.RecoveryResponder
	if len(r.Targets) == 0 {
		return fmt.Errorf("%s: recovery_responder requires at least one target", source)
	}
	seen := make(map[string]struct{}, len(r.Targets))
	for _, raw := range r.Targets {
		target := strings.TrimSpace(raw)
		if target == "" || strings.ContainsAny(target, "\r\n") {
			return fmt.Errorf("%s: recovery_responder target %q is invalid", source, raw)
		}
		if _, ok := seen[target]; ok {
			return fmt.Errorf("%s: recovery_responder target %q is duplicate", source, target)
		}
		seen[target] = struct{}{}
		configured := false
		for i := range cfg.Agents {
			if AgentMatchesIdentity(&cfg.Agents[i], target) {
				configured = true
				break
			}
		}
		if !configured {
			return fmt.Errorf("%s: recovery_responder target %q is not a configured agent", source, target)
		}
	}
	if r.MaxAttempts < 0 || r.MaxAttempts > len(r.Targets) {
		return fmt.Errorf("%s: recovery_responder max_attempts must be between 0 and the number of targets", source)
	}
	for _, check := range []struct {
		name    string
		raw     string
		maximum time.Duration
	}{
		{name: "hold", raw: r.Hold, maximum: maximumRecoveryHold},
		{name: "advisory_timeout", raw: r.AdvisoryTimeout, maximum: maximumRecoveryAdvisoryTimeout},
		{name: "cooldown", raw: r.Cooldown, maximum: maximumRecoveryCooldown},
	} {
		if strings.TrimSpace(check.raw) == "" {
			continue
		}
		value, err := time.ParseDuration(check.raw)
		if err != nil || value <= 0 || value > check.maximum {
			return fmt.Errorf("%s: recovery_responder %s must be a positive duration no greater than %s", source, check.name, check.maximum)
		}
	}
	wayfinderURL := strings.TrimSpace(r.WayfinderURL)
	requestFile := strings.TrimSpace(r.WayfinderRequestFile)
	if wayfinderURL == "" && requestFile != "" {
		return fmt.Errorf("%s: recovery_responder wayfinder_url is required with wayfinder_request_file", source)
	}
	if wayfinderURL != "" && requestFile == "" {
		return fmt.Errorf("%s: recovery_responder wayfinder_request_file is required with wayfinder_url", source)
	}
	if wayfinderURL != "" {
		if err := validateRecoveryWayfinderBaseURL(wayfinderURL); err != nil {
			return fmt.Errorf("%s: recovery_responder wayfinder_url %w", source, err)
		}
	}
	return nil
}

func validateRecoveryWayfinderBaseURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("must be an http loopback base URL with an explicit port")
	}
	if parsed.User != nil {
		return fmt.Errorf("must not contain user info")
	}
	if parsed.Scheme != "http" {
		return fmt.Errorf("must use http")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must be a base URL without a path, query, or fragment")
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || port == "" {
		return fmt.Errorf("must include an explicit port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("must include a valid port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("must use a numeric loopback address")
	}
	return nil
}
