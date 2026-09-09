// Package launchorigin captures an opaque identifier for the actor that
// launched a piece of work, so a convoy can carry its launch origin without
// anyone having to register it.
//
// The captured value is opaque route data. This package does not parse it, and
// no caller may infer a provider, harness, role, runtime, URL, or credential
// from it. The only property it guarantees is that a non-empty result satisfies
// the opaque-identity rules of the convoy callback contract, so an origin
// captured here can be placed on a callback record without being rejected at
// the wire boundary.
//
// When no trustworthy actor route exists the capture is empty. An empty origin
// is not an error: it is the legacy shape, and callers must keep behaving
// exactly as they did before origin capture existed.
package launchorigin

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/convoycallback"
)

// routeKeys names the environment keys that may carry a trustworthy actor
// identity, in precedence order. The order mirrors the claim-actor resolution
// the agent-script and hook paths already use, so a launch is attributed to the
// same identity that claims work.
//
// These are ambient infrastructure keys, not role names: the package neither
// knows nor cares which agent a value denotes.
var routeKeys = []string{
	"GC_ALIAS",
	"BEADS_ACTOR",
	"GC_AGENT",
	"GC_SESSION_NAME",
}

// RouteKeys returns a copy of the actor route keys in precedence order. It is
// a copy so a caller inspecting the routes cannot reorder capture itself.
func RouteKeys() []string { return slices.Clone(routeKeys) }

// Capture resolves an opaque launch origin by consulting each actor route key
// in precedence order and returning the first usable value. lookup reads one
// key and returns its value, so the caller owns every environment read; pass
// os.Getenv in production.
//
// A route whose value is absent, blank, or unusable as an opaque identity is
// skipped rather than trusted, and capture falls through to the next route.
// Capture returns the empty string when no route yields a usable value.
func Capture(lookup func(string) string) string {
	if lookup == nil {
		return ""
	}
	for _, key := range routeKeys {
		if origin := Normalize(lookup(key)); origin != "" {
			return origin
		}
	}
	return ""
}

// Normalize trims raw and returns it when the result is usable as an opaque
// launch origin, or the empty string when it is not.
//
// A usable origin is non-blank, valid UTF-8, free of control characters, and
// within the callback contract's opaque-identity bound. Normalize rejects
// rather than repairs: silently truncating or rewriting an identity would
// attribute a launch to an actor that does not exist.
func Normalize(raw string) string {
	origin := strings.TrimSpace(raw)
	if origin == "" {
		return ""
	}
	if !utf8.ValidString(origin) {
		return ""
	}
	if len(origin) > convoycallback.MaxOpaqueIdentityLength {
		return ""
	}
	for _, r := range origin {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return origin
}
