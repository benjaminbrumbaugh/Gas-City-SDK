package subprocess

import "github.com/gastownhall/gascity/internal/runtime"

// SupportsLocalBoundExecution recognizes only this built-in local process
// implementation, including the actual registry's seam-backed cutover. Remote,
// hybrid, and unknown provider wrappers are not inferred to be local.
func SupportsLocalBoundExecution(provider runtime.Provider) bool {
	switch provider.(type) {
	case *Provider, *seamBackedProvider:
		return true
	default:
		return false
	}
}
