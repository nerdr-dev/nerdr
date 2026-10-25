package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"nerdr/internal/hooks"
)

// tabSocket is the tab the command runs in and the socket of the nerdr
// that runs it, for the commands an agent reports to nerdr with. When one
// is missing it writes why, prefixed with name, and ok is false: outside
// nerdr there is no tab, and outside says what that means for the
// command. Inside one, a sandbox around the agent may have dropped the
// variables, and the agent should be told so rather than go looking for
// another nerdr: any nerdr is only a client of the one running the tab.
func tabSocket(name, outside string) (tab, sock string, ok bool) {
	tab, sock = os.Getenv("NERDR_TAB"), os.Getenv("NERDR_SOCKET")
	if tab != "" && sock != "" {
		return tab, sock, true
	}
	switch {
	case os.Getenv("NERDR") != "1":
		fmt.Fprintf(os.Stderr, "%s: %s\n", name, outside)
	case os.Getenv("NERDR_SHELL") == "1":
		fmt.Fprintf(os.Stderr, "%s: this is a tab's shell; only the tab's agent reports to nerdr\n", name)
	case sock == "" && os.Getenv("NERDR_NO_SOCKET") != "":
		fmt.Fprintf(os.Stderr, "%s: the nerdr running this tab has no socket: %s. Another nerdr program will not help: tell the user.\n",
			name, os.Getenv("NERDR_NO_SOCKET"))
	default:
		missing := "NERDR_SOCKET"
		if tab == "" {
			missing = "NERDR_TAB"
		}
		fmt.Fprintf(os.Stderr, "%s: NERDR=1 is set but %s is not. Most likely a sandbox around the agent drops it: "+
			"let the NERDR_* variables through, or restart an agent an older nerdr started. "+
			"Another nerdr program will not help: tell the user.\n", name, missing)
	}
	return "", "", false
}

// send is hooks.Send, its error saying what to do when a sandbox keeps
// the agent from the socket or nerdr is gone.
func send(sock, method string, p map[string]any) error {
	_, err := request(sock, method, p)
	return err
}

// request is send, also returning what nerdr's answer says.
func request(sock, method string, p map[string]any) (string, error) {
	answer, err := hooks.Request(sock, method, p)
	return answer, explain(sock, err)
}

// explain is err saying what to do when a sandbox keeps the agent from
// the socket or nerdr is gone.
func explain(sock string, err error) error {
	switch {
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		return fmt.Errorf("%w. A sandbox around the agent keeps it from nerdr's socket: "+
			"allow connecting to %s. Another nerdr program will not help: tell the user", err, sock)
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%w: nerdr is not listening at %s. It has quit, or a sandbox around the agent hides the socket", err, sock)
	}
	return err
}
