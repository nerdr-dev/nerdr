package holder

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"testing"
)

// A frame comes through whole, its descriptors with it, one after another.
func TestFramesCarryBytesAndFiles(t *testing.T) {
	a, b, err := connPair()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("in the file")
	defer f.Close()
	big := bytes.Repeat([]byte("ab"), MaxBody/2)
	go func() {
		a.send(&Msg{Op: OpSpawn, Seq: 1, Files: 1, Spawn: &Spawn{Argv: []string{"x"}}}, []byte("stdin"), []*os.File{f})
		a.send(&Msg{Op: OpWrite, Seq: 2}, big, nil)
		a.Close()
	}()
	m, body, files, err := b.recv()
	if err != nil {
		t.Fatal(err)
	}
	if m.Op != OpSpawn || m.Seq != 1 || string(body) != "stdin" || len(files) != 1 || m.Spawn.Argv[0] != "x" {
		t.Fatalf("got %+v %q %d files", m, body, len(files))
	}
	got, _ := io.ReadAll(io.NewSectionReader(files[0], 0, 100))
	files[0].Close()
	if string(got) != "in the file" {
		t.Errorf("file reads %q", got)
	}
	m, body, files, err = b.recv()
	if err != nil || m.Seq != 2 || !bytes.Equal(body, big) || len(files) != 0 {
		t.Fatalf("second frame: %+v, %d bytes, %v", m, len(body), err)
	}
	if _, _, _, err := b.recv(); err == nil {
		t.Error("read past the end")
	}
	if err := a.send(&Msg{Op: OpWrite}, make([]byte, MaxBody+1), nil); err == nil || !strings.Contains(err.Error(), "body") {
		t.Errorf("too big a body sent: %v", err)
	}
}

// A checkpoint's block may be larger than MaxBody, and only a checkpoint's:
// a frame of another op that big is refused as it is read.
func TestOnlyACheckpointMayBeLarge(t *testing.T) {
	a, b, err := connPair()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	block := bytes.Repeat([]byte("c"), 3*MaxBody)
	go a.send(&Msg{Op: OpCheckpoint, Seq: 1}, block, nil)
	m, body, _, err := b.recv()
	if err != nil || m.Op != OpCheckpoint || !bytes.Equal(body, block) {
		t.Fatalf("checkpoint: %+v, %d bytes, %v", m, len(body), err)
	}
	if err := a.send(&Msg{Op: OpCheckpoint}, make([]byte, MaxCheckpoint+1), nil); err == nil {
		t.Error("too big a checkpoint sent")
	}
	// A write that big, sent past send's check.
	h := []byte(`{"op":"write"}`)
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(h)))
	frame = binary.BigEndian.AppendUint32(frame, MaxBody+1)
	frame = append(append(frame, h...), make([]byte, MaxBody+1)...)
	go a.c.Write(frame)
	if _, _, _, err := b.recv(); err == nil || !strings.Contains(err.Error(), "write with a body") {
		t.Errorf("too big a write read: %v", err)
	}
}
