package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutingDecisionIngestAllowsSignedLoopbackWithoutTransportSecret(t *testing.T) {
	t.Setenv("GC_ROUTING_AUTHORITY_KEY", "")
	reached := false
	handler := routingDecisionIngestPerimeter(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "http://example.com/v0/city/Gas-City/routing/decisions", nil)
	request.RemoteAddr = "127.0.0.1:9000"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !reached || recorder.Code != http.StatusNoContent {
		t.Fatalf("signed-loopback perimeter status=%d reached=%v body=%s", recorder.Code, reached, recorder.Body.String())
	}
}
