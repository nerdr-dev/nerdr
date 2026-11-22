package vt

import (
	"image/color"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
)

// SafeEmulator is a wrapper around an Emulator that adds concurrency safety.
// Read and Close take no lock: Read waits for an answer that only a Write,
// which holds the lock, can give, and Close is what ends a Write waiting
// for its answer to be read. The cells it gives are copies, not the
// emulator's own, which change under the next call.
type SafeEmulator struct {
	*Emulator
	mu sync.RWMutex
}

var _ Terminal = (*SafeEmulator)(nil)

// NewSafeEmulator creates a new SafeEmulator instance.
func NewSafeEmulator(w, h int) *SafeEmulator {
	return &SafeEmulator{
		Emulator: NewEmulator(w, h),
	}
}

// Write writes data to the emulator in a concurrency-safe manner.
func (se *SafeEmulator) Write(data []byte) (int, error) {
	se.mu.Lock()
	defer se.mu.Unlock()
	return se.Emulator.Write(data)
}

// WriteString writes a string to the emulator in a concurrency-safe manner.
func (se *SafeEmulator) WriteString(s string) (int, error) {
	return se.Write([]byte(s))
}

// Read reads data from the emulator in a concurrency-safe manner. It takes
// no lock; see SafeEmulator.
func (se *SafeEmulator) Read(p []byte) (int, error) {
	return se.Emulator.Read(p)
}

// Close closes the emulator in a concurrency-safe manner. It takes no
// lock, so it can end a Write that waits for its answers to be read.
func (se *SafeEmulator) Close() error {
	return se.Emulator.Close()
}

// String returns the screen as text in a concurrency-safe manner.
func (se *SafeEmulator) String() string {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.String()
}

// Bounds returns the screen's bounds in a concurrency-safe manner.
func (se *SafeEmulator) Bounds() uv.Rectangle {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.Bounds()
}

// WidthMethod returns how widths are measured in a concurrency-safe manner.
func (se *SafeEmulator) WidthMethod() uv.WidthMethod {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.WidthMethod()
}

// AtGround reports whether the parser is between sequences in a
// concurrency-safe manner.
func (se *SafeEmulator) AtGround() bool {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.AtGround()
}

// Checkpoint returns the emulator's state in a concurrency-safe manner.
func (se *SafeEmulator) Checkpoint() ([]byte, error) {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.Checkpoint()
}

// Restore puts a checkpoint back in a concurrency-safe manner.
func (se *SafeEmulator) Restore(b []byte) error {
	se.mu.Lock()
	defer se.mu.Unlock()
	return se.Emulator.Restore(b)
}

// MuteReplies turns the answers to queries off or on in a concurrency-safe
// manner.
func (se *SafeEmulator) MuteReplies(on bool) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.MuteReplies(on)
}

// SetLogger sets the logger in a concurrency-safe manner.
func (se *SafeEmulator) SetLogger(l Logger) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetLogger(l)
}

// SetCallbacks sets the callbacks in a concurrency-safe manner.
func (se *SafeEmulator) SetCallbacks(cb Callbacks) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetCallbacks(cb)
}

// RegisterApcHandler registers an APC handler in a concurrency-safe manner.
func (se *SafeEmulator) RegisterApcHandler(handler ApcHandler) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.RegisterApcHandler(handler)
}

// RegisterCsiHandler registers a CSI handler in a concurrency-safe manner.
func (se *SafeEmulator) RegisterCsiHandler(cmd int, handler CsiHandler) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.RegisterCsiHandler(cmd, handler)
}

// RegisterDcsHandler registers a DCS handler in a concurrency-safe manner.
func (se *SafeEmulator) RegisterDcsHandler(cmd int, handler DcsHandler) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.RegisterDcsHandler(cmd, handler)
}

// RegisterEscHandler registers an ESC handler in a concurrency-safe manner.
func (se *SafeEmulator) RegisterEscHandler(cmd int, handler EscHandler) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.RegisterEscHandler(cmd, handler)
}

// RegisterOscHandler registers an OSC handler in a concurrency-safe manner.
func (se *SafeEmulator) RegisterOscHandler(cmd int, handler OscHandler) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.RegisterOscHandler(cmd, handler)
}

// RegisterPmHandler registers a PM handler in a concurrency-safe manner.
func (se *SafeEmulator) RegisterPmHandler(handler PmHandler) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.RegisterPmHandler(handler)
}

// RegisterSosHandler registers a SOS handler in a concurrency-safe manner.
func (se *SafeEmulator) RegisterSosHandler(handler SosHandler) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.RegisterSosHandler(handler)
}

// Focus tells the program it has focus in a concurrency-safe manner.
func (se *SafeEmulator) Focus() {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.Focus()
}

// Blur tells the program it lost focus in a concurrency-safe manner.
func (se *SafeEmulator) Blur() {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.Blur()
}

// Resize resizes the emulator in a concurrency-safe manner.
func (se *SafeEmulator) Resize(w, h int) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.Resize(w, h)
}

// Render renders the emulator's current state in a concurrency-safe manner.
func (se *SafeEmulator) Render() string {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.Render()
}

// SetCell sets a cell in the emulator in a concurrency-safe manner.
func (se *SafeEmulator) SetCell(x, y int, cell *uv.Cell) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetCell(x, y, cell)
}

