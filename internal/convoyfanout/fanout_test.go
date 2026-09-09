package convoyfanout

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/convoycallback"
)

func testTime() time.Time { return time.Date(2026, 9, 8, 19, 30, 0, 0, time.UTC) }

func testEvent(now time.Time) convoycallback.Event {
	return convoycallback.Event{
		SchemaVersion: convoycallback.ContractVersion,
		EventID:       "event-1",
		Type:          convoycallback.EventConvoyCreated,
		ConvoyID:      "convoy-a",
		LaunchOrigin:  "origin-a",
		CorrelationID: "correlation-1",
		OccurredAt:    now,
		RouteIdentity: map[string]string{"route": "opaque-event"},
	}
}

func testSubscriptionView() SubscriptionView {
	return SubscriptionView{
		ID:             "sub-1",
		Owner:          "agent-a",
		RegistrationID: "reg-a",
		Generation:     2,
		Active:         true,
		Scope:          WorkScope{ConvoyID: "convoy-a"},
		Interests:      []convoycallback.EventType{convoycallback.EventConvoyCreated},
		RouteIdentity:  "opaque-agent-a",
	}
}

func testAdmitInput(now time.Time) AdmitInput {
	return AdmitInput{
		City:   "city-a",
		Event:  testEvent(now),
		Intent: IntentNotification,
		LaunchOrigin: LaunchOriginAuthorization{
			Trusted:       true,
			Principal:     "launcher-a",
			Scope:         WorkScope{City: "city-a"},
			Interests:     []convoycallback.EventType{convoycallback.EventConvoyCreated},
			RouteIdentity: "opaque-launcher",
		},
		Subscriptions: []SubscriptionView{testSubscriptionView()},
		Target:        TargetFence{TargetID: "target-1", ConfigRevision: 7},
		DefaultPolicy: DefaultWhenNoExplicitRecipient,
		Now:           now,
	}
}

func logicalIDs(recipients []RecipientSnapshot) []string {
	out := make([]string, len(recipients))
	for i, recipient := range recipients {
		out[i] = recipient.LogicalID
	}
	return out
}

func rejectionReasons(rejections []RecipientRejection) map[string]RejectionReason {
	out := make(map[string]RejectionReason, len(rejections))
	for _, rejection := range rejections {
		out[rejection.LogicalID] = rejection.Reason
	}
	return out
}

func TestIdempotencyKeyIsStableAndRecipientSpecific(t *testing.T) {
	first, err := IdempotencyKey("event-1", "principal:agent-a")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := IdempotencyKey("event-1", "principal:agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if first != replay {
		t.Fatalf("key is not replay stable: %q vs %q", first, replay)
	}
	if len(first) != 64 {
		t.Fatalf("key length = %d, want a fixed-length 64-character digest", len(first))
	}
	otherRecipient, err := IdempotencyKey("event-1", "principal:agent-b")
	if err != nil {
		t.Fatal(err)
	}
	if otherRecipient == first {
		t.Fatal("different recipients must not share an idempotency key")
	}
	otherEvent, err := IdempotencyKey("event-2", "principal:agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if otherEvent == first {
		t.Fatal("different events must not share an idempotency key")
	}
}

func TestIdempotencyKeyIsUnambiguousAcrossFieldBoundaries(t *testing.T) {
	left, err := IdempotencyKey("ab", "c")
	if err != nil {
		t.Fatal(err)
	}
	right, err := IdempotencyKey("a", "bc")
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("key derivation must length-prefix its components so field boundaries cannot be forged")
	}
}

