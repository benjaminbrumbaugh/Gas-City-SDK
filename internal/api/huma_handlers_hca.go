package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/extmsg"
	"github.com/gastownhall/gascity/internal/hca"
)

func (s *Server) humaHCAService() (*hca.Service, error) {
	if s.state.CityBeadStore() == nil {
		return nil, apierr.ServiceUnavailable.Msg("city bead store unavailable")
	}
	cfg := s.state.Config()
	if cfg == nil || cfg.HumanCoordinator == nil || !cfg.HumanCoordinator.Enabled {
		return nil, apierr.ServiceUnavailable.Msg("human coordinator affordance is not enabled")
	}
	if err := cfg.HumanCoordinator.Validate(); err != nil {
		return nil, apierr.InvalidRequest.Msg(err.Error())
	}
	return hca.NewService(s.state.CityBeadStore()), nil
}

// humaHandleHCAAffordance exposes the signifier separately from request
// delivery so a Mayor can discover the affordance without creating traffic.
func (s *Server) humaHandleHCAAffordance(_ context.Context, _ *HCAAffordanceInput) (*HCAAffordanceOutput, error) {
	cfg := s.state.Config()
	if cfg == nil {
		return nil, apierr.ServiceUnavailable.Msg("city configuration unavailable")
	}
	if cfg.HumanCoordinator == nil {
		return &HCAAffordanceOutput{Body: config.HumanCoordinatorConfig{}.Signifier()}, nil
	}
	return &HCAAffordanceOutput{Body: cfg.HumanCoordinator.Signifier()}, nil
}

// humaHandleHCARequest queues a Mayor/orchestrator request. It never attempts
// to interrupt an external session; an adapter/dispatcher delivers the durable
// record according to the configured policy.
func (s *Server) humaHandleHCARequest(ctx context.Context, input *HCARequestInput) (*HCARequestOutput, error) {
	service, err := s.humaHCAService()
	if err != nil {
		return nil, err
	}
	cfg := s.state.Config().HumanCoordinator
	request := hca.RequestInput{
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
	if request.Target.TargetID == "" {
		request.Target.TargetID = cfg.Target
	}
	if request.Target.Adapter == "" {
		request.Target.Adapter = cfg.Adapter
	}
	if request.Target.Provider == "" {
		request.Target.Provider = cfg.Provider
	}
	if request.Target.AccountID == "" {
		request.Target.AccountID = cfg.AccountID
	}
	if request.Target.ConversationID == "" {
		request.Target.ConversationID = cfg.ConversationID
	}
	if request.Target.DeliveryMode == "" {
		request.Target.DeliveryMode = hca.DeliveryMode(cfg.EffectiveDelivery())
	}
	if request.Target.SessionMode == "" {
		request.Target.SessionMode = hca.SessionMode(cfg.EffectiveSessionPolicy())
	}
	if request.DeliveryMode == "" {
		request.DeliveryMode = hca.DeliveryMode(cfg.EffectiveDelivery())
	}
	if request.SessionMode == "" {
		request.SessionMode = hca.SessionMode(cfg.EffectiveSessionPolicy())
	}
	if request.Target.ConfigRevision == 0 {
		request.Target.ConfigRevision = cfg.ConfigRevision
	}
	if !request.Target.InterruptAllowed {
		request.Target.InterruptAllowed = cfg.EffectiveInterruptPolicy() == "emergency_only"
	}
	if request.City == "" {
		request.City = s.state.CityName()
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
	record, err := withIdempotency(s.idem, "/v0/hca/requests", input.IdempotencyKey, request,
		func() (hca.RequestRecord, error) {
			return service.Enqueue(ctx, request)
		})
	if err != nil {
		if errors.Is(err, hca.ErrInvalidInput) {
			return nil, apierr.InvalidRequest.Msg(err.Error())
		}
		return nil, apierr.Internal.Msg(err.Error())
	}
	s.state.Poke()
	queued := record
	s.runBackground(func(ctx context.Context) {
		s.dispatchHCARequest(ctx, queued)
	})
	return &HCARequestOutput{Body: record}, nil
}

// dispatchHCARequest is best-effort at the API boundary. If the configured
// adapter has not registered yet, the durable request remains queued for the
// next controller dispatch opportunity; the API must not turn that absence
// into a false success or delete the causal record.
func (s *Server) dispatchHCARequest(ctx context.Context, record hca.RequestRecord) {
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
	dispatcher := hca.Dispatcher{
		Queue:   hca.NewService(s.state.CityBeadStore()),
		Adapter: hca.NewTransportAdapter(transport, s.state.CityName()),
		Worker:  "city-api-hca-dispatcher",
	}
	_, _, _ = dispatcher.DeliverNext(ctx, time.Now())
}

func (s *Server) humaHandleHCARequestList(ctx context.Context, input *HCARequestListInput) (*HCARequestListOutput, error) {
	service, err := s.humaHCAService()
	if err != nil {
		return nil, err
	}
	var states []hca.DeliveryState
	if state := strings.TrimSpace(input.State); state != "" {
		states = []hca.DeliveryState{hca.DeliveryState(state)}
	}
	items, err := service.List(ctx, states...)
	if err != nil {
		return nil, apierr.Internal.Msg(err.Error())
	}
	out := &HCARequestListOutput{}
	out.Body.Items = items
	out.Body.Total = len(items)
	return out, nil
}

// humaHandleHCAResponse records an external execution outcome. The request ID
// may be either the durable bead ID or the opaque request_id in the envelope.
func (s *Server) humaHandleHCAResponse(ctx context.Context, input *HCAResponseInput) (*HCAResponseOutput, error) {
	service, err := s.humaHCAService()
	if err != nil {
		return nil, err
	}
	_, err = withIdempotency(s.idem, "/v0/hca/responses", input.IdempotencyKey, input.Body,
		func() (string, error) {
			return "recorded", service.RecordResponse(ctx, input.Body)
		})
	if err != nil {
		if errors.Is(err, hca.ErrInvalidInput) || errors.Is(err, hca.ErrNotFound) {
			return nil, apierr.InvalidRequest.Msg(err.Error())
		}
		return nil, apierr.Internal.Msg(fmt.Sprintf("recording hca response: %v", err))
	}
	out := &HCAResponseOutput{}
	out.Body.Status = "recorded"
	return out, nil
}
