// Package convoysubscription exposes the provider-neutral operation contract
// for a session's convoy callback registration.
//
// The package resolves a trusted-local selector to one open canonical session
// bead, then delegates durable ownership and generation fencing to convoy's
// SubscriptionService. A selector is routing context, not authentication; the
// package does not expose a raw owner override or create an authentication
// boundary. External owners are represented as existing configured bindings
// and are validated as contract data only.
package convoysubscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/session"
)

const (
	// ContractVersion identifies the pinned operation/result contract consumed
	// by a local client or an existing external binding.
	ContractVersion          = "convoy-subscription.v1"
	maxOpaqueLength          = 256
	terminalTransitionReason = "convoy became terminal during subscription"
)

var (
	// ErrOwnerRequired reports a missing local session owner.
	ErrOwnerRequired = errors.New("convoy subscription owner required")
	// ErrOwnerNotOpen reports a session that is not an open canonical owner.
	ErrOwnerNotOpen = errors.New("convoy subscription owner session is not open")
	// ErrTerminalState reports that a late subscription was refused because the
	// scoped convoy already has a terminal state.
	ErrTerminalState = errors.New("convoy subscription refused for terminal convoy")
	// ErrTerminalStateUnavailable reports that a convoy-scoped subscription
	// cannot be admitted without an authoritative terminal-state lookup.
	ErrTerminalStateUnavailable = errors.New("convoy terminal-state authority unavailable")
)

// Operation names one subscription operation at the package boundary.
type Operation string

const (
	// OperationSubscribe registers the caller's own session for a convoy.
	OperationSubscribe Operation = "subscribe"
	// OperationList lists all durable registrations owned by the exact session.
	OperationList Operation = "list"
	// OperationRenew advances a registration fence and lease.
	OperationRenew Operation = "renew"
	// OperationRetire removes a registration under its current fence.
	OperationRetire Operation = "retire"
)

// ResultStatus is the truthful result state of an operation.
type ResultStatus string

const (
	// StatusSubscribed means a durable active registration was created.
	StatusSubscribed ResultStatus = "subscribed"
	// StatusListed means the owner list was read successfully.
	StatusListed ResultStatus = "listed"
	// StatusRenewed means a lease and generation were advanced.
	StatusRenewed ResultStatus = "renewed"
	// StatusRetired means the registration was durably unregistered.
	StatusRetired ResultStatus = "retired"
	// StatusTerminalRefused means a late registration was explicitly refused.
	StatusTerminalRefused ResultStatus = "terminal_refused"
)

// OwnerKind distinguishes a canonical local session from an existing external
// binding. It is a routing contract distinction, not an authentication claim.
type OwnerKind string

const (
	// OwnerLocalSession identifies a canonical Gas City session bead.
	OwnerLocalSession OwnerKind = "local_session"
	// OwnerExternalBinding identifies an already-authorized external binding.
	OwnerExternalBinding OwnerKind = "external_binding"
)

// Owner is the immutable owner identity carried in an operation result or an
// external contract request. Local results contain the canonical session bead
// ID; callers must retain it rather than re-resolving a recycled alias.
type Owner struct {
	Kind       OwnerKind `json:"kind"`
	SessionID  string    `json:"session_id,omitempty"`
	BindingRef string    `json:"binding_ref,omitempty"`
}

// Validate checks the shape of an owner identity. It does not authenticate a
// local session or prove that an external binding is authorized.
func (o Owner) Validate() error {
	switch o.Kind {
	case OwnerLocalSession:
		if err := validateOpaque("owner session id", o.SessionID); err != nil {
			return err
		}
		if o.BindingRef != "" {
			return invalidInput("local owner cannot carry binding_ref")
		}
	case OwnerExternalBinding:
		if err := validateOpaque("owner binding reference", o.BindingRef); err != nil {
			return err
		}
		if o.SessionID != "" {
			return invalidInput("external owner cannot carry session_id")
		}
	default:
		return invalidInput("owner kind is unsupported")
	}
	return nil
}

// SubscribeRequest is the local trusted-boundary input. SessionSelector is
// resolved once to an open canonical session; it is never treated as an auth
// credential or accepted as a raw owner override.
type SubscribeRequest struct {
	SchemaVersion   string                     `json:"schema_version"`
	SessionSelector string                     `json:"session_selector"`
	ConversationRef string                     `json:"conversation_ref"`
	Scope           convoy.WorkScope           `json:"scope"`
	Interests       []convoy.LifecycleInterest `json:"interests"`
	RegistrationID  string                     `json:"registration_id"`
	Generation      uint64                     `json:"generation"`
	LeaseExpiresAt  time.Time                  `json:"lease_expires_at"`
	Default         bool                       `json:"default,omitempty"`
	Now             time.Time                  `json:"-"`
}

