package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// hookClaimStoreSourceEnv enables the private source annotation that gc ready
// adds for the built-in hook work query. It is intentionally an environment
// seam rather than a public ready flag: the final hook result never exposes
// physical store details.
const hookClaimStoreSourceEnv = "GC_HOOK_CLAIM_STORE_SOURCE"

// hookClaimStoreMetadataKey is an invocation-local metadata key emitted by the
// built-in federated ready reader. It exists only on the decoded work-query
// candidate and is never written to a bead.
const hookClaimStoreMetadataKey = beadmeta.HookClaimStoreMetadataKey

type hookRouteState uint8

const (
	hookRouteUnrouted hookRouteState = iota
	hookRouteRouted
)

// hookClaimRouteState gives gc.routed_to an explicit state. A missing key and a
// present-but-empty key are both unrouted; workflow gc.run_target remains a
// separate compatibility fallback in hookClaimMatchesRoute.
func hookClaimRouteState(candidate beads.Bead) hookRouteState {
	if strings.TrimSpace(candidate.Metadata[beadmeta.RoutedToMetadataKey]) == "" {
		return hookRouteUnrouted
	}
	return hookRouteRouted
}

// hookClaimStoreSourceEnabled reports whether a built-in ready reader should
// attach its invocation-local source annotation to emitted rows.
func hookClaimStoreSourceEnabled() bool {
	value := strings.TrimSpace(os.Getenv(hookClaimStoreSourceEnv))
	return strings.EqualFold(value, "1") || strings.EqualFold(value, "true")
}

// hookStoreAffinityRefFromEnv maps a hook query environment to the same stable
// labels used by readyLegLabel. The city work reference is represented as
// "city" because an empty metadata value cannot distinguish a source annotation
// from an absent one.
func hookStoreAffinityRefFromEnv(env []string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(hookStoreEnvValueFromEntries(env, "GC_STORE_SCOPE"))) {
	case "city":
		return "city", true
	case "rig":
		rig := strings.TrimSpace(hookStoreEnvValueFromEntries(env, "GC_RIG"))
		if rig == "" {
			return "", false
		}
		return "rig " + rig, true
	default:
		return "", false
	}
}

// hookStoreAffinityRef returns the private physical-store label for a hook
// store. Explicit refs are used by production constructors; environment
// derivation keeps test seams and older callers useful without ID inference.
func hookStoreAffinityRef(store hookStore) (string, bool) {
	if ref := strings.TrimSpace(store.storeRef); ref != "" {
		return ref, true
	}
	return hookStoreAffinityRefFromEnv(store.env)
}

// hookClaimCandidateStoreAllowed checks the source annotation before any claim
// CAS. Graph rows are allowed only when the explicit class route is available;
// that route proves binding residency before writing. All ordinary annotated
// rows must match the selected hook store exactly.
func hookClaimCandidateStoreAllowed(candidate beads.Bead, claimStoreRef string, claimStoreKnown bool, classRoute *hookClaimClassRoute) (bool, string) {
	source, annotated := candidate.Metadata[hookClaimStoreMetadataKey]
	source = strings.TrimSpace(source)
	if !annotated {
		return true, ""
	}
	if source == "graph" {
		return classRoute != nil, source
	}
	if !claimStoreKnown || source == "" || source != strings.TrimSpace(claimStoreRef) {
		return false, source
	}
	return true, source
}

// filterHookClaimCandidatesByStore excludes candidates whose federated ready
// source differs from the store that will receive the claim. It returns whether
// any candidate was skipped so the caller preserves the claims_errored drain
// reason instead of acknowledging a misleading idle result.
func filterHookClaimCandidatesByStore(candidates []beads.Bead, claimStore hookStore, classRoute *hookClaimClassRoute, stderrWriter io.Writer) ([]beads.Bead, bool) {
	claimStoreRef, claimStoreKnown := hookStoreAffinityRef(claimStore)
	filtered := make([]beads.Bead, 0, len(candidates))
	skipped := false
	for _, candidate := range candidates {
		allowed, source := hookClaimCandidateStoreAllowed(candidate, claimStoreRef, claimStoreKnown, classRoute)
		if allowed {
			filtered = append(filtered, candidate)
			continue
		}
		skipped = true
		if source == "" {
			source = "<unknown>"
		}
		destination := strings.TrimSpace(claimStoreRef)
		if !claimStoreKnown || destination == "" {
			destination = "<unknown>"
		}
		_, _ = fmt.Fprintf(stderrWriter,
			"gc hook --claim: skipping %s: store affinity mismatch (candidate store %q, claim store %q)\n",
			candidate.ID, source, destination)
	}
	return filtered, skipped
}

func hookStoreEnvValueFromEntries(env []string, key string) string {
	value := ""
	for _, entry := range env {
		name, candidate, ok := strings.Cut(entry, "=")
		if ok && name == key {
			value = candidate
		}
	}
	return value
}
