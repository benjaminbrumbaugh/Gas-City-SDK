package dashboardbff

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutingProxyForwardsOnlyKnownLoopbackEndpoints(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true,"message":"accepted"}`))
	}))
	defer daemon.Close()

	plane := New(Deps{RoutingDaemonBaseURL: daemon.URL})
	req := httptest.NewRequest(http.MethodPost, "/api/routing/resolve", strings.NewReader(`{"workId":"gc-1"}`))
	req.Header.Set("X-GC-Request", "dashboard")
	rec := httptest.NewRecorder()
	plane.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/routing/resolve" || gotBody != `{"workId":"gc-1"}` {
		t.Fatalf("forwarded = %s %s %q", gotMethod, gotPath, gotBody)
	}

	for _, path := range []string{"/api/routing/unknown", "/api/routing/../healthz"} {
		rec = httptest.NewRecorder()
		plane.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestRoutingProxyFailsClosedForMissingOrNonLoopbackDaemon(t *testing.T) {
	for name, base := range map[string]string{
		"missing":      "",
		"non-loopback": "https://example.com",
		"malformed":    "://bad",
	} {
		t.Run(name, func(t *testing.T) {
			plane := New(Deps{RoutingDaemonBaseURL: base})
			rec := httptest.NewRecorder()
			plane.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/routing/status", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "routing service unavailable") {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestRoutingProxyMapsDaemonFailureToStructuredServiceUnavailable(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	base := daemon.URL
	daemon.Close()
	plane := New(Deps{RoutingDaemonBaseURL: base})
	rec := httptest.NewRecorder()
	plane.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/routing/status", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "routing service unavailable") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
