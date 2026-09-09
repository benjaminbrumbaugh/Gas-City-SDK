// Package convoycallback defines the versioned, harness-neutral wire contract
// for lifecycle notifications about a launched convoy.
//
// This package owns schema shape and validation only. It does not capture
// launch origin, authorize recipients, deliver callbacks, discover a runtime,
// or perform a rebuild/deployment. Origin and route identity are opaque data;
// callers must not infer a provider, role, URL, credential, or runtime from
// them.
package convoycallback

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// ContractVersion identifies the current callback lifecycle wire contract.
	ContractVersion = "convoy-callback.v1"
	// LegacyVersion identifies an unversioned convoy.created/convoy.closed
	// event adapted for read-only compatibility. Legacy records are not valid
	// callback records because they have no launch origin or event identity.
	LegacyVersion = "legacy.convoy.v0"
	// MaxOpaqueIdentityLength bounds durable opaque identifiers without
	// assigning meaning to their contents.
	MaxOpaqueIdentityLength = 256
	maxRouteIdentityEntries = 32
)

// EventType identifies the lifecycle transition represented by an event.
type EventType string

const (
	// EventReadyForRebuild marks work as ready for a rebuild action. It does
	// not imply that a convoy was created, accepted, closed, or deployed.
	EventReadyForRebuild EventType = "convoy.ready_for_rebuild"
	// EventConvoyCreated marks creation of the convoy container.
	EventConvoyCreated EventType = "convoy.created"
	// EventTaskAccepted marks acceptance of one task in the convoy.
	EventTaskAccepted EventType = "task.accepted"
	// EventConvoyClosed marks closure of the convoy container.
	EventConvoyClosed EventType = "convoy.closed"
	// EventDeploymentCompleted marks completion of a deployment associated
	// with the lifecycle, which is distinct from convoy closure.
	EventDeploymentCompleted EventType = "deployment.completed"
)

// Event is a versioned callback lifecycle record. LaunchOrigin identifies the
// launching actor/context as opaque data. RouteIdentity is optional opaque
// route data supplied to an authorized downstream consumer; neither field is
// interpreted by this package.
type Event struct {
	SchemaVersion string            `json:"schema_version"`
	EventID       string            `json:"event_id"`
	Type          EventType         `json:"type"`
	ConvoyID      string            `json:"convoy_id"`
	TaskID        string            `json:"task_id,omitempty"`
	DeploymentID  string            `json:"deployment_id,omitempty"`
	LaunchOrigin  string            `json:"launch_origin"`
	CorrelationID string            `json:"correlation_id"`
	OccurredAt    time.Time         `json:"occurred_at"`
	RouteIdentity map[string]string `json:"route_identity,omitempty"`
}

// LegacyEvent is the minimal shape of an existing unversioned convoy event.
// It is accepted only by DecodeLegacyEvent and never treated as a v1 callback
// record.
type LegacyEvent struct {
	Type       string
	Subject    string
	OccurredAt time.Time
}

// LegacyRecord is a read-only compatibility projection of a legacy convoy
// event. It intentionally lacks callback identity and launch-origin fields.
type LegacyRecord struct {
	SchemaVersion string    `json:"schema_version"`
	Type          EventType `json:"type"`
	ConvoyID      string    `json:"convoy_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	Legacy        bool      `json:"legacy"`
}

// Validate checks that e is a complete current-version callback record.
// Validation proves only the contract boundary; it cannot prove that opaque
// identities are truthful or that the caller is authorized to use them.
func (e Event) Validate() error {
	if e.SchemaVersion != ContractVersion {
		return fmt.Errorf("schema_version %q is unsupported; want %q", e.SchemaVersion, ContractVersion)
	}
	if err := validateOpaque("event_id", e.EventID, true); err != nil {
		return err
	}
	if !knownEventType(e.Type) {
		return fmt.Errorf("type %q is unsupported", e.Type)
	}
	if err := validateOpaque("convoy_id", e.ConvoyID, true); err != nil {
		return err
	}
	if err := validateOpaque("launch_origin", e.LaunchOrigin, true); err != nil {
		return err
	}
	if err := validateOpaque("correlation_id", e.CorrelationID, true); err != nil {
		return err
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	if err := validateOpaque("task_id", e.TaskID, e.Type == EventTaskAccepted); err != nil {
		return err
	}
	if err := validateOpaque("deployment_id", e.DeploymentID, e.Type == EventDeploymentCompleted); err != nil {
		return err
	}
	if len(e.RouteIdentity) > maxRouteIdentityEntries {
		return fmt.Errorf("route_identity has %d entries; maximum is %d", len(e.RouteIdentity), maxRouteIdentityEntries)
	}
	for key, value := range e.RouteIdentity {
		if err := validateOpaque("route_identity key", key, true); err != nil {
			return err
		}
		if err := validateOpaque("route_identity value", value, true); err != nil {
			return err
		}
	}
	return nil
}

// DecodeLegacyEvent adapts the two pre-contract convoy lifecycle event types
// for read-only consumers. It rejects task/deployment events and incomplete
// legacy envelopes instead of silently upgrading them into callback records.
func DecodeLegacyEvent(input LegacyEvent) (LegacyRecord, error) {
	if input.Type != string(EventConvoyCreated) && input.Type != string(EventConvoyClosed) {
		return LegacyRecord{}, fmt.Errorf("legacy event type %q is unsupported", input.Type)
	}
	if err := validateOpaque("subject", input.Subject, true); err != nil {
		return LegacyRecord{}, err
	}
	if input.OccurredAt.IsZero() {
		return LegacyRecord{}, fmt.Errorf("occurred_at is required")
	}
	return LegacyRecord{
		SchemaVersion: LegacyVersion,
		Type:          EventType(input.Type),
		ConvoyID:      input.Subject,
		OccurredAt:    input.OccurredAt,
		Legacy:        true,
	}, nil
}

func knownEventType(kind EventType) bool {
	switch kind {
	case EventReadyForRebuild, EventConvoyCreated, EventTaskAccepted, EventConvoyClosed, EventDeploymentCompleted:
		return true
	default:
		return false
	}
}

func validateOpaque(field, value string, required bool) error {
	if strings.TrimSpace(value) == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", field)
	}
	if len(value) > MaxOpaqueIdentityLength {
		return fmt.Errorf("%s exceeds maximum length %d", field, MaxOpaqueIdentityLength)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}