// Validate checks the pinned local subscribe contract without resolving the
// selector or performing any authorization.
func (r SubscribeRequest) Validate() error {
	if err := validateSchema(r.SchemaVersion); err != nil {
		return err
	}
	if err := validateOpaque("session selector", r.SessionSelector); err != nil {
		return err
	}
	if err := validateTarget(r.ConversationRef, r.Scope, r.Interests); err != nil {
		return err
	}
	if err := validateFence(convoy.RegistrationFence{RegistrationID: r.RegistrationID, Generation: r.Generation}); err != nil {
		return err
	}
	if r.LeaseExpiresAt.IsZero() {
		return invalidInput("lease expiry required")
	}
	return nil
}

// ExternalSubscriptionRequest is the external half of the pinned contract.
// It names an existing binding and never causes this package to create a
// parallel external registry or to assert that the binding is authorized.
type ExternalSubscriptionRequest struct {
	SchemaVersion   string                     `json:"schema_version"`
	Owner           Owner                      `json:"owner"`
	ConversationRef string                     `json:"conversation_ref"`
	Scope           convoy.WorkScope           `json:"scope"`
	Interests       []convoy.LifecycleInterest `json:"interests"`
	RegistrationID  string                     `json:"registration_id"`
	Generation      uint64                     `json:"generation"`
	LeaseExpiresAt  time.Time                  `json:"lease_expires_at"`
	Default         bool                       `json:"default,omitempty"`
}

// Validate checks external contract shape. Existing adapter/controller
// admission must authenticate the binding before an external caller uses it.
func (r ExternalSubscriptionRequest) Validate() error {
	if err := validateSchema(r.SchemaVersion); err != nil {
		return err
	}
	if r.Owner.Kind != OwnerExternalBinding {
		return invalidInput("external subscription requires an external owner")
	}
	if err := r.Owner.Validate(); err != nil {
		return err
	}
	if err := validateTarget(r.ConversationRef, r.Scope, r.Interests); err != nil {
		return err
	}
	if err := validateFence(convoy.RegistrationFence{RegistrationID: r.RegistrationID, Generation: r.Generation}); err != nil {
		return err
	}
	if r.LeaseExpiresAt.IsZero() {
		return invalidInput("lease expiry required")
	}
	return nil
}

// ListRequest asks for durable records owned by one exact canonical session
// ID. Aliases and session selectors are intentionally not accepted here.
type ListRequest struct {
	SchemaVersion  string `json:"schema_version"`
	OwnerSessionID string `json:"owner_session_id"`
}

// RenewRequest advances a registration under its exact owner and current
// fence. The new expiry is required so a renewal cannot silently preserve a
// lease that has already expired.
type RenewRequest struct {
	SchemaVersion  string                   `json:"schema_version"`
	OwnerSessionID string                   `json:"owner_session_id"`
	SubscriptionID string                   `json:"subscription_id"`
	Fence          convoy.RegistrationFence `json:"fence"`
	LeaseExpiresAt time.Time                `json:"lease_expires_at"`
	Now            time.Time                `json:"-"`
}

// RetireRequest removes one registration under its exact owner and current
// fence. Reason is durable audit context, not a routing or authorization key.
type RetireRequest struct {
	SchemaVersion  string                   `json:"schema_version"`
	OwnerSessionID string                   `json:"owner_session_id"`
	SubscriptionID string                   `json:"subscription_id"`
	Fence          convoy.RegistrationFence `json:"fence"`
	Reason         string                   `json:"reason"`
	Now            time.Time                `json:"-"`
}

// TerminalRefusal explains why a late registration was not acknowledged.
type TerminalRefusal struct {
	ConvoyID string `json:"convoy_id"`
	Reason   string `json:"reason"`
}

// Result is the versioned operation result. Acknowledged is true only after
// the durable mutation or read completed; terminal refusal is explicit and is
// never represented as a successful subscription.
type Result struct {
	SchemaVersion  string                      `json:"schema_version"`
	Operation      Operation                   `json:"operation"`
	Status         ResultStatus                `json:"status"`
	Acknowledged   bool                        `json:"acknowledged"`
	Owner          Owner                       `json:"owner"`
	SubscriptionID string                      `json:"subscription_id,omitempty"`
	Fence          convoy.RegistrationFence    `json:"fence,omitempty"`
	LeaseExpiresAt time.Time                   `json:"lease_expires_at,omitempty"`
	Default        bool                        `json:"default,omitempty"`
	Subscriptions  []convoy.SubscriptionRecord `json:"subscriptions,omitempty"`
	Refusal        *TerminalRefusal            `json:"refusal,omitempty"`
}

