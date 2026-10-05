// Package convoydelivery composes convoy lifecycle events with the durable
// callback admission and external-coordination delivery boundaries.
//
// The package owns no transport and no readiness judgment. It turns an
// authoritative convoy creation or closure into a versioned callback event,
// admits each authorized logical recipient, and queues one provider-neutral
// external-coordination request per admitted recipient. The external
// coordination service records queue, delivery, and response outcomes; this
// package never treats queue admission as recipient observation.
package convoydelivery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
	"github.com/gastownhall/gascity/internal/convoyfanout"
	"github.com/gastownhall/gascity/internal/externalcoordination"
)

// Result is the durable result of one lifecycle callback attempt. Admission
// contains the authorization snapshot and queued delivery records; Requests
// contains the corresponding provider-neutral delivery records. A request in
// StateQueued proves only durable queue admission, not recipient receipt.
type Result struct {
	Event     convoycallback.Event
	Admission convoyfanout.Admission
	Requests  []externalcoordination.RequestRecord
	Skipped   bool
}

// Service owns the composition boundary for convoy lifecycle callbacks.
type Service struct {
	store beads.Store
	city  string
	coord config.ExternalCoordinationConfig
	now   func() time.Time
}

// NewService creates a lifecycle callback delivery service. The configuration
// is copied so a caller changing its live config snapshot cannot mutate an
// in-flight delivery decision.
func NewService(store beads.Store, city string, cfg *config.ExternalCoordinationConfig) *Service {
	service := &Service{store: store, city: strings.TrimSpace(city), now: func() time.Time { return time.Now().UTC() }}
	if cfg != nil {
		service.coord = *cfg
		service.coord.Triggers = append([]string(nil), cfg.Triggers...)
	}
	return service
}

// WithClock replaces the service clock for deterministic tests. A nil clock
// restores the production UTC clock.
func (s *Service) WithClock(now func() time.Time) *Service {
	if s == nil {
		return s
	}
	if now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	} else {
		s.now = now
	}
	return s
}

