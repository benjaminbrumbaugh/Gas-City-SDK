package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	registration := registerExternalCoordinationResponseTestAdapter(t, h, state)
	// Registering an adapter triggers a background drain. Let it finish
	// before claiming by hand, or the drain and the test race for the
	// same queued record and the claim below fails intermittently.
	srv.waitForBackground()
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

func TestExternalCoordinationResponseRouteRejectsOutcomeAfterCompletionAsConflict(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	registration := registerExternalCoordinationResponseTestAdapter(t, h, state)
	// Registering an adapter triggers a background drain. Let it finish
	// before claiming by hand, or the drain and the test race for the
	// same queued record and the claim below fails intermittently.
	srv.waitForBackground()
	claimed := claimExternalCoordinationResponseTestRequest(t, state)
	if response := postExternalCoordinationResponse(t, h, state, claimed, registration); response.Code != http.StatusOK {
		t.Fatalf("first POST /external-coordination/responses status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	response := postExternalCoordinationResponse(t, h, state, claimed, registration)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate POST /external-coordination/responses status = %d, want 409; body = %s", response.Code, response.Body.String())
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
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	first := registerExternalCoordinationResponseTestAdapter(t, h, state)
	second := registerExternalCoordinationResponseTestAdapter(t, h, state)
	// Registering an adapter triggers a background drain. Let it finish
	// before claiming by hand, or the drain and the test race for the
	// same queued record and the claim below fails intermittently.
	srv.waitForBackground()
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

// bridgeCallbackServer stands in for a registered external coordination bridge.
// It accepts the publish callback the city pushes to it and records what it
// saw, so a test can assert that delivery was initiated BY THE CITY through the
// registered callback rather than pulled by the coordinator.
type bridgeCallbackServer struct {
	*httptest.Server
	mu        sync.Mutex
	published []extmsg.PublishRequest
}

func newBridgeCallbackServer(t *testing.T) *bridgeCallbackServer {
	t.Helper()
	bridge := &bridgeCallbackServer{}
	bridge.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/publish" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request extmsg.PublishRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		bridge.mu.Lock()
		bridge.published = append(bridge.published, request)
		bridge.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message_id":"bridge-message-1","delivered":true}`))
	}))
	t.Cleanup(bridge.Close)
	return bridge
}

func (b *bridgeCallbackServer) deliveries() []extmsg.PublishRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]extmsg.PublishRequest(nil), b.published...)
}

func registerExternalCoordinationBridge(t *testing.T, h http.Handler, state State, callbackURL string) {
	t.Helper()
	body := fmt.Sprintf(`{"provider":"hermes","account_id":"desktop","name":"hermes","callback_url":%q}`, callbackURL)
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-bridge-register")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /extmsg/adapters status = %d, want 201; body = %s", response.Code, response.Body.String())
	}
}