// TerminalStateLookup is the authoritative convoy terminal-state seam. A nil
// lookup cannot admit a convoy-scoped subscription because late registration
// would otherwise promise an event that may never arrive.
type TerminalStateLookup func(context.Context, string) (bool, error)

// Service composes the operation contract with durable subscriptions and the
// existing session store. It owns no external registry and no transport.
type Service struct {
	subscriptions *convoy.SubscriptionService
	sessions      beads.Store
	terminal      TerminalStateLookup
}

// NewService creates a subscription operation service over the supplied stores.
// subscriptionStore and sessionStore may be the same store when the city uses
// one bead backend, but their roles remain distinct at this boundary.
func NewService(subscriptionStore, sessionStore beads.Store, terminal TerminalStateLookup) *Service {
	return &Service{
		subscriptions: convoy.NewSubscriptionService(subscriptionStore),
		sessions:      sessionStore,
		terminal:      terminal,
	}
}

// ResolveLocalOwner resolves one selector to one open canonical session bead.
// This is trusted-local routing context, not authentication.
func ResolveLocalOwner(ctx context.Context, store beads.Store, selector string) (Owner, error) {
	if err := checkContext(ctx); err != nil {
		return Owner{}, err
	}
	if store == nil {
		return Owner{}, fmt.Errorf("%w: session store unavailable", ErrOwnerRequired)
	}
	if err := validateOpaque("session selector", selector); err != nil {
		return Owner{}, err
	}
	id, err := session.ResolveSessionID(store, selector)
	if err != nil {
		return Owner{}, fmt.Errorf("resolve local subscription owner: %w", err)
	}
	return loadOpenOwner(ctx, store, id)
}

// Subscribe resolves the local selector once and creates a fenced durable
// registration. A terminal convoy is refused explicitly rather than treated
// as a future callback promise.
func (s *Service) Subscribe(ctx context.Context, request SubscribeRequest) (Result, error) {
	if err := checkContext(ctx); err != nil {
		return Result{}, err
	}
	if err := request.Validate(); err != nil {
		return Result{}, err
	}
	if s == nil || s.subscriptions == nil {
		return Result{}, invalidInput("nil subscription service")
	}
	owner, err := ResolveLocalOwner(ctx, s.sessions, request.SessionSelector)
	if err != nil {
		return Result{}, err
	}
	now := operationTime(request.Now)
	if request.Scope.ConvoyID != "" {
		if s.terminal == nil {
			return Result{}, ErrTerminalStateUnavailable
		}
		terminal, lookupErr := s.terminal(ctx, request.Scope.ConvoyID)
		if lookupErr != nil {
			return Result{}, fmt.Errorf("check terminal convoy %q: %w", request.Scope.ConvoyID, lookupErr)
		}
		if terminal {
			return Result{
				SchemaVersion: ContractVersion,
				Operation:     OperationSubscribe,
				Status:        StatusTerminalRefused,
				Owner:         owner,
				Refusal:       &TerminalRefusal{ConvoyID: request.Scope.ConvoyID, Reason: "convoy is already terminal"},
			}, ErrTerminalState
		}
	}
	expiresAt := request.LeaseExpiresAt.UTC()
	record, err := s.subscriptions.Create(ctx, convoy.CreateSubscriptionInput{
		Owner:          owner.SessionID,
		RegistrationID: request.RegistrationID,
		Generation:     request.Generation,
		Scope:          cloneScope(request.Scope),
		RouteIdentity:  request.ConversationRef,
		Interests:      append([]convoy.LifecycleInterest(nil), request.Interests...),
		LeaseExpiresAt: &expiresAt,
		Default:        request.Default,
		Now:            now,
	})
	if err != nil {
		return Result{}, err
	}
	if request.Scope.ConvoyID != "" {
		terminal, lookupErr := s.terminal(ctx, request.Scope.ConvoyID)
		if lookupErr != nil {
			retireErr := s.retireCreatedSubscription(ctx, owner, record, now)
			lookupFailure := fmt.Errorf("re-check terminal convoy %q: %w", request.Scope.ConvoyID, lookupErr)
			if retireErr != nil {
				return Result{}, errors.Join(lookupFailure, fmt.Errorf("retire subscription after terminal-state lookup failure: %w", retireErr))
			}
			return Result{}, lookupFailure
		}
		if terminal {
			if err := s.retireCreatedSubscription(ctx, owner, record, now); err != nil {
				return Result{}, fmt.Errorf("retire subscription for terminal convoy %q: %w", request.Scope.ConvoyID, err)
			}
			return Result{
				SchemaVersion: ContractVersion,
				Operation:     OperationSubscribe,
				Status:        StatusTerminalRefused,
				Owner:         owner,
				Refusal:       &TerminalRefusal{ConvoyID: request.Scope.ConvoyID, Reason: terminalTransitionReason},
			}, ErrTerminalState
		}
	}
	return resultFromRecord(OperationSubscribe, StatusSubscribed, owner, record), nil
}