// Deliver admits and queues one supported convoy lifecycle callback.
//
// Missing launch origin preserves legacy behavior and returns Skipped without
// writing a callback record. Disabled or incomplete external coordination
// configuration also leaves the lifecycle operation unchanged. A configured
// callback failure is returned to the caller so production call sites can
// record a warning while preserving the already-committed convoy transition.
// Repeating Deliver for the same convoy transition reuses the deterministic
// event and recipient identities; both admission and external coordination
// therefore return the original durable records after restart or replay.
func (s *Service) Deliver(ctx context.Context, input beads.Bead, kind convoycallback.EventType) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("convoy callback delivery: nil context")
	}
	if s == nil || s.store == nil {
		return Result{}, fmt.Errorf("convoy callback delivery: nil store")
	}
	if kind != convoycallback.EventConvoyCreated && kind != convoycallback.EventConvoyClosed {
		return Result{}, fmt.Errorf("convoy callback delivery: unsupported lifecycle event %q", kind)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	convoyBead, err := s.store.Get(input.ID)
	if err != nil {
		return Result{}, fmt.Errorf("load convoy %s for callback delivery: %w", input.ID, err)
	}
	if convoyBead.Type != "convoy" {
		return Result{}, fmt.Errorf("convoy callback delivery: bead %s is not a convoy", convoyBead.ID)
	}
	origin := convoy.GetConvoyFields(convoyBead).LaunchOrigin
	if origin == "" {
		return Result{Skipped: true}, nil
	}
	if !s.coord.Enabled {
		return Result{Skipped: true}, nil
	}
	if err := s.coord.Validate(); err != nil {
		return Result{}, err
	}

	now := s.currentTime()
	event := convoycallback.Event{
		SchemaVersion: convoycallback.ContractVersion,
		EventID:       lifecycleEventID(convoyBead.ID, kind),
		Type:          kind,
		ConvoyID:      convoyBead.ID,
		LaunchOrigin:  origin,
		CorrelationID: convoyBead.ID,
		OccurredAt:    lifecycleTime(convoyBead, kind, now),
	}
	if err := event.Validate(); err != nil {
		return Result{}, fmt.Errorf("validate convoy callback event: %w", err)
	}

	records, err := convoy.NewSubscriptionService(s.store).ListAll(ctx)
	if err != nil {
		return Result{Event: event}, err
	}
	target := coordinationTarget(s.coord)
	admission, err := convoyfanout.NewAdmissionService(s.store).Admit(ctx, convoyfanout.AdmitInput{
		City:   s.city,
		Event:  event,
		Intent: convoyfanout.IntentNotification,
		LaunchOrigin: convoyfanout.LaunchOriginAuthorization{
			// launchorigin.Normalize guarantees that a captured non-empty
			// origin satisfies the callback identity contract. The lifecycle
			// producer is the authority making this explicit trust decision;
			// fan-out never infers it from route data.
			Trusted:       true,
			Principal:     origin,
			Scope:         convoyfanout.WorkScope{City: s.city, ConvoyID: convoyBead.ID},
			Interests:     []convoycallback.EventType{kind},
			RouteIdentity: origin,
		},
		Subscriptions: convoyfanout.SubscriptionViews(records),
		Target:        convoyfanout.TargetFence{TargetID: target.TargetID, ConfigRevision: target.ConfigRevision},
		DefaultPolicy: convoyfanout.DefaultWhenNoExplicitRecipient,
		Now:           now,
	})
	result := Result{Event: event, Admission: admission}
	if err != nil {
		return result, err
	}
	prompt, err := json.Marshal(event)
	if err != nil {
		return result, fmt.Errorf("encode convoy callback event: %w", err)
	}

	coordination := externalcoordination.NewService(s.store)
	result.Requests = make([]externalcoordination.RequestRecord, 0, len(admission.Deliveries))
	for _, delivery := range admission.Deliveries {
		request, err := coordination.Enqueue(ctx, externalcoordination.RequestInput{
			SourceAgent:      origin,
			Target:           target,
			City:             s.city,
			WorkRef:          event.ConvoyID,
			Reason:           externalcoordination.ReasonDirectRequest,
			DeliveryMode:     target.DeliveryMode,
			SessionMode:      target.SessionMode,
			Prompt:           string(prompt),
			ContentRetention: externalcoordination.RetentionDurable,
			CorrelationID:    event.CorrelationID,
			IdempotencyKey:   delivery.IdempotencyKey,
			RouteIdentity:    routeIdentity(delivery.Recipient.RouteIdentity),
			Now:              now,
		})
		if err != nil {
			return result, fmt.Errorf("queue callback for recipient %s: %w", delivery.Recipient.LogicalID, err)
		}
		result.Requests = append(result.Requests, request)
	}
	return result, nil
}

func (s *Service) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func lifecycleEventID(convoyID string, kind convoycallback.EventType) string {
	return convoyID + ":" + string(kind)
}

func lifecycleTime(b beads.Bead, kind convoycallback.EventType, fallback time.Time) time.Time {
	if kind == convoycallback.EventConvoyCreated && !b.CreatedAt.IsZero() {
		return b.CreatedAt.UTC()
	}
	if kind == convoycallback.EventConvoyClosed && !b.UpdatedAt.IsZero() {
		return b.UpdatedAt.UTC()
	}
	return fallback.UTC()
}

func coordinationTarget(cfg config.ExternalCoordinationConfig) externalcoordination.Target {
	return externalcoordination.Target{
		LogicalRole:      "external-coordination",
		TargetID:         strings.TrimSpace(cfg.Target),
		Adapter:          strings.TrimSpace(cfg.Adapter),
		Provider:         strings.TrimSpace(cfg.Provider),
		AccountID:        strings.TrimSpace(cfg.AccountID),
		ConversationID:   strings.TrimSpace(cfg.ConversationID),
		DeliveryMode:     externalcoordination.DeliveryMode(cfg.EffectiveDelivery()),
		SessionMode:      externalcoordination.SessionMode(cfg.EffectiveSessionPolicy()),
		InterruptAllowed: cfg.EffectiveInterruptPolicy() == "emergency_only",
		ConfigRevision:   cfg.ConfigRevision,
	}
}

func routeIdentity(identity string) map[string]string {
	if identity == "" {
		return nil
	}
	return map[string]string{"route_identity": identity}
}
