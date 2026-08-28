package api

import (
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/externalcoordination"
)

// ExternalCoordinationCapabilityOutput is the runtime-visible external coordination capability.
type ExternalCoordinationCapabilityOutput struct {
	Body config.ExternalCoordinationCapability
}

// ExternalCoordinationCapabilityInput scopes capability lookup to a city.
type ExternalCoordinationCapabilityInput struct{ CityScope }

// ExternalCoordinationRequestInput is the input for POST /v0/city/{cityName}/external-coordination/requests.
// Target fields omitted by the caller are filled from [external_coordination].
type ExternalCoordinationRequestInput struct {
	CityScope
	IdempotencyKey string `header:"Idempotency-Key" required:"false" doc:"Idempotency key for safe retries."`
	Body           ExternalCoordinationRequestBody
}

// ExternalCoordinationRequestBody is the explicit wire representation of externalcoordination.RequestInput.
// Keeping transport tags here prevents Go field names from becoming an
// accidental API contract.
type ExternalCoordinationRequestBody struct {
	SourceAgent       string                                `json:"source_agent" minLength:"1"`
	Target            externalcoordination.Target           `json:"target,omitempty"`
	City              string                                `json:"city,omitempty"`
	WorkRef           string                                `json:"work_ref,omitempty"`
	Repository        string                                `json:"repository,omitempty"`
	Rig               string                                `json:"rig,omitempty"`
	Reason            externalcoordination.Reason           `json:"reason"`
	DeliveryMode      externalcoordination.DeliveryMode     `json:"delivery_mode,omitempty"`
	SessionMode       externalcoordination.SessionMode      `json:"session_mode,omitempty"`
	Prompt            string                                `json:"prompt" minLength:"1"`
	ContentRetention  externalcoordination.ContentRetention `json:"content_retention,omitempty"`
	AllowedTools      []string                              `json:"allowed_tools,omitempty"`
	CorrelationID     string                                `json:"correlation_id" required:"true" minLength:"1"`
	IdempotencyKey    string                                `json:"idempotency_key,omitempty"`
	ExpiresAt         time.Time                             `json:"expires_at,omitempty"`
	ResultDestination string                                `json:"result_destination,omitempty"`
	RouteIdentity     map[string]string                     `json:"route_identity,omitempty"`
}

// ExternalCoordinationRequestOutput returns the durable request and its delivery state.
type ExternalCoordinationRequestOutput struct {
	Body externalcoordination.RequestRecord
}

// ExternalCoordinationRequestListInput lists durable coordinator requests.
type ExternalCoordinationRequestListInput struct {
	CityScope
	State string `query:"state" required:"false" doc:"Optional delivery state filter."`
}

// ExternalCoordinationRequestListOutput is the typed list response.
type ExternalCoordinationRequestListOutput struct {
	Body struct {
		Items []externalcoordination.RequestRecord `json:"items"`
		Total int                                  `json:"total"`
	}
}

// ExternalCoordinationResponseInput accepts an execution outcome from the configured adapter.
type ExternalCoordinationResponseInput struct {
	CityScope
	IdempotencyKey    string `header:"Idempotency-Key" required:"false" doc:"Idempotency key for safe retries."`
	Authorization     string `header:"Authorization" required:"true" doc:"Bearer credential issued to the registered external coordination adapter."`
	Adapter           string `header:"X-GC-Coordination-Adapter" required:"true" doc:"Registered external coordination adapter name."`
	AdapterGeneration uint64 `header:"X-GC-Coordination-Adapter-Generation" required:"true" doc:"Current adapter registration generation."`
	AdapterInstance   string `header:"X-GC-Coordination-Adapter-Instance" required:"true" doc:"Current adapter registration instance."`
	Body              externalcoordination.Response
}

// ExternalCoordinationResponseOutput acknowledges a recorded response.
type ExternalCoordinationResponseOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}
