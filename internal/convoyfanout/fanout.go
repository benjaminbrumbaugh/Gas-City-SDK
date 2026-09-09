// Package convoyfanout admits convoy lifecycle callbacks for mechanical
// delivery. It turns one validated lifecycle event into one durable delivery
// record per authorized logical recipient, before any transport side effect.
//
// The package owns admission only. It decides which recipients are authorized,
// derives their replay-stable identities, and persists the admission snapshot.
// It does not submit anything, does not interpret a route, and does not own a
// transport. These rules are stable and load-bearing:
//
//   - Route identity is opaque data. It is never authority, never a target
//     selection, and never a URL. Route equality never merges two logical
//     recipients.
//   - Admission is not delivery. A record admitted here is queued; queue
//     admission must never be reported as a delivered notification. Submission
//     receipts, reconciliation, responses, ReceivedAt, and terminal outcomes
//     belong to the delivery-outcome boundary, which extends these records.
//   - The one configured External Coordination target is the only transport
//     target. It is resolved at admission time into a TargetFence, and a
//     changed or missing fence fails closed instead of redirecting an existing
//     record to a target that was never authorized for that event.
//   - Notification is not intervention. Lifecycle fan-out reports convoy state;
//     it never authorizes a recipient to mutate work or spawn execution.
//   - Credentials, callback URLs, and prompt bodies never enter these durable
//     records.
package convoyfanout

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/convoycallback"
)

// KeyVersion is the version marker mixed into every idempotency key. Changing
// it changes every derived key, so it is part of the durable contract.
const KeyVersion = "gc-callback/v1"

// SchemaVersion is the durable delivery-record schema version.
const SchemaVersion = 1

// principalPrefix and defaultPrefix namespace logical recipient identities so
// an explicit authorization principal can never collide with the configured
// default target, and so the configured default stays a distinct recipient.
const (
	principalPrefix = "principal:"
	defaultPrefix   = "default:"
)

var (
	// ErrInvalidInput reports malformed admission input.
	ErrInvalidInput = errors.New("convoy callback fan-out invalid input")
	// ErrUnsupportedIntent reports an intent this boundary does not admit.
	// Intervention and execution requests need their own authorization.
	ErrUnsupportedIntent = errors.New("convoy callback fan-out admits notification intent only")
	// ErrNoAuthorizedRecipient reports that no recipient was authorized. The
	// returned admission still carries the auditable rejection reasons.
	ErrNoAuthorizedRecipient = errors.New("convoy callback fan-out authorized no recipient")
	// ErrStaleTargetFence reports that the configured target no longer matches
	// the fence persisted with an existing delivery record.
	ErrStaleTargetFence = errors.New("convoy callback delivery target fence is stale")
	// ErrEventIdentityConflict reports that an immutable event field changed for
	// an event ID that already has durable delivery records.
	ErrEventIdentityConflict = errors.New("convoy callback delivery event identity conflict")
	// ErrCorruptRecord reports malformed durable delivery data.
	ErrCorruptRecord = errors.New("convoy callback delivery durable record is corrupt")
)

// DeliveryIntent separates lifecycle notification from intervention. A
// notification reports convoy state; an intervention asks a recipient to act
// and requires its own authorization, scope, and outcome contract.
type DeliveryIntent string

const (
	// IntentNotification delivers lifecycle information only.
	IntentNotification DeliveryIntent = "notification"
	// IntentIntervention requests an action. Fan-out never admits it.
	IntentIntervention DeliveryIntent = "intervention"
)

// DeliveryState is the durable delivery state. Fan-out only ever writes
// StateQueued: admission proves authorization, not delivery. Post-admission
// states (submitted, uncertain, reconciled, responded, failed,
// outcome_recorded) are owned by the delivery-outcome boundary.
type DeliveryState string

// StateQueued marks an authorized delivery admitted with no submission started.
const StateQueued DeliveryState = "queued"

// RecipientKind records how a recipient entered the admission set.
type RecipientKind string

const (
	// KindSubscription is a durable owner- and generation-fenced subscription.
	KindSubscription RecipientKind = "subscription"
	// KindLaunchOrigin is the captured launching actor.
	KindLaunchOrigin RecipientKind = "launch_origin"
	// KindConfiguredDefault is the sole configured External Coordination target.
	KindConfiguredDefault RecipientKind = "configured_default"
)

// AdmissionReason is the auditable reason a recipient was admitted.
type AdmissionReason string