func (s *Service) retireCreatedSubscription(ctx context.Context, owner Owner, record convoy.SubscriptionRecord, now time.Time) error {
	_, err := s.subscriptions.Unregister(ctx, owner.SessionID, record.ID, convoy.RegistrationFence{
		RegistrationID: record.RegistrationID,
		Generation:     record.Generation,
	}, terminalTransitionReason, now)
	return err
}

// List returns every durable registration, including terminal records, for an
// exact open canonical session owner.
func (s *Service) List(ctx context.Context, request ListRequest) (Result, error) {
	if err := checkContext(ctx); err != nil {
		return Result{}, err
	}
	if err := validateSchema(request.SchemaVersion); err != nil {
		return Result{}, err
	}
	owner, err := s.resolveExactOpenOwner(ctx, request.OwnerSessionID)
	if err != nil {
		return Result{}, err
	}
	records, err := s.subscriptions.List(ctx, owner.SessionID)
	if err != nil {
		return Result{}, err
	}
	return Result{
		SchemaVersion: ContractVersion,
		Operation:     OperationList,
		Status:        StatusListed,
		Acknowledged:  true,
		Owner:         owner,
		Subscriptions: cloneRecords(records),
	}, nil
}

// Renew advances one exact owner's registration generation and lease under
// the caller's current registration fence.
func (s *Service) Renew(ctx context.Context, request RenewRequest) (Result, error) {
	if err := checkContext(ctx); err != nil {
		return Result{}, err
	}
	if err := validateSchema(request.SchemaVersion); err != nil {
		return Result{}, err
	}
	if err := validateExactMutation(request.OwnerSessionID, request.SubscriptionID, request.Fence, request.LeaseExpiresAt, operationTime(request.Now)); err != nil {
		return Result{}, err
	}
	owner, err := s.resolveExactOpenOwner(ctx, request.OwnerSessionID)
	if err != nil {
		return Result{}, err
	}
	record, err := s.subscriptions.RenewLease(ctx, owner.SessionID, request.SubscriptionID, request.Fence, request.LeaseExpiresAt, operationTime(request.Now))
	if err != nil {
		return Result{}, err
	}
	return resultFromRecord(OperationRenew, StatusRenewed, owner, record), nil
}

// Retire unregisters one exact owner's registration under its current fence.
func (s *Service) Retire(ctx context.Context, request RetireRequest) (Result, error) {
	if err := checkContext(ctx); err != nil {
		return Result{}, err
	}
	if err := validateSchema(request.SchemaVersion); err != nil {
		return Result{}, err
	}
	if err := validateOpaque("owner session id", request.OwnerSessionID); err != nil {
		return Result{}, err
	}
	if err := validateOpaque("subscription id", request.SubscriptionID); err != nil {
		return Result{}, err
	}
	if err := validateFence(request.Fence); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(request.Reason) == "" {
		return Result{}, invalidInput("retirement reason required")
	}
	owner, err := s.resolveExactOpenOwner(ctx, request.OwnerSessionID)
	if err != nil {
		return Result{}, err
	}
	record, err := s.subscriptions.Unregister(ctx, owner.SessionID, request.SubscriptionID, request.Fence, request.Reason, operationTime(request.Now))
	if err != nil {
		return Result{}, err
	}
	return resultFromRecord(OperationRetire, StatusRetired, owner, record), nil
}

func (s *Service) resolveExactOpenOwner(ctx context.Context, id string) (Owner, error) {
	if s == nil || s.subscriptions == nil {
		return Owner{}, invalidInput("nil subscription service")
	}
	if err := validateOpaque("owner session id", id); err != nil {
		return Owner{}, err
	}
	return loadOpenOwner(ctx, s.sessions, id)
}

func loadOpenOwner(ctx context.Context, store beads.Store, id string) (Owner, error) {
	if err := checkContext(ctx); err != nil {
		return Owner{}, err
	}
	bead, resolvedID, err := session.ResolveSessionBeadByExactID(store, id)
	if err != nil {
		return Owner{}, fmt.Errorf("load canonical subscription owner: %w", err)
	}
	if bead.Status != "open" {
		return Owner{}, fmt.Errorf("%w: %s has status %q", ErrOwnerNotOpen, resolvedID, bead.Status)
	}
	return Owner{Kind: OwnerLocalSession, SessionID: resolvedID}, nil
}

