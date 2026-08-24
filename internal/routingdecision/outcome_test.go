package routingdecision

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

func TestDecisionRecommendationIDIsOptionalAndSignedWhenPresent(t *testing.T) {
	legacy := testDecisionPayload(t)
	legacy.RecommendationID = ""
	legacy.BindingID = BindingID(legacy)
	legacyBytes, err := CanonicalDecisionBytes(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyBytes), "recommendation_id") {
		t.Fatalf("legacy canonical bytes changed: %s", legacyBytes)
	}

	linked := legacy
	linked.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
	linked.BindingID = BindingID(linked)
	linkedBytes, err := CanonicalDecisionBytes(linked)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(linkedBytes), `"recommendation_id":"routing/v2:`) {
		t.Fatalf("recommendation id is not signed: %s", linkedBytes)
	}
	if string(legacyBytes) == string(linkedBytes) {
		t.Fatal("recommendation id did not change signed decision bytes")
	}

	invalid := linked
	invalid.RecommendationID = "recommendation-safe-but-not-v2"
	invalid.BindingID = BindingID(invalid)
	if err := invalid.Validate(); err == nil {
		t.Fatal("non-v2 recommendation id accepted")
	}
	invalid.RecommendationID = " secret\n"
	invalid.BindingID = BindingID(invalid)
	if err := invalid.Validate(); err == nil {
		t.Fatal("non-canonical recommendation id accepted")
	}
}

func TestProjectOutcomeAcceptsPortableOpaqueIDsFromSignedDecision(t *testing.T) {
	payload := testDecisionPayload(t)
	payload.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
	payload.WorkBeadID = "tokenizer-task"
	payload.BindingID = BindingID(payload)
	if err := payload.Validate(); err != nil {
		t.Fatalf("signed decision precondition is invalid: %v", err)
	}

	record := ProjectOutcome(DecisionWithAudits{
		Record: Record{Payload: payload, State: StateRefusedAfterRace},
	}, OutcomeWorkSnapshot{}, time.Unix(1, 0).UTC())
	if err := record.Validate(); err != nil {
		t.Fatalf("accepted signed decision became unprojectable: %v", err)
	}
}

