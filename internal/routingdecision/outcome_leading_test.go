package routingdecision

import "testing"

func TestValidateOutcomeOpaqueRejectsLeadingPunctuation(t *testing.T) {
	for _, value := range []string{".task", ":task", "_task", "/task", "-task"} {
		t.Run(value, func(t *testing.T) {
			if err := validateOutcomeOpaque("work_id", value); err == nil {
				t.Fatalf("leading-punctuation identifier %q accepted", value)
			}
		})
	}
}
