package config

import (
	"strings"
	"testing"
)

func TestExternalCoordinationCapabilityDefaultsToQueuedNonInterrupting(t *testing.T) {
	cfg := ExternalCoordinationConfig{Enabled: true, Target: "hermes-desktop", Adapter: "hermes"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	signifier := cfg.Capability()
	if !signifier.Available || signifier.Delivery != "queued" || signifier.InterruptPolicy != "never" || signifier.SessionPolicy != "resume_or_create" {
		t.Fatalf("signifier = %+v", signifier)
	}
	if strings.Contains(strings.ToLower(signifier.Instruction), "status report") == false {
		t.Fatalf("instruction must reject routine status reporting: %q", signifier.Instruction)
	}
}

func TestExternalCoordinationSwitchIsConfigurationOnly(t *testing.T) {
	first := ExternalCoordinationConfig{Enabled: true, Target: "hermes-desktop", Adapter: "hermes", ConfigRevision: 4}
	second := ExternalCoordinationConfig{Enabled: true, Target: "other-coordinator", Adapter: "other", ConfigRevision: 5}
	if first.Capability().LogicalRole != second.Capability().LogicalRole {
		t.Fatalf("logical role changed during target switch: %q -> %q", first.Capability().LogicalRole, second.Capability().LogicalRole)
	}
	if first.Capability().Target == second.Capability().Target || first.Capability().Adapter == second.Capability().Adapter {
		t.Fatal("target/adapter switch was not reflected")
	}
}

func TestExternalCoordinationValidationRejectsUnsafeImplicitInterrupt(t *testing.T) {
	cfg := ExternalCoordinationConfig{Enabled: true, Target: "coord", Adapter: "http", Delivery: "interrupt", InterruptPolicy: "never"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("interrupt delivery with never policy was accepted")
	}
}
