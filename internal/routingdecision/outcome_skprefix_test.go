package routingdecision

import (
	"strings"
	"testing"
)

// TestValidateOutcomeOpaqueRejectsAnySkPrefixedValue pins the frozen Wayfinder
// safeString rule: every "sk-" prefixed identifier is rejected unconditionally,
// with no length-scoped exception, regardless of suffix content or case.
func TestValidateOutcomeOpaqueRejectsAnySkPrefixedValue(t *testing.T) {
	for _, value := range []string{
		"sk-" + strings.Repeat("a", 48),
		"sk-short",
		"sk-task",
		"SK-" + strings.Repeat("a", 48),
	} {
		t.Run(value, func(t *testing.T) {
			if err := validateOutcomeOpaque("work_id", value, true); err == nil {
				t.Fatalf("sk-prefixed identifier %q accepted", value)
			}
		})
	}
}
