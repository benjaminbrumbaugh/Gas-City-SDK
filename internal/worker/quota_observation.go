package worker

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/sessionlog"
)

// DefaultQuotaObservationTTL bounds how long a transcript quota snapshot can
// be used as current evidence. It is independent from provider reset times.
const DefaultQuotaObservationTTL = 5 * time.Minute

// QuotaWindow preserves one provider quota window without converting unknown
// values into zeroes or inferring capacity from a percentage.
type QuotaWindow struct {
	UsedPercent   *float64 `json:"used_percent,omitempty"`
	WindowMinutes *int     `json:"window_minutes,omitempty"`
	ResetAtUnix   *int64   `json:"reset_at_unix,omitempty"`
	Reached       *bool    `json:"reached,omitempty"`
}

// QuotaObservation is controller-owned, attributable quota evidence derived
// from one exact provider transcript. It is advisory input, not a gate.
type QuotaObservation struct {
	Provider    string       `json:"provider"`
	SessionID   string       `json:"session_id"`
	Incarnation string       `json:"incarnation"`
	WorkDir     string       `json:"work_dir,omitempty"`
	AccountRef  string       `json:"account_ref,omitempty"`
	Scope       string       `json:"scope,omitempty"`
	Model       string       `json:"model,omitempty"`
	Effort      string       `json:"effort,omitempty"`
	LimitID     string       `json:"limit_id,omitempty"`
	ObservedAt  time.Time    `json:"observed_at"`
	ExpiresAt   time.Time    `json:"expires_at"`
	Primary     *QuotaWindow `json:"primary,omitempty"`
	Secondary   *QuotaWindow `json:"secondary,omitempty"`
}

// IsHardExhausted reports only explicit provider reached evidence. A 100%
// usage value without reached=true remains descriptive and is not exhaustion.
func (o QuotaObservation) IsHardExhausted() bool {
	return quotaWindowReached(o.Primary) || quotaWindowReached(o.Secondary)
}

// IsFresh reports whether the observation is usable at now. Reset deadlines
// remain inside the windows; expiry is a separate freshness boundary.
func (o QuotaObservation) IsFresh(now time.Time) bool {
	return !o.ObservedAt.IsZero() && !o.ExpiresAt.IsZero() &&
		!now.Before(o.ObservedAt) && now.Before(o.ExpiresAt)
}

func quotaWindowReached(window *QuotaWindow) bool {
	return window != nil && window.Reached != nil && *window.Reached
}

// CodexQuotaObservation projects the passive Codex extractor into the worker
// boundary and attaches the current session incarnation. The caller must pass
// an exact path resolved for this session; this method performs no discovery.
func (a SessionLogAdapter) CodexQuotaObservation(info session.Info, path string, now time.Time) (*QuotaObservation, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("quota observation time is required")
	}
	if sessionlog.ProviderFamily(session.ProviderFamilyFromInfo(info, "")) != sessionlog.CodexQuotaProvider {
		return nil, nil
	}
	parsed, err := sessionlog.ExtractCodexTailQuotaFromSearchPaths(a.SearchPaths, path, sessionlog.CodexQuotaContext{
		Provider:   sessionlog.CodexQuotaProvider,
		SessionID:  strings.TrimSpace(info.SessionKey),
		WorkDir:    info.WorkDir,
		AccountRef: strings.TrimSpace(info.Provider),
		Scope:      quotaScope(info),
	})
	if err != nil {
		return nil, fmt.Errorf("observe Codex quota for session %q: %w", info.ID, err)
	}
	if parsed == nil {
		return nil, nil
	}
	observation := quotaObservationFromCodex(*parsed)
	observation.Incarnation = strings.TrimSpace(info.InstanceToken)
	observation.ExpiresAt = observation.ObservedAt.Add(DefaultQuotaObservationTTL)
	return &observation, nil
}

// ObserveCodexQuota resolves one exact transcript for info and projects its
// latest passive quota snapshot. Missing, unsupported, or ambiguous transcripts
// produce no observation; parser and path-validation failures are returned.
func ObserveCodexQuota(info session.Info, searchPaths []string, now time.Time) (*QuotaObservation, error) {
	if sessionlog.ProviderFamily(session.ProviderFamilyFromInfo(info, "")) != sessionlog.CodexQuotaProvider {
		return nil, nil
	}
	if len(searchPaths) == 0 {
		searchPaths = sessionlog.DefaultCodexSearchPaths()
	}
	path := session.ResolveKeyedTranscriptPath(info, searchPaths)
	if path == "" {
		return nil, nil
	}
	return (SessionLogAdapter{SearchPaths: searchPaths}).CodexQuotaObservation(info, path, now)
}

func quotaScope(info session.Info) string {
	provider := strings.TrimSpace(info.Provider)
	if provider == "" {
		return ""
	}
	return "account:" + provider
}

func quotaObservationFromCodex(parsed sessionlog.CodexQuotaObservation) QuotaObservation {
	return QuotaObservation{
		Provider:   parsed.Provider,
		SessionID:  parsed.SessionID,
		WorkDir:    parsed.WorkDir,
		AccountRef: parsed.AccountRef,
		Scope:      parsed.Scope,
		Model:      parsed.Model,
		Effort:     parsed.Effort,
		LimitID:    parsed.LimitID,
		ObservedAt: parsed.ObservedAt,
		Primary:    quotaWindowFromCodex(parsed.Primary),
		Secondary:  quotaWindowFromCodex(parsed.Secondary),
	}
}

func quotaWindowFromCodex(parsed *sessionlog.CodexQuotaWindow) *QuotaWindow {
	if parsed == nil {
		return nil
	}
	clone := &QuotaWindow{
		UsedPercent:   cloneFloat64Ptr(parsed.UsedPercent),
		WindowMinutes: cloneIntPtr(parsed.WindowMinutes),
		ResetAtUnix:   cloneInt64Ptr(parsed.ResetAtUnix),
		Reached:       cloneBoolPtr(parsed.Reached),
	}
	return clone
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func marshalQuotaObservation(observation *QuotaObservation) (string, error) {
	if observation == nil {
		return "", nil
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return "", fmt.Errorf("encode quota observation: %w", err)
	}
	return string(encoded), nil
}

func decodeCurrentQuotaObservation(raw string, info session.Info, now time.Time) *QuotaObservation {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var observation QuotaObservation
	if json.Unmarshal([]byte(raw), &observation) != nil || !observation.IsFresh(now) || !observation.IsHardExhausted() {
		return nil
	}
	if strings.TrimSpace(observation.SessionID) == "" || observation.SessionID != strings.TrimSpace(info.SessionKey) {
		return nil
	}
	if strings.TrimSpace(observation.Incarnation) == "" || observation.Incarnation != strings.TrimSpace(info.InstanceToken) {
		return nil
	}
	return &observation
}