// CellAt retrieves a copy of a cell from the emulator in a
// concurrency-safe manner.
func (se *SafeEmulator) CellAt(x, y int) *uv.Cell {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return cellCopy(se.Emulator.CellAt(x, y))
}

// cellCopy is a copy of c, which is nil when c is: what a caller is given
// must not change under it once the lock is let go.
func cellCopy(c *uv.Cell) *uv.Cell {
	if c == nil {
		return nil
	}
	own := *c
	return &own
}

// SendKeys sends key events to the emulator in a concurrency-safe manner.
func (se *SafeEmulator) SendKeys(keys ...uv.KeyEvent) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SendKeys(keys...)
}

// SendKey sends a key event to the emulator in a concurrency-safe manner.
func (se *SafeEmulator) SendKey(key uv.KeyEvent) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SendKey(key)
}

// SendMouse sends a mouse event to the emulator in a concurrency-safe manner.
func (se *SafeEmulator) SendMouse(mouse uv.MouseEvent) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SendMouse(mouse)
}

// SendText sends text input to the emulator in a concurrency-safe manner.
func (se *SafeEmulator) SendText(text string) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SendText(text)
}

// Paste pastes text into the emulator in a concurrency-safe manner.
func (se *SafeEmulator) Paste(text string) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.Paste(text)
}

// SetForegroundColor sets the foreground color in a concurrency-safe manner.
func (se *SafeEmulator) SetForegroundColor(color color.Color) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetForegroundColor(color)
}

// SetBackgroundColor sets the background color in a concurrency-safe manner.
func (se *SafeEmulator) SetBackgroundColor(color color.Color) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetBackgroundColor(color)
}

// SetCursorColor sets the cursor color in a concurrency-safe manner.
func (se *SafeEmulator) SetCursorColor(color color.Color) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetCursorColor(color)
}

// SetDefaultForegroundColor sets the default foreground color in a concurrency-safe manner.
func (se *SafeEmulator) SetDefaultForegroundColor(c color.Color) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetDefaultForegroundColor(c)
}

// SetDefaultBackgroundColor sets the default background color in a concurrency-safe manner.
func (se *SafeEmulator) SetDefaultBackgroundColor(c color.Color) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetDefaultBackgroundColor(c)
}

// SetDefaultCursorColor sets the default cursor color in a concurrency-safe manner.
func (se *SafeEmulator) SetDefaultCursorColor(c color.Color) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetDefaultCursorColor(c)
}

// SetIndexedColor sets an indexed color in a concurrency-safe manner.
func (se *SafeEmulator) SetIndexedColor(index int, color color.Color) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetIndexedColor(index, color)
}

// IndexedColor retrieves an indexed color in a concurrency-safe manner.
func (se *SafeEmulator) IndexedColor(index int) color.Color {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.IndexedColor(index)
}

// Touched returns the touched lines in a concurrency-safe manner.
func (se *SafeEmulator) Touched() []*uv.LineData {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.Touched()
}

// Height returns the height of the emulator in a concurrency-safe manner.
func (se *SafeEmulator) Height() int {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.Height()
}

// Width returns the width of the emulator in a concurrency-safe manner.
func (se *SafeEmulator) Width() int {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.Width()
}

// ForegroundColor returns the foreground color in a concurrency-safe manner.
func (se *SafeEmulator) ForegroundColor() color.Color {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.ForegroundColor()
}

// BackgroundColor returns the background color in a concurrency-safe manner.
func (se *SafeEmulator) BackgroundColor() color.Color {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.BackgroundColor()
}

// CursorColor returns the cursor color in a concurrency-safe manner.
func (se *SafeEmulator) CursorColor() color.Color {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.CursorColor()
}

// CursorPosition returns the cursor position in a concurrency-safe manner.
func (se *SafeEmulator) CursorPosition() uv.Position {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.CursorPosition()
}

// Draw draws the emulator's content onto a given surface in a concurrency-safe manner.
func (se *SafeEmulator) Draw(s uv.Screen, a uv.Rectangle) {
	se.mu.RLock()
	defer se.mu.RUnlock()
	se.Emulator.Draw(s, a)
}

// Scrollback returns the scrollback buffer in a concurrency-safe manner.
func (se *SafeEmulator) Scrollback() *Scrollback {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.Scrollback()
}

// ScrollbackLen returns the number of lines in the scrollback buffer in a concurrency-safe manner.
func (se *SafeEmulator) ScrollbackLen() int {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.ScrollbackLen()
}

// ScrollbackCellAt returns a copy of a cell from the scrollback buffer in a concurrency-safe manner.
// It takes the lock for writing: the scrollback unpacks the line for it.
func (se *SafeEmulator) ScrollbackCellAt(x, y int) *uv.Cell {
	se.mu.Lock()
	defer se.mu.Unlock()
	return cellCopy(se.Emulator.ScrollbackCellAt(x, y))
}

// SetScrollbackSize sets the scrollback buffer size in a concurrency-safe manner.
func (se *SafeEmulator) SetScrollbackSize(maxLines int) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.SetScrollbackSize(maxLines)
}

// ClearScrollback clears the scrollback buffer in a concurrency-safe manner.
func (se *SafeEmulator) ClearScrollback() {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.Emulator.ClearScrollback()
}

// IsAltScreen returns whether in alternate screen mode in a concurrency-safe manner.
func (se *SafeEmulator) IsAltScreen() bool {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.Emulator.IsAltScreen()
}
