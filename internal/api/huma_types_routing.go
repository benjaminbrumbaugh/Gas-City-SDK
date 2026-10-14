package api

import "github.com/gastownhall/gascity/internal/routingdecision"

// RoutingDecisionStatusInput is the Huma input for the live routing status.
type RoutingDecisionStatusInput struct{ CityScope }

// RoutingDecisionStatusOutput is the live routing status response.
type RoutingDecisionStatusOutput struct {
	Body routingdecision.LiveStatus
}

// RoutingDecisionTargetsInput is the Huma input for deterministic targets.
type RoutingDecisionTargetsInput struct{ CityScope }

// RoutingDecisionTargetsBody is the deterministic target collection.
type RoutingDecisionTargetsBody struct {
	Items []routingdecision.TargetSnapshot `json:"items"`
}

// RoutingDecisionTargetsOutput wraps the deterministic target collection.
type RoutingDecisionTargetsOutput struct {
	Body RoutingDecisionTargetsBody
}

// RoutingDecisionEligibleInput is the Huma input for selector inputs.
type RoutingDecisionEligibleInput struct{ CityScope }

// RoutingDecisionEligibleOutput is one atomically observed selection boundary.
type RoutingDecisionEligibleOutput struct {
	Body routingdecision.SelectionSnapshot
}

// RoutingDecisionListInput is the Huma input for decision-ID keyset listing.
type RoutingDecisionListInput struct {
	CityScope
	State  string `query:"state" required:"false" enum:"proposed,approved,admitted,refused_after_race,expired,revoked,claimed,outcome_recorded" doc:"Filter by exact lifecycle state."`
	Limit  int    `query:"limit" required:"false" minimum:"1" maximum:"256" default:"100" doc:"Maximum decision rows to scan and return."`
	Cursor string `query:"cursor" required:"false" doc:"Opaque decision-ID keyset cursor."`
}

// RoutingDecisionListBody is one bounded decision-ID page.
type RoutingDecisionListBody struct {
	Items      []RoutingDecisionWithAudits `json:"items"`
	Total      int                         `json:"total"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

// RoutingDecisionListOutput wraps one bounded decision-ID page.
type RoutingDecisionListOutput struct {
	Body RoutingDecisionListBody
}

// RoutingOutcomeListInput is the Huma input for bounded outcome projection.
type RoutingOutcomeListInput struct {
	CityScope
	Limit  int    `query:"limit" required:"false" minimum:"1" maximum:"100" default:"100" doc:"Maximum claimed or terminal outcome records to return."`
	Cursor string `query:"cursor" required:"false" doc:"Opaque stable decision-ID keyset cursor."`
}

// RoutingOutcomeListOutput wraps one strict redacted routing/outcome/v2 page.
type RoutingOutcomeListOutput struct {
	Body routingdecision.OutcomePage
}

// RoutingDeliveryPendingInput is the Huma input for bounded immutable delivery reads.
type RoutingDeliveryPendingInput struct {
	CityScope
	Limit  int    `query:"limit" required:"false" minimum:"1" maximum:"100" default:"100" doc:"Maximum pending delivery items to return."`
	Cursor string `query:"cursor" required:"false" doc:"Opaque delivery-ID keyset cursor."`
}

// RoutingDeliveryPendingOutput wraps one pending delivery page.
type RoutingDeliveryPendingOutput struct {
	Body routingdecision.DeliveryPage
}

// RoutingDeliveryAckInput is the city-write-authenticated acknowledgement body.
type RoutingDeliveryAckInput struct {
	CityScope
	Body routingdecision.DeliveryAckRequest
}

// RoutingDeliveryAckOutput wraps one durable acknowledgement result.
type RoutingDeliveryAckOutput struct {
	Body routingdecision.DeliveryAckResult
}

// RoutingDecisionIngestBody is the exact signed approval envelope.
type RoutingDecisionIngestBody struct {
	Payload   routingdecision.DecisionPayload `json:"payload"`
	Approval  routingdecision.ApprovalPayload `json:"approval"`
	Signature routingdecision.Signature       `json:"signature"`
}

// RoutingDecisionIngestInput is the Huma input for durable signed ingest.
type RoutingDecisionIngestInput struct {
	CityScope
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"4096" doc:"Required stable key for exact signed-envelope retries."`
	Body           RoutingDecisionIngestBody
}

// RoutingDecisionIngestOutput is the approved record and immutable receipt.
type RoutingDecisionIngestOutput struct {
	Body RoutingDecisionIngestResult
}

// RoutingDecisionRecord is the API-namespaced durable decision record.
type RoutingDecisionRecord routingdecision.Record

// RoutingDecisionWithAudits is one API record and its ordered history.
type RoutingDecisionWithAudits struct {
	Record RoutingDecisionRecord             `json:"record"`
	Audits []routingdecision.TransitionAudit `json:"audits"`
}

// RoutingDecisionIngestResult is the API-namespaced ingest result.
type RoutingDecisionIngestResult struct {
	Record  RoutingDecisionRecord             `json:"record"`
	Receipt routingdecision.TransitionReceipt `json:"receipt"`
}

// RoutingDecisionLocalAdmissionInput is the normal city-write-authenticated
// local advisory admission request. Its idempotency key is request metadata;
// the body contains only the typed selector output.
type RoutingDecisionLocalAdmissionInput struct {
	CityScope
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"4096" doc:"Required stable key for exact local-admission retries."`
	Body           routingdecision.LocalAdmissionRequest
}

// RoutingDecisionLocalAdmissionResult is the local record and admission
// receipt. It does not assert provider execution or work completion.
type RoutingDecisionLocalAdmissionResult struct {
	Record  RoutingDecisionRecord             `json:"record"`
	Receipt routingdecision.TransitionReceipt `json:"receipt"`
}

// RoutingDecisionLocalAdmissionOutput wraps the local admission result.
type RoutingDecisionLocalAdmissionOutput struct {
	Body RoutingDecisionLocalAdmissionResult
}
