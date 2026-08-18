package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

func TestHCARequestRouteQueuesEphemeralRequestWithoutTouchingMail(t *testing.T) {
	state := newFakeState(t)
	state.cityBeadStore = beads.NewMemStore()
	state.cfg.HumanCoordinator = &config.HumanCoordinatorConfig{
		Enabled:         true,
		Target:          "hermes-desktop",
		Adapter:         "hermes",
		Provider:        "hermes",
		AccountID:       "desktop",
		ConversationID:  "hca-smoke",
		Delivery:        "queued",
		InterruptPolicy: "never",
		SessionPolicy:   "resume_or_create",
		ConfigRevision:  1,
	}
	h := newTestCityHandler(t, state)

	body := `{"source_agent":"mayor","reason":"direct_request","prompt":"one-off smoke question","idempotency_key":"hca-smoke-1"}`
	req := httptest.NewRequest(http.MethodPost, cityURL(state, "/hca/requests"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "hca-smoke")
	req.Header.Set("Idempotency-Key", "hca-smoke-1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /hca/requests status = %d, body = %s", response.Code, response.Body.String())
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
