package app

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// NERDR_DEBUG_LOG names a file nerdr appends a line to for each focus
// change, each change of a tab's light and each notification, sent or not
// and why. A notification that never comes is otherwise silent: the light
// it follows is right, and the done it was about is seen as you come back.

var debugLog = struct {
	sync.Mutex
	path string
}{path: os.Getenv("NERDR_DEBUG_LOG")}

func debugf(format string, args ...any) {
	if debugLog.path == "" {
		return
	}
	debugLog.Lock()
	defer debugLog.Unlock()
	f, err := os.OpenFile(debugLog.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}