func TestProjectOutcomeUsesOnlyExactCarrierMetadataAndRedactsRawFields(t *testing.T) {
	observed := time.Date(2026, 8, 22, 12, 34, 56, 0, time.UTC)
	payload := testDecisionPayload(t)
	payload.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
	payload.BindingID = BindingID(payload)
	item := DecisionWithAudits{
		Record:             Record{Payload: payload, State: StateOutcomeRecorded},
		AdmissionReceiptID: "controller-admit:decision-1:2",
		Audits:             []TransitionAudit{{DecisionID: payload.DecisionID, To: StateOutcomeRecorded, At: observed.Add(-time.Minute)}},
	}
	work := OutcomeWorkSnapshot{
		Found: true, WorkID: payload.WorkBeadID, Status: "closed", ClaimFence: payload.ClaimFence + 1,
		Metadata: map[string]string{
			"gc.routing_decision_id":          payload.DecisionID,
			"gc.routing_decision_claim_fence": "7",
			"gc.run_target":                   payload.Target,
			"gc.work_outcome":                 "shipped",
			"gc.failure_class":                "hard",
			"gc.session_id":                   "session-1",
			"gc.current_run_id":               "execution-1",
			"gc.failure_reason":               "credential=secret",
		},
	}
	work.Metadata["gc.routing_decision_claim_fence"] = "3"
	payload.ClaimFence = 3
	item.Record.Payload = payload

	got := ProjectOutcome(item, work, observed)
	if err := got.Validate(); err != nil {
		t.Fatalf("projected outcome is schema-invalid: %v: %+v", err, got)
	}
	if !strings.HasPrefix(got.OutcomeID, "outcome_") {
		t.Fatalf("outcome id = %q", got.OutcomeID)
	}
	same := ProjectOutcome(item, work, observed.Add(time.Hour))
	if same.OutcomeID != got.OutcomeID {
		t.Fatalf("same immutable input changed outcome id: %q != %q", same.OutcomeID, got.OutcomeID)
	}
	changedWork := work
	changedWork.Metadata = make(map[string]string, len(work.Metadata))
	for key, value := range work.Metadata {
		changedWork.Metadata[key] = value
	}
	changedWork.Metadata["gc.work_outcome"] = "no-op"
	changed := ProjectOutcome(item, changedWork, observed)
	if changed.OutcomeID == got.OutcomeID {
		t.Fatal("changed canonical outcome content retained outcome id")
	}
	if got.SchemaVersion != OutcomeSchemaVersion || got.CorrelationID != payload.WorkBeadID || got.RecommendationID != payload.RecommendationID {
		t.Fatalf("identity = %+v", got)
	}
	if got.RoutingDecisionID == nil || *got.RoutingDecisionID != payload.DecisionID || got.WorkID != payload.WorkBeadID {
		t.Fatalf("join ids = %+v", got)
	}
	if got.Disposition != OutcomeDispositionShipped || got.FailureClass != OutcomeFailureNone || got.Coverage != OutcomeCoverageAvailable {
		t.Fatalf("classification = %+v", got)
	}
	if got.Provenance != "authoritative_routing_decision_exact_work" {
		t.Fatalf("provenance = %q, want canonical exact-work enum", got.Provenance)
	}
	if got.Status != OutcomeStatusSucceeded || got.RequestedTargetID != payload.Target || got.ActualTargetID == nil || *got.ActualTargetID != payload.Target || got.RequestedConfigDigest != "sha256:"+payload.TargetConfigDigest || got.ActualConfigDigest == nil || *got.ActualConfigDigest != "sha256:"+payload.TargetConfigDigest {
		t.Fatalf("targets = %+v", got)
	}
	if got.AdmissionReceiptID == nil || *got.AdmissionReceiptID != item.AdmissionReceiptID || got.SessionID == nil || *got.SessionID != "session-1" || got.ExecutionID == nil || *got.ExecutionID != "execution-1" || got.ObservedAtUnix != observed.Add(-time.Minute).Unix() {
		t.Fatalf("runtime ids/time = %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"credential=secret", "failure_reason", "title", "description", "comments", "reason"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("redacted outcome leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProjectOutcomeDoesNotClaimSuccessWithoutCompleteCausalExecutionIDs(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 34, 56, 0, time.UTC)
	payload := testDecisionPayload(t)
	payload.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
	payload.BindingID = BindingID(payload)
	baseItem := DecisionWithAudits{
		Record:             Record{Payload: payload, State: StateOutcomeRecorded},
		AdmissionReceiptID: "controller-admit:decision-1:2",
	}
	baseWork := OutcomeWorkSnapshot{
		Found: true, WorkID: payload.WorkBeadID,
		Metadata: map[string]string{
			beadmeta.RoutingDecisionIDMetadataKey:         payload.DecisionID,
			beadmeta.RoutingDecisionClaimFenceMetadataKey: strconv.FormatInt(payload.ClaimFence, 10),
			beadmeta.RunTargetMetadataKey:                 payload.Target,
			beadmeta.WorkOutcomeMetadataKey:               "shipped",
			beadmeta.SessionIDMetadataKey:                 "session-1",
			beadmeta.CurrentRunIDMetadataKey:              "execution-1",
		},
	}

	for _, missing := range []string{"admission_receipt_id", "session_id", "execution_id"} {
		t.Run(missing, func(t *testing.T) {
			item := baseItem
			work := baseWork
			work.Metadata = make(map[string]string, len(baseWork.Metadata))
			for key, value := range baseWork.Metadata {
				work.Metadata[key] = value
			}
			switch missing {
			case "admission_receipt_id":
				item.AdmissionReceiptID = ""
			case "session_id":
				delete(work.Metadata, beadmeta.SessionIDMetadataKey)
			case "execution_id":
				delete(work.Metadata, beadmeta.CurrentRunIDMetadataKey)
			}

			got := ProjectOutcome(item, work, now)
			if got.Status == OutcomeStatusSucceeded || got.Disposition != OutcomeDispositionUnknown || got.FailureClass != OutcomeFailureUnknown {
				t.Fatalf("incomplete causal evidence invented success: %+v", got)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("incomplete causal projection is invalid: %v: %+v", err, got)
			}
		})
	}
}

func TestProjectOutcomeClassifiesClaimedNotAdmittedAndUnknownCoverage(t *testing.T) {
	now := time.Date(2026, 8, 22, 13, 0, 0, 0, time.UTC)
	payload := testDecisionPayload(t)
	payload.RecommendationID = "routing/v2:" + strings.Repeat("d", 64)
	payload.BindingID = BindingID(payload)

	claimed := ProjectOutcome(DecisionWithAudits{Record: Record{Payload: payload, State: StateClaimed}}, OutcomeWorkSnapshot{}, now)
	if claimed.Status != OutcomeStatusClaimed || claimed.Disposition != OutcomeDispositionUnknown || claimed.Coverage != OutcomeCoverageUnknown || claimed.FailureClass != OutcomeFailureUnknown || claimed.ActualTargetID == nil || claimed.ActualConfigDigest == nil {
		t.Fatalf("claimed = %+v", claimed)
	}
	exactClaimed := ProjectOutcome(DecisionWithAudits{Record: Record{Payload: payload, State: StateClaimed}}, OutcomeWorkSnapshot{
		Found: true, WorkID: payload.WorkBeadID, Metadata: map[string]string{
			beadmeta.RoutingDecisionIDMetadataKey:         payload.DecisionID,
			beadmeta.RoutingDecisionClaimFenceMetadataKey: strconv.FormatInt(payload.ClaimFence, 10),
			beadmeta.RunTargetMetadataKey:                 payload.Target,
			beadmeta.SessionIDMetadataKey:                 "session-enriched",
		},
	}, now)
	if exactClaimed.OutcomeID == claimed.OutcomeID {
		t.Fatal("outcome evidence enrichment retained the same immutable outcome_id")
	}

	refused := ProjectOutcome(DecisionWithAudits{Record: Record{Payload: payload, State: StateRefusedAfterRace}}, OutcomeWorkSnapshot{}, now)
	if refused.Status != OutcomeStatusFailed || refused.Disposition != OutcomeDispositionNotAdmitted || refused.FailureClass != OutcomeFailureUnknown || refused.ActualTargetID != nil || refused.ActualConfigDigest != nil {
		t.Fatalf("refused = %+v", refused)
	}

	mismatched := OutcomeWorkSnapshot{Found: true, WorkID: payload.WorkBeadID, Metadata: map[string]string{"gc.routing_decision_id": "another-decision"}}
	partial := ProjectOutcome(DecisionWithAudits{Record: Record{Payload: payload, State: StateOutcomeRecorded}}, mismatched, now)
	if partial.Coverage != OutcomeCoveragePartial || partial.Disposition != OutcomeDispositionUnknown {
		t.Fatalf("mismatched = %+v", partial)
	}
}

func TestOutcomeRecordValidationEnforcesPortableConsistency(t *testing.T) {
	valid := OutcomeRecord{
		SchemaVersion: OutcomeSchemaVersion, OutcomeID: "outcome_" + strings.Repeat("a", 64),
		CorrelationID: "corr", RecommendationID: "routing/v2:" + strings.Repeat("c", 64), RoutingDecisionID: stringPointer("decision"), WorkID: "work",
		RequestedTargetID: "worker", RequestedConfigDigest: "sha256:" + strings.Repeat("b", 64),
		Status: OutcomeStatusFailed, Disposition: OutcomeDispositionNotAdmitted,
		FailureClass: OutcomeFailureUnknown, Coverage: OutcomeCoverageUnknown,
		Provenance: OutcomeProvenanceDecision, ObservedAtUnix: 1,
	}
	valid.OutcomeID = outcomeID(valid)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid not-admitted outcome: %v", err)
	}
	for name, mutate := range map[string]func(*OutcomeRecord){
		"zero observed time":       func(r *OutcomeRecord) { r.ObservedAtUnix = 0 },
		"missing requested target": func(r *OutcomeRecord) { r.RequestedTargetID = "" },
		"not admitted actual pair": func(r *OutcomeRecord) { value := "worker"; r.ActualTargetID = &value },
		"not admitted none failure": func(r *OutcomeRecord) {
			r.FailureClass = OutcomeFailureNone
		},
		"not admitted execution id": func(r *OutcomeRecord) {
			r.ExecutionID = stringPointer("execution-a")
		},
		"failed shipped": func(r *OutcomeRecord) {
			target, digest := "worker", "sha256:"+strings.Repeat("b", 64)
			r.Disposition, r.FailureClass = OutcomeDispositionShipped, OutcomeFailureUnknown
			r.ActualTargetID, r.ActualConfigDigest = &target, &digest
		},
		"succeeded blocked": func(r *OutcomeRecord) {
			target, digest := "worker", "sha256:"+strings.Repeat("b", 64)
			r.Status, r.Disposition, r.FailureClass = OutcomeStatusSucceeded, OutcomeDispositionBlocked, OutcomeFailureNone
			r.ActualTargetID, r.ActualConfigDigest = &target, &digest
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			candidate.OutcomeID = outcomeID(candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("invalid outcome accepted: %+v", candidate)
			}
		})
	}
}

