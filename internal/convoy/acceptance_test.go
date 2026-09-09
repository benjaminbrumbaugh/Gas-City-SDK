package convoy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func TestConvoyProgressAcceptanceGate(t *testing.T) {
	tests := []struct {
		name                  string
		verdict               string
		malformedClose        bool
		omitClose             bool
		reviewedCommit        string
		storedCandidateCommit string
		remediationOpen       bool
		includeRemediation    bool
		omitRemediationLink   bool
		wantAdminComplete     bool
		wantAccepted          bool
		wantState             string
		wantIssue             string
	}{
		{name: "missing typed close fails closed", omitClose: true, wantAdminComplete: true, wantState: AcceptanceStateStranded, wantIssue: "missing typed close"},
		{name: "missing published verdict fails closed", wantAdminComplete: true, wantState: AcceptanceStateStranded, wantIssue: "missing published verdict"},
		{name: "malformed typed close fails closed", malformedClose: true, wantAdminComplete: true, wantState: AcceptanceStateStranded, wantIssue: "malformed typed close"},
		{name: "BLOCK verdict fails closed", verdict: "block", wantAdminComplete: true, wantState: AcceptanceStateStranded, wantIssue: "block"},
		{name: "different candidate bytes fail closed", verdict: beadmeta.OutcomePass, reviewedCommit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", wantAdminComplete: true, wantState: AcceptanceStateStranded, wantIssue: "candidate"},
		{name: "candidate bytes are not whitespace normalized", verdict: beadmeta.OutcomePass, storedCandidateCommit: " aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ", wantAdminComplete: true, wantState: AcceptanceStateStranded, wantIssue: "current bytes"},
		{name: "PASS verdict accepts terminal convoy", verdict: beadmeta.OutcomePass, wantAdminComplete: true, wantAccepted: true, wantState: AcceptanceStateComplete},
		{name: "open linked remediation stays visible", verdict: "block", includeRemediation: true, remediationOpen: true, wantState: AcceptanceStateBlockedRemediation, wantIssue: "block"},
		{name: "partially linked remediation fails closed", verdict: "block", includeRemediation: true, omitRemediationLink: true, wantAdminComplete: true, wantState: AcceptanceStateStranded, wantIssue: "resolved tracked member"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := beads.NewMemStore()
			storedCandidateCommit := tc.storedCandidateCommit
			if storedCandidateCommit == "" {
				storedCandidateCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			candidate := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "candidate", Metadata: map[string]string{
				beadmeta.WorkCommitMetadataKey: storedCandidateCommit,
			}})
			review := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "independent gate"})

			var remediation beads.Bead
			if tc.includeRemediation {
				remediation = mustCreateAcceptanceBead(t, store, beads.Bead{Title: "fix review finding"})
			}
			contract := fmt.Sprintf(`{"contract_version":1,"candidate_work_id":%q,"candidate_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","review_gate_ids":[%q]}`, candidate.ID, review.ID)
			convoy := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "release", Type: "convoy", Metadata: map[string]string{
				beadmeta.ConvoyAcceptanceMetadataKey: contract,
			}})
			for _, member := range []beads.Bead{candidate, review} {
				if err := TrackItem(store, convoy.ID, member.ID); err != nil {
					t.Fatalf("TrackItem(%s): %v", member.ID, err)
				}
			}
			if tc.includeRemediation && !tc.omitRemediationLink {
				if err := TrackItem(store, convoy.ID, remediation.ID); err != nil {
					t.Fatalf("TrackItem(remediation): %v", err)
				}
			}

			if !tc.omitClose {
				reviewedCommit := tc.reviewedCommit
				if reviewedCommit == "" {
					reviewedCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				}
				remediationJSON := "[]"
				if tc.includeRemediation {
					remediationJSON = fmt.Sprintf("[%q]", remediation.ID)
				}
				closeEnvelope := fmt.Sprintf(`{"contract_version":1,"disposition":"deliverable","work_id":%q,"recorded_by":"gate-agent","reason":"review complete","producer":"gate-agent","passing_verdict":"review_verdict","candidate_work_id":%q,"candidate_commit":%q,"remediation_ids":%s}`, review.ID, candidate.ID, reviewedCommit, remediationJSON)
				if tc.malformedClose {
					closeEnvelope = "{not-json"
				}
				if err := store.SetMetadata(review.ID, beadmeta.CoordinatorOutcomeProducerDispositionMetadataKey, closeEnvelope); err != nil {
					t.Fatal(err)
				}
				if err := store.SetMetadata(review.ID, beadmeta.ReviewGateMetadataKey, "consumed"); err != nil {
					t.Fatal(err)
				}
				if err := store.SetMetadata(review.ID, beadmeta.CoordinatorPassingVerdictReview, tc.verdict); err != nil {
					t.Fatal(err)
				}
			}

			if err := store.Close(candidate.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(review.ID); err != nil {
				t.Fatal(err)
			}
			if tc.includeRemediation && !tc.remediationOpen {
				if err := store.Close(remediation.ID); err != nil {
					t.Fatal(err)
				}
			}

			got, err := ConvoyProgress(ConvoyDeps{}, MemberClasses{Convoy: store}, convoy.ID)
			if err != nil {
				t.Fatalf("ConvoyProgress: %v", err)
			}
			if got.Complete != tc.wantAdminComplete {
				t.Errorf("Complete = %v, want administrative Complete=%v", got.Complete, tc.wantAdminComplete)
			}
			if got.AcceptedComplete != tc.wantAccepted {
				t.Errorf("AcceptedComplete = %v, want %v", got.AcceptedComplete, tc.wantAccepted)
			}
			if got.AcceptanceState != tc.wantState {
				t.Errorf("AcceptanceState = %q, want %q (issues=%v)", got.AcceptanceState, tc.wantState, got.AcceptanceIssues)
			}
			if tc.wantIssue != "" && !containsAcceptanceIssue(got.AcceptanceIssues, tc.wantIssue) {
				t.Errorf("AcceptanceIssues = %v, want issue containing %q", got.AcceptanceIssues, tc.wantIssue)
			}
			if tc.includeRemediation && (len(got.RemediationIDs) != 1 || got.RemediationIDs[0] != remediation.ID) {
				t.Errorf("RemediationIDs = %v, want [%s]", got.RemediationIDs, remediation.ID)
			}
		})
	}
}

