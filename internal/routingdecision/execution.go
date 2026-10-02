package routingdecision

import "strings"

// MatchesExecution requires an exact complete locally attested tuple at both
// the admission and final launch boundaries. It does not infer adapter truth.
func (payload DecisionPayload) MatchesExecution(actual ExecutionBinding) error {
	if payload.Schema != ExecutionSchemaVersion || payload.Execution == nil {
		return invalidf("complete execution selection required")
	}
	if err := payload.Validate(); err != nil {
		return err
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	if *payload.Execution != actual {
		return invalidf("resolved execution binding differs from signed selection")
	}
	return nil
}

func validV3RecommendationID(value string) bool {
	return strings.HasPrefix(value, "routing/v3:") && validDigest(strings.TrimPrefix(value, "routing/v3:"))
}

// ExecutionSchemaVersion is the signed decision generation that requires a
// complete v3 selection and a locally authorized final invocation binding.
// The storage and authority-file schemas remain at version one.
const ExecutionSchemaVersion = 2

// ExecutionBinding is the complete selected tuple attested by the existing
// signing authority after producer verification. It is not a ranking result
// or executable instruction. Digests use the SDK's lowercase SHA-256 encoding.
// Local adapters must attest the same tuple from the resolved invocation.
type ExecutionBinding struct {
	Schema           int    `json:"schema"`
	CanonicalModel   string `json:"canonical_model"`
	ServeAs          string `json:"serve_as"`
	ReasoningEffort  string `json:"reasoning_effort"`
	Account          string `json:"account"`
	Provider         string `json:"provider"`
	Target           string `json:"target"`
	ConfigDigest     string `json:"config_digest"`
	AdapterID        string `json:"adapter_id"`
	AdapterDigest    string `json:"adapter_digest"`
	InvocationDigest string `json:"invocation_digest"`
}

// Validate rejects incomplete tuples and unrepresentable reasoning. No
// max/xhigh/default translation is performed at this consumption boundary.
func (binding ExecutionBinding) Validate() error {
	if binding.Schema != 1 {
		return invalidf("execution schema is unsupported")
	}
	for name, value := range map[string]string{
		"canonical_model": binding.CanonicalModel, "serve_as": binding.ServeAs,
		"account": binding.Account, "provider": binding.Provider, "target": binding.Target,
		"adapter_id": binding.AdapterID,
	} {
		if err := validateText(name, value, true); err != nil {
			return err
		}
	}
	switch binding.ReasoningEffort {
	case "none", "low", "medium", "high":
	default:
		return invalidf("execution reasoning is unrepresentable")
	}
	for name, value := range map[string]string{"config_digest": binding.ConfigDigest, "adapter_digest": binding.AdapterDigest, "invocation_digest": binding.InvocationDigest} {
		if !validDigest(value) {
			return invalidf("%s is not a lowercase SHA-256 value", name)
		}
	}
	return nil
}
