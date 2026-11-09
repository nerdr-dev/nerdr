package attach

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// A client on another machine reaches a core through a bridge: `nerdr
// bridge`, run over ssh on the core's machine, with the client's stream
// as its stdin and stdout. The bridge first says what it is (Preamble),
// then takes the client's Hello, puts in the key from the key file and
// the environment of the core's machine, and passes it on to the client
// socket; from then on it copies bytes both ways. The key never leaves
// the core's machine, the client's environment never reaches it, and the
// core sees the bridge, a process there, in its check of who connected.

// preambleMark starts what a bridge says first. It and the line after it
// never change, whatever the generation: a client of any generation reads
// them to tell what the other machine runs.
const preambleMark = "\x00nerdr-bridge "

// maxPreambleSkip is how much a client skips before the bridge's preamble:
// what a shell on the core's machine writes as it starts, say.
const maxPreambleSkip = 64 << 10

// Preamble is what a bridge of version says first: the generation and
// the version.
func Preamble(version string) []byte {
	return fmt.Appendf(nil, "%s%d %s\n", preambleMark, Generation, strings.ReplaceAll(version, "\n", " "))
}

// ReadPreamble reads a bridge's preamble from r, what came before it
// skipped: the bridge's generation and version. Nothing after it is read.
func ReadPreamble(r io.Reader) (gen int, version string, err error) {
	var seen []byte
	b := make([]byte, 1)
	for !bytes.HasSuffix(seen, []byte(preambleMark)) {
		if len(seen) > maxPreambleSkip+len(preambleMark) {
			return 0, "", errors.New("no nerdr bridge answered")
		}
		if _, err := io.ReadFull(r, b); err != nil {
			return 0, "", fmt.Errorf("no nerdr bridge answered: %w", err)
		}
		seen = append(seen, b[0])
	}
	var line []byte
	for {
		if _, err := io.ReadFull(r, b); err != nil {
			return 0, "", err
		}
		if b[0] == '\n' {
			break
		}
		if line = append(line, b[0]); len(line) > 256 {
			return 0, "", errors.New("a nerdr bridge's preamble is too long")
		}
	}
	g, v, _ := strings.Cut(string(line), " ")
	if gen, err = strconv.Atoi(g); err != nil {
		return 0, "", fmt.Errorf("a nerdr bridge's preamble: %q", line)
	}
	return gen, v, nil
}

// Bridge passes on what a client says on in, from another machine, to
// core, a connection to the client socket of a core here, and what core
// says back to out. The client's first frame is its Hello: the key in
// keyFile and env, the environment of this machine for the programs core
// starts, take the place of whatever it gave. It returns once either end
// has closed.
func Bridge(in io.Reader, out io.Writer, core net.Conn, keyFile string, env []string) error {
	typ, body, err := readFrame(in)
	if err != nil {
		return err
	}
	var h Hello
	if typ != frameHello || json.Unmarshal(body, &h) != nil {
		err := errors.New("a client must say hello first")
		_ = Refuse(out, err.Error())
		return err
	}
	if h.Key, err = readKey(keyFile); err != nil {
		_ = Refuse(out, err.Error())
		return err
	}
	h.Env = env
	if err := writeJSON(core, frameHello, h); err != nil {
		return err
	}
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(core, in)
		if c, ok := core.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
		done <- err
	}()
	go func() {
		_, err := io.Copy(out, core)
		done <- err
	}()
	return <-done
}

// Refuse says to a client, which has said or is about to say its Hello,
// that it is not let in, and why: as a core refuses one, for a bridge
// that cannot reach the core.
func Refuse(w io.Writer, reason string) error {
	return writeJSON(w, frameRefused, Refused{Reason: reason, Gen: Generation})
}
