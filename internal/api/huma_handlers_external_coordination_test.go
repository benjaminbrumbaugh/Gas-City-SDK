package api

import (
	"context"
	"encoding/json"
	"errors"
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

func TestExternalCoordinationResponseAuthenticatesDurableRequestTargetAfterConfigChange(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	registration := registerExternalCoordinationResponseTestAdapter(t, h, state)
	srv.waitForBackground()
	claimed := claimExternalCoordinationResponseTestRequest(t, state)

	// A hot reload may redirect future requests, but it must not transfer or
	// revoke the callback authority bound to an already-running durable request.
	state.cfg.ExternalCoordination.Adapter = "future-adapter"
	state.cfg.ExternalCoordination.Provider = "future-provider"
	state.cfg.ExternalCoordination.AccountID = "future-account"
	state.cfg.ExternalCoordination.ConversationID = "future-conversation"
	state.cfg.ExternalCoordination.ConfigRevision++

	response := postExternalCoordinationResponse(t, h, state, claimed, registration)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /external-coordination/responses after config change status = %d, want 200; body = %s", response.Code, response.Body.String())
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
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	registration := registerExternalCoordinationResponseTestAdapter(t, h, state)
	// Registering an adapter triggers a background drain. Let it finish
	// before claiming by hand, or the drain and the test race for the
	// same queued record and the claim below fails intermittently.
	srv.waitForBackground()
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

func TestExtMsgAdapterActivationRejectsWrongCredentialAndFence(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)
	registration := registerExternalCoordinationBridge(t, h, state, "http://127.0.0.1:9/callback")
	key := extmsg.AdapterKey{Provider: "hermes", AccountID: "desktop"}

	for name, mutation := range map[string]func(*coordinationCallbackRegistration){
		"wrong credential": func(candidate *coordinationCallbackRegistration) { candidate.Credential = "wrong" },
		"wrong generation": func(candidate *coordinationCallbackRegistration) { candidate.Generation++ },
		"wrong instance":   func(candidate *coordinationCallbackRegistration) { candidate.Instance = "wrong" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := registration
			mutation(&candidate)
			body := fmt.Sprintf(`{"provider":"hermes","account_id":"desktop","name":"hermes","generation":%d,"instance":%q}`, candidate.Generation, candidate.Instance)
			req := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters/activate"), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+candidate.Credential)
			req.Header.Set("X-GC-Request", "coordination-bridge-invalid-activate")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != http.StatusForbidden {
				t.Fatalf("invalid activation status = %d, want 403; body = %s", response.Code, response.Body.String())
			}
			if got := state.AdapterRegistry().Lookup(key); got != nil {
				t.Fatalf("invalid activation exposed adapter %T", got)
			}
		})
	}
	activateExternalCoordinationBridge(t, h, state, registration)
}

func TestExtMsgAdapterRegisterRejectsUnsafeSecretBearingCallbackURLs(t *testing.T) {
	for _, callbackURL := range []string{
		"",
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

func newExternalCoordinationResponseTestState(t *testing.T) *fakeState {
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
	activateExternalCoordinationBridge(t, h, state, registration)
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

// bridgeCallbackAdapter stands in for a registered external coordination
// bridge. It records what the city pushes so this handler-level test can assert
// the registration-triggered causal chain without opening another loopback
// listener. HTTP wire behavior remains owned by extmsg.HTTPAdapter tests.
type bridgeCallbackAdapter struct {
	name      string
	caps      extmsg.AdapterCapabilities
	mu        sync.Mutex
	published []extmsg.PublishRequest
}

func (b *bridgeCallbackAdapter) Name() string { return b.name }

func (b *bridgeCallbackAdapter) Capabilities() extmsg.AdapterCapabilities { return b.caps }

func (b *bridgeCallbackAdapter) VerifyAndNormalizeInbound(context.Context, extmsg.InboundPayload) (*extmsg.ExternalInboundMessage, error) {
	return nil, errors.New("inbound not supported by callback fixture")
}

func (b *bridgeCallbackAdapter) Publish(_ context.Context, request extmsg.PublishRequest) (*extmsg.PublishReceipt, error) {
	b.mu.Lock()
	b.published = append(b.published, request)
	b.mu.Unlock()
	return &extmsg.PublishReceipt{MessageID: "bridge-message-1", Delivered: true}, nil
}

func (b *bridgeCallbackAdapter) EnsureChildConversation(context.Context, extmsg.ConversationRef, string) (*extmsg.ConversationRef, error) {
	return nil, errors.New("child conversations not supported by callback fixture")
}

func (b *bridgeCallbackAdapter) deliveries() []extmsg.PublishRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]extmsg.PublishRequest(nil), b.published...)
}

