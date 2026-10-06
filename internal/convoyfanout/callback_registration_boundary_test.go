package convoyfanout

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
)

// TestSubscriptionBoundaryRequiresOwnerAndFenceBeforeFanout proves the
// provider-free boundary consumed by callback admission: the durable
// subscription service requires the explicit owner and current registration
// fence, while fan-out treats route identity as opaque data. It does not prove
// that an HTTP, CLI, or provider caller is authenticated as that owner.
func TestSubscriptionBoundaryRequiresOwnerAndFenceBeforeFanout(t *testing.T) {
	ctx := context.Background()
	now := testTime()
	service := convoy.NewSubscriptionService(beads.NewMemStore())
	record, err := service.Create(ctx, convoy.CreateSubscriptionInput{
		Owner:          "session-a",
		RegistrationID: "registration-a",
		Generation:     1,
		Scope: convoy.WorkScope{
			City:     "city-a",
			ConvoyID: "convoy-a",
		},
		RouteIdentity: "opaque-route-a",
		Interests:     []convoy.LifecycleInterest{convoy.LifecycleInterest(convoycallback.EventConvoyClosed)},
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}

	owned, err := service.Get(ctx, "session-a", record.ID)
	if err != nil {
		t.Fatalf("owner lookup = %v, want success", err)
	}
	if _, err := service.Get(ctx, "session-b", record.ID); !errors.Is(err, convoy.ErrUnauthorized) {
		t.Fatalf("cross-session lookup = %v, want %v", err, convoy.ErrUnauthorized)
	}
	if _, err := service.Get(ctx, "opaque-route-a", record.ID); !errors.Is(err, convoy.ErrUnauthorized) {
		t.Fatalf("route-as-owner lookup = %v, want %v", err, convoy.ErrUnauthorized)
	}
	if _, err := service.Get(ctx, "session-a,session-b", record.ID); !errors.Is(err, convoy.ErrInvalidInput) {
		t.Fatalf("ambiguous owner lookup = %v, want %v", err, convoy.ErrInvalidInput)
	}

	renewed, err := service.Renew(ctx, owned.Owner, owned.ID, convoy.RegistrationFence{
		RegistrationID: owned.RegistrationID,
		Generation:     owned.Generation,
	}, now)
	if err != nil {
		t.Fatalf("owner renewal = %v, want success", err)
	}
	if renewed.Generation != 2 {
		t.Fatalf("renewed generation = %d, want 2", renewed.Generation)
	}
	if _, err := service.Renew(ctx, owned.Owner, owned.ID, convoy.RegistrationFence{
		RegistrationID: owned.RegistrationID,
		Generation:     owned.Generation,
	}, now); !errors.Is(err, convoy.ErrStaleFence) {
		t.Fatalf("stale renewal = %v, want %v", err, convoy.ErrStaleFence)
	}

	event := convoycallback.Event{
		SchemaVersion: convoycallback.ContractVersion,
		EventID:       "event-session-a",
		Type:          convoycallback.EventConvoyClosed,
		ConvoyID:      "convoy-a",
		LaunchOrigin:  "ambient-selector-only",
		CorrelationID: "correlation-session-a",
		OccurredAt:    now,
	}
	recipients, rejections, err := SelectRecipients(AdmitInput{
		City:   "city-a",
		Event:  event,
		Intent: IntentNotification,
		LaunchOrigin: LaunchOriginAuthorization{
			Trusted:       false,
			Principal:     "session-a",
			RouteIdentity: "opaque-route-a",
		},
		Subscriptions: SubscriptionViews([]convoy.SubscriptionRecord{renewed}),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("authorized subscription admission = %v, want success", err)
	}
	if len(recipients) != 1 || recipients[0].LogicalID != "principal:session-a" {
		t.Fatalf("recipients = %#v, want one owner-scoped recipient", recipients)
	}
	if recipients[0].RouteIdentity != "opaque-route-a" {
		t.Fatalf("route identity = %q, want opaque route preserved", recipients[0].RouteIdentity)
	}
	if recipients[0].Fence.Generation != 2 {
		t.Fatalf("admission fence = %#v, want renewed generation", recipients[0].Fence)
	}
	if len(rejections) != 1 || rejections[0].Reason != RejectLaunchOriginUntrusted {
		t.Fatalf("rejections = %#v, want the ambient launch selector refused", rejections)
	}

	event.ConvoyID = "convoy-other"
	recipients, rejections, err = SelectRecipients(AdmitInput{
		City:          "city-a",
		Event:         event,
		Intent:        IntentNotification,
		Subscriptions: SubscriptionViews([]convoy.SubscriptionRecord{renewed}),
		Now:           now,
	})
	if !errors.Is(err, ErrNoAuthorizedRecipient) {
		t.Fatalf("cross-convoy admission = %v, want %v", err, ErrNoAuthorizedRecipient)
	}
	if len(recipients) != 0 {
		t.Fatalf("cross-convoy recipients = %#v, want none", recipients)
	}
	var scopedRejection bool
	for _, rejection := range rejections {
		if rejection.Kind == KindSubscription && rejection.Reason == RejectWorkScopeMismatch {
			scopedRejection = true
			break
		}
	}
	if !scopedRejection {
		t.Fatalf("cross-convoy rejections = %#v, want the subscription scope refusal", rejections)
	}
}
