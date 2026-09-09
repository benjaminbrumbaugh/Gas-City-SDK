package main

import (
	"os"

	"github.com/gastownhall/gascity/internal/launchorigin"
)

// resolveLaunchOrigin returns the opaque identity of the actor launching work
// from this process.
//
// It is the single seam every local launch path uses, so a convoy is
// attributed the same way whether it was minted by `gc sling` or by
// `gc convoy create` — with no flag, no registration step, and no instruction
// to the agent. An explicit origin the caller already resolved wins; an
// explicit value the callback contract would reject falls back to capture
// rather than being stamped. The result is empty when no trustworthy actor
// route exists, which leaves the launch on its legacy path.
func resolveLaunchOrigin(explicit string) string {
	if origin := launchorigin.Normalize(explicit); origin != "" {
		return origin
	}
	return launchorigin.Capture(os.Getenv)
}
