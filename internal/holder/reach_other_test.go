//go:build !linux

package holder

import "errors"

// No /proc or pidfd here: only that no socket is inherited is tried.
func openThroughProc(pid, fd int) error { return errors.New("no /proc") }

func getfd(pid, fd int) error { return errors.New("no pidfd") }