func TestConvoyProgressLegacyCompletionUnchanged(t *testing.T) {
	store := beads.NewMemStore()
	convoy := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "legacy", Type: "convoy"})
	child := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "done", ParentID: convoy.ID})
	if err := store.Close(child.ID); err != nil {
		t.Fatal(err)
	}

	got, err := ConvoyProgress(ConvoyDeps{}, MemberClasses{Convoy: store}, convoy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || !got.AcceptedComplete || got.AcceptanceGated || got.AcceptanceState != AcceptanceStateComplete {
		t.Fatalf("legacy progress = %+v, want administrative and accepted completion", got)
	}
}

func TestMarshalAcceptanceContractRequiresFullImmutableGitOID(t *testing.T) {
	_, err := MarshalAcceptanceContract(AcceptanceContract{
		ContractVersion: AcceptanceContractVersion,
		CandidateWorkID: "gc-candidate",
		CandidateCommit: "abc123",
		ReviewGateIDs:   []string{"gc-review"},
	})
	if err == nil || !strings.Contains(err.Error(), "full lowercase") {
		t.Fatalf("MarshalAcceptanceContract error = %v, want full Git OID refusal", err)
	}
}

func TestParseAcceptanceContractRejectsAmbiguousOrUnboundedJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "duplicate key", raw: `{"contract_version":1,"contract_version":1}`, want: "duplicate JSON key"},
		{name: "oversized contract", raw: `{"candidate_work_id":"` + strings.Repeat("x", maxAcceptanceContractBytes) + `"}`, want: "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAcceptanceContract(tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseAcceptanceContract error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBlockedReviewKeepsProgramOpenAndRemediationDiscoverable(t *testing.T) {
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	store := beads.NewMemStore()
	implementation := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "implementation", Metadata: map[string]string{
		beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeShipped,
		beadmeta.WorkCommitMetadataKey:  commit,
	}})
	review := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "required gate"})
	install := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "install withheld"})
	remediation := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "repair blocking finding"})
	contract, err := MarshalAcceptanceContract(AcceptanceContract{
		ContractVersion: AcceptanceContractVersion,
		CandidateWorkID: implementation.ID,
		CandidateCommit: commit,
		ReviewGateIDs:   []string{review.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	program := mustCreateAcceptanceBead(t, store, beads.Bead{Title: "program", Type: "convoy", Metadata: map[string]string{
		beadmeta.ConvoyAcceptanceMetadataKey: contract,
	}})
	for _, id := range []string{implementation.ID, review.ID, install.ID, remediation.ID} {
		if err := TrackItem(store, program.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	envelope := fmt.Sprintf(`{"contract_version":1,"disposition":"deliverable","work_id":%q,"recorded_by":"configured-gate","reason":"blocking finding","producer":"configured-gate","passing_verdict":"review_verdict","candidate_work_id":%q,"candidate_commit":%q,"remediation_ids":[%q]}`, review.ID, implementation.ID, commit, remediation.ID)
	for key, value := range map[string]string{
		beadmeta.CoordinatorOutcomeProducerDispositionMetadataKey: envelope,
		beadmeta.ReviewGateMetadataKey:                            "consumed",
		beadmeta.CoordinatorPassingVerdictReview:                  "block",
	} {
		if err := store.SetMetadata(review.ID, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(implementation.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(review.ID); err != nil {
		t.Fatal(err)
	}

	progress, err := ConvoyProgress(ConvoyDeps{}, MemberClasses{Convoy: store}, program.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Complete || progress.AcceptedComplete || progress.AcceptanceState != AcceptanceStateBlockedRemediation {
		t.Fatalf("progress = %+v, want open blocked-remediation program", progress)
	}
	if len(progress.RemediationIDs) != 1 || progress.RemediationIDs[0] != remediation.ID {
		t.Fatalf("remediation ids = %v, want [%s]", progress.RemediationIDs, remediation.ID)
	}
	if err := ConvoyClose(ConvoyDeps{}, store, program.ID); err == nil {
		t.Fatal("ConvoyClose accepted BLOCKed program")
	}
	gotProgram, err := store.Get(program.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotProgram.Status != "open" {
		t.Fatalf("program status = %q, want open", gotProgram.Status)
	}
	gotInstall, err := store.Get(install.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotInstall.Status != "open" {
		t.Fatalf("install status = %q, want withheld/open", gotInstall.Status)
	}
}

func mustCreateAcceptanceBead(t *testing.T, store beads.Store, bead beads.Bead) beads.Bead {
	t.Helper()
	created, err := store.Create(bead)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func containsAcceptanceIssue(issues []string, needle string) bool {
	for _, issue := range issues {
		if len(issue) >= len(needle) {
			for i := 0; i+len(needle) <= len(issue); i++ {
				if issue[i:i+len(needle)] == needle {
					return true
				}
			}
		}
	}
	return false
}
