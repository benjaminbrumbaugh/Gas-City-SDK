// Package routingdecision owns the durable, signed authorization contract for
// routing fresh work to an already configured static target. It contains no
// provider, process, session, or launch behavior.
package routingdecision

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// SchemaVersion is the only routing-decision schema understood by this build.
	SchemaVersion = 1

	// SignatureAlgorithmEd25519 is the only approval signature algorithm.
	SignatureAlgorithmEd25519 = "ed25519"

	decisionDomain = "gascity.routing-decision.v1\x00"
	approvalDomain = "gascity.routing-decision-approval.v1\x00"
	signingDomain  = "gascity.routing-decision-signature.v1\x00"

	maxStringBytes  = 4096
	maxEvidence     = 64
	maxAlternatives = 32
	maxOptions      = 64
)

var (
	// ErrInvalidDecision classifies malformed or internally inconsistent data.
	ErrInvalidDecision = errors.New("invalid routing decision")
	// ErrAuthorizationRequired classifies the default-deny verifier state.
	ErrAuthorizationRequired = errors.New("routing decision authorization required")
	// ErrUnknownAuthority classifies signatures naming an unconfigured authority.
	ErrUnknownAuthority = errors.New("unknown routing decision authority")
	// ErrInvalidSignature classifies an approval signature mismatch.
	ErrInvalidSignature = errors.New("invalid routing decision signature")
)

// AuditOption is a typed, ordered audit fact. Canonical encoding sorts options
// by key so equivalent inputs have one signed representation.
type AuditOption struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Alternative records a considered route. Alternatives are evidence only and
// are never selected, probed, or promoted by the controller.
type Alternative struct {
	Target   string `json:"target"`
	Model    string `json:"model"`
	Source   string `json:"source"`
	Account  string `json:"account"`
	ServeAs  string `json:"serve_as"`
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	Reason   string `json:"reason"`
}

// DecisionPayload is immutable once approved. BindingID commits to the exact
// work revision/fence/state and target configuration independently of the
// caller-chosen DecisionID.
type DecisionPayload struct {
	Schema             int           `json:"schema"`
	DecisionID         string        `json:"decision_id"`
	RecommendationID   string        `json:"recommendation_id,omitempty"`
	BindingID          string        `json:"binding_id"`
	WorkBeadID         string        `json:"work_bead_id"`
	WorkRevision       int64         `json:"work_revision"`
	ClaimFence         int64         `json:"claim_fence"`
	WorkStateDigest    string        `json:"work_state_digest"`
	City               string        `json:"city"`
	Rig                string        `json:"rig"`
	Target             string        `json:"target"`
	TargetConfigDigest string        `json:"target_config_digest"`
	PolicyDigest       string        `json:"policy_digest"`
	ObservationDigest  string        `json:"observation_digest"`
	Model              string        `json:"model"`
	Source             string        `json:"source"`
	Account            string        `json:"account"`
	ServeAs            string        `json:"serve_as"`
	Provider           string        `json:"provider"`
	Endpoint           string        `json:"endpoint"`
	Reason             string        `json:"reason"`
	Evidence           []string      `json:"evidence"`
	Alternatives       []Alternative `json:"alternatives"`
	Options            []AuditOption `json:"options"`
	CreatedAt          time.Time     `json:"created_at"`
	ExpiresAt          time.Time     `json:"expires_at"`
	NoMigration        bool          `json:"no_migration"`
}

// ApprovalPayload is the immutable authority-authored portion of an approval.
type ApprovalPayload struct {
	Schema      int       `json:"schema"`
	DecisionID  string    `json:"decision_id"`
	BindingID   string    `json:"binding_id"`
	AuthorityID string    `json:"authority_id"`
	ApprovedAt  time.Time `json:"approved_at"`
}

// Signature is the detached approval signature stored with a record.
type Signature struct {
	Algorithm   string `json:"algorithm"`
	AuthorityID string `json:"authority_id"`
	Value       []byte `json:"value"`
}

