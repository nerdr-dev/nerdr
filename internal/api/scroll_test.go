package api

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// keptProc keeps what is written to its program.
type keptProc struct {
	spawn.Proc
	s *keptSpawner
}

func (p keptProc) Write(b []byte) (int, error) {
	p.s.mu.Lock()
	p.s.b.Write(b)
	p.s.mu.Unlock()
	return p.Proc.Write(b)
}

type keptSpawner struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *keptSpawner) Spawn(sp spawn.Spec) (spawn.Proc, error) {
	p, err := spawn.Local{}.Spawn(sp)
	if err != nil {
		return nil, err
	}
	return keptProc{p, s}, nil
}

// wait waits for what was written to the program to be want.
func (s *keptSpawner) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		got := s.b.String()
		s.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sent %q, want %q", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestScrollOverTheAPI: read says whether the program scrolls, scroll
// turns the wheel when it does and is refused, sending nothing, when it
// does not, and scrolling takes the program's size for the one scrolling.
func TestScrollOverTheAPI(t *testing.T) {
	m := testCore()
	sp := &keptSpawner{}
	p, err := term.StartWith(sp, spawn.Spec{Argv: []string{"cat"}, Dir: t.TempDir(), Env: os.Environ(), PTY: true, W: 40, H: 10}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	m.Tabs[0].Pane = p
	_, sock, keyFile := serve(t, m, nil)
	phone, laptop := dial(t, sock), dial(t, sock)
	phone.hello(keyFile)
	laptop.hello(keyFile)

	read := func(alt, wheel bool) {
		t.Helper()
		res := phone.ask(map[string]any{"op": "read", "target": "t1"})
		if res["alt"] != alt || res["wheel"] != wheel {
			t.Fatalf("read %v, want alt %v, wheel %v", res, alt, wheel)
		}
	}
	refused := func(what string) {
		t.Helper()
		phone.send(map[string]any{"op": "scroll", "agent": "t1", "lines": 3})
		if e, _ := phone.line()["error"].(string); !strings.Contains(e, "does not scroll") {
			t.Fatalf("%s: scroll answered %q", what, e)
		}
	}

	read(false, false)
	refused("the main screen")
	p.Feed([]byte("\x1b[?1049h"))
	read(true, false)
	refused("no mouse")
	p.Feed([]byte("\x1b[?1002h\x1b[?1006h"))
	read(true, true)

	phone.ask(map[string]any{"op": "view", "target": "t1", "cols": 40, "rows": 20})
	laptop.ask(map[string]any{"op": "view", "target": "t1", "cols": 100, "rows": 30})
	if w, h := p.Size(); w != 100 || h != 30 {
		t.Fatalf("%d×%d, want the laptop's 100×30", w, h)
	}
	phone.ask(map[string]any{"op": "scroll", "target": "t1", "lines": 2})
	phone.ask(map[string]any{"op": "scroll", "target": "t1", "lines": -1})
	phone.ask(map[string]any{"op": "scroll", "target": "t1", "lines": 0})
	if w, h := p.Size(); w != 40 || h != 20 {
		t.Fatalf("%d×%d after scrolling, want the phone's 40×20", w, h)
	}
	// Two steps up and one down at the middle of 40×20; the refused
	// scrolls sent nothing.
	sp.wait(t, strings.Repeat("\x1b[<64;21;11M", 2)+"\x1b[<65;21;11M")
}
