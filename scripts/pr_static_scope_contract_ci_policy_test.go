//go:build ci_policy

package scripts_test

import "testing"

func TestChangedStaticTargetsScopeLintAndFormattingToTheDiff(t *testing.T) {
	testChangedStaticTargetsScopeLintAndFormattingToTheDiff(t)
}