type canonicalDecision struct {
	Schema             int           `json:"schema"`
	DecisionID         string        `json:"decision_id"`
	RecommendationID   string        `json:"recommendation_id,omitempty"`
	BindingID          string        `json:"binding_id"`
	WorkBeadID         string        `json:"work_bead_id"`
	WorkRevision       int64         `json:"work_revision"`
	ClaimFence         int64         `json:"claim_fence"`
	WorkStateDigest    string        `json:"work_state_digest"`
	City               string        `json:"city"`
	Rig                string        `json:"rig"`
	Target             string        `json:"target"`
	TargetConfigDigest string        `json:"target_config_digest"`
	PolicyDigest       string        `json:"policy_digest"`
	ObservationDigest  string        `json:"observation_digest"`
	Model              string        `json:"model"`
	Source             string        `json:"source"`
	Account            string        `json:"account"`
	ServeAs            string        `json:"serve_as"`
	Provider           string        `json:"provider"`
	Endpoint           string        `json:"endpoint"`
	Reason             string        `json:"reason"`
	Evidence           []string      `json:"evidence"`
	Alternatives       []Alternative `json:"alternatives"`
	Options            []AuditOption `json:"options"`
	CreatedAt          string        `json:"created_at"`
	ExpiresAt          string        `json:"expires_at"`
	NoMigration        bool          `json:"no_migration"`
}

type canonicalApproval struct {
	Schema      int    `json:"schema"`
	DecisionID  string `json:"decision_id"`
	BindingID   string `json:"binding_id"`
	AuthorityID string `json:"authority_id"`
	ApprovedAt  string `json:"approved_at"`
}

type canonicalBinding struct {
	Schema             int    `json:"schema"`
	WorkBeadID         string `json:"work_bead_id"`
	WorkRevision       int64  `json:"work_revision"`
	ClaimFence         int64  `json:"claim_fence"`
	WorkStateDigest    string `json:"work_state_digest"`
	City               string `json:"city"`
	Rig                string `json:"rig"`
	Target             string `json:"target"`
	TargetConfigDigest string `json:"target_config_digest"`
	NoMigration        bool   `json:"no_migration"`
}

func normalizedDecision(payload DecisionPayload, includeBinding bool) canonicalDecision {
	evidence := append([]string(nil), payload.Evidence...)
	if evidence == nil {
		evidence = []string{}
	}
	alternatives := append([]Alternative(nil), payload.Alternatives...)
	if alternatives == nil {
		alternatives = []Alternative{}
	}
	options := append([]AuditOption(nil), payload.Options...)
	if options == nil {
		options = []AuditOption{}
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Key < options[j].Key })
	bindingID := payload.BindingID
	if !includeBinding {
		bindingID = ""
	}
	return canonicalDecision{
		Schema: payload.Schema, DecisionID: payload.DecisionID, RecommendationID: payload.RecommendationID, BindingID: bindingID,
		WorkBeadID: payload.WorkBeadID, WorkRevision: payload.WorkRevision, ClaimFence: payload.ClaimFence,
		WorkStateDigest: payload.WorkStateDigest, City: payload.City, Rig: payload.Rig, Target: payload.Target,
		TargetConfigDigest: payload.TargetConfigDigest, PolicyDigest: payload.PolicyDigest,
		ObservationDigest: payload.ObservationDigest, Model: payload.Model, Source: payload.Source,
		Account: payload.Account, ServeAs: payload.ServeAs, Provider: payload.Provider, Endpoint: payload.Endpoint,
		Reason: payload.Reason, Evidence: evidence, Alternatives: alternatives, Options: options,
		CreatedAt: canonicalTime(payload.CreatedAt), ExpiresAt: canonicalTime(payload.ExpiresAt), NoMigration: payload.NoMigration,
	}
}

// CanonicalDecisionBytes returns the single schema-fixed signed representation.
func CanonicalDecisionBytes(payload DecisionPayload) ([]byte, error) {
	if err := payload.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalizedDecision(payload, true))
	if err != nil {
		return nil, fmt.Errorf("%w: encode decision", ErrInvalidDecision)
	}
	return append([]byte(decisionDomain), encoded...), nil
}

