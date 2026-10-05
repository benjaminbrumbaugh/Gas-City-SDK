package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/extmsg"
	"github.com/spf13/cobra"
)

func TestExtMsgReplyBodyFileAndJSONThroughRoot(t *testing.T) {
	var gotBody struct {
		Conversation extmsg.ConversationRef `json:"conversation"`
		SessionID    string                 `json:"session_id"`
		Text         string                 `json:"text"`
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v0/city/alpha/extmsg/outbound" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode reply body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(extmsg.OutboundResult{Receipt: extmsg.PublishReceipt{
			MessageID:    "queued-1",
			Conversation: gotBody.Conversation,
			Accepted:     true,
			Queued:       true,
			Delivered:    false,
		}})
	}))
	defer ts.Close()

	old := extMsgCommandClientHook
	extMsgCommandClientHook = func(string, io.Writer) (*api.Client, string, bool) {
		return api.NewCityScopedClient(ts.URL, "alpha"), "/tmp/city", true
	}
	t.Cleanup(func() { extMsgCommandClientHook = old })

	bodyPath := filepath.Join(t.TempDir(), "reply.txt")
	if err := os.WriteFile(bodyPath, []byte("body-file reply\n"), 0o600); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root := newRootCmd(&stdout, &stderr)
	root.SetArgs([]string{
		"extmsg", "reply",
		"--scope-id", "alpha",
		"--provider", "telegram",
		"--conversation-id", "conversation-1",
		"--session", "sess-1",
		"--body-file", bodyPath,
		"--json",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("root.Execute: %v", err)
	}
	if gotBody.SessionID != "sess-1" || gotBody.Text != "body-file reply\n" {
		t.Fatalf("reply body = %+v, want explicit session and body-file text", gotBody)
	}
	if gotBody.Conversation.Provider != "telegram" || gotBody.Conversation.ConversationID != "conversation-1" {
		t.Fatalf("reply conversation = %+v, want explicit conversation", gotBody.Conversation)
	}
	if !strings.Contains(stdout.String(), `"Queued":true`) || !strings.Contains(stdout.String(), `"Delivered":false`) {
		t.Fatalf("JSON output = %q, want queued/delivered distinction", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestExtMsgReplyCurrentConversationRejectsAmbiguity(t *testing.T) {
	postCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/city/alpha/extmsg/bindings":
			if r.URL.Query().Get("session_id") != "sess-2" {
				t.Fatalf("session_id = %q, want sess-2", r.URL.Query().Get("session_id"))
			}
			ref := func(id string) extmsg.SessionBindingRecord {
				return extmsg.SessionBindingRecord{ID: "binding-" + id, SessionID: "sess-2", Status: extmsg.BindingActive, Conversation: extmsg.ConversationRef{
					ScopeID: "alpha", Provider: "telegram", AccountID: "default", ConversationID: id, Kind: extmsg.ConversationDM,
				}}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Items []extmsg.SessionBindingRecord `json:"items"`
			}{Items: []extmsg.SessionBindingRecord{ref("one"), ref("two")}})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/city/alpha/extmsg/outbound":
			postCalls++
			t.Fatalf("outbound called despite ambiguous current conversation")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	old := extMsgCommandClientHook
	extMsgCommandClientHook = func(string, io.Writer) (*api.Client, string, bool) {
		return api.NewCityScopedClient(ts.URL, "alpha"), "/tmp/city", true
	}
	t.Cleanup(func() { extMsgCommandClientHook = old })

	var stdout, stderr bytes.Buffer
	root := newRootCmd(&stdout, &stderr)
	root.SetArgs([]string{
		"extmsg", "reply", "--session", "sess-2", "--body", "do not guess",
	})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("root.Execute error = %v, want ambiguous current conversation", err)
	}
	if postCalls != 0 {
		t.Fatalf("outbound calls = %d, want 0", postCalls)
	}
}

func TestExtMsgReplyRequiresSessionAttribution(t *testing.T) {
	old := extMsgCommandClientHook
	extMsgCommandClientHook = func(string, io.Writer) (*api.Client, string, bool) {
		return api.NewCityScopedClient("http://127.0.0.1:1", "alpha"), "/tmp/city", true
	}
	t.Cleanup(func() { extMsgCommandClientHook = old })

	var stdout, stderr bytes.Buffer
	root := newRootCmd(&stdout, &stderr)
	root.SetArgs([]string{
		"extmsg", "reply",
		"--scope-id", "alpha", "--provider", "telegram", "--conversation-id", "c1",
		"--body", "attributed reply",
	})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--session") {
		t.Fatalf("root.Execute error = %v, want explicit session attribution error", err)
	}
}

