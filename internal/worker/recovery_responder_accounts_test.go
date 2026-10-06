package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWayfinderRecoveryAccountSnapshotIsRequiredAndCopied(t *testing.T) {
	template := recoveryWayfinderTemplate(t, "rig/first")
	for _, refs := range [][]string{nil, {}, {" claude-personal"}, {"claude-personal", "../forged"}} {
		if _, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", template, refs); err == nil {
			t.Fatalf("invalid account snapshot accepted: %v", refs)
		}
	}
	refs := []string{"claude-personal"}
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", template, refs)
	if err != nil {
		t.Fatal(err)
	}
	refs[0] = "forged-account"
	if _, ok := advisor.allowedAccountRefs["claude-personal"]; !ok {
		t.Fatal("caller mutation changed the admitted account snapshot")
	}
}

func TestWayfinderRecoveryRejectsUnknownEntitlementBeforeHTTP(t *testing.T) {
	packet := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	entitlement := packet.Entitlements[0]
	entitlement.RecordID = "unreferenced-entitlement"
	entitlement.AccountRef = "forged-account"
	packet.Entitlements = append(packet.Entitlements, entitlement)
	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", encoded, testWayfinderAccountRefs()); err == nil || !strings.Contains(err.Error(), "installed configured account") {
		t.Fatalf("constructor error = %v, want unknown entitlement rejection", err)
	}
}

func TestWayfinderRecoveryRechecksOutgoingAccountBeforeHTTP(t *testing.T) {
	advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", recoveryWayfinderTemplate(t, "rig/first"), testWayfinderAccountRefs())
	if err != nil {
		t.Fatal(err)
	}
	advisor.template.Candidates[0].ExecutionTarget.AccountRef = "forged-account"
	advisor.template.Entitlements[0].AccountRef = "forged-account"
	calls := 0
	advisor.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		t.Fatal("unknown account reached HTTP")
		return nil, nil
	})
	if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil || !strings.Contains(err.Error(), "installed configured account") {
		t.Fatalf("Recommend error = %v, want unknown account rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want zero", calls)
	}
}

func TestWayfinderRecoveryRejectsSubstitutionWithAnotherAdmittedAccount(t *testing.T) {
	request := recoveryWayfinderRequest(t, string(recoveryWayfinderTemplate(t, "rig/first")))
	result := validWayfinderResult(request, "rig/first")
	result.Candidates[0].ExecutionTarget.AccountRef = "claude-gladstone"
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	advisor := recoveryWayfinderAdvisorWithResponse(t, encoded)
	advisor.allowedAccountRefs["claude-gladstone"] = struct{}{}
	if _, err := advisor.Recommend(context.Background(), recoveryRequest()); err == nil {
		t.Fatal("another admitted account was substituted for the request-bound account")
	}
}

func TestWayfinderRecoveryAcceptsLegitimateAccountBindings(t *testing.T) {
	for _, account := range []string{"claude-personal", "claude-gladstone", "codex"} {
		t.Run(account, func(t *testing.T) {
			template := recoveryWayfinderTemplate(t, "rig/first")
			var packet wayfinderEvaluateRequest
			if err := json.Unmarshal(template, &packet); err != nil {
				t.Fatal(err)
			}
			packet.Candidates[0].ExecutionTarget.AccountRef = account
			packet.Entitlements[0].AccountRef = account
			encoded, err := json.Marshal(packet)
			if err != nil {
				t.Fatal(err)
			}
			advisor, err := NewWayfinderRecoveryAdvisor("http://127.0.0.1:1234", encoded, []string{account})
			if err != nil {
				t.Fatal(err)
			}
			advisor.now = func() time.Time { return time.Unix(151, 0) }
			advisor.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				var sent wayfinderEvaluateRequest
				if err := json.NewDecoder(req.Body).Decode(&sent); err != nil {
					t.Fatal(err)
				}
				if sent.Candidates[0].ExecutionTarget.AccountRef != account || sent.Entitlements[0].AccountRef != account {
					t.Fatal("outgoing account binding changed")
				}
				body, err := json.Marshal(validWayfinderResult(sent, "rig/first"))
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
			})
			if got, err := advisor.Recommend(context.Background(), recoveryRequest()); err != nil || got.Outcome != RecoveryAdviceSelected || got.Target != "rig/first" {
				t.Fatalf("Recommend = %+v, %v", got, err)
			}
		})
	}
}
