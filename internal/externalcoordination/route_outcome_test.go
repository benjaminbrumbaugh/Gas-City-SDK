package externalcoordination

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// queuedTestRecord enqueues one request and returns the durable record.
func queuedTestRecord(t *testing.T, service *Service, now time.Time) RequestRecord {
	t.Helper()
	record, err := service.Enqueue(context.Background(), testRequestInput(now))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return record
}

// claimedTestRecord enqueues and claims one request so submission-boundary
// transitions can be exercised.
func claimedTestRecord(t *testing.T, service *Service, now time.Time) RequestRecord {
	t.Helper()
	record := queuedTestRecord(t, service, now)
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return claimed
}

func TestRouteIdentityRejectsURLsCredentialsAndUnboundedData(t *testing.T) {
	oversized := make(map[string]string, maxRouteIdentityEntries+1)
	for i := 0; i <= maxRouteIdentityEntries; i++ {
		oversized[fmt.Sprintf("key%d", i)] = "value"
	}
	cases := []struct {
		name  string
		route map[string]string
	}{
		{"absolute url value", map[string]string{"thread": "https://coordinator.example/callback"}},
		{"scheme relative url value", map[string]string{"thread": "//coordinator.example/callback"}},
		{"embedded scheme value", map[string]string{"thread": "redirect=http://coordinator.example"}},
		{"authorization key", map[string]string{"authorization": "opaque"}},
		{"token key", map[string]string{"session_token": "opaque"}},
		{"api key", map[string]string{"api_key": "opaque"}},
		{"secret key", map[string]string{"client_secret": "opaque"}},
		{"bearer value", map[string]string{"thread": "Bearer abc.def.ghi"}},
		{"basic value", map[string]string{"thread": "Basic dXNlcjpwYXNz"}},
		{"control character value", map[string]string{"thread": "abc\ndef"}},
		{"uppercase key", map[string]string{"Thread": "abc"}},
		{"empty key", map[string]string{"": "abc"}},
		{"oversized key", map[string]string{strings.Repeat("k", maxRouteIdentityKeyLen+1): "abc"}},
		{"oversized value", map[string]string{"thread": strings.Repeat("v", maxRouteIdentityValueLen+1)}},
		{"too many entries", oversized},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewService(beads.NewMemStore())
			input := testRequestInput(time.Now().UTC())
			input.RouteIdentity = testCase.route
			if _, err := service.Enqueue(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Enqueue(%v) error = %v, want ErrInvalidInput", testCase.route, err)
			}
		})
	}
}

func TestRouteIdentityAcceptsOpaqueRouteDataAndCopiesIt(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	input := testRequestInput(now)
	route := map[string]string{"conversation": "abc-123", "harness.thread": "t_9"}
	input.RouteIdentity = route

	record, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatalf("enqueue with opaque route identity: %v", err)
	}
	if got := record.Request.RouteIdentity["conversation"]; got != "abc-123" {
		t.Fatalf("route identity conversation = %q, want abc-123", got)
	}

	// Copy-on-write: mutating the caller's map must not change the durable record.
	route["conversation"] = "mutated"
	if got := record.Request.RouteIdentity["conversation"]; got != "abc-123" {
		t.Fatalf("durable route identity aliased caller map: %q", got)
	}
}

func TestRouteIdentityNeverSelectsTheTarget(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	input := testRequestInput(now)
	input.RouteIdentity = map[string]string{"target_id": "other-coordinator", "adapter": "other"}

	record, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if record.Request.Target.TargetID != "coord-a" || record.Request.Target.Adapter != "hermes" {
		t.Fatalf("route identity changed the configured target: %+v", record.Request.Target)
	}
}

