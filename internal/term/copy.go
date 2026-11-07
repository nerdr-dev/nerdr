package term

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// Copy is a client's copy of a program's screen run elsewhere, kept from
// the messages a RowsSender makes of it. It is drawn as the screen itself
// is (it has Frame and the rest a client draws by), and what the user does
// in it goes to Out, to be sent to the program.
type Copy struct {
	mu   sync.Mutex
	view View
	// gen counts the messages taken: what a frame of it shows changes with
	// nothing else.
	gen uint64
	// started is when the program started, by the last message's
	// ExitAfter.
	started time.Time
	// scroll is how many lines back the client last asked to see; size the
	// size it last asked for, 0 when it has not.
	scroll int
	w, h   int
	copied string
	out    CopyOut
	// drawnOn is the frame the copy was last drawn on; see Drawn.
	drawnOn atomic.Uint64
}

// CopyOut is where a Copy sends what the user does in it.
type CopyOut interface {
	// Input is what is typed, as Pane.Input takes it; Write raw bytes.
	Input(b []byte)
	Write(b []byte)
	Mouse(ev MouseEvent)
	Focus(in bool)
	Resize(w, h int)
	// Scroll asks for the screen scroll lines back.
	Scroll(scroll int)
	// AskRows asks for lines from to to, as Screen.Rows gives them, and
	// gives them to done when they come, on a goroutine of its own; nil
	// when they do not.
	AskRows(from, to int, done func(int, [][]Cell))
}

// NewCopy is a copy with nothing in it yet, w×h, which sends to out.
func NewCopy(w, h int, out CopyOut) *Copy {
	c := &Copy{out: out, started: time.Now()}
	c.view.Mouse = -1
	c.blank(max(w, 1), max(h, 1))
	return c
}

// blank makes the copy's rows w×h and empty. c.mu is held.
func (c *Copy) blank(w, h int) {
	f := &c.view.Frame
	f.W, f.H = w, h
	if cap(f.Rows) < h {
		f.Rows = make([][]Cell, h)
	}
	f.Rows = f.Rows[:h]
	for y := range f.Rows {
		f.Rows[y] = blankRow(f.Rows[y], w)
	}
}

func blankRow(row []Cell, w int) []Cell {
	if cap(row) < w {
		row = make([]Cell, w)
	}
	row = row[:w]
	for x := range row {
		row[x] = Cell{Text: " ", Width: 1}
	}
	return row
}

