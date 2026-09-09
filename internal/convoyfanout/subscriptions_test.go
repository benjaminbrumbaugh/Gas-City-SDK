package convoyfanout

import (
	"reflect"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
)

func TestSubscriptionViewsProjectDurableAuthorizationEvidence(t *testing.T) {
	registered := time.Date(2026, 9, 8, 19, 0, 0, 0, time.UTC)
	records := []convoy.SubscriptionRecord{
		{
			ID:             "sub-1",
			Owner:          "agent-a",
			RegistrationID: "reg-a",
			Generation:     3,
			State:          convoy.SubscriptionActive,
			Scope:          convoy.WorkScope{City: "city-a", ConvoyID: "convoy-a", WorkRefs: []string{"work-1"}},
			RouteIdentity:  "opaque-agent-a",
			Interests:      []convoy.LifecycleInterest{"convoy.created", "convoy.closed"},
			RegisteredAt:   registered,
		},
		{
			ID:             "sub-2",
			Owner:          "agent-b",
			RegistrationID: "reg-b",
			Generation:     1,
			State:          convoy.SubscriptionRevoked,
			Scope:          convoy.WorkScope{ConvoyID: "convoy-a"},
			RouteIdentity:  "opaque-agent-b",
			Interests:      []convoy.LifecycleInterest{"convoy.created"},
			RegisteredAt:   registered,
		},
	}
	views := SubscriptionViews(records)
	if len(views) != 2 {
		t.Fatalf("views = %d, want one per durable record", len(views))
	}
	want := SubscriptionView{
		ID:             "sub-1",
		Owner:          "agent-a",
		RegistrationID: "reg-a",
		Generation:     3,
		Active:         true,
		Scope:          WorkScope{City: "city-a", ConvoyID: "convoy-a", WorkRefs: []string{"work-1"}},
		Interests:      []convoycallback.EventType{convoycallback.EventConvoyCreated, convoycallback.EventConvoyClosed},
		RouteIdentity:  "opaque-agent-a",
	}
	if !reflect.DeepEqual(views[0], want) {
		t.Fatalf("view = %#v, want %#v", views[0], want)
	}
	if views[1].Active {
		t.Fatal("a revoked subscription must not project as active")
	}
}

func TestSubscriptionViewsDoNotAliasRecordSlices(t *testing.T) {
	records := []convoy.SubscriptionRecord{{
		ID:             "sub-1",
		Owner:          "agent-a",
		RegistrationID: "reg-a",
		Generation:     1,
		State:          convoy.SubscriptionActive,
		Scope:          convoy.WorkScope{WorkRefs: []string{"work-1"}},
		Interests:      []convoy.LifecycleInterest{"convoy.created"},
	}}
	views := SubscriptionViews(records)
	records[0].Scope.WorkRefs[0] = "mutated"
	records[0].Interests[0] = "mutated"
	if views[0].Scope.WorkRefs[0] != "work-1" {
		t.Fatal("work refs must be copied, not aliased")
	}
	if views[0].Interests[0] != convoycallback.EventConvoyCreated {
		t.Fatal("interests must be copied, not aliased")
	}
}

func TestSubscriptionViewsHandleAnEmptyLedger(t *testing.T) {
	if views := SubscriptionViews(nil); len(views) != 0 {
		t.Fatalf("views = %#v, want empty", views)
	}
}

func TestSubscriptionViewsKeepUnknownInterestsOpaque(t *testing.T) {
	records := []convoy.SubscriptionRecord{{
		ID:             "sub-1",
		Owner:          "agent-a",
		RegistrationID: "reg-a",
		Generation:     1,
		State:          convoy.SubscriptionActive,
		Scope:          convoy.WorkScope{ConvoyID: "convoy-a"},
		Interests:      []convoy.LifecycleInterest{"some.future.event"},
	}}
	views := SubscriptionViews(records)
	if len(views[0].Interests) != 1 || views[0].Interests[0] != convoycallback.EventType("some.future.event") {
		t.Fatalf("interests = %#v, want the unknown value carried through", views[0].Interests)
	}
	input := testAdmitInput(testTime())
	input.LaunchOrigin.Trusted = false
	input.Subscriptions = views
	input.Target = TargetFence{}
	recipients, rejections, err := SelectRecipients(input)
	if err == nil {
		t.Fatalf("recipients = %v, want an unmatched interest to authorize nothing", logicalIDs(recipients))
	}
	if reason := rejectionReasons(rejections)["principal:agent-a"]; reason != RejectLifecycleInterestMismatch {
		t.Fatalf("rejection = %q, want %q", reason, RejectLifecycleInterestMismatch)
	}
}
