package convoycallback

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validEvent(kind EventType) Event {
	event := Event{
		SchemaVersion: ContractVersion,
		EventID:       "evt-123",
		Type:          kind,
		ConvoyID:      "convoy-123",
		LaunchOrigin:  "opaque-launch-origin",
		CorrelationID: "corr-123",
		OccurredAt:    time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC),
	}
	if kind == EventTaskAccepted {
		event.TaskID = "task-123"
	}
	if kind == EventDeploymentCompleted {
		event.DeploymentID = "deployment-123"
	}
	return event
}

func TestContractVersionAndLifecycleVocabulary(t *testing.T) {
	if ContractVersion != "convoy-callback.v1" {
		t.Fatalf("ContractVersion = %q, want convoy-callback.v1", ContractVersion)
	}
	want := []EventType{
		EventReadyForRebuild,
		EventConvoyCreated,
		EventTaskAccepted,
		EventConvoyClosed,
		EventDeploymentCompleted,
	}
	for _, kind := range want {
		if err := validEvent(kind).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v", kind, err)
		}
	}
}

func TestEventJSONUsesVersionedNeutralWireFields(t *testing.T) {
	event := validEvent(EventConvoyCreated)
	event.RouteIdentity = map[string]string{"opaque": "route-7"}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(encoded)
	for _, field := range []string{
		`"schema_version":"convoy-callback.v1"`,
		`"event_id":"evt-123"`,
		`"type":"convoy.created"`,
		`"convoy_id":"convoy-123"`,
		`"launch_origin":"opaque-launch-origin"`,
		`"correlation_id":"corr-123"`,
		`"route_identity":{"opaque":"route-7"}`,
	} {
		if !strings.Contains(wire, field) {
			t.Errorf("wire = %s, missing %s", wire, field)
		}
	}
	for _, forbidden := range []string{"callback_url", "authorization", "access_token", "provider", "runtime"} {
		if strings.Contains(wire, forbidden) {
			t.Errorf("wire = %s, contains forbidden transport/private field %q", wire, forbidden)
		}
	}
	var roundTrip Event
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := roundTrip.Validate(); err != nil {
		t.Fatalf("round-trip Validate() = %v", err)
	}
}

func TestEventValidationRequiresCommonIdentity(t *testing.T) {
	base := validEvent(EventConvoyCreated)
	cases := []struct {
		name   string
		mutate func(*Event)
		want   string
	}{
		{"schema version", func(e *Event) { e.SchemaVersion = "" }, "schema_version"},
		{"event id", func(e *Event) { e.EventID = "" }, "event_id"},
		{"convoy id", func(e *Event) { e.ConvoyID = "" }, "convoy_id"},
		{"launch origin", func(e *Event) { e.LaunchOrigin = "" }, "launch_origin"},
		{"correlation id", func(e *Event) { e.CorrelationID = "" }, "correlation_id"},
		{"occurred at", func(e *Event) { e.OccurredAt = time.Time{} }, "occurred_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := base
			tc.mutate(&event)
			if err := event.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error naming %s", err, tc.want)
			}
		})
	}
}

func TestEventValidationSeparatesLifecyclePhaseRequirements(t *testing.T) {
	cases := []struct {
		name   string
		kind   EventType
		mutate func(*Event)
		want   string
	}{
		{"task accepted requires task", EventTaskAccepted, func(e *Event) { e.TaskID = "" }, "task_id"},
		{"deployment completed requires deployment", EventDeploymentCompleted, func(e *Event) { e.DeploymentID = "" }, "deployment_id"},
		{"convoy created does not require task", EventConvoyCreated, func(e *Event) { e.TaskID = "" }, ""},
		{"convoy closed does not imply deployment", EventConvoyClosed, func(e *Event) { e.DeploymentID = "" }, ""},
		{"ready for rebuild does not imply closure", EventReadyForRebuild, func(*Event) {}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := validEvent(tc.kind)
			tc.mutate(&event)
			err := event.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error naming %s", err, tc.want)
			}
		})
	}
}

