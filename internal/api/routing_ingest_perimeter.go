package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/citywriteauth"
)

const routingDecisionIngestTail = "/routing/decisions"

func isRoutingDecisionIngest(r *http.Request) bool {
	const prefix = "/v0/city/"
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	slash := strings.IndexByte(rest, '/')
	return slash > 0 && rest[slash:] == routingDecisionIngestTail
}

func directPeerIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.Unmap().IsLoopback()
}

func routingDecisionIngestPerimeter(verifier *citywriteauth.Verifier, next http.Handler) http.Handler {
	transportKey := strings.TrimSpace(os.Getenv("GC_ROUTING_AUTHORITY_KEY"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isRoutingDecisionIngest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !directPeerIsLoopback(r.RemoteAddr) {
			problemRoutingIngestNonloopback.writeTo(w)
			return
		}
		values := r.Header.Values(writeAuthHeader)
		if len(values) != 1 {
			problemRoutingIngestUnhardened.writeTo(w)
			return
		}
		if verifier == nil && !validRoutingTransportKey(transportKey, values[0]) {
			problemRoutingIngestUnhardened.writeTo(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validRoutingTransportKey(configured, supplied string) bool {
	configured = strings.TrimSpace(configured)
	supplied = strings.TrimSpace(supplied)
	if len(configured) < 32 || len(configured) != len(supplied) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(configured), []byte(supplied)) == 1
}

var (
	problemRoutingIngestUnhardened  = registeredProblemBody(apierr.RoutingIngestUnhardened, "routing ingest requires city-write verification or a configured loopback routing authority key")
	problemRoutingIngestNonloopback = registeredProblemBody(apierr.RoutingIngestNonloopback, "routing ingest requires a direct loopback peer")
)
