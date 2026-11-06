package vt

// This file is nerdr's: a way to take all of an emulator's state out as
// bytes and put it back, which needs its private fields. See UPSTREAM.md.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"slices"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// CheckpointVersion is the version of what Checkpoint writes. Restore takes
// no other: a change to what is written, or to the fields it is read into,
// must change it.
const CheckpointVersion = 1

const checkpointMagic = "nerdr-vt"

// Limits on what Restore takes, so a damaged checkpoint cannot make it
// allocate without end.
const (
	maxCheckpointSide  = 1 << 14
	maxCheckpointLines = 1 << 20
	maxCheckpointStr   = 16 << 20
)

// ErrNotAtGround is returned by Checkpoint while the parser is inside a
// sequence or a UTF-8 character: what it holds of it is not taken out.
var ErrNotAtGround = errors.New("vt: parser is inside a sequence")

// AtGround reports whether a checkpoint can be taken: the parser is not
// inside an escape sequence or a UTF-8 character.
func (e *Emulator) AtGround() bool { return e.parser.State() == parser.GroundState }

// Checkpoint is all of the emulator's state as bytes, for Restore: both
// screens and their scrollback, cursors, modes, colours, character sets, tab
// stops, title and a pending wrap. The handlers and callbacks are not in it.
// It fails with ErrNotAtGround inside a sequence; see AtGround.
func (e *Emulator) Checkpoint() ([]byte, error) {
	if !e.AtGround() {
		return nil, ErrNotAtGround
	}
	if len(e.grapheme) > 0 {
		// Write flushes the cluster at its end, so this is not reached.
		e.flushGrapheme()
	}
	var body ckWriter
	body.styles = map[string]int{}
	body.uvarint(uint64(e.Width()))
	body.uvarint(uint64(e.Height()))
	body.bool(e.scr == &e.scrs[1])
	for i := range e.scrs {
		body.screen(&e.scrs[i])
	}
	modes := make([]ansi.Mode, 0, len(e.modes))
	for m := range e.modes {
		modes = append(modes, m)
	}
	slices.SortFunc(modes, func(a, b ansi.Mode) int {
		if da, db := isDEC(a), isDEC(b); da != db {
			if da {
				return 1
			}
			return -1
		}
		return a.Mode() - b.Mode()
	})
	body.uvarint(uint64(len(modes)))
	for _, m := range modes {
		body.bool(isDEC(m))
		body.uvarint(uint64(m.Mode()))
		body.byte(byte(e.modes[m]))
	}
	n := 0
	for _, c := range e.colors {
		if c != nil {
			n++
		}
	}
	body.uvarint(uint64(n))
	for i, c := range e.colors {
		if c != nil {
			body.uvarint(uint64(i))
			body.color(c)
		}
	}
	for _, c := range []color.Color{e.defaultFg, e.defaultBg, e.defaultCur, e.fgColor, e.bgColor, e.curColor} {
		body.color(c)
	}
	for _, cs := range e.charsets {
		body.charset(cs)
	}
	body.varint(int64(e.lastChar))
	body.str(string(e.grapheme))
	body.str(e.iconName)
	body.str(e.title)
	body.str(e.cwd)
	var tabs []int
	for x := 0; x < e.Width(); x++ {
		if e.tabstops.IsStop(x) {
			tabs = append(tabs, x)
		}
	}
	body.uvarint(uint64(len(tabs)))
	for _, x := range tabs {
		body.uvarint(uint64(x))
	}
	body.varint(int64(e.gl))
	body.varint(int64(e.gr))
	body.varint(int64(e.gsingle))
	body.bool(e.atPhantom)

	var out ckWriter
	out.b = append(out.b, checkpointMagic...)
	out.uvarint(CheckpointVersion)
	out.uvarint(uint64(len(body.styleList)))
	for _, s := range body.styleList {
		out.str(s)
	}
	out.b = append(out.b, body.b...)
	return out.b, nil
}

func isDEC(m ansi.Mode) bool { _, ok := m.(ansi.DECMode); return ok }

// CheckpointVersionOf is the version a checkpoint was written with, or an
// error if it is not one.
func CheckpointVersionOf(b []byte) (uint64, error) {
	r := ckReader{b: b}
	r.magic()
	v := r.uvarint()
	return v, r.err
}

