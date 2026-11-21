package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/api"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
)

const apiUsage = `usage: nerdr api [FOLDER]
       nerdr api --schema

Connects standard input and output to the API of the nerdr running for
FOLDER (this folder when not given): JSON, one object a line. It says
hello with the key itself; the core's welcome is the first line out.
Then each line in is a request, and each line out an answer or, once
subscribed, an event. When input ends, the requests are answered and
it ends, unless it subscribed: then the events go on. For example:

  echo '{"id":1,"op":"state"}' | nerdr api ~/develop
  echo '{"op":"subscribe"}' | nerdr api ~/develop

Over ssh: ssh HOST nerdr api FOLDER. A nerdr must already run for the
folder. Programs nerdr runs, its agents among them, are refused.
See https://nerdr.app/api/requests/ for the requests and events.

--schema prints the API as JSON Schema, to generate a client's types
from; no nerdr need run. See https://nerdr.app/api/schema/.
`

// runAPI is `nerdr api`.
func runAPI(args []string) int {
	if len(args) > 1 || len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(os.Stderr, apiUsage)
		if len(args) == 1 {
			return 0
		}
		return 2
	}
	if len(args) == 1 && args[0] == "--schema" {
		if _, err := os.Stdout.Write(api.Schema); err != nil {
			return 1
		}
		return 0
	}
	folder := "."
	if len(args) == 1 {
		folder = args[0]
	}
	root, err := bridgeRoot(folder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr api:", err)
		return 1
	}
	c, err := dialAPI(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr api:", err)
		return 1
	}
	defer c.Close()
	// The welcome, or why the core refused: then no more comes.
	in := bufio.NewReaderSize(c, 64<<10)
	first, err := in.ReadBytes('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr api: the core closed the connection:", err)
		return 1
	}
	var refused api.Answer
	if json.Unmarshal(first, &refused) == nil && refused.Error != "" {
		fmt.Fprintln(os.Stderr, "nerdr api:", refused.Error)
		return 1
	}
	if _, err := os.Stdout.Write(first); err != nil {
		return 1
	}
	go func() {
		// What comes in, until it ends; then the core is told no more
		// comes, and answers what it was asked before it closes.
		_, _ = io.Copy(c, os.Stdin)
		if uc, ok := c.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
	}()
	if _, err := io.Copy(os.Stdout, in); err != nil && !errors.Is(err, net.ErrClosed) {
		fmt.Fprintln(os.Stderr, "nerdr api:", err)
		return 1
	}
	return 0
}

// dialAPI connects to the API socket of the core of root, and says hello
// with its key.
func dialAPI(root string) (net.Conn, error) {
	sock, keyFile, err := attach.APIPaths(root)
	if err != nil {
		return nil, err
	}
	c, err := dialAPIAt(sock, keyFile)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil, fmt.Errorf("no nerdr runs for %s: start nerdr there first", root)
	}
	return c, err
}

// dialAPIAt connects to the API socket sock, and says hello with the key
// in keyFile.
func dialAPIAt(sock, keyFile string) (net.Conn, error) {
	return dialAPIBy(sock, keyFile, time.Time{})
}

// dialAPIBy is dialAPIAt giving up at deadline, the zero time for none;
// the connection keeps deadline for what is read and written on it.
func dialAPIBy(sock, keyFile string, deadline time.Time) (net.Conn, error) {
	d := net.Dialer{Deadline: deadline}
	c, err := d.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(deadline)
	key, err := attach.ReadKey(keyFile)
	if err != nil {
		// Not wrapped: a core listens, so this is not a socket missing.
		_ = c.Close()
		return nil, fmt.Errorf("cannot read the API's key: %v", err)
	}
	b, _ := json.Marshal(api.Hello{Key: key, API: core.APIVersion})
	if _, err := c.Write(append(b, '\n')); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
