package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
	"github.com/gastownhall/gascity/internal/externalcoordination"
)

func TestConvoyCloseAPIOwnershipQueuesLifecycleCallback(t *testing.T) {
	state := newFakeMutatorState(t)
	state.cfg.ExternalCoordination = &config.ExternalCoordinationConfig{
		Enabled:        true,
		Target:         "configured-coordinator",
		Adapter:        "bridge",
		ConfigRevision: 11,
	}
	store := state.stores["myrig"]
	convoy, err := store.Create(beads.Bead{
		Title: "api closure convoy",
		Type:  "convoy",
		Metadata: beads.StringMap{
			convoycore.LaunchOriginMetadataKey: "launcher-api",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := newTestCityHandler(t, state)
	rec := httptest.NewRecorder()
	req := newPostRequest(cityURL(state, "/convoy/")+convoy.ID+"/close", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("close: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	requests, err := externalcoordination.NewService(store).List(context.Background())
	if err != nil {
		t.Fatalf("list external coordination requests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("queued requests = %d, want one authorized lifecycle callback", len(requests))
	}
	var event convoycallback.Event
	if err := json.Unmarshal([]byte(requests[0].Request.Prompt), &event); err != nil {
		t.Fatalf("decode callback prompt: %v", err)
	}
	if event.Type != convoycallback.EventConvoyClosed {
		t.Fatalf("callback type = %q, want %q", event.Type, convoycallback.EventConvoyClosed)
	}
	if event.LaunchOrigin != "launcher-api" {
		t.Fatalf("callback launch origin = %q, want launcher-api", event.LaunchOrigin)
	}
}
