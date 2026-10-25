package app

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"nerdr/internal/clean"
	"nerdr/internal/config"
	"nerdr/internal/hooks"
	"nerdr/internal/plugin"
	"nerdr/internal/state"
	"nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The review panel's F5 screen: the user's plugins (see internal/plugin),
// each with what it does and the parameters agents give it, and below
// them the screen of one that has its own (its ui), running for the
// active tab in a terminal of the panel's.
//
// A plugin's screen runs as long as its tab, whichever screen is shown,
// and is drawn only on F5. While it has the keyboard every key goes to it
// but F1-F5 and the prefix: F5 goes back to the list, and from the list to
// the screen again.

// pluginsCheckEvery is how long a look at the plugins' folders is
// trusted: the screen is drawn on every frame.
const pluginsCheckEvery = 2 * time.Second

// pluginsView is the F5 screen's state.
type pluginsView struct {
	cursor int
	// picked is the folder of the plugin picked: the list is read again
	// every few seconds, and a plugin added or taken away before it must
	// not move the pick, nor the keyboard, to another.
	picked string
	// uiFocus is the pluginUIKey of the screen with the keyboard, "" for
	// none: a screen of another tab or plugin is not taken from the user.
	uiFocus string

	key  string // the tab looked for
	at   time.Time
	list []plugin.Plugin
	// allowed is whether the user allowed each of list as it was read,
	// by folder.
	allowed map[string]bool

	// uis are the plugins' screens started, by pluginUIKey.
	uis map[string]*pluginUI
	// runs are the last run of each plugin agents ran, by pluginUIKey,
	// and byID those running, by their id: see pluginruns.go.
	runs, byID map[string]*pluginRun
	// finished holds ended foreground runs until their clients have had
	// time to read them, even when a newer run occupies runs.
	finished map[string]*pluginRun
	// asking is the question whether an agent may run a plugin, while it
	// is asked.
	asking *runQuestion
	// uiW and uiH are the size the last drawing left a screen.
	uiW, uiH int
	// goneAt is when the plugins' folders were last looked for, and
	// missing when each absent folder was first seen: a plugin.toml
	// written by rename is missing for a moment.
	goneAt  time.Time
	missing map[string]time.Time
}

// pluginUI is a plugin's screen, started in tab t.
type pluginUI struct {
	t    *tab
	pane *term.Pane
	// events is where it is told of agents' runs of the plugin.
	events *os.File
	// dir is the plugin's folder, and key the content it was started from.
	dir, key string
}

// close stops u's program, and takes its events file away.
func (u *pluginUI) close() {
	if u.events != nil {
		_ = u.events.Close()
		_ = os.Remove(u.events.Name())
	}
	go u.pane.Close()
}

// pluginUIKey is the key of plugin p's screen in tab t.
func pluginUIKey(t *tab, p plugin.Plugin) string { return t.ID + "\x00" + p.Dir }

// pluginsAllowedFile is where the plugins the user allowed are kept.
func pluginsAllowedFile() string { return plugin.AllowedFile(state.Dir()) }

// panelPlugins reports whether the review panel shows the plugins.
func (m *Model) panelPlugins() bool { return m.reviewOpen && m.review.Plugins() }

// pluginList is the plugins for t, looked for again when the last look is
// old or was for another tab.
func (m *Model) pluginList(t *tab) []plugin.Plugin {
	v := &m.plugins
	key := ""
	if t != nil {
		key = t.ID
	}
	if key == v.key && time.Since(v.at) < pluginsCheckEvery {
		return v.list
	}
	v.key, v.at = key, time.Now()
	v.list = plugin.Find(plugin.UserDir(config.Path()))
	v.allowed = map[string]bool{}
	for _, p := range v.list {
		v.allowed[p.Dir] = plugin.Allowed(pluginsAllowedFile(), p)
	}
	v.repick()
	return v.list
}