func TestDeliveryTransitionTableMatchesPolicy(t *testing.T) {
	legal := []struct{ from, to DeliveryState }{
		{StateQueued, StateRunning},
		{StateQueued, StateUncertain},
		{StateQueued, StateFailed},
		{StateRunning, StateSubmitted},
		{StateRunning, StateUncertain},
		{StateSubmitted, StateResponded},
		{StateSubmitted, StateOutcomeRecorded},
		{StateSubmitted, StateUncertain},
		{StateUncertain, StateReconciled},
		{StateReconciled, StateSubmitted},
		{StateReconciled, StateFailed},
		{StateReconciled, StateUncertain},
		{StateResponded, StateOutcomeRecorded},
		{StateResponded, StateFailed},
	}
	for _, transition := range legal {
		if !CanTransition(transition.from, transition.to) {
			t.Errorf("CanTransition(%q, %q) = false, want true", transition.from, transition.to)
		}
	}

	illegal := []struct{ from, to DeliveryState }{
		// Uncertainty must reconcile before anything else can happen.
		{StateUncertain, StateSubmitted},
		{StateUncertain, StateFailed},
		{StateUncertain, StateOutcomeRecorded},
		{StateUncertain, StateResponded},
		// Terminal states are terminal.
		{StateOutcomeRecorded, StateSubmitted},
		{StateOutcomeRecorded, StateFailed},
		{StateFailed, StateSubmitted},
		{StateFailed, StateOutcomeRecorded},
		// A queued request cannot skip submission.
		{StateQueued, StateOutcomeRecorded},
		{StateQueued, StateResponded},
	}
	for _, transition := range illegal {
		if CanTransition(transition.from, transition.to) {
			t.Errorf("CanTransition(%q, %q) = true, want false", transition.from, transition.to)
		}
	}

	for _, state := range []DeliveryState{StateOutcomeRecorded, StateFailed, StateExpired, StateCancelled} {
		if !IsTerminal(state) {
			t.Errorf("IsTerminal(%q) = false, want true", state)
		}
	}
	for _, state := range []DeliveryState{StateQueued, StateRunning, StateSubmitted, StateUncertain, StateReconciled, StateResponded} {
		if IsTerminal(state) {
			t.Errorf("IsTerminal(%q) = true, want false", state)
		}
	}
}

func TestCompleteRecordsSubmittedWithPersistedTimestamp(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	claimed := claimedTestRecord(t, service, now)
	submittedAt := now.Add(2 * time.Second)

	receipt := DeliveryReceipt{
		RequestID:       claimed.Request.RequestID,
		Attempt:         claimed.Attempt,
		CorrelationID:   claimed.Request.CorrelationID,
		State:           StateQueued,
		Accepted:        true,
		TargetSessionID: "session-1",
	}
	if err := service.Complete(context.Background(), claimed.ID, receipt, submittedAt); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	stored, err := service.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateSubmitted {
		t.Fatalf("state = %q, want submitted", stored.State)
	}
	if !stored.SubmittedAt().Equal(submittedAt) {
		t.Fatalf("SubmittedAt = %s, want %s", stored.SubmittedAt(), submittedAt)
	}
	if stored.Outcome() != OutcomeNone {
		t.Fatalf("outcome = %q, want none: submission is not an outcome", stored.Outcome())
	}
}

func TestUncertainSubmissionCannotRetryOrFallBackBeforeReconciliation(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	claimed := claimedTestRecord(t, service, now)
	uncertainAt := now.Add(2 * time.Second)

	if err := service.MarkUncertain(context.Background(), claimed.ID, "transport timeout after send", uncertainAt); err != nil {
		t.Fatalf("MarkUncertain: %v", err)
	}
	stored, err := service.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateUncertain {
		t.Fatalf("state = %q, want uncertain", stored.State)
	}
	if !stored.UncertainAt().Equal(uncertainAt) {
		t.Fatalf("UncertainAt = %s, want %s", stored.UncertainAt(), uncertainAt)
	}
	if stored.Attempt != claimed.Attempt {
		t.Fatalf("attempt moved during uncertainty: %d, want %d", stored.Attempt, claimed.Attempt)
	}
	if stored.Request.IdempotencyKey != claimed.Request.IdempotencyKey {
		t.Fatal("idempotency key changed during uncertainty")
	}

	// An uncertain request must not be re-claimed for another attempt, and it
	// must not be failed into a state that would permit default fallback.
	if _, err := service.Claim(context.Background(), claimed.ID, "dispatcher-b", now.Add(3*time.Second)); !errors.Is(err, ErrNotQueued) {
		t.Fatalf("Claim on uncertain request error = %v, want ErrNotQueued", err)
	}
	if err := service.Fail(context.Background(), claimed.ID, errors.New("give up"), now.Add(3*time.Second)); !errors.Is(err, ErrUnreconciled) {
		t.Fatalf("Fail on uncertain request error = %v, want ErrUnreconciled", err)
	}
}

