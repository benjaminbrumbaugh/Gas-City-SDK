package sessionlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/pathutil"
)

// CodexQuotaProvider is the provider identity attached to Codex quota
// observations. It is fixed by this provider-specific parser, not inferred
// from account files or transcript prose.
const CodexQuotaProvider = "codex"

// CodexQuotaContext is the verified session context supplied by the session
// owner when a resolved Codex rollout is read. Empty AccountRef and Scope
// intentionally mean unknown; the extractor never derives either value.
type CodexQuotaContext struct {
	Provider   string
	SessionID  string
	WorkDir    string
	AccountRef string
	Scope      string
}

// CodexQuotaObservation is the latest structured rate-limit snapshot observed
// in one resolved Codex session log. It is descriptive data only: it does not
// classify exhaustion or grant permission to execute work.
type CodexQuotaObservation struct {
	Provider   string            `json:"provider"`
	SessionID  string            `json:"session_id"`
	WorkDir    string            `json:"work_dir,omitempty"`
	AccountRef string            `json:"account_ref,omitempty"`
	Scope      string            `json:"scope,omitempty"`
	Model      string            `json:"model,omitempty"`
	Effort     string            `json:"effort,omitempty"`
	ObservedAt time.Time         `json:"observed_at"`
	LimitID    string            `json:"limit_id,omitempty"`
	Primary    *CodexQuotaWindow `json:"primary,omitempty"`
	Secondary  *CodexQuotaWindow `json:"secondary,omitempty"`
}

// CodexQuotaWindow preserves one provider rate-limit window. Pointer fields
// retain provider-null and unknown reset/reached values instead of converting
// them into misleading zeroes.
type CodexQuotaWindow struct {
	UsedPercent   *float64 `json:"used_percent,omitempty"`
	WindowMinutes *int     `json:"window_minutes,omitempty"`
	ResetAtUnix   *int64   `json:"reset_at_unix,omitempty"`
	Reached       *bool    `json:"reached,omitempty"`
}

type codexQuotaPayload struct {
	Type            string           `json:"type"`
	Model           string           `json:"model"`
	Effort          string           `json:"effort"`
	ReasoningEffort string           `json:"reasoning_effort"`
	RateLimits      *codexRateLimits `json:"rate_limits"`
}

type codexRateLimits struct {
	LimitID   string                `json:"limit_id"`
	Primary   *codexRateLimitWindow `json:"primary"`
	Secondary *codexRateLimitWindow `json:"secondary"`
}

type codexRateLimitWindow struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes *int     `json:"window_minutes"`
	ResetsAt      *int64   `json:"resets_at"`
	Reached       *bool    `json:"reached"`
}

// ExtractCodexTailQuotaFromSearchPaths reads quota evidence only from the
// already-resolved managed Codex rollout at path. The path must be contained
// by one of searchPaths and its rollout filename must carry context.SessionID;
// this function does not discover other sessions or inspect credentials. It
// scans only the bounded tail window: a nil observation means no usable
// snapshot was present in that window, not that older evidence is absent.
func ExtractCodexTailQuotaFromSearchPaths(searchPaths []string, path string, context CodexQuotaContext) (*CodexQuotaObservation, error) {
	if err := validateCodexQuotaContext(context); err != nil {
		return nil, err
	}
	safePath, file, err := openValidatedCodexQuotaFile(mergeCodexSearchPaths(searchPaths), path)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // best-effort close on read-only file
	if sessionID, ok := codexRolloutFilenameSessionID(filepath.Base(safePath)); !ok || sessionID != context.SessionID {
		return nil, fmt.Errorf("resolved Codex rollout %q does not match session %q", safePath, context.SessionID)
	}
	candidate, ok := codexSessionCandidateFromFile(file, safePath)
	if !ok {
		return nil, fmt.Errorf("resolved Codex rollout %q is missing valid session metadata", safePath)
	}
	if candidate.sessionID == "" || candidate.sessionID != context.SessionID {
		return nil, fmt.Errorf("resolved Codex rollout %q metadata does not match session %q", safePath, context.SessionID)
	}
	if context.WorkDir != "" && !pathutil.SamePath(candidate.WorkDir, context.WorkDir) {
		return nil, fmt.Errorf("resolved Codex rollout %q does not match workdir %q", safePath, context.WorkDir)
	}
	return extractCodexTailQuotaFromFile(file, context)
}