// Apply takes a message of the screen. One that does not fit what the
// copy has is an error, and the copy is then to be sent every row again.
func (c *Copy) Apply(msg *ScreenMsg) error {
	if msg.W < 1 || msg.H < 1 || msg.W*msg.H > maxWireCells {
		return fmt.Errorf("screen of %d×%d", msg.W, msg.H)
	}
	styles := make([]uv.Style, len(msg.Styles))
	for i, ws := range msg.Styles {
		st, err := ws.style()
		if err != nil {
			return err
		}
		styles[i] = st
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f := &c.view.Frame
	if !msg.Full && (msg.W != f.W || msg.H != f.H) {
		return errors.New("rows of a screen of another size")
	}
	if msg.Full {
		c.blank(msg.W, msg.H)
	} else if k := msg.Shift; k > 0 && k < f.H {
		// The rows kept move up; the ones that fall off are used again
		// below, where rows are sent.
		rows := append([][]Cell(nil), f.Rows[:k]...)
		copy(f.Rows, f.Rows[k:])
		copy(f.Rows[f.H-k:], rows)
	}
	var err error
	for _, r := range msg.Rows {
		if r.Y < 0 || r.Y >= f.H {
			err = fmt.Errorf("row %d of %d", r.Y, f.H)
			continue
		}
		if e := setRow(f.Rows[r.Y], r.Runs, styles); e != nil {
			err = e
		}
	}
	v := &c.view
	v.W, v.H, v.Top, v.Scrollback, v.Scroll = msg.W, msg.H, msg.Top, msg.Scrollback, msg.Scroll
	v.CursorX, v.CursorY, v.Cursor, v.Alt = msg.CursorX, msg.CursorY, msg.Cursor, msg.Alt
	v.Title, v.Incomplete, v.Mouse = msg.Title, msg.Incomplete, msg.Mouse
	v.Exited, v.ExitErr, v.Busy = msg.Exited, msg.ExitErr, msg.Busy
	v.ExitAfter = time.Duration(msg.ExitAfter) * time.Millisecond
	c.started = time.Now().Add(-v.ExitAfter)
	if msg.Copied != "" {
		c.copied = msg.Copied
	}
	c.gen++
	v.Gen = c.gen
	return err
}

// setRow fills row from runs; what they leave out is blank. A row the runs
// do not fit is an error, and as much of it as they do is filled.
func setRow(row []Cell, runs []WireRun, styles []uv.Style) error {
	x := 0
	var err error
	for _, r := range runs {
		var st uv.Style
		if r.S > 0 && r.S <= len(styles) {
			st = styles[r.S-1]
		} else if r.S != 0 {
			err = fmt.Errorf("style %d of %d", r.S, len(styles))
		}
		if r.C != nil {
			if len(r.W) != len(r.C) {
				err = errors.New("cells and widths differ")
			}
			for i, t := range r.C {
				if x >= len(row) {
					return errors.New("row too long")
				}
				w := 1
				if i < len(r.W) {
					w = r.W[i]
				}
				row[x] = Cell{Text: t, Width: w, Style: st}
				x++
			}
			continue
		}
		for _, ch := range r.T {
			if x >= len(row) {
				return errors.New("row too long")
			}
			row[x] = Cell{Text: string(ch), Width: 1, Style: st}
			x++
		}
	}
	for ; x < len(row); x++ {
		row[x] = Cell{Text: " ", Width: 1}
	}
	return err
}

// DrawGen counts the messages the copy has taken.
func (c *Copy) DrawGen() uint64 {
	c.drawnOn.Store(frame.Load())
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// Frame is the copy as it is, scroll lines back: see FrameInto.
func (c *Copy) Frame(scroll int) Frame {
	var f Frame
	c.FrameInto(&f, scroll)
	return f
}

// FrameInto is the copy as last sent, into f. It is the screen scroll lines
// back once that has been sent: a scroll other than the one asked for last
// is asked for.
func (c *Copy) FrameInto(f *Frame, scroll int) {
	c.drawnOn.Store(frame.Load())
	c.mu.Lock()
	src := &c.view.Frame
	rows := f.Rows
	*f = *src
	if cap(rows) < len(src.Rows) {
		rows = make([][]Cell, len(src.Rows))
	}
	f.Rows = rows[:len(src.Rows)]
	for y, r := range src.Rows {
		f.Rows[y] = append(f.Rows[y][:0], r...)
	}
	ask := scroll != c.scroll
	c.scroll = scroll
	c.mu.Unlock()
	if ask {
		c.out.Scroll(scroll)
	}
}

// Drawn reports whether the copy was drawn on the frame drawn last (see
// NextFrame), and how many lines back: the screens a client shows, which
// are all it is sent.
func (c *Copy) Drawn() (bool, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drawnOn.Load() == frame.Load(), c.scroll
}

// Rows are lines from to to, as Screen.Rows gives them, of those the copy
// has: the screen sent. RowsLater has the others too.
func (c *Copy) Rows(from, to int) (int, [][]Cell) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := &c.view.Frame
	from, to = max(from, f.Top), min(to, f.Top+len(f.Rows)-1)
	var out [][]Cell
	for line := from; line <= to; line++ {
		out = append(out, append([]Cell(nil), f.Rows[line-f.Top]...))
	}
	return from, out
}

// RowsLater gives done lines from to to, as Screen.Rows gives them: there
// and then when the copy has them all, else once they have been asked for
// and come, on another goroutine. It never waits for them.
func (c *Copy) RowsLater(from, to int, done func(int, [][]Cell)) {
	c.mu.Lock()
	f := &c.view.Frame
	all := from >= f.Top && to < f.Top+len(f.Rows) && from <= to
	c.mu.Unlock()
	if all {
		done(c.Rows(from, to))
		return
	}
	c.out.AskRows(from, to, done)
}

// TopAt is the index of the first row a frame shows scroll lines back.
func (c *Copy) TopAt(scroll int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.view.Alt {
		return 0
	}
	sb := c.view.Scrollback
	return sb - min(max(scroll, 0), sb)
}

func (c *Copy) ScrollbackLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view.Scrollback
}

func (c *Copy) AltScreen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view.Alt
}

// Title is the window title the program last set.
func (c *Copy) Title() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view.Title
}

func (c *Copy) Input(b []byte) { c.out.Input(b) }
func (c *Copy) Write(b []byte) { c.out.Write(b) }
func (c *Copy) Focus(in bool)  { c.out.Focus(in) }

// SendMouse sends ev to the program when it takes the mouse, and reports
// whether it does, as Pane.SendMouse.
func (c *Copy) SendMouse(ev MouseEvent) bool {
	c.mu.Lock()
	lvl := c.view.Mouse
	c.mu.Unlock()
	if lvl < 0 {
		return false
	}
	c.out.Mouse(ev)
	return true
}

func (c *Copy) WantsMouse() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view.Mouse >= 0
}

func (c *Copy) WantsAllMotion() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view.Mouse == 3
}

// Resize asks for the program's screen to be w×h; Size is that from then.
func (c *Copy) Resize(w, h int) {
	c.mu.Lock()
	same := c.w == w && c.h == h
	c.w, c.h = w, h
	c.mu.Unlock()
	if !same {
		c.out.Resize(w, h)
	}
}

// Size is the size last asked for, else the size of the screen sent.
func (c *Copy) Size() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.w > 0 {
		return c.w, c.h
	}
	return c.view.W, c.view.H
}

func (c *Copy) TakeCopied() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.copied
	c.copied = ""
	return s
}

func (c *Copy) Exited() (bool, error, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if c.view.ExitErr != "" {
		err = errors.New(c.view.ExitErr)
	}
	return c.view.Exited, err, time.Since(c.started)
}

func (c *Copy) Busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view.Busy
}
