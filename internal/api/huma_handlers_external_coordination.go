package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/externalcoordination"
	"github.com/gastownhall/gascity/internal/extmsg"
)

func (s *Server) humaExternalCoordinationService() (*externalcoordination.Service, error) {
	if s.state.CityBeadStore() == nil {
		return nil, apierr.ServiceUnavailable.Msg("city bead store unavailable")
	}
	cfg := s.state.Config()
	if cfg == nil || cfg.ExternalCoordination == nil || !cfg.ExternalCoordination.Enabled {
		return nil, apierr.ServiceUnavailable.Msg("external coordination is not enabled")
	}
	if err := cfg.ExternalCoordination.Validate(); err != nil {
		return nil, apierr.InvalidRequest.Msg(err.Error())
	}
	return externalcoordination.NewService(s.state.CityBeadStore()), nil
}

// humaHandleExternalCoordinationCapability exposes capability separately from request
// delivery so an orchestrator can discover it without creating traffic.
func (s *Server) humaHandleExternalCoordinationCapability(_ context.Context, _ *ExternalCoordinationCapabilityInput) (*ExternalCoordinationCapabilityOutput, error) {
	cfg := s.state.Config()
	if cfg == nil {
		return nil, apierr.ServiceUnavailable.Msg("city configuration unavailable")
	}
	if cfg.ExternalCoordination == nil {
		return &ExternalCoordinationCapabilityOutput{Body: config.ExternalCoordinationConfig{}.Capability()}, nil
	}
	return &ExternalCoordinationCapabilityOutput{Body: cfg.ExternalCoordination.Capability()}, nil
}

// humaHandleExternalCoordinationRequest queues an external coordination request. It never attempts
// to interrupt an external session; an adapter/dispatcher delivers the durable
// record according to the configured policy.
func (s *Server) humaHandleExternalCoordinationRequest(ctx context.Context, input *ExternalCoordinationRequestInput) (*ExternalCoordinationRequestOutput, error) {
	service, err := s.humaExternalCoordinationService()
	if err != nil {
		return nil, err
	}
	cfg := s.state.Config().ExternalCoordination
	request := externalcoordination.RequestInput{
		SourceAgent:       input.Body.SourceAgent,
		Target:            input.Body.Target,
		City:              input.Body.City,
		WorkRef:           input.Body.WorkRef,
		Repository:        input.Body.Repository,
		Rig:               input.Body.Rig,
		Reason:            input.Body.Reason,
		DeliveryMode:      input.Body.DeliveryMode,
		SessionMode:       input.Body.SessionMode,
		Prompt:            input.Body.Prompt,
		ContentRetention:  input.Body.ContentRetention,
		AllowedTools:      input.Body.AllowedTools,
		CorrelationID:     input.Body.CorrelationID,
		IdempotencyKey:    input.Body.IdempotencyKey,
		ExpiresAt:         input.Body.ExpiresAt,
		ResultDestination: input.Body.ResultDestination,
		RouteIdentity:     input.Body.RouteIdentity,
	}
	if err := normalizeExternalCoordinationTarget(&request, cfg, s.state.CityName()); err != nil {
		return nil, apierr.InvalidRequest.Msg(err.Error())
	}
	if request.Now.IsZero() {
		request.Now = time.Now()
	}
	if input.IdempotencyKey != "" {
		if request.IdempotencyKey != "" && request.IdempotencyKey != input.IdempotencyKey {
			return nil, apierr.InvalidRequest.Msg("Idempotency-Key header does not match body idempotency_key")
		}
		request.IdempotencyKey = input.IdempotencyKey
	}
	record, err := withIdempotency(s.idem, "/v0/external-coordination/requests", input.IdempotencyKey, request,
		func() (externalcoordination.RequestRecord, error) {
			return service.Enqueue(ctx, request)
		})
	if err != nil {
		if errors.Is(err, externalcoordination.ErrInvalidInput) {
			return nil, apierr.InvalidRequest.Msg(err.Error())
		}
		return nil, apierr.Internal.Msg(err.Error())
	}
	s.state.Poke()
	queued := record
	s.runBackground(func(ctx context.Context) {
		s.dispatchExternalCoordinationRequest(ctx, queued)
	})
	return &ExternalCoordinationRequestOutput{Body: record}, nil
}