// CanonicalApprovalBytes returns the schema-fixed approval representation.
func CanonicalApprovalBytes(approval ApprovalPayload) ([]byte, error) {
	if err := approval.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(canonicalApproval{
		Schema: approval.Schema, DecisionID: approval.DecisionID, BindingID: approval.BindingID,
		AuthorityID: approval.AuthorityID, ApprovedAt: canonicalTime(approval.ApprovedAt),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode approval", ErrInvalidDecision)
	}
	return append([]byte(approvalDomain), encoded...), nil
}

// SigningBytes domain-separates and concatenates the immutable decision and approval.
func SigningBytes(payload DecisionPayload, approval ApprovalPayload) ([]byte, error) {
	decision, err := CanonicalDecisionBytes(payload)
	if err != nil {
		return nil, err
	}
	if approval.DecisionID != payload.DecisionID || approval.BindingID != payload.BindingID {
		return nil, fmt.Errorf("%w: approval binding mismatch", ErrInvalidDecision)
	}
	approved, err := CanonicalApprovalBytes(approval)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 0, len(signingDomain)+len(decision)+len(approved)+16)
	result = append(result, signingDomain...)
	result = binary.BigEndian.AppendUint64(result, uint64(len(decision)))
	result = append(result, decision...)
	result = binary.BigEndian.AppendUint64(result, uint64(len(approved)))
	result = append(result, approved...)
	return result, nil
}

// BindingID computes the lowercase SHA-256 commitment for the exact route binding.
func BindingID(payload DecisionPayload) string {
	encoded, err := json.Marshal(canonicalBinding{
		Schema: payload.Schema, WorkBeadID: payload.WorkBeadID, WorkRevision: payload.WorkRevision,
		ClaimFence: payload.ClaimFence, WorkStateDigest: payload.WorkStateDigest, City: payload.City,
		Rig: payload.Rig, Target: payload.Target, TargetConfigDigest: payload.TargetConfigDigest,
		NoMigration: payload.NoMigration,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("gascity.routing-decision-binding.v1\x00"), encoded...))
	return hex.EncodeToString(sum[:])
}

