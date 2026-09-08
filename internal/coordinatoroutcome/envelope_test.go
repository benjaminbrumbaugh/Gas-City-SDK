package coordinatoroutcome

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func TestParseForCarriesBoundedCandidateAndRemediationIdentity(t *testing.T) {
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bead := beads.Bead{ID: "gc-review", Metadata: map[string]string{
		beadmeta.CoordinatorOutcomeProducerDispositionMetadataKey: fmt.Sprintf(`{"contract_version":1,"disposition":"deliverable","work_id":"gc-review","recorded_by":"configured-gate","reason":"finding","producer":"configured-gate","passing_verdict":"review_verdict","candidate_work_id":"gc-candidate","candidate_commit":%q,"remediation_ids":["gc-fix"]}`, commit),
		beadmeta.ReviewGateMetadataKey:                            "consumed",
		beadmeta.CoordinatorPassingVerdictReview:                  "block",
	}}
	envelope, err := ParseFor(bead)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.CandidateWorkID != "gc-candidate" || envelope.CandidateCommit != commit || len(envelope.RemediationIDs) != 1 || envelope.RemediationIDs[0] != "gc-fix" {
		t.Fatalf("envelope = %+v", envelope)
	}
	verdict, err := PublishedVerdict(bead, envelope)
	if err != nil || verdict != "block" {
		t.Fatalf("PublishedVerdict = %q, %v; want block", verdict, err)
	}
	if IsDeliverablePass(bead) {
		t.Fatal("BLOCK must not satisfy deliverable pass")
	}
}

func TestParseForRejectsUnboundedRemediationList(t *testing.T) {
	ids := make([]string, maxRemediationIDs+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("gc-fix-%d", i)
	}
	producer := "configured-gate"
	bead := beads.Bead{ID: "gc-review", Metadata: map[string]string{
		beadmeta.CoordinatorOutcomeProducerDispositionMetadataKey: mustEnvelopeJSON(t, Envelope{
			ContractVersion: beadmeta.CoordinatorOutcomeContractVersion,
			Disposition:     beadmeta.CoordinatorDispositionDeliverable,
			WorkID:          "gc-review",
			RecordedBy:      "configured-gate",
			Reason:          "finding",
			Producer:        &producer,
			RemediationIDs:  ids,
		}),
	}}
	if _, err := ParseFor(bead); err == nil {
		t.Fatal("ParseFor accepted unbounded remediation list")
	}
}

func TestIsDeliverablePassDoesNotNormalizeVerdict(t *testing.T) {
	producer := "configured-gate"
	bead := beads.Bead{ID: "gc-review", Metadata: map[string]string{
		beadmeta.CoordinatorOutcomeProducerDispositionMetadataKey: mustEnvelopeJSON(t, Envelope{
			ContractVersion: beadmeta.CoordinatorOutcomeContractVersion,
			Disposition:     beadmeta.CoordinatorDispositionDeliverable,
			WorkID:          "gc-review",
			RecordedBy:      producer,
			Reason:          "reviewed",
			Producer:        &producer,
			PassingVerdict:  beadmeta.CoordinatorPassingVerdictReview,
		}),
		beadmeta.ReviewGateMetadataKey:           "consumed",
		beadmeta.CoordinatorPassingVerdictReview: " pass ",
	}}
	if IsDeliverablePass(bead) {
		t.Fatal("IsDeliverablePass accepted a normalized verdict that the existing typed-close contract rejects")
	}
}

func TestParseForRejectsAmbiguousOrUnboundedJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "duplicate key", raw: `{"contract_version":1,"contract_version":1}`, want: "duplicate JSON key"},
		{name: "oversized envelope", raw: `{"reason":"` + strings.Repeat("x", maxEnvelopeBytes) + `"}`, want: "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bead := beads.Bead{ID: "gc-review", Metadata: map[string]string{
				beadmeta.CoordinatorOutcomeProducerDispositionMetadataKey: tc.raw,
			}}
			_, err := ParseFor(bead)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseFor error = %v, want %q", err, tc.want)
			}
		})
	}
}

func mustEnvelopeJSON(t *testing.T, envelope Envelope) string {
	t.Helper()
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