func TestOutcomeRecordValidationRequiresCompleteCausalIDsForSuccess(t *testing.T) {
	payload := testDecisionPayload(t)
	payload.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
	payload.BindingID = BindingID(payload)
	record := ProjectOutcome(DecisionWithAudits{
		Record:             Record{Payload: payload, State: StateOutcomeRecorded},
		AdmissionReceiptID: "controller-admit:decision-1:2",
	}, OutcomeWorkSnapshot{
		Found: true, WorkID: payload.WorkBeadID,
		Metadata: map[string]string{
			beadmeta.RoutingDecisionIDMetadataKey:         payload.DecisionID,
			beadmeta.RoutingDecisionClaimFenceMetadataKey: strconv.FormatInt(payload.ClaimFence, 10),
			beadmeta.RunTargetMetadataKey:                 payload.Target,
			beadmeta.WorkOutcomeMetadataKey:               "no_op",
			beadmeta.SessionIDMetadataKey:                 "session-1",
			beadmeta.CurrentRunIDMetadataKey:              "execution-1",
		},
	}, time.Unix(1, 0).UTC())
	if record.Status != OutcomeStatusSucceeded {
		t.Fatalf("complete causal record did not succeed: %+v", record)
	}
	for name, clear := range map[string]func(*OutcomeRecord){
		"admission receipt": func(record *OutcomeRecord) { record.AdmissionReceiptID = nil },
		"session":           func(record *OutcomeRecord) { record.SessionID = nil },
		"execution":         func(record *OutcomeRecord) { record.ExecutionID = nil },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := record
			clear(&candidate)
			candidate.OutcomeID = outcomeID(candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("successful outcome accepted without %s id", name)
			}
		})
	}
}

