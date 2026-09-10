package launchorigin

import (
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/convoycallback"
)

// fixedTime keeps callback fixtures deterministic.
func fixedTime() time.Time {
	return time.Date(2026, time.September, 8, 17, 0, 0, 0, time.UTC)
}

// envLookup builds a lookup function over a fixed map so a test never touches
// the process environment.
func envLookup(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func TestCaptureReadsEveryActorRouteKey(t *testing.T) {
	for _, key := range RouteKeys() {
		t.Run(key, func(t *testing.T) {
			got := Capture(envLookup(map[string]string{key: "actor-token"}))
			if got != "actor-token" {
				t.Fatalf("Capture(%s) = %q, want %q", key, got, "actor-token")
			}
		})
	}
}

func TestCapturePrefersTheEarlierRouteKey(t *testing.T) {
	keys := RouteKeys()
	if len(keys) < 2 {
		t.Fatalf("RouteKeys() = %v, want at least two entries to prove precedence", keys)
	}
	for i := 0; i < len(keys)-1; i++ {
		env := map[string]string{}
		for j := i; j < len(keys); j++ {
			env[keys[j]] = keys[j] + "-value"
		}
		want := keys[i] + "-value"
		if got := Capture(envLookup(env)); got != want {
			t.Fatalf("Capture with %v set = %q, want %q", keys[i:], got, want)
		}
	}
}

func TestCaptureReturnsEmptyWithoutATrustworthyRoute(t *testing.T) {
	cases := map[string]map[string]string{
		"no keys at all":     {},
		"unrelated key only": {"HOME": "/home/someone", "USER": "someone"},
		"empty values":       {"GC_ALIAS": "", "GC_AGENT": ""},
		"whitespace values":  {"GC_ALIAS": "   ", "GC_AGENT": "\t\n"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Capture(envLookup(env)); got != "" {
				t.Fatalf("Capture = %q, want empty so legacy behavior is preserved", got)
			}
		})
	}
}

func TestCaptureSkipsAnUnusableRouteAndFallsThrough(t *testing.T) {
	keys := RouteKeys()
	env := map[string]string{
		keys[0]: strings.Repeat("x", convoycallback.MaxOpaqueIdentityLength+1),
		keys[1]: "usable-token",
	}
	if got := Capture(envLookup(env)); got != "usable-token" {
		t.Fatalf("Capture = %q, want the first usable route value", got)
	}
}

func TestNormalizeTrimsSurroundingWhitespace(t *testing.T) {
	if got := Normalize("  actor-token\n"); got != "actor-token" {
		t.Fatalf("Normalize = %q, want %q", got, "actor-token")
	}
}

func TestNormalizeRejectsValuesTheCallbackContractWouldRefuse(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"whitespace only":    "   ",
		"control character":  "actor\x00token",
		"newline inside":     "actor\ntoken",
		"invalid utf-8":      "actor\xff\xfe",
		"over maximum bound": strings.Repeat("x", convoycallback.MaxOpaqueIdentityLength+1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Normalize(raw); got != "" {
				t.Fatalf("Normalize(%q) = %q, want empty", raw, got)
			}
		})
	}
}

// A normalized origin must satisfy the callback contract it is captured for,
// otherwise fan-out would emit records the wire rejects.
func TestNormalizeAcceptsTheMaximumBoundAndValidatesOnTheWire(t *testing.T) {
	raw := strings.Repeat("x", convoycallback.MaxOpaqueIdentityLength)
	got := Normalize(raw)
	if got != raw {
		t.Fatalf("Normalize on the exact bound = %q (len %d), want it accepted", got, len(got))
	}
	event := convoycallback.Event{
		SchemaVersion: convoycallback.ContractVersion,
		EventID:       "event-1",
		Type:          convoycallback.EventConvoyCreated,
		ConvoyID:      "convoy-1",
		LaunchOrigin:  got,
		CorrelationID: "correlation-1",
		OccurredAt:    fixedTime(),
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("a captured origin must validate on the callback wire: %v", err)
	}
}

func TestRouteKeysAreStableAndCopied(t *testing.T) {
	first := RouteKeys()
	first[0] = "MUTATED"
	if second := RouteKeys(); second[0] == "MUTATED" {
		t.Fatal("RouteKeys() must return a copy so a caller cannot rewrite the route order")
	}
}
