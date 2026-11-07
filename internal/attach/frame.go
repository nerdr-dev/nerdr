package attach

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// A frame is a type byte, the length of the body as 4 bytes big-endian,
// and the body. Every body but a Msg's is JSON; a Msg's is the caller's.
const (
	frameHello   byte = 'H' // client: Hello
	frameWelcome byte = 'W' // core: Welcome, the client is in
	frameRefused byte = 'R' // core: Refused, and the connection closes
	frameBye     byte = 'B' // either: Bye, and the connection closes
	frameMsg     byte = 'M' // either: what client and core say, once in
)

// MaxFrame is the longest body a frame may have. A screen with its
// scrollback is sent in parts well below it.
const MaxFrame = 16 << 20

func writeFrame(w io.Writer, typ byte, body []byte) error {
	if len(body) > MaxFrame {
		return fmt.Errorf("frame of %d bytes is over the limit", len(body))
	}
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(body)))
	if _, err := w.Write(append(hdr[:], body...)); err != nil {
		return err
	}
	return nil
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes is over the limit", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return hdr[0], body, nil
}

func writeJSON(w io.Writer, typ byte, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFrame(w, typ, b)
}

// Generation is the generation of the protocol between client and core.
// It changes only when a client and a core of different ones cannot work
// together; what one may lack goes in the features instead.
const Generation = 1

// Hello is what a client says first.
type Hello struct {
	Gen      int      `json:"gen"`
	Version  string   `json:"version"`
	Features []string `json:"features,omitempty"`
	Key      string   `json:"key"`
}

// Welcome is the core's answer to a client it lets in.
type Welcome struct {
	Gen      int      `json:"gen"`
	Version  string   `json:"version"`
	Features []string `json:"features,omitempty"`
}

// Refused is the core's answer to a client it does not let in: Reason is
// for the user. Gen and Version are the core's, so a client of another
// generation can say what to do.
type Refused struct {
	Reason  string `json:"reason"`
	Gen     int    `json:"gen,omitempty"`
	Version string `json:"version,omitempty"`
}

// Bye says the connection is closing, and why, for the user.
type Bye struct {
	Reason string `json:"reason"`
}

// Why a core says bye.
const (
	ByeTakenOver = "another nerdr connected to this core and took over"
	ByeQuit      = "nerdr quit"
)

// RefusedError is a connection the core refused.
type RefusedError struct{ Refused }

func (e *RefusedError) Error() string { return "the nerdr core refused: " + e.Reason }

// ByeError is a connection the other end closed, saying why.
type ByeError struct{ Bye }

func (e *ByeError) Error() string { return e.Reason }
