package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutingDecisionIngestAllowsConfiguredLoopbackAuthorityTransport(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef0123456789abcdef"
	t.Setenv("GC_ROUTING_AUTHORITY_KEY", key)
	called := false
	handler := routingDecisionIngestPerimeter(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v0/city/Gas-City/routing/decisions", nil)
	req.RemoteAddr = "127.0.0.1:9000"
	req.Header.Set(writeAuthHeader, key)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !called || rec.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d body=%s", called, rec.Code, rec.Body.String())
	}

	called = false
	req = httptest.NewRequest(http.MethodPost, "/v0/city/Gas-City/routing/decisions", nil)
	req.RemoteAddr = "127.0.0.1:9000"
	req.Header.Set(writeAuthHeader, "wrong-key-wrong-key-wrong-key-wrong-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if called || rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("wrong key called=%v status=%d body=%s", called, rec.Code, rec.Body.String())
	}
}
