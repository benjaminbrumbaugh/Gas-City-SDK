package routingdecision

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func testExecutionPayload(t *testing.T) DecisionPayload {
	t.Helper()
	p := testDecisionPayload(t)
	p.Schema = 2
	p.RecommendationID = "routing/v3:" + strings.Repeat("e", 64)
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["execution"] = map[string]any{
		"schema": 1, "canonical_model": p.Model, "serve_as": p.ServeAs,
		"reasoning_effort": "high", "account": p.Account, "provider": p.Provider,
		"target": p.Target, "config_digest": p.TargetConfigDigest,
		"adapter_id": "test-adapter", "adapter_digest": strings.Repeat("f", 64),
		"invocation_digest": strings.Repeat("a", 64),
	}
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p.BindingID = BindingID(p)
	return p
}

func TestExecutionMatchRequiresEveryLiteralField(t *testing.T) {
	p := testExecutionPayload(t)
	actual := *p.Execution
	if err := p.MatchesExecution(actual); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ExecutionBinding){
		"model":      func(e *ExecutionBinding) { e.CanonicalModel = "other" },
		"literal":    func(e *ExecutionBinding) { e.ServeAs = e.CanonicalModel },
		"reasoning":  func(e *ExecutionBinding) { e.ReasoningEffort = "max" },
		"account":    func(e *ExecutionBinding) { e.Account = "other" },
		"provider":   func(e *ExecutionBinding) { e.Provider = "other" },
		"target":     func(e *ExecutionBinding) { e.Target = "other" },
		"config":     func(e *ExecutionBinding) { e.ConfigDigest = strings.Repeat("c", 64) },
		"adapter":    func(e *ExecutionBinding) { e.AdapterDigest = strings.Repeat("c", 64) },
		"invocation": func(e *ExecutionBinding) { e.InvocationDigest = strings.Repeat("c", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := actual
			mutate(&changed)
			if err := p.MatchesExecution(changed); err == nil {
				t.Fatal("altered executable tuple accepted")
			}
		})
	}
	p.Execution = nil
	if err := p.MatchesExecution(actual); err == nil {
		t.Fatal("missing selection accepted")
	}
}

func TestExecutionBindingRejectsIncompleteTypedTuples(t *testing.T) {
	p := testExecutionPayload(t)
	encoded, err := json.Marshal(p.Execution)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for field := range fields {
		t.Run("missing_"+field, func(t *testing.T) {
			copyFields := make(map[string]json.RawMessage, len(fields))
			for k, v := range fields {
				if k != field {
					copyFields[k] = v
				}
			}
			wire, err := json.Marshal(copyFields)
			if err != nil {
				t.Fatal(err)
			}
			var tuple ExecutionBinding
			if err := json.Unmarshal(wire, &tuple); err != nil {
				t.Fatal(err)
			}
			if err := tuple.Validate(); err == nil {
				t.Fatal("incomplete tuple accepted")
			}
		})
	}
	for _, value := range []string{"max", "xhigh", "", "HIGH"} {
		tuple := *p.Execution
		tuple.ReasoningEffort = value
		if err := tuple.Validate(); err == nil {
			t.Fatalf("unrepresentable reasoning %q accepted", value)
		}
	}
	for _, wire := range []string{`{"reasoning_effort":{"high":null}}`, `{"serve_as":42}`, `{"account":[]}`} {
		var tuple ExecutionBinding
		if err := json.Unmarshal([]byte(wire), &tuple); err == nil {
			t.Fatalf("foreign wire type accepted: %s", wire)
		}
	}
}