func TestReconcileResolvesUncertaintyDefinitively(t *testing.T) {
	cases := []struct {
		name      string
		result    ReconcileResult
		wantState DeliveryState
		wantOut   Outcome
	}{
		{"accepted resubmits", ReconcileAccepted, StateSubmitted, OutcomeNone},
		{"rejected fails", ReconcileRejected, StateFailed, OutcomeRejected},
		{"unknown stays uncertain", ReconcileUnknown, StateUncertain, OutcomeNone},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			now := time.Now().UTC()
			service := NewService(beads.NewMemStore())
			claimed := claimedTestRecord(t, service, now)
			if err := service.MarkUncertain(context.Background(), claimed.ID, "lost receipt", now.Add(time.Second)); err != nil {
				t.Fatalf("MarkUncertain: %v", err)
			}
			reconciledAt := now.Add(2 * time.Second)
			if err := service.Reconcile(context.Background(), claimed.ID, testCase.result, reconciledAt); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			stored, err := service.Get(context.Background(), claimed.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != testCase.wantState {
				t.Fatalf("state = %q, want %q", stored.State, testCase.wantState)
			}
			if stored.Outcome() != testCase.wantOut {
				t.Fatalf("outcome = %q, want %q", stored.Outcome(), testCase.wantOut)
			}
			if !stored.ReconciledAt().Equal(reconciledAt) {
				t.Fatalf("ReconciledAt = %s, want %s", stored.ReconciledAt(), reconciledAt)
			}
			// Reconciliation never mints a new identity.
			if stored.Request.IdempotencyKey != claimed.Request.IdempotencyKey {
				t.Fatal("reconciliation changed the idempotency key")
			}
		})
	}
}

func TestRecordResponsePersistsFirstValidReceivedAtOnReplay(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	claimed := claimedTestRecord(t, service, now)
	submitTestReceipt(t, service, claimed, now.Add(time.Second))

	firstReceivedAt := now.Add(2 * time.Second)
	response := Response{
		RequestID:        claimed.Request.RequestID,
		Attempt:          claimed.Attempt,
		CorrelationID:    claimed.Request.CorrelationID,
		ResponseID:       "response-1",
		State:            "answered",
		ContentRetention: RetentionDurable,
		ReceivedAt:       firstReceivedAt,
	}
	if err := service.RecordResponse(context.Background(), response); err != nil {
		t.Fatalf("first RecordResponse: %v", err)
	}

	// The same response commitment observed later must not move the timestamp.
	replay := response
	replay.ReceivedAt = now.Add(30 * time.Second)
	if err := service.RecordResponse(context.Background(), replay); err != nil {
		t.Fatalf("replay RecordResponse: %v", err)
	}
	stored, err := service.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.ReceivedAt().Equal(firstReceivedAt) {
		t.Fatalf("ReceivedAt = %s, want first valid %s", stored.ReceivedAt(), firstReceivedAt)
	}
	if stored.State != StateOutcomeRecorded {
		t.Fatalf("state = %q, want outcome_recorded", stored.State)
	}
	if stored.Outcome() != OutcomeResponseRecorded {
		t.Fatalf("outcome = %q, want response_recorded", stored.Outcome())
	}
}

func TestRecordResponseRejectsConflictingBodyForSameRequest(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	claimed := claimedTestRecord(t, service, now)
	submitTestReceipt(t, service, claimed, now.Add(time.Second))

	response := Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-1",
		State:         "answered",
		ReceivedAt:    now.Add(2 * time.Second),
	}
	if err := service.RecordResponse(context.Background(), response); err != nil {
		t.Fatalf("first RecordResponse: %v", err)
	}
	conflicting := response
	conflicting.State = "refused"
	if err := service.RecordResponse(context.Background(), conflicting); err == nil {
		t.Fatal("conflicting response body for the same request was accepted")
	}
}

