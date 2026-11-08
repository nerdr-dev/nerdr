//go:build !linux && !darwin

package spawn

import "errors"

// Args cannot ask the kernel here.
func Args(int) ([]string, error) { return nil, errors.ErrUnsupported }