// pluginGone stops the screens and runs of a plugin whose folder is no
// longer there: the user cannot pick them, nor stop them, in F5.
func (m *Model) pluginGone(dir string) {
	delete(m.plugins.missing, dir)
	for k, u := range m.plugins.uis {
		if u.dir != dir {
			continue
		}
		delete(m.plugins.uis, k)
		if m.plugins.uiFocus == k {
			m.plugins.uiFocus = ""
		}
		u.close()
	}
	for _, r := range m.plugins.byID {
		if r.dir != dir {
			continue
		}
		if r.running() && r.stopBy == "" {
			r.stopBy = "the plugin is gone"
			r.proc.stop()
		}
	}
	for k, r := range m.plugins.runs {
		if r.dir == dir {
			delete(m.plugins.runs, k)
		}
	}
}

// pluginGoneAfter is how long a plugin's folder may be there without its
// plugin.toml before its screens and runs are taken to be orphaned: an
// editor writes plugin.toml by rename.
const pluginGoneAfter = 5 * time.Second

// checkPluginsGone stops screens and runs whose plugin folder is gone, or
// has had no plugin.toml for a while. It looks every few seconds, so a
// watch does not outlive its plugin just because F5 is not open.
func (m *Model) checkPluginsGone(now time.Time) {
	if now.Sub(m.plugins.goneAt) < pluginsCheckEvery {
		return
	}
	m.plugins.goneAt = now
	if m.plugins.missing == nil {
		m.plugins.missing = map[string]time.Time{}
	}
	dirs := map[string]bool{}
	for _, u := range m.plugins.uis {
		dirs[u.dir] = true
	}
	for _, r := range m.plugins.byID {
		dirs[r.dir] = true
	}
	for _, r := range m.plugins.runs {
		dirs[r.dir] = true
	}
	if len(dirs) == 0 {
		for dir := range m.plugins.missing {
			delete(m.plugins.missing, dir)
		}
		return
	}
	live := map[string]bool{}
	for _, p := range plugin.Find(plugin.UserDir(config.Path())) {
		live[p.Dir] = true
	}
	for dir := range dirs {
		if live[dir] {
			delete(m.plugins.missing, dir)
			continue
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			m.pluginGone(dir)
			continue
		}
		since, ok := m.plugins.missing[dir]
		if !ok {
			m.plugins.missing[dir] = now
			continue
		}
		if now.Sub(since) >= pluginGoneAfter {
			m.pluginGone(dir)
		}
	}
	for dir := range m.plugins.missing {
		if !dirs[dir] {
			delete(m.plugins.missing, dir)
		}
	}
}

// repick puts the cursor on the plugin picked, in the list read anew. One
// gone leaves the cursor where it was, on another, and the keyboard with
// the list.
func (v *pluginsView) repick() {
	for i, p := range v.list {
		if p.Dir == v.picked {
			v.cursor = i
			return
		}
	}
	v.uiFocus = ""
	v.pick(v.cursor)
}

// pick picks the i-th plugin of the list.
func (v *pluginsView) pick(i int) {
	v.cursor = min(max(i, 0), max(len(v.list)-1, 0))
	v.picked = ""
	if v.cursor < len(v.list) {
		v.picked = v.list[v.cursor].Dir
	}
}

// pickedPlugin is the plugin picked in F5 for t, if there is one.
func (m *Model) pickedPlugin(t *tab) (plugin.Plugin, bool) {
	list := m.pluginList(t)
	if len(list) == 0 {
		return plugin.Plugin{}, false
	}
	return list[min(max(m.plugins.cursor, 0), len(list)-1)], true
}

// pluginUIOf is p's screen in t, if one was started.
func (m *Model) pluginUIOf(t *tab, p plugin.Plugin) *pluginUI {
	if t == nil {
		return nil
	}
	return m.plugins.uis[pluginUIKey(t, p)]
}

// running reports whether u's program still runs.
func (u *pluginUI) running() bool {
	if u == nil || u.pane == nil {
		return false
	}
	exited, _, _ := u.pane.Exited()
	return !exited
}

