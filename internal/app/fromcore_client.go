package app

import (
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/plugin"
)

// What core asks of the TUI; see core.Client.

func (m *Model) AgentVisible(a *core.Tab) bool { return m.agentVisible(a) }

func (m *Model) Detect() core.Cmd { return toCore(m.detect()) }

func (m *Model) DropPane(t *core.Tab, p *core.PaneTab) { m.Gen++; m.dropPane(t, p) }

func (m *Model) DropReviewPane(t *core.Tab) { m.Gen++; m.dropReviewPane(t) }

func (m *Model) FocusPane(t *core.Tab, p *core.PaneTab) core.Cmd {
	m.Gen++
	return toCore(m.focusPane(t, p))
}

func (m *Model) FocusTerms(t *core.Tab) { m.Gen++; m.focusTerms(t) }

func (m *Model) NotifyChange(a *core.Tab, visible bool, now time.Time) {
	m.notifyChange(a, visible, now)
}

func (m *Model) NotifyReview(a *core.Tab, text string) { m.Gen++; m.notifyReview(a, text) }

func (m *Model) PinReview() core.Cmd {
	m.Gen++
	return toCore(m.pinReview())
}

func (m *Model) KeyName(command string) string { return m.pk(command) }

func (m *Model) Resize() core.Cmd {
	m.Gen++
	return toCore(m.resize())
}

func (m *Model) ResizePanes() { m.Gen++; m.resizePanes() }

func (m *Model) Layout() core.Layout { return m.layout() }

func (m *Model) ScreenKey(screen int) string { return m.screenKey(screen) }

func (m *Model) SetFocus(area core.FocusArea) { m.Gen++; m.setFocus(area) }

func (m *Model) ShareOverlaps() { m.Gen++; m.shareOverlaps() }

func (m *Model) ShellSize(t *core.Tab, p *core.PaneTab) (int, int) { return m.shellSize(t, p) }

func (m *Model) StopScrolling() { m.Gen++; m.stopScrolling() }

func (m *Model) TabSize(t *core.Tab) (int, int) { return m.tabSize(t) }

func (m *Model) DialogClosed() bool { return m.dialogClosed() }

func (m *Model) OpenSpinoff(t *core.Tab) core.Cmd {
	m.Gen++
	return toCore(m.openSpinoff(t))
}

func (m *Model) OpenSwitchAgent() core.Cmd {
	m.Gen++
	return toCore(m.openSwitchAgent())
}

func (m *Model) AskPluginRun(a *core.Tab, p plugin.Plugin, vals map[string]any, dir string, confined bool, decide func(answer string) core.Cmd) {
	m.Gen++
	m.askPluginRun(a, p, vals, dir, confined, decide)
}

func (m *Model) EnsurePluginUI(t *core.Tab, p plugin.Plugin, key string) {
	m.Gen++
	m.ensurePluginUI(t, p, key)
}

func (m *Model) PluginEvent(t *core.Tab, dir string, ev map[string]any) {
	m.Gen++
	m.pluginEvent(t, dir, ev)
}

func (m *Model) ShowPluginRun(t *core.Tab, p plugin.Plugin, run *core.PluginRun) core.Cmd {
	m.Gen++
	return toCore(m.showPluginRun(t, p, run))
}
