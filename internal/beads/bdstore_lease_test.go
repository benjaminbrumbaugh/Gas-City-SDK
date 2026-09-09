package beads

import (
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
	}, WithBdStoreLeaseRunner(func(dir, holder string, args ...string) ([]byte, error) {
		gotDir = dir
		gotHolder = holder
		gotArgs = append([]string(nil), args...)
		return []byte(`{"ok":true}`), nil
	}))

	if err := store.HeartbeatClaim("rig-claim", "instance-token"); err != nil {
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

func TestBdStoreReclaimExpiredClaimsUsesGraceAndReturnsCount(t *testing.T) {
	var gotHolder string
	var gotArgs []string
	store := NewBdStore("/city", nil, WithBdStoreLeaseRunner(func(_, holder string, args ...string) ([]byte, error) {
		gotHolder = holder
		gotArgs = append([]string(nil), args...)
		return []byte(`{"reclaimed":[{"id":"one"},{"id":"two"}]}`), nil
	}))

	got, err := store.ReclaimExpiredClaims(10*time.Minute, "worker", " worker ", "worker")
	if err != nil {
		t.Fatalf("ReclaimExpiredClaims() error = %v", err)
	}
	if got != 2 {
		t.Fatalf("ReclaimExpiredClaims() count = %d, want 2", got)
	}
	if gotHolder != "" {
		t.Fatalf("reclaim holder = %q, want empty local-replica reclaim holder", gotHolder)
	}
	wantArgs := []string{"reclaim", "--older-than", "10m0s", "--assignee", "worker", "--json"}
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
	store := NewBdStore("/city", nil, WithBdStoreLeaseRunner(func(_, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"count":0,"reclaimed":null,"schema_version":1,"scoped":false}`), nil
	}))

	got, err := store.ReclaimExpiredClaims(10 * time.Minute)
	if err != nil {
		t.Fatalf("ReclaimExpiredClaims() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("ReclaimExpiredClaims() count = %d, want 0", got)
	}
}

func TestBdStoreLeaseMethodsRefuseWithoutNativeLeaseRunner(t *testing.T) {
	store := NewBdStore("/city", nil)
	if err := store.HeartbeatClaim("claim", "token"); !errors.Is(err, ErrClaimLeaseUnsupported) {
		t.Fatalf("HeartbeatClaim() error = %v, want ErrClaimLeaseUnsupported", err)
	}
	if _, err := store.ReclaimExpiredClaims(time.Minute); !errors.Is(err, ErrClaimLeaseUnsupported) {
		t.Fatalf("ReclaimExpiredClaims() error = %v, want ErrClaimLeaseUnsupported", err)
	}
}