// pluginUIPane is the plugin screen with the keyboard, if one has it: the
// input router passes keys straight to it.
func (m *Model) pluginUIPane() *term.Pane {
	if m.focus != focusReview || !m.panelPlugins() || m.plugins.uiFocus == "" {
		return nil
	}
	t := m.cur()
	p, ok := m.pickedPlugin(t)
	if !ok || pluginUIKey(t, p) != m.plugins.uiFocus {
		return nil
	}
	if u := m.pluginUIOf(t, p); u.running() {
		return u.pane
	}
	return nil
}

// pluginsKey handles a key while the plugins have the keyboard, and their
// screen does not.
func (m *Model) pluginsKey(msg tea.KeyMsg) tea.Cmd {
	if p := m.pluginUIPane(); p != nil {
		// Keys reach here while the router is switching over.
		if b := keyBytes(msg); len(b) > 0 {
			p.Input(b)
		}
		return nil
	}
	t := m.cur()
	list := m.pluginList(t)
	v := &m.plugins
	switch msg.String() {
	case "esc", "q":
		v.uiFocus = ""
		m.setFocus(focusAgent)
		return m.resize()
	case "up", "k":
		v.pick(v.cursor - 1)
		v.uiFocus = ""
	case "down", "j":
		v.pick(min(v.cursor+1, len(list)-1))
		v.uiFocus = ""
	case "enter":
		if p, ok := m.pickedPlugin(t); ok {
			return m.openPluginUI(t, p)
		}
	case "x":
		if p, ok := m.pickedPlugin(t); ok {
			m.stopPluginUI(t, p)
		}
	case "s":
		if p, ok := m.pickedPlugin(t); ok {
			m.stopPickedRun(t, p)
		}
	case "a":
		if p, ok := m.pickedPlugin(t); ok {
			return m.allowPlugin(p)
		}
	case "d":
		if p, ok := m.pickedPlugin(t); ok {
			return m.disallowPlugin(p)
		}
	}
	return nil
}

// togglePluginUI is F5 pressed on F5: from the list to the picked
// plugin's screen when it runs, and back. It reports whether it did.
func (m *Model) togglePluginUI() bool {
	if m.focus != focusReview || !m.panelPlugins() {
		return false
	}
	t := m.cur()
	p, ok := m.pickedPlugin(t)
	if !ok || !m.pluginUIOf(t, p).running() {
		return false
	}
	key := pluginUIKey(t, p)
	if m.plugins.uiFocus == key {
		m.plugins.uiFocus = ""
	} else {
		m.plugins.uiFocus = key
	}
	return true
}

// openPluginUI gives plugin p's screen in t the keyboard, starting it when
// it does not run, once the user has allowed the plugin.
func (m *Model) openPluginUI(t *tab, p plugin.Plugin) tea.Cmd {
	switch {
	case t == nil:
		m.toast("open a tab first: a plugin's screen runs in a tab's folder")
		return nil
	case p.Err != "":
		m.toast(clean.Line(p.Name) + " is broken: " + clean.Line(p.Err))
		return nil
	case len(p.UI) == 0:
		m.toast(clean.Line(p.Name) + " has no screen of its own: agents run it")
		return nil
	}
	// What is started, and what the user is asked about, is the plugin as
	// it is now, not as the list read it.
	p, key, err := plugin.Fresh(p)
	switch {
	case err != nil:
		m.toast(clean.Line(p.Name) + ": " + clean.Line(err.Error()))
		return nil
	case p.Err != "" || len(p.UI) == 0:
		m.toast(clean.Line(p.Name) + " changed and has no screen to start now")
		return nil
	}
	if u := m.pluginUIOf(t, p); u.running() && u.key == key {
		m.plugins.uiFocus = pluginUIKey(t, p)
		m.setFocus(focusReview)
		return nil
	}
	if plugin.AllowedKey(pluginsAllowedFile(), key) {
		return m.startPluginUI(t, p, true, key)
	}
	return m.askAllowPlugin(p, key, func(m *Model) tea.Cmd { return m.openPluginUI(t, p) },
		func(m *Model) tea.Cmd { return m.startPluginUI(t, p, true, key) })
}