func TestRespondedStaysOpenWhenFollowUpIsRequired(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	claimed := claimedTestRecord(t, service, now)
	submitTestReceipt(t, service, claimed, now.Add(time.Second))

	if err := service.RecordResponse(context.Background(), Response{
		RequestID:        claimed.Request.RequestID,
		Attempt:          claimed.Attempt,
		CorrelationID:    claimed.Request.CorrelationID,
		ResponseID:       "response-1",
		State:            "needs_more",
		FollowUpRequired: true,
		ReceivedAt:       now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("RecordResponse: %v", err)
	}
	stored, err := service.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateResponded {
		t.Fatalf("state = %q, want responded while follow-up is required", stored.State)
	}
	if IsTerminal(stored.State) {
		t.Fatal("a response requiring follow-up must not be terminal")
	}
}

func TestOutcomeRecordedSurvivesServiceRestart(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	claimed := claimedTestRecord(t, service, now)
	submitTestReceipt(t, service, claimed, now.Add(time.Second))

	receivedAt := now.Add(2 * time.Second)
	response := Response{
		RequestID:        claimed.Request.RequestID,
		Attempt:          claimed.Attempt,
		CorrelationID:    claimed.Request.CorrelationID,
		ResponseID:       "response-restart",
		State:            "answered",
		ContentRetention: RetentionDurable,
		ReceivedAt:       receivedAt,
	}
	if err := service.RecordResponse(context.Background(), response); err != nil {
		t.Fatalf("RecordResponse: %v", err)
	}

	// A brand new service over the same durable store must read the same
	// outcome and timestamps, and must accept the exact replay idempotently.
	restarted := NewService(store)
	stored, err := restarted.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateOutcomeRecorded {
		t.Fatalf("state after restart = %q, want outcome_recorded", stored.State)
	}
	if stored.Outcome() != OutcomeResponseRecorded {
		t.Fatalf("outcome after restart = %q, want response_recorded", stored.Outcome())
	}
	if !stored.ReceivedAt().Equal(receivedAt) {
		t.Fatalf("ReceivedAt after restart = %s, want %s", stored.ReceivedAt(), receivedAt)
	}
	if err := restarted.RecordResponse(context.Background(), response); err != nil {
		t.Fatalf("exact replay after restart: %v", err)
	}
}

func TestUncertaintySurvivesServiceRestartAndStillBlocksRetry(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	claimed := claimedTestRecord(t, service, now)
	if err := service.MarkUncertain(context.Background(), claimed.ID, "timeout after send", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkUncertain: %v", err)
	}

	restarted := NewService(store)
	stored, err := restarted.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateUncertain {
		t.Fatalf("state after restart = %q, want uncertain", stored.State)
	}
	if _, err := restarted.Claim(context.Background(), claimed.ID, "dispatcher-c", now.Add(2*time.Second)); !errors.Is(err, ErrNotQueued) {
		t.Fatalf("Claim after restart error = %v, want ErrNotQueued", err)
	}
}

func TestVerifyTargetFenceFailsClosedOnStaleTarget(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	record := queuedTestRecord(t, service, now)

	rotated := record.Request.Target
	rotated.TargetID = "coord-b"
	rotated.ConfigRevision = 8

	if _, err := service.VerifyTargetFence(context.Background(), record.ID, rotated, now.Add(time.Second)); !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("VerifyTargetFence error = %v, want ErrStaleTarget", err)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateFailed {
		t.Fatalf("state = %q, want failed on stale fence", stored.State)
	}
	if stored.Outcome() != OutcomeStaleTarget {
		t.Fatalf("outcome = %q, want stale_target", stored.Outcome())
	}
	// The old record must never be redirected onto the newly configured target.
	if stored.Request.Target.TargetID != "coord-a" || stored.Request.Target.ConfigRevision != 7 {
		t.Fatalf("stale fence redirected the record to a new target: %+v", stored.Request.Target)
	}
}

func TestVerifyTargetFenceAcceptsTheConfiguredTarget(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	record := queuedTestRecord(t, service, now)

	verified, err := service.VerifyTargetFence(context.Background(), record.ID, record.Request.Target, now.Add(time.Second))
	if err != nil {
		t.Fatalf("VerifyTargetFence on the configured target: %v", err)
	}
	if verified.State != StateQueued {
		t.Fatalf("state = %q, want queued: a fence check is not a transition", verified.State)
	}
}

func TestDispatcherMarksLostReceiptUncertainRatherThanFailed(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	record := queuedTestRecord(t, service, now)

	dispatcher := &Dispatcher{
		Queue:   service,
		Adapter: &stubAdapter{err: errors.New("connection reset after request bytes were written")},
		Worker:  "dispatcher-a",
	}
	if _, _, err := dispatcher.DeliverNext(context.Background(), now.Add(time.Second)); err == nil {
		t.Fatal("DeliverNext returned no error for a lost receipt")
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateUncertain {
		t.Fatalf("state = %q, want uncertain: a lost receipt may already have been observed", stored.State)
	}
}

func TestDispatcherFailsDeliveryThatWasNeverAttempted(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	record := queuedTestRecord(t, service, now)

	dispatcher := &Dispatcher{
		Queue:   service,
		Adapter: &stubAdapter{err: fmt.Errorf("%w: callback URL is not configured", ErrDeliveryNotAttempted)},
		Worker:  "dispatcher-a",
	}
	if _, _, err := dispatcher.DeliverNext(context.Background(), now.Add(time.Second)); err == nil {
		t.Fatal("DeliverNext returned no error for an unattempted delivery")
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateFailed {
		t.Fatalf("state = %q, want failed: nothing was ever sent", stored.State)
	}
}

func TestDispatcherFailsClosedWhenTheConfiguredTargetRotated(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	record := queuedTestRecord(t, service, now)

	rotated := record.Request.Target
	rotated.ConfigRevision = 9
	adapter := &stubAdapter{receipt: DeliveryReceipt{State: StateQueued, Accepted: true}}
	dispatcher := &Dispatcher{Queue: service, Adapter: adapter, Worker: "dispatcher-a", Fence: &rotated}

	if _, _, err := dispatcher.DeliverNext(context.Background(), now.Add(time.Second)); !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("DeliverNext error = %v, want ErrStaleTarget", err)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter was called %d times despite a stale fence", adapter.calls)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Outcome() != OutcomeStaleTarget {
		t.Fatalf("outcome = %q, want stale_target", stored.Outcome())
	}
}

func TestHTTPAdapterRejectsRedirectInsteadOfFollowingIt(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()

	adapter := NewHTTPAdapter("hermes", redirecting.URL, Capability{CanSubmitPrompt: true})
	receipt, err := adapter.Deliver(context.Background(), Request{RequestID: "req-1", CorrelationID: "corr-1", Attempt: 1})
	if err == nil && receipt.State != StateFailed {
		t.Fatalf("redirect was not rejected: receipt = %+v, err = %v", receipt, err)
	}
	if followed {
		t.Fatal("adapter followed a redirect to another origin")
	}
}

func TestNewHTTPAdapterRejectsUnsafeCallbackURLForms(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"not a url", "::not-a-url"},
		{"non http scheme", "ftp://coordinator.example/callback"},
		{"plain http non loopback", "http://coordinator.example/callback"},
		{"userinfo", "https://user:pass@coordinator.example/callback"},
		{"empty host", "https:///callback"},
		{"query", "https://coordinator.example/callback?token=abc"},
		{"fragment", "https://coordinator.example/callback#frag"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := NewHTTPAdapter("hermes", testCase.url, Capability{})
			_, err := adapter.Deliver(context.Background(), Request{RequestID: "req-1", CorrelationID: "corr-1", Attempt: 1})
			if !errors.Is(err, ErrDeliveryNotAttempted) {
				t.Fatalf("Deliver with %q error = %v, want ErrDeliveryNotAttempted", testCase.url, err)
			}
		})
	}
	// Loopback HTTP stays available for local operation.
	if adapter := NewHTTPAdapter("hermes", "http://127.0.0.1:8080/callback", Capability{}); adapter.configErr != nil {
		t.Fatalf("loopback HTTP callback rejected: %v", adapter.configErr)
	}
}

