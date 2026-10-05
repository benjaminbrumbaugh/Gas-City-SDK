package sessionlog

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestExtractCodexTailQuotaFromSearchPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "2026", "10", "05", "rollout-2026-10-05T12-00-00-session-1.jsonl")
	fixture := filepath.Join("testdata", "codex", "quota-refresh.jsonl")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", fixture, err)
	}
	writeCodexUsageLines(t, path, []string{string(data)})

	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, CodexQuotaContext{
		Provider:   CodexQuotaProvider,
		SessionID:  "session-1",
		WorkDir:    "/work/dir",
		AccountRef: "acct-known",
		Scope:      "account:acct-known",
	})
	if err != nil {
		t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
	}
	if observation == nil {
		t.Fatal("observation = nil, want latest structured quota evidence")
	}

	if observation.Provider != CodexQuotaProvider || observation.SessionID != "session-1" {
		t.Errorf("context = (%q, %q), want (%q, session-1)", observation.Provider, observation.SessionID, CodexQuotaProvider)
	}
	if observation.AccountRef != "acct-known" || observation.Scope != "account:acct-known" {
		t.Errorf("scope = (%q, %q), want caller-verified context", observation.AccountRef, observation.Scope)
	}
	if got, want := observation.Model, "gpt-5.6"; got != want {
		t.Errorf("Model = %q, want %q", got, want)
	}
	if got, want := observation.Effort, "xhigh"; got != want {
		t.Errorf("Effort = %q, want %q", got, want)
	}
	if got, want := observation.ObservedAt, time.Date(2026, 10, 5, 19, 0, 2, 0, time.UTC); !got.Equal(want) {
		t.Errorf("ObservedAt = %s, want %s", got, want)
	}
	if got, want := observation.LimitID, "codex"; got != want {
		t.Errorf("LimitID = %q, want %q", got, want)
	}
	if observation.Primary == nil || observation.Secondary == nil {
		t.Fatalf("windows = %#v, want primary and secondary windows", observation)
	}
	if got, want := *observation.Primary.UsedPercent, 100.0; got != want {
		t.Errorf("Primary.UsedPercent = %v, want %v", got, want)
	}
	if got, want := *observation.Primary.WindowMinutes, 300; got != want {
		t.Errorf("Primary.WindowMinutes = %d, want %d", got, want)
	}
	if observation.Primary.ResetAtUnix != nil {
		t.Errorf("Primary.ResetAtUnix = %v, want nil for provider null", *observation.Primary.ResetAtUnix)
	}
	if observation.Primary.Reached == nil || !*observation.Primary.Reached {
		t.Errorf("Primary.Reached = %v, want true", observation.Primary.Reached)
	}
	if got, want := *observation.Secondary.UsedPercent, 37.5; got != want {
		t.Errorf("Secondary.UsedPercent = %v, want %v", got, want)
	}
	if got, want := *observation.Secondary.WindowMinutes, 10080; got != want {
		t.Errorf("Secondary.WindowMinutes = %d, want %d", got, want)
	}
	if observation.Secondary.ResetAtUnix != nil || observation.Secondary.Reached != nil {
		t.Errorf("Secondary nullable fields = (%v, %v), want both nil", observation.Secondary.ResetAtUnix, observation.Secondary.Reached)
	}
}

func TestExtractCodexTailQuotaReminderOnlyReturnsNoEvidence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2026-10-05T18:59:00Z", "/work/dir"),
		`{"timestamp":"2026-10-05T18:59:01Z","type":"event_msg","payload":{"type":"agent_message","message":"You have 2 usage limit resets available. Run /usage to use one."}}`,
	})

	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, validCodexQuotaContext())
	if err != nil {
		t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
	}
	if observation != nil {
		t.Fatalf("observation = %#v, want nil for reminder-only transcript", observation)
	}
}

