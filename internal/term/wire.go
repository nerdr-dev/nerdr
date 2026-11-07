package term

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strconv"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// A screen as it goes to a client in a process of its own: what Frame
// gives, as rows of runs of cells in one style, and only the rows changed
// since the last it was sent. The client keeps a Copy of it, which it
// draws as it would the screen itself.

// View is a Frame and what else a client is told of the program it shows:
// whether it takes the mouse, has exited or runs a command, and what it has
// put on the clipboard.
type View struct {
	Frame
	// Mouse is the most the program has asked to be told of the mouse, as
	// an index into mouseModes; -1 when it has not asked.
	Mouse int
	// Scroll is how many lines back the frame shows.
	Scroll    int
	Exited    bool
	ExitErr   string
	ExitAfter time.Duration
	Busy      bool
	// Copied is what the program has put on the clipboard since the last
	// view, taken from it.
	Copied string
}

// ViewInto is FrameInto, into v, with the rest of what a client is told.
// It takes what the program has copied.
func (p *Pane) ViewInto(v *View, scroll int) {
	p.FrameInto(&v.Frame, scroll)
	p.mu.Lock()
	v.Mouse = p.mouseLevel()
	if p.exited {
		v.Mouse = -1
	}
	v.Exited, v.ExitAfter = p.exited, time.Since(p.started)
	v.ExitErr = ""
	if p.exitErr != nil {
		v.ExitErr = p.exitErr.Error()
	}
	v.Copied, p.copied = p.copied, ""
	p.mu.Unlock()
	v.Scroll = scroll
	if v.Alt {
		v.Scroll = 0
	} else {
		v.Scroll = min(max(scroll, 0), v.Scrollback)
	}
	v.Busy = p.Busy()
}

// ScreenMsg is a View as it is sent: Full with every row, else only those
// that changed, after the rows kept are moved up Shift rows.
type ScreenMsg struct {
	W          int    `json:"w"`
	H          int    `json:"h"`
	Top        int    `json:"top"`
	Scrollback int    `json:"sb,omitempty"`
	Scroll     int    `json:"scroll,omitempty"`
	CursorX    int    `json:"cx,omitempty"`
	CursorY    int    `json:"cy,omitempty"`
	Cursor     bool   `json:"cur,omitempty"`
	Alt        bool   `json:"alt,omitempty"`
	Title      string `json:"title,omitempty"`
	Incomplete bool   `json:"inc,omitempty"`
	Mouse      int    `json:"mouse"`
	Exited     bool   `json:"exited,omitempty"`
	ExitErr    string `json:"err,omitempty"`
	// ExitAfter is View.ExitAfter in milliseconds.
	ExitAfter int64  `json:"after,omitempty"`
	Busy      bool   `json:"busy,omitempty"`
	Copied    string `json:"copied,omitempty"`

	Full   bool        `json:"full,omitempty"`
	Shift  int         `json:"shift,omitempty"`
	Styles []WireStyle `json:"styles,omitempty"`
	Rows   []WireRow   `json:"rows,omitempty"`
}

// WireRow is row Y of the frame, as runs of cells.
type WireRow struct {
	Y    int       `json:"y"`
	Runs []WireRun `json:"r"`
}

// WireRun is cells of one style: S is its place in Styles plus one, 0 for
// no style. T is the cells' text when each is one rune one column wide;
// else C has each cell's text and W its width, 0 for the tail of a wide
// character in the cell before.
type WireRun struct {
	S int      `json:"s,omitempty"`
	T string   `json:"t,omitempty"`
	C []string `json:"c,omitempty"`
	W []int    `json:"w,omitempty"`
}

// WireStyle is a uv.Style: its colours as wireColor writes them, the
// underline and the attributes.
type WireStyle struct {
	Fg string `json:"f,omitempty"`
	Bg string `json:"b,omitempty"`
	Uc string `json:"uc,omitempty"`
	U  uint8  `json:"u,omitempty"`
	A  uint8  `json:"a,omitempty"`
}