// allowPlugin asks the user to allow p as it is now, for agents to run
// and its screen to start (a in F5).
func (m *Model) allowPlugin(p plugin.Plugin) tea.Cmd {
	p, key, err := plugin.Fresh(p)
	switch {
	case err != nil:
		m.toast(clean.Line(p.Name) + ": " + clean.Line(err.Error()))
		return nil
	case p.Err != "":
		m.toast(clean.Line(p.Name) + " is broken: " + clean.Line(p.Err))
		return nil
	case plugin.AllowedKey(pluginsAllowedFile(), key):
		m.toast(clean.Line(p.Name) + " is allowed as it is")
		return nil
	}
	return m.askAllowPlugin(p, key, func(m *Model) tea.Cmd { return m.allowPlugin(p) },
		func(m *Model) tea.Cmd {
			m.toast(clean.Line(p.Name) + " is allowed until something in its folder changes")
			return nil
		})
}

// disallowPlugin takes the user's allowance of p as it is now away.
func (m *Model) disallowPlugin(p plugin.Plugin) tea.Cmd {
	file := pluginsAllowedFile()
	key := ""
	if _, k, err := plugin.Fresh(p); err == nil {
		key = k
	}
	if !plugin.AllowedDir(file, p.Dir) && (key == "" || !plugin.AllowedKey(file, key)) {
		m.toast(clean.Line(p.Name) + " is not allowed")
		return nil
	}
	m.dialog = &confirm{
		title: "Stop allowing " + clean.Line(p.Name) + "?",
		lines: []string{
			"Agents will not run it, nor will its screen start, until you allow it again.",
			"This is every version of it in this folder. A screen or a watch already running goes on; stop it in F5.",
			"", dim("y stops allowing it · n keeps it allowed"),
		},
		yes: func(m *Model) tea.Cmd {
			if _, err := plugin.DisallowDir(file, p.Dir); err != nil {
				m.toast("could not take the allowance away: " + clean.Line(err.Error()))
				return nil
			}
			if key != "" {
				if err := plugin.Disallow(file, key); err != nil {
					m.toast("could not take the allowance away: " + clean.Line(err.Error()))
					return nil
				}
			}
			m.plugins.at = time.Time{}
			m.toast(clean.Line(p.Name) + " is no longer allowed")
			return nil
		},
	}
	return nil
}

// askAllowPlugin shows the user what is in p's folder, key a Key of it,
// and keeps that they allow it, before then. A change to the folder while
// they read it is not allowed by their yes: again asks anew.
func (m *Model) askAllowPlugin(p plugin.Plugin, key string, again, then func(m *Model) tea.Cmd) tea.Cmd {
	file := pluginsAllowedFile()
	lines := []string{
		"A plugin runs as you, outside every agent's sandbox: its screen when",
		"you start it, its run whenever an agent asks, with the parameters the",
		"agent gives. An agent may have written it: allow it only if you know",
		"what it does, whatever it is given.", "",
	}
	bold := lipgloss.NewStyle().Bold(true)
	if len(p.UI) > 0 {
		lines = append(lines, "screen:     "+bold.Render(ansi.Truncate(clean.Line(strings.Join(p.Command(p.UI), " ")), 8<<10, "…")))
	}
	if len(p.Run) > 0 {
		lines = append(lines, "agents run: "+bold.Render(ansi.Truncate(clean.Line(strings.Join(p.Command(p.Run), " ")), 8<<10, "…")))
		var free []string
		for _, q := range p.Params {
			if q.Type == plugin.String || q.Type == plugin.Multi && len(q.Options) == 0 {
				free = append(free, "--"+q.Name)
			}
		}
		if len(free) > 0 {
			lines = append(lines, warn("any text an agent likes: "+strings.Join(free, ", ")+
				". Check the plugin uses it with care: quoted, never run as code."))
		}
	}
	lines = append(lines, dim("in "+clean.Line(tildePath(p.Dir, homeDir()))), "")
	m.dialog = &confirm{
		title:  "Allow " + clean.Line(p.Name) + "?",
		lines:  lines,
		scroll: pluginPreview(p.Dir, maxPreviewLines, max(boxInner(m.width)-2, 20)),
		after:  []string{"", dim("y allows it until something in its folder changes · n does not")},
		yes: func(m *Model) tea.Cmd {
			if now, err := p.Key(); err != nil || now != key {
				m.toast(clean.Line(p.Name) + " changed while you read it: look again")
				return again(m)
			}
			if err := plugin.Allow(file, p, key); err != nil {
				m.toast("could not keep that it is allowed: " + clean.Line(err.Error()))
				return nil
			}
			m.plugins.at = time.Time{} // its mark in the list
			return then(m)
		},
	}
	return nil
}

