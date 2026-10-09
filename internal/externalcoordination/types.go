// Package externalcoordination provides provider-neutral external coordination.
//
// The package deliberately knows nothing about Gas Town roles or Hermes. A
// configured orchestrator can enqueue a durable request for an external
// coordinator, and an adapter can deliver it to the selected coordinator.
package externalcoordination

import (
	"context"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

const requestLabel = "gc:external-coordination-request"

// DeliveryMode controls how the coordinator receives a request.
type DeliveryMode string

const (
	// DeliveryQueued is the safe default. The adapter delivers at a session
	// boundary and must not interrupt an active coordinator turn.
	DeliveryQueued DeliveryMode = "queued"
	// DeliveryInterrupt is opt-in and should only be enabled by explicit policy.
	DeliveryInterrupt DeliveryMode = "interrupt"
)

// Reason identifies why an orchestrator is contacting the coordinator.
type Reason string

const (
	// ReasonOutsideHelp asks the coordinator for external assistance.
	ReasonOutsideHelp Reason = "outside_help"
	// ReasonEscalation asks the coordinator to handle an escalation.
	ReasonEscalation Reason = "escalation"
	// ReasonDirectRequest returns an answer to a direct request.
	ReasonDirectRequest Reason = "direct_request"
	// ReasonLargeSummary delivers a large decision-relevant summary.
	ReasonLargeSummary Reason = "large_summary"
	// ReasonAuthorization asks for an authorization decision.
	ReasonAuthorization Reason = "authorization"
	// ReasonAmbiguity asks the coordinator to resolve ambiguity.
	ReasonAmbiguity Reason = "ambiguity"
)

// SessionMode describes how an adapter should address its coordinator session.
type SessionMode string

const (
	// SessionNew asks the adapter to create a new coordinator session.
	SessionNew SessionMode = "new"
	// SessionResume asks the adapter to resume an existing session.
	SessionResume SessionMode = "resume"
	// SessionSubmit submits to an already selected session.
	SessionSubmit SessionMode = "submit"
	// SessionResumeOrCreate resumes a session or creates one if absent.
	SessionResumeOrCreate SessionMode = "resume_or_create"
)

// ContentRetention controls whether callback content remains in the durable
// request record after delivery.
type ContentRetention string

const (
	// RetentionDurable keeps content for retry, audit, or later correlation.
	RetentionDurable ContentRetention = "durable"
	// RetentionEphemeral scrubs content after transport acceptance or response.
	RetentionEphemeral ContentRetention = "ephemeral"
)

// DeliveryState is the durable lifecycle of one coordinator request.
type DeliveryState string

const (
	// StateAccepted records adapter acceptance.
	StateAccepted DeliveryState = "accepted"
	// StateQueued records a durable queued request.
	StateQueued DeliveryState = "queued"
	// StateRunning records a delivered request awaiting an external coordination outcome.
	StateRunning DeliveryState = "running"
	// StateCompleted records a correlated external coordination response.
	StateCompleted DeliveryState = "completed"
	// StateFailed records a failed delivery.
	StateFailed DeliveryState = "failed"
	// StateExpired records an expired request.
	StateExpired DeliveryState = "expired"
	// StateCancelled records an operator cancellation.
	StateCancelled DeliveryState = "cancelled" //nolint:misspell // public wire state spelling
	// StateUncertain records a delivery whose remote acceptance cannot be
	// ruled out. It is not claimable until the original request identity is
	// reconciled by an owner that understands the provider.
	StateUncertain DeliveryState = "uncertain"
)

// ResponseOutcome is the coordinator's explicit result. It is separate from
// DeliveryState: a refused or failed answer is still a response that was
// causally recorded, while a transport refusal is not a coordinator outcome.
type ResponseOutcome string

const (
	// ResponseOutcomeAnswered records an answer or decision.
	ResponseOutcomeAnswered ResponseOutcome = "answered"
	// ResponseOutcomeRefused records an explicit coordinator refusal.
	ResponseOutcomeRefused ResponseOutcome = "refused"
	// ResponseOutcomeFailed records that the coordinator could not produce an answer.
	ResponseOutcomeFailed ResponseOutcome = "failed"
	// ResponseOutcomeExpired records that no answer was produced before expiry.
	ResponseOutcomeExpired ResponseOutcome = "expired"
)

func (o ResponseOutcome) valid() bool {
	switch o {
	case ResponseOutcomeAnswered, ResponseOutcomeRefused, ResponseOutcomeFailed, ResponseOutcomeExpired:
		return true
	default:
		return false
	}
}

// RecoveryAction describes the durable result of recovering one abandoned
// running claim.
type RecoveryAction string

const (
	// RecoveryActionMarkedUncertain preserves a possible remote acceptance.
	RecoveryActionMarkedUncertain RecoveryAction = "marked_uncertain"
	// RecoveryActionExpired records that the request deadline passed before recovery.
	RecoveryActionExpired RecoveryAction = "expired"
)

// RecoveryResult reports a recovery transition after its durable write wins.
type RecoveryResult struct {
	Record RequestRecord
	Action RecoveryAction
}

// Capability describes an adapter's supported coordinator operations.
type Capability struct {
	CanCreateSession bool `json:"can_create_session"`
	CanResumeSession bool `json:"can_resume_session"`
	CanSubmitPrompt  bool `json:"can_submit_prompt"`
	CanInterrupt     bool `json:"can_interrupt"`
	CanReceiveEvents bool `json:"can_receive_events"`
	CanReturnResults bool `json:"can_return_results"`
}

// Target is the logical coordinator binding selected by configuration. The
// adapter and provider/account identity are deliberately separate so switching
// coordinators does not require changing caller code.
type Target struct {
	LogicalRole      string       `json:"logical_role"`
	TargetID         string       `json:"target_id"`
	Adapter          string       `json:"adapter"`
	Provider         string       `json:"provider"`
	AccountID        string       `json:"account_id"`
	ConversationID   string       `json:"conversation_id"`
	SessionMode      SessionMode  `json:"session_mode"`
	DeliveryMode     DeliveryMode `json:"delivery_mode"`
	InterruptAllowed bool         `json:"interrupt_allowed"`
	ConfigRevision   int64        `json:"config_revision"`
}

// Request is the authenticated, causally-linked envelope delivered for external coordination.
type Request struct {
	RequestID         string            `json:"request_id"`
	Attempt           int               `json:"attempt"`
	SourceAgent       string            `json:"source_agent"`
	Target            Target            `json:"target"`
	City              string            `json:"city,omitempty"`
	WorkRef           string            `json:"work_ref,omitempty"`
	Repository        string            `json:"repository,omitempty"`
	Rig               string            `json:"rig,omitempty"`
	Reason            Reason            `json:"reason"`
	DeliveryMode      DeliveryMode      `json:"delivery_mode"`
	SessionMode       SessionMode       `json:"session_mode"`
	Prompt            string            `json:"prompt"`
	ContentRetention  ContentRetention  `json:"content_retention"`
	AllowedTools      []string          `json:"allowed_tools,omitempty"`
	CorrelationID     string            `json:"correlation_id"`
	IdempotencyKey    string            `json:"idempotency_key"`
	ExpiresAt         time.Time         `json:"expires_at"`
	ResultDestination string            `json:"result_destination,omitempty"`
	RouteIdentity     map[string]string `json:"route_identity,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
}

// RequestInput is the caller-facing enqueue input. RequestID and CreatedAt are
// assigned by the service when omitted.
type RequestInput struct {
	SourceAgent       string
	Target            Target
	City              string
	WorkRef           string
	Repository        string
	Rig               string
	Reason            Reason
	DeliveryMode      DeliveryMode
	SessionMode       SessionMode
	Prompt            string
	ContentRetention  ContentRetention
	AllowedTools      []string
	CorrelationID     string
	IdempotencyKey    string
	ExpiresAt         time.Time
	ResultDestination string
	RouteIdentity     map[string]string
	Now               time.Time
}

// RequestRecord is the durable request plus delivery metadata.
type RequestRecord struct {
	ID          string        `json:"id"`
	Request     Request       `json:"request"`
	State       DeliveryState `json:"state"`
	Attempt     int           `json:"attempt"`
	ClaimedBy   string        `json:"claimed_by,omitempty"`
	ClaimedAt   time.Time     `json:"claimed_at,omitempty"`
	DeliveredAt time.Time     `json:"delivered_at,omitempty"`
	Error       string        `json:"error,omitempty"`

	revision             int64
	responseCommitment   string
	responseScrubPending bool
	outcome              ResponseOutcome
	claimExpiresAt       time.Time
	retryAt              time.Time
	uncertaintyClass     string
}

// Outcome returns the explicit coordinator outcome recorded for this request.
// An empty value means no response outcome has been recorded.
func (r RequestRecord) Outcome() ResponseOutcome { return r.outcome }

// ClaimExpiresAt returns the durable deadline for the current worker claim.
func (r RequestRecord) ClaimExpiresAt() time.Time { return r.claimExpiresAt }

// RetryAt returns the earliest instant at which a queued retry may be claimed.
func (r RequestRecord) RetryAt() time.Time { return r.retryAt }

// UncertaintyClass returns the sanitized reason a request became uncertain.
func (r RequestRecord) UncertaintyClass() string { return r.uncertaintyClass }

// DeliveryReceipt reports adapter acceptance, not external coordinator execution completion.
type DeliveryReceipt struct {
	RequestID       string        `json:"request_id"`
	Attempt         int           `json:"attempt" minimum:"1"`
	CorrelationID   string        `json:"correlation_id" minLength:"1"`
	State           DeliveryState `json:"state"`
	Accepted        bool          `json:"accepted"`
	TargetSessionID string        `json:"target_session_id,omitempty"`
	ResponseID      string        `json:"response_id,omitempty"`
	RetryAfter      time.Duration `json:"retry_after,omitempty"`
	Error           string        `json:"error,omitempty"`

	retryClass string
}

// Response records a result returned by the external coordinator.
type Response struct {
	RequestID        string           `json:"request_id"`
	Attempt          int              `json:"attempt" minimum:"1"`
	CorrelationID    string           `json:"correlation_id" minLength:"1"`
	ResponseID       string           `json:"response_id"`
	State            ResponseOutcome  `json:"state"`
	Summary          string           `json:"summary,omitempty"`
	ContentRetention ContentRetention `json:"content_retention,omitempty"`
	FollowUpRequired bool             `json:"follow_up_required"`
	ReceivedAt       time.Time        `json:"received_at"`
}

// Adapter is the provider-specific delivery boundary. Implementations must
// preserve RequestID and IdempotencyKey and must not claim completion merely
// because the transport accepted a request.
type Adapter interface {
	Name() string
	Capabilities() Capability
	Deliver(context.Context, Request) (DeliveryReceipt, error)
}

// Store exposes durable request lifecycle operations.
type Store interface {
	Enqueue(context.Context, RequestInput) (RequestRecord, error)
	Get(context.Context, string) (RequestRecord, error)
	List(context.Context, ...DeliveryState) ([]RequestRecord, error)
	Claim(context.Context, string, string, time.Time) (RequestRecord, error)
	Complete(context.Context, string, DeliveryReceipt, time.Time) error
	RecordResponse(context.Context, Response) error
	Fail(context.Context, string, error, time.Time) error
	Cancel(context.Context, string, time.Time) error
}

// Service is the concrete durable request queue.
type Service struct {
	store         beads.Store
	claimMu       sync.Mutex
	now           func() time.Time
	claimLease    time.Duration
	maxAttempts   int
	retryBase     time.Duration
	retryMax      time.Duration
	recoveryBatch int
}

// ServiceOptions controls the bounded recovery policy of a Service. Now is an
// injectable clock seam for deterministic expiry and lease tests; production
// callers should leave it nil.
type ServiceOptions struct {
	Now           func() time.Time
	ClaimLease    time.Duration
	MaxAttempts   int
	RetryBase     time.Duration
	RetryMax      time.Duration
	RecoveryBatch int
}

// NewService creates a durable external coordination request service over the
// city's bead store using the production recovery policy.
func NewService(store beads.Store) *Service {
	return NewServiceWithOptions(store, ServiceOptions{})
}

// NewServiceWithOptions creates a service with an explicit clock and bounded
// claim/retry policy. Zero-valued options use safe production defaults.
func NewServiceWithOptions(store beads.Store, options ServiceOptions) *Service {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.ClaimLease <= 0 {
		options.ClaimLease = defaultClaimLease
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = defaultMaxAttempts
	}
	if options.RetryBase <= 0 {
		options.RetryBase = defaultRetryBase
	}
	if options.RetryMax <= 0 {
		options.RetryMax = defaultRetryMax
	}
	if options.RetryMax < options.RetryBase {
		options.RetryMax = options.RetryBase
	}
	if options.RecoveryBatch <= 0 {
		options.RecoveryBatch = defaultRecoveryBatch
	}
	return &Service{
		store:         store,
		now:           options.Now,
		claimLease:    options.ClaimLease,
		maxAttempts:   options.MaxAttempts,
		retryBase:     options.RetryBase,
		retryMax:      options.RetryMax,
		recoveryBatch: options.RecoveryBatch,
	}
}

// NewServiceWithClock creates a service using an injected clock and default
// recovery limits. It is convenient for callers that only need deterministic
// time in tests.
func NewServiceWithClock(store beads.Store, now func() time.Time) *Service {
	return NewServiceWithOptions(store, ServiceOptions{Now: now})
}

func (s *Service) nowTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}
