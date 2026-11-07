//go:build !linux && !darwin

package spawn

import "errors"

// ProcInfo cannot ask the kernel here.
func ProcInfo(int) (int, uint64, error) { return 0, 0, errors.ErrUnsupported }
