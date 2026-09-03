package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		t.Fatal("registration is missing a callback credential, generation, or instance")
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

func TestExternalCoordinationResponseRouteAcknowledgesExactReplayAndRejectsDivergenceAfterRestart(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)
	registration := registerExternalCoordinationResponseTestAdapter(t, h, state)
	claimed := claimExternalCoordinationResponseTestRequest(t, state)
	receivedAt := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)
	exactBody := externalCoordinationResponseTestBody(claimed, "response-1", "approved", receivedAt)
	if response := postExternalCoordinationResponseBody(t, h, state, exactBody, "response-replay-1", registration); response.Code != http.StatusOK {
		t.Fatalf("first POST /external-coordination/responses status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	if response := postExternalCoordinationResponseBody(t, h, state, exactBody, "response-replay-1", registration); response.Code != http.StatusOK {
		t.Fatalf("cached exact replay status = %d, want 200; body = %s", response.Code, response.Body.String())
	}

	// Build a new per-city Server, including a fresh process-local idempotency
	// cache, over the same durable bead store and adapter registry.
	restarted := newTestCityHandler(t, state)
	if response := postExternalCoordinationResponseBody(t, restarted, state, exactBody, "response-replay-1", registration); response.Code != http.StatusOK {
		t.Fatalf("exact replay after server restart status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	divergentBody := externalCoordinationResponseTestBody(claimed, "response-2", "denied", receivedAt)
	response := postExternalCoordinationResponseBody(t, restarted, state, divergentBody, "response-divergent-1", registration)
	if response.Code != http.StatusConflict {
		t.Fatalf("divergent POST /external-coordination/responses status = %d, want 409; body = %s", response.Code, response.Body.String())
	}
	stored, err := externalcoordination.NewService(state.CityBeadStore()).Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != externalcoordination.StateCompleted {
		t.Fatalf("stored request state after conflict = %q, want completed", stored.State)
	}
}

func TestExternalCoordinationResponseRouteRejectsReplacedExternalAdapterRegistration(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)
	first := registerExternalCoordinationResponseTestAdapter(t, h, state)
	second := registerExternalCoordinationResponseTestAdapter(t, h, state)
	if first.Credential == "" || second.Credential == "" || first.Credential == second.Credential || first.Generation >= second.Generation || first.Instance == second.Instance {
		t.Fatal("replacement did not issue a distinct callback credential, generation, and instance")
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

func TestExtMsgAdapterUnregisterRequiresCurrentRegistrationFence(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)
	first := registerExternalCoordinationResponseTestAdapter(t, h, state)
	second := registerExternalCoordinationResponseTestAdapter(t, h, state)

	missingFence := unregisterExternalCoordinationTestAdapter(t, h, state, coordinationCallbackRegistration{})
	if missingFence.Code != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE /extmsg/adapters without fence status = %d, want 422; body = %s", missingFence.Code, missingFence.Body.String())
	}
	stale := unregisterExternalCoordinationTestAdapter(t, h, state, first)
	if stale.Code != http.StatusConflict {
		t.Fatalf("DELETE /extmsg/adapters with stale fence status = %d, want 409; body = %s", stale.Code, stale.Body.String())
	}
	key := extmsg.AdapterKey{Provider: "hermes", AccountID: "desktop"}
	if got := state.AdapterRegistry().Lookup(key); got == nil {
		t.Fatal("stale unregister removed replacement registration")
	}

	current := unregisterExternalCoordinationTestAdapter(t, h, state, second)
	if current.Code != http.StatusOK {
		t.Fatalf("DELETE /extmsg/adapters with current fence status = %d, want 200; body = %s", current.Code, current.Body.String())
	}
	if got := state.AdapterRegistry().Lookup(key); got != nil {
		t.Fatalf("current unregister left adapter %T registered", got)
	}
}

func TestExtMsgAdapterListReturnsExactCurrentRegistrationIdentity(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)
	first := registerExternalCoordinationResponseTestAdapter(t, h, state)
	second := registerExternalCoordinationResponseTestAdapter(t, h, state)
	if first.Generation >= second.Generation || first.Instance == second.Instance {
		t.Fatal("replacement did not issue a distinct generation and instance")
	}

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/extmsg/adapters"), nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /extmsg/adapters status = %d, want 200; body = %s", response.Code, response.Body.String())
	}

	var listed struct {
		Items []struct {
			Provider   string `json:"provider"`
			AccountID  string `json:"account_id"`
			Name       string `json:"name"`
			Generation uint64 `json:"generation"`
			InstanceID string `json:"instance_id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&listed); err != nil {
		t.Fatalf("strict decode GET /extmsg/adapters: %v; body = %s", err, response.Body.String())
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		t.Fatalf("GET /extmsg/adapters trailing JSON decode error = %v, want EOF", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 {
		t.Fatalf("adapter list total/items = %d/%d, want 1/1", listed.Total, len(listed.Items))
	}
	got := listed.Items[0]
	if got.Provider != "hermes" || got.AccountID != "desktop" || got.Name != "hermes" || got.Generation != second.Generation || got.InstanceID != second.Instance {
		t.Fatalf("adapter list item = %+v, want current generation=%d instance_id=%q", got, second.Generation, second.Instance)
	}
}

func TestExtMsgAdapterRegisterRejectsUnsafeSecretBearingCallbackURLs(t *testing.T) {
	for _, callbackURL := range []string{
		"http://example.com/bridge",
		"https://user:password@example.com/bridge",
		"https://example.com/bridge?target=other",
		"https://example.com/bridge#fragment",
		"ftp://example.com/bridge",
	} {
		t.Run(callbackURL, func(t *testing.T) {
			state := newExternalCoordinationResponseTestState(t)
			h := newTestCityHandler(t, state)
			body := fmt.Sprintf(`{"provider":"hermes","account_id":"desktop","name":"hermes","callback_url":%q}`, callbackURL)
			req := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters"), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-GC-Request", "coordination-adapter-register")
			response := httptest.NewRecorder()

			h.ServeHTTP(response, req)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("POST /extmsg/adapters callback_url=%q status = %d, want 400; body = %s", callbackURL, response.Code, response.Body.String())
			}
			key := extmsg.AdapterKey{Provider: "hermes", AccountID: "desktop"}
			if got := state.AdapterRegistry().Lookup(key); got != nil {
				t.Fatalf("unsafe registration left adapter %T registered", got)
			}
		})
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

func unregisterExternalCoordinationTestAdapter(t *testing.T, h http.Handler, state State, registration coordinationCallbackRegistration) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"provider":"hermes","account_id":"desktop","generation":%d,"instance":%q}`, registration.Generation, registration.Instance)
	req := httptest.NewRequest(http.MethodDelete, cityURL(state, "/extmsg/adapters"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-adapter-unregister")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return response
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

func externalCoordinationResponseTestBody(claimed externalcoordination.RequestRecord, responseID, summary string, receivedAt time.Time) string {
	return fmt.Sprintf(`{"request_id":%q,"attempt":%d,"correlation_id":%q,"response_id":%q,"state":"answered","summary":%q,"follow_up_required":false,"received_at":%q}`, claimed.Request.RequestID, claimed.Request.Attempt, claimed.Request.CorrelationID, responseID, summary, receivedAt.UTC().Format(time.RFC3339Nano))
}

func postExternalCoordinationResponseBody(t *testing.T, h http.Handler, state State, body, idempotencyKey string, registration coordinationCallbackRegistration) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/external-coordination/responses"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-response-auth")
	req.Header.Set("Authorization", "Bearer "+registration.Credential)
	req.Header.Set("X-GC-Coordination-Adapter", "hermes")
	req.Header.Set("X-GC-Coordination-Adapter-Generation", fmt.Sprintf("%d", registration.Generation))
	req.Header.Set("X-GC-Coordination-Adapter-Instance", registration.Instance)
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return response
}

func postExternalCoordinationResponse(t *testing.T, h http.Handler, state State, claimed externalcoordination.RequestRecord, registration coordinationCallbackRegistration) *httptest.ResponseRecorder {
	t.Helper()
	body := externalCoordinationResponseTestBody(claimed, "response-1", "", time.Now())
	return postExternalCoordinationResponseBody(t, h, state, body, "", registration)
}