func registerExternalCoordinationBridge(t *testing.T, h http.Handler, state State, callbackURL string) coordinationCallbackRegistration {
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
	var registration coordinationCallbackRegistration
	if err := json.Unmarshal(response.Body.Bytes(), &registration); err != nil {
		t.Fatal(err)
	}
	return registration
}

func activateExternalCoordinationBridge(t *testing.T, h http.Handler, state State, registration coordinationCallbackRegistration) {
	t.Helper()
	body := fmt.Sprintf(`{"provider":"hermes","account_id":"desktop","name":"hermes","generation":%d,"instance":%q}`, registration.Generation, registration.Instance)
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters/activate"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+registration.Credential)
	req.Header.Set("X-GC-Request", "coordination-bridge-activate")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /extmsg/adapters/activate status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
}

// TestExternalCoordinationDeliversQueuedRequestWhenAdapterRegistersAfterEnqueue
// is the regression this file exists for. Delivery used to be attempted exactly
// once, inline with the enqueue, so a request queued before its bridge
// registered had no later dispatch opportunity and sat at attempt 0 forever —
// which is precisely what stalled the first city-to-coordinator handoff.
// Activation is now itself a dispatch trigger. Registration remains pending
// until the bridge has received its response credential.
func TestExternalCoordinationDeliversQueuedRequestWhenAdapterRegistersAfterEnqueue(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	srv := New(state)
	h := newTestCityHandlerWith(t, state, srv)
	bridge := &bridgeCallbackAdapter{}
	srv.newHTTPAdapter = func(name, _ string, caps extmsg.AdapterCapabilities) extmsg.TransportAdapter {
		bridge.name = name
		bridge.caps = caps
		return bridge
	}

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

	registration := registerExternalCoordinationBridge(t, h, state, "http://127.0.0.1")
	srv.waitForBackground()
	if got := len(bridge.deliveries()); got != 0 {
		t.Fatalf("bridge saw %d callback(s) before credential-bound activation, want 0", got)
	}
	activateExternalCoordinationBridge(t, h, state, registration)
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
	registration = registerExternalCoordinationBridge(t, h, state, "http://127.0.0.1")
	activateExternalCoordinationBridge(t, h, state, registration)
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
	bridge := &bridgeCallbackAdapter{}
	srv.newHTTPAdapter = func(name, _ string, caps extmsg.AdapterCapabilities) extmsg.TransportAdapter {
		bridge.name = name
		bridge.caps = caps
		return bridge
	}

	body := `{"source_agent":"mayor","reason":"direct_request","prompt":"only hermes may answer","correlation_id":"corr-unrelated-adapter","idempotency_key":"idem-unrelated-adapter"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/external-coordination/requests"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-unrelated-adapter")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /external-coordination/requests status = %d, body = %s", response.Code, response.Body.String())
	}

	registerBody := fmt.Sprintf(`{"provider":"discord","account_id":"acct-1","name":"discord","callback_url":%q}`, "http://127.0.0.1")
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

// getExternalCoordinationCapability performs the pre-flight check an
// orchestrator is instructed to run before using external coordination.
func getExternalCoordinationCapability(t *testing.T, h http.Handler, state State) config.ExternalCoordinationCapability {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/external-coordination"), nil)
	req.Header.Set("X-GC-Request", "coordination-capability")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /external-coordination status = %d, body = %s", response.Code, response.Body.String())
	}
	var capability config.ExternalCoordinationCapability
	if err := json.Unmarshal(response.Body.Bytes(), &capability); err != nil {
		t.Fatal(err)
	}
	return capability
}

// TestExternalCoordinationCapabilityTracksLiveAdapterRegistration pins the
// signifier to what can actually deliver. Adapter registrations are in-memory
// and do not survive a controller restart, so [external_coordination] can stay
// enabled while the registry is empty. Reporting available=true in that window
// gave an orchestrator following its own pre-flight instructions a false green,
// and every request it enqueued sat undelivered until a bridge re-registered.
func TestExternalCoordinationCapabilityTracksLiveAdapterRegistration(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)

	capability := getExternalCoordinationCapability(t, h, state)
	if capability.Available || capability.Registered {
		t.Fatalf("capability with an empty adapter registry = %+v, want available=false registered=false", capability)
	}
	if !capability.Configured {
		t.Fatalf("capability.Configured = false with [external_coordination] enabled: %+v", capability)
	}

	registration := registerExternalCoordinationBridge(t, h, state, "http://127.0.0.1:9/callback")
	capability = getExternalCoordinationCapability(t, h, state)
	if capability.Available || capability.Registered {
		t.Fatalf("capability with pending adapter registration = %+v, want available=false registered=false", capability)
	}
	activateExternalCoordinationBridge(t, h, state, registration)
	capability = getExternalCoordinationCapability(t, h, state)
	if !capability.Available || !capability.Registered || !capability.Configured {
		t.Fatalf("capability after adapter registration = %+v, want available/registered/configured all true", capability)
	}

	if response := unregisterExternalCoordinationTestAdapter(t, h, state, registration); response.Code != http.StatusOK {
		t.Fatalf("DELETE /extmsg/adapters status = %d, want 200; body = %s", response.Code, response.Body.String())
	}

	capability = getExternalCoordinationCapability(t, h, state)
	if capability.Available || capability.Registered {
		t.Fatalf("capability after adapter unregistration = %+v, want available=false registered=false", capability)
	}
}

func TestExternalCoordinationCapabilityRejectsUnsupportedConfiguredSessionPolicy(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	state.cfg.ExternalCoordination.SessionPolicy = "new"
	h := newTestCityHandler(t, state)
	registration := registerExternalCoordinationBridge(t, h, state, "http://127.0.0.1:9/callback")
	activateExternalCoordinationBridge(t, h, state, registration)

	capability := getExternalCoordinationCapability(t, h, state)
	if capability.Available || capability.Registered {
		t.Fatalf("capability for unsupported session policy = %+v, want available=false registered=false", capability)
	}
}

func TestExternalCoordinationCapabilityRejectsWrongAdapterName(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)
	body := `{"provider":"hermes","account_id":"desktop","name":"not-hermes","callback_url":"http://127.0.0.1:9/callback"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-wrong-adapter-register")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /extmsg/adapters status = %d, want 201; body = %s", response.Code, response.Body.String())
	}
	var registration coordinationCallbackRegistration
	if err := json.Unmarshal(response.Body.Bytes(), &registration); err != nil {
		t.Fatal(err)
	}
	activateBody := fmt.Sprintf(`{"provider":"hermes","account_id":"desktop","name":"not-hermes","generation":%d,"instance":%q}`, registration.Generation, registration.Instance)
	activateReq := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters/activate"), strings.NewReader(activateBody))
	activateReq.Header.Set("Content-Type", "application/json")
	activateReq.Header.Set("Authorization", "Bearer "+registration.Credential)
	activateReq.Header.Set("X-GC-Request", "coordination-wrong-adapter-activate")
	activateResponse := httptest.NewRecorder()
	h.ServeHTTP(activateResponse, activateReq)
	if activateResponse.Code != http.StatusOK {
		t.Fatalf("POST /extmsg/adapters/activate status = %d, want 200; body = %s", activateResponse.Code, activateResponse.Body.String())
	}

	capability := getExternalCoordinationCapability(t, h, state)
	if capability.Available || capability.Registered {
		t.Fatalf("capability for wrong adapter name = %+v, want available=false registered=false", capability)
	}
}