func TestDurableRecordNeverPersistsCredentialsOrCallbackURLs(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	input := testRequestInput(now)
	input.RouteIdentity = map[string]string{"conversation": "abc-123"}
	record, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	durable := strings.ToLower(fmt.Sprint(stored.Metadata))
	for _, forbidden := range []string{"://", "authorization", "bearer ", "secret", "api_key"} {
		if strings.Contains(durable, forbidden) {
			t.Fatalf("durable record contains %q: %v", forbidden, stored.Metadata)
		}
	}
}

// stubAdapter is a deterministic Adapter for dispatcher policy tests.
type stubAdapter struct {
	receipt DeliveryReceipt
	err     error
	calls   int
}

func (a *stubAdapter) Name() string { return "stub" }

func (a *stubAdapter) Capabilities() Capability {
	return Capability{CanCreateSession: true, CanResumeSession: true, CanSubmitPrompt: true, CanReturnResults: true}
}

func (a *stubAdapter) Deliver(_ context.Context, request Request) (DeliveryReceipt, error) {
	a.calls++
	receipt := a.receipt
	receipt.RequestID = request.RequestID
	receipt.Attempt = request.Attempt
	receipt.CorrelationID = request.CorrelationID
	return receipt, a.err
}

// submitTestReceipt drives a claimed record to the submitted state.
func submitTestReceipt(t *testing.T, service *Service, claimed RequestRecord, now time.Time) {
	t.Helper()
	if err := service.Complete(context.Background(), claimed.ID, DeliveryReceipt{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		State:         StateQueued,
		Accepted:      true,
	}, now); err != nil {
		t.Fatalf("submit receipt: %v", err)
	}
}

