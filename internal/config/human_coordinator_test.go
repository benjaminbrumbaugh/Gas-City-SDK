package config

import (
	"strings"
	"testing"
)

func TestHumanCoordinatorSignifierDefaultsToQueuedNonInterrupting(t *testing.T) {
	cfg := HumanCoordinatorConfig{Enabled: true, Target: "hermes-desktop", Adapter: "hermes"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	signifier := cfg.Signifier()
	if !signifier.Available || signifier.Delivery != "queued" || signifier.InterruptPolicy != "never" || signifier.SessionPolicy != "resume_or_create" {
		t.Fatalf("signifier = %+v", signifier)
	}
	if strings.Contains(strings.ToLower(signifier.Instruction), "status report") == false {
		t.Fatalf("instruction must reject routine status reporting: %q", signifier.Instruction)
	}
}

func TestHumanCoordinatorSwitchIsConfigurationOnly(t *testing.T) {
	first := HumanCoordinatorConfig{Enabled: true, Target: "hermes-desktop", Adapter: "hermes", ConfigRevision: 4}
	second := HumanCoordinatorConfig{Enabled: true, Target: "other-coordinator", Adapter: "other", ConfigRevision: 5}
	if first.Signifier().LogicalRole != second.Signifier().LogicalRole {
		t.Fatalf("logical role changed during target switch: %q -> %q", first.Signifier().LogicalRole, second.Signifier().LogicalRole)
	}
	if first.Signifier().Target == second.Signifier().Target || first.Signifier().Adapter == second.Signifier().Adapter {
		t.Fatal("target/adapter switch was not reflected")
	}
}

func TestHumanCoordinatorValidationRejectsUnsafeImplicitInterrupt(t *testing.T) {
	cfg := HumanCoordinatorConfig{Enabled: true, Target: "coord", Adapter: "http", Delivery: "interrupt", InterruptPolicy: "never"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("interrupt delivery with never policy was accepted")
	}
}
