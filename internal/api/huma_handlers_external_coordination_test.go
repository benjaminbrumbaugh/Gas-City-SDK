package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/externalcoordination"
	"github.com/gastownhall/gascity/internal/extmsg"
)

func TestExternalCoordinationRequestRouteQueuesEphemeralRequestWithoutTouchingMail(t *testing.T) {
	state := newFakeState(t)
	state.cityBeadStore = beads.NewMemStore()
	state.cfg.ExternalCoordination = &config.ExternalCoordinationConfig{
		Enabled:         true,
		Target:          "hermes-desktop",
		Adapter:         "hermes",
		Provider:        "hermes",
		AccountID:       "desktop",
		ConversationID:  "coordination-smoke",
		Delivery:        "queued",
		InterruptPolicy: "never",
		SessionPolicy:   "resume_or_create",
		ConfigRevision:  1,
	}
	h := newTestCityHandler(t, state)

	body := `{"source_agent":"mayor","reason":"direct_request","prompt":"one-off smoke question","correlation_id":"corr-smoke-1","idempotency_key":"coordination-smoke-1"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/external-coordination/requests"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-smoke")
	req.Header.Set("Idempotency-Key", "coordination-smoke-1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /external-coordination/requests status = %d, body = %s", response.Code, response.Body.String())
	}

	var record struct {
		ID      string `json:"id"`
		State   string `json:"state"`
		Request struct {
			ContentRetention string `json:"content_retention"`
		} `json:"request"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record.ID == "" || record.State != "queued" || record.Request.ContentRetention != "ephemeral" {
		t.Fatalf("record = %+v", record)
	}
	if state.pokeCount != 1 {
		t.Fatalf("poke count = %d, want 1", state.pokeCount)
	}
	if messages, err := state.cityMailProv.All("mayor"); err != nil {
		t.Fatal(err)
	} else if len(messages) != 0 {
		t.Fatalf("mail count = %d, want 0", len(messages))
	}
}

