package convoy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func testSubscriptionInput(now time.Time) CreateSubscriptionInput {
	return CreateSubscriptionInput{
		Owner:          "agent-a",
		RegistrationID: "registration-a",
		Generation:     1,
		Scope: WorkScope{
			City:     "city-a",
			ConvoyID: "convoy-a",
			WorkRefs: []string{"work-a", "work-b"},
		},
		RouteIdentity: "opaque-route-a",
		Interests:     []LifecycleInterest{"convoy.created", "convoy.closed"},
		Now:           now,
	}
}

func testSubscriptionService() (*SubscriptionService, *beads.MemStore) {
	store := beads.NewMemStore()
	return NewSubscriptionService(store), store
}

func testFence(record SubscriptionRecord) RegistrationFence {
	return RegistrationFence{
		RegistrationID: record.RegistrationID,
		Generation:     record.Generation,
	}
}

func stringPointer(value string) *string { return &value }

func TestSubscriptionCreateRoundTripIsDurableAndOwnerScoped(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, store := testSubscriptionService()
	input := testSubscriptionInput(now)
	record, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID == "" || record.SchemaVersion == 0 {
		t.Fatalf("record identity = %#v, want durable identity and schema", record)
	}
	if record.State != SubscriptionActive {
		t.Fatalf("state = %q, want active", record.State)
	}
	if !record.RegisteredAt.Equal(now) || !record.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps = (%s, %s), want %s", record.RegisteredAt, record.UpdatedAt, now)
	}

	input.Scope.WorkRefs[0] = "caller-mutated"
	input.Interests[0] = "caller-mutated"
	if record.Scope.WorkRefs[0] != "work-a" || record.Interests[0] != "convoy.created" {
		t.Fatal("create did not snapshot mutable input values")
	}

	reread, err := service.Get(context.Background(), "agent-a", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.ID != record.ID || reread.RouteIdentity != "opaque-route-a" {
		t.Fatalf("reread = %#v, want created record", reread)
	}
	if got := reread.Scope.WorkRefs; len(got) != 2 || got[0] != "work-a" || got[1] != "work-b" {
		t.Fatalf("reread scope refs = %#v", got)
	}

	owned, err := service.List(context.Background(), "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned[0].ID != record.ID {
		t.Fatalf("owned list = %#v, want one record", owned)
	}
	other, err := service.List(context.Background(), "agent-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("cross-owner list = %#v, want empty", other)
	}

	bead, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCallbackSubscriptionLabel(bead) || strings.TrimSpace(bead.Metadata[subscriptionRecordMetadata]) == "" {
		t.Fatalf("durable bead metadata = %#v, want callback-subscription record", bead.Metadata)
	}
	var persisted SubscriptionRecord
	if err := json.Unmarshal([]byte(bead.Metadata[subscriptionRecordMetadata]), &persisted); err != nil {
		t.Fatalf("persisted record JSON: %v", err)
	}
	if persisted.ID != "" || persisted.Owner != record.Owner || persisted.RegistrationID != record.RegistrationID {
		t.Fatalf("persisted = %#v, want payload fields with bead ID projected by the enclosing record", persisted)
	}
}

