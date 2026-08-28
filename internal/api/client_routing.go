package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// RoutingDecisionListRequest controls one bounded decision-ID page.
type RoutingDecisionListRequest struct {
	State  routingdecision.State
	Limit  int
	Cursor string
}

// RoutingOutcomeListRequest controls one bounded decision-ID outcome page.
type RoutingOutcomeListRequest struct {
	Limit  int
	Cursor string
}

func convertRoutingWire[To any, From any](from From) (To, error) {
	var to To
	encoded, err := json.Marshal(from)
	if err != nil {
		return to, fmt.Errorf("encode generated routing response: %w", err)
	}
	if err := json.Unmarshal(encoded, &to); err != nil {
		return to, fmt.Errorf("decode routing response: %w", err)
	}
	return to, nil
}

// RoutingStatus reads the boot-latched routing service and ledger status.
func (c *Client) RoutingStatus() (routingdecision.LiveStatus, error) {
	if err := c.requireCityScope(); err != nil {
		return routingdecision.LiveStatus{}, err
	}
	response, err := c.cw.GetRoutingStatusWithResponse(context.Background(), c.cityName)
	if err != nil {
		return routingdecision.LiveStatus{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return routingdecision.LiveStatus{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return routingdecision.LiveStatus{}, err
	}
	if response.JSON200 == nil {
		return routingdecision.LiveStatus{}, fmt.Errorf("API returned %d with no body", response.StatusCode())
	}
	return convertRoutingWire[routingdecision.LiveStatus](*response.JSON200)
}

// RoutingTargets reads the deterministic selection-safe target snapshot.
func (c *Client) RoutingTargets() ([]routingdecision.TargetSnapshot, error) {
	if err := c.requireCityScope(); err != nil {
		return nil, err
	}
	response, err := c.cw.ListRoutingTargetsWithResponse(context.Background(), c.cityName)
	if err != nil {
		return nil, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return nil, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return nil, err
	}
	if response.JSON200 == nil || response.JSON200.Items == nil {
		return []routingdecision.TargetSnapshot{}, nil
	}
	return convertRoutingWire[[]routingdecision.TargetSnapshot](*response.JSON200.Items)
}

// RoutingEligible reads one deterministic selector-input observation.
func (c *Client) RoutingEligible() (routingdecision.SelectionSnapshot, error) {
	if err := c.requireCityScope(); err != nil {
		return routingdecision.SelectionSnapshot{}, err
	}
	response, err := c.cw.GetRoutingEligibleWithResponse(context.Background(), c.cityName)
	if err != nil {
		return routingdecision.SelectionSnapshot{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return routingdecision.SelectionSnapshot{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return routingdecision.SelectionSnapshot{}, err
	}
	if response.JSON200 == nil {
		return routingdecision.SelectionSnapshot{}, fmt.Errorf("API returned %d with no body", response.StatusCode())
	}
	return convertRoutingWire[routingdecision.SelectionSnapshot](*response.JSON200)
}

// RoutingDecisions reads one bounded decision-ID keyset page.
func (c *Client) RoutingDecisions(request RoutingDecisionListRequest) (routingdecision.DecisionPage, error) {
	if err := c.requireCityScope(); err != nil {
		return routingdecision.DecisionPage{}, err
	}
	params := &genclient.ListRoutingDecisionsParams{}
	if request.State != "" {
		state := genclient.ListRoutingDecisionsParamsState(request.State)
		params.State = &state
	}
	if request.Limit != 0 {
		limit := int64(request.Limit)
		params.Limit = &limit
	}
	if request.Cursor != "" {
		params.Cursor = &request.Cursor
	}
	response, err := c.cw.ListRoutingDecisionsWithResponse(context.Background(), c.cityName, params)
	if err != nil {
		return routingdecision.DecisionPage{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return routingdecision.DecisionPage{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return routingdecision.DecisionPage{}, err
	}
	if response.JSON200 == nil || response.JSON200.Items == nil {
		return routingdecision.DecisionPage{Items: []routingdecision.DecisionWithAudits{}}, nil
	}
	items, err := convertRoutingWire[[]routingdecision.DecisionWithAudits](*response.JSON200.Items)
	if err != nil {
		return routingdecision.DecisionPage{}, err
	}
	next := ""
	if response.JSON200.NextCursor != nil {
		next = *response.JSON200.NextCursor
	}
	return routingdecision.DecisionPage{Items: items, Total: int(response.JSON200.Total), NextCursor: next}, nil
}

// RoutingOutcomes reads one strict redacted routing/outcome/v2 page.
func (c *Client) RoutingOutcomes(request RoutingOutcomeListRequest) (routingdecision.OutcomePage, error) {
	if err := c.requireCityScope(); err != nil {
		return routingdecision.OutcomePage{}, err
	}
	params := &genclient.ListRoutingOutcomesParams{}
	if request.Limit != 0 {
		limit := int64(request.Limit)
		params.Limit = &limit
	}
	if request.Cursor != "" {
		params.Cursor = &request.Cursor
	}
	response, err := c.cw.ListRoutingOutcomesWithResponse(context.Background(), c.cityName, params)
	if err != nil {
		return routingdecision.OutcomePage{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return routingdecision.OutcomePage{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return routingdecision.OutcomePage{}, err
	}
	if response.JSON200 == nil {
		return routingdecision.OutcomePage{}, fmt.Errorf("API returned %d with no body", response.StatusCode())
	}
	return convertRoutingWire[routingdecision.OutcomePage](*response.JSON200)
}

// RoutingIngest sends one exact typed signed envelope and idempotency key.
func (c *Client) RoutingIngest(request routingdecision.IngestApprovedRequest) (routingdecision.IngestApprovedResult, error) {
	if err := c.requireCityScope(); err != nil {
		return routingdecision.IngestApprovedResult{}, err
	}
	body, err := convertRoutingWire[genclient.RoutingDecisionIngestBody](RoutingDecisionIngestBody{
		Payload: request.Payload, Approval: request.Approval, Signature: request.Signature,
	})
	if err != nil {
		return routingdecision.IngestApprovedResult{}, err
	}
	response, err := c.cw.IngestRoutingDecisionWithResponse(context.Background(), c.cityName, &genclient.IngestRoutingDecisionParams{
		XGCRequest: "true", IdempotencyKey: request.IdempotencyToken,
	}, body)
	if err != nil {
		return routingdecision.IngestApprovedResult{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return routingdecision.IngestApprovedResult{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return routingdecision.IngestApprovedResult{}, err
	}
	if response.JSON201 == nil {
		return routingdecision.IngestApprovedResult{}, fmt.Errorf("API returned %d with no body", response.StatusCode())
	}
	return convertRoutingWire[routingdecision.IngestApprovedResult](*response.JSON201)
}
