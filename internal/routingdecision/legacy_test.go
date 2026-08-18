package routingdecision

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLegacyFixture(t *testing.T, cityRoot, decisionID, workID string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cityRoot, ".gc"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"schema":1,"intents":[{"schema":1,"intent_id":%q,"work_bead_id":%q,"city":"test-city","rig":"demo","target":"demo/reviewer","expected_status":"open","expected_assignee":"","expected_state_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","policy_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","observation_digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","model":"model-a","source":"governor-a","account":"account-a","approval_id":"legacy-approval-is-audit-only","created_at":"2026-08-07T19:00:00Z","expires_at":"2026-08-07T20:00:00Z","no_migration":true}]}`, decisionID, workID)
	path := filepath.Join(cityRoot, LegacyRelativePath)
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyImportIsExplicitAtomicProposedAndArchivesAfterCommit(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	cityRoot := t.TempDir()
	sourcePath := writeLegacyFixture(t, cityRoot, "old-1", "GC-OLD-1")
	source, err := LoadLegacySource(cityRoot)
	if err != nil {
		t.Fatalf("LoadLegacySource: %v", err)
	}
	store, err := OpenStore(cityRoot, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	payload := testDecisionPayload(t)
	payload.DecisionID = "imported-old-1"
	payload.WorkBeadID = "GC-OLD-1"
	payload.CreatedAt = now.Add(-time.Minute)
	payload.ExpiresAt = now.Add(time.Minute)
	payload.BindingID = BindingID(payload)

	result, err := store.ImportLegacy(source, []DecisionPayload{payload})
	if err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}
	if len(result.DecisionIDs) != 1 || result.DecisionIDs[0] != payload.DecisionID {
		t.Fatalf("import result = %+v", result)
	}
	record, err := store.Get(payload.DecisionID)
	if err != nil || record.State != StateProposed || record.Approval != nil || record.Signature != nil {
		t.Fatalf("imported record = (%+v, %v), want unsigned proposed", record, err)
	}
	if _, err := os.Lstat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("legacy source still exists after commit: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cityRoot, ".gc", result.ArchiveName)); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
}

func TestLegacyImportCommittedReceiptRetriesOnlyArchive(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	cityRoot := t.TempDir()
	writeLegacyFixture(t, cityRoot, "old-2", "GC-OLD-2")
	source, err := LoadLegacySource(cityRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(cityRoot, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	payload := testDecisionPayload(t)
	payload.DecisionID = "imported-old-2"
	payload.WorkBeadID = "GC-OLD-2"
	payload.BindingID = BindingID(payload)
	archiveName := LegacyArchiveName(source.Digest)
	archivePath := filepath.Join(cityRoot, ".gc", archiveName)
	if err := os.WriteFile(archivePath, []byte("collision"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportLegacy(source, []DecisionPayload{payload}); err == nil {
		t.Fatal("ImportLegacy succeeded despite archive collision")
	}
	if record, err := store.Get(payload.DecisionID); err != nil || record.State != StateProposed {
		t.Fatalf("database commit did not survive archive failure: (%+v, %v)", record, err)
	}
	if err := os.Remove(archivePath); err != nil {
		t.Fatal(err)
	}
	result, err := store.ImportLegacy(source, nil)
	if err != nil {
		t.Fatalf("receipt retry: %v", err)
	}
	if len(result.DecisionIDs) != 1 || result.DecisionIDs[0] != payload.DecisionID {
		t.Fatalf("receipt retry result = %+v", result)
	}
}

func TestLoadLegacySourceRejectsSymlinkAndAmbiguousJSON(t *testing.T) {
	cityRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(cityRoot, ".gc"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "routing-intents.json")
	if err := os.WriteFile(target, []byte(`{"schema":1,"intents":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(cityRoot, LegacyRelativePath)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacySource(cityRoot); err == nil {
		t.Fatal("LoadLegacySource accepted symlink")
	}

	ambiguousRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(ambiguousRoot, ".gc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ambiguousRoot, LegacyRelativePath), []byte(`{"schema":1,"schema":1,"intents":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacySource(ambiguousRoot); err == nil {
		t.Fatal("LoadLegacySource accepted duplicate JSON member")
	}
}

func TestLoadLegacySourceRejectsMalformedDigestAndAuditText(t *testing.T) {
	for name, mutation := range map[string]func(string) string{
		"short digest": func(payload string) string {
			return strings.Replace(payload, "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "sha256:bbbb", 1)
		},
		"control text": func(payload string) string {
			return strings.Replace(payload, `"source":"governor-a"`, `"source":"governor\na"`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cityRoot := t.TempDir()
			path := writeLegacyFixture(t, cityRoot, "old-invalid", "GC-INVALID")
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(mutation(string(payload))), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadLegacySource(cityRoot); err == nil {
				t.Fatalf("LoadLegacySource accepted %s", name)
			}
		})
	}
}
