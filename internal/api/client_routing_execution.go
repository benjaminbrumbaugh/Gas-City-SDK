package api

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// RoutingExecutionOutcomes is additive; RoutingOutcomes remains strictly v2.
func (c *Client) RoutingExecutionOutcomes(request RoutingOutcomeListRequest) (routingdecision.ProducerExecutionOutcomePage, error) {
	if err := c.requireCityScope(); err != nil {
		return routingdecision.ProducerExecutionOutcomePage{}, err
	}
	params := &genclient.ListRoutingExecutionOutcomesParams{}
	if request.Limit != 0 {
		limit := int64(request.Limit)
		params.Limit = &limit
	}
	if request.Cursor != "" {
		params.Cursor = &request.Cursor
	}
	response, err := c.cw.ListRoutingExecutionOutcomesWithResponse(context.Background(), c.cityName, params)
	if err != nil {
		return routingdecision.ProducerExecutionOutcomePage{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return routingdecision.ProducerExecutionOutcomePage{}, fmt.Errorf("nil routing execution response")
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return routingdecision.ProducerExecutionOutcomePage{}, err
	}
	if response.JSON200 == nil {
		return routingdecision.ProducerExecutionOutcomePage{}, fmt.Errorf("API returned %d without routing execution body", response.StatusCode())
	}
	return convertRoutingWire[routingdecision.ProducerExecutionOutcomePage](*response.JSON200)
}
