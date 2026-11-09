package vt

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// TestScrollbackPacked: a line comes out of the scrollback as it went in,
// whatever its cells, and costs far less kept than as cells.
func TestScrollbackPacked(t *testing.T) {
	red := uv.Style{Fg: ansi.Red, Attrs: uv.AttrBold}
	lines := []uv.Line{
		{},
		{{Content: "a", Width: 1}, {Content: "b", Width: 1, Style: red}, {Content: "c", Width: 1, Style: red}},
		{{Content: "世", Width: 2}, {Content: "", Width: 0}, {Content: "e\u0301", Width: 1, Link: uv.Link{URL: "https://x", Params: "id=1"}}},
		{{Content: strings.Repeat("z", 300), Width: 1}}, // too long to pack: kept as it was
		{{}, {Content: " ", Width: 1, Style: uv.Style{Bg: ansi.IndexedColor(33)}}},
	}
	sb := NewScrollback(3)
	for _, l := range lines {
		sb.add(l)
	}
	if sb.Len() != 3 {
		t.Fatalf("Len %d", sb.Len())
	}
	for i, want := range lines[2:] {
		got := sb.Line(i)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("line %d:\n got %v\nwant %v", i, got, want)
		}
		for x := range want {
			if c := sb.CellAt(x, i); c == nil || *c != want[x] {
				t.Errorf("cell %d,%d: %v, want %v", x, i, c, want[x])
			}
		}
		if sb.CellAt(len(want), i) != nil {
			t.Errorf("cell past line %d", i)
		}
	}
	// A line read by CellAt is not mistaken for another once lines come
	// and go.
	_ = sb.CellAt(0, 0)
	sb.add(uv.Line{{Content: "n", Width: 1}})
	if c := sb.CellAt(0, 0); c == nil || c.Content != strings.Repeat("z", 300) {
		t.Errorf("after a push: %v", c)
	}
	if c := sb.CellAt(0, 2); c == nil || c.Content != "n" {
		t.Errorf("newest: %v", c)
	}
	sb.SetMaxLines(1)
	if sb.Len() != 1 || sb.CellAt(0, 0).Content != "n" {
		t.Errorf("after SetMaxLines: %d lines", sb.Len())
	}
	sb.Clear()
	if sb.Len() != 0 || sb.CellAt(0, 0) != nil {
		t.Error("after Clear")
	}
}

func TestScrollbackPackedMemory(t *testing.T) {
	line := "\x1b[38;5;33m" + strings.Repeat("x", 60) + "\x1b[1m" + strings.Repeat("y", 60) + "\x1b[0m\r\n"
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	e := NewEmulator(200, 50)
	e.SetScrollbackSize(5000)
	_, _ = e.Write([]byte(strings.Repeat(line, 6000)))
	runtime.GC()
	runtime.ReadMemStats(&m1)
	mb := float64(m1.HeapAlloc-m0.HeapAlloc) / (1 << 20)
	t.Logf("5000 lines of 120 columns: %.1f MB", mb)
	if mb > 10 {
		t.Errorf("%.1f MB for the scrollback", mb)
	}
	runtime.KeepAlive(e)
}
