package attach

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"syscall"
	"time"
)

// ErrNoCore is returned by Dial when no core listens at the socket: none
// runs for the folder, and one is to be started.
var ErrNoCore = errors.New("no nerdr core runs for this folder")

// Dial connects to the core at sock, with the key in keyFile, saying h:
// the key and the generation are filled in. A core that refuses gives a
// *RefusedError.
func Dial(sock, keyFile string, h Hello) (*Conn, Welcome, error) {
	c, err := net.DialTimeout("unix", sock, handshakeTimeout)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, Welcome{}, fmt.Errorf("%w (%s)", ErrNoCore, sock)
		}
		return nil, Welcome{}, err
	}
	// The key is read after the core is found: a core that died leaves
	// none to read.
	key, err := readKey(keyFile)
	if err != nil {
		_ = c.Close()
		return nil, Welcome{}, err
	}
	h.Key, h.Gen = key, Generation
	cn, w, err := Handshake(c, h)
	if err != nil {
		_ = c.Close()
		return nil, Welcome{}, err
	}
	return cn, w, nil
}

// Handshake says h on c, already connected to a core, and reads its
// answer.
func Handshake(c net.Conn, h Hello) (*Conn, Welcome, error) {
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := writeJSON(c, frameHello, h); err != nil {
		// A core that refuses the peer says why before it reads anything,
		// and hangs up: the reason may be waiting all the same.
		if typ, body, rerr := readFrame(c); rerr == nil && typ == frameRefused {
			var r Refused
			_ = json.Unmarshal(body, &r)
			return nil, Welcome{}, &RefusedError{r}
		}
		return nil, Welcome{}, err
	}
	typ, body, err := readFrame(c)
	if err != nil {
		return nil, Welcome{}, err
	}
	switch typ {
	case frameWelcome:
		var w Welcome
		if err := json.Unmarshal(body, &w); err != nil {
			return nil, Welcome{}, err
		}
		_ = c.SetDeadline(time.Time{})
		return newConn(c), w, nil
	case frameRefused:
		var r Refused
		_ = json.Unmarshal(body, &r)
		return nil, Welcome{}, &RefusedError{r}
	}
	return nil, Welcome{}, fmt.Errorf("unexpected frame %q from the core", typ)
}