func TestRecordNotificationDeliveredIsTerminalWithoutARecipientResponse(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	claimed := claimedTestRecord(t, service, now)
	submitTestReceipt(t, service, claimed, now.Add(time.Second))

	deliveredAt := now.Add(2 * time.Second)
	if err := service.RecordNotificationDelivered(context.Background(), claimed.ID, deliveredAt); err != nil {
		t.Fatalf("RecordNotificationDelivered: %v", err)
	}
	stored, err := service.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateOutcomeRecorded {
		t.Fatalf("state = %q, want outcome_recorded", stored.State)
	}
	if stored.Outcome() != OutcomeNotificationDelivered {
		t.Fatalf("outcome = %q, want notification_delivered", stored.Outcome())
	}
	if !stored.ReceivedAt().IsZero() {
		t.Fatal("a delivered notification must not invent a recipient response time")
	}
	// The terminal record replays idempotently rather than transitioning again.
	if err := service.RecordNotificationDelivered(context.Background(), claimed.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("replay of a delivered notification: %v", err)
	}
}

func TestUncertainNotificationCannotRecordADeliveredOutcome(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	claimed := claimedTestRecord(t, service, now)
	if err := service.MarkUncertain(context.Background(), claimed.ID, "timeout after send", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkUncertain: %v", err)
	}
	if err := service.RecordNotificationDelivered(context.Background(), claimed.ID, now.Add(2*time.Second)); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("RecordNotificationDelivered on uncertain error = %v, want ErrIllegalTransition", err)
	}
	if err := service.RecordResponse(context.Background(), Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-1",
		State:         "answered",
		ReceivedAt:    now.Add(2 * time.Second),
	}); !errors.Is(err, ErrUnreconciled) {
		t.Fatalf("RecordResponse on uncertain error = %v, want ErrUnreconciled", err)
	}
}

