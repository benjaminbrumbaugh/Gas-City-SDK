package convoysubscription

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/session"
)

var operationNow = time.Date(2026, 10, 7, 18, 0, 0, 0, time.UTC)

func TestOperationContractUsesPinnedNeutralWireShape(t *testing.T) {
	request := SubscribeRequest{
		SchemaVersion:   ContractVersion,
		SessionSelector: "ambient-session",
		ConversationRef: "opaque-conversation-ref",
		Scope:           convoy.WorkScope{ConvoyID: "convoy-1"},
		Interests:       []convoy.LifecycleInterest{"convoy.closed"},
		RegistrationID:  "registration-1",
		Generation:      4,
		LeaseExpiresAt:  operationNow.Add(10 * time.Minute),
		Default:         true,
		Now:             operationNow,
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(encoded)
	for _, field := range []string{
		`"schema_version":"convoy-subscription.v1"`,
		`"session_selector":"ambient-session"`,
		`"conversation_ref":"opaque-conversation-ref"`,
		`"registration_id":"registration-1"`,
		`"generation":4`,
		`"default":true`,
	} {
		if !strings.Contains(wire, field) {
			t.Errorf("wire = %s, missing %s", wire, field)
		}
	}
	for _, forbidden := range []string{"owner_session_id", "authorization", "access_token", "callback_url"} {
		if strings.Contains(wire, forbidden) {
			t.Errorf("wire = %s, contains forbidden authority/transport field %q", wire, forbidden)
		}
	}
	if strings.Contains(wire, `"now"`) {
		t.Errorf("wire = %s, contains test/runtime clock", wire)
	}
}

func TestResolveLocalOwnerCanonicalizesOneOpenSession(t *testing.T) {
	store := beads.NewMemStore()
	created, err := store.Create(beads.Bead{
		Type:   session.BeadType,
		Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"session_name": "canonical-session",
			"alias":        "ambient-session",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	owner, err := ResolveLocalOwner(context.Background(), store, "ambient-session")
	if err != nil {
		t.Fatal(err)
	}
	if owner.Kind != OwnerLocalSession || owner.SessionID != created.ID {
		t.Fatalf("owner = %#v, want canonical open session %q", owner, created.ID)
	}

	if _, err := ResolveLocalOwner(context.Background(), store, created.ID); err != nil {
		t.Fatalf("canonical ID resolution = %v", err)
	}
	if err := store.Close(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLocalOwner(context.Background(), store, created.ID); !errors.Is(err, ErrOwnerNotOpen) {
		t.Fatalf("closed canonical session error = %v, want ErrOwnerNotOpen", err)
	}
}

func TestResolveLocalOwnerRejectsAmbiguousSelector(t *testing.T) {
	store := beads.NewMemStore()
	for range 2 {
		if _, err := store.Create(beads.Bead{
			Type:     session.BeadType,
			Status:   "open",
			Labels:   []string{session.LabelSession},
			Metadata: map[string]string{"session_name": "ambiguous"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ResolveLocalOwner(context.Background(), store, "ambiguous"); !errors.Is(err, session.ErrAmbiguous) {
		t.Fatalf("ambiguous selector error = %v, want session.ErrAmbiguous", err)
	}
}

func TestServiceSubscribeListRenewRetireOwnSession(t *testing.T) {
	ctx := context.Background()
	store := beads.NewMemStore()
	sessionBead, err := store.Create(beads.Bead{
		Type:     session.BeadType,
		Status:   "open",
		Labels:   []string{session.LabelSession},
		Metadata: map[string]string{"session_name": "own-session"},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, store, func(context.Context, string) (bool, error) { return false, nil })
	created, err := service.Subscribe(ctx, SubscribeRequest{
		SchemaVersion:   ContractVersion,
		SessionSelector: "own-session",
		ConversationRef: "opaque-conversation-ref",
		Scope:           convoy.WorkScope{ConvoyID: "convoy-1"},
		Interests:       []convoy.LifecycleInterest{"convoy.closed"},
		RegistrationID:  "registration-1",
		Generation:      4,
		LeaseExpiresAt:  operationNow.Add(10 * time.Minute),
		Default:         true,
		Now:             operationNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Acknowledged || created.Status != StatusSubscribed || created.Owner.SessionID != sessionBead.ID {
		t.Fatalf("subscribe result = %#v", created)
	}
	if created.SubscriptionID == "" || created.Fence.Generation != 4 || !created.Default {
		t.Fatalf("subscribe result fence/identity = %#v", created)
	}

	listed, err := service.List(ctx, ListRequest{SchemaVersion: ContractVersion, OwnerSessionID: sessionBead.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Subscriptions) != 1 || listed.Subscriptions[0].ID != created.SubscriptionID {
		t.Fatalf("list result = %#v", listed)
	}
	if listed.Subscriptions[0].LeaseExpiresAt == nil || !listed.Subscriptions[0].Default {
		t.Fatalf("list record lease/default = %#v", listed.Subscriptions[0])
	}

	renewed, err := service.Renew(ctx, RenewRequest{
		SchemaVersion:  ContractVersion,
		OwnerSessionID: sessionBead.ID,
		SubscriptionID: created.SubscriptionID,
		Fence:          created.Fence,
		LeaseExpiresAt: operationNow.Add(20 * time.Minute),
		Now:            operationNow.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Status != StatusRenewed || renewed.Fence.Generation != 5 || !renewed.Acknowledged {
		t.Fatalf("renew result = %#v", renewed)
	}

	retired, err := service.Retire(ctx, RetireRequest{
		SchemaVersion:  ContractVersion,
		OwnerSessionID: sessionBead.ID,
		SubscriptionID: created.SubscriptionID,
		Fence:          renewed.Fence,
		Reason:         "session ended",
		Now:            operationNow.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if retired.Status != StatusRetired || !retired.Acknowledged {
		t.Fatalf("retire result = %#v", retired)
	}
}

func TestServiceRejectsCrossOwnerAndStaleFence(t *testing.T) {
	store := beads.NewMemStore()
	ownerA := mustSession(t, store, "owner-a")
	ownerB := mustSession(t, store, "owner-b")
	service := NewService(store, store, func(context.Context, string) (bool, error) { return false, nil })
	created, err := service.Subscribe(context.Background(), validSubscribeRequest("owner-a"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Renew(context.Background(), RenewRequest{
		SchemaVersion:  ContractVersion,
		OwnerSessionID: ownerB.ID,
		SubscriptionID: created.SubscriptionID,
		Fence:          created.Fence,
		LeaseExpiresAt: operationNow.Add(time.Hour),
		Now:            operationNow.Add(time.Minute),
	})
	if !errors.Is(err, convoy.ErrUnauthorized) {
		t.Fatalf("cross-owner renew error = %v, want convoy.ErrUnauthorized", err)
	}

	_, err = service.Renew(context.Background(), RenewRequest{
		SchemaVersion:  ContractVersion,
		OwnerSessionID: ownerA.ID,
		SubscriptionID: created.SubscriptionID,
		Fence:          convoy.RegistrationFence{RegistrationID: "registration-1", Generation: 3},
		LeaseExpiresAt: operationNow.Add(time.Hour),
		Now:            operationNow.Add(time.Minute),
	})
	if !errors.Is(err, convoy.ErrStaleFence) {
		t.Fatalf("stale-fence renew error = %v, want convoy.ErrStaleFence", err)
	}
}

func TestServiceRequiresCanonicalOwnerAfterInitialResolution(t *testing.T) {
	store := beads.NewMemStore()
	owner := mustSession(t, store, "recycled-alias")
	if err := store.Update(owner.ID, beads.UpdateOpts{Metadata: map[string]string{"alias": "recycled-alias"}}); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, store, func(context.Context, string) (bool, error) { return false, nil })
	created, err := service.Subscribe(context.Background(), validSubscribeRequest("recycled-alias"))
	if err != nil {
		t.Fatal(err)
	}
	if created.Owner.SessionID != owner.ID {
		t.Fatalf("created owner = %#v, want %q", created.Owner, owner.ID)
	}

	_, err = service.List(context.Background(), ListRequest{SchemaVersion: ContractVersion, OwnerSessionID: "recycled-alias"})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("alias list error = %v, want exact-owner rejection", err)
	}
	_, err = service.Renew(context.Background(), RenewRequest{
		SchemaVersion:  ContractVersion,
		OwnerSessionID: "recycled-alias",
		SubscriptionID: created.SubscriptionID,
		Fence:          created.Fence,
		LeaseExpiresAt: operationNow.Add(2 * time.Hour),
		Now:            operationNow.Add(time.Minute),
	})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("alias renew error = %v, want exact-owner rejection", err)
	}
}

func TestServiceRefusesLateSubscriptionWhenConvoyIsTerminal(t *testing.T) {
	store := beads.NewMemStore()
	mustSession(t, store, "own-session")
	service := NewService(store, store, func(context.Context, string) (bool, error) { return true, nil })
	result, err := service.Subscribe(context.Background(), validSubscribeRequest("own-session"))
	if !errors.Is(err, ErrTerminalState) {
		t.Fatalf("terminal subscribe error = %v, want ErrTerminalState", err)
	}
	if result.Status != StatusTerminalRefused || result.Acknowledged || result.Refusal == nil {
		t.Fatalf("terminal result = %#v, want explicit refusal", result)
	}
	if result.Refusal.ConvoyID != "convoy-1" {
		t.Fatalf("terminal refusal = %#v, want convoy-1", result.Refusal)
	}
}

func TestServiceRefusesConvoySubscriptionWithoutTerminalAuthority(t *testing.T) {
	store := beads.NewMemStore()
	mustSession(t, store, "own-session")
	service := NewService(store, store, nil)
	result, err := service.Subscribe(context.Background(), validSubscribeRequest("own-session"))
	if !errors.Is(err, ErrTerminalStateUnavailable) {
		t.Fatalf("terminal checker error = %v, want ErrTerminalStateUnavailable", err)
	}
	if result.Acknowledged || result.Status != "" {
		t.Fatalf("result = %#v, want no acknowledgement", result)
	}
}

func TestExternalOwnerContractDoesNotPretendToAuthenticate(t *testing.T) {
	owner := Owner{Kind: OwnerExternalBinding, BindingRef: "configured-binding"}
	if err := owner.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Owner{Kind: OwnerExternalBinding, BindingRef: ""}).Validate(); err == nil {
		t.Fatal("external owner without configured binding was accepted")
	}
	if err := (Owner{Kind: OwnerLocalSession, SessionID: "session-a"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func validSubscribeRequest(selector string) SubscribeRequest {
	return SubscribeRequest{
		SchemaVersion:   ContractVersion,
		SessionSelector: selector,
		ConversationRef: "opaque-conversation-ref",
		Scope:           convoy.WorkScope{ConvoyID: "convoy-1"},
		Interests:       []convoy.LifecycleInterest{"convoy.closed"},
		RegistrationID:  "registration-1",
		Generation:      4,
		LeaseExpiresAt:  operationNow.Add(time.Hour),
		Now:             operationNow,
	}
}

func mustSession(t *testing.T, store beads.Store, name string) beads.Bead {
	t.Helper()
	created, err := store.Create(beads.Bead{
		Type:     session.BeadType,
		Status:   "open",
		Labels:   []string{session.LabelSession},
		Metadata: map[string]string{"session_name": name},
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}
