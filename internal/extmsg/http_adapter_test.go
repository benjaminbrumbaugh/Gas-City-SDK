package extmsg

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newHTTPAdapterTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// TestHTTPAdapterPublishSetsCSRFHeader pins that HTTPAdapter.Publish sets
// X-GC-Request on the outbound request. When an adapter registers a
// callback URL pointing at gc's own /svc/<service>/publish proxy
// (proxy_process mode — the standard slack-pack registration shape), gc's
// CSRF gate at internal/api/handler_services.go:serviceRequestAllowed
// requires the header on private-service-proxy mutations and 403s the
// request without it. Without the header, every Publish call silently
// returns FailureKind=auth and the message never reaches the actual
// adapter. See gastownhall/gascity#1817.
func TestHTTPAdapterPublishSetsCSRFHeader(t *testing.T) {
	t.Parallel()

	// Pass observations from the handler goroutine to the test
	// goroutine via a buffered channel — receiving on the channel
	// happens-before the test's assertions, satisfying the Go memory
	// model. A bare shared variable would race.
	gotHeader := make(chan string, 1)
	server := newHTTPAdapterTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/publish") {
			t.Errorf("expected /publish suffix, got %s", r.URL.Path)
		}
		gotHeader <- r.Header.Get(csrfHeaderName)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wirePublishReceipt{
			MessageID: "ts-100",
			Conversation: ConversationRef{
				Provider:       "slack",
				ConversationID: "C123",
				Kind:           ConversationRoom,
			},
			Delivered: true,
		})
	}))

	adapter := NewHTTPAdapter("test", server.URL, AdapterCapabilities{})
	receipt, err := adapter.Publish(context.Background(), PublishRequest{
		Conversation: ConversationRef{
			Provider:       "slack",
			ConversationID: "C123",
			Kind:           ConversationRoom,
		},
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !receipt.Delivered {
		t.Fatalf("expected Delivered=true, got receipt=%+v", receipt)
	}
	select {
	case h := <-gotHeader:
		if h != "true" {
			t.Fatalf("expected %s=%q on outbound request, got %q. "+
				"This header is required by gc's /svc-proxy CSRF gate when the "+
				"adapter callback URL points at a gc-internal proxy.",
				csrfHeaderName, "true", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server handler was never invoked; request did not reach the callback URL")
	}
}

// TestHTTPAdapterPublishParsesQueuedAcceptanceSeparatelyFromDelivery pins
// queue admission as distinct from execution completion.
func TestHTTPAdapterPublishParsesQueuedAcceptanceSeparatelyFromDelivery(t *testing.T) {
	t.Parallel()

	server := newHTTPAdapterTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accepted":true,"queued":true,"delivered":false,"message_id":"queue-1"}`)
	}))

	adapter := NewHTTPAdapter("hermes", server.URL, AdapterCapabilities{})
	receipt, err := adapter.Publish(context.Background(), PublishRequest{
		Conversation: ConversationRef{Provider: "hermes", ConversationID: "conversation-1", Kind: ConversationDM},
		Text:         "hello",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !receipt.Accepted || !receipt.Queued || receipt.Delivered {
		t.Fatalf("receipt = %+v, want accepted queued but not delivered", receipt)
	}
	if receipt.MessageID != "queue-1" {
		t.Fatalf("MessageID = %q, want queue-1", receipt.MessageID)
	}
}

func TestAdapterRegistryCredentialAuthenticatesHTTPCallbacks(t *testing.T) {
	t.Parallel()

	gotAuthorization := make(chan string, 2)
	server := newHTTPAdapterTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/child-conversation") {
			_ = json.NewEncoder(w).Encode(ConversationRef{Provider: "hermes", ConversationID: "child-1", Kind: ConversationThread})
			return
		}
		_ = json.NewEncoder(w).Encode(wirePublishReceipt{Accepted: true, Delivered: true})
	}))

	adapter := NewHTTPAdapter("hermes", server.URL, AdapterCapabilities{})
	registration := NewAdapterRegistry().Register(AdapterKey{Provider: "hermes", AccountID: "desktop"}, adapter)
	request := PublishRequest{Conversation: ConversationRef{Provider: "hermes", ConversationID: "conversation-1", Kind: ConversationDM}, Text: "hello"}
	if _, err := adapter.Publish(context.Background(), request); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := adapter.EnsureChildConversation(context.Background(), request.Conversation, "child"); err != nil {
		t.Fatalf("EnsureChildConversation: %v", err)
	}

	want := "Bearer " + registration.Credential
	for range 2 {
		select {
		case got := <-gotAuthorization:
			if got != want {
				t.Fatal("callback did not receive the registration bearer")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("callback request was not observed")
		}
	}
}