// maxPreviewLines is the most of a plugin's folder the user is shown to
// scroll through; one with more says to read the rest in the folder.
const maxPreviewLines = 5000

// maxPreviewFile is the most of one file the question reads to show: a
// large one is read in its folder instead.
const maxPreviewFile = 1 << 20

// pluginPreview is what is in the plugin folder dir, for the user to read
// before allowing it: each file's name and, for text, its lines, at most
// n lines in all, each at most w columns.
func pluginPreview(dir string, n, w int) []string {
	var out []string
	more := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = clean.Line(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			to, _ := os.Readlink(path)
			out = append(out, lipgloss.NewStyle().Bold(true).Render(rel)+dim(" → "+clean.Line(to)))
			return nil
		}
		head := lipgloss.NewStyle().Bold(true).Render(rel)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !info.Mode().IsRegular() {
			out = append(out, head+dim("  not a regular file"))
			return nil
		}
		if info.Size() > maxPreviewFile {
			out = append(out, head+dim(fmt.Sprintf("  %d bytes, too large to show: read it in %s",
				info.Size(), clean.Line(tildePath(dir, homeDir())))))
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			out = append(out, head+dim("  could not be read"))
			return nil
		}
		if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
			out = append(out, head+dim(fmt.Sprintf("  %d bytes, not text", len(b))))
			return nil
		}
		text := strings.Split(strings.TrimRight(clean.Text(string(b)), "\n"), "\n")
		out = append(out, head)
		for _, l := range text {
			if len(out) >= n {
				more++
				continue
			}
			out = append(out, "  "+ansi.Truncate(strings.ReplaceAll(l, "\t", "    "), w-2, "…"))
		}
		return nil
	})
	if more > 0 {
		out = append(out, warn(fmt.Sprintf("… and %d more lines: read them in %s before you allow it", more, tildePath(dir, homeDir()))))
	}
	return out
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// startPluginUI starts plugin p's screen in t, in place of one that ended,
// and gives it the keyboard when focus is set. It is told of a run of the
// plugin going on.
func (m *Model) startPluginUI(t *tab, p plugin.Plugin, focus bool, key string) tea.Cmd {
	if len(p.UI) == 0 {
		m.toast(clean.Line(p.Name) + " has no screen of its own")
		return nil
	}
	hadFocus := m.plugins.uiFocus == pluginUIKey(t, p)
	m.stopPluginUI(t, p)
	w, h := m.plugins.uiW, m.plugins.uiH
	if w <= 0 || h <= 0 {
		w, h = 60, 20
	}
	events, err := pluginEventsFile(p)
	if err != nil {
		m.toast(clean.Line(p.Name) + ": " + clean.Line(err.Error()))
		return nil
	}
	dropEvents := func() {
		_ = events.Close()
		_ = os.Remove(events.Name())
	}
	env := append(append(childEnv(t), "NERDR_SHELL=1", "NERDR_PLUGIN_EVENTS="+events.Name()), p.Env(nil)...)
	argv := p.Command(p.UI)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	dir, err := runDir(t, t.Dir)
	if err != nil {
		dropEvents()
		m.toast(clean.Line(p.Name) + ": " + clean.Line(err.Error()))
		return nil
	}
	if err := pluginCommandIn(cmd, dir, p.Dir, key); err != nil {
		dir.Close()
		dropEvents()
		m.toast(clean.Line(p.Name) + ": " + clean.Line(err.Error()))
		return nil
	}
	pane, err := term.StartCmd(cmd, w, h, m.notify)
	dir.Close()
	if err != nil {
		dropEvents()
		m.toast(clean.Line(p.Name) + ": " + clean.Line(err.Error()))
		return nil
	}
	if m.plugins.uis == nil {
		m.plugins.uis = map[string]*pluginUI{}
	}
	m.plugins.uis[pluginUIKey(t, p)] = &pluginUI{t: t, pane: pane, events: events, dir: p.Dir, key: key}
	if run := m.pluginRunOf(t, p); run.running() {
		m.pluginEvent(t, p.Dir, run.startEvent())
		if text := strings.Join(run.lines(), "\n"); text != "" {
			m.pluginEvent(t, p.Dir, map[string]any{"event": "output", "run": run.id, "text": text + "\n"})
		}
	}
	if focus || hadFocus {
		uiKey := pluginUIKey(t, p)
		m.plugins.uiFocus = uiKey
		if t == m.cur() && m.focus == focusReview && m.panelPlugins() {
			m.setFocus(focusReview)
		}
	}
	return nil
}