func TestEventValidationBoundsOpaqueIdentitiesWithoutParsingThem(t *testing.T) {
	valid := validEvent(EventConvoyCreated)
	valid.LaunchOrigin = "opaque://route/reference with spaces"
	if err := valid.Validate(); err != nil {
		t.Fatalf("opaque launch origin was parsed/rejected: %v", err)
	}

	tooLong := validEvent(EventConvoyCreated)
	tooLong.LaunchOrigin = strings.Repeat("x", MaxOpaqueIdentityLength+1)
	if err := tooLong.Validate(); err == nil || !strings.Contains(err.Error(), "launch_origin") {
		t.Fatalf("Validate() = %v, want bounded launch_origin error", err)
	}

	control := validEvent(EventConvoyCreated)
	control.LaunchOrigin = "opaque\norigin"
	if err := control.Validate(); err == nil || !strings.Contains(err.Error(), "launch_origin") {
		t.Fatalf("Validate() = %v, want control-character launch_origin error", err)
	}

	routeControl := validEvent(EventConvoyCreated)
	routeControl.RouteIdentity = map[string]string{"opaque": "route\tvalue"}
	if err := routeControl.Validate(); err == nil || !strings.Contains(err.Error(), "route_identity") {
		t.Fatalf("Validate() = %v, want control-character route_identity error", err)
	}

	tooManyRoutes := validEvent(EventConvoyCreated)
	tooManyRoutes.RouteIdentity = make(map[string]string, maxRouteIdentityEntries+1)
	for index := 0; index <= maxRouteIdentityEntries; index++ {
		tooManyRoutes.RouteIdentity["key-"+string(rune('a'+index))] = "value"
	}
	if err := tooManyRoutes.Validate(); err == nil || !strings.Contains(err.Error(), "route_identity") {
		t.Fatalf("Validate() = %v, want bounded route_identity error", err)
	}
}

func TestEventValidationRejectsUnknownVersionAndType(t *testing.T) {
	unknownVersion := validEvent(EventConvoyCreated)
	unknownVersion.SchemaVersion = "convoy-callback.v2"
	if err := unknownVersion.Validate(); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("Validate() = %v, want unknown version error", err)
	}

	unknownType := validEvent(EventConvoyCreated)
	unknownType.Type = EventType("convoy.rebuild_started")
	if err := unknownType.Validate(); err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("Validate() = %v, want unknown type error", err)
	}
}

func TestEventJSONDecodeIgnoresUnknownOptionalFields(t *testing.T) {
	encoded := []byte(`{"schema_version":"convoy-callback.v1","event_id":"evt-123","type":"convoy.created","convoy_id":"convoy-123","launch_origin":"opaque","correlation_id":"corr-123","occurred_at":"2026-09-08T17:00:00Z","future_optional":"ignored"}`)
	var event Event
	if err := json.Unmarshal(encoded, &event); err != nil {
		t.Fatal(err)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for an unknown optional field", err)
	}
}

func TestDecodeLegacyConvoyEventIsExplicitAndReadOnlyCompatible(t *testing.T) {
	legacy, err := DecodeLegacyEvent(LegacyEvent{
		Type:       "convoy.created",
		Subject:    "convoy-legacy",
		OccurredAt: time.Date(2026, 9, 8, 16, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !legacy.Legacy || legacy.Type != EventConvoyCreated || legacy.ConvoyID != "convoy-legacy" {
		t.Fatalf("legacy result = %+v, want explicit legacy convoy record", legacy)
	}
	if legacy.SchemaVersion != LegacyVersion {
		t.Fatalf("legacy schema version = %q, want %q", legacy.SchemaVersion, LegacyVersion)
	}

	if _, err := DecodeLegacyEvent(LegacyEvent{Type: "task.accepted", Subject: "task-1", OccurredAt: time.Now()}); err == nil {
		t.Fatal("DecodeLegacyEvent accepted a non-legacy convoy event")
	}
	if _, err := DecodeLegacyEvent(LegacyEvent{Type: "convoy.closed", OccurredAt: time.Now()}); err == nil {
		t.Fatal("DecodeLegacyEvent accepted a legacy event without a subject")
	}
}