func TestValidateOutcomeOpaqueAllowsOrdinarySecurityWordIdentifiers(t *testing.T) {
	for _, value := range []string{
		"tokenizer-task",
		"credential-check",
		"password-reset",
		"private_key_rotation",
		"prompt-run",
		"secretariat-task",
		"rk-job",
		"bearer-worker",
		"basic-session",
		"github_pat_rotation",
		"xoxb-worker",
	} {
		t.Run(value, func(t *testing.T) {
			if err := validateOutcomeOpaque("work_id", value, true); err != nil {
				t.Fatalf("ordinary identifier rejected: %v", err)
			}
		})
	}
}

func TestValidateOutcomeOpaqueRejectsCredentialShapesWithoutEcho(t *testing.T) {
	for name, value := range map[string]string{
		"OpenAI key":            "sk-" + strings.Repeat("a", 48),
		"provider key":          "rk-" + strings.Repeat("a", 48),
		"GitHub token":          "ghp_" + strings.Repeat("a", 36),
		"GitHub OAuth token":    "gho_" + strings.Repeat("a", 36),
		"GitHub fine-grained":   "github_pat_" + strings.Repeat("A", 82),
		"Slack bot token":       "xoxb-" + strings.Repeat("1", 40),
		"Slack user token":      "xoxp-" + strings.Repeat("1", 40),
		"AWS access key":        "AKIA" + strings.Repeat("A", 16),
		"AWS temporary key":     "ASIA" + strings.Repeat("A", 16),
		"Google API key":        "AIza" + strings.Repeat("A", 35),
		"JWT":                   "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature",
		"bearer credential":     "bearer-" + strings.Repeat("a", 32),
		"basic auth credential": "basic-" + strings.Repeat("a", 32),
	} {
		t.Run(name, func(t *testing.T) {
			err := validateOutcomeOpaque("session_id", value, true)
			if err == nil {
				t.Fatal("credential-shaped identifier accepted")
			}
			if strings.Contains(err.Error(), value) {
				t.Fatal("credential value echoed in validation error")
			}
		})
	}
}
