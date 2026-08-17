package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/agentutil"
	"github.com/gastownhall/gascity/internal/config"
)

const routingDecisionDigestFieldIncluded = "included in one-way digest"

// Every exported config.Agent field is intentionally included, including
// runtime provenance and credential-bearing maps. Only the final digest leaves
// this function; the projection and raw values are never stored or logged.
var routingDecisionAgentDigestFieldPolicy = map[string]string{
	"Name": routingDecisionDigestFieldIncluded, "Description": routingDecisionDigestFieldIncluded,
	"Dir": routingDecisionDigestFieldIncluded, "WorkDir": routingDecisionDigestFieldIncluded,
	"TmuxAlias": routingDecisionDigestFieldIncluded, "Scope": routingDecisionDigestFieldIncluded,
	"Suspended": routingDecisionDigestFieldIncluded, "PreStart": routingDecisionDigestFieldIncluded,
	"PromptTemplate": routingDecisionDigestFieldIncluded, "Nudge": routingDecisionDigestFieldIncluded,
	"Session": routingDecisionDigestFieldIncluded, "Provider": routingDecisionDigestFieldIncluded,
	"Upstream": routingDecisionDigestFieldIncluded, "InheritedProvider": routingDecisionDigestFieldIncluded,
	"StartCommand": routingDecisionDigestFieldIncluded, "Lifecycle": routingDecisionDigestFieldIncluded,
	"Args": routingDecisionDigestFieldIncluded, "PromptMode": routingDecisionDigestFieldIncluded,
	"PromptFlag": routingDecisionDigestFieldIncluded, "ReadyDelayMs": routingDecisionDigestFieldIncluded,
	"ReadyPromptPrefix": routingDecisionDigestFieldIncluded, "ProcessNames": routingDecisionDigestFieldIncluded,
	"EmitsPermissionWarning": routingDecisionDigestFieldIncluded, "Env": routingDecisionDigestFieldIncluded,
	"OptionDefaults": routingDecisionDigestFieldIncluded, "MaxActiveSessions": routingDecisionDigestFieldIncluded,
	"MinActiveSessions": routingDecisionDigestFieldIncluded, "ScaleCheck": routingDecisionDigestFieldIncluded,
	"DrainTimeout": routingDecisionDigestFieldIncluded, "OnBoot": routingDecisionDigestFieldIncluded,
	"OnDeath": routingDecisionDigestFieldIncluded, "Namepool": routingDecisionDigestFieldIncluded,
	"NamepoolNames": routingDecisionDigestFieldIncluded, "WorkQuery": routingDecisionDigestFieldIncluded,
	"SlingQuery": routingDecisionDigestFieldIncluded, "IdleTimeout": routingDecisionDigestFieldIncluded,
	"MaxSessionAge": routingDecisionDigestFieldIncluded, "MaxSessionAgeJitter": routingDecisionDigestFieldIncluded,
	"AssignedWorkDeferLimit": routingDecisionDigestFieldIncluded, "SleepAfterIdle": routingDecisionDigestFieldIncluded,
	"InstallAgentHooks": routingDecisionDigestFieldIncluded, "Skills": routingDecisionDigestFieldIncluded,
	"MCP": routingDecisionDigestFieldIncluded, "HooksInstalled": routingDecisionDigestFieldIncluded,
	"SessionSetup": routingDecisionDigestFieldIncluded, "SessionSetupScript": routingDecisionDigestFieldIncluded,
	"SessionLive": routingDecisionDigestFieldIncluded, "OverlayDir": routingDecisionDigestFieldIncluded,
	"SourceDir": routingDecisionDigestFieldIncluded, "SharedSkills": routingDecisionDigestFieldIncluded,
	"SharedMCP": routingDecisionDigestFieldIncluded, "SkillsDir": routingDecisionDigestFieldIncluded,
	"MCPDir": routingDecisionDigestFieldIncluded, "Implicit": routingDecisionDigestFieldIncluded,
	"DefaultSlingFormula": routingDecisionDigestFieldIncluded, "InheritedDefaultSlingFormula": routingDecisionDigestFieldIncluded,
	"InjectFragments": routingDecisionDigestFieldIncluded, "AppendFragments": routingDecisionDigestFieldIncluded,
	"InheritedAppendFragments": routingDecisionDigestFieldIncluded, "InjectAssignedSkills": routingDecisionDigestFieldIncluded,
	"Attach": routingDecisionDigestFieldIncluded, "DependsOn": routingDecisionDigestFieldIncluded,
	"ResumeCommand": routingDecisionDigestFieldIncluded, "WakeMode": routingDecisionDigestFieldIncluded,
	"MouseMode": routingDecisionDigestFieldIncluded, "SleepAfterIdleSource": routingDecisionDigestFieldIncluded,
	"PoolName": routingDecisionDigestFieldIncluded, "BindingName": routingDecisionDigestFieldIncluded,
	"PackName": routingDecisionDigestFieldIncluded,
}

type routingDecisionTargetDigestProjection struct {
	Schema                      int    `json:"schema"`
	RoutedToIdentity            string `json:"routed_to_identity"`
	Agent                       any    `json:"agent"`
	ResolvedProvider            any    `json:"resolved_provider"`
	EffectiveUpstreamName       string `json:"effective_upstream_name"`
	EffectiveUpstream           any    `json:"effective_upstream"`
	ResolvedMaxActiveSessions   *int   `json:"resolved_max_active_sessions"`
	EffectiveMinActiveSessions  int    `json:"effective_min_active_sessions"`
	SupportsGenericSessions     bool   `json:"supports_generic_sessions"`
	SupportsInstanceExpansion   bool   `json:"supports_instance_expansion"`
	EffectiveWorkQuery          string `json:"effective_work_query"`
	EffectiveAssignedInProgress string `json:"effective_assigned_in_progress_query"`
	EffectiveAssignedReady      string `json:"effective_assigned_ready_query"`
	EffectiveRoutedPool         string `json:"effective_routed_pool_query"`
	EffectivePoolDemand         string `json:"effective_pool_demand_query"`
	EffectiveSlingQuery         string `json:"effective_sling_query"`
	Beads                       any    `json:"beads"`
}