func TestIdempotencyKeyRejectsMissingComponents(t *testing.T) {
	for _, tc := range []struct{ name, event, recipient string }{
		{"no event", " ", "principal:agent-a"},
		{"no recipient", "event-1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := IdempotencyKey(tc.event, tc.recipient); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestSelectRecipientsAdmitsAuthorizedSubscriptionAndTrustedLaunchOrigin(t *testing.T) {
	now := testTime()
	recipients, rejections, err := SelectRecipients(testAdmitInput(now))
	if err != nil {
		t.Fatal(err)
	}
	if len(rejections) != 0 {
		t.Fatalf("rejections = %#v, want none", rejections)
	}
	got := logicalIDs(recipients)
	want := []string{"principal:agent-a", "principal:launcher-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recipients = %v, want %v", got, want)
	}
	if recipients[0].Kind != KindSubscription || recipients[0].Reason != ReasonSubscriptionAuthorized {
		t.Fatalf("subscription recipient = %#v", recipients[0])
	}
	if recipients[0].SubscriptionID != "sub-1" || recipients[0].Fence.RegistrationID != "reg-a" || recipients[0].Fence.Generation != 2 {
		t.Fatalf("subscription authorization snapshot = %#v", recipients[0])
	}
	if recipients[1].Kind != KindLaunchOrigin || recipients[1].Reason != ReasonLaunchOriginAuthorized {
		t.Fatalf("launch-origin recipient = %#v", recipients[1])
	}
	if recipients[1].RouteIdentity != "opaque-launcher" {
		t.Fatalf("launch-origin route = %q, want the caller-supplied opaque route", recipients[1].RouteIdentity)
	}
	for _, recipient := range recipients {
		if recipient.IsDefault {
			t.Fatalf("recipient %q must not be marked as the configured default", recipient.LogicalID)
		}
	}
}

func TestSelectRecipientsRejectsUntrustedLaunchOrigin(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	recipients, rejections, err := SelectRecipients(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := logicalIDs(recipients); !reflect.DeepEqual(got, []string{"principal:agent-a"}) {
		t.Fatalf("recipients = %v, want only the authorized subscription", got)
	}
	if reason := rejectionReasons(rejections)["principal:launcher-a"]; reason != RejectLaunchOriginUntrusted {
		t.Fatalf("rejection = %q, want %q", reason, RejectLaunchOriginUntrusted)
	}
}

func TestSelectRecipientsRejectsLaunchOriginWithoutPrincipal(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Principal = "  "
	if _, _, err := SelectRecipients(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput: a trusted origin needs an explicit principal", err)
	}
}

func TestSelectRecipientsRejectsUnauthorizedSubscriptions(t *testing.T) {
	now := testTime()
	cases := []struct {
		name   string
		mutate func(*SubscriptionView)
		reason RejectionReason
	}{
		{"inactive", func(v *SubscriptionView) { v.Active = false }, RejectSubscriptionInactive},
		{"no registration", func(v *SubscriptionView) { v.RegistrationID = "" }, RejectSubscriptionUnfenced},
		{"zero generation", func(v *SubscriptionView) { v.Generation = 0 }, RejectSubscriptionUnfenced},
		{"scope mismatch", func(v *SubscriptionView) { v.Scope = WorkScope{ConvoyID: "convoy-other"} }, RejectWorkScopeMismatch},
		{"city mismatch", func(v *SubscriptionView) { v.Scope = WorkScope{City: "city-other"} }, RejectWorkScopeMismatch},
		{"interest mismatch", func(v *SubscriptionView) {
			v.Interests = []convoycallback.EventType{convoycallback.EventConvoyClosed}
		}, RejectLifecycleInterestMismatch},
		{"no interest", func(v *SubscriptionView) { v.Interests = nil }, RejectLifecycleInterestMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := testAdmitInput(now)
			input.LaunchOrigin.Trusted = false
			// No configured target, so an unauthorized subscription cannot be
			// masked by admitting the default recipient in its place.
			input.Target = TargetFence{}
			view := testSubscriptionView()
			tc.mutate(&view)
			input.Subscriptions = []SubscriptionView{view}
			recipients, rejections, err := SelectRecipients(input)
			if !errors.Is(err, ErrNoAuthorizedRecipient) {
				t.Fatalf("err = %v, want ErrNoAuthorizedRecipient", err)
			}
			if len(recipients) != 0 {
				t.Fatalf("recipients = %v, want none", logicalIDs(recipients))
			}
			if reason := rejectionReasons(rejections)["principal:agent-a"]; reason != tc.reason {
				t.Fatalf("rejection = %q, want %q", reason, tc.reason)
			}
		})
	}
}

func TestSelectRecipientsRejectsSubscriptionWithoutOwner(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	view := testSubscriptionView()
	view.Owner = ""
	input.Subscriptions = []SubscriptionView{view}
	if _, _, err := SelectRecipients(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestSelectRecipientsRejectsCommaJoinedIdentity(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	view := testSubscriptionView()
	view.Owner = "agent-a,agent-b"
	input.Subscriptions = []SubscriptionView{view}
	if _, _, err := SelectRecipients(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput for a comma-joined identity", err)
	}
}

func TestSelectRecipientsDeduplicatesTheSameLogicalPrincipal(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Principal = "agent-a"
	recipients, rejections, err := SelectRecipients(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := logicalIDs(recipients); !reflect.DeepEqual(got, []string{"principal:agent-a"}) {
		t.Fatalf("recipients = %v, want one record for the shared principal", got)
	}
	if recipients[0].Kind != KindSubscription {
		t.Fatalf("kind = %q, want the fenced subscription authorization to win", recipients[0].Kind)
	}
	if len(rejections) != 1 || rejections[0].Reason != RejectDuplicateLogicalRecipient {
		t.Fatalf("rejections = %#v, want one duplicate-recipient rejection", rejections)
	}
}

func TestSelectRecipientsNeverMergesDistinctPrincipalsSharingARoute(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	first := testSubscriptionView()
	second := testSubscriptionView()
	second.ID = "sub-2"
	second.Owner = "agent-b"
	second.RouteIdentity = first.RouteIdentity
	input.Subscriptions = []SubscriptionView{first, second}
	recipients, _, err := SelectRecipients(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := logicalIDs(recipients); !reflect.DeepEqual(got, []string{"principal:agent-a", "principal:agent-b"}) {
		t.Fatalf("recipients = %v, want route equality to never merge logical recipients", got)
	}
}

func TestSelectRecipientsAdmitsConfiguredDefaultWhenNoExplicitRecipient(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	input.Subscriptions = nil
	recipients, _, err := SelectRecipients(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := logicalIDs(recipients); !reflect.DeepEqual(got, []string{"default:target-1"}) {
		t.Fatalf("recipients = %v, want the configured default target", got)
	}
	if !recipients[0].IsDefault || recipients[0].Kind != KindConfiguredDefault {
		t.Fatalf("default recipient = %#v", recipients[0])
	}
	if recipients[0].Reason != ReasonConfiguredDefaultTarget {
		t.Fatalf("reason = %q, want %q", recipients[0].Reason, ReasonConfiguredDefaultTarget)
	}
	if recipients[0].RouteIdentity != "" {
		t.Fatalf("route = %q, want the configured target to carry no route of its own", recipients[0].RouteIdentity)
	}
}

func TestSelectRecipientsWithholdsDefaultWhenAnExplicitRecipientIsAuthorized(t *testing.T) {
	now := testTime()
	recipients, _, err := SelectRecipients(testAdmitInput(now))
	if err != nil {
		t.Fatal(err)
	}
	for _, recipient := range recipients {
		if recipient.IsDefault {
			t.Fatal("the configured default must not be admitted while an explicit recipient is authorized")
		}
	}
}

func TestSelectRecipientsAdmitsDistinctDefaultUnderAlwaysPolicy(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.DefaultPolicy = DefaultAlwaysDistinct
	input.LaunchOrigin.Trusted = false
	view := testSubscriptionView()
	view.Owner = "target-1"
	view.RouteIdentity = "target-1"
	input.Subscriptions = []SubscriptionView{view}
	recipients, _, err := SelectRecipients(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := logicalIDs(recipients); !reflect.DeepEqual(got, []string{"principal:target-1", "default:target-1"}) {
		t.Fatalf("recipients = %v, want the configured default to stay a distinct recipient", got)
	}
	explicitKey, err := IdempotencyKey("event-1", recipients[0].LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	defaultKey, err := IdempotencyKey("event-1", recipients[1].LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	if explicitKey == defaultKey {
		t.Fatal("the configured default must get its own idempotency key")
	}
}

func TestSelectRecipientsReportsUnconfiguredDefaultTarget(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	input.Subscriptions = nil
	input.Target = TargetFence{}
	recipients, rejections, err := SelectRecipients(input)
	if !errors.Is(err, ErrNoAuthorizedRecipient) {
		t.Fatalf("err = %v, want ErrNoAuthorizedRecipient", err)
	}
	if len(recipients) != 0 {
		t.Fatalf("recipients = %v, want none", logicalIDs(recipients))
	}
	if reason := rejectionReasons(rejections)[defaultPrefix]; reason != RejectDefaultTargetUnconfigured {
		t.Fatalf("rejections = %#v, want an unconfigured-default rejection", rejections)
	}
}

func TestSelectRecipientsFallsBackToTheDefaultWhenNoSubscriptionIsAuthorized(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	view := testSubscriptionView()
	view.Active = false
	input.Subscriptions = []SubscriptionView{view}
	recipients, rejections, err := SelectRecipients(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := logicalIDs(recipients); !reflect.DeepEqual(got, []string{"default:target-1"}) {
		t.Fatalf("recipients = %v, want the configured default in place of the unauthorized subscription", got)
	}
	if reason := rejectionReasons(rejections)["principal:agent-a"]; reason != RejectSubscriptionInactive {
		t.Fatalf("rejection = %q, want the subscription rejection to stay auditable", reason)
	}
}

func TestSelectRecipientsIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	now := testTime()
	build := func(order []string) []SubscriptionView {
		out := make([]SubscriptionView, 0, len(order))
		for i, owner := range order {
			view := testSubscriptionView()
			view.ID = "sub-" + owner
			view.Owner = owner
			view.Generation = uint64(i + 1)
			out = append(out, view)
		}
		return out
	}
	forward := testAdmitInput(now)
	forward.Subscriptions = build([]string{"agent-a", "agent-b", "agent-c"})
	reverse := testAdmitInput(now)
	reverse.Subscriptions = build([]string{"agent-c", "agent-b", "agent-a"})
	first, _, err := SelectRecipients(forward)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := SelectRecipients(reverse)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(logicalIDs(first), logicalIDs(second)) {
		t.Fatalf("recipient order is input dependent: %v vs %v", logicalIDs(first), logicalIDs(second))
	}
}

func TestSelectRecipientsMatchesWorkRefScope(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	input.Event.Type = convoycallback.EventTaskAccepted
	input.Event.TaskID = "task-9"
	view := testSubscriptionView()
	view.Interests = []convoycallback.EventType{convoycallback.EventTaskAccepted}
	view.Scope = WorkScope{WorkRefs: []string{"task-9"}}
	input.Subscriptions = []SubscriptionView{view}
	recipients, _, err := SelectRecipients(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := logicalIDs(recipients); !reflect.DeepEqual(got, []string{"principal:agent-a"}) {
		t.Fatalf("recipients = %v, want the work-ref scoped subscription", got)
	}
}

func TestSelectRecipientsRejectsInterventionIntent(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.Intent = IntentIntervention
	if _, _, err := SelectRecipients(input); !errors.Is(err, ErrUnsupportedIntent) {
		t.Fatalf("err = %v, want ErrUnsupportedIntent", err)
	}
}

func TestSelectRecipientsRejectsInvalidEventAndMissingClock(t *testing.T) {
	now := testTime()
	t.Run("invalid event", func(t *testing.T) {
		input := testAdmitInput(now)
		input.Event.CorrelationID = ""
		if _, _, err := SelectRecipients(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("missing clock", func(t *testing.T) {
		input := testAdmitInput(now)
		input.Now = time.Time{}
		if _, _, err := SelectRecipients(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("missing city", func(t *testing.T) {
		input := testAdmitInput(now)
		input.City = ""
		if _, _, err := SelectRecipients(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("unknown default policy", func(t *testing.T) {
		input := testAdmitInput(now)
		input.DefaultPolicy = DefaultRecipientPolicy("whatever")
		if _, _, err := SelectRecipients(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
}

func TestSelectRecipientsRejectsMalformedTargetFence(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.Target = TargetFence{ConfigRevision: 3}
	if _, _, err := SelectRecipients(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput for a revision without a target identity", err)
	}
}

func TestTargetFenceComparesIdentityAndRevision(t *testing.T) {
	base := TargetFence{TargetID: "target-1", ConfigRevision: 7}
	if !base.Equal(TargetFence{TargetID: "target-1", ConfigRevision: 7}) {
		t.Fatal("identical fences must compare equal")
	}
	if base.Equal(TargetFence{TargetID: "target-1", ConfigRevision: 8}) {
		t.Fatal("a changed configuration revision must not compare equal")
	}
	if base.Equal(TargetFence{TargetID: "target-2", ConfigRevision: 7}) {
		t.Fatal("a changed target identity must not compare equal")
	}
	if (TargetFence{}).Configured() {
		t.Fatal("an empty fence is not configured")
	}
}

func TestSnapshotRouteIdentityIsNeverATarget(t *testing.T) {
	now := testTime()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	view := testSubscriptionView()
	view.RouteIdentity = "https://attacker.example/callback"
	input.Subscriptions = []SubscriptionView{view}
	recipients, _, err := SelectRecipients(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 {
		t.Fatalf("recipients = %v, want one", logicalIDs(recipients))
	}
	if strings.Contains(recipients[0].LogicalID, "attacker.example") {
		t.Fatal("route data must never become recipient identity")
	}
	if recipients[0].RouteIdentity != view.RouteIdentity {
		t.Fatal("route data must be carried through verbatim as opaque data")
	}
}
