package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/citywriteauth"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

type routingTestState struct {
	*fakeState
	store     *routingdecision.Store
	verifier  routingdecision.Verifier
	targets   []routingdecision.TargetSnapshot
	selection routingdecision.SelectionSnapshot
}

func (s *routingTestState) RoutingDecisionStatus() routingdecision.LiveStatus {
	status, err := s.store.Status()
	if err != nil {
		panic(err)
	}
	return routingdecision.LiveStatus{
		Schema: routingdecision.SchemaVersion, Status: routingdecision.AvailabilityReady,
		Reason: routingdecision.ReasonReady, AuthorityReady: true,
		RetentionMonths: routingdecision.TerminalRetentionMonths, TerminalStateBasis: "latest_terminal_transition_at",
		Store: status,
	}
}

func (s *routingTestState) RoutingDecisionTargets(context.Context) ([]routingdecision.TargetSnapshot, error) {
	return append([]routingdecision.TargetSnapshot(nil), s.targets...), nil
}

func (s *routingTestState) RoutingDecisionEligible(context.Context) (routingdecision.SelectionSnapshot, error) {
	return s.selection, nil
}

func (s *routingTestState) RoutingDecisionList(_ context.Context, opts routingdecision.ListOptions) (routingdecision.DecisionPage, error) {
	return s.store.ListDecisions(opts)
}

func (s *routingTestState) RoutingDecisionIngest(_ context.Context, request routingdecision.IngestApprovedRequest) (routingdecision.IngestApprovedResult, error) {
	return s.store.IngestApproved(request, s.verifier)
}

func newRoutingTestState(t *testing.T, now time.Time) (*routingTestState, ed25519.PrivateKey) {
	t.Helper()
	base := newFakeState(t)
	store, err := routingdecision.OpenStore(base.cityPath, routingdecision.StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	targets := []routingdecision.TargetSnapshot{{
		Target: "worker", Rig: "myrig", Description: "safe", ResolvedProvider: "provider", ConfigDigest: strings.Repeat("a", 64),
	}}
	return &routingTestState{
		fakeState: base, store: store,
		verifier: routingdecision.NewVerifier(map[string]ed25519.PublicKey{"board": publicKey}),
		targets:  targets,
		selection: routingdecision.SelectionSnapshot{
			ObservedAt: now, Targets: targets,
			Work: []routingdecision.EligibleWorkSnapshot{{Rig: "myrig", Scope: "rig", WorkBeadID: "work-1", WorkRevision: 7, ClaimFence: 3, WorkStateDigest: strings.Repeat("b", 64)}},
		},
	}, privateKey
}

func signedRoutingIngest(t *testing.T, now time.Time, privateKey ed25519.PrivateKey) RoutingDecisionIngestBody {
	t.Helper()
	digest := func(value string) string {
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:])
	}
	payload := routingdecision.DecisionPayload{
		Schema: routingdecision.SchemaVersion, DecisionID: "decision-api", WorkBeadID: "work-1",
		WorkRevision: 7, ClaimFence: 3, WorkStateDigest: digest("work"), City: "test-city", Rig: "myrig",
		Target: "worker", TargetConfigDigest: digest("target"), PolicyDigest: digest("policy"),
		ObservationDigest: digest("observation"), Model: "model", Source: "selector", Account: "account",
		Evidence: []string{}, Alternatives: []routingdecision.Alternative{}, Options: []routingdecision.AuditOption{},
		CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), NoMigration: true,
	}
	payload.BindingID = routingdecision.BindingID(payload)
	approval := routingdecision.ApprovalPayload{
		Schema: routingdecision.SchemaVersion, DecisionID: payload.DecisionID, BindingID: payload.BindingID,
		AuthorityID: "board", ApprovedAt: now.Add(-time.Second),
	}
	signing, err := routingdecision.SigningBytes(payload, approval)
	if err != nil {
		t.Fatal(err)
	}
	return RoutingDecisionIngestBody{
		Payload: payload, Approval: approval,
		Signature: routingdecision.Signature{Algorithm: routingdecision.SignatureAlgorithmEd25519, AuthorityID: "board", Value: ed25519.Sign(privateKey, signing)},
	}
}