// Restore puts a checkpoint into the emulator, which takes its size. Its
// handlers and callbacks stay its own, and none of them are called. If the
// checkpoint cannot be read, the emulator is left as it was.
func (e *Emulator) Restore(b []byte) error {
	r := ckReader{b: b}
	r.magic()
	if v := r.uvarint(); r.err == nil && v != CheckpointVersion {
		return fmt.Errorf("vt: checkpoint version %d, want %d", v, CheckpointVersion)
	}
	ns := r.count(maxCheckpointLines)
	for range ns {
		if r.err != nil {
			break
		}
		r.styles = append(r.styles, r.styleOf(r.str()))
	}
	w, h := r.count(maxCheckpointSide), r.count(maxCheckpointSide)
	if w*h*2 > len(r.b) {
		// Each cell takes a byte at least: the screens must be there
		// before they are made.
		r.fail("short screens")
	}
	alt := r.bool()
	var scrs [2]Screen
	for i := range scrs {
		scrs[i] = r.screen(w, h)
	}
	modes := ansi.Modes{}
	for range r.count(1 << 16) {
		dec, n, set := r.bool(), int(r.uvarint()), ansi.ModeSetting(r.byte())
		if dec {
			modes[ansi.DECMode(n)] = set
		} else {
			modes[ansi.ANSIMode(n)] = set
		}
	}
	var colors [256]color.Color
	for range r.count(256) {
		i := r.uvarint()
		c := r.color()
		if i >= 256 {
			r.fail("colour index")
			break
		}
		colors[i] = c
	}
	var cols [6]color.Color
	for i := range cols {
		cols[i] = r.color()
	}
	var charsets [4]CharSet
	for i := range charsets {
		charsets[i] = r.charset()
	}
	lastChar := rune(r.varint())
	grapheme := r.str()
	iconName, title, cwd := r.str(), r.str(), r.str()
	tabs := uv.DefaultTabStops(w)
	tabs.Clear()
	for range r.count(maxCheckpointSide) {
		x := int(r.uvarint())
		if x < w {
			tabs.Set(x)
		}
	}
	gl, gr, gsingle := int(r.varint()), int(r.varint()), int(r.varint())
	phantom := r.bool()
	for _, g := range []int{gl, gr, gsingle} {
		if g < 0 || g >= len(charsets) {
			r.fail("character set")
		}
	}
	if r.err == nil && len(r.b) != 0 {
		r.fail("trailing bytes")
	}
	if r.err != nil {
		return r.err
	}

	for i := range scrs {
		cb := e.scrs[i].cb
		e.scrs[i] = scrs[i]
		e.scrs[i].cb = cb
	}
	e.scr = &e.scrs[0]
	if alt {
		e.scr = &e.scrs[1]
	}
	e.modes = modes
	e.colors = colors
	e.defaultFg, e.defaultBg, e.defaultCur = cols[0], cols[1], cols[2]
	e.fgColor, e.bgColor, e.curColor = cols[3], cols[4], cols[5]
	e.charsets = charsets
	e.lastChar = lastChar
	e.grapheme = append(e.grapheme[:0], grapheme...)
	e.iconName, e.title, e.cwd = iconName, title, cwd
	e.tabstops = tabs
	e.gl, e.gr, e.gsingle = gl, gr, gsingle
	e.atPhantom = phantom
	return nil
}

// ckWriter writes a checkpoint. Each distinct style and link of a cell or a
// cursor is written once, in a table, and referred to by its place in it.
type ckWriter struct {
	b []byte

	styles    map[string]int
	styleList []string
}

func (w *ckWriter) byte(b byte)      { w.b = append(w.b, b) }
func (w *ckWriter) uvarint(v uint64) { w.b = binary.AppendUvarint(w.b, v) }
func (w *ckWriter) varint(v int64)   { w.b = binary.AppendVarint(w.b, v) }
func (w *ckWriter) str(s string)     { w.uvarint(uint64(len(s))); w.b = append(w.b, s...) }
func (w *ckWriter) bool(v bool) {
	if v {
		w.byte(1)
	} else {
		w.byte(0)
	}
}
func (w *ckWriter) pos(p uv.Position)   { w.varint(int64(p.X)); w.varint(int64(p.Y)) }
func (w *ckWriter) rect(r uv.Rectangle) { w.pos(r.Min); w.pos(r.Max) }

// Colour kinds: what the emulator makes is kept as it is; any other colour
// comes back as a color.RGBA64 of the same value.
const (
	ckNoColor = iota
	ckBasic
	ckIndexed
	ckRGBA
	ckRGBA64
)

func (w *ckWriter) color(c color.Color) {
	switch c := c.(type) {
	case nil:
		w.byte(ckNoColor)
	case ansi.BasicColor:
		w.byte(ckBasic)
		w.byte(byte(c))
	case ansi.IndexedColor:
		w.byte(ckIndexed)
		w.byte(byte(c))
	case color.RGBA:
		w.byte(ckRGBA)
		w.b = append(w.b, c.R, c.G, c.B, c.A)
	default:
		w.byte(ckRGBA64)
		r, g, b, a := c.RGBA()
		for _, v := range []uint32{r, g, b, a} {
			w.b = binary.BigEndian.AppendUint16(w.b, uint16(v))
		}
	}
}

