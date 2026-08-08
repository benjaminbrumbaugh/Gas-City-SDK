package routingdecision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// WorkState is the ownership/routing projection bound to a decision. It omits
// ordinary task prose while retaining every field that can prove prior control.
type WorkState struct {
	Schema                    int    `json:"schema"`
	WorkBeadID                string `json:"work_bead_id"`
	Status                    string `json:"status"`
	Assignee                  string `json:"assignee"`
	RoutedTo                  string `json:"routed_to"`
	RunTarget                 string `json:"run_target"`
	DecisionID                string `json:"decision_id"`
	ExecutionRoutedTo         string `json:"execution_routed_to"`
	DeferredExecutionRoutedTo string `json:"deferred_execution_routed_to"`
	ExecutionRigContext       string `json:"execution_rig_context"`
	DeferredAssignee          string `json:"deferred_assignee"`
	DeferredRoutedTo          string `json:"deferred_routed_to"`
	Kind                      string `json:"kind"`
	SessionID                 string `json:"session_id"`
	SessionName               string `json:"session_name"`
	Continuation              string `json:"continuation_group"`
	CurrentRunID              string `json:"current_run_id"`
	ClaimFence                int64  `json:"claim_fence"`
}

// WorkStateFrom projects exact ownership and route facts from bead metadata.
func WorkStateFrom[M ~map[string]string](workBeadID, status, assignee string, metadata M, claimFence int64) WorkState {
	value := func(key string) string { return strings.TrimSpace(metadata[key]) }
	return WorkState{
		Schema: SchemaVersion, WorkBeadID: strings.TrimSpace(workBeadID), Status: strings.TrimSpace(status), Assignee: strings.TrimSpace(assignee),
		RoutedTo: value(beadmeta.RoutedToMetadataKey), RunTarget: value(beadmeta.RunTargetMetadataKey), DecisionID: value(beadmeta.RoutingDecisionIDMetadataKey),
		ExecutionRoutedTo: value(beadmeta.ExecutionRoutedToMetadataKey), DeferredExecutionRoutedTo: value(beadmeta.DeferredExecutionRoutedToMetadataKey),
		ExecutionRigContext: value(beadmeta.ExecutionRigContextMetadataKey), DeferredAssignee: value(beadmeta.DeferredAssigneeMetadataKey),
		DeferredRoutedTo: value(beadmeta.DeferredRoutedToMetadataKey), Kind: value(beadmeta.KindMetadataKey),
		SessionID:    firstWorkStateValue(value(beadmeta.SessionIDMetadataKey), value(beadmeta.SessionIDCamelMetadataKey)),
		SessionName:  firstWorkStateValue(value(beadmeta.SessionNameMetadataKey), value(beadmeta.SessionNameCamelMetadataKey)),
		Continuation: value(beadmeta.ContinuationGroupMetadataKey), CurrentRunID: value(beadmeta.CurrentRunIDMetadataKey), ClaimFence: claimFence,
	}
}

// WorkStateDigest returns the lowercase SHA-256 of the fixed projection.
func WorkStateDigest(state WorkState) string {
	encoded, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func firstWorkStateValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
