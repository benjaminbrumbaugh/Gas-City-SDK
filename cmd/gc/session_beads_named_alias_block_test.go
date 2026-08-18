package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func namedSessionCity() *config.City {
	return &config.City{
		Agents: []config.Agent{
			{Name: "mayor"},
			{Name: "polecat"},
		},
		NamedSessions: []config.NamedSession{
			{Name: "gastown.mayor", Template: "mayor"},
		},
	}
}

// TestAliasUnavailableMustBlock_NamedIdentityViaPoolDemand is the gc-mtb
// regression. When the reconciler wants a named session whose alias is already
// held by a live incumbent, it must ABORT rather than fall through and create
// the bead without its alias. The fall-through produces a substitute target
// (s-<beadID>) that can never hold the alias it was spawned to satisfy, and
// because named demand is keyed on the alias, nothing can ever retire that
// demand — so the reconciler spawns another full provider session every window,
// without bound.
//
// The load-bearing case is the second one: isConfiguredNamed is derived from
// tp.ConfiguredNamedIdentity, which is empty when the same identity arrives via
// POOL demand. That is the path the guard used to miss.
func TestAliasUnavailableMustBlock_NamedIdentityViaPoolDemand(t *testing.T) {
	cfg := namedSessionCity()

	if !aliasUnavailableMustBlock(cfg, true, "gastown.mayor") {
		t.Error("stamped named identity did not block; this case already worked and must keep working")
	}
	if !aliasUnavailableMustBlock(cfg, false, "gastown.mayor") {
		t.Error("named identity arriving WITHOUT ConfiguredNamedIdentity did not block — this is the substitute-target leak (gc-mtb)")
	}
}

// TestAliasUnavailableMustBlock_PoolMemberStillFallsThrough is the guard on the
// fix. A substitute target is coherent for a pool member, which is addressed by
// pool rather than by name, and blocking there would stall ordinary pool spawns
// on transient alias contention. Only named identities may block.
func TestAliasUnavailableMustBlock_PoolMemberStillFallsThrough(t *testing.T) {
	cfg := namedSessionCity()

	if aliasUnavailableMustBlock(cfg, false, "polecat-3") {
		t.Error("a pool instance alias blocked; pool members must keep falling through to a substitute target")
	}
	if aliasUnavailableMustBlock(cfg, false, "") {
		t.Error("an empty alias blocked")
	}
	if aliasUnavailableMustBlock(nil, false, "gastown.mayor") {
		t.Error("blocked with no config; a nil cfg cannot prove an identity is named")
	}
}

// TestAliasUnavailableMustBlock_SuspendedNamedAgentDoesNotBlock pins that the
// named check inherits isConfiguredNamedSessionIdentity's suspended-agent
// semantics rather than re-deriving them: a suspended agent's identity is not
// live, so holding a spawn for it would stall on an identity nothing will claim.
func TestAliasUnavailableMustBlock_SuspendedNamedAgentDoesNotBlock(t *testing.T) {
	cfg := namedSessionCity()
	cfg.Agents[0].Suspended = true

	if aliasUnavailableMustBlock(cfg, false, "gastown.mayor") {
		t.Error("blocked on a suspended named agent's alias")
	}
}
