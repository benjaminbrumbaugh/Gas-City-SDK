//go:build !darwin && !linux

package routingdecision

import "errors"

func renameNoReplace(string, string) error {
	return errors.New("no-replace rename unavailable")
}
