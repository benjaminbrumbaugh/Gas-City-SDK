package beads

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBdStoreHeartbeatClaimUsesNativeLeaseRunner(t *testing.T) {
	var gotDir, gotHolder string
	var gotArgs []string
	store := NewBdStore("/rig", func(string, string, ...string) ([]byte, error) {
		return nil, errors.New("ordinary store runner must not handle leases")
	}, WithBdStoreLeaseRunner(func(_ context.Context, dir, holder string, args ...string) ([]byte, error) {
		gotDir = dir
		gotHolder = holder
		gotArgs = append([]string(nil), args...)
		return []byte(`{"ok":true}`), nil
	}))

	if err := store.HeartbeatClaim(context.Background(), "rig-claim", "instance-token"); err != nil {
		t.Fatalf("HeartbeatClaim() error = %v", err)
	}
	if gotDir != "/rig" || gotHolder != "instance-token" {
		t.Fatalf("lease runner identity = (%q, %q), want (%q, %q)", gotDir, gotHolder, "/rig", "instance-token")
	}
	wantArgs := []string{"heartbeat", "rig-claim", "--json"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("lease args = %v, want %v", gotArgs, wantArgs)
	}
}

func TestBdStoreLeaseReadsUseBoundedNativeRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls [][]string
	store := NewBdStore("/rig", nil, WithBdStoreLeaseRunner(func(gotCtx context.Context, dir, holder string, args ...string) ([]byte, error) {
		if gotCtx != ctx || dir != "/rig" || holder != "" {
			t.Fatalf("lease read identity = (%v, %q, %q), want caller context, /rig, empty holder", gotCtx, dir, holder)
		}
		calls = append(calls, append([]string(nil), args...))
		return []byte(`[{"id":"row","title":"row","status":"in_progress","issue_type":"task"}]`), nil
	}))

	claims, err := store.ListInProgressClaims(ctx)
	if err != nil || len(claims) != 1 || claims[0].ID != "row" {
		t.Fatalf("ListInProgressClaims() = (%#v, %v), want row", claims, err)
	}
	sessions, err := store.ListOpenSessionRows(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].ID != "row" {
		t.Fatalf("ListOpenSessionRows() = (%#v, %v), want row", sessions, err)
	}
	owner, err := store.GetLeaseRow(ctx, "row")
	if err != nil || owner.ID != "row" {
		t.Fatalf("GetLeaseRow() = (%#v, %v), want exact row", owner, err)
	}
	want := [][]string{
		{"list", "--json", "--include-infra", "--include-gates", "--include-templates", "--limit", "0", "--status", "in_progress"},
		{"list", "--json", "--include-infra", "--include-gates", "--include-templates", "--limit", "0", "--status", "open,in_progress", "--label", "gc:session"},
		{"show", "--json", "row"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("lease read args = %#v, want %#v", calls, want)
	}
}

func TestBdStoreGetLeaseRowRejectsNonExactResolution(t *testing.T) {
	store := NewBdStore("/city", nil, WithBdStoreLeaseRunner(func(context.Context, string, string, ...string) ([]byte, error) {
		return []byte(`[{"id":"prefix-other","title":"row","status":"closed","issue_type":"session"}]`), nil
	}))

	if _, err := store.GetLeaseRow(context.Background(), "prefix"); !errors.Is(err, ErrIDCollision) {
		t.Fatalf("GetLeaseRow() error = %v, want ErrIDCollision", err)
	}
}

func TestBdStoreLeaseReadsPropagateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := NewBdStore("/city", nil, WithBdStoreLeaseRunner(func(gotCtx context.Context, _ string, _ string, _ ...string) ([]byte, error) {
		return nil, gotCtx.Err()
	}))

	if _, err := store.ListInProgressClaims(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListInProgressClaims() error = %v, want context.Canceled", err)
	}
	if _, err := store.ListOpenSessionRows(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListOpenSessionRows() error = %v, want context.Canceled", err)
	}
	if _, err := store.GetLeaseRow(ctx, "owner"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetLeaseRow() error = %v, want context.Canceled", err)
	}
}