func decodeRoutingProblem(t *testing.T, rec *httptest.ResponseRecorder) apierr.ErrorModel {
	t.Helper()
	var problem apierr.ErrorModel
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem body: %v: %s", err, rec.Body.String())
	}
	return problem
}

func TestRoutingReadRoutesExposeTypedDeterministicSnapshots(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	state, _ := newRoutingTestState(t, now)
	h := newTestCityHandler(t, state)
	for _, path := range []string{"/routing/status", "/routing/targets", "/routing/eligible", "/routing/decisions?limit=1"} {
		req := httptest.NewRequest(http.MethodGet, cityURL(state, path), nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("GET %s content type = %q", path, rec.Header().Get("Content-Type"))
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, cityURL(state, "/routing/eligible"), nil))
	var selection routingdecision.SelectionSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &selection); err != nil {
		t.Fatal(err)
	}
	if !selection.ObservedAt.Equal(now) || len(selection.Work) != 1 || selection.Work[0].WorkBeadID != "work-1" ||
		len(selection.Targets) != 1 || selection.Targets[0].Target != "worker" {
		t.Fatalf("eligible snapshot = %+v", selection)
	}
}

func TestRoutingRoutesExposeExactTypedContract(t *testing.T) {
	sm := NewSupervisorMux(&stateCityResolver{state: newFakeState(t)}, nil, false, "test", "", time.Time{})
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json = %d: %s", rec.Code, rec.Body.String())
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
			} `json:"parameters"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	type operationContract struct {
		method, path, id string
	}
	for _, want := range []operationContract{
		{http.MethodGet, "/v0/city/{cityName}/routing/status", "get-routing-status"},
		{http.MethodGet, "/v0/city/{cityName}/routing/targets", "list-routing-targets"},
		{http.MethodGet, "/v0/city/{cityName}/routing/eligible", "get-routing-eligible"},
		{http.MethodGet, "/v0/city/{cityName}/routing/decisions", "list-routing-decisions"},
		{http.MethodPost, "/v0/city/{cityName}/routing/decisions", "ingest-routing-decision"},
	} {
		op, ok := spec.Paths[want.path][strings.ToLower(want.method)]
		if !ok || op.OperationID != want.id {
			t.Errorf("%s %s operation = (%t, %q), want %q", want.method, want.path, ok, op.OperationID, want.id)
		}
	}
	post := spec.Paths["/v0/city/{cityName}/routing/decisions"]["post"]
	requiredHeaders := map[string]bool{"X-GC-Request": false, "Idempotency-Key": false}
	for _, parameter := range post.Parameters {
		if parameter.In == "header" {
			if _, ok := requiredHeaders[parameter.Name]; ok && parameter.Required {
				requiredHeaders[parameter.Name] = true
			}
		}
	}
	for name, present := range requiredHeaders {
		if !present {
			t.Errorf("POST routing decisions missing required %s header", name)
		}
	}
	for _, status := range []string{"201", "400", "401", "403", "409", "413", "422", "500", "503"} {
		if _, ok := post.Responses[status]; !ok {
			t.Errorf("POST routing decisions missing documented %s response", status)
		}
	}
}

func TestRoutingIngestPerimeterRejectsBeforeWriteGrantConsumption(t *testing.T) {
	now := time.Now().UTC()
	state, routingPrivate := newRoutingTestState(t, now)
	body, err := json.Marshal(signedRoutingIngest(t, now, routingPrivate))
	if err != nil {
		t.Fatal(err)
	}
	path := cityURL(state, "/routing/decisions")

	withoutVerifier := NewSupervisorMux(&stateCityResolver{state: state}, nil, false, "test", "", now).WithAllowedHosts([]string{"example.com"})
	req := newPostRequest(path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "api-ingest")
	req.RemoteAddr = "127.0.0.1:9000"
	rec := httptest.NewRecorder()
	withoutVerifier.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "routing-ingest-unhardened") {
		t.Fatalf("missing write verifier = %d %s", rec.Code, rec.Body.String())
	}

	writePublic, writePrivate := mustKeypair(t)
	writeVerifier := realClockVerifier(t, writePublic)
	hardened := NewSupervisorMux(&stateCityResolver{state: state}, nil, false, "test", "", now).
		WithAllowedHosts([]string{"example.com"}).WithWriteAuth(writeVerifier)
	for _, tc := range []struct {
		name       string
		remoteAddr string
		forwarded  string
	}{
		{name: "blank"},
		{name: "private", remoteAddr: "192.168.1.5:9000", forwarded: "127.0.0.1"},
		{name: "public", remoteAddr: "198.51.100.5:9000", forwarded: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newPostRequest(path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "api-ingest-"+tc.name)
			req.Header.Set("X-GC-City-Write", "unconsumed-invalid-token")
			req.Header.Set("X-Forwarded-For", tc.forwarded)
			req.RemoteAddr = tc.remoteAddr
			rec := httptest.NewRecorder()
			hardened.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "routing-ingest-nonloopback") {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	binding := GrantBinding{
		Method: http.MethodPost,
		Path:   path,
		BodySHA256: func() string {
			sum := sha256.Sum256(body)
			return hex.EncodeToString(sum[:])
		}(),
		ReqDigest: citywriteauth.ReqDigest(http.MethodPost, path, "", body),
	}
	token, err := signingGrantSource(writePrivate, state.CityName())(binding)
	if err != nil {
		t.Fatal(err)
	}
	requestWithToken := func(remoteAddr string) *http.Request {
		req := newPostRequest(path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "api-ingest-preserved-grant")
		req.Header.Set("X-GC-City-Write", token)
		req.RemoteAddr = remoteAddr
		return req
	}
	nonloopback := httptest.NewRecorder()
	hardened.Handler().ServeHTTP(nonloopback, requestWithToken("198.51.100.5:9000"))
	if nonloopback.Code != http.StatusForbidden {
		t.Fatalf("non-loopback valid grant = %d: %s", nonloopback.Code, nonloopback.Body.String())
	}
	loopback := httptest.NewRecorder()
	hardened.Handler().ServeHTTP(loopback, requestWithToken("127.0.0.1:9000"))
	if loopback.Code != http.StatusCreated {
		t.Fatalf("same grant after perimeter rejection = %d: %s", loopback.Code, loopback.Body.String())
	}

	loopbackReq, err := http.NewRequest(http.MethodPost, "http://local"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response, err := hardened.LoopbackTransport().RoundTrip(loopbackReq)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		got, _ := io.ReadAll(response.Body)
		t.Fatalf("mutation through self-read transport = %d %s", response.StatusCode, got)
	}
}

func TestRoutingWireErrorsUseStableProblemCodes(t *testing.T) {
	now := time.Now().UTC().Round(0)
	state, routingPrivate := newRoutingTestState(t, now)
	writePublic, writePrivate := mustKeypair(t)
	h := NewSupervisorMux(&stateCityResolver{state: state}, nil, false, "test", "", now).
		WithAllowedHosts([]string{"example.com"}).WithWriteAuth(realClockVerifier(t, writePublic)).Handler()
	grantSource := signingGrantSource(writePrivate, state.CityName())
	post := func(t *testing.T, encoded []byte, key string) *httptest.ResponseRecorder {
		t.Helper()
		path := cityURL(state, "/routing/decisions")
		binding := GrantBinding{
			Method: http.MethodPost,
			Path:   path,
			BodySHA256: func() string {
				sum := sha256.Sum256(encoded)
				return hex.EncodeToString(sum[:])
			}(),
			ReqDigest: citywriteauth.ReqDigest(http.MethodPost, path, "", encoded),
		}
		token, err := grantSource(binding)
		if err != nil {
			t.Fatal(err)
		}
		req := newPostRequest(path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("X-GC-City-Write", token)
		req.RemoteAddr = "127.0.0.1:9000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	invalidCursor := httptest.NewRecorder()
	h.ServeHTTP(invalidCursor, httptest.NewRequest(http.MethodGet, cityURL(state, "/routing/decisions?limit=1&cursor=not-a-cursor"), nil))
	if invalidCursor.Code != http.StatusBadRequest || decodeRoutingProblem(t, invalidCursor).Code != "invalid-cursor" {
		t.Fatalf("invalid cursor = %d: %s", invalidCursor.Code, invalidCursor.Body.String())
	}

	invalidSignatureBody := signedRoutingIngest(t, now, routingPrivate)
	invalidSignatureBody.Signature.Value[0] ^= 0xff
	encoded, err := json.Marshal(invalidSignatureBody)
	if err != nil {
		t.Fatal(err)
	}
	invalidSignature := post(t, encoded, "invalid-signature")
	if invalidSignature.Code != http.StatusForbidden || decodeRoutingProblem(t, invalidSignature).Code != "routing-signature-refused" {
		t.Fatalf("invalid signature = %d: %s", invalidSignature.Code, invalidSignature.Body.String())
	}

	staleBody := signedRoutingIngest(t, now, routingPrivate)
	staleBody.Payload.ExpiresAt = now.Add(-30 * time.Second)
	staleBody.Payload.BindingID = routingdecision.BindingID(staleBody.Payload)
	staleBody.Approval.BindingID = staleBody.Payload.BindingID
	staleBody.Approval.ApprovedAt = now.Add(-45 * time.Second)
	signing, err := routingdecision.SigningBytes(staleBody.Payload, staleBody.Approval)
	if err != nil {
		t.Fatal(err)
	}
	staleBody.Signature.Value = ed25519.Sign(routingPrivate, signing)
	encoded, err = json.Marshal(staleBody)
	if err != nil {
		t.Fatal(err)
	}
	stale := post(t, encoded, "stale-decision")
	if stale.Code != http.StatusUnprocessableEntity || decodeRoutingProblem(t, stale).Code != "routing-decision-invalid" {
		t.Fatalf("stale decision = %d: %s", stale.Code, stale.Body.String())
	}

	validBody, err := json.Marshal(signedRoutingIngest(t, now, routingPrivate))
	if err != nil {
		t.Fatal(err)
	}
	missingIdempotencyKey := post(t, validBody, "")
	if missingIdempotencyKey.Code != http.StatusUnprocessableEntity || decodeRoutingProblem(t, missingIdempotencyKey).Code != "validation-failed" {
		t.Fatalf("missing idempotency key = %d: %s", missingIdempotencyKey.Code, missingIdempotencyKey.Body.String())
	}
	created := post(t, validBody, "created-decision")
	if created.Code != http.StatusCreated {
		t.Fatalf("valid decision = %d: %s", created.Code, created.Body.String())
	}
	conflict := post(t, validBody, "different-key-same-decision")
	if conflict.Code != http.StatusConflict || decodeRoutingProblem(t, conflict).Code != "routing-idempotency-conflict" {
		t.Fatalf("decision conflict = %d: %s", conflict.Code, conflict.Body.String())
	}

	unavailable := newTestCityHandler(t, newFakeState(t))
	rec := httptest.NewRecorder()
	unavailable.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v0/city/test-city/routing/status", nil))
	if rec.Code != http.StatusServiceUnavailable || decodeRoutingProblem(t, rec).Code != "routing-unavailable" {
		t.Fatalf("missing routing capability = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRoutingIngestTraversesFullMuxAndRealBoltStore(t *testing.T) {
	now := time.Now().UTC().Round(0)
	state, routingPrivate := newRoutingTestState(t, now)
	body, err := json.Marshal(signedRoutingIngest(t, now, routingPrivate))
	if err != nil {
		t.Fatal(err)
	}
	writePublic, writePrivate := mustKeypair(t)
	sm := NewSupervisorMux(&stateCityResolver{state: state}, nil, false, "test", "", now).
		WithAllowedHosts([]string{"example.com"}).WithWriteAuth(realClockVerifier(t, writePublic))
	path := cityURL(state, "/routing/decisions")
	binding := GrantBinding{
		Method: http.MethodPost, Path: path, BodySHA256: func() string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }(),
		ReqDigest: citywriteauth.ReqDigest(http.MethodPost, path, "", body),
	}
	token, err := signingGrantSource(writePrivate, state.CityName())(binding)
	if err != nil {
		t.Fatal(err)
	}
	req := newPostRequest(path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "api-ingest")
	req.Header.Set("X-GC-City-Write", token)
	req.RemoteAddr = "127.0.0.1:9000"
	rec := httptest.NewRecorder()
	sm.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest = %d: %s", rec.Code, rec.Body.String())
	}
	record, err := state.store.Get("decision-api")
	if err != nil || record.State != routingdecision.StateApproved {
		t.Fatalf("real ledger record = (%+v, %v)", record, err)
	}
}
