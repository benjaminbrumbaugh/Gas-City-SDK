package api

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

// GetHCAAffordance returns the configured external-coordinator signifier for
// the current city. It is intentionally a read-only discovery call.
func (c *Client) GetHCAAffordance() (genclient.HumanCoordinatorSignifier, error) {
	if err := c.requireCityScope(); err != nil {
		return genclient.HumanCoordinatorSignifier{}, err
	}
	resp, err := c.cw.GetV0CityByCityNameHcaWithResponse(context.Background(), c.cityName)
	if err != nil {
		return genclient.HumanCoordinatorSignifier{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return genclient.HumanCoordinatorSignifier{}, err
	}
	if resp.JSON200 == nil {
		return genclient.HumanCoordinatorSignifier{}, fmt.Errorf("API returned %d with no body", resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// EnqueueHCARequest routes a Mayor callback request through the controller.
// There is intentionally no local-store fallback: the controller owns the
// durable queue and its dispatcher boundary.
func (c *Client) EnqueueHCARequest(body genclient.HCARequestBody) (genclient.RequestRecord, error) {
	if err := c.requireCityScope(); err != nil {
		return genclient.RequestRecord{}, err
	}
	params := &genclient.PostV0CityByCityNameHcaRequestsParams{XGCRequest: "gc-hca", IdempotencyKey: body.IdempotencyKey}
	resp, err := c.cw.PostV0CityByCityNameHcaRequestsWithResponse(context.Background(), c.cityName, params, body)
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

// ListHCARequests reads the durable callback queue through the controller.
func (c *Client) ListHCARequests(state string) ([]genclient.RequestRecord, error) {
	if err := c.requireCityScope(); err != nil {
		return nil, err
	}
	params := &genclient.GetV0CityByCityNameHcaRequestsParams{}
	if state != "" {
		params.State = &state
	}
	resp, err := c.cw.GetV0CityByCityNameHcaRequestsWithResponse(context.Background(), c.cityName, params)
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