// RowsSender makes the messages of one screen for one client: the first
// with every row, then what changed since the one before. It is kept for as
// long as the client is, and Reset when the client may have missed one.
type RowsSender struct {
	cur, prev View
	sent      bool
	styles    map[uv.Style]int
	cells     []string
	widths    []int
}

// View is the view to fill for the next message.
func (s *RowsSender) View() *View { return &s.cur }

// Reset has the next message carry every row.
func (s *RowsSender) Reset() { s.sent = false }

// Next is the message of the view filled since the last, as JSON; the view
// then counts as sent.
func (s *RowsSender) Next() ([]byte, error) {
	msg := s.Msg()
	return json.Marshal(&msg)
}

// Msg is Next, before it is made JSON.
func (s *RowsSender) Msg() ScreenMsg {
	c, p := &s.cur, &s.prev
	msg := ScreenMsg{
		W: c.W, H: c.H, Top: c.Top, Scrollback: c.Scrollback, Scroll: c.Scroll,
		CursorX: c.CursorX, CursorY: c.CursorY, Cursor: c.Cursor, Alt: c.Alt,
		Title: c.Title, Incomplete: c.Incomplete, Mouse: c.Mouse,
		Exited: c.Exited, ExitErr: c.ExitErr, ExitAfter: c.ExitAfter.Milliseconds(),
		Busy: c.Busy, Copied: c.Copied,
	}
	full := !s.sent || p.W != c.W || p.H != c.H || len(p.Rows) != len(c.Rows)
	if s.styles == nil {
		s.styles = map[uv.Style]int{}
	}
	clear(s.styles)
	if full {
		msg.Full = true
		for y := range c.Rows {
			msg.Rows = append(msg.Rows, s.row(&msg, y, c.Rows[y]))
		}
	} else {
		k := shiftOf(p, c)
		msg.Shift = k
		for y := range c.Rows {
			if y+k < len(p.Rows) && sameRow(c.Rows[y], p.Rows[y+k]) {
				continue
			}
			msg.Rows = append(msg.Rows, s.row(&msg, y, c.Rows[y]))
		}
	}
	s.sent = true
	s.cur, s.prev = s.prev, s.cur
	return msg
}

// shiftOf is how many rows up the rows of p are best moved to make c: by
// what the scrollback grew, or where c's first row is found in p when the
// scrollback is full, or 0.
func shiftOf(p, c *View) int {
	h := len(c.Rows)
	if c.Alt != p.Alt || h < 2 {
		return 0
	}
	k := c.Top - p.Top
	if k <= 0 || k >= h {
		k = 0
		if c.Top == p.Top && !sameRow(c.Rows[0], p.Rows[0]) {
			for j := 1; j < h; j++ {
				if sameRow(c.Rows[0], p.Rows[j]) {
					k = j
					break
				}
			}
		}
	}
	if k == 0 {
		return 0
	}
	if kept(p, c, k) > kept(p, c, 0) {
		return k
	}
	return 0
}

// kept is how many rows of c need not be sent when p's are moved up k.
func kept(p, c *View, k int) int {
	n := 0
	for y := 0; y+k < len(p.Rows) && y < len(c.Rows); y++ {
		if sameRow(c.Rows[y], p.Rows[y+k]) {
			n++
		}
	}
	return n
}

func sameRow(a, b []Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Text != b[i].Text || a[i].Width != b[i].Width || !a[i].Style.Equal(&b[i].Style) {
			return false
		}
	}
	return true
}

// row is row y as runs, its styles added to msg's.
func (s *RowsSender) row(msg *ScreenMsg, y int, row []Cell) WireRow {
	out := WireRow{Y: y}
	for i := 0; i < len(row); {
		st := row[i].Style
		j := i
		for j < len(row) && row[j].Style.Equal(&st) {
			j++
		}
		out.Runs = append(out.Runs, s.run(msg, st, row[i:j]))
		i = j
	}
	return out
}