func routingDecisionTargetConfigDigest(agent config.Agent, cfg *config.City) (string, error) {
	if cfg == nil {
		return "", errors.New("routing decision target config unavailable")
	}
	resolvedProvider, err := config.ResolveProvider(&agent, &cfg.Workspace, cfg.Providers, func(file string) (string, error) {
		if strings.TrimSpace(file) == "" {
			return "", errors.New("empty provider executable")
		}
		return file, nil
	})
	if err != nil {
		return "", errors.New("routing decision target provider unresolved")
	}
	upstreamName := agent.Upstream
	if upstreamName == "" {
		upstreamName = cfg.AgentDefaults.Upstream
	}
	var upstream config.UpstreamSpec
	if upstreamName != "" {
		var found bool
		upstream, found = cfg.Upstreams[upstreamName]
		if !found {
			return "", errors.New("routing decision target upstream unresolved")
		}
	}
	agentValue, err := routingDecisionCanonicalReflect(reflect.ValueOf(agent))
	if err != nil {
		return "", errors.New("routing decision agent projection failed")
	}
	providerValue, err := routingDecisionCanonicalReflect(reflect.ValueOf(*resolvedProvider))
	if err != nil {
		return "", errors.New("routing decision provider projection failed")
	}
	upstreamValue, err := routingDecisionCanonicalReflect(reflect.ValueOf(upstream))
	if err != nil {
		return "", errors.New("routing decision upstream projection failed")
	}
	beadsValue, err := routingDecisionCanonicalReflect(reflect.ValueOf(cfg.Beads))
	if err != nil {
		return "", errors.New("routing decision beads projection failed")
	}
	projection := routingDecisionTargetDigestProjection{
		Schema: 1, RoutedToIdentity: agentutil.RoutedToIdentity(&agent), Agent: agentValue,
		ResolvedProvider: providerValue, EffectiveUpstreamName: upstreamName, EffectiveUpstream: upstreamValue,
		ResolvedMaxActiveSessions: agent.ResolvedMaxActiveSessions(cfg), EffectiveMinActiveSessions: agent.EffectiveMinActiveSessions(),
		SupportsGenericSessions: agent.SupportsGenericEphemeralSessions(), SupportsInstanceExpansion: agent.SupportsInstanceExpansion(),
		EffectiveWorkQuery: agent.EffectiveWorkQueryFor(config.QueryTopology{Beads: cfg.Beads}), EffectiveAssignedInProgress: agent.EffectiveAssignedInProgressQueryFor(config.QueryTopology{Beads: cfg.Beads}),
		EffectiveAssignedReady: agent.EffectiveAssignedReadyQueryFor(config.QueryTopology{Beads: cfg.Beads}), EffectiveRoutedPool: agent.EffectiveRoutedPoolQueryFor(config.QueryTopology{Beads: cfg.Beads}),
		EffectivePoolDemand: agent.EffectivePoolDemandQueryFor(config.QueryTopology{Beads: cfg.Beads}), EffectiveSlingQuery: agent.EffectiveSlingQuery(), Beads: beadsValue,
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return "", errors.New("routing decision target projection encode failed")
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func routingDecisionCanonicalReflect(value reflect.Value) (any, error) {
	if !value.IsValid() {
		return nil, nil
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, nil
		}
		return routingDecisionCanonicalReflect(value.Elem())
	}
	switch value.Kind() {
	case reflect.Bool:
		return value.Bool(), nil
	case reflect.String:
		return value.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint(), nil
	case reflect.Slice, reflect.Array:
		items := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			item, err := routingDecisionCanonicalReflect(value.Index(index))
			if err != nil {
				return nil, err
			}
			items[index] = item
		}
		return items, nil
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return nil, errors.New("unsupported map key")
		}
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		entries := make([]struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}, 0, len(keys))
		for _, key := range keys {
			entry, err := routingDecisionCanonicalReflect(value.MapIndex(key))
			if err != nil {
				return nil, err
			}
			entries = append(entries, struct {
				Key   string `json:"key"`
				Value any    `json:"value"`
			}{Key: key.String(), Value: entry})
		}
		return entries, nil
	case reflect.Struct:
		entries := make([]struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		}, 0, value.NumField())
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if !field.IsExported() {
				continue
			}
			fieldValue, err := routingDecisionCanonicalReflect(value.Field(index))
			if err != nil {
				return nil, err
			}
			entries = append(entries, struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			}{Name: field.Name, Value: fieldValue})
		}
		return entries, nil
	default:
		return nil, errors.New("unsupported digest field")
	}
}

func (cr *CityRuntime) resolveRoutingDecisionTarget(target, rig string) (config.Agent, string, bool) {
	if cr == nil || cr.cfg == nil {
		return config.Agent{}, "", false
	}
	agent, ok := agentutil.ResolveAgent(cr.cfg, target, agentutil.ResolveOpts{TemplateOnly: true})
	if !ok || agent.Suspended || strings.TrimSpace(agent.Dir) != rig || agentutil.RoutedToIdentity(&agent) != target || !agent.SupportsGenericEphemeralSessions() || !agent.SupportsInstanceExpansion() {
		return config.Agent{}, "", false
	}
	digest, err := routingDecisionTargetConfigDigest(agent, cr.cfg)
	if err != nil {
		return config.Agent{}, "", false
	}
	return agent, digest, true
}
