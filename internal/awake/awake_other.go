//go:build !linux && !darwin

package awake

import "errors"

type holder struct{}

func (h *holder) hold(sleep, screen bool) error {
	if sleep {
		return errors.New("not on this system")
	}
	return nil
}