// TestExternalCoordinationCapabilityIgnoresUnrelatedAdapterRegistration keeps
// the signifier scoped to the configured (provider, account_id). An unrelated
// chat adapter registering cannot carry a coordination request, so it must not
// flip the signifier green either.
func TestExternalCoordinationCapabilityIgnoresUnrelatedAdapterRegistration(t *testing.T) {
	state := newExternalCoordinationResponseTestState(t)
	h := newTestCityHandler(t, state)

	body := `{"provider":"discord","account_id":"acct-1","name":"discord","callback_url":"http://127.0.0.1:9/callback"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/extmsg/adapters"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "coordination-capability-unrelated-register")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /extmsg/adapters status = %d, want 201; body = %s", response.Code, response.Body.String())
	}

	capability := getExternalCoordinationCapability(t, h, state)
	if capability.Available || capability.Registered {
		t.Fatalf("capability with only an unrelated adapter registered = %+v, want available=false registered=false", capability)
	}
}

// TestExternalCoordinationCapabilityReportsUnconfiguredCityAsUnavailable keeps
// the two negative cases distinguishable: a city with no [external_coordination]
// table at all is not merely unreachable, it is unconfigured.
func TestExternalCoordinationCapabilityReportsUnconfiguredCityAsUnavailable(t *testing.T) {
	state := newFakeState(t)
	state.adapterReg = extmsg.NewAdapterRegistry()
	h := newTestCityHandler(t, state)

	capability := getExternalCoordinationCapability(t, h, state)
	if capability.Available || capability.Registered || capability.Configured {
		t.Fatalf("capability for an unconfigured city = %+v, want available/registered/configured all false", capability)
	}
}

func postExternalCoordinationResponse(t *testing.T, h http.Handler, state State, claimed externalcoordination.RequestRecord, registration coordinationCallbackRegistration) *httptest.ResponseRecorder {
	t.Helper()
	body := externalCoordinationResponseTestBody(claimed, "response-1", "", time.Now())
	return postExternalCoordinationResponseBody(t, h, state, body, "", registration)
}
