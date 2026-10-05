package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/externalcoordination"
)

func TestConvoyCreateProductionPathQueuesLifecycleCallback(t *testing.T) {
	store := beads.NewMemStore()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "callback-city"},
		ExternalCoordination: &config.ExternalCoordinationConfig{
			Enabled:        true,
			Target:         "configured-coordinator",
			Adapter:        "bridge",
			ConfigRevision: 7,
		},
	}

	if code := doConvoyCreateWithOptionsJSON(
		store,
		cfg,
		"",
		events.Discard,
		[]string{"callback convoy"},
		convoyCreateOptions{Fields: ConvoyFields{LaunchOrigin: "launcher-1"}},
		false,
		unusedWriter{},
		unusedWriter{},
	); code != 0 {
		t.Fatalf("doConvoyCreateWithOptionsJSON = %d", code)
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
	if event.Type != convoycallback.EventConvoyCreated {
		t.Fatalf("callback type = %q, want %q", event.Type, convoycallback.EventConvoyCreated)
	}
	if event.LaunchOrigin != "launcher-1" {
		t.Fatalf("callback launch origin = %q, want launcher-1", event.LaunchOrigin)
	}
	if requests[0].Request.IdempotencyKey == "" {
		t.Fatal("callback request has no recipient idempotency key")
	}
}

func TestConvoyCloseProductionPathQueuesClosureCallback(t *testing.T) {
	store := beads.NewMemStore()
	convoy, err := store.Create(beads.Bead{Title: "closure convoy", Type: "convoy", Metadata: beads.StringMap{
		convoycore.LaunchOriginMetadataKey: "launcher-2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.City{Workspace: config.Workspace{Name: "callback-city"}, ExternalCoordination: &config.ExternalCoordinationConfig{
		Enabled:        true,
		Target:         "configured-coordinator",
		Adapter:        "bridge",
		ConfigRevision: 8,
	}}
	var stdout, stderr unusedWriter
	if code := doConvoyCloseJSONWithConfig(store, cfg, "", events.Discard, []string{convoy.ID}, false, stdout, stderr); code != 0 {
		t.Fatalf("doConvoyCloseJSONWithConfig = %d", code)
	}
	requests, err := externalcoordination.NewService(store).List(context.Background())
	if err != nil {
		t.Fatalf("list external coordination requests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("queued closure requests = %d, want one", len(requests))
	}
	var event convoycallback.Event
	if err := json.Unmarshal([]byte(requests[0].Request.Prompt), &event); err != nil {
		t.Fatalf("decode closure callback prompt: %v", err)
	}
	if event.Type != convoycallback.EventConvoyClosed {
		t.Fatalf("closure callback type = %q, want %q", event.Type, convoycallback.EventConvoyClosed)
	}
}

type unusedWriter struct{}

func (unusedWriter) Write(p []byte) (int, error) { return len(p), nil }
