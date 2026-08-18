package beadmeta

import "strings"

// RunIdentityKind describes which durable metadata source supplied a run id.
// It is intentionally separate from the ID itself so readers can distinguish
// an execution root from the persistent session fallback used by named agents.
type RunIdentityKind string

const (
	RunIdentityWorkflow        RunIdentityKind = "workflow_id"
	RunIdentityMolecule        RunIdentityKind = "molecule_id"
	RunIdentityRootBead        RunIdentityKind = "root_bead_id"
	RunIdentitySelfBead        RunIdentityKind = "self_bead_id"
	RunIdentitySessionFallback RunIdentityKind = "session_fallback"
	RunIdentityUnknown         RunIdentityKind = "unknown"
)

// RunIdentity is the resolved run id plus the evidence source used to resolve
// it. The source is needed by usage projections: a session fallback is a
// logical named-session identity, not evidence of a fresh execution run.
type RunIdentity struct {
	ID     string
	Source RunIdentityKind
}

// runIDChain is the bead-metadata run-chain precedence: a graph workflow root
// (workflow_id), then a poured/wisp molecule root (molecule_id), then the
// nested-workflow root (gc.root_bead_id). workflow_id and molecule_id are the
// engine's bare (non-"gc."-namespaced) run-chain keys written by internal/sling;
// RootBeadIDMetadataKey is the gc.-namespaced nesting root.
var runIDChain = []struct {
	key    string
	source RunIdentityKind
}{
	{key: "workflow_id", source: RunIdentityWorkflow},
	{key: MoleculeIDMetadataKey, source: RunIdentityMolecule},
	{key: RootBeadIDMetadataKey, source: RunIdentityRootBead},
}

// ResolveRunID derives the run-root identifier for a bead from its metadata run
// chain, falling back to the bead's own id and then a caller-supplied fallback.
// Precedence: workflow_id || molecule_id || gc.root_bead_id || selfID ||
// fallbackID, skipping blank values at each step.
//
// Every usage-fact emitter MUST resolve a run id through this one helper so a
// run's model facts (worker prompt path) and compute facts (controller reconcile
// path) carry the same RunID and `gc costs` can group them; two independent
// copies could silently drift and split one run's rows (see
// engdocs/design/usage-facts-v0.md). The worker passes the session id as
// fallbackID so a manual chat with no work bead still resolves to its session
// bead as the run root; the compute path passes "" because the session bead is
// always present.
func ResolveRunID(metadata map[string]string, selfID, fallbackID string) string {
	return ResolveRunIdentity(metadata, selfID, fallbackID).ID
}

// ResolveRunIdentity derives the run-root identifier and records the first
// non-blank source in the same precedence order as ResolveRunID.
func ResolveRunIdentity(metadata map[string]string, selfID, fallbackID string) RunIdentity {
	for _, check := range runIDChain {
		if v := strings.TrimSpace(metadata[check.key]); v != "" {
			return RunIdentity{ID: v, Source: check.source}
		}
	}
	if v := strings.TrimSpace(selfID); v != "" {
		return RunIdentity{ID: v, Source: RunIdentitySelfBead}
	}
	if v := strings.TrimSpace(fallbackID); v != "" {
		return RunIdentity{ID: v, Source: RunIdentitySessionFallback}
	}
	return RunIdentity{Source: RunIdentityUnknown}
}