// TestExternalCoordinationDeliversQueuedRequestWhenAdapterRegistersAfterEnqueue
// is the regression this file exists for. Delivery used to be attempted exactly
// once, inline with the enqueue, so a request queued before its bridge
// registered had no later dispatch opportunity and sat at attempt 0 forever —
// which is precisely what stalled the first city-to-coordinator handoff.
// Registration is now itself a dispatch trigger.
func TestExternalCoordinationDeliversQueuedRequestWhenAdapterRegistersAfterEnqueue(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	bridge := newBridgeCallbackServer(t)

	body := `{"source_agent":"mayor","reason":"direct_request","prompt":"is the bridge reachable?","correlation_id":"corr-register-after-enqueue","idempotency_key":"idem-register-after-enqueue"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/external-coordination/requests"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-register-after-enqueue")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /external-coordination/requests status = %d, body = %s", response.Code, response.Body.String())
	}
	srv.waitForBackground()

	var enqueued struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &enqueued); err != nil {
		t.Fatal(err)
	}
	if enqueued.ID == "" || enqueued.State != "queued" {
		t.Fatalf("enqueued record = %+v, want a queued record", enqueued)
	}
	if got := len(bridge.deliveries()); got != 0 {
		t.Fatalf("bridge saw %d callback(s) before it registered, want 0", got)
	}

	service := externalcoordination.NewService(state.CityBeadStore())
	queued, err := service.Get(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != externalcoordination.StateQueued || queued.Attempt != 0 {
		t.Fatalf("record before registration = state %q attempt %d, want queued/0", queued.State, queued.Attempt)
	}

	registerExternalCoordinationBridge(t, h, state, bridge.URL)
	srv.waitForBackground()

	delivered := bridge.deliveries()
	if len(delivered) != 1 {
		t.Fatalf("bridge saw %d callback(s) after registering, want exactly 1", len(delivered))
	}
	if delivered[0].Metadata["coordination_request_id"] != queued.Request.RequestID {
		t.Fatalf("callback coordination_request_id = %q, want %q", delivered[0].Metadata["coordination_request_id"], queued.Request.RequestID)
	}
	if delivered[0].Metadata["correlation_id"] != "corr-register-after-enqueue" {
		t.Fatalf("callback correlation_id = %q, want corr-register-after-enqueue", delivered[0].Metadata["correlation_id"])
	}
	if delivered[0].IdempotencyKey != "idem-register-after-enqueue" {
		t.Fatalf("callback idempotency_key = %q, want idem-register-after-enqueue", delivered[0].IdempotencyKey)
	}

	stored, err := service.Get(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != externalcoordination.StateRunning {
		t.Fatalf("record after registration state = %q, want running", stored.State)
	}
	if stored.Attempt != 1 {
		t.Fatalf("record after registration attempt = %d, want 1", stored.Attempt)
	}
	if stored.DeliveredAt.IsZero() {
		t.Fatal("record after registration has no delivered_at; delivery was not recorded on the causal record")
	}

	// A second registration re-triggers the drain. The delivered request is no
	// longer queued, so the coordinator must not be asked to take a second turn.
	registerExternalCoordinationBridge(t, h, state, bridge.URL)
	srv.waitForBackground()
	if got := len(bridge.deliveries()); got != 1 {
		t.Fatalf("bridge saw %d callback(s) after re-registering, want the original 1", got)
	}
}

// TestExternalCoordinationDrainLeavesQueueIntactWhenNoAdapterIsRegistered pins
// the other half of the contract: adapter absence must never be turned into a
// false success, and the causal record must survive it.
func TestExternalCoordinationDrainLeavesQueueIntactWhenNoAdapterIsRegistered(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)

	body := `{"source_agent":"mayor","reason":"escalation","prompt":"nobody is listening yet","correlation_id":"corr-no-adapter","idempotency_key":"idem-no-adapter"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/external-coordination/requests"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-no-adapter")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /external-coordination/requests status = %d, body = %s", response.Code, response.Body.String())
	}
	srv.waitForBackground()

	var enqueued struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &enqueued); err != nil {
		t.Fatal(err)
	}
	stored, err := externalcoordination.NewService(state.CityBeadStore()).Get(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("causal record was lost while no adapter was registered: %v", err)
	}
	if stored.State != externalcoordination.StateQueued || stored.Attempt != 0 {
		t.Fatalf("record with no adapter = state %q attempt %d, want queued/0", stored.State, stored.Attempt)
	}
}

// TestExternalCoordinationRegistrationOfAnUnrelatedAdapterDoesNotDeliver keeps
// the registration trigger scoped to the configured coordination target, so an
// unrelated chat adapter registering cannot hand a coordination request to a
// bridge that was never selected for it.
func TestExternalCoordinationRegistrationOfAnUnrelatedAdapterDoesNotDeliver(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	bridge := newBridgeCallbackServer(t)

	body := `{"source_agent":"mayor","reason":"direct_request","prompt":"only hermes may answer","correlation_id":"corr-unrelated-adapter","idempotency_key":"idem-unrelated-adapter"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/external-coordination/requests"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-unrelated-adapter")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /external-coordination/requests status = %d, body = %s", response.Code, response.Body.String())
	}

	registerBody := fmt.Sprintf(`{"provider":"discord","account_id":"acct-1","name":"discord","callback_url":%q}`, bridge.URL)
	registerReq := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters"), strings.NewReader(registerBody))
	registerReq.Header.Set("Content-Type", "application/json")
	registerReq.Header.Set("X-GC-Request", "coordination-unrelated-adapter-register")
	registerResponse := httptest.NewRecorder()
	h.ServeHTTP(registerResponse, registerReq)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("POST /extmsg/adapters status = %d, want 201; body = %s", registerResponse.Code, registerResponse.Body.String())
	}
	srv.waitForBackground()

	if got := len(bridge.deliveries()); got != 0 {
		t.Fatalf("unrelated adapter received %d coordination callback(s), want 0", got)
	}
}
