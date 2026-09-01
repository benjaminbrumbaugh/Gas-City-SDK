// Package externalcoordination provides provider-neutral external coordination.
//
// The package deliberately knows nothing about Gas Town roles or Hermes. A
// configured orchestrator can enqueue a durable request for an external
// coordinator, and an adapter can deliver it to the selected coordinator.
package externalcoordination

import (
	"context"
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
)

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
	// DeliveryIndeterminate means the callback may have accepted this exact
	// attempt but its receipt was lost or unreadable. A retry must preserve the
	// same attempt and idempotency identity so a late response remains valid.
	DeliveryIndeterminate bool `json:"delivery_indeterminate,omitempty"`

	revision               int64
	responseCommitment     string
	responseCommitmentSalt string
	responseScrubPending   bool
}

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
}

// Response records a result returned by the external coordinator.
type Response struct {
	RequestID        string           `json:"request_id"`
	Attempt          int              `json:"attempt" minimum:"1"`
	CorrelationID    string           `json:"correlation_id" minLength:"1"`
	ResponseID       string           `json:"response_id"`
	State            string           `json:"state"`
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
	MarkDeliveryIndeterminate(context.Context, string, error, time.Time) error
	Fail(context.Context, string, error, time.Time) error
	Cancel(context.Context, string, time.Time) error
}

// Service is the concrete durable request queue.
type Service struct {
	store beads.Store
}

// NewService creates a durable external coordination request service over the city's bead store.
func NewService(store beads.Store) *Service { return &Service{store: store} }