// style is the place in the table of a style with a link; the plain one
// is 0.
func (w *ckWriter) style(s *uv.Style, l *uv.Link) int {
	if s.IsZero() && l.URL == "" && l.Params == "" {
		return 0
	}
	var k ckWriter
	k.color(s.Fg)
	k.color(s.Bg)
	k.color(s.UnderlineColor)
	k.byte(byte(s.Underline))
	k.byte(s.Attrs)
	k.str(l.URL)
	k.str(l.Params)
	key := string(k.b)
	i, ok := w.styles[key]
	if !ok {
		i = len(w.styleList) + 1
		w.styles[key] = i
		w.styleList = append(w.styleList, key)
	}
	return i
}

// Cell codes in a line: a printable ASCII byte is a cell of that one
// character, of width 1, in the style in force; ckStyle changes the style
// for the cells after it; ckCell is any other cell.
const (
	ckCell  = 0x00
	ckStyle = 0x01
)

func (w *ckWriter) line(l uv.Line) {
	w.uvarint(uint64(len(l)))
	cur := 0
	for i := range l {
		c := &l[i]
		// Most cells have the style of the one before: the table is only
		// looked in when it changes.
		if i == 0 || c.Link != l[i-1].Link || !c.Style.Equal(&l[i-1].Style) {
			if s := w.style(&c.Style, &c.Link); s != cur {
				w.byte(ckStyle)
				w.uvarint(uint64(s))
				cur = s
			}
		}
		if len(c.Content) == 1 && c.Content[0] >= 0x20 && c.Content[0] < 0x7f && c.Width == 1 {
			w.byte(c.Content[0])
			continue
		}
		w.byte(ckCell)
		w.str(c.Content)
		w.uvarint(uint64(c.Width))
	}
}

func (w *ckWriter) cursor(c *Cursor) {
	w.uvarint(uint64(w.style(&c.Pen, &c.Link)))
	w.pos(c.Position)
	w.varint(int64(c.Style))
	w.bool(c.Steady)
	w.bool(c.Hidden)
}

func (w *ckWriter) screen(s *Screen) {
	for y := range s.buf.Height() {
		w.line(s.buf.Line(y))
	}
	w.cursor(&s.cur)
	w.cursor(&s.saved)
	w.rect(s.scroll)
	w.bool(s.scrollback != nil)
	if s.scrollback != nil {
		w.uvarint(uint64(s.scrollback.maxLines))
		w.uvarint(uint64(len(s.scrollback.lines)))
		for _, l := range s.scrollback.lines {
			w.line(l)
		}
	}
}

func (w *ckWriter) charset(cs CharSet) {
	w.bool(cs != nil)
	keys := make([]byte, 0, len(cs))
	for k := range cs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	w.uvarint(uint64(len(keys)))
	for _, k := range keys {
		w.byte(k)
		w.str(cs[k])
	}
}

// ckReader reads a checkpoint. The first error is kept and every read
// after it gives a zero value.
type ckReader struct {
	b      []byte
	err    error
	styles []ckStyleOf
}

type ckStyleOf struct {
	style uv.Style
	link  uv.Link
}

func (r *ckReader) fail(what string) {
	if r.err == nil {
		r.err = fmt.Errorf("vt: bad checkpoint: %s", what)
	}
	r.b = nil
}

func (r *ckReader) magic() {
	if len(r.b) < len(checkpointMagic) || string(r.b[:len(checkpointMagic)]) != checkpointMagic {
		r.fail("not a checkpoint")
		return
	}
	r.b = r.b[len(checkpointMagic):]
}

func (r *ckReader) byte() byte {
	if len(r.b) == 0 {
		r.fail("short")
		return 0
	}
	b := r.b[0]
	r.b = r.b[1:]
	return b
}

func (r *ckReader) uvarint() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.fail("bad number")
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *ckReader) varint() int64 {
	v, n := binary.Varint(r.b)
	if n <= 0 {
		r.fail("bad number")
		return 0
	}
	r.b = r.b[n:]
	return v
}

// count is a length of at most limit.
func (r *ckReader) count(limit int) int {
	v := r.uvarint()
	if v > uint64(limit) {
		r.fail("too long")
		return 0
	}
	return int(v)
}

