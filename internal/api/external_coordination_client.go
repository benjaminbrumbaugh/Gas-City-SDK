package api

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

// GetExternalCoordinationCapability returns the configured external coordination capability for
// the current city. It is intentionally a read-only discovery call.
func (c *Client) GetExternalCoordinationCapability() (genclient.ExternalCoordinationCapability, error) {
	if err := c.requireCityScope(); err != nil {
		return genclient.ExternalCoordinationCapability{}, err
	}
	resp, err := c.cw.GetV0CityByCityNameExternalCoordinationWithResponse(context.Background(), c.cityName)
	if err != nil {
		return genclient.ExternalCoordinationCapability{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return genclient.ExternalCoordinationCapability{}, err
	}
	if resp.JSON200 == nil {
		return genclient.ExternalCoordinationCapability{}, fmt.Errorf("API returned %d with no body", resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// EnqueueExternalCoordinationRequest routes a callback request through the controller.
// There is intentionally no local-store fallback: the controller owns the
// durable queue and its dispatcher boundary.
func (c *Client) EnqueueExternalCoordinationRequest(body genclient.ExternalCoordinationRequestBody) (genclient.RequestRecord, error) {
	if err := c.requireCityScope(); err != nil {
		return genclient.RequestRecord{}, err
	}
	params := &genclient.PostV0CityByCityNameExternalCoordinationRequestsParams{XGCRequest: "gc-external-coordination", IdempotencyKey: body.IdempotencyKey}
	resp, err := c.cw.PostV0CityByCityNameExternalCoordinationRequestsWithResponse(context.Background(), c.cityName, params, body)
	if err != nil {
		return genclient.RequestRecord{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return genclient.RequestRecord{}, err
	}
	if resp.JSON200 == nil {
		return genclient.RequestRecord{}, fmt.Errorf("API returned %d with no body", resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// ListExternalCoordinationRequests reads the durable callback queue through the controller.
func (c *Client) ListExternalCoordinationRequests(state string) ([]genclient.RequestRecord, error) {
	if err := c.requireCityScope(); err != nil {
		return nil, err
	}
	params := &genclient.GetV0CityByCityNameExternalCoordinationRequestsParams{}
	if state != "" {
		params.State = &state
	}
	resp, err := c.cw.GetV0CityByCityNameExternalCoordinationRequestsWithResponse(context.Background(), c.cityName, params)
	if err != nil {
		return nil, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Items == nil {
		return []genclient.RequestRecord{}, nil
	}
	return *resp.JSON200.Items, nil
}