const (
	// ReasonSubscriptionAuthorized admits a matching fenced subscription.
	ReasonSubscriptionAuthorized AdmissionReason = "subscription_authorized"
	// ReasonLaunchOriginAuthorized admits a trusted launching actor.
	ReasonLaunchOriginAuthorized AdmissionReason = "launch_origin_authorized"
	// ReasonConfiguredDefaultTarget admits the configured default target.
	ReasonConfiguredDefaultTarget AdmissionReason = "configured_default_target"
)

// RejectionReason is the auditable reason a candidate was not admitted.
type RejectionReason string

const (
	// RejectLaunchOriginUntrusted rejects an origin the caller did not trust.
	RejectLaunchOriginUntrusted RejectionReason = "launch_origin_untrusted"
	// RejectSubscriptionInactive rejects a revoked or unregistered subscription.
	RejectSubscriptionInactive RejectionReason = "subscription_not_active"
	// RejectSubscriptionUnfenced rejects a subscription with no registration fence.
	RejectSubscriptionUnfenced RejectionReason = "subscription_unfenced"
	// RejectWorkScopeMismatch rejects a scope that does not cover the event.
	RejectWorkScopeMismatch RejectionReason = "work_scope_mismatch"
	// RejectLifecycleInterestMismatch rejects a recipient that did not ask for
	// this lifecycle event.
	RejectLifecycleInterestMismatch RejectionReason = "lifecycle_interest_mismatch"
	// RejectDuplicateLogicalRecipient rejects a second candidate for one
	// logical recipient already admitted through stronger evidence.
	RejectDuplicateLogicalRecipient RejectionReason = "duplicate_logical_recipient"
	// RejectDefaultTargetUnconfigured rejects default admission with no
	// configured External Coordination target.
	RejectDefaultTargetUnconfigured RejectionReason = "default_target_unconfigured"
)

// DefaultRecipientPolicy decides when the configured default target joins the
// admission set. It is caller configuration, never an inference from route data.
type DefaultRecipientPolicy string

const (
	// DefaultWhenNoExplicitRecipient admits the configured target only when no
	// explicit recipient is authorized. This is the delivery-policy default and
	// the zero value.
	DefaultWhenNoExplicitRecipient DefaultRecipientPolicy = "when_no_explicit_recipient"
	// DefaultAlwaysDistinct admits the configured target alongside authorized
	// explicit recipients, as its own distinct logical recipient with its own
	// idempotency key.
	DefaultAlwaysDistinct DefaultRecipientPolicy = "always_distinct"
)

// TargetFence pins the sole configured External Coordination target by opaque
// identity and configuration revision. It is the authorization boundary for a
// delivery; route identity never selects a target.
type TargetFence struct {
	TargetID       string `json:"target_id,omitempty"`
	ConfigRevision int64  `json:"config_revision,omitempty"`
}

// Configured reports whether a target identity is present.
func (f TargetFence) Configured() bool { return strings.TrimSpace(f.TargetID) != "" }

// Equal compares target identity and configuration revision.
func (f TargetFence) Equal(other TargetFence) bool {
	return f.TargetID == other.TargetID && f.ConfigRevision == other.ConfigRevision
}

// WorkScope is the work a recipient is authorized to observe. Every non-empty
// dimension must match the event for the scope to authorize it.
type WorkScope struct {
	City     string   `json:"city,omitempty"`
	ConvoyID string   `json:"convoy_id,omitempty"`
	WorkRefs []string `json:"work_refs,omitempty"`
}

// SubscriptionView is the authorization evidence fan-out consumes for one
// durable subscription. The subscription boundary owns the records themselves;
// SubscriptionViews projects them into this shape.
type SubscriptionView struct {
	ID             string
	Owner          string
	RegistrationID string
	Generation     uint64
	Active         bool
	Scope          WorkScope
	Interests      []convoycallback.EventType
	RouteIdentity  string
}

// LaunchOriginAuthorization is the caller's explicit decision about the
// captured launching actor. Fan-out never infers trust from an origin string.
type LaunchOriginAuthorization struct {
	Trusted       bool
	Principal     string
	Scope         WorkScope
	Interests     []convoycallback.EventType
	RouteIdentity string
}

// RegistrationFence is the subscription incarnation recorded in an admission
// snapshot so the delivery boundary can recheck it before submission.
type RegistrationFence struct {
	RegistrationID string `json:"registration_id"`
	Generation     uint64 `json:"generation"`
}

