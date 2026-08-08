package routingdecision

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

func testDecisionPayload(t *testing.T) DecisionPayload {
	t.Helper()
	payload := DecisionPayload{
		Schema:             SchemaVersion,
		DecisionID:         "decision-001",
		WorkBeadID:         "GC-WORK-1",
		WorkRevision:       7,
		ClaimFence:         3,
		WorkStateDigest:    strings.Repeat("a", sha256.Size*2),
		City:               "test-city",
		Rig:                "demo",
		Target:             "demo/reviewer",
		TargetConfigDigest: strings.Repeat("b", sha256.Size*2),
		PolicyDigest:       strings.Repeat("c", sha256.Size*2),
		ObservationDigest:  strings.Repeat("d", sha256.Size*2),
		Model:              "model-a",
		Source:             "governor-a",
		Account:            "account-a",
		ServeAs:            "reviewer",
		Provider:           "provider-a",
		Endpoint:           "endpoint-a",
		Reason:             "fresh work requires review",
		Evidence:           []string{"policy:allow", "capacity:available"},
		Alternatives:       []Alternative{{Target: "demo/worker", Model: "model-b", Source: "governor-a", Account: "account-a", Reason: "lower priority"}},
		Options:            []AuditOption{{Key: "permission_mode", Value: "plan"}},
		CreatedAt:          time.Date(2026, 8, 7, 20, 1, 2, 345, time.UTC),
		ExpiresAt:          time.Date(2026, 8, 7, 20, 6, 2, 345, time.UTC),
		NoMigration:        true,
	}
	payload.BindingID = BindingID(payload)
	return payload
}

func TestWorkStateDigestBindsDecisionOwnershipMetadata(t *testing.T) {
	metadata := map[string]string{
		beadmeta.RoutedToMetadataKey:            "demo/reviewer",
		beadmeta.RoutingDecisionIDMetadataKey:   "decision-001",
		beadmeta.ExecutionRigContextMetadataKey: "demo",
		beadmeta.ContinuationGroupMetadataKey:   "convoy-1",
	}
	state := WorkStateFrom("GC-WORK-1", "open", "", metadata, 4)
	first := WorkStateDigest(state)
	metadata[beadmeta.RoutingDecisionIDMetadataKey] = "decision-002"
	second := WorkStateDigest(WorkStateFrom("GC-WORK-1", "open", "", metadata, 4))
	if !validDigest(first) || first == second {
		t.Fatalf("work state digests = (%q, %q), want distinct SHA-256 values", first, second)
	}
}

func TestDecisionCanonicalBytesAndBindingAreDeterministic(t *testing.T) {
	payload := testDecisionPayload(t)
	first, err := CanonicalDecisionBytes(payload)
	if err != nil {
		t.Fatalf("canonical decision: %v", err)
	}
	second, err := CanonicalDecisionBytes(payload)
	if err != nil {
		t.Fatalf("canonical decision again: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical bytes drifted:\nfirst  %s\nsecond %s", first, second)
	}
	const wantBinding = "4d9fc9dcfd974bf49ca39467b3e1a898ea8402886b6d7e1f6fd744dff3c2dbb1"
	const wantJSON = `{"schema":1,"decision_id":"decision-001","binding_id":"4d9fc9dcfd974bf49ca39467b3e1a898ea8402886b6d7e1f6fd744dff3c2dbb1","work_bead_id":"GC-WORK-1","work_revision":7,"claim_fence":3,"work_state_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","city":"test-city","rig":"demo","target":"demo/reviewer","target_config_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","policy_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","observation_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","model":"model-a","source":"governor-a","account":"account-a","serve_as":"reviewer","provider":"provider-a","endpoint":"endpoint-a","reason":"fresh work requires review","evidence":["policy:allow","capacity:available"],"alternatives":[{"target":"demo/worker","model":"model-b","source":"governor-a","account":"account-a","serve_as":"","provider":"","endpoint":"","reason":"lower priority"}],"options":[{"key":"permission_mode","value":"plan"}],"created_at":"2026-08-07T20:01:02.000000345Z","expires_at":"2026-08-07T20:06:02.000000345Z","no_migration":true}`
	if payload.BindingID != wantBinding || string(first) != decisionDomain+wantJSON {
		t.Fatalf("canonical golden drifted: binding=%q bytes=%q", payload.BindingID, first)
	}
	if got, want := BindingID(payload), payload.BindingID; got != want {
		t.Fatalf("BindingID() = %q, want %q", got, want)
	}
	if !strings.Contains(string(first), `"options":[{"key":"permission_mode","value":"plan"}]`) {
		t.Fatalf("canonical bytes did not retain ordered typed audit options: %s", first)
	}
	if !strings.Contains(string(first), `"created_at":"2026-08-07T20:01:02.000000345Z"`) {
		t.Fatalf("canonical timestamp is not fixed-width nanosecond UTC: %s", first)
	}

	sum := sha256.Sum256(first)
	if got := hex.EncodeToString(sum[:]); got == strings.Repeat("0", sha256.Size*2) {
		t.Fatal("canonical digest unexpectedly zero")
	}
}

