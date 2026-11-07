//go:build !linux

package sandboxcheck

// probeHolder finds no way to the holder's socket where there is no
// pidfd_getfd, and no /proc to open it through. Tracing it is not tried:
// on macOS that takes rights a sandboxed agent is not given.
func probeHolder(Target) []string { return nil }
