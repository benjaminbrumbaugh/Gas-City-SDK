package convoydelivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
	"github.com/gastownhall/gascity/internal/convoyfanout"
	"github.com/gastownhall/gascity/internal/externalcoordination"
)

func TestDeliverQueuesAuthorizedSubscribersAndReplaysByRecipient(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 5, 18, 0, 0, 0, time.UTC)
	store := beads.NewMemStore()
	convoyBead, err := store.Create(beads.Bead{
		Title: "callback convoy",
		Type:  "convoy",
		Metadata: beads.StringMap{
			convoy.LaunchOriginMetadataKey: "launcher-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := convoy.NewSubscriptionService(store).Create(ctx, convoy.CreateSubscriptionInput{
		Owner:          "subscriber-1",
		RegistrationID: "registration-1",
		Generation:     2,
		Scope:          convoy.WorkScope{City: "callback-city", ConvoyID: convoyBead.ID},
		RouteIdentity:  "opaque-route-1",
		Interests:      []convoy.LifecycleInterest{convoy.LifecycleInterest(convoycallback.EventConvoyCreated)},
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}

	service := NewService(store, "callback-city", callbackConfig()).WithClock(func() time.Time { return now })
	first, err := service.Deliver(ctx, convoyBead, convoycallback.EventConvoyCreated)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if len(first.Admission.Deliveries) != 2 {
		t.Fatalf("first admitted deliveries = %d, want launch origin plus subscription", len(first.Admission.Deliveries))
	}
	if len(first.Requests) != 2 {
		t.Fatalf("first queued requests = %d, want two", len(first.Requests))
	}
	if first.Requests[0].State != externalcoordination.StateQueued {
		t.Fatalf("first request state = %q, want queued", first.Requests[0].State)
	}

	replay, err := service.Deliver(ctx, convoyBead, convoycallback.EventConvoyCreated)
	if err != nil {
		t.Fatalf("replay delivery: %v", err)
	}
	if len(replay.Requests) != len(first.Requests) {
		t.Fatalf("replay queued requests = %d, want %d", len(replay.Requests), len(first.Requests))
	}
	for i := range first.Requests {
		if replay.Requests[i].ID != first.Requests[i].ID {
			t.Fatalf("replay request %d ID = %q, want original %q", i, replay.Requests[i].ID, first.Requests[i].ID)
		}
		if replay.Requests[i].Request.IdempotencyKey != first.Requests[i].Request.IdempotencyKey {
			t.Fatalf("replay request %d changed idempotency key", i)
		}
	}
	if got, err := externalcoordination.NewService(store).List(ctx); err != nil {
		t.Fatal(err)
	} else if len(got) != 2 {
		t.Fatalf("durable requests after replay = %d, want two", len(got))
	}
	if subscription.ID == "" {
		t.Fatal("subscription ID was not assigned")
	}
}

func TestDeliverRejectsInactiveAndMismatchedSubscriptions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 5, 18, 0, 0, 0, time.UTC)
	store := beads.NewMemStore()
	convoyBead, err := store.Create(beads.Bead{Title: "callback convoy", Type: "convoy", Metadata: beads.StringMap{
		convoy.LaunchOriginMetadataKey: "launcher-1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	service := convoy.NewSubscriptionService(store)
	_, err = service.Create(ctx, convoy.CreateSubscriptionInput{
		Owner:          "wrong-scope",
		RegistrationID: "registration-wrong",
		Generation:     1,
		Scope:          convoy.WorkScope{City: "other-city", ConvoyID: convoyBead.ID},
		RouteIdentity:  "route-wrong",
		Interests:      []convoy.LifecycleInterest{convoy.LifecycleInterest(convoycallback.EventConvoyCreated)},
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := service.Create(ctx, convoy.CreateSubscriptionInput{
		Owner:          "revoked",
		RegistrationID: "registration-revoked",
		Generation:     4,
		Scope:          convoy.WorkScope{City: "callback-city", ConvoyID: convoyBead.ID},
		RouteIdentity:  "route-revoked",
		Interests:      []convoy.LifecycleInterest{convoy.LifecycleInterest(convoycallback.EventConvoyCreated)},
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(ctx, revoked.Owner, revoked.ID, convoy.RegistrationFence{RegistrationID: revoked.RegistrationID, Generation: revoked.Generation}, "test revocation", now); err != nil {
		t.Fatal(err)
	}

	result, err := NewService(store, "callback-city", callbackConfig()).WithClock(func() time.Time { return now }).Deliver(ctx, convoyBead, convoycallback.EventConvoyCreated)
	if err != nil {
		t.Fatalf("delivery with rejected subscriptions: %v", err)
	}
	if len(result.Requests) != 1 {
		t.Fatalf("queued requests = %d, want only the trusted launch origin", len(result.Requests))
	}
	if len(result.Admission.Rejections) != 2 {
		t.Fatalf("subscription rejections = %d, want two", len(result.Admission.Rejections))
	}
}

func TestDeliverMissingOriginIsLegacyNoOpAndCloseIsNotReadiness(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 5, 18, 0, 0, 0, time.UTC)
	store := beads.NewMemStore()
	legacy, err := store.Create(beads.Bead{Title: "legacy convoy", Type: "convoy"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, "callback-city", callbackConfig()).WithClock(func() time.Time { return now })
	legacyResult, err := service.Deliver(ctx, legacy, convoycallback.EventConvoyCreated)
	if err != nil {
		t.Fatalf("legacy delivery: %v", err)
	}
	if !legacyResult.Skipped {
		t.Fatal("missing launch origin should preserve legacy no-op behavior")
	}

	current, err := store.Create(beads.Bead{Title: "closed convoy", Type: "convoy", Metadata: beads.StringMap{
		convoy.LaunchOriginMetadataKey: "launcher-2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(current.ID); err != nil {
		t.Fatal(err)
	}
	closed, err := store.Get(current.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Deliver(ctx, closed, convoycallback.EventConvoyClosed)
	if err != nil {
		t.Fatalf("closed delivery: %v", err)
	}
	if result.Event.Type != convoycallback.EventConvoyClosed {
		t.Fatalf("closed callback type = %q, want %q", result.Event.Type, convoycallback.EventConvoyClosed)
	}
	if result.Event.Type == convoycallback.EventReadyForRebuild {
		t.Fatal("convoy closure must not assert rebuild readiness")
	}
	if len(result.Requests) != 1 {
		t.Fatalf("closed callback requests = %d, want one", len(result.Requests))
	}
	var decoded convoycallback.Event
	if err := json.Unmarshal([]byte(result.Requests[0].Request.Prompt), &decoded); err != nil {
		t.Fatalf("decode closed callback: %v", err)
	}
	if decoded.Type != convoycallback.EventConvoyClosed {
		t.Fatalf("queued callback type = %q, want convoy.closed", decoded.Type)
	}
	if _, err := service.Deliver(ctx, closed, convoycallback.EventReadyForRebuild); err == nil {
		t.Fatal("unsupported readiness event unexpectedly accepted")
	} else if !strings.Contains(err.Error(), "unsupported lifecycle event") {
		t.Fatalf("unsupported readiness error = %q", err)
	}
}

func TestDeliverRejectsChangedConfiguredTargetOnReplay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 5, 18, 0, 0, 0, time.UTC)
	store := beads.NewMemStore()
	convoyBead, err := store.Create(beads.Bead{Title: "fenced convoy", Type: "convoy", Metadata: beads.StringMap{
		convoy.LaunchOriginMetadataKey: "launcher-fenced",
	}})
	if err != nil {
		t.Fatal(err)
	}

	first, err := NewService(store, "callback-city", callbackConfig()).WithClock(func() time.Time { return now }).Deliver(ctx, convoyBead, convoycallback.EventConvoyCreated)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if len(first.Requests) != 1 {
		t.Fatalf("first queued requests = %d, want one", len(first.Requests))
	}

	changed := *callbackConfig()
	changed.Target = "different-coordinator"
	changed.ConfigRevision++
	_, err = NewService(store, "callback-city", &changed).WithClock(func() time.Time { return now }).Deliver(ctx, convoyBead, convoycallback.EventConvoyCreated)
	if !errors.Is(err, convoyfanout.ErrStaleTargetFence) {
		t.Fatalf("replay error = %v, want ErrStaleTargetFence", err)
	}
}

func callbackConfig() *config.ExternalCoordinationConfig {
	return &config.ExternalCoordinationConfig{
		Enabled:        true,
		Target:         "configured-coordinator",
		Adapter:        "bridge",
		ConfigRevision: 9,
	}
}
