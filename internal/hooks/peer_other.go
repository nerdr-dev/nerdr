//go:build !linux && !darwin

package hooks

import "errors"

// Elsewhere nerdr cannot ask, and takes a tab's secret as before.
const peerSupported = false

func peerPid(int) (int, error) { return 0, errors.ErrUnsupported }

func parentPid(int) (int, error) { return 0, errors.ErrUnsupported }
