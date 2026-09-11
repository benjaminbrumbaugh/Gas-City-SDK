package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
)

type staleProviderFenceStartReadStore struct {
	*beads.MemStore
	readCaptured chan struct{}
	releaseRead  chan struct{}
	readOnce     sync.Once
}

func (s *staleProviderFenceStartReadStore) Get(id string) (beads.Bead, error) {
	row, err := s.MemStore.Get(id)
	blocked := false
	s.readOnce.Do(func() {
		blocked = true
		close(s.readCaptured)
	})
	if blocked {
		<-s.releaseRead
	}
	return row, err
}

func TestProviderFenceStoreObservesCorruptNonOpenRowsButSkipsValidClosedHistory(t *testing.T) {
	mem := beads.NewMemStore()
	store := NewStore(beads.SessionStore{Store: mem})
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	validMetadata := map[string]string{
		"kind":                    providerFenceBeadKind,
		"provider_fence_identity": "account:closed-history",
		"fenced_until":            now.Add(time.Hour).Format(time.RFC3339),
		"observed_at":             now.Format(time.RFC3339),
	}
	closed, err := mem.Create(beads.Bead{
		Title: "valid closed history", Type: WaitBeadType,
		Labels: []string{ProviderFenceBeadLabel}, Metadata: validMetadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(closed.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ActiveProviderFences(now); err != nil || len(got) != 0 {
		t.Fatalf("valid closed history = %#v, %v; want inactive without error", got, err)
	}
	corrupt, err := mem.Create(beads.Bead{
		Title: "corrupt non-open fence", Type: WaitBeadType,
		Labels: []string{ProviderFenceBeadLabel}, Metadata: validMetadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	corruptStatus := "in_progress"
	if err := mem.Update(corrupt.ID, beads.UpdateOpts{Status: &corruptStatus}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveProviderFences(now); err == nil || !strings.Contains(err.Error(), corrupt.ID) {
		t.Fatalf("corrupt non-open fence error = %v, want operator-visible row %s", err, corrupt.ID)
	}
}

func TestProviderFenceLaunchClaimPreventsCrossManagerIdentityTheft(t *testing.T) {
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	mgrA := NewManagerWithOptions(mem, sp)
	mgrB := NewManagerWithOptions(mem, sp)
	info, err := mgrA.CreateSession(context.Background(), CreateOptions{
		BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	rowA, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim, alreadyRunning, err := mgrA.prepareProviderFenceStart(info.ID, &rowA, "account:winner")
	if err != nil {
		t.Fatal(err)
	}
	if alreadyRunning {
		t.Fatal("winner observed an unexpected running session")
	}
	if claim == "" {
		t.Fatal("winner did not acquire durable launch ownership")
	}
	defer mgrA.releaseProviderFenceLaunchClaim(info.ID, claim)

	rowB, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgrB.prepareProviderFenceStart(info.ID, &rowB, "account:winner"); err == nil {
		t.Fatal("second manager bypassed durable ownership for the same identity")
	}
	rowB, err = mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgrB.prepareProviderFenceStart(info.ID, &rowB, "account:loser"); err == nil {
		t.Fatal("losing manager stole in-flight provider account launch ownership")
	}
	current, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := current.Metadata["launch_provider_fence_identity"]; got != "account:winner" {
		t.Fatalf("launch identity = %q, want winner", got)
	}
}

func TestProviderFenceStartDoesNotAdoptUncommittedLiveRuntimeAfterClaim(t *testing.T) {
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	mgr := NewManagerWithOptions(mem, sp)
	info, err := mgr.CreateSession(context.Background(), CreateOptions{
		BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
		ExtraMeta: map[string]string{"state": string(StateSuspended)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Start(context.Background(), info.SessionName, runtime.Config{Command: "foreign-runtime"}); err != nil {
		t.Fatal(err)
	}
	row, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim, alreadyRunning, err := mgr.prepareProviderFenceStart(info.ID, &row, "account:caller")
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.releaseProviderFenceLaunchClaim(info.ID, claim)
	if alreadyRunning {
		t.Fatal("suspended row with an uncommitted live runtime was adopted as a completed prior start")
	}
	if got := row.Metadata["launch_provider_fence_identity"]; got != "account:caller" {
		t.Fatalf("launch identity = %q, want caller staged for guarded orphan cleanup/start", got)
	}
}

func TestProviderFenceStartDoesNotRelabelWinnerAfterPriorClaimRelease(t *testing.T) {
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	mgrA := NewManagerWithOptions(mem, sp)
	info, err := mgrA.CreateSession(context.Background(), CreateOptions{
		BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.SetMetadata(info.ID, "launch_provider_fence_identity", "account:loser"); err != nil {
		t.Fatal(err)
	}

	staleStore := &staleProviderFenceStartReadStore{
		MemStore:     mem,
		readCaptured: make(chan struct{}),
		releaseRead:  make(chan struct{}),
	}
	mgrB := NewManagerWithOptions(staleStore, sp)
	loserDone := make(chan error, 1)
	go func() {
		row, sessionName, err := mgrB.sessionBead(info.ID)
		if err == nil {
			err = mgrB.ensureRunning(context.Background(), info.ID, row, sessionName, "true", runtime.Config{ProviderFenceIdentity: "account:loser"})
		}
		loserDone <- err
	}()
	<-staleStore.readCaptured

	if err := mgrA.Start(context.Background(), info.ID, "true", runtime.Config{ProviderFenceIdentity: "account:winner"}); err != nil {
		t.Fatalf("winner Start: %v", err)
	}
	close(staleStore.releaseRead)
	if err := <-loserDone; err != nil {
		t.Fatalf("delayed concurrent Start: %v", err)
	}

	current, err := mem.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := current.Metadata["started_provider_fence_identity"]; got != "account:winner" {
		t.Fatalf("started provider identity = %q, want prior runtime winner after its claim was released", got)
	}
	if got := current.Metadata["launch_provider_fence_identity"]; got != "" {
		t.Fatalf("launch provider identity = %q, want no delayed staging left behind", got)
	}
}

func TestProviderFenceStartDoesNotClaimUnattributedLiveRuntime(t *testing.T) {
	mem := beads.NewMemStore()
	sp := runtime.NewFake()
	mgr := NewManagerWithOptions(mem, sp)
	info, err := mgr.CreateSession(context.Background(), CreateOptions{
		BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Start(context.Background(), info.SessionName, runtime.Config{Command: "winner"}); err != nil {
		t.Fatal(err)
	}

	err = mgr.Start(context.Background(), info.ID, "true", runtime.Config{ProviderFenceIdentity: "account:late-caller"})
	if err == nil || !strings.Contains(err.Error(), "live runtime has no authoritative provider account attribution") {
		t.Fatalf("Start error = %v, want unattributed-live-runtime refusal", err)
	}
	got, getErr := mgr.Get(info.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.State != StateStartPending {
		t.Fatalf("state = %q, want start_pending unchanged", got.State)
	}
	if got.StartedProviderFenceIdentity != "" || got.LaunchProviderFenceIdentity != "" {
		t.Fatalf("provider attribution changed: started=%q launch=%q", got.StartedProviderFenceIdentity, got.LaunchProviderFenceIdentity)
	}
	if !sp.IsRunning(info.SessionName) {
		t.Fatal("unattributed live runtime was stopped")
	}
}

func TestLegacyGlobalProviderFenceBlocksIdentityAwareDirectStarts(t *testing.T) {
	for _, mode := range []string{"resume", "create"} {
		t.Run(mode, func(t *testing.T) {
			mem := beads.NewMemStore()
			sp := runtime.NewFake()
			mgr := NewManagerWithOptions(mem, sp)
			now := time.Now().UTC()
			if err := NewStore(beads.SessionStore{Store: mem}).RecordProviderFence(
				"legacy:any-provider-account",
				now.Add(time.Hour),
				now,
				"usage_limit_modal",
			); err != nil {
				t.Fatal(err)
			}

			var err error
			switch mode {
			case "resume":
				var info Info
				info, err = mgr.CreateSession(context.Background(), CreateOptions{
					BeadOnly: true, Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
				})
				if err == nil {
					err = mgr.Start(context.Background(), info.ID, "true", runtime.Config{ProviderFenceIdentity: "account:current"})
				}
			case "create":
				_, err = mgr.CreateSession(context.Background(), CreateOptions{
					Template: "worker", Title: "worker", Command: "true", WorkDir: t.TempDir(),
					ExtraMeta: map[string]string{"launch_provider_fence_identity": "account:current"},
				})
			}
			if err == nil || !strings.Contains(err.Error(), "provider account is fenced until") {
				t.Fatalf("direct %s error = %v, want active legacy provider fence refusal", mode, err)
			}
			for _, call := range sp.Calls {
				if call.Method == "Start" {
					t.Fatalf("direct %s launched provider despite active legacy fence: %#v", mode, sp.Calls)
				}
			}
		})
	}
}
