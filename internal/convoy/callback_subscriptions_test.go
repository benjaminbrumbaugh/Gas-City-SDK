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

// ownedSubscriptionInput builds a create input for owner with a distinct
// registration and route so cross-owner isolation is unambiguous.
func ownedSubscriptionInput(owner string, now time.Time) CreateSubscriptionInput {
	input := testSubscriptionInput(now)
	input.Owner = owner
	input.RegistrationID = "registration-" + owner
	input.RouteIdentity = "opaque-route-" + owner
	return input
}

// TestSubscriptionListIsolatesOwnersFromForeignCorruptRecords proves one
// owner cannot deny another owner's reads. A corrupt durable record must fail
// closed for its own owner while leaving every other owner's list intact;
// otherwise a single poisoned row is a cross-owner availability channel.
func TestSubscriptionListIsolatesOwnersFromForeignCorruptRecords(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, store := testSubscriptionService()
	first, err := service.Create(context.Background(), ownedSubscriptionInput("agent-a", now))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), ownedSubscriptionInput("agent-a", now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := service.Create(context.Background(), ownedSubscriptionInput("agent-b", now.Add(2*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMetadata(foreign.ID, subscriptionRecordMetadata, "{not-json"); err != nil {
		t.Fatal(err)
	}

	owned, err := service.List(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("List for uninvolved owner error = %v, want nil despite a foreign corrupt row", err)
	}
	if len(owned) != 2 || owned[0].ID != first.ID || owned[1].ID != second.ID {
		t.Fatalf("List = %#v, want exactly the two agent-a records in creation order", owned)
	}
	if _, err := service.List(context.Background(), "agent-b"); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("List for the poisoned owner error = %v, want %v", err, ErrCorruptRecord)
	}
}

// TestSubscriptionTerminalTransitionsFailClosedWithoutFencing proves revoke and
// unregister never fall back to an unconditional write. Losing the fence must
// surface as an explicit refusal, because a silent unfenced terminal write
// would let a stale actor retire a subscription it no longer owns.
func TestSubscriptionTerminalTransitionsFailClosedWithoutFencing(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	store := beads.NewMemStore()
	service := NewSubscriptionService(store)
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	store.DisableConditionalWrites = true

	if _, err := service.Revoke(context.Background(), record.Owner, record.ID, testFence(record), "operator revoked", now.Add(time.Minute)); !errors.Is(err, ErrFencingUnavailable) || !errors.Is(err, beads.ErrConditionalWriteUnsupported) {
		t.Fatalf("unfenced Revoke error = %v, want fencing and store errors", err)
	}
	if _, err := service.Unregister(context.Background(), record.Owner, record.ID, testFence(record), "registration ended", now.Add(time.Minute)); !errors.Is(err, ErrFencingUnavailable) || !errors.Is(err, beads.ErrConditionalWriteUnsupported) {
		t.Fatalf("unfenced Unregister error = %v, want fencing and store errors", err)
	}
	store.DisableConditionalWrites = false
	unchanged, err := service.Get(context.Background(), record.Owner, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.State != SubscriptionActive || unchanged.RevokedAt != nil || unchanged.UnregisteredAt != nil {
		t.Fatalf("refused terminal writes changed the record: %#v", unchanged)
	}
	bead, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bead.Status != "open" {
		t.Fatalf("refused unregister closed the bead: status = %q", bead.Status)
	}
}

// TestSubscriptionListValidatesOwnerBeforeReading distinguishes malformed owner
// input from a valid owner that simply holds no subscriptions.
func TestSubscriptionListValidatesOwnerBeforeReading(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	if _, err := service.Create(context.Background(), testSubscriptionInput(now)); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"", "   ", "agent-a,agent-b"} {
		if _, err := service.List(context.Background(), owner); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("List(%q) error = %v, want %v", owner, err, ErrInvalidInput)
		}
	}
	empty, err := service.List(context.Background(), "agent-unknown")
	if err != nil {
		t.Fatalf("List for a valid unknown owner error = %v, want nil", err)
	}
	if len(empty) != 0 {
		t.Fatalf("List for a valid unknown owner = %#v, want empty", empty)
	}
}

// TestSubscriptionTerminalReplayRequiresExactFenceAndReason pins the narrow
// idempotency window: only the exact owner, fence, and terminal reason replay
// safely. A stale actor must never be able to confirm someone else's terminal
// transition.
func TestSubscriptionTerminalReplayRequiresExactFenceAndReason(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := service.Revoke(context.Background(), record.Owner, record.ID, testFence(record), "operator revoked", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(context.Background(), revoked.Owner, revoked.ID, testFence(revoked), "different reason", now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replay with a different reason error = %v, want %v", err, ErrInvalidState)
	}
	staleFence := RegistrationFence{RegistrationID: revoked.RegistrationID, Generation: revoked.Generation + 1}
	if _, err := service.Revoke(context.Background(), revoked.Owner, revoked.ID, staleFence, "operator revoked", now.Add(3*time.Minute)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("replay with a stale fence error = %v, want %v", err, ErrStaleFence)
	}
	if _, err := service.Revoke(context.Background(), "agent-b", revoked.ID, testFence(revoked), "operator revoked", now.Add(4*time.Minute)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-owner replay error = %v, want %v", err, ErrUnauthorized)
	}
	current, err := service.Get(context.Background(), revoked.Owner, revoked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.RevocationReason != "operator revoked" || !current.RevokedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("rejected replays mutated the terminal record: %#v", current)
	}
}

// TestSubscriptionGenerationExhaustionFailsWithoutMutation proves the fence
// counter refuses to wrap, since a wrapped generation would let a retired
// fence authorize a mutation again.
func TestSubscriptionGenerationExhaustionFailsWithoutMutation(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	input := testSubscriptionInput(now)
	input.Generation = ^uint64(0)
	record, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Renew(context.Background(), record.Owner, record.ID, testFence(record), now.Add(time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("exhausted Renew error = %v, want %v", err, ErrInvalidInput)
	}
	unchanged, err := service.Get(context.Background(), record.Owner, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Generation != ^uint64(0) || !unchanged.UpdatedAt.Equal(now) {
		t.Fatalf("exhausted Renew mutated the record: %#v", unchanged)
	}
}

// TestSubscriptionResultsDoNotAliasDurableState proves returned records are
// deep copies, so a caller mutating its own copy cannot reach durable scope or
// interest data.
func TestSubscriptionResultsDoNotAliasDurableState(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	record.Scope.WorkRefs[0] = "hijacked"
	record.Interests[0] = "hijacked"

	reread, err := service.Get(context.Background(), record.Owner, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Scope.WorkRefs[0] != "work-a" || reread.Interests[0] != "convoy.created" {
		t.Fatalf("durable record aliased caller memory: %#v", reread)
	}
	reread.Scope.WorkRefs[0] = "hijacked-again"
	listed, err := service.List(context.Background(), record.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Scope.WorkRefs[0] != "work-a" {
		t.Fatalf("List aliased caller memory: %#v", listed)
	}
}

// TestSubscriptionCrossOwnerReadsDoNotLeakTerminalRecords proves an
// unregistered (closed) record is still owner-fenced rather than falling back
// to a not-found disclosure difference.
func TestSubscriptionCrossOwnerReadsDoNotLeakTerminalRecords(t *testing.T) {
	now := time.Date(2026, 9, 8, 17, 20, 0, 0, time.UTC)
	service, _ := testSubscriptionService()
	record, err := service.Create(context.Background(), testSubscriptionInput(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Unregister(context.Background(), record.Owner, record.ID, testFence(record), "registration ended", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(context.Background(), "agent-b", record.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-owner Get of a closed record error = %v, want %v", err, ErrUnauthorized)
	}
	if listed, err := service.List(context.Background(), "agent-b"); err != nil {
		t.Fatal(err)
	} else if len(listed) != 0 {
		t.Fatalf("cross-owner List of a closed record = %#v, want empty", listed)
	}
}