func TestCitySchemaTwoSignedVectorMatchesSDK(t *testing.T) {
	// Produced by the separate City's issuer.go/execution.go through its actual
	// Issuer.Sign. Zero test seed, not a production authority. See engdocs plan
	// for source hashes and regeneration provenance.
	encoded, err := os.ReadFile("testdata/city_schema2_signed.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Envelope struct {
			Payload   DecisionPayload `json:"payload"`
			Approval  ApprovalPayload `json:"approval"`
			Signature Signature       `json:"signature"`
		} `json:"envelope"`
		PublicKey    []byte `json:"public_key"`
		SigningBytes []byte `json:"signing_bytes"`
	}
	if err := json.Unmarshal(encoded, &vector); err != nil {
		t.Fatal(err)
	}
	p, a, s := vector.Envelope.Payload, vector.Envelope.Approval, vector.Envelope.Signature
	preimage, err := SigningBytes(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preimage, vector.SigningBytes) {
		t.Fatal("City and SDK canonical signing preimages differ")
	}
	if p.BindingID != BindingID(p) {
		t.Fatal("City and SDK binding digests differ")
	}
	verifier := NewVerifier(map[string]ed25519.PublicKey{a.AuthorityID: vector.PublicKey})
	if err := verifier.Verify(p, a, s); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionSchemaUsesVersionedSigningDomains(t *testing.T) {
	p := testExecutionPayload(t)
	approval := ApprovalPayload{Schema: ExecutionSchemaVersion, DecisionID: p.DecisionID, BindingID: p.BindingID, AuthorityID: "board", ApprovedAt: p.CreatedAt}
	decision, err := CanonicalDecisionBytes(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(decision, []byte("gascity.routing-decision.v2\x00")) {
		t.Fatal("schema-2 decision uses legacy domain")
	}
	approved, err := CanonicalApprovalBytes(approval)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(approved, []byte("gascity.routing-decision-approval.v2\x00")) {
		t.Fatal("schema-2 approval uses legacy domain")
	}
	signed, err := SigningBytes(p, approval)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(signed, []byte("gascity.routing-decision-signature.v2\x00")) {
		t.Fatal("schema-2 signature uses legacy domain")
	}
	canonical, err := json.Marshal(canonicalBinding{Schema: p.Schema, Execution: p.Execution, RecommendationID: p.RecommendationID, WorkBeadID: p.WorkBeadID, WorkRevision: p.WorkRevision, ClaimFence: p.ClaimFence, WorkStateDigest: p.WorkStateDigest, City: p.City, Rig: p.Rig, Target: p.Target, TargetConfigDigest: p.TargetConfigDigest, NoMigration: p.NoMigration})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte("gascity.routing-decision-binding.v2\x00"), canonical...))
	if BindingID(p) != hex.EncodeToString(digest[:]) {
		t.Fatal("schema-2 binding uses legacy domain")
	}
}

func TestV3ExecutionSelectionIsSignedAndBound(t *testing.T) {
	p := testExecutionPayload(t)
	if err := p.Validate(); err != nil {
		t.Fatalf("complete v3 execution rejected: %v", err)
	}
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	approval := ApprovalPayload{Schema: 2, DecisionID: p.DecisionID, BindingID: p.BindingID, AuthorityID: "board", ApprovedAt: p.CreatedAt}
	signed, err := SigningBytes(p, approval)
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(map[string]ed25519.PublicKey{"board": key.Public().(ed25519.PublicKey)})
	sig := Signature{Algorithm: SignatureAlgorithmEd25519, AuthorityID: "board", Value: ed25519.Sign(key, signed)}
	if err := verifier.Verify(p, approval, sig); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	data, _ := json.Marshal(p)
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["execution"].(map[string]any)["reasoning_effort"] = "low"
	data, _ = json.Marshal(fields)
	var changed DecisionPayload
	if err := json.Unmarshal(data, &changed); err != nil {
		t.Fatal(err)
	}
	changed.BindingID = BindingID(changed)
	if changed.BindingID == p.BindingID {
		t.Fatal("reasoning change did not change binding")
	}
	approval.BindingID = changed.BindingID
	if err := verifier.Verify(changed, approval, sig); err == nil {
		t.Fatal("signature accepted changed reasoning")
	}
}