// stopPluginUI stops plugin p's screen in t, if it was started.
func (m *Model) stopPluginUI(t *tab, p plugin.Plugin) {
	key := pluginUIKey(t, p)
	if u := m.pluginUIOf(t, p); u != nil {
		delete(m.plugins.uis, key)
		if m.plugins.uiFocus == key {
			m.plugins.uiFocus = ""
		}
		u.close()
	}
}

// checkPluginUIs stops the screens of the tabs closed, and forgets their
// runs.
func (m *Model) checkPluginUIs() {
	for k, u := range m.plugins.uis {
		if !slices.Contains(m.tabs, u.t) {
			delete(m.plugins.uis, k)
			if m.plugins.uiFocus == k {
				m.plugins.uiFocus = ""
			}
			u.close()
		}
	}
	now := time.Now()
	m.checkPluginsGone(now)
	m.pumpRuns(now)
	if q := m.plugins.asking; q != nil && now.After(q.until) {
		q.answer(m, runUnanswered)
		m.toast(q.agent + "'s run of " + q.name + " was not answered in time, and did not run")
	}
	for k, r := range m.plugins.runs {
		if !slices.Contains(m.tabs, r.t) {
			if r.running() {
				r.proc.stop()
			}
			delete(m.plugins.runs, k)
			// Leave it in byID until endRun, so a client still reads
			// its result and stopRuns waits for its stop at quitting.
		}
	}
}

// pluginUIPanes are the plugins' screens, to stop when nerdr quits; the
// agents' runs are stopped there and then.
func (m *Model) pluginUIPanes() []*term.Pane {
	m.stopRuns()
	var out []*term.Pane
	for _, u := range m.plugins.uis {
		out = append(out, u.pane)
		if u.events != nil {
			_ = os.Remove(u.events.Name())
		}
	}
	return out
}