// RecipientSnapshot is the audit snapshot of one admitted logical recipient. It
// is evidence of the authorization observed at admission, not proof that the
// recipient is still authorized at submission time.
type RecipientSnapshot struct {
	LogicalID      string            `json:"logical_id"`
	Kind           RecipientKind     `json:"kind"`
	Principal      string            `json:"principal,omitempty"`
	SubscriptionID string            `json:"subscription_id,omitempty"`
	Fence          RegistrationFence `json:"fence,omitzero"`
	Scope          WorkScope         `json:"scope,omitzero"`
	RouteIdentity  string            `json:"route_identity,omitempty"`
	IsDefault      bool              `json:"is_default"`
	Reason         AdmissionReason   `json:"reason"`
}

// RecipientRejection records why a candidate recipient was not admitted.
type RecipientRejection struct {
	Kind      RecipientKind   `json:"kind"`
	LogicalID string          `json:"logical_id"`
	Reason    RejectionReason `json:"reason"`
}

// AdmitInput is one lifecycle event plus the authorization evidence and
// configuration needed to admit its recipients.
type AdmitInput struct {
	// City is the admitting city. A subscription scoped to a city authorizes
	// only events admitted by that city; the event itself carries no city.
	City string
	// Event is the immutable lifecycle event. It must satisfy the callback
	// contract before any recipient is considered.
	Event convoycallback.Event
	// Intent must be IntentNotification.
	Intent DeliveryIntent
	// LaunchOrigin is the caller's decision about the launching actor.
	LaunchOrigin LaunchOriginAuthorization
	// Subscriptions is the durable subscription evidence, in any order.
	Subscriptions []SubscriptionView
	// Target is the sole configured External Coordination target, resolved by
	// the caller at admission time.
	Target TargetFence
	// DefaultPolicy decides when Target joins the admission set.
	DefaultPolicy DefaultRecipientPolicy
	// Now is the injected admission clock. Every persisted timestamp uses it.
	Now time.Time
}