func TestSubscriptionIdentityBytesAreNotNormalizedOrAliased(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	input := testSubscriptionInput(now)
	input.Owner = " agent-a "
	input.RegistrationID = "registration-a "
	input.Scope.City = " city-a "
	input.RouteIdentity = " opaque-route-a "
	record, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if record.Owner != input.Owner || record.RegistrationID != input.RegistrationID || record.Scope.City != input.Scope.City || record.RouteIdentity != input.RouteIdentity {
		t.Fatalf("identity bytes changed: got=%#v input=%#v", record, input)
	}
	if _, err := service.Get(context.Background(), "agent-a", record.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("trimmed owner lookup error = %v, want %v", err, ErrUnauthorized)
	}
	if _, err := service.Update(context.Background(), input.Owner, record.ID, RegistrationFence{RegistrationID: "registration-a", Generation: 1}, SubscriptionPatch{RouteIdentity: stringPointer("new-route")}, now.Add(time.Minute)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("trimmed fence update error = %v, want %v", err, ErrStaleFence)
	}
	if _, err := service.Update(context.Background(), input.Owner, record.ID, RegistrationFence{RegistrationID: input.RegistrationID, Generation: 1}, SubscriptionPatch{RouteIdentity: stringPointer("new-route")}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionCreateRejectsMalformedOrAmbiguousInput(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(*CreateSubscriptionInput)
		want   error
	}{
		{name: "missing owner", mutate: func(input *CreateSubscriptionInput) { input.Owner = "" }, want: ErrInvalidInput},
		{name: "comma joined owner", mutate: func(input *CreateSubscriptionInput) { input.Owner = "agent-a,agent-b" }, want: ErrInvalidInput},
		{name: "comma joined registration", mutate: func(input *CreateSubscriptionInput) { input.RegistrationID = "registration-a,registration-b" }, want: ErrInvalidInput},
		{name: "missing generation", mutate: func(input *CreateSubscriptionInput) { input.Generation = 0 }, want: ErrInvalidInput},
		{name: "missing scope", mutate: func(input *CreateSubscriptionInput) { input.Scope = WorkScope{} }, want: ErrInvalidInput},
		{name: "comma joined convoy", mutate: func(input *CreateSubscriptionInput) { input.Scope.ConvoyID = "convoy-a,convoy-b" }, want: ErrInvalidInput},
		{name: "missing route", mutate: func(input *CreateSubscriptionInput) { input.RouteIdentity = "" }, want: ErrInvalidInput},
		{name: "comma joined route", mutate: func(input *CreateSubscriptionInput) { input.RouteIdentity = "opaque-route-a,opaque-route-b" }, want: ErrInvalidInput},
		{name: "missing interests", mutate: func(input *CreateSubscriptionInput) { input.Interests = nil }, want: ErrInvalidInput},
		{name: "comma joined interest", mutate: func(input *CreateSubscriptionInput) {
			input.Interests = []LifecycleInterest{"convoy.created,convoy.closed"}
		}, want: ErrInvalidInput},
		{name: "duplicate interests", mutate: func(input *CreateSubscriptionInput) {
			input.Interests = []LifecycleInterest{"convoy.created", "convoy.created"}
		}, want: ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, store := testSubscriptionService()
			input := testSubscriptionInput(now)
			tc.mutate(&input)
			if _, err := service.Create(context.Background(), input); !errors.Is(err, tc.want) {
				t.Fatalf("Create error = %v, want %v", err, tc.want)
			}
			items, err := store.List(beads.ListQuery{Label: callbackSubscriptionLabel, IncludeClosed: true, AllowScan: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 0 {
				t.Fatalf("invalid create persisted %d beads", len(items))
			}
		})
	}
}

func TestSubscriptionMutationRequiresOwnerAndCurrentFence(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	patch := SubscriptionPatch{RouteIdentity: stringPointer("opaque-route-b")}

	if _, err := service.Get(context.Background(), "opaque-route-a", record.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("route possession Get error = %v, want %v", err, ErrUnauthorized)
	}
	if byRoute, err := service.List(context.Background(), "opaque-route-a"); err != nil {
		t.Fatal(err)
	} else if len(byRoute) != 0 {
		t.Fatalf("route possession List = %#v, want empty", byRoute)
	}
	if _, err := service.Update(context.Background(), "agent-b", record.ID, testFence(record), patch, now.Add(time.Minute)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-owner Update error = %v, want %v", err, ErrUnauthorized)
	}
	if _, err := service.Update(context.Background(), "agent-a", record.ID, RegistrationFence{}, patch, now.Add(time.Minute)); !errors.Is(err, ErrFenceRequired) {
		t.Fatalf("unfenced Update error = %v, want %v", err, ErrFenceRequired)
	}
	if _, err := service.Update(context.Background(), "agent-a", record.ID, RegistrationFence{RegistrationID: "wrong", Generation: 1}, patch, now.Add(time.Minute)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("wrong-registration Update error = %v, want %v", err, ErrStaleFence)
	}

	updated, err := service.Update(context.Background(), "agent-a", record.ID, testFence(record), patch, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != 2 || updated.RouteIdentity != "opaque-route-b" {
		t.Fatalf("updated = %#v, want generation 2 and new route", updated)
	}
	if _, err := service.Update(context.Background(), "agent-a", record.ID, testFence(record), patch, now.Add(2*time.Minute)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("stale Update error = %v, want %v", err, ErrStaleFence)
	}

	renewed, err := service.Renew(context.Background(), "agent-a", record.ID, testFence(updated), now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Generation != 3 || !renewed.UpdatedAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("renewed = %#v, want generation 3", renewed)
	}
	if _, err := service.Renew(context.Background(), "agent-a", record.ID, testFence(updated), now.Add(3*time.Minute)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("stale Renew error = %v, want %v", err, ErrStaleFence)
	}
}

func TestSubscriptionUpdateRejectsInvalidPatchWithoutMutation(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	cases := []SubscriptionPatch{
		{RouteIdentity: stringPointer("route-a,route-b")},
		{Interests: []LifecycleInterest{"convoy.created", "convoy.created"}},
		{Interests: []LifecycleInterest{}},
		{Scope: &WorkScope{}},
	}
	for _, patch := range cases {
		if _, err := service.Update(context.Background(), record.Owner, record.ID, testFence(record), patch, now.Add(time.Minute)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid patch %#v error = %v, want %v", patch, err, ErrInvalidInput)
		}
	}
	unchanged, err := service.Get(context.Background(), record.Owner, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Generation != record.Generation || unchanged.RouteIdentity != record.RouteIdentity {
		t.Fatalf("invalid patches mutated record: before=%#v after=%#v", record, unchanged)
	}
}

func TestSubscriptionCASRejectsStaleServiceInstance(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	store := beads.NewMemStore()
	first := NewSubscriptionService(store)
	second := NewSubscriptionService(store)
	record, err := first.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := second.Get(context.Background(), record.Owner, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Update(context.Background(), record.Owner, record.ID, testFence(record), SubscriptionPatch{RouteIdentity: stringPointer("route-first")}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Update(context.Background(), record.Owner, record.ID, testFence(snapshot), SubscriptionPatch{RouteIdentity: stringPointer("route-second")}, now.Add(2*time.Minute)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CAS conflict error = %v, want %v", err, ErrStaleFence)
	}
	current, err := first.Get(context.Background(), record.Owner, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.RouteIdentity != "route-first" || current.Generation != 2 {
		t.Fatalf("CAS conflict changed record: %#v", current)
	}
}

func TestSubscriptionRevokeAndUnregisterAreExplicitDurableTerminalStates(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, store := testSubscriptionService()
	revoked, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	revoked, err = service.Revoke(context.Background(), revoked.Owner, revoked.ID, testFence(revoked), "operator revoked", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if revoked.State != SubscriptionRevoked || revoked.RevocationReason != "operator revoked" || revoked.RevokedAt == nil {
		t.Fatalf("revoked = %#v, want explicit revocation", revoked)
	}
	if _, err := service.Update(context.Background(), revoked.Owner, revoked.ID, testFence(revoked), SubscriptionPatch{RouteIdentity: stringPointer("route-after-revoke")}, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("update after revoke error = %v, want %v", err, ErrInvalidState)
	}
	if replay, err := service.Revoke(context.Background(), revoked.Owner, revoked.ID, testFence(revoked), "operator revoked", now.Add(3*time.Minute)); err != nil || replay.State != SubscriptionRevoked {
		t.Fatalf("same-fence revoke replay = (%#v, %v), want idempotent terminal replay", replay, err)
	}
	if _, err := service.Unregister(context.Background(), revoked.Owner, revoked.ID, testFence(revoked), "remove revoked", now.Add(4*time.Minute)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unregister after revoke error = %v, want %v", err, ErrInvalidState)
	}

	unregistered, err := service.Create(context.Background(), testSubscriptionInput(now.Add(5*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	unregistered, err = service.Unregister(context.Background(), unregistered.Owner, unregistered.ID, testFence(unregistered), "registration ended", now.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if unregistered.State != SubscriptionUnregistered {
		t.Fatalf("unregistered state = %q, want unregistered", unregistered.State)
	}
	bead, err := store.Get(unregistered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bead.Status != "closed" {
		t.Fatalf("unregistered bead status = %q, want closed", bead.Status)
	}
	if replay, err := service.Unregister(context.Background(), unregistered.Owner, unregistered.ID, testFence(unregistered), "registration ended", now.Add(7*time.Minute)); err != nil || replay.State != SubscriptionUnregistered {
		t.Fatalf("same-fence unregister replay = (%#v, %v), want idempotent terminal replay", replay, err)
	}
	owned, err := service.List(context.Background(), "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 2 {
		t.Fatalf("owner list after terminal transitions = %#v, want both durable records", owned)
	}
}

func TestSubscriptionUnregisterRequiresFence(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Unregister(context.Background(), record.Owner, record.ID, RegistrationFence{}, "missing fence", now.Add(time.Minute)); !errors.Is(err, ErrFenceRequired) {
		t.Fatalf("unfenced Unregister error = %v, want %v", err, ErrFenceRequired)
	}
	if _, err := service.Revoke(context.Background(), record.Owner, record.ID, RegistrationFence{}, "missing fence", now.Add(time.Minute)); !errors.Is(err, ErrFenceRequired) {
		t.Fatalf("unfenced Revoke error = %v, want %v", err, ErrFenceRequired)
	}
	unchanged, err := service.Get(context.Background(), record.Owner, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.State != SubscriptionActive {
		t.Fatalf("unfenced unregister changed state to %q", unchanged.State)
	}
}

func TestSubscriptionFailsClosedWhenConditionalWritesUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	store := beads.NewMemStore()
	service := NewSubscriptionService(store)
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	store.DisableConditionalWrites = true
	if _, err := service.Update(context.Background(), record.Owner, record.ID, testFence(record), SubscriptionPatch{RouteIdentity: stringPointer("route-no-cas")}, now.Add(time.Minute)); !errors.Is(err, ErrFencingUnavailable) || !errors.Is(err, beads.ErrConditionalWriteUnsupported) {
		t.Fatalf("unavailable CAS Update error = %v, want fencing and store errors", err)
	}
	unchanged, err := service.Get(context.Background(), record.Owner, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Generation != record.Generation || unchanged.RouteIdentity != record.RouteIdentity {
		t.Fatalf("unavailable CAS mutated record: %#v", unchanged)
	}
}

func TestSubscriptionMalformedDurableRecordIsAnError(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, store := testSubscriptionService()
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMetadata(record.ID, subscriptionRecordMetadata, "{not-json"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(context.Background(), record.Owner, record.ID); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("malformed record error = %v, want %v", err, ErrCorruptRecord)
	}
}