func TestExtractCodexTailQuotaUsesLatestValidSnapshotAfterMalformedInput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", "/work/dir"),
		codexTurnContextLine("2026-10-05T18:58:01Z", "gpt-5.5"),
		`{"timestamp":"2026-10-05T18:58:02Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":11,"window_minutes":300,"resets_at":1776394093,"reached":false}}}}`,
		`{"timestamp":"2026-10-05T18:58:03Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":22,"window_minutes":300,"resets_at":1776394094,"reached":false}}}}`,
		`{"timestamp":"2026-10-05T18:58:02.500Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":99,"window_minutes":300,"resets_at":1776394095,"reached":true}}}}`,
		`{"timestamp":"2026-10-05T18:58:04Z"`,
	})

	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, validCodexQuotaContext())
	if err != nil {
		t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
	}
	if observation == nil || observation.Primary == nil {
		t.Fatalf("observation = %#v, want latest valid snapshot", observation)
	}
	if got, want := *observation.Primary.UsedPercent, 22.0; got != want {
		t.Errorf("Primary.UsedPercent = %v, want latest snapshot %v", got, want)
	}
	if got, want := observation.ObservedAt, time.Date(2026, 10, 5, 18, 58, 3, 0, time.UTC); !got.Equal(want) {
		t.Errorf("ObservedAt = %s, want %s", got, want)
	}
}

func TestExtractCodexTailQuotaPreservesStaleObservationTimestamp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2024-01-01T00:00:00Z", "/work/dir"),
		`{"timestamp":"2024-01-01T00:00:01Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":100,"window_minutes":300,"resets_at":1704067200,"reached":true}}}}`,
	})

	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, validCodexQuotaContext())
	if err != nil {
		t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
	}
	if observation == nil {
		t.Fatal("observation = nil, want stale snapshot preserved for caller freshness policy")
	}
	if got, want := observation.ObservedAt, time.Date(2024, 1, 1, 0, 0, 1, 0, time.UTC); !got.Equal(want) {
		t.Errorf("ObservedAt = %s, want preserved stale timestamp %s", got, want)
	}
}

func TestExtractCodexTailQuotaLeavesUnknownContextEmpty(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", "/work/dir"),
		`{"timestamp":"2026-10-05T18:58:02Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":null,"window_minutes":null,"resets_at":null,"reached":null}}}}`,
	})

	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, validCodexQuotaContextWithoutWorkDir())
	if err != nil {
		t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
	}
	if observation == nil || observation.Primary == nil {
		t.Fatalf("observation = %#v, want unknown primary window", observation)
	}
	if observation.Model != "" || observation.Effort != "" || observation.AccountRef != "" || observation.Scope != "" {
		t.Errorf("unknown context = %#v, want empty model/effort/account/scope", observation)
	}
	if observation.Primary.UsedPercent != nil || observation.Primary.WindowMinutes != nil || observation.Primary.ResetAtUnix != nil || observation.Primary.Reached != nil {
		t.Errorf("unknown window = %#v, want nullable fields preserved as nil", observation.Primary)
	}
}

func TestExtractCodexTailQuotaRequiresVerifiedContextAndContainedPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		`{"timestamp":"2026-10-05T18:58:02Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":100,"window_minutes":300,"reached":true}}}}`,
	})

	for _, test := range []struct {
		name    string
		context CodexQuotaContext
	}{
		{name: "missing provider", context: CodexQuotaContext{SessionID: "session-1"}},
		{name: "wrong provider", context: CodexQuotaContext{Provider: "other", SessionID: "session-1"}},
		{name: "missing session", context: CodexQuotaContext{Provider: CodexQuotaProvider}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, test.context); err == nil {
				t.Fatal("expected missing or mismatched verified context to fail")
			}
		})
	}

	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	writeCodexUsageLines(t, outside, []string{})
	if _, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, outside, validCodexQuotaContext()); err == nil {
		t.Fatal("path outside managed Codex search roots must be rejected")
	}
}

func validCodexQuotaContext() CodexQuotaContext {
	return CodexQuotaContext{Provider: CodexQuotaProvider, SessionID: "session-1", WorkDir: "/work/dir"}
}

func validCodexQuotaContextWithoutWorkDir() CodexQuotaContext {
	return CodexQuotaContext{Provider: CodexQuotaProvider, SessionID: "session-1"}
}

func TestCodexQuotaReplayFixtureIsProviderFree(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	fixture := filepath.Join(filepath.Dir(filename), "testdata", "codex", "quota-refresh.jsonl")
	if _, err := os.Stat(fixture); err != nil {
		t.Fatalf("replay fixture missing: %v", err)
	}
}