func (r *ckReader) str() string {
	n := r.count(maxCheckpointStr)
	if n > len(r.b) {
		r.fail("short")
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}

func (r *ckReader) bool() bool {
	switch r.byte() {
	case 0:
		return false
	case 1:
		return true
	}
	r.fail("bad bool")
	return false
}

func (r *ckReader) pos() uv.Position {
	return uv.Pos(int(r.varint()), int(r.varint()))
}

func (r *ckReader) rect() uv.Rectangle {
	return uv.Rectangle{Min: r.pos(), Max: r.pos()}
}

func (r *ckReader) color() color.Color {
	switch r.byte() {
	case ckNoColor:
		return nil
	case ckBasic:
		return ansi.BasicColor(r.byte())
	case ckIndexed:
		return ansi.IndexedColor(r.byte())
	case ckRGBA:
		return color.RGBA{r.byte(), r.byte(), r.byte(), r.byte()}
	case ckRGBA64:
		var v [4]uint16
		for i := range v {
			v[i] = uint16(r.byte())<<8 | uint16(r.byte())
		}
		return color.RGBA64{v[0], v[1], v[2], v[3]}
	}
	r.fail("bad colour")
	return nil
}

// styleOf reads a style from its key in the table.
func (r *ckReader) styleOf(key string) ckStyleOf {
	k := ckReader{b: []byte(key)}
	var s ckStyleOf
	s.style.Fg, s.style.Bg, s.style.UnderlineColor = k.color(), k.color(), k.color()
	s.style.Underline = uv.Underline(k.byte())
	s.style.Attrs = k.byte()
	s.link.URL, s.link.Params = k.str(), k.str()
	if k.err != nil {
		r.fail("bad style")
	}
	return s
}

func (r *ckReader) style() ckStyleOf {
	i := r.uvarint()
	if i == 0 {
		return ckStyleOf{}
	}
	if i > uint64(len(r.styles)) {
		r.fail("bad style index")
		return ckStyleOf{}
	}
	return r.styles[i-1]
}

func (r *ckReader) line() uv.Line {
	n := r.count(maxCheckpointSide)
	if n > len(r.b) {
		r.fail("short line")
		return nil
	}
	l := make(uv.Line, 0, n)
	var cur ckStyleOf
	for len(l) < n && r.err == nil {
		switch b := r.byte(); {
		case b == ckStyle:
			cur = r.style()
		case b == ckCell:
			l = append(l, uv.Cell{Content: r.str(), Width: int(r.uvarint()), Style: cur.style, Link: cur.link})
		case b >= 0x20 && b < 0x7f:
			l = append(l, uv.Cell{Content: string(rune(b)), Width: 1, Style: cur.style, Link: cur.link})
		default:
			r.fail("bad cell")
		}
	}
	return l
}

func (r *ckReader) cursor() Cursor {
	s := r.style()
	c := Cursor{Pen: s.style, Link: s.link, Position: r.pos()}
	c.Style = CursorStyle(r.varint())
	c.Steady, c.Hidden = r.bool(), r.bool()
	return c
}

func (r *ckReader) screen(w, h int) Screen {
	s := Screen{buf: uv.NewRenderBuffer(w, h)}
	for y := range h {
		l := r.line()
		if r.err == nil && len(l) != w {
			r.fail("line of the wrong width")
		}
		copy(s.buf.Lines[y], l)
		s.buf.TouchLine(0, y, w)
	}
	s.cur, s.saved = r.cursor(), r.cursor()
	s.scroll = r.rect()
	for _, c := range []Cursor{s.cur, s.saved} {
		// The cursor may be one past the last column, for a pending wrap.
		if c.X < 0 || c.X > w || c.Y < 0 || c.Y >= max(h, 1) {
			r.fail("cursor off the screen")
		}
	}
	if sc := s.scroll; sc.Min.X < 0 || sc.Min.Y < 0 || sc.Max.X > w || sc.Max.Y > h || sc.Min.X > sc.Max.X || sc.Min.Y > sc.Max.Y {
		r.fail("scroll region off the screen")
	}
	if r.bool() {
		s.scrollback = &Scrollback{maxLines: r.count(maxCheckpointLines)}
		n := r.count(maxCheckpointLines)
		if n > len(r.b) {
			r.fail("short scrollback")
		}
		s.scrollback.lines = make([]uv.Line, 0, min(n, 1000))
		for range n {
			if r.err != nil {
				break
			}
			s.scrollback.lines = append(s.scrollback.lines, r.line())
		}
	}
	return s
}

func (r *ckReader) charset() CharSet {
	isSet := r.bool()
	n := r.count(256)
	var cs CharSet
	if isSet {
		cs = make(CharSet, n)
	}
	for range n {
		k, v := r.byte(), r.str()
		if cs != nil {
			cs[k] = v
		}
	}
	return cs
}
