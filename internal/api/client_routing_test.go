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

func routingStringPointer(value string) *string { return &value }

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
				Items:         []routingdecision.OutcomeRecord{{SchemaVersion: routingdecision.OutcomeSchemaVersion, RecommendationID: "routing/v2:" + strings.Repeat("c", 64), RoutingDecisionID: routingStringPointer("decision-1")}},
				NextCursor:    "next", Partial: true,
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v0/city/acme/routing/delivery/pending":
			if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("cursor") != "after-delivery" {
				t.Fatalf("delivery query = %q", r.URL.RawQuery)
			}
			status = http.StatusOK
			value = routingdecision.DeliveryPage{SchemaVersion: routingdecision.DeliverySchemaVersion, Items: []routingdecision.DeliveryItem{{DeliveryID: "delivery-1", Payload: []byte("exact")}}, NextCursor: "next-delivery"}
		case r.Method == http.MethodPost && r.URL.Path == "/v0/city/acme/routing/decisions":
			if r.Header.Get("Idempotency-Key") != "idem-1" || r.Header.Get("X-GC-City-Write") != "grant-token" {
				t.Fatalf("headers = %#v", r.Header)
			}
			status = http.StatusCreated
			value = routingdecision.IngestApprovedResult{
				Record:  routingdecision.Record{Payload: routingdecision.DecisionPayload{DecisionID: "decision-1"}, State: routingdecision.StateApproved},
				Receipt: routingdecision.TransitionReceipt{DecisionID: "decision-1", State: routingdecision.StateApproved},
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v0/city/acme/routing/admit":
			if r.Header.Get("Idempotency-Key") != "local-1" || r.Header.Get("X-GC-City-Write") != "grant-token" {
				t.Fatalf("local headers = %#v", r.Header)
			}
			status = http.StatusOK
			value = routingdecision.LocalAdmissionResult{
				Record:  routingdecision.Record{Local: true, Payload: routingdecision.DecisionPayload{DecisionID: "local-1"}, State: routingdecision.StateAdmitted},
				Receipt: routingdecision.TransitionReceipt{DecisionID: "local-1", State: routingdecision.StateAdmitted},
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v0/city/acme/routing/delivery/ack":
			if r.Header.Get("X-GC-Request") == "" || r.Header.Get("X-GC-City-Write") != "grant-token" {
				t.Fatalf("delivery ack headers = %#v", r.Header)
			}
			status = http.StatusOK
			value = routingdecision.DeliveryAckResult{Ack: routingdecision.DeliveryAck{DeliveryID: "delivery-1", PayloadSHA256: "sha256:" + strings.Repeat("a", 64), AcknowledgedAtUnix: 1}, Replay: true}
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
	if err := client.SetGrantSource(func(got GrantBinding) (string, error) {
		binding = got
		return "grant-token", nil
	}); err != nil {
		t.Fatal(err)
	}
	status, err := client.RoutingStatus()
	if err != nil || status.Status != routingdecision.AvailabilityReady {
		t.Fatalf("RoutingStatus = (%+v, %v)", status, err)
	}
	page, err := client.RoutingDecisions(RoutingDecisionListRequest{Limit: 10})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Record.Payload.DecisionID != "decision-1" {
		t.Fatalf("RoutingDecisions = (%+v, %v)", page, err)
	}
	outcomes, err := client.RoutingOutcomes(RoutingOutcomeListRequest{Limit: 10, Cursor: "after"})
	if err != nil || outcomes.SchemaVersion != routingdecision.OutcomeSchemaVersion || len(outcomes.Items) != 1 || outcomes.Items[0].RecommendationID != "routing/v2:"+strings.Repeat("c", 64) || outcomes.NextCursor != "next" || !outcomes.Partial {
		t.Fatalf("RoutingOutcomes = (%+v, %v)", outcomes, err)
	}
	deliveries, err := client.RoutingDeliveryPending(RoutingDeliveryListRequest{Limit: 10, Cursor: "after-delivery"})
	if err != nil || deliveries.SchemaVersion != routingdecision.DeliverySchemaVersion || len(deliveries.Items) != 1 || string(deliveries.Items[0].Payload) != "exact" || deliveries.NextCursor != "next-delivery" {
		t.Fatalf("RoutingDeliveryPending = (%+v, %v)", deliveries, err)
	}
	ack, err := client.RoutingDeliveryAck(routingdecision.DeliveryAckRequest{DeliveryID: "delivery-1", PayloadSHA256: "sha256:" + strings.Repeat("a", 64)})
	if err != nil || !ack.Replay || ack.Ack.DeliveryID != "delivery-1" {
		t.Fatalf("RoutingDeliveryAck = (%+v, %v)", ack, err)
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
	localRequest := routingdecision.LocalAdmissionRequest{
		RecommendationID: "routing/v3:" + strings.Repeat("a", 64),
		Work: routingdecision.EligibleWorkSnapshot{
			Rig: "myrig", Scope: "rig", WorkBeadID: "work-1", WorkRevision: 7, ClaimFence: 3, WorkStateDigest: strings.Repeat("b", 64),
		},
		Candidate: routingdecision.ExecutionCandidateSnapshot{
			Schema: 1, CanonicalModel: "model", ServeAs: "model", ReasoningEffort: "high", Account: "account", Provider: "provider", Target: "worker",
			ConfigDigest: strings.Repeat("c", 64), AdapterID: "adapter", AdapterDigest: strings.Repeat("d", 64), InvocationDigest: strings.Repeat("e", 64),
		},
	}
	local, err := client.RoutingAdmitLocal(localRequest, "local-1")
	if err != nil || !local.Record.Local || local.Record.State != routingdecision.StateAdmitted {
		t.Fatalf("RoutingAdmitLocal = (%+v, %v)", local, err)
	}
	if binding.Method != http.MethodPost || binding.Path != "/v0/city/acme/routing/admit" ||
		binding.BodySHA256 == "" || binding.ReqDigest == "" || strings.Contains(binding.ReqDigest, "model") {
		t.Fatalf("local binding = %+v", binding)
	}
}
