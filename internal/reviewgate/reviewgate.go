// Package reviewgate defines the durable review outcome used by delivery
// boundaries. A bead's terminal status is deliberately not a review verdict.
package reviewgate

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	// SchemaVersion is the only review-record schema accepted by delivery gates.
	SchemaVersion = "1"

	// Metadata keys are part of the durable bead protocol. Keep the verdict and
	// candidate separate from status so closing a review cannot mint approval.
	SchemaMetadataKey       = "gc.review.schema"
	VerdictMetadataKey      = "gc.review.verdict"
	CandidateMetadataKey    = "gc.review.candidate"
	DependenciesMetadataKey = "gc.review.dependencies"

	VerdictApprove = "APPROVE"
	VerdictBlock   = "BLOCK"
)

// Result explains why a review dependency is or is not deliverable.
type Result struct {
	Eligible bool
	Reason   string
}

// IsReviewRecord reports whether b carries a canonical record or a legacy
// verdict marker. Legacy markers are recognized only to keep old BLOCK records
// blocking; they can never create an approval without the canonical schema and
// candidate fields.
func IsReviewRecord(b beads.Bead) bool {
	if _, ok := b.Metadata[SchemaMetadataKey]; ok {
		return true
	}
	if _, ok := b.Metadata[VerdictMetadataKey]; ok {
		return true
	}
	if _, ok := b.Metadata[CandidateMetadataKey]; ok {
		return true
	}
	for _, key := range []string{"review_verdict", "verdict"} {
		value := strings.TrimSpace(b.Metadata[key])
		if value == VerdictApprove || value == VerdictBlock {
			return true
		}
	}
	return false
}

// Check returns the fail-closed delivery verdict for one dependency. Ordinary
// terminal dependencies retain their existing semantics; only an explicitly
// marked review record is subject to this gate.
func Check(review beads.Bead, candidate string) Result {
	if !beadsTerminal(review.Status) {
		return Result{Reason: "review dependency is not terminal"}
	}
	if !IsReviewRecord(review) {
		return Result{Eligible: true, Reason: "ordinary terminal dependency"}
	}

	verdict := strings.TrimSpace(review.Metadata[VerdictMetadataKey])
	if verdict == "" {
		// Legacy BLOCK is still a blocker. Legacy approval is intentionally not
		// accepted because it has no schema/candidate binding.
		for _, key := range []string{"review_verdict", "verdict"} {
			if value := strings.TrimSpace(review.Metadata[key]); value != "" {
				verdict = value
				break
			}
		}
	}
	if verdict == VerdictBlock {
		return Result{Reason: "review verdict is BLOCK"}
	}
	if verdict != VerdictApprove {
		return Result{Reason: "review verdict is missing or invalid"}
	}
	if strings.TrimSpace(review.Metadata[SchemaMetadataKey]) != SchemaVersion {
		return Result{Reason: "approval has no supported review schema"}
	}
	reviewed := review.Metadata[CandidateMetadataKey]
	if strings.TrimSpace(reviewed) == "" {
		return Result{Reason: "approval has no candidate identity"}
	}
	if strings.TrimSpace(candidate) == "" || reviewed != candidate {
		return Result{Reason: fmt.Sprintf("approval candidate %q does not match delivery candidate %q", reviewed, candidate)}
	}
	return Result{Eligible: true, Reason: "APPROVE is bound to the delivery candidate"}
}

// CheckRequired applies the review contract to a dependency explicitly named
// by a delivery gate. Unlike Check, a terminal bead without a review record is
// a failure: the caller has declared that this edge is an approval edge, not
// an ordinary work dependency.
func CheckRequired(review beads.Bead, candidate string) Result {
	if !beadsTerminal(review.Status) {
		return Result{Reason: "review dependency is not terminal"}
	}
	if !IsReviewRecord(review) {
		return Result{Reason: "required review dependency has no durable review record"}
	}
	return Check(review, candidate)
}

// DependencySet parses the comma-separated bead IDs carried by a delivery
// owner. Whitespace is syntax around IDs; candidate identities remain exact
// byte comparisons in Check.
func DependencySet(raw string) map[string]bool {
	set := make(map[string]bool)
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			set[id] = true
		}
	}
	return set
}

// beadsTerminal mirrors convoy membership's terminal vocabulary without
// importing the convoy package, keeping this protocol package dependency-free.
func beadsTerminal(status string) bool {
	return status == "closed" || status == "tombstone"
}
