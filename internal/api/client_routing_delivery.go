package api

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// RoutingDeliveryListRequest controls one bounded delivery-ID page.
type RoutingDeliveryListRequest struct {
	Limit  int
	Cursor string
}

// RoutingDeliveryPending reads immutable source payloads that have not been
// acknowledged by a transport consumer.
func (c *Client) RoutingDeliveryPending(request RoutingDeliveryListRequest) (routingdecision.DeliveryPage, error) {
	if err := c.requireCityScope(); err != nil {
		return routingdecision.DeliveryPage{}, err
	}
	params := &genclient.GetV0CityByCityNameRoutingDeliveryPendingParams{}
	if request.Limit != 0 {
		limit := int64(request.Limit)
		params.Limit = &limit
	}
	if request.Cursor != "" {
		params.Cursor = &request.Cursor
	}
	response, err := c.cw.GetV0CityByCityNameRoutingDeliveryPendingWithResponse(context.Background(), c.cityName, params)
	if err != nil {
		return routingdecision.DeliveryPage{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return routingdecision.DeliveryPage{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return routingdecision.DeliveryPage{}, err
	}
	if response.JSON200 == nil {
		return routingdecision.DeliveryPage{}, fmt.Errorf("API returned %d with no body", response.StatusCode())
	}
	return convertRoutingWire[routingdecision.DeliveryPage](*response.JSON200)
}

// RoutingDeliveryAck acknowledges one exact immutable delivery payload through
// the normal city-write grant and CSRF path.
func (c *Client) RoutingDeliveryAck(request routingdecision.DeliveryAckRequest) (routingdecision.DeliveryAckResult, error) {
	if err := c.requireCityScope(); err != nil {
		return routingdecision.DeliveryAckResult{}, err
	}
	body, err := convertRoutingWire[genclient.DeliveryAckRequest](request)
	if err != nil {
		return routingdecision.DeliveryAckResult{}, err
	}
	response, err := c.cw.PostV0CityByCityNameRoutingDeliveryAckWithResponse(context.Background(), c.cityName, &genclient.PostV0CityByCityNameRoutingDeliveryAckParams{XGCRequest: "true"}, body)
	if err != nil {
		return routingdecision.DeliveryAckResult{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if response == nil {
		return routingdecision.DeliveryAckResult{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(response.StatusCode(), pdOf(response)); err != nil {
		return routingdecision.DeliveryAckResult{}, err
	}
	if response.JSON200 == nil {
		return routingdecision.DeliveryAckResult{}, fmt.Errorf("API returned %d with no body", response.StatusCode())
	}
	return convertRoutingWire[routingdecision.DeliveryAckResult](*response.JSON200)
}
