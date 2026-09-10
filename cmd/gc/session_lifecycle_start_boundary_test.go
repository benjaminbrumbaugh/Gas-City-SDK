package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/session/sessiontest"
)

func TestExecutePreparedStartWaveUsesWorkerBoundaryForKnownSession(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	mgr := newSessionManagerWithConfig("", store, sp, nil)
	info, err := mgr.CreateSession(context.Background(), sessionpkg.CreateOptions{BeadOnly: true, Template: "worker", Title: "Worker", Command: "claude", WorkDir: t.TempDir(), Provider: "claude", Transport: "", Resume: sessionpkg.ProviderResume{}})
	if err != nil {
		t.Fatalf("CreateBeadOnly: %v", err)
	}
	bead, err := store.Get(info.ID)
	if err != nil {
		t.Fatalf("Get bead: %v", err)
	}

	results := executePreparedStartWave(
		context.Background(),
		[]preparedStart{{
			candidate: startCandidate{
				info: sessiontest.SeedBead(t, bead),
				tp: TemplateParams{
					TemplateName:          "worker",
					ProviderFenceIdentity: "account:hmac-sha256:test-identity",
				},
			},
			cfg: runtime.Config{
				Command: "claude --resume seeded-session",
				WorkDir: info.WorkDir,
			},
		}},
		sp,
		store,
		10*time.Second,
	)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].err != nil {
		t.Fatalf("start result err = %v, want nil", results[0].err)
	}

	got, err := mgr.Get(info.ID)
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if got.State != sessionpkg.StateStartPending {
		t.Fatalf("state = %q, want %q before lifecycle commit", got.State, sessionpkg.StateStartPending)
	}
	updatedBead, err := store.Get(info.ID)
	if err != nil {
		t.Fatalf("Get updated bead: %v", err)
	}
	if updatedBead.Metadata["pending_create_claim"] != "true" {
		t.Fatalf("pending_create_claim = %q, want preserved before commit", updatedBead.Metadata["pending_create_claim"])
	}
	if got := updatedBead.Metadata["launch_provider_fence_identity"]; got != "account:hmac-sha256:test-identity" {
		t.Fatalf("launch_provider_fence_identity = %q, want persisted before provider start returns", got)
	}
	if !sp.IsRunning(info.SessionName) {
		t.Fatal("session should be running after prepared start")
	}
}

func TestStartPreparedStartCandidateUsesWorkerBoundaryForRuntimeOnlyTarget(t *testing.T) {
	sp := runtime.NewFake()
	cityPath := t.TempDir()

	usedWorker, err := startPreparedStartCandidate(
		context.Background(),
		preparedStart{
			candidate: startCandidate{
				info: sessionpkg.Info{SessionName: "legacy-runtime-only", SessionNameMetadata: "legacy-runtime-only"},
				tp:   TemplateParams{TemplateName: "worker"},
			},
			cfg: runtime.Config{
				Command: "claude --resume seeded",
				WorkDir: t.TempDir(),
			},
		},
		cityPath,
		nil,
		sp,
		nil,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("startPreparedStartCandidate: %v", err)
	}
	if !usedWorker {
		t.Fatal("usedWorker = false, want true")
	}
	if !sp.IsRunning("legacy-runtime-only") {
		t.Fatal("legacy-runtime-only should be running after prepared start")
	}
	var start runtime.Call
	foundStart := false
	for _, call := range sp.Calls {
		if call.Method == "Start" {
			start = call
			foundStart = true
			break
		}
	}
	if !foundStart {
		t.Fatalf("runtime calls = %#v, want Start", sp.Calls)
	}
	if start.Name != "legacy-runtime-only" {
		t.Fatalf("start name = %q, want legacy-runtime-only", start.Name)
	}
	if start.Config.Command != "claude --resume seeded" {
		t.Fatalf("start command = %q, want claude --resume seeded", start.Config.Command)
	}
	if _, err := os.Stat(filepath.Join(cityPath, ".gc", providerFenceIdentityKeyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("storeless runtime-only start invented durable account identity: %v", err)
	}
}

func TestStartPreparedStartCandidateClearsLaunchIdentityWhenStartFailsDead(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	mgr := newSessionManagerWithConfig("", store, sp, nil)
	info, err := mgr.CreateSession(context.Background(), sessionpkg.CreateOptions{
		BeadOnly: true,
		Template: "worker",
		Title:    "Worker",
		Command:  "claude",
		WorkDir:  t.TempDir(),
		Provider: "claude",
		ExtraMeta: map[string]string{
			"started_provider_fence_identity": "account:hmac-sha256:previous",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sp.StartErrors[info.SessionName] = errors.New("injected start failure")
	usedWorker, err := startPreparedStartCandidate(
		context.Background(),
		preparedStart{
			candidate: startCandidate{
				info: info,
				tp:   TemplateParams{TemplateName: "worker", ProviderFenceIdentity: "account:hmac-sha256:desired"},
			},
			cfg: runtime.Config{Command: "claude", WorkDir: info.WorkDir},
		},
		"",
		store,
		sp,
		nil,
		nil,
		nil,
		nil,
	)
	if !usedWorker || err == nil {
		t.Fatalf("start result usedWorker=%v err=%v, want worker failure", usedWorker, err)
	}
	row, getErr := store.Get(info.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got := row.Metadata["launch_provider_fence_identity"]; got != "" {
		t.Fatalf("failed dead start retained launch identity %q", got)
	}
	if got := row.Metadata["started_provider_fence_identity"]; got != "account:hmac-sha256:previous" {
		t.Fatalf("failed start changed prior attribution to %q", got)
	}
}

type providerWithoutDefinitiveAbsence struct {
	runtime.Provider
}

func TestStartPreparedStartCandidateRetainsLaunchIdentityWhenFailureAbsenceIsAmbiguous(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	mgr := newSessionManagerWithConfig("", store, sp, nil)
	info, err := mgr.CreateSession(context.Background(), sessionpkg.CreateOptions{
		BeadOnly: true,
		Template: "worker",
		Title:    "Worker",
		Command:  "claude",
		WorkDir:  t.TempDir(),
		Provider: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	sp.StartErrors[info.SessionName] = errors.New("ambiguous injected start failure")
	provider := providerWithoutDefinitiveAbsence{Provider: sp}
	usedWorker, err := startPreparedStartCandidate(
		context.Background(),
		preparedStart{
			candidate: startCandidate{
				info: info,
				tp:   TemplateParams{TemplateName: "worker", ProviderFenceIdentity: "account:hmac-sha256:desired"},
			},
			cfg: runtime.Config{Command: "claude", WorkDir: info.WorkDir},
		},
		"",
		store,
		provider,
		nil,
		nil,
		nil,
		nil,
	)
	if !usedWorker || err == nil {
		t.Fatalf("start result usedWorker=%v err=%v, want worker failure", usedWorker, err)
	}
	row, getErr := store.Get(info.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got := row.Metadata["launch_provider_fence_identity"]; got != "account:hmac-sha256:desired" {
		t.Fatalf("ambiguous failed start launch identity = %q, want retained desired attribution", got)
	}
}