func TestUncertaintyClassNeverPersistsURLsOrCredentials(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	claimed := claimedTestRecord(t, service, now)

	if err := service.MarkUncertain(context.Background(), claimed.ID,
		"POST https://coordinator.example/callback failed with Authorization: Bearer abc123", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkUncertain: %v", err)
	}
	stored, err := service.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.UncertaintyClass(), "://") || strings.Contains(strings.ToLower(stored.UncertaintyClass()), "bearer") {
		t.Fatalf("uncertainty class leaked transport detail: %q", stored.UncertaintyClass())
	}
	raw, err := store.Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	durable := strings.ToLower(fmt.Sprint(raw.Metadata))
	for _, forbidden := range []string{"coordinator.example", "bearer abc123"} {
		if strings.Contains(durable, forbidden) {
			t.Fatalf("durable record leaked %q: %v", forbidden, raw.Metadata)
		}
	}
}

func TestFollowUpResponseAdvancesWithoutMovingFirstReceivedAt(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	claimed := claimedTestRecord(t, service, now)
	submitTestReceipt(t, service, claimed, now.Add(time.Second))

	firstReceivedAt := now.Add(2 * time.Second)
	if err := service.RecordResponse(context.Background(), Response{
		RequestID:        claimed.Request.RequestID,
		Attempt:          claimed.Attempt,
		CorrelationID:    claimed.Request.CorrelationID,
		ResponseID:       "response-1",
		State:            "needs_more",
		FollowUpRequired: true,
		ReceivedAt:       firstReceivedAt,
	}); err != nil {
		t.Fatalf("first response: %v", err)
	}

	finalReceivedAt := now.Add(10 * time.Second)
	if err := service.RecordResponse(context.Background(), Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-2",
		State:         "answered",
		ReceivedAt:    finalReceivedAt,
	}); err != nil {
		t.Fatalf("follow-up response: %v", err)
	}
	stored, err := service.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateOutcomeRecorded {
		t.Fatalf("state = %q, want outcome_recorded after the follow-up", stored.State)
	}
	if !stored.ReceivedAt().Equal(firstReceivedAt) {
		t.Fatalf("ReceivedAt = %s, want the first valid observation %s", stored.ReceivedAt(), firstReceivedAt)
	}
	if !stored.RespondedAt().Equal(finalReceivedAt) {
		t.Fatalf("RespondedAt = %s, want the latest response %s", stored.RespondedAt(), finalReceivedAt)
	}
}

func TestDispatcherMarksUnreachableCoordinatorUncertainThroughTheRealAdapter(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	record := queuedTestRecord(t, service, now)

	// Port 1 on loopback refuses the connection. The adapter cannot know
	// whether bytes were observed, so the delivery must not be failed
	// definitively.
	adapter := NewHTTPAdapter("unreachable", "http://127.0.0.1:1/callback",
		Capability{CanCreateSession: true, CanResumeSession: true, CanSubmitPrompt: true})
	dispatcher := Dispatcher{Queue: service, Adapter: adapter, Worker: "test-dispatcher"}
	if _, _, err := dispatcher.DeliverNext(context.Background(), now.Add(time.Second)); err == nil {
		t.Fatal("DeliverNext returned no error for an unreachable coordinator")
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateUncertain {
		t.Fatalf("state = %q, want uncertain", stored.State)
	}
	if stored.UncertaintyClass() == "" {
		t.Fatal("uncertainty class was not persisted")
	}
}

func TestUncertainRequestIsNeverSilentlyExpired(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	input := testRequestInput(now)
	// Expire almost immediately, so the read below is past the deadline.
	input.ExpiresAt = now.Add(time.Millisecond)
	record, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkUncertain(context.Background(), claimed.ID, "timeout after send", now); err != nil {
		t.Fatalf("MarkUncertain: %v", err)
	}

	// Expiry projection must not move an uncertain request to a terminal state:
	// the recipient may already hold it, so only reconciliation may resolve it.
	stored, err := service.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateUncertain {
		t.Fatalf("state = %q, want uncertain: expiry must not bypass reconciliation", stored.State)
	}
}
