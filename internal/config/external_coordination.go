package config

import (
	"fmt"
	"strings"
)

// ExternalCoordinationConfig declares the external coordination capability. It is
// provider-neutral: Hermes is one adapter value, not an SDK role or dependency.
type ExternalCoordinationConfig struct {
	Enabled         bool                             `toml:"enabled,omitempty"`
	Target          string                           `toml:"target,omitempty"`
	Adapter         string                           `toml:"adapter,omitempty"`
	Provider        string                           `toml:"provider,omitempty"`
	AccountID       string                           `toml:"account_id,omitempty"`
	ConversationID  string                           `toml:"conversation_id,omitempty"`
	Delivery        string                           `toml:"delivery,omitempty"`
	InterruptPolicy string                           `toml:"interrupt_policy,omitempty"`
	SessionPolicy   string                           `toml:"session_policy,omitempty"`
	ConfigRevision  int64                            `toml:"config_revision,omitempty"`
	Triggers        []string                         `toml:"triggers,omitempty"`
	Capabilities    ExternalCoordinationCapabilities `toml:"capabilities,omitempty"`
}

// ExternalCoordinationCapabilities describes declared adapter capabilities. These
// are configuration evidence, not proof that a live adapter is registered.
type ExternalCoordinationCapabilities struct {
	CanCreateSession bool `toml:"can_create_session,omitempty" json:"can_create_session"`
	CanResumeSession bool `toml:"can_resume_session,omitempty" json:"can_resume_session"`
	CanSubmitPrompt  bool `toml:"can_submit_prompt,omitempty" json:"can_submit_prompt"`
	CanInterrupt     bool `toml:"can_interrupt,omitempty" json:"can_interrupt"`
	CanReceiveEvents bool `toml:"can_receive_events,omitempty" json:"can_receive_events"`
	CanReturnResults bool `toml:"can_return_results,omitempty" json:"can_return_results"`
}

// ExternalCoordinationCapability is the runtime-visible hint an orchestrator can
// use to discover when and how external coordination should be used.
type ExternalCoordinationCapability struct {
	Available       bool                             `json:"available"`
	LogicalRole     string                           `json:"logical_role"`
	Target          string                           `json:"target"`
	Adapter         string                           `json:"adapter"`
	ConfigRevision  int64                            `json:"config_revision"`
	Delivery        string                           `json:"delivery"`
	InterruptPolicy string                           `json:"interrupt_policy"`
	SessionPolicy   string                           `json:"session_policy"`
	Capabilities    ExternalCoordinationCapabilities `json:"capabilities"`
	Triggers        []string                         `json:"triggers"`
	Instruction     string                           `json:"instruction"`
}

// EffectiveDelivery returns the safe default when the table omits delivery.
func (c ExternalCoordinationConfig) EffectiveDelivery() string {
	if value := strings.TrimSpace(c.Delivery); value != "" {
		return value
	}
	return "queued"
}

// EffectiveInterruptPolicy defaults to never interrupting a coordinator turn.
func (c ExternalCoordinationConfig) EffectiveInterruptPolicy() string {
	if value := strings.TrimSpace(c.InterruptPolicy); value != "" {
		return value
	}
	return "never"
}

// EffectiveSessionPolicy defaults to resuming an existing coordinator session
// or creating one when no live session exists.
func (c ExternalCoordinationConfig) EffectiveSessionPolicy() string {
	if value := strings.TrimSpace(c.SessionPolicy); value != "" {
		return value
	}
	return "resume_or_create"
}

// Capability returns the compact external coordination description intended for diagnostics
// and prompt assembly. It deliberately describes exceptional communication,
// not a periodic status-reporting loop.
func (c ExternalCoordinationConfig) Capability() ExternalCoordinationCapability {
	triggers := append([]string(nil), c.Triggers...)
	if len(triggers) == 0 {
		triggers = []string{"outside_help", "escalation", "direct_request", "large_summary", "authorization", "ambiguity"}
	}
	return ExternalCoordinationCapability{
		Available:       c.Enabled && strings.TrimSpace(c.Target) != "" && strings.TrimSpace(c.Adapter) != "",
		LogicalRole:     "external-coordination",
		Target:          strings.TrimSpace(c.Target),
		Adapter:         strings.TrimSpace(c.Adapter),
		ConfigRevision:  c.ConfigRevision,
		Delivery:        c.EffectiveDelivery(),
		InterruptPolicy: c.EffectiveInterruptPolicy(),
		SessionPolicy:   c.EffectiveSessionPolicy(),
		Capabilities:    c.Capabilities,
		Triggers:        triggers,
		Instruction:     "Use external coordination for outside help, escalation, authorization, direct-request responses, or large summaries; do not send routine status reports. Requests are queued by default and must retain their work and correlation references.",
	}
}

// Validate checks the configuration's explicit policy values. An omitted table
// remains disabled and valid; a partially configured enabled table fails closed.
func (c ExternalCoordinationConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Target) == "" || strings.TrimSpace(c.Adapter) == "" {
		return fmt.Errorf("external_coordination: enabled configuration requires target and adapter")
	}
	if delivery := c.EffectiveDelivery(); delivery != "queued" && delivery != "interrupt" {
		return fmt.Errorf("external_coordination: delivery must be queued or interrupt, got %q", delivery)
	}
	if policy := c.EffectiveInterruptPolicy(); policy != "never" && policy != "emergency_only" {
		return fmt.Errorf("external_coordination: interrupt_policy must be never or emergency_only, got %q", policy)
	}
	if session := c.EffectiveSessionPolicy(); session != "new" && session != "resume" && session != "submit" && session != "resume_or_create" {
		return fmt.Errorf("external_coordination: invalid session_policy %q", session)
	}
	if c.EffectiveDelivery() == "interrupt" && c.EffectiveInterruptPolicy() == "never" {
		return fmt.Errorf("external_coordination: interrupt delivery conflicts with interrupt_policy=never")
	}
	return nil
}
