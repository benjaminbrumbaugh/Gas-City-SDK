package sessionlog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestExtractCodexTailQuotaUsesCanonicalWorkDirBinding(t *testing.T) {
	root := t.TempDir()
	realWorkDir := filepath.Join(root, "workspace")
	if err := os.Mkdir(realWorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedWorkDir := filepath.Join(root, "workspace-link")
	if err := os.Symlink(realWorkDir, linkedWorkDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", realWorkDir),
		codexTurnContextLine("2026-10-05T18:58:01Z", "model-a"),
		codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
	})

	for _, workDir := range []string{realWorkDir + string(os.PathSeparator), linkedWorkDir} {
		t.Run(workDir, func(t *testing.T) {
			observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, CodexQuotaContext{
				Provider:   CodexQuotaProvider,
				SessionID:  "session-1",
				WorkDir:    workDir,
				AccountRef: "acct-literal",
				Scope:      "scope-literal",
			})
			if err != nil {
				t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
			}
			if observation == nil {
				t.Fatal("observation = nil, want canonical workdir match")
			}
			if observation.AccountRef != "acct-literal" || observation.Scope != "scope-literal" {
				t.Fatalf("identity = (%q, %q), want literal caller context", observation.AccountRef, observation.Scope)
			}
		})
	}
}

func TestExtractCodexTailQuotaRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(link, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", "/work/dir"),
		codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
	})

	if _, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, validCodexQuotaContext()); err == nil {
		t.Fatal("symlink escape from managed search root must be rejected")
	}
}

func TestExtractCodexTailQuotaAcceptsSymlinkedSearchRoot(t *testing.T) {
	realRoot := t.TempDir()
	parent := t.TempDir()
	linkedRoot := filepath.Join(parent, "managed")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(linkedRoot, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", "/work/dir"),
		codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
	})

	if _, err := ExtractCodexTailQuotaFromSearchPaths([]string{linkedRoot}, path, validCodexQuotaContext()); err != nil {
		t.Fatalf("symlinked search root should retain canonical containment: %v", err)
	}
}

func TestExtractCodexTailQuotaRejectsEachIsolationGuard(t *testing.T) {
	root := t.TempDir()
	validContent := []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", "/work/dir"),
		codexTurnContextLine("2026-10-05T18:58:01Z", "model-a"),
		codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
	}

	tests := []struct {
		name       string
		path       string
		content    []string
		context    CodexQuotaContext
		searchPath string
	}{
		{
			name:       "valid rollout outside root",
			path:       filepath.Join(t.TempDir(), "rollout-2026-10-05T12-00-00-session-1.jsonl"),
			content:    validContent,
			context:    validCodexQuotaContext(),
			searchPath: root,
		},
		{
			name:       "filename session mismatch",
			path:       filepath.Join(root, "rollout-2026-10-05T12-00-00-session-2.jsonl"),
			content:    validContent,
			context:    validCodexQuotaContext(),
			searchPath: root,
		},
		{
			name: "missing session metadata",
			path: filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl"),
			content: []string{
				codexTurnContextLine("2026-10-05T18:58:01Z", "model-a"),
				codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
			},
			context:    validCodexQuotaContextWithoutWorkDir(),
			searchPath: root,
		},
		{
			name: "workdir mismatch",
			path: filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl"),
			content: []string{
				codexSessionMetaLine("2026-10-05T18:58:00Z", "/other/workdir"),
				codexTurnContextLine("2026-10-05T18:58:01Z", "model-a"),
				codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
			},
			context:    validCodexQuotaContext(),
			searchPath: root,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writeCodexUsageLines(t, test.path, test.content)
			if _, err := ExtractCodexTailQuotaFromSearchPaths([]string{test.searchPath}, test.path, test.context); err == nil {
				t.Fatal("independent isolation guard accepted invalid fixture")
			}
		})
	}
}

func TestValidateCodexQuotaContextRejectsPathLikeSessionIDs(t *testing.T) {
	for _, sessionID := range []string{"session..one", "session/one", `session\\one`} {
		t.Run(sessionID, func(t *testing.T) {
			if err := validateCodexQuotaContext(CodexQuotaContext{
				Provider:  CodexQuotaProvider,
				SessionID: sessionID,
			}); err == nil {
				t.Fatal("path-like session ID accepted")
			}
		})
	}
}

func TestExtractCodexTailQuotaResetsTurnEffort(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", "/work/dir"),
		`{"timestamp":"2026-10-05T18:58:01Z","type":"turn_context","payload":{"model":"model-a","effort":"xhigh"}}`,
		codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
		`{"timestamp":"2026-10-05T18:58:03Z","type":"turn_context","payload":{"model":"model-b"}}`,
		codexTokenCountLine("2026-10-05T18:58:04Z", 43, 43, 0, 0, 0),
	})

	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, validCodexQuotaContext())
	if err != nil {
		t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
	}
	if observation == nil {
		t.Fatal("observation = nil, want latest turn snapshot")
	}
	if observation.Model != "model-b" {
		t.Fatalf("Model = %q, want model-b", observation.Model)
	}
	if observation.Effort != "" {
		t.Fatalf("Effort = %q, want empty for later turn without effort", observation.Effort)
	}
}

func TestExtractCodexTailQuotaUsesReasoningEffortFallback(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", "/work/dir"),
		`{"timestamp":"2026-10-05T18:58:01Z","type":"turn_context","payload":{"model":"model-c","reasoning_effort":"medium"}}`,
		codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
	})

	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, validCodexQuotaContext())
	if err != nil {
		t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
	}
	if observation == nil || observation.Effort != "medium" {
		t.Fatalf("observation = %#v, want reasoning_effort fallback", observation)
	}
}

func TestExtractCodexTailQuotaReportsBoundedTailAbsenceAsUnknown(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	lines := []string{
		codexSessionMetaLine("2026-10-05T18:58:00Z", "/work/dir"),
		codexTokenCountLine("2026-10-05T18:58:02Z", 42, 42, 0, 0, 0),
	}
	for len(strings.Join(lines, "\n")) <= int(tailChunkSize)+1024 {
		lines = append(lines, `{"timestamp":"2026-10-05T18:58:03Z","type":"event_msg","payload":{"type":"agent_message","message":"filler"}}`)
	}
	writeCodexUsageLines(t, path, lines)

	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, validCodexQuotaContext())
	if err != nil {
		t.Fatalf("ExtractCodexTailQuotaFromSearchPaths: %v", err)
	}
	if observation != nil {
		t.Fatalf("observation = %#v, want nil when the only snapshot is outside the bounded tail", observation)
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
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("replay fixture missing: %v", err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "rollout-2026-10-05T12-00-00-session-1.jsonl")
	writeCodexUsageLines(t, path, []string{string(data)})
	observation, err := ExtractCodexTailQuotaFromSearchPaths([]string{root}, path, CodexQuotaContext{
		Provider:   CodexQuotaProvider,
		SessionID:  "session-1",
		WorkDir:    "/work/dir",
		AccountRef: "acct-known",
		Scope:      "account:acct-known",
	})
	if err != nil {
		t.Fatalf("provider-free replay extraction: %v", err)
	}
	if observation == nil || observation.Provider != CodexQuotaProvider || observation.Model != "gpt-5.6" || observation.Effort != "xhigh" {
		t.Fatalf("replay observation = %#v, want literal provider/model/effort", observation)
	}
	if observation.AccountRef != "acct-known" || observation.Scope != "account:acct-known" {
		t.Fatalf("replay identity = (%q, %q), want caller values", observation.AccountRef, observation.Scope)
	}
}
