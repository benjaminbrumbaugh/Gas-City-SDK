package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/routingdecision"
)

func TestRoutingClientUsesGeneratedRoutesAndFinalGrantBinding(t *testing.T) {
	var binding GrantBinding
	transport := rtFunc(func(r *http.Request) (*http.Response, error) {
		var status int
		var value any
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/city/acme/routing/status":
			status = http.StatusOK
			value = routingdecision.LiveStatus{Schema: 1, Status: "ready", Reason: "ready"}
		case r.Method == http.MethodGet && r.URL.Path == "/v0/city/acme/routing/decisions":
			status = http.StatusOK
			value = routingdecision.DecisionPage{
				Items: []routingdecision.DecisionWithAudits{{
					Record: routingdecision.Record{Payload: routingdecision.DecisionPayload{DecisionID: "decision-1"}},
				}},
				Total: 1,
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v0/city/acme/routing/outcomes":
			if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("cursor") != "after" {
				t.Fatalf("outcome query = %q", r.URL.RawQuery)
			}
			status = http.StatusOK
			value = routingdecision.OutcomePage{
				SchemaVersion: routingdecision.OutcomeSchemaVersion,
				Items:         []routingdecision.OutcomeRecord{{SchemaVersion: routingdecision.OutcomeSchemaVersion, RecommendationID: "rec-1", RoutingDecisionID: "decision-1"}},
				NextCursor:    "next", Partial: true,
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v0/city/acme/routing/decisions":
			if r.Header.Get("Idempotency-Key") != "idem-1" || r.Header.Get("X-GC-City-Write") != "grant-token" {
				t.Fatalf("headers = %#v", r.Header)
			}
			status = http.StatusCreated
			value = routingdecision.IngestApprovedResult{
				Record:  routingdecision.Record{Payload: routingdecision.DecisionPayload{DecisionID: "decision-1"}, State: routingdecision.StateApproved},
				Receipt: routingdecision.TransitionReceipt{DecisionID: "decision-1", State: routingdecision.StateApproved},
			}
		default:
			status = http.StatusNotFound
			value = struct{}{}
		}
		body, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})

	client, err := NewInProcessCityScopedClient("acme", transport)
	if err != nil {
		t.Fatal(err)
	}
	client.SetGrantSource(func(got GrantBinding) (string, error) {
		binding = got
		return "grant-token", nil
	})
	status, err := client.RoutingStatus()
	if err != nil || status.Status != routingdecision.AvailabilityReady {
		t.Fatalf("RoutingStatus = (%+v, %v)", status, err)
	}
	page, err := client.RoutingDecisions(RoutingDecisionListRequest{Limit: 10})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Record.Payload.DecisionID != "decision-1" {
		t.Fatalf("RoutingDecisions = (%+v, %v)", page, err)
	}
	outcomes, err := client.RoutingOutcomes(RoutingOutcomeListRequest{Limit: 10, Cursor: "after"})
	if err != nil || outcomes.SchemaVersion != routingdecision.OutcomeSchemaVersion || len(outcomes.Items) != 1 || outcomes.Items[0].RecommendationID != "rec-1" || outcomes.NextCursor != "next" || !outcomes.Partial {
		t.Fatalf("RoutingOutcomes = (%+v, %v)", outcomes, err)
	}
	request := routingdecision.IngestApprovedRequest{
		Payload:          routingdecision.DecisionPayload{DecisionID: "decision-1", CreatedAt: time.Now()},
		IdempotencyToken: "idem-1",
	}
	result, err := client.RoutingIngest(request)
	if err != nil || result.Record.Payload.DecisionID != "decision-1" {
		t.Fatalf("RoutingIngest = (%+v, %v)", result, err)
	}
	if binding.Method != http.MethodPost || binding.Path != "/v0/city/acme/routing/decisions" ||
		binding.BodySHA256 == "" || binding.ReqDigest == "" || strings.Contains(binding.ReqDigest, "decision-1") {
		t.Fatalf("binding = %+v", binding)
	}
}
