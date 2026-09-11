package session

import (
	"context"
	"encoding/hex"
	"errors"
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

// LegacyGlobalProviderFenceIdentity is the fail-closed identity used by
// pre-account-attribution provider fences. It blocks every identity-aware
// provider start until the legacy fence expires.
const LegacyGlobalProviderFenceIdentity = "legacy:any-provider-account"

const providerFenceBeadKind = "provider_usage_fence"

const (
	providerFenceKeyContinuityBeadLabel = "gc:provider-fence-key-continuity"
	providerFenceKeyContinuityBeadKind  = "provider_fence_key_continuity"
	providerFenceKeyDigestMetadataKey   = "provider_fence_key_sha256"
)

const maxExpiredProviderFencesClosedPerRead = 100

// Corrupt observations fail closed for longer than any fence the controller
// can create (the modal path caps valid fences at eight days). After this
// conservative bound, the store annotates and closes them in bounded batches,
// returning an error for that final blocked read so the repair is visible in
// controller logs. Rows without a trustworthy creation time remain blocked and
// name their bead ID in the error for explicit operator repair.
const (
	maxCorruptProviderFenceFailClosedAge    = 9 * 24 * time.Hour
	maxCorruptProviderFencesClosedPerRead   = 100
	providerFenceRemediationMetadataKey     = "provider_fence_remediation"
	providerFenceRemediationAtMetadataKey   = "provider_fence_remediated_at"
	providerFenceAutoClosedCorruptionReason = "auto_closed_corrupt_after_safety_bound"
)

// ProviderFence is the typed durable record used by the reconciler to block
// starts for a provider account until its observed reset deadline.
type ProviderFence struct {
	Identity   string
	Until      time.Time
	ObservedAt time.Time
	Reason     string
}

// ProviderFenceIdentityMatches centralizes the compatibility rule shared by
// direct starts and reconciler starts: an exact account fence is isolated to
// that account, while a legacy global fence applies to every provider account.
func ProviderFenceIdentityMatches(recordedIdentity, currentIdentity string) bool {
	recordedIdentity = strings.TrimSpace(recordedIdentity)
	if recordedIdentity == LegacyGlobalProviderFenceIdentity {
		return true
	}
	currentIdentity = strings.TrimSpace(currentIdentity)
	return currentIdentity != "" && recordedIdentity == currentIdentity
}

// ActiveProviderFences reads the durable provider-fence records. The returned
// slice is folded by identity using the latest deadline; expired records are
// ignored and a bounded batch is closed while remaining in history. A read error
// is returned to callers so lifecycle code can fail closed instead of treating
// an unavailable or corrupt fence store as an empty store.
func (s *Store) ActiveProviderFences(now time.Time) ([]ProviderFence, error) {
	if s == nil || s.store.Store == nil {
		return nil, fmt.Errorf("provider fence store is unavailable")
	}
	// The label is authoritative. Filtering by type would make a labeled row
	// whose type was damaged disappear and silently fail open.
	rows, err := s.store.List(beads.ListQuery{
		Label:         ProviderFenceBeadLabel,
		IncludeClosed: true,
		Sort:          beads.SortCreatedDesc,
		Live:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("listing provider fences: %w", err)
	}
	byIdentity := make(map[string]ProviderFence)
	expiredClosed := 0
	corruptClosed := 0
	var corruptErrs []error
	for _, row := range rows {
		// A prior bounded remediation deliberately leaves the malformed payload
		// in immutable history with an audited closed marker. Do not repeatedly
		// remediate that already-contained row on every read.
		if strings.TrimSpace(row.Status) == "closed" &&
			strings.TrimSpace(row.Metadata[providerFenceRemediationMetadataKey]) == providerFenceAutoClosedCorruptionReason {
			continue
		}
		identity, until, observedAt, validationErr := decodeProviderFence(row)
		if validationErr != nil {
			oldEnough := !row.CreatedAt.IsZero() && !row.CreatedAt.After(now) && now.Sub(row.CreatedAt) >= maxCorruptProviderFenceFailClosedAge
			if oldEnough && corruptClosed < maxCorruptProviderFencesClosedPerRead {
				writer, ok := beads.ConditionalWriterFor(s.store.Store)
				if !ok {
					corruptErrs = append(corruptErrs, fmt.Errorf("provider fence %q is corrupt and past the %s safety bound, but the store lacks atomic revision-fenced remediation: %w; inspect and close bead %q explicitly after verifying account safety", row.ID, maxCorruptProviderFenceFailClosedAge, validationErr, row.ID))
					continue
				}
				metadata := map[string]string{
					providerFenceRemediationMetadataKey:   providerFenceAutoClosedCorruptionReason,
					providerFenceRemediationAtMetadataKey: now.UTC().Format(time.RFC3339),
				}
				closed := "closed"
				if err := writer.UpdateIfMatch(row.ID, row.Revision, beads.UpdateOpts{Status: &closed, Metadata: metadata}); err != nil {
					return nil, fmt.Errorf("atomically remediating corrupt provider fence %q after safety bound: %w", row.ID, err)
				}
				corruptClosed++
				corruptErrs = append(corruptErrs, fmt.Errorf("provider fence %q was auto-closed after the %s corruption safety bound: %w", row.ID, maxCorruptProviderFenceFailClosedAge, validationErr))
				continue
			}
			corruptErrs = append(corruptErrs, fmt.Errorf("provider fence %q is corrupt and blocks provider starts: %w; inspect and close bead %q explicitly after verifying account safety", row.ID, validationErr, row.ID))
			continue
		}
		if strings.TrimSpace(row.Status) == "closed" {
			continue
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
	if len(corruptErrs) > 0 {
		return nil, errors.Join(corruptErrs...)
	}
	result := make([]ProviderFence, 0, len(byIdentity))
	for _, fence := range byIdentity {
		result = append(result, fence)
	}
	return result, nil
}

func decodeProviderFence(row beads.Bead) (string, time.Time, time.Time, error) {
	status := strings.TrimSpace(row.Status)
	if status != "open" && status != "closed" {
		return "", time.Time{}, time.Time{}, fmt.Errorf("invalid status %q", row.Status)
	}
	if strings.TrimSpace(row.Type) != WaitBeadType {
		return "", time.Time{}, time.Time{}, fmt.Errorf("invalid type %q", row.Type)
	}
	if strings.TrimSpace(row.Metadata["kind"]) != providerFenceBeadKind {
		return "", time.Time{}, time.Time{}, fmt.Errorf("invalid kind %q", row.Metadata["kind"])
	}
	identity := strings.TrimSpace(row.Metadata["provider_fence_identity"])
	if identity == "" {
		return "", time.Time{}, time.Time{}, fmt.Errorf("empty identity")
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(row.Metadata["fenced_until"]))
	if err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("invalid deadline: %w", err)
	}
	observedAt := time.Time{}
	if raw := strings.TrimSpace(row.Metadata["observed_at"]); raw != "" {
		observedAt, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return "", time.Time{}, time.Time{}, fmt.Errorf("invalid observation time: %w", err)
		}
	}
	return identity, until, observedAt, nil
}

// HasKeyedProviderFenceIdentityHistory reports whether any durable provider
// fence or session row, open or closed, still references an identity derived by
// the city key. It is the continuity guard used before creating a replacement
// key after both key files have disappeared.
func (s *Store) HasKeyedProviderFenceIdentityHistory() (bool, error) {
	if s == nil || s.store.Store == nil {
		return false, fmt.Errorf("provider fence store is unavailable")
	}
	fences, err := s.store.List(beads.ListQuery{
		Label:         ProviderFenceBeadLabel,
		IncludeClosed: true,
		Live:          true,
	})
	if err != nil {
		return false, fmt.Errorf("listing durable provider fence history: %w", err)
	}
	for _, row := range fences {
		if isKeyedProviderFenceIdentity(row.Metadata["provider_fence_identity"]) {
			return true, nil
		}
	}
	sessions, err := ListAllSessionBeads(s.store.Store, beads.ListQuery{IncludeClosed: true, Live: true})
	if err != nil {
		return false, fmt.Errorf("listing durable session attribution history: %w", err)
	}
	for _, row := range sessions {
		for _, key := range []string{"provider_fence_identity", "started_provider_fence_identity", "launch_provider_fence_identity"} {
			if isKeyedProviderFenceIdentity(row.Metadata[key]) {
				return true, nil
			}
		}
	}
	return false, nil
}

func isKeyedProviderFenceIdentity(identity string) bool {
	return strings.HasPrefix(strings.TrimSpace(identity), "account:hmac-sha256:")
}

// AttestProviderFenceIdentityKeyDigest anchors the non-secret SHA-256 digest of
// the city identity key in durable beads state. The local key and digest files
// can otherwise be replaced coherently; the independent durable anchor makes
// that replacement detectable once keyed fence/session history exists.
func (s *Store) AttestProviderFenceIdentityKeyDigest(digest string) error {
	digest = strings.TrimSpace(digest)
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("provider fence durable key digest is invalid")
	}
	if s == nil || s.store.Store == nil {
		return fmt.Errorf("provider fence store is unavailable")
	}
	rows, err := s.store.List(beads.ListQuery{
		Label: providerFenceKeyContinuityBeadLabel, IncludeClosed: true, Live: true,
	})
	if err != nil {
		return fmt.Errorf("listing provider fence durable key digests: %w", err)
	}
	found := false
	for _, row := range rows {
		rowDigest := strings.TrimSpace(row.Metadata[providerFenceKeyDigestMetadataKey])
		rowDecoded, decodeErr := hex.DecodeString(rowDigest)
		if strings.TrimSpace(row.Type) != WaitBeadType ||
			strings.TrimSpace(row.Metadata["kind"]) != providerFenceKeyContinuityBeadKind ||
			decodeErr != nil || len(rowDecoded) != 32 {
			return fmt.Errorf("provider fence durable key digest row %q is corrupt", row.ID)
		}
		if rowDigest != digest {
			return fmt.Errorf("provider fence durable key digest row %q does not match the current key", row.ID)
		}
		found = true
	}
	if found {
		return nil
	}
	hasHistory, err := s.HasKeyedProviderFenceIdentityHistory()
	if err != nil {
		return err
	}
	if hasHistory {
		return fmt.Errorf("provider fence durable key digest is missing while keyed fence or session attribution history exists")
	}
	_, err = s.store.Create(beads.Bead{
		Title:  "provider fence key continuity",
		Status: "open",
		Type:   WaitBeadType,
		Labels: []string{providerFenceKeyContinuityBeadLabel},
		Metadata: map[string]string{
			"kind":                            providerFenceKeyContinuityBeadKind,
			providerFenceKeyDigestMetadataKey: digest,
		},
	})
	if err != nil {
		return fmt.Errorf("recording provider fence durable key digest: %w", err)
	}
	return nil
}

// RecordProviderFence appends a durable fence record. It is deliberately
// monotonic at read time: overlapping records may remain open until expiry, and
// ActiveProviderFences selects the maximum deadline. That avoids an update race
// where a slower observer could shorten a newer account quarantine. Fence
// records are safety-critical durable observations and have no caller context,
// so contention waits fail closed rather than dropping the quarantine.
func (s *Store) RecordProviderFence(identity string, until, observedAt time.Time, reason string) error {
	return s.recordProviderFence(context.Background(), identity, until, observedAt, reason)
}

func (s *Store) recordProviderFence(ctx context.Context, identity string, until, observedAt time.Time, reason string) error {
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
	err := s.withProviderFenceRecordContext(ctx, identity, func() error {
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
		return err
	})
	if err != nil {
		return fmt.Errorf("recording provider fence %q: %w", identity, err)
	}
	return nil
}