func TestHTTPAdapterDoesNotForwardRegistrationCredentialAcrossRedirect(t *testing.T) {
	t.Parallel()

	destinationAuthorization := make(chan string, 1)
	destination := newHTTPAdapterTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationAuthorization <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wirePublishReceipt{Accepted: true, Delivered: true})
	}))

	redirect := newHTTPAdapterTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL+"/publish")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte(`{"accepted":true,"queued":true,"delivered":false}`))
	}))

	adapter := NewHTTPAdapter("hermes", redirect.URL, AdapterCapabilities{})
	NewAdapterRegistry().Register(AdapterKey{Provider: "hermes", AccountID: "desktop"}, adapter)
	receipt, err := adapter.Publish(context.Background(), PublishRequest{
		Conversation: ConversationRef{Provider: "hermes", ConversationID: "conversation-1", Kind: ConversationDM},
		Text:         "hello",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if receipt.Delivered || receipt.FailureKind != PublishFailureTransient {
		t.Fatalf("receipt = %+v, want transient redirect refusal", receipt)
	}
	select {
	case authorization := <-destinationAuthorization:
		t.Fatalf("redirect destination received callback authorization (present=%t)", authorization != "")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAdapterRegistryRotationReplacesHTTPCallbackBearer(t *testing.T) {
	t.Parallel()

	gotAuthorization := make(chan string, 2)
	server := newHTTPAdapterTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wirePublishReceipt{Accepted: true, Delivered: true})
	}))

	adapter := NewHTTPAdapter("hermes", server.URL, AdapterCapabilities{})
	registry := NewAdapterRegistry()
	key := AdapterKey{Provider: "hermes", AccountID: "desktop"}
	first := registry.Register(key, adapter)
	request := PublishRequest{
		Conversation: ConversationRef{Provider: "hermes", ConversationID: "conversation-1", Kind: ConversationDM},
		Text:         "hello",
	}
	if _, err := adapter.Publish(context.Background(), request); err != nil {
		t.Fatalf("Publish with first registration: %v", err)
	}
	second := registry.Register(key, adapter)
	if _, err := adapter.Publish(context.Background(), request); err != nil {
		t.Fatalf("Publish with replacement registration: %v", err)
	}
	if first.Credential == second.Credential {
		t.Fatal("replacement registration reused the previous credential")
	}
	for _, want := range []string{"Bearer " + first.Credential, "Bearer " + second.Credential} {
		select {
		case got := <-gotAuthorization:
			if got != want {
				t.Fatal("callback did not receive the credential for its registration generation")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("callback request was not observed")
		}
	}
}

// TestHTTPAdapterEnsureChildConversationSetsCSRFHeader pins the same
// header on the sibling /child-conversation callback. Same /svc-proxy
// CSRF reasoning applies — without the header, child-conversation
// requests against a /svc-proxy callback URL also 403 silently.
func TestHTTPAdapterEnsureChildConversationSetsCSRFHeader(t *testing.T) {
	t.Parallel()

	gotHeader := make(chan string, 1)
	server := newHTTPAdapterTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/child-conversation") {
			t.Errorf("expected /child-conversation suffix, got %s", r.URL.Path)
		}
		gotHeader <- r.Header.Get(csrfHeaderName)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ConversationRef{
			Provider:             "slack",
			ConversationID:       "C123-thread",
			Kind:                 ConversationThread,
			ParentConversationID: "C123",
		})
	}))

	adapter := NewHTTPAdapter("test", server.URL, AdapterCapabilities{})
	parent := ConversationRef{
		Provider:       "slack",
		ConversationID: "C123",
		Kind:           ConversationRoom,
	}
	if _, err := adapter.EnsureChildConversation(context.Background(), parent, "test-label"); err != nil {
		t.Fatalf("EnsureChildConversation: %v", err)
	}
	select {
	case h := <-gotHeader:
		if h != "true" {
			t.Fatalf("expected %s=%q on outbound child-conversation request, got %q.",
				csrfHeaderName, "true", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server handler was never invoked; request did not reach the callback URL")
	}
}