func TestExternalCoordinationRequestRouteRejectsCallerTargetOverride(t *testing.T) {
	state := newFakeState(t)
	state.cityBeadStore = beads.NewMemStore()
	state.cfg.ExternalCoordination = &config.ExternalCoordinationConfig{
		Enabled:         true,
		Target:          "hermes-desktop",
		Adapter:         "hermes",
		Provider:        "hermes",
		AccountID:       "desktop",
		ConversationID:  "coordination-smoke",
		Delivery:        "queued",
		InterruptPolicy: "never",
		SessionPolicy:   "resume_or_create",
		ConfigRevision:  1,
	}
	h := newTestCityHandler(t, state)

	body := `{"source_agent":"mayor","reason":"direct_request","prompt":"one-off smoke question","correlation_id":"corr-1","target":{"logical_role":"external-coordination","target_id":"other","adapter":"other","provider":"other","account_id":"other","conversation_id":"other","delivery_mode":"interrupt","session_mode":"new","interrupt_allowed":true,"config_revision":2}}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/external-coordination/requests"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-target-override")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST /external-coordination/requests target override status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestExternalCoordinationResponseRouteAcceptsCurrentExternalAdapterRegistration(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)
	registration := registerExternalCoordinationResponseTestAdapter(t, h, state)
	if registration.Credential == "" || registration.Generation == 0 || registration.Instance == "" {
		t.Fatalf("registration = %+v, want usable callback credential, generation, and instance", registration)
	}

	claimed := claimExternalCoordinationResponseTestRequest(t, state)
	response := postExternalCoordinationResponse(t, h, state, claimed, registration)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /external-coordination/responses status = %d, want 200; body = %s", response.Code, response.Body.String())
	}

	stored, err := externalcoordination.NewService(state.CityBeadStore()).Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != externalcoordination.StateCompleted {
		t.Fatalf("stored request state = %q, want completed", stored.State)
	}
}

func TestExternalCoordinationResponseRouteRejectsReplacedExternalAdapterRegistration(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)
	first := registerExternalCoordinationResponseTestAdapter(t, h, state)
	second := registerExternalCoordinationResponseTestAdapter(t, h, state)
	if first.Credential == "" || second.Credential == "" || first.Credential == second.Credential || first.Generation >= second.Generation || first.Instance == second.Instance {
		t.Fatalf("registrations first=%+v second=%+v, want distinct rotated callback identities", first, second)
	}

	claimed := claimExternalCoordinationResponseTestRequest(t, state)
	response := postExternalCoordinationResponse(t, h, state, claimed, first)
	if response.Code != http.StatusForbidden {
		t.Fatalf("POST /external-coordination/responses with replaced registration status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
	stored, err := externalcoordination.NewService(state.CityBeadStore()).Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != externalcoordination.StateRunning {
		t.Fatalf("stored request state after stale callback = %q, want running", stored.State)
	}
}

type coordinationCallbackRegistration struct {
	Credential string `json:"credential"`
	Generation uint64 `json:"generation"`
	Instance   string `json:"instance"`
}

func newExternalCoordinationResponseTestState(t *testing.T) State {
	t.Helper()
	state := newFakeState(t)
	state.cityBeadStore = beads.NewMemStore()
	state.cfg.ExternalCoordination = &config.ExternalCoordinationConfig{
		Enabled:         true,
		Target:          "hermes-desktop",
		Adapter:         "hermes",
		Provider:        "hermes",
		AccountID:       "desktop",
		ConversationID:  "coordination-smoke",
		Delivery:        "queued",
		InterruptPolicy: "never",
		SessionPolicy:   "resume_or_create",
		ConfigRevision:  1,
	}
	state.adapterReg = extmsg.NewAdapterRegistry()
	return state
}

func registerExternalCoordinationResponseTestAdapter(t *testing.T, h http.Handler, state State) coordinationCallbackRegistration {
	t.Helper()
	body := `{"provider":"hermes","account_id":"desktop","name":"hermes","callback_url":"http://127.0.0.1:9/callback"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-adapter-register")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /extmsg/adapters status = %d, want 201; body = %s", response.Code, response.Body.String())
	}
	var registration coordinationCallbackRegistration
	if err := json.Unmarshal(response.Body.Bytes(), &registration); err != nil {
		t.Fatal(err)
	}
	return registration
}

func claimExternalCoordinationResponseTestRequest(t *testing.T, state State) externalcoordination.RequestRecord {
	t.Helper()
	service := externalcoordination.NewService(state.CityBeadStore())
	record, err := service.Enqueue(context.Background(), externalcoordination.RequestInput{
		SourceAgent:   "mayor",
		Target:        externalcoordination.Target{TargetID: "hermes-desktop", Adapter: "hermes", Provider: "hermes", AccountID: "desktop", ConversationID: "coordination-smoke", ConfigRevision: 1},
		Reason:        externalcoordination.ReasonDirectRequest,
		Prompt:        "one-off smoke question",
		CorrelationID: "corr-response-1",
		Now:           time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return claimed
}

func postExternalCoordinationResponse(t *testing.T, h http.Handler, state State, claimed externalcoordination.RequestRecord, registration coordinationCallbackRegistration) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"request_id":%q,"attempt":%d,"correlation_id":%q,"response_id":"response-1","state":"answered","follow_up_required":false,"received_at":%q}`, claimed.Request.RequestID, claimed.Request.Attempt, claimed.Request.CorrelationID, time.Now().UTC().Format(time.RFC3339Nano))
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/external-coordination/responses"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-response-auth")
	req.Header.Set("Authorization", "Bearer "+registration.Credential)
	req.Header.Set("X-GC-Coordination-Adapter", "hermes")
	req.Header.Set("X-GC-Coordination-Adapter-Generation", fmt.Sprintf("%d", registration.Generation))
	req.Header.Set("X-GC-Coordination-Adapter-Instance", registration.Instance)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return response
}