func normalizeExternalCoordinationTarget(request *externalcoordination.RequestInput, cfg *config.ExternalCoordinationConfig, city string) error {
	expected := externalcoordination.Target{
		LogicalRole:      "external-coordination",
		TargetID:         cfg.Target,
		Adapter:          cfg.Adapter,
		Provider:         cfg.Provider,
		AccountID:        cfg.AccountID,
		ConversationID:   cfg.ConversationID,
		DeliveryMode:     externalcoordination.DeliveryMode(cfg.EffectiveDelivery()),
		SessionMode:      externalcoordination.SessionMode(cfg.EffectiveSessionPolicy()),
		InterruptAllowed: cfg.EffectiveInterruptPolicy() == "emergency_only",
		ConfigRevision:   cfg.ConfigRevision,
	}
	got := request.Target
	if (got.LogicalRole != "" && got.LogicalRole != expected.LogicalRole) ||
		(got.TargetID != "" && got.TargetID != expected.TargetID) ||
		(got.Adapter != "" && got.Adapter != expected.Adapter) ||
		(got.Provider != "" && got.Provider != expected.Provider) ||
		(got.AccountID != "" && got.AccountID != expected.AccountID) ||
		(got.ConversationID != "" && got.ConversationID != expected.ConversationID) ||
		(got.DeliveryMode != "" && got.DeliveryMode != expected.DeliveryMode) ||
		(got.SessionMode != "" && got.SessionMode != expected.SessionMode) ||
		(got.InterruptAllowed && !expected.InterruptAllowed) ||
		(got.ConfigRevision != 0 && got.ConfigRevision != expected.ConfigRevision) {
		return fmt.Errorf("external coordination target must match configuration")
	}
	if request.City != "" && request.City != city {
		return fmt.Errorf("external coordination city must match request city scope")
	}
	if request.DeliveryMode != "" && request.DeliveryMode != expected.DeliveryMode {
		return fmt.Errorf("external coordination delivery_mode must match configuration")
	}
	if request.SessionMode != "" && request.SessionMode != expected.SessionMode {
		return fmt.Errorf("external coordination session_mode must match configuration")
	}
	request.Target = expected
	request.City = city
	request.DeliveryMode = expected.DeliveryMode
	request.SessionMode = expected.SessionMode
	return nil
}

// dispatchExternalCoordinationRequest is best-effort at the API boundary. If the configured
// adapter has not registered yet, the durable request remains queued for the
// next controller dispatch opportunity; the API must not turn that absence
// into a false success or delete the causal record.
func (s *Server) dispatchExternalCoordinationRequest(ctx context.Context, record externalcoordination.RequestRecord) {
	registry := s.state.AdapterRegistry()
	if registry == nil {
		return
	}
	transport := registry.Lookup(extmsg.AdapterKey{
		Provider:  record.Request.Target.Provider,
		AccountID: record.Request.Target.AccountID,
	})
	if transport == nil {
		return
	}
	dispatcher := externalcoordination.Dispatcher{
		Queue:   externalcoordination.NewService(s.state.CityBeadStore()),
		Adapter: externalcoordination.NewTransportAdapter(transport, s.state.CityName()),
		Worker:  "city-api-external-coordination-dispatcher",
	}
	_, _, _ = dispatcher.DeliverNext(ctx, time.Now())
}

func (s *Server) humaHandleExternalCoordinationRequestList(ctx context.Context, input *ExternalCoordinationRequestListInput) (*ExternalCoordinationRequestListOutput, error) {
	service, err := s.humaExternalCoordinationService()
	if err != nil {
		return nil, err
	}
	var states []externalcoordination.DeliveryState
	if state := strings.TrimSpace(input.State); state != "" {
		states = []externalcoordination.DeliveryState{externalcoordination.DeliveryState(state)}
	}
	items, err := service.List(ctx, states...)
	if err != nil {
		return nil, apierr.Internal.Msg(err.Error())
	}
	out := &ExternalCoordinationRequestListOutput{}
	out.Body.Items = items
	out.Body.Total = len(items)
	return out, nil
}

// humaHandleExternalCoordinationResponse records an external execution outcome. The request ID
// may be either the durable bead ID or the opaque request_id in the envelope.
func (s *Server) humaHandleExternalCoordinationResponse(ctx context.Context, input *ExternalCoordinationResponseInput) (*ExternalCoordinationResponseOutput, error) {
	service, err := s.humaExternalCoordinationService()
	if err != nil {
		return nil, err
	}
	cfg := s.state.Config().ExternalCoordination
	registry := s.state.AdapterRegistry()
	if registry == nil || input.Adapter != cfg.Adapter {
		return nil, apierr.Forbidden.Msg("external coordination response adapter is not the configured adapter")
	}
	if _, ok := registry.Authenticate(extmsg.AdapterKey{Provider: cfg.Provider, AccountID: cfg.AccountID}, input.Adapter, input.AdapterGeneration, input.AdapterInstance, input.Authorization); !ok {
		return nil, apierr.Forbidden.Msg("external coordination response adapter credential is invalid or stale")
	}
	_, err = withIdempotency(s.idem, "/v0/external-coordination/responses", input.IdempotencyKey, input.Body,
		func() (string, error) {
			return "recorded", service.RecordResponse(ctx, input.Body)
		})
	if err != nil {
		if errors.Is(err, externalcoordination.ErrInvalidInput) || errors.Is(err, externalcoordination.ErrNotFound) {
			return nil, apierr.InvalidRequest.Msg(err.Error())
		}
		return nil, apierr.Internal.Msg(fmt.Sprintf("recording external coordination response: %v", err))
	}
	out := &ExternalCoordinationResponseOutput{}
	out.Body.Status = "recorded"
	return out, nil
}