// onPlugin answers an agent's `nerdr plugin`: whether a plugin is
// allowed (list), running one (run), what a run printed (read), and a
// stop.
func (m *Model) onPlugin(r hooks.Report) tea.Cmd {
	from := m.agentByHook(r.Pane)
	if from == nil {
		answerer(r)(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	switch r.What {
	case hooks.PluginRun:
		return m.onRunPlugin(r, from)
	case hooks.PluginRead:
		m.onReadRun(r, from)
	case hooks.PluginStop:
		m.stopAgentRuns(r, from)
	default:
		answerer(r)(hooks.Answer{Text: onOff(plugin.AllowedKey(pluginsAllowedFile(), r.Text), "allowed", runNotAllowed)})
	}
	return nil
}

// pluginsBody draws the plugins for t, w×h, for the panel's frame.
func (m *Model) pluginsBody(t *tab, w, h int) []string {
	list := m.pluginList(t)
	v := &m.plugins
	textW := max(w-2, 10)
	keys := m.pluginKeys("↑↓", "pick", "enter", "open", "a", "allow", "d", "disallow", "esc", "agent")
	if len(list) == 0 {
		lines := []string{""}
		for _, l := range wrapLines("No plugins. Put your own in "+tildePath(plugin.UserDir(config.Path()), homeDir())+
			"/NAME, each a folder with a "+plugin.File+
			". See https://nerdr-dev.github.io/nerdr/configure/plugins/.", textW, 20) {
			lines = append(lines, " "+l)
		}
		return append(fitLines(lines, w, h-1), ansi.Truncate(keys, w, "…"))
	}
	p := list[v.cursor]
	u := m.pluginUIOf(t, p)
	run := m.pluginRunOf(t, p)
	// What the run printed, when the plugin shows it: below the rest, or
	// above its screen, a few rows of it.
	output := func(top []string, rows int) []string {
		if run == nil || !p.Shows(plugin.ShowOutput) || rows < 2 {
			return top
		}
		return append(append(top, ""), m.runOutput(run, w, rows-1)...)
	}
	focused := m.focus == focusReview
	inUI := focused && v.uiFocus == pluginUIKey(t, p) && u.running()
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)

	rows := min(len(list), max(h/3, 3))
	first := 0
	if v.cursor >= rows {
		first = v.cursor - rows + 1
	}
	first = min(max(first, 0), max(len(list)-rows, 0))
	var head []string
	for i, q := range list {
		if i < first || i >= first+rows {
			continue
		}
		name, title := clean.Line(q.Name), clean.Line(q.Title)
		if title != "" {
			name += dim(" · " + title)
		}
		mark := dim("·")
		switch {
		case q.Err != "":
			mark = warn("!")
		case len(q.UI) > 0 && m.pluginUIOf(t, q).running():
			mark = lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Render("▣")
		case len(q.UI) > 0:
			mark = "▣"
		}
		cursor := "  "
		if i == v.cursor && focused && !inUI {
			cursor, name = sel.Render("❯ "), sel.Render(clean.Line(q.Name))+onOff(title != "", dim(" · "+title), "")
		} else if i == v.cursor {
			name = lipgloss.NewStyle().Bold(true).Render(clean.Line(q.Name)) + onOff(title != "", dim(" · "+title), "")
		}
		if q.Err == "" && !v.allowed[q.Dir] {
			name += warn("  not allowed")
		}
		if run := m.pluginRunOf(t, q); run != nil {
			lt, what := m.runState(run)
			name += "  " + lt + " " + dim(what)
		}
		head = append(head, m.onClick(ansi.Truncate(cursor+mark+" "+name, w, "…"), func(m *Model) tea.Cmd {
			if m.plugins.cursor != i {
				m.plugins.pick(i)
			}
			m.plugins.uiFocus = ""
			m.setFocus(focusReview)
			return nil
		}))
	}

	var info []string
	pname := clean.Line(p.Name)
	info = append(info, dim("── "+pname+" "+strings.Repeat("─", max(textW-lipgloss.Width(pname)-4, 0))))
	info = append(info, dim(" "+ansi.Truncate(clean.Line(tildePath(p.Dir, homeDir())), textW, "…")))
	if p.Err != "" {
		for _, l := range wrapRows("broken: "+clean.Text(p.Err), textW, 6) {
			info = append(info, warn(" "+l))
		}
	} else {
		descRows := 1 << 20
		if len(p.UI) > 0 {
			descRows = 4 // the rest is the screen's
		}
		for _, l := range wrapRows(clean.Text(p.Description), textW, descRows) {
			info = append(info, " "+lipgloss.NewStyle().Italic(true).Render(l))
		}
		if !v.allowed[p.Dir] {
			for _, l := range wrapRows("not allowed as it is: no agent runs it, nor does its screen start, until you allow it. a shows what is in it.", textW, 3) {
				info = append(info, warn(" "+l))
			}
		}
		if run != nil {
			info = append(info, "")
			info = append(info, m.runLines(p, run, textW)...)
		}
		if len(p.Run) > 0 {
			info = append(info, "")
			for _, l := range wrapRows("agents: "+ansi.Truncate(clean.Line(p.Usage()), 2<<10, "…"), textW, 3) {
				info = append(info, dim(" "+l))
			}
			for _, q := range p.Params {
				for _, l := range wrapRows("--"+q.Name+"  "+ansi.Truncate(clean.Line(q.About()), 2<<10, "…"), textW-2, 2) {
					info = append(info, dim("   "+l))
				}
			}
		} else {
			info = append(info, dim(" for you only: agents cannot run it"))
		}
	}

	if len(p.UI) == 0 || p.Err != "" {
		lines := append(append(head, ""), info...)
		if run != nil && p.Shows(plugin.ShowOutput) {
			// The output takes the rows left, at least a few: the parameters'
			// lines go first.
			if over := len(lines) + 7 - (h - 1); over > 0 {
				lines = lines[:max(len(lines)-over, len(head))]
			}
			lines = output(lines, h-1-len(lines))
		}
		return append(fitLines(lines, w, h-1), ansi.Truncate(keys, w, "…"))
	}

	// The plugin's own screen takes the rows left.
	top := append(append(head, ""), info...)
	if run != nil && p.Shows(plugin.ShowOutput) {
		top = output(top, min(7, h-len(top)-7))
	}
	room := h - len(top) - 1 // its rule
	if room < 6 {
		top = top[:max(len(top)-(6-room), len(head))]
		room = h - len(top) - 1
	}
	var state string
	switch {
	case inUI:
		state = "has the keyboard · " + m.screenKey(5) + " list"
	case u.running():
		state = "runs · enter or " + m.screenKey(5) + " uses it · x stops it"
	case u != nil:
		state = "ended · enter starts it again"
	case !v.allowed[p.Dir]:
		state = "enter shows it, to allow and start it"
	default:
		state = "enter starts it"
	}
	ruleStyle := lipgloss.NewStyle().Foreground(frameColor)
	if inUI {
		ruleStyle = lipgloss.NewStyle().Foreground(accent)
	}
	label := "─ screen: " + state + " "
	rule := m.onClick(ruleStyle.Render(ansi.Truncate(label+strings.Repeat("─", max(w-lipgloss.Width(label), 0)), w, "…")),
		func(m *Model) tea.Cmd {
			m.setFocus(focusReview)
			return m.openPluginUI(t, p)
		})
	out := append(top, rule)
	v.uiW, v.uiH = w, max(room, 3)
	if u == nil {
		return fitLines(out, w, h)
	}
	if pw, ph := u.pane.Size(); pw != v.uiW || ph != v.uiH {
		u.pane.Resize(v.uiW, v.uiH)
	}
	return fitLines(append(out, u.pane.Render(0, inUI)...), w, h)
}

// pluginKeys is the F5 screen's row of keys, each one a button.
func (m *Model) pluginKeys(pairs ...string) string {
	var out string
	for i := 0; i+1 < len(pairs); i += 2 {
		if out != "" {
			out += dim(" · ")
		}
		k, what := pairs[i], pairs[i+1]
		key := map[string]string{"↑↓": "down"}[k]
		if key == "" {
			key = k
		}
		out += m.onClick(keyHint(k)+" "+dim(what), func(m *Model) tea.Cmd { return m.pluginsKey(keyMsg(key)) })
	}
	return " " + out
}

// pluginsTitle is the F5 frame's title.
func (m *Model) pluginsTitle(t *tab) string {
	n := len(m.pluginList(t))
	if n == 0 {
		return "plugins"
	}
	return fmt.Sprintf("plugins (%d)", n)
}