func openValidatedCodexQuotaFile(searchPaths []string, path string) (string, *os.File, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil, fmt.Errorf("empty session log path")
	}
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", nil, fmt.Errorf("resolving session log path: %w", err)
	}
	file, err := os.Open(cleanPath)
	if err != nil {
		return "", nil, err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close() //nolint:errcheck // best-effort cleanup on validation failure
		return "", nil, fmt.Errorf("stating session log path: %w", err)
	}
	for _, root := range searchPaths {
		if strings.TrimSpace(root) == "" {
			continue
		}
		cleanRoot, err := filepath.Abs(filepath.Clean(root))
		if err != nil || pathutil.SamePath(cleanRoot, cleanPath) || !pathutil.PathWithin(cleanRoot, cleanPath) {
			continue
		}
		pathInfo, err := os.Stat(cleanPath)
		if err == nil && os.SameFile(fileInfo, pathInfo) {
			return cleanPath, file, nil
		}
	}
	file.Close() //nolint:errcheck // best-effort cleanup on validation failure
	return "", nil, fmt.Errorf("session log path is outside configured search paths")
}

func validateCodexQuotaContext(context CodexQuotaContext) error {
	if context.Provider != CodexQuotaProvider {
		return fmt.Errorf("codex quota provider must be %q", CodexQuotaProvider)
	}
	if strings.TrimSpace(context.SessionID) == "" {
		return fmt.Errorf("codex quota session ID is required")
	}
	if strings.Contains(context.SessionID, "..") || strings.ContainsAny(context.SessionID, `/\\`) {
		return fmt.Errorf("invalid Codex quota session ID %q", context.SessionID)
	}
	return nil
}

func extractCodexTailQuotaFromFile(f *os.File, context CodexQuotaContext) (*CodexQuotaObservation, error) {
	data, _, _, err := readTailWindow(f, tailChunkSize)
	if err != nil {
		return nil, err
	}

	var model, effort string
	var latest *CodexQuotaObservation
	for _, line := range splitLines(data) {
		var entry codexRawEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		var payload codexQuotaPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		switch entry.Type {
		case "turn_context":
			model = payload.Model
			effort = ""
			if payload.Effort != "" {
				effort = payload.Effort
			} else if payload.ReasoningEffort != "" {
				effort = payload.ReasoningEffort
			}
		case "event_msg":
			if payload.Type != "token_count" || payload.RateLimits == nil {
				continue
			}
			observedAt := parseCodexSessionTime(entry.Timestamp)
			if observedAt.IsZero() {
				continue
			}
			if latest != nil && observedAt.Before(latest.ObservedAt) {
				continue
			}
			latest = &CodexQuotaObservation{
				Provider:   context.Provider,
				SessionID:  context.SessionID,
				WorkDir:    context.WorkDir,
				AccountRef: context.AccountRef,
				Scope:      context.Scope,
				Model:      model,
				Effort:     effort,
				ObservedAt: observedAt,
				LimitID:    payload.RateLimits.LimitID,
				Primary:    cloneCodexQuotaWindow(payload.RateLimits.Primary),
				Secondary:  cloneCodexQuotaWindow(payload.RateLimits.Secondary),
			}
		}
	}
	return latest, nil
}

func cloneCodexQuotaWindow(window *codexRateLimitWindow) *CodexQuotaWindow {
	if window == nil {
		return nil
	}
	return &CodexQuotaWindow{
		UsedPercent:   cloneFloat64(window.UsedPercent),
		WindowMinutes: cloneInt(window.WindowMinutes),
		ResetAtUnix:   cloneInt64(window.ResetsAt),
		Reached:       cloneBool(window.Reached),
	}
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
