package api

import (
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/hca"
)

// HCAAffordanceOutput is the runtime-visible signifier for the external
// Human Coordinator Agent affordance.
type HCAAffordanceOutput struct {
	Body config.HumanCoordinatorSignifier
}

// HCAAffordanceInput scopes the signifier lookup to a city.
type HCAAffordanceInput struct {
	CityScope
}

// HCARequestInput is the input for POST /v0/city/{cityName}/hca/requests.
// Target fields omitted by the caller are filled from [human_coordinator].
type HCARequestInput struct {
	CityScope
	IdempotencyKey string `header:"Idempotency-Key" required:"false" doc:"Idempotency key for safe retries."`
	Body           HCARequestBody
}

// HCARequestBody is the explicit wire representation of hca.RequestInput.
// Keeping transport tags here prevents Go field names from becoming an
// accidental API contract.
type HCARequestBody struct {
	SourceAgent       string               `json:"source_agent" minLength:"1"`
	Target            hca.Target           `json:"target,omitempty"`
	City              string               `json:"city,omitempty"`
	WorkRef           string               `json:"work_ref,omitempty"`
	Repository        string               `json:"repository,omitempty"`
	Rig               string               `json:"rig,omitempty"`
	Reason            hca.Reason           `json:"reason"`
	DeliveryMode      hca.DeliveryMode     `json:"delivery_mode,omitempty"`
	SessionMode       hca.SessionMode      `json:"session_mode,omitempty"`
	Prompt            string               `json:"prompt" minLength:"1"`
	ContentRetention  hca.ContentRetention `json:"content_retention,omitempty"`
	AllowedTools      []string             `json:"allowed_tools,omitempty"`
	CorrelationID     string               `json:"correlation_id" required:"true" minLength:"1"`
	IdempotencyKey    string               `json:"idempotency_key,omitempty"`
	ExpiresAt         time.Time            `json:"expires_at,omitempty"`
	ResultDestination string               `json:"result_destination,omitempty"`
	RouteIdentity     map[string]string    `json:"route_identity,omitempty"`
}

// HCARequestOutput returns the durable request and its delivery state.
type HCARequestOutput struct {
	Body hca.RequestRecord
}

// HCARequestListInput lists durable coordinator requests.
type HCARequestListInput struct {
	CityScope
	State string `query:"state" required:"false" doc:"Optional delivery state filter."`
}

// HCARequestListOutput is the typed list response.
type HCARequestListOutput struct {
	Body struct {
		Items []hca.RequestRecord `json:"items"`
		Total int                 `json:"total"`
	}
}

// HCAResponseInput accepts an execution outcome from the configured adapter.
type HCAResponseInput struct {
	CityScope
	IdempotencyKey    string `header:"Idempotency-Key" required:"false" doc:"Idempotency key for safe retries."`
	Authorization     string `header:"Authorization" required:"true" doc:"Bearer credential issued to the registered HCA adapter."`
	Adapter           string `header:"X-HCA-Adapter" required:"true" doc:"Registered HCA adapter name."`
	AdapterGeneration uint64 `header:"X-HCA-Adapter-Generation" required:"true" doc:"Current adapter registration generation."`
	AdapterInstance   string `header:"X-HCA-Adapter-Instance" required:"true" doc:"Current adapter registration instance."`
	Body              hca.Response
}

// HCAResponseOutput acknowledges a recorded response.
type HCAResponseOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}
