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
	signifier := cfg.Capability(true)
	if !signifier.Available || signifier.Delivery != "queued" || signifier.InterruptPolicy != "never" || signifier.SessionPolicy != "resume_or_create" {
		t.Fatalf("signifier = %+v", signifier)
	}
	if strings.Contains(strings.ToLower(signifier.Instruction), "status report") == false {
		t.Fatalf("instruction must reject routine status reporting: %q", signifier.Instruction)
	}
}

// TestExternalCoordinationCapabilityIsNotAvailableWithoutARegisteredAdapter
// keeps configuration from standing in for reachability. A complete, valid
// [external_coordination] table describes where requests should go; it is not
// evidence that anything is currently able to take them.
func TestExternalCoordinationCapabilityIsNotAvailableWithoutARegisteredAdapter(t *testing.T) {
	cfg := ExternalCoordinationConfig{Enabled: true, Target: "hermes-desktop", Adapter: "hermes", Provider: "hermes", AccountID: "desktop"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	signifier := cfg.Capability(false)
	if signifier.Available || signifier.Registered {
		t.Fatalf("signifier with no registered adapter = %+v, want available=false registered=false", signifier)
	}
	if !signifier.Configured {
		t.Fatalf("signifier.Configured = false for a complete enabled table: %+v", signifier)
	}
	// The target stays visible so the unreachable case remains diagnosable.
	if signifier.Target != "hermes-desktop" || signifier.Adapter != "hermes" {
		t.Fatalf("signifier lost its target while unreachable: %+v", signifier)
	}
}

// TestExternalCoordinationCapabilityStaysUnavailableWhenDisabled keeps a
// registered adapter from making a disabled city look ready. Registration is
// not consent: an adapter can be attached for unrelated messaging while
// external coordination is switched off.
func TestExternalCoordinationCapabilityStaysUnavailableWhenDisabled(t *testing.T) {
	cfg := ExternalCoordinationConfig{Enabled: false, Target: "hermes-desktop", Adapter: "hermes"}
	signifier := cfg.Capability(true)
	if signifier.Available || signifier.Configured {
		t.Fatalf("signifier for a disabled table = %+v, want available=false configured=false", signifier)
	}
}

func TestExternalCoordinationSwitchIsConfigurationOnly(t *testing.T) {
	first := ExternalCoordinationConfig{Enabled: true, Target: "hermes-desktop", Adapter: "hermes", ConfigRevision: 4}
	second := ExternalCoordinationConfig{Enabled: true, Target: "other-coordinator", Adapter: "other", ConfigRevision: 5}
	if first.Capability(true).LogicalRole != second.Capability(true).LogicalRole {
		t.Fatalf("logical role changed during target switch: %q -> %q", first.Capability(true).LogicalRole, second.Capability(true).LogicalRole)
	}
	if first.Capability(true).Target == second.Capability(true).Target || first.Capability(true).Adapter == second.Capability(true).Adapter {
		t.Fatal("target/adapter switch was not reflected")
	}
}

func TestExternalCoordinationValidationRejectsUnsafeImplicitInterrupt(t *testing.T) {
	cfg := ExternalCoordinationConfig{Enabled: true, Target: "coord", Adapter: "http", Delivery: "interrupt", InterruptPolicy: "never"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("interrupt delivery with never policy was accepted")
	}
}
