package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// ProviderFenceBeadLabel identifies durable provider-account fence records.
// These records intentionally live beside session beads in the city store, but
// are independent of any one session row so closing or deleting the source
// role cannot erase an active account quarantine.
const ProviderFenceBeadLabel = "gc:provider-fence"

const providerFenceBeadKind = "provider_usage_fence"

const maxExpiredProviderFencesClosedPerRead = 100

// ProviderFence is the typed durable record used by the reconciler to block
// starts for a provider account until its observed reset deadline.
type ProviderFence struct {
	Identity   string
	Until      time.Time
	ObservedAt time.Time
	Reason     string
}

// ActiveProviderFences reads the durable provider-fence records. The returned
// slice is folded by identity using the latest deadline; expired records are
// ignored and a bounded batch is closed while remaining in history. A read error is returned to
// callers so lifecycle code can fail closed instead of treating an unavailable
// fence store as an empty store.
func (s *Store) ActiveProviderFences(now time.Time) ([]ProviderFence, error) {
	if s == nil || s.store.Store == nil {
		return nil, fmt.Errorf("provider fence store is unavailable")
	}
	rows, err := s.store.List(beads.ListQuery{
		Label:         ProviderFenceBeadLabel,
		Type:          WaitBeadType,
		Status:        "open",
		IncludeClosed: false,
		Sort:          beads.SortCreatedDesc,
		Live:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("listing provider fences: %w", err)
	}
	byIdentity := make(map[string]ProviderFence)
	expiredClosed := 0
	for _, row := range rows {
		if strings.TrimSpace(row.Metadata["kind"]) != providerFenceBeadKind {
			continue
		}
		identity := strings.TrimSpace(row.Metadata["provider_fence_identity"])
		until, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(row.Metadata["fenced_until"]))
		if identity == "" {
			return nil, fmt.Errorf("provider fence %q has empty identity", row.ID)
		}
		if parseErr != nil {
			return nil, fmt.Errorf("provider fence %q has invalid deadline: %w", row.ID, parseErr)
		}
		if !until.After(now) {
			// Fence rows are immutable append-only observations, so an expired
			// row can be closed without racing a deadline extension. Bound the
			// opportunistic work so one reconciliation read cannot turn an old
			// backlog into an unbounded write burst.
			if expiredClosed < maxExpiredProviderFencesClosedPerRead {
				if err := s.store.Close(row.ID); err != nil {
					return nil, fmt.Errorf("closing expired provider fence %q: %w", row.ID, err)
				}
				expiredClosed++
			}
			continue
		}
		observedAt := time.Time{}
		if raw := strings.TrimSpace(row.Metadata["observed_at"]); raw != "" {
			observedAt, parseErr = time.Parse(time.RFC3339, raw)
			if parseErr != nil {
				return nil, fmt.Errorf("provider fence %q has invalid observation time: %w", row.ID, parseErr)
			}
		}
		candidate := ProviderFence{
			Identity:   identity,
			Until:      until,
			ObservedAt: observedAt,
			Reason:     strings.TrimSpace(row.Metadata["reason"]),
		}
		if current, ok := byIdentity[identity]; !ok || candidate.Until.After(current.Until) {
			byIdentity[identity] = candidate
		}
	}
	result := make([]ProviderFence, 0, len(byIdentity))
	for _, fence := range byIdentity {
		result = append(result, fence)
	}
	return result, nil
}

// RecordProviderFence appends a durable fence record. It is deliberately
// monotonic at read time: overlapping records may remain open until expiry, and
// ActiveProviderFences selects the maximum deadline. That avoids an update race where a slower
// observer could shorten a newer account quarantine.
func (s *Store) RecordProviderFence(identity string, until, observedAt time.Time, reason string) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return fmt.Errorf("provider fence identity is empty")
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if until.IsZero() || !until.After(observedAt) {
		return fmt.Errorf("provider fence deadline must be after observation")
	}
	if s == nil || s.store.Store == nil {
		return fmt.Errorf("provider fence store is unavailable")
	}
	_, err := s.store.Create(beads.Bead{
		Title:  "provider usage fence",
		Status: "open",
		Type:   WaitBeadType,
		Labels: []string{ProviderFenceBeadLabel},
		Metadata: map[string]string{
			"kind":                    providerFenceBeadKind,
			"provider_fence_identity": identity,
			"fenced_until":            until.UTC().Format(time.RFC3339),
			"observed_at":             observedAt.UTC().Format(time.RFC3339),
			"reason":                  strings.TrimSpace(reason),
		},
	})
	if err != nil {
		return fmt.Errorf("recording provider fence %q: %w", identity, err)
	}
	return nil
}