func resultFromRecord(operation Operation, status ResultStatus, owner Owner, record convoy.SubscriptionRecord) Result {
	result := Result{
		SchemaVersion:  ContractVersion,
		Operation:      operation,
		Status:         status,
		Acknowledged:   true,
		Owner:          owner,
		SubscriptionID: record.ID,
		Fence: convoy.RegistrationFence{
			RegistrationID: record.RegistrationID,
			Generation:     record.Generation,
		},
		Default: record.Default,
	}
	if record.LeaseExpiresAt != nil {
		result.LeaseExpiresAt = record.LeaseExpiresAt.UTC()
	}
	return result
}

func validateExactMutation(ownerID, subscriptionID string, fence convoy.RegistrationFence, expiresAt, now time.Time) error {
	if err := validateOpaque("owner session id", ownerID); err != nil {
		return err
	}
	if err := validateOpaque("subscription id", subscriptionID); err != nil {
		return err
	}
	if err := validateFence(fence); err != nil {
		return err
	}
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return invalidInput("lease expiry must be after now")
	}
	return nil
}

func validateTarget(conversationRef string, scope convoy.WorkScope, interests []convoy.LifecycleInterest) error {
	if err := validateOpaque("conversation reference", conversationRef); err != nil {
		return err
	}
	if strings.TrimSpace(scope.City) == "" && strings.TrimSpace(scope.ConvoyID) == "" && len(scope.WorkRefs) == 0 {
		return invalidInput("authorized work scope required")
	}
	for name, value := range map[string]string{"scope city": scope.City, "scope convoy": scope.ConvoyID} {
		if value != "" {
			if err := validateOpaque(name, value); err != nil {
				return err
			}
		}
	}
	seenRefs := make(map[string]struct{}, len(scope.WorkRefs))
	for _, ref := range scope.WorkRefs {
		if err := validateOpaque("scope work reference", ref); err != nil {
			return err
		}
		if _, ok := seenRefs[ref]; ok {
			return invalidInput("duplicate scope work reference")
		}
		seenRefs[ref] = struct{}{}
	}
	if len(interests) == 0 {
		return invalidInput("lifecycle interest required")
	}
	seenInterests := make(map[convoy.LifecycleInterest]struct{}, len(interests))
	for _, interest := range interests {
		if err := validateOpaque("lifecycle interest", string(interest)); err != nil {
			return err
		}
		if _, ok := seenInterests[interest]; ok {
			return invalidInput("duplicate lifecycle interest")
		}
		seenInterests[interest] = struct{}{}
	}
	return nil
}

func validateFence(fence convoy.RegistrationFence) error {
	if fence.Generation == 0 {
		return convoy.ErrFenceRequired
	}
	return validateOpaque("registration id", fence.RegistrationID)
}

func validateSchema(version string) error {
	if version != ContractVersion {
		return invalidInput(fmt.Sprintf("schema version %q is unsupported", version))
	}
	return nil
}

func validateOpaque(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return invalidInput(field + " required")
	}
	if !utf8.ValidString(value) {
		return invalidInput(field + " is not valid UTF-8")
	}
	if len(value) > maxOpaqueLength {
		return invalidInput(fmt.Sprintf("%s exceeds maximum length %d", field, maxOpaqueLength))
	}
	if strings.Contains(value, ",") {
		return invalidInput(field + " must identify exactly one value")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return invalidInput(field + " contains a control character")
		}
	}
	return nil
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return invalidInput("nil context")
	}
	return ctx.Err()
}

func operationTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func cloneScope(scope convoy.WorkScope) convoy.WorkScope {
	scope.WorkRefs = append([]string(nil), scope.WorkRefs...)
	return scope
}

func cloneRecords(records []convoy.SubscriptionRecord) []convoy.SubscriptionRecord {
	out := make([]convoy.SubscriptionRecord, len(records))
	copy(out, records)
	for i := range out {
		out[i].Scope = cloneScope(out[i].Scope)
		out[i].Interests = append([]convoy.LifecycleInterest(nil), out[i].Interests...)
		if out[i].LeaseExpiresAt != nil {
			value := out[i].LeaseExpiresAt.UTC()
			out[i].LeaseExpiresAt = &value
		}
	}
	return out
}

func invalidInput(message string) error {
	return fmt.Errorf("%w: %s", convoy.ErrInvalidInput, message)
}
