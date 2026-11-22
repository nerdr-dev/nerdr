//go:build !linux

package spawn

// exited cannot wait for a child without reaping it here (no waitid in
// golang.org/x/sys/unix), so says it did not.
func exited(int) bool { return false }
