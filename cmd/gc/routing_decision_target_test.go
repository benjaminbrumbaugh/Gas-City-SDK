package main

import (
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestRoutingDecisionTargetDigestCoversEveryExportedAgentField(t *testing.T) {
	typeOfAgent := reflect.TypeFor[config.Agent]()
	for index := 0; index < typeOfAgent.NumField(); index++ {
		field := typeOfAgent.Field(index)
		if !field.IsExported() {
			continue
		}
		if _, covered := routingDecisionAgentDigestFieldPolicy[field.Name]; !covered {
			t.Errorf("exported config.Agent field %q has no digest policy", field.Name)
		}
	}
	for field := range routingDecisionAgentDigestFieldPolicy {
		if _, ok := typeOfAgent.FieldByName(field); !ok {
			t.Errorf("digest policy names nonexistent config.Agent field %q", field)
		}
	}
}

func TestRoutingDecisionTargetDigestIsStableAndChangesWithSecurityFacts(t *testing.T) {
	max := 2
	agent := config.Agent{
		Name: "reviewer", Dir: "demo", StartCommand: "review", MaxActiveSessions: &max,
		Env:            map[string]string{"TOKEN": "credential-secret", "REGION": "west"},
		OptionDefaults: map[string]string{"model": "fast", "permission_mode": "plan"},
	}
	cfg := &config.City{Beads: config.BeadsConfig{BDCompatibility: config.BeadsBDCompatibility105}}
	first, err := routingDecisionTargetConfigDigest(agent, cfg)
	if err != nil {
		t.Fatalf("routingDecisionTargetConfigDigest: %v", err)
	}
	reordered := agent.Clone()
	reordered.Env = map[string]string{"REGION": "west", "TOKEN": "credential-secret"}
	reordered.OptionDefaults = map[string]string{"permission_mode": "plan", "model": "fast"}
	second, err := routingDecisionTargetConfigDigest(reordered, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("map insertion order changed digest: %q != %q", first, second)
	}
	drifted := agent.Clone()
	drifted.Env["TOKEN"] = "rotated-secret"
	third, err := routingDecisionTargetConfigDigest(drifted, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("credential-bearing effective config drift did not change one-way digest")
	}
	if len(first) != 64 || first == "credential-secret" {
		t.Fatalf("digest = %q, want only a full SHA-256 value", first)
	}
}