// Validate checks stable decision shape, bounds, canonical timestamps, and binding.
func (payload DecisionPayload) Validate() error {
	if payload.Schema != SchemaVersion {
		return invalidf("schema %d is unsupported", payload.Schema)
	}
	for name, value := range map[string]string{
		"decision_id": payload.DecisionID, "work_bead_id": payload.WorkBeadID, "city": payload.City,
		"target": payload.Target, "model": payload.Model, "source": payload.Source, "account": payload.Account,
	} {
		if err := validateText(name, value, true); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{
		"rig": payload.Rig, "serve_as": payload.ServeAs, "provider": payload.Provider,
		"endpoint": payload.Endpoint, "reason": payload.Reason, "recommendation_id": payload.RecommendationID,
	} {
		if err := validateText(name, value, false); err != nil {
			return err
		}
	}
	if payload.RecommendationID != "" && !validRecommendationID(payload.RecommendationID) {
		return invalidf("recommendation_id must be a routing/v2 decision identity")
	}
	for name, digest := range map[string]string{
		"binding_id": payload.BindingID, "work_state_digest": payload.WorkStateDigest,
		"target_config_digest": payload.TargetConfigDigest, "policy_digest": payload.PolicyDigest,
		"observation_digest": payload.ObservationDigest,
	} {
		if !validDigest(digest) {
			return invalidf("%s must be a lowercase SHA-256 value", name)
		}
	}
	if payload.WorkRevision < 0 || payload.ClaimFence < 0 {
		return invalidf("work revision and claim fence must be non-negative")
	}
	if payload.CreatedAt.IsZero() || payload.ExpiresAt.IsZero() || !payload.ExpiresAt.After(payload.CreatedAt) {
		return invalidf("expires_at must be after created_at")
	}
	if !unixNanoRepresentable(payload.CreatedAt) || !unixNanoRepresentable(payload.ExpiresAt) {
		return invalidf("validity window is outside the durable index range")
	}
	if !payload.NoMigration {
		return invalidf("no_migration must be true")
	}
	if len(payload.Evidence) > maxEvidence || len(payload.Alternatives) > maxAlternatives || len(payload.Options) > maxOptions {
		return invalidf("audit collection exceeds schema bounds")
	}
	for i, item := range payload.Evidence {
		if err := validateText(fmt.Sprintf("evidence[%d]", i), item, true); err != nil {
			return err
		}
	}
	for i, alternative := range payload.Alternatives {
		for name, value := range map[string]string{"target": alternative.Target, "model": alternative.Model, "source": alternative.Source, "account": alternative.Account} {
			if err := validateText(fmt.Sprintf("alternatives[%d].%s", i, name), value, true); err != nil {
				return err
			}
		}
		for name, value := range map[string]string{"serve_as": alternative.ServeAs, "provider": alternative.Provider, "endpoint": alternative.Endpoint, "reason": alternative.Reason} {
			if err := validateText(fmt.Sprintf("alternatives[%d].%s", i, name), value, false); err != nil {
				return err
			}
		}
	}
	seen := make(map[string]struct{}, len(payload.Options))
	for i, option := range payload.Options {
		if err := validateText(fmt.Sprintf("options[%d].key", i), option.Key, true); err != nil {
			return err
		}
		if err := validateText(fmt.Sprintf("options[%d].value", i), option.Value, true); err != nil {
			return err
		}
		if _, exists := seen[option.Key]; exists {
			return invalidf("duplicate audit option %q", option.Key)
		}
		seen[option.Key] = struct{}{}
	}
	if BindingID(payload) != payload.BindingID {
		return invalidf("binding_id does not match decision fields")
	}
	return nil
}

func unixNanoRepresentable(value time.Time) bool {
	return time.Unix(0, value.UTC().UnixNano()).UTC().Equal(value.UTC())
}

// Validate checks approval shape independently of a decision.
func (approval ApprovalPayload) Validate() error {
	if approval.Schema != SchemaVersion {
		return invalidf("approval schema %d is unsupported", approval.Schema)
	}
	for name, value := range map[string]string{"decision_id": approval.DecisionID, "authority_id": approval.AuthorityID} {
		if err := validateText(name, value, true); err != nil {
			return err
		}
	}
	if !validDigest(approval.BindingID) || approval.ApprovedAt.IsZero() {
		return invalidf("approval binding and time are required")
	}
	return nil
}

// IsActiveAt reports whether now falls inside the half-open validity interval.
func (payload DecisionPayload) IsActiveAt(now time.Time) bool {
	now = now.UTC()
	return !now.Before(payload.CreatedAt.UTC()) && now.Before(payload.ExpiresAt.UTC())
}

// Verifier validates detached Ed25519 approvals against an injected allowlist.
// Its zero value intentionally denies every approval.
type Verifier struct {
	keys map[string]ed25519.PublicKey
}

// NewVerifier copies the authority allowlist into an immutable verifier value.
func NewVerifier(keys map[string]ed25519.PublicKey) Verifier {
	copyKeys := make(map[string]ed25519.PublicKey, len(keys))
	for id, key := range keys {
		copyKeys[id] = append(ed25519.PublicKey(nil), key...)
	}
	return Verifier{keys: copyKeys}
}

// Verify authenticates the exact stored decision and approval bytes.
func (verifier Verifier) Verify(payload DecisionPayload, approval ApprovalPayload, signature Signature) error {
	if len(verifier.keys) == 0 {
		return ErrAuthorizationRequired
	}
	if signature.Algorithm != SignatureAlgorithmEd25519 || signature.AuthorityID != approval.AuthorityID {
		return ErrInvalidSignature
	}
	key, ok := verifier.keys[signature.AuthorityID]
	if !ok {
		return ErrUnknownAuthority
	}
	if len(key) != ed25519.PublicKeySize || len(signature.Value) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	signing, err := SigningBytes(payload, approval)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, signing, signature.Value) {
		return ErrInvalidSignature
	}
	return nil
}

func canonicalTime(value time.Time) string {
	return value.UTC().Round(0).Format("2006-01-02T15:04:05.000000000Z")
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateText(name, value string, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return invalidf("%s is required", name)
	}
	if value != strings.TrimSpace(value) || len(value) > maxStringBytes {
		return invalidf("%s is not canonical or exceeds bounds", name)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return invalidf("%s contains a control character", name)
		}
	}
	return nil
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidDecision, fmt.Sprintf(format, args...))
}