func (s *RowsSender) run(msg *ScreenMsg, st uv.Style, cells []Cell) WireRun {
	r := WireRun{S: s.style(msg, st)}
	plain := true
	n := 0
	for i := range cells {
		if !oneRune(cells[i]) {
			plain = false
			break
		}
		n += len(cells[i].Text)
	}
	if plain {
		b := make([]byte, 0, n)
		for i := range cells {
			b = append(b, cells[i].Text...)
		}
		r.T = string(b)
		return r
	}
	r.C = make([]string, len(cells))
	r.W = make([]int, len(cells))
	for i := range cells {
		r.C[i], r.W[i] = cells[i].Text, cells[i].Width
	}
	return r
}

// oneRune reports whether c is one rune one column wide, as a run's T
// gives it back.
func oneRune(c Cell) bool {
	if c.Width != 1 {
		return false
	}
	r, n := utf8.DecodeRuneInString(c.Text)
	return n > 0 && n == len(c.Text) && r != utf8.RuneError
}

// style is st's place in msg.Styles plus one, 0 for no style.
func (s *RowsSender) style(msg *ScreenMsg, st uv.Style) int {
	if st.IsZero() {
		return 0
	}
	key := st
	if i, ok := s.styles[key]; ok {
		return i
	}
	msg.Styles = append(msg.Styles, WireStyle{
		Fg: wireColor(st.Fg), Bg: wireColor(st.Bg), Uc: wireColor(st.UnderlineColor),
		U: uint8(st.Underline), A: st.Attrs,
	})
	i := len(msg.Styles)
	s.styles[key] = i
	return i
}

// wireColor is c as a string: "" for none, b and i and a number for a
// basic and an indexed colour, # and eight hex digits for an RGBA one, and
// w and sixteen for any other, by its RGBA64.
func wireColor(c color.Color) string {
	switch c := c.(type) {
	case nil:
		return ""
	case ansi.BasicColor:
		return "b" + strconv.Itoa(int(c))
	case ansi.IndexedColor:
		return "i" + strconv.Itoa(int(c))
	case color.RGBA:
		return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
	}
	r, g, b, a := c.RGBA()
	return fmt.Sprintf("w%04x%04x%04x%04x", r, g, b, a)
}

func colorOf(s string) (color.Color, error) {
	if s == "" {
		return nil, nil
	}
	bad := fmt.Errorf("bad colour %q", s)
	switch s[0] {
	case 'b', 'i':
		n, err := strconv.Atoi(s[1:])
		if err != nil || n < 0 || n > 255 {
			return nil, bad
		}
		if s[0] == 'b' {
			return ansi.BasicColor(n), nil
		}
		return ansi.IndexedColor(n), nil
	case '#':
		v, err := strconv.ParseUint(s[1:], 16, 32)
		if err != nil || len(s) != 9 {
			return nil, bad
		}
		return color.RGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
	case 'w':
		v, err := strconv.ParseUint(s[1:], 16, 64)
		if err != nil || len(s) != 17 {
			return nil, bad
		}
		return color.RGBA64{uint16(v >> 48), uint16(v >> 32), uint16(v >> 16), uint16(v)}, nil
	}
	return nil, bad
}

func (w WireStyle) style() (uv.Style, error) {
	var st uv.Style
	var err error
	if st.Fg, err = colorOf(w.Fg); err != nil {
		return st, err
	}
	if st.Bg, err = colorOf(w.Bg); err != nil {
		return st, err
	}
	if st.UnderlineColor, err = colorOf(w.Uc); err != nil {
		return st, err
	}
	st.Underline, st.Attrs = uv.Underline(w.U), w.A
	return st, nil
}

// maxWireCells is the most cells a screen sent may have, so a bad message
// cannot have a client make room for more.
const maxWireCells = 1 << 22
