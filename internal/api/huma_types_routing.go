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