// IdempotencyKey derives the replay-stable, recipient-specific delivery key. It
// is the SHA-256 digest of a canonical length-prefixed tuple of the version
// marker, the immutable event ID, and the logical recipient ID. Attempt number,
// target identity, route data, response time, and mutable configuration are
// deliberately excluded so retries and reconciliation reuse one key.
func IdempotencyKey(eventID, logicalRecipientID string) (string, error) {
	if strings.TrimSpace(eventID) == "" {
		return "", invalidInput("event id required")
	}
	if strings.TrimSpace(logicalRecipientID) == "" {
		return "", invalidInput("logical recipient id required")
	}
	digest := sha256.New()
	for _, field := range []string{KeyVersion, eventID, logicalRecipientID} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		digest.Write(length[:])
		digest.Write([]byte(field))
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// SelectRecipients is the pure admission decision. It returns the deterministic
// authorized recipient set and an auditable rejection for every candidate that
// was considered and refused. When nothing is authorized it returns the
// rejections together with ErrNoAuthorizedRecipient so the caller cannot treat
// an empty fan-out as a successful one.
func SelectRecipients(input AdmitInput) ([]RecipientSnapshot, []RecipientRejection, error) {
	normalized, err := normalizeAdmitInput(input)
	if err != nil {
		return nil, nil, err
	}
	return selectNormalizedRecipients(normalized)
}

// selectNormalizedRecipients is the admission decision over already-normalized
// input, so the durable path normalizes exactly once.
func selectNormalizedRecipients(normalized AdmitInput) ([]RecipientSnapshot, []RecipientRejection, error) {
	candidates, rejections, err := candidateRecipients(normalized)
	if err != nil {
		return nil, nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		if candidates[i].snapshot.LogicalID != candidates[j].snapshot.LogicalID {
			return candidates[i].snapshot.LogicalID < candidates[j].snapshot.LogicalID
		}
		return candidates[i].snapshot.SubscriptionID < candidates[j].snapshot.SubscriptionID
	})
	admitted := make([]RecipientSnapshot, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	explicit := 0
	for _, candidate := range candidates {
		if _, duplicate := seen[candidate.snapshot.LogicalID]; duplicate {
			rejections = append(rejections, RecipientRejection{
				Kind:      candidate.snapshot.Kind,
				LogicalID: candidate.snapshot.LogicalID,
				Reason:    RejectDuplicateLogicalRecipient,
			})
			continue
		}
		seen[candidate.snapshot.LogicalID] = struct{}{}
		admitted = append(admitted, candidate.snapshot)
		if !candidate.snapshot.IsDefault {
			explicit++
		}
	}
	admitted, rejections = applyDefaultPolicy(normalized, admitted, rejections, explicit)
	if len(admitted) == 0 {
		return nil, rejections, ErrNoAuthorizedRecipient
	}
	return admitted, rejections, nil
}

// rankedCandidate carries the admission preference for one candidate. A fenced
// subscription outranks the launching actor for the same logical principal
// because it is the stronger, durable authorization evidence.
type rankedCandidate struct {
	rank     int
	snapshot RecipientSnapshot
}

func candidateRecipients(input AdmitInput) ([]rankedCandidate, []RecipientRejection, error) {
	candidates := make([]rankedCandidate, 0, len(input.Subscriptions)+1)
	rejections := make([]RecipientRejection, 0, len(input.Subscriptions)+1)
	for _, view := range input.Subscriptions {
		owner, err := validateIdentity("subscription owner", view.Owner, true)
		if err != nil {
			return nil, nil, err
		}
		if _, err := validateIdentity("subscription id", view.ID, false); err != nil {
			return nil, nil, err
		}
		logicalID := principalPrefix + owner
		if reason, ok := rejectSubscription(input, view); ok {
			rejections = append(rejections, RecipientRejection{Kind: KindSubscription, LogicalID: logicalID, Reason: reason})
			continue
		}
		candidates = append(candidates, rankedCandidate{rank: 0, snapshot: RecipientSnapshot{
			LogicalID:      logicalID,
			Kind:           KindSubscription,
			Principal:      owner,
			SubscriptionID: view.ID,
			Fence:          RegistrationFence{RegistrationID: view.RegistrationID, Generation: view.Generation},
			Scope:          cloneWorkScope(view.Scope),
			RouteIdentity:  view.RouteIdentity,
			Reason:         ReasonSubscriptionAuthorized,
		}})
	}
	origin := input.LaunchOrigin
	if !origin.Trusted {
		logicalID := principalPrefix + strings.TrimSpace(origin.Principal)
		if strings.TrimSpace(origin.Principal) == "" {
			logicalID = principalPrefix + input.Event.LaunchOrigin
		}
		rejections = append(rejections, RecipientRejection{Kind: KindLaunchOrigin, LogicalID: logicalID, Reason: RejectLaunchOriginUntrusted})
		return candidates, rejections, nil
	}
	principal, err := validateIdentity("launch origin principal", origin.Principal, true)
	if err != nil {
		return nil, nil, err
	}
	logicalID := principalPrefix + principal
	if reason, ok := rejectLaunchOrigin(input, origin); ok {
		rejections = append(rejections, RecipientRejection{Kind: KindLaunchOrigin, LogicalID: logicalID, Reason: reason})
		return candidates, rejections, nil
	}
	candidates = append(candidates, rankedCandidate{rank: 1, snapshot: RecipientSnapshot{
		LogicalID:     logicalID,
		Kind:          KindLaunchOrigin,
		Principal:     principal,
		Scope:         cloneWorkScope(origin.Scope),
		RouteIdentity: origin.RouteIdentity,
		Reason:        ReasonLaunchOriginAuthorized,
	}})
	return candidates, rejections, nil
}

func rejectSubscription(input AdmitInput, view SubscriptionView) (RejectionReason, bool) {
	switch {
	case !view.Active:
		return RejectSubscriptionInactive, true
	case strings.TrimSpace(view.RegistrationID) == "" || view.Generation == 0:
		return RejectSubscriptionUnfenced, true
	case !scopeCovers(input, view.Scope):
		return RejectWorkScopeMismatch, true
	case !interestCovers(view.Interests, input.Event.Type):
		return RejectLifecycleInterestMismatch, true
	}
	return "", false
}

func rejectLaunchOrigin(input AdmitInput, origin LaunchOriginAuthorization) (RejectionReason, bool) {
	switch {
	case !scopeCovers(input, origin.Scope):
		return RejectWorkScopeMismatch, true
	case !interestCovers(origin.Interests, input.Event.Type):
		return RejectLifecycleInterestMismatch, true
	}
	return "", false
}

// applyDefaultPolicy admits the sole configured External Coordination target as
// its own distinct logical recipient. Selecting the configured target happens
// here, at admission, and is never failover from a submission that already
// started.
func applyDefaultPolicy(input AdmitInput, admitted []RecipientSnapshot, rejections []RecipientRejection, explicit int) ([]RecipientSnapshot, []RecipientRejection) {
	if input.DefaultPolicy == DefaultWhenNoExplicitRecipient && explicit > 0 {
		return admitted, rejections
	}
	logicalID := defaultPrefix + input.Target.TargetID
	if !input.Target.Configured() {
		return admitted, append(rejections, RecipientRejection{
			Kind:      KindConfiguredDefault,
			LogicalID: defaultPrefix,
			Reason:    RejectDefaultTargetUnconfigured,
		})
	}
	return append(admitted, RecipientSnapshot{
		LogicalID: logicalID,
		Kind:      KindConfiguredDefault,
		Principal: input.Target.TargetID,
		IsDefault: true,
		Reason:    ReasonConfiguredDefaultTarget,
	}), rejections
}

// scopeCovers reports whether every dimension the scope declares matches the
// admitting city and the event. A scope with no dimension authorizes nothing.
func scopeCovers(input AdmitInput, scope WorkScope) bool {
	declared := false
	if city := strings.TrimSpace(scope.City); city != "" {
		if city != input.City {
			return false
		}
		declared = true
	}
	if convoyID := strings.TrimSpace(scope.ConvoyID); convoyID != "" {
		if convoyID != input.Event.ConvoyID {
			return false
		}
		declared = true
	}
	if len(scope.WorkRefs) > 0 {
		if !containsWorkRef(scope.WorkRefs, input.Event) {
			return false
		}
		declared = true
	}
	return declared
}

func containsWorkRef(refs []string, event convoycallback.Event) bool {
	for _, ref := range refs {
		switch strings.TrimSpace(ref) {
		case "":
			continue
		case event.ConvoyID, event.TaskID, event.DeploymentID:
			return true
		}
	}
	return false
}

func interestCovers(interests []convoycallback.EventType, kind convoycallback.EventType) bool {
	for _, interest := range interests {
		if interest == kind {
			return true
		}
	}
	return false
}

func normalizeAdmitInput(input AdmitInput) (AdmitInput, error) {
	if input.Intent == "" {
		input.Intent = IntentNotification
	}
	if input.Intent != IntentNotification {
		return AdmitInput{}, fmt.Errorf("%w: %q", ErrUnsupportedIntent, input.Intent)
	}
	if input.DefaultPolicy == "" {
		input.DefaultPolicy = DefaultWhenNoExplicitRecipient
	}
	if input.DefaultPolicy != DefaultWhenNoExplicitRecipient && input.DefaultPolicy != DefaultAlwaysDistinct {
		return AdmitInput{}, invalidInput(fmt.Sprintf("unknown default recipient policy %q", input.DefaultPolicy))
	}
	city, err := validateIdentity("admitting city", input.City, true)
	if err != nil {
		return AdmitInput{}, err
	}
	input.City = city
	if err := input.Event.Validate(); err != nil {
		return AdmitInput{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if input.Now.IsZero() {
		return AdmitInput{}, invalidInput("admission clock required")
	}
	if !input.Target.Configured() && input.Target.ConfigRevision != 0 {
		return AdmitInput{}, invalidInput("target configuration revision requires a target identity")
	}
	targetID, err := validateIdentity("target id", input.Target.TargetID, false)
	if err != nil {
		return AdmitInput{}, err
	}
	// The fence is persisted and compared verbatim on every re-admission, so it
	// carries the canonical target identity rather than whatever padding the
	// caller's configuration happened to hold. Otherwise the same configured
	// target would fail closed as a stale fence purely on surrounding space.
	input.Target.TargetID = targetID
	input.Event.RouteIdentity = cloneRouteIdentity(input.Event.RouteIdentity)
	return input, nil
}

// validateIdentity mirrors the subscription boundary: an identity names exactly
// one value, so a comma-joined identity is rejected rather than split.
func validateIdentity(kind, value string, required bool) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return "", invalidInput(kind + " required")
		}
		return "", nil
	}
	if strings.Contains(trimmed, ",") {
		return "", invalidInput(kind + " must identify exactly one value")
	}
	return trimmed, nil
}

func cloneWorkScope(scope WorkScope) WorkScope {
	out := WorkScope{City: scope.City, ConvoyID: scope.ConvoyID}
	if len(scope.WorkRefs) > 0 {
		out.WorkRefs = append([]string(nil), scope.WorkRefs...)
	}
	return out
}

func cloneRouteIdentity(route map[string]string) map[string]string {
	if len(route) == 0 {
		return nil
	}
	out := make(map[string]string, len(route))
	for key, value := range route {
		out[key] = value
	}
	return out
}

func invalidInput(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, message)
}
