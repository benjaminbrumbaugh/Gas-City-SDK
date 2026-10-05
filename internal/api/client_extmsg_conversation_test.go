package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/extmsg"
)

func TestClientSendExtMsgPreservesReceiptState(t *testing.T) {
	var gotBody struct {
		Conversation     extmsg.ConversationRef `json:"conversation"`
		IdempotencyKey   string                 `json:"idempotency_key"`
		ReplyToMessageID string                 `json:"reply_to_message_id"`
		SessionID        string                 `json:"session_id"`
		Text             string                 `json:"text"`
	}
	var gotHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v0/city/alpha/extmsg/outbound" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		gotHeader = r.Header.Get("X-GC-Request")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode outbound body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(extmsg.OutboundResult{
			Receipt: extmsg.PublishReceipt{
				MessageID:    "queued-1",
				Conversation: gotBody.Conversation,
				Accepted:     true,
				Queued:       true,
				Delivered:    false,
			},
		})
	}))
	defer ts.Close()

	ref := extmsg.ConversationRef{
		ScopeID:        "alpha",
		Provider:       "telegram",
		AccountID:      "default",
		ConversationID: "7113355",
		Kind:           extmsg.ConversationDM,
	}
	result, err := NewCityScopedClient(ts.URL, "alpha").SendExtMsg(ExtMsgOutboundSpec{
		Conversation:     ref,
		SessionID:        "sess-1",
		Text:             "hello from cli",
		ReplyToMessageID: "in-1",
		IdempotencyKey:   "idem-1",
	})
	if err != nil {
		t.Fatalf("SendExtMsg: %v", err)
	}
	if gotHeader != "true" {
		t.Fatalf("X-GC-Request = %q, want true", gotHeader)
	}
	if gotBody.Conversation != ref || gotBody.SessionID != "sess-1" || gotBody.Text != "hello from cli" || gotBody.ReplyToMessageID != "in-1" || gotBody.IdempotencyKey != "idem-1" {
		t.Fatalf("outbound body = %+v, want attributed request", gotBody)
	}
	if result.Receipt.MessageID != "queued-1" || !result.Receipt.Accepted || !result.Receipt.Queued || result.Receipt.Delivered {
		t.Fatalf("receipt = %+v, want accepted queued and not delivered", result.Receipt)
	}
}

func TestClientListExtMsgTranscriptScopesQuery(t *testing.T) {
	ref := extmsg.ConversationRef{
		ScopeID:              "alpha",
		Provider:             "discord",
		AccountID:            "work",
		ConversationID:       "room-7",
		ParentConversationID: "thread-2",
		Kind:                 extmsg.ConversationThread,
	}
	wantRecord := extmsg.ConversationTranscriptRecord{
		ID:           "entry-2",
		Conversation: ref,
		Sequence:     2,
		Kind:         extmsg.TranscriptMessageInbound,
		Text:         "history",
		CreatedAt:    time.Unix(10, 0).UTC(),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v0/city/alpha/extmsg/transcript" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		q := r.URL.Query()
		for key, want := range map[string]string{
			"scope_id": "alpha", "provider": "discord", "account_id": "work",
			"conversation_id": "room-7", "parent_conversation_id": "thread-2",
			"kind": "thread", "after_sequence": "1", "limit": "25", "order": "desc",
		} {
			if q.Get(key) != want {
				t.Errorf("query %s = %q, want %q", key, q.Get(key), want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Items []extmsg.ConversationTranscriptRecord `json:"items"`
			Total int64                                 `json:"total"`
		}{Items: []extmsg.ConversationTranscriptRecord{wantRecord}, Total: 1})
	}))
	defer ts.Close()

	entries, err := NewCityScopedClient(ts.URL, "alpha").ListExtMsgTranscript(ExtMsgTranscriptSpec{
		Conversation:  ref,
		AfterSequence: 1,
		Limit:         25,
		Order:         extmsg.TranscriptOrderDesc,
	})
	if err != nil {
		t.Fatalf("ListExtMsgTranscript: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != wantRecord.ID || entries[0].Conversation != ref || entries[0].Text != wantRecord.Text {
		t.Fatalf("entries = %+v, want scoped transcript entry", entries)
	}
}

func TestClientAckExtMsgTranscriptSendsSessionAndSequence(t *testing.T) {
	var gotBody struct {
		Conversation extmsg.ConversationRef `json:"conversation"`
		SessionID    string                 `json:"session_id"`
		Sequence     int64                  `json:"sequence"`
	}
	var gotHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v0/city/alpha/extmsg/transcript/ack" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		gotHeader = r.Header.Get("X-GC-Request")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode ack body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	ref := extmsg.ConversationRef{ScopeID: "alpha", Provider: "telegram", AccountID: "default", ConversationID: "7113355", Kind: extmsg.ConversationDM}
	if err := NewCityScopedClient(ts.URL, "alpha").AckExtMsgTranscript(ExtMsgTranscriptAckSpec{Conversation: ref, SessionID: "sess-2", Sequence: 9}); err != nil {
		t.Fatalf("AckExtMsgTranscript: %v", err)
	}
	if gotHeader != "true" {
		t.Fatalf("X-GC-Request = %q, want true", gotHeader)
	}
	if gotBody.Conversation != ref || gotBody.SessionID != "sess-2" || gotBody.Sequence != 9 {
		t.Fatalf("ack body = %+v, want scoped acknowledgement", gotBody)
	}
}

func TestClientListExtMsgBindingsScopesSession(t *testing.T) {
	ref := extmsg.ConversationRef{ScopeID: "alpha", Provider: "telegram", AccountID: "default", ConversationID: "7113355", Kind: extmsg.ConversationDM}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v0/city/alpha/extmsg/bindings" || r.URL.Query().Get("session_id") != "sess-3" {
			t.Fatalf("unexpected binding request %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Items []extmsg.SessionBindingRecord `json:"items"`
		}{Items: []extmsg.SessionBindingRecord{{ID: "binding-3", Conversation: ref, SessionID: "sess-3", Status: extmsg.BindingActive}}})
	}))
	defer ts.Close()

	bindings, err := NewCityScopedClient(ts.URL, "alpha").ListExtMsgBindings("sess-3")
	if err != nil {
		t.Fatalf("ListExtMsgBindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].ID != "binding-3" || bindings[0].Conversation != ref {
		t.Fatalf("bindings = %+v, want scoped binding", bindings)
	}
}

func TestClientTranscriptOrderRejectsInvalidValue(t *testing.T) {
	ref := extmsg.ConversationRef{ScopeID: "alpha", Provider: "telegram", AccountID: "default", ConversationID: "7113355", Kind: extmsg.ConversationDM}
	c := NewCityScopedClient("http://127.0.0.1:1", "alpha")
	_, err := c.ListExtMsgTranscript(ExtMsgTranscriptSpec{Conversation: ref, Order: extmsg.TranscriptOrder("sideways")})
	if err == nil || !strings.Contains(err.Error(), "invalid transcript order") {
		t.Fatalf("ListExtMsgTranscript invalid order error = %v, want validation failure", err)
	}
}