func TestBindingIDCommitsOnlyToAdmissionBindingFields(t *testing.T) {
	payload := testDecisionPayload(t)
	want := payload.BindingID

	auditOnly := payload
	auditOnly.DecisionID = "different-caller-id"
	auditOnly.Reason = "different audit explanation"
	auditOnly.Evidence = []string{"different:evidence"}
	auditOnly.CreatedAt = auditOnly.CreatedAt.Add(time.Second)
	auditOnly.ExpiresAt = auditOnly.ExpiresAt.Add(time.Second)
	if got := BindingID(auditOnly); got != want {
		t.Fatalf("audit-only changes altered binding: got %q, want %q", got, want)
	}

	bound := payload
	bound.Target = "demo/worker"
	if got := BindingID(bound); got == want {
		t.Fatal("target change did not alter binding")
	}
}

func TestVerifierRejectsTamperingAndUnknownAuthority(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := testDecisionPayload(t)
	approval := ApprovalPayload{
		Schema:      SchemaVersion,
		DecisionID:  payload.DecisionID,
		BindingID:   payload.BindingID,
		AuthorityID: "security-board",
		ApprovedAt:  payload.CreatedAt.Add(time.Second),
	}
	signing, err := SigningBytes(payload, approval)
	if err != nil {
		t.Fatalf("signing bytes: %v", err)
	}
	signature := Signature{Algorithm: SignatureAlgorithmEd25519, AuthorityID: approval.AuthorityID, Value: ed25519.Sign(privateKey, signing)}
	verifier := NewVerifier(map[string]ed25519.PublicKey{approval.AuthorityID: publicKey})
	if err := verifier.Verify(payload, approval, signature); err != nil {
		t.Fatalf("verify valid signature: %v", err)
	}

	tampered := payload
	tampered.Target = "demo/worker"
	if err := verifier.Verify(tampered, approval, signature); err == nil {
		t.Fatal("Verify accepted tampered decision")
	}
	unknown := signature
	unknown.AuthorityID = "unknown"
	if err := verifier.Verify(payload, approval, unknown); err == nil {
		t.Fatal("Verify accepted unknown authority")
	}
	if err := (Verifier{}).Verify(payload, approval, signature); err == nil {
		t.Fatal("zero verifier did not default deny")
	}
}

func TestDecisionValidationRejectsBindingAndTemporalDrift(t *testing.T) {
	payload := testDecisionPayload(t)
	if err := payload.Validate(); err != nil {
		t.Fatalf("valid payload: %v", err)
	}

	wrongBinding := payload
	wrongBinding.BindingID = strings.Repeat("f", sha256.Size*2)
	if err := wrongBinding.Validate(); err == nil {
		t.Fatal("Validate accepted mismatched binding ID")
	}
	badTime := payload
	badTime.ExpiresAt = badTime.CreatedAt
	badTime.BindingID = BindingID(badTime)
	if err := badTime.Validate(); err == nil {
		t.Fatal("Validate accepted non-increasing validity window")
	}
	duplicateOption := payload
	duplicateOption.Options = append(duplicateOption.Options, duplicateOption.Options[0])
	duplicateOption.BindingID = BindingID(duplicateOption)
	if err := duplicateOption.Validate(); err == nil {
		t.Fatal("Validate accepted duplicate audit option key")
	}
	badAlternativeAudit := payload
	badAlternativeAudit.Alternatives[0].Reason = "line one\nline two"
	if err := badAlternativeAudit.Validate(); err == nil {
		t.Fatal("Validate accepted control-bearing alternative audit text")
	}
	outsideIndexRange := testDecisionPayload(t)
	outsideIndexRange.CreatedAt = time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)
	outsideIndexRange.ExpiresAt = outsideIndexRange.CreatedAt.Add(time.Hour)
	outsideIndexRange.BindingID = BindingID(outsideIndexRange)
	if err := outsideIndexRange.Validate(); err == nil {
		t.Fatal("Validate accepted time outside the durable expiry index range")
	}
}