func TestBdStoreReclaimExpiredClaimsUsesGraceAndReturnsCount(t *testing.T) {
	var gotHolder string
	var gotArgs []string
	store := NewBdStore("/city", nil, WithBdStoreLeaseRunner(func(_ context.Context, _ string, holder string, args ...string) ([]byte, error) {
		gotHolder = holder
		gotArgs = append([]string(nil), args...)
		return []byte(`{"reclaimed":[{"id":"one"},{"id":"two"}]}`), nil
	}))

	got, err := store.ReclaimExpiredClaims(context.Background(), 10*time.Minute, ClaimLeaseReclaimScope{
		ClaimIDs:  []string{"claim-two", "claim-one", " claim-one "},
		Assignees: []string{"worker", " worker ", "worker"},
	})
	if err != nil {
		t.Fatalf("ReclaimExpiredClaims() error = %v", err)
	}
	if got != 2 {
		t.Fatalf("ReclaimExpiredClaims() count = %d, want 2", got)
	}
	if gotHolder != "" {
		t.Fatalf("reclaim holder = %q, want empty local-replica reclaim holder", gotHolder)
	}
	wantArgs := []string{"reclaim", "--older-than", "10m0s", "--assignee", "worker", "--id", "claim-one", "--id", "claim-two", "--json"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("reclaim args = %v, want %v", gotArgs, wantArgs)
	}
	for _, arg := range gotArgs {
		if arg == "--any-replica" {
			t.Fatal("reclaim passed --any-replica, which can mutate another replica's live claims")
		}
	}
}

func TestBdStoreReclaimExpiredClaimsAcceptsEmptyNativeResult(t *testing.T) {
	store := NewBdStore("/city", nil, WithBdStoreLeaseRunner(func(context.Context, string, string, ...string) ([]byte, error) {
		return []byte(`{"count":0,"reclaimed":null,"schema_version":1,"scoped":false}`), nil
	}))

	got, err := store.ReclaimExpiredClaims(context.Background(), 10*time.Minute, ClaimLeaseReclaimScope{ClaimIDs: []string{"claim"}, Assignees: []string{"worker"}})
	if err != nil {
		t.Fatalf("ReclaimExpiredClaims() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("ReclaimExpiredClaims() count = %d, want 0", got)
	}
}

func TestBdStoreReclaimExpiredClaimsRequiresExactBoundary(t *testing.T) {
	called := false
	store := NewBdStore("/city", nil, WithBdStoreLeaseRunner(func(context.Context, string, string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	}))

	for _, scope := range []ClaimLeaseReclaimScope{
		{Assignees: []string{"worker"}},
		{ClaimIDs: []string{"claim"}},
	} {
		if _, err := store.ReclaimExpiredClaims(context.Background(), 10*time.Minute, scope); err == nil {
			t.Fatalf("ReclaimExpiredClaims(%#v) succeeded without an exact AND boundary", scope)
		}
	}
	if called {
		t.Fatal("invalid reclaim boundary reached native runner")
	}
}

func TestBdStoreLeaseMethodsRefuseWithoutNativeLeaseRunner(t *testing.T) {
	store := NewBdStore("/city", nil)
	if _, err := store.ListInProgressClaims(context.Background()); !errors.Is(err, ErrClaimLeaseUnsupported) {
		t.Fatalf("ListInProgressClaims() error = %v, want ErrClaimLeaseUnsupported", err)
	}
	if _, err := store.ListOpenSessionRows(context.Background()); !errors.Is(err, ErrClaimLeaseUnsupported) {
		t.Fatalf("ListOpenSessionRows() error = %v, want ErrClaimLeaseUnsupported", err)
	}
	if _, err := store.GetLeaseRow(context.Background(), "owner"); !errors.Is(err, ErrClaimLeaseUnsupported) {
		t.Fatalf("GetLeaseRow() error = %v, want ErrClaimLeaseUnsupported", err)
	}
	if err := store.HeartbeatClaim(context.Background(), "claim", "token"); !errors.Is(err, ErrClaimLeaseUnsupported) {
		t.Fatalf("HeartbeatClaim() error = %v, want ErrClaimLeaseUnsupported", err)
	}
	if _, err := store.ReclaimExpiredClaims(context.Background(), time.Minute, ClaimLeaseReclaimScope{}); !errors.Is(err, ErrClaimLeaseUnsupported) {
		t.Fatalf("ReclaimExpiredClaims() error = %v, want ErrClaimLeaseUnsupported", err)
	}
}
