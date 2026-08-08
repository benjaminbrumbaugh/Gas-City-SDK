package dashboardbff

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	routingProxyTimeout       = 10 * time.Second
	routingProxyMaxRequest    = 64 << 10
	routingProxyMaxResponse   = 4 << 20
	routingUnavailableMessage = "routing service unavailable"
)

type routingProxy struct {
	base   *url.URL
	client *http.Client
}

func newRoutingProxy(rawBase string) *routingProxy {
	rawBase = strings.TrimSpace(rawBase)
	if rawBase == "" {
		return &routingProxy{}
	}
	u, err := url.Parse(rawBase)
	if err != nil || u.Scheme != "http" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return &routingProxy{}
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !(strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		return &routingProxy{}
	}
	u.Path = ""
	return &routingProxy{base: u, client: &http.Client{Timeout: routingProxyTimeout}}
}

func (p *Plane) registerRoutingProxy() {
	for _, path := range []string{"status", "targets", "eligible", "decisions", "usage"} {
		path := path
		p.mux.HandleFunc("GET /api/routing/"+path, p.proxyRouting)
	}
	for _, path := range []string{"collect", "resolve"} {
		path := path
		p.mux.HandleFunc("POST /api/routing/"+path, p.proxyRouting)
	}
}

func (p *Plane) proxyRouting(w http.ResponseWriter, r *http.Request) {
	if p.routing == nil || p.routing.base == nil || p.routing.client == nil {
		writeError(w, http.StatusServiceUnavailable, routingUnavailableMessage)
		return
	}
	body, err := readBoundedRoutingBody(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "routing request too large")
		return
	}
	target := *p.routing.base
	target.Path = r.URL.Path
	target.RawQuery = r.URL.RawQuery
	ctx, cancel := context.WithTimeout(r.Context(), routingProxyTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, routingUnavailableMessage)
		return
	}
	if contentType := strings.TrimSpace(r.Header.Get("Content-Type")); contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json")
	if requestID := strings.TrimSpace(r.Header.Get("X-GC-Request")); requestID != "" {
		request.Header.Set("X-GC-Request", requestID)
	}
	response, err := p.routing.client.Do(request)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, routingUnavailableMessage)
		return
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, routingProxyMaxResponse+1))
	if err != nil || len(payload) > routingProxyMaxResponse {
		writeError(w, http.StatusBadGateway, "routing service returned an invalid response")
		return
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(payload)
}

func readBoundedRoutingBody(body io.ReadCloser) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	payload, err := io.ReadAll(io.LimitReader(body, routingProxyMaxRequest+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > routingProxyMaxRequest {
		return nil, fmt.Errorf("request exceeds %d bytes", routingProxyMaxRequest)
	}
	return payload, nil
}