func TestExtMsgTranscriptReadAndAckThroughRoot(t *testing.T) {
	ref := extmsg.ConversationRef{ScopeID: "alpha", Provider: "telegram", AccountID: "default", ConversationID: "conversation-4", Kind: extmsg.ConversationDM}
	var gotAck struct {
		Conversation extmsg.ConversationRef `json:"conversation"`
		SessionID    string                 `json:"session_id"`
		Sequence     int64                  `json:"sequence"`
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/city/alpha/extmsg/transcript":
			if r.URL.Query().Get("conversation_id") != ref.ConversationID || r.URL.Query().Get("provider") != ref.Provider {
				t.Fatalf("transcript query = %s, want scoped conversation", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Items []extmsg.ConversationTranscriptRecord `json:"items"`
			}{Items: []extmsg.ConversationTranscriptRecord{{
				ID: "entry-4", Conversation: ref, Sequence: 4, Kind: extmsg.TranscriptMessageInbound, Text: "history",
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/city/alpha/extmsg/transcript/ack":
			if err := json.NewDecoder(r.Body).Decode(&gotAck); err != nil {
				t.Fatalf("decode transcript ack: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	old := extMsgCommandClientHook
	extMsgCommandClientHook = func(string, io.Writer) (*api.Client, string, bool) {
		return api.NewCityScopedClient(ts.URL, "alpha"), "/tmp/city", true
	}
	t.Cleanup(func() { extMsgCommandClientHook = old })

	var readOut, readErr bytes.Buffer
	readRoot := newRootCmd(&readOut, &readErr)
	readRoot.SetArgs([]string{"extmsg", "transcript", "--scope-id", "alpha", "--provider", "telegram", "--conversation-id", "conversation-4", "--json"})
	if err := readRoot.Execute(); err != nil {
		t.Fatalf("transcript read: %v", err)
	}
	if !strings.Contains(readOut.String(), `"Sequence":4`) || !strings.Contains(readOut.String(), `"Text":"history"`) {
		t.Fatalf("transcript JSON = %q, want entry", readOut.String())
	}

	var ackOut, ackErr bytes.Buffer
	ackRoot := newRootCmd(&ackOut, &ackErr)
	ackRoot.SetArgs([]string{"extmsg", "transcript", "ack", "--scope-id", "alpha", "--provider", "telegram", "--conversation-id", "conversation-4", "--session", "sess-4", "--sequence", "4", "--json"})
	if err := ackRoot.Execute(); err != nil {
		t.Fatalf("transcript ack: %v", err)
	}
	if gotAck.Conversation != ref || gotAck.SessionID != "sess-4" || gotAck.Sequence != 4 {
		t.Fatalf("ack body = %+v, want scoped acknowledgement", gotAck)
	}
	if !strings.Contains(ackOut.String(), `"acknowledged":true`) {
		t.Fatalf("ack JSON = %q, want acknowledged result", ackOut.String())
	}
}

func TestExtMsgConversationCommandsAreRegistered(t *testing.T) {
	cmd := newExtMsgCmd(io.Discard, io.Discard)
	for _, name := range []string{"reply", "transcript"} {
		if commands := cmd.Commands(); !containsCommand(commands, name) {
			t.Fatalf("extmsg commands = %v, want %q", commandNames(commands), name)
		}
	}
}

func containsCommand(commands []*cobra.Command, name string) bool {
	for _, command := range commands {
		if command.Name() == name {
			return true
		}
	}
	return false
}

func commandNames(commands []*cobra.Command) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name())
	}
	return names
}
