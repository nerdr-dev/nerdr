package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/internal/workspace"
	"github.com/nerdr-dev/nerdr/skills"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Asked to, the agents explain a change for the review panel (the
// nerdr-explain skill, one of those nerdr ships). The first start asks
// where new worktrees go, where nerdr may install those skills and its
// plugins and hooks, and where nerdr's data —
// the explanations, code reviews and handoffs — is kept, and writes the answers into
// the config file; see setupDialog. All the skills are turned on: single
// ones are turned off in F4.

// setupDialog is the first start's questions.
type setupDialog struct {
	choice int // index into setupChoices
	// worktrees and notes are the folders typed; field is which of
	// setupWorktrees, setupSkills and setupNotes has the keys.
	worktrees, notes textinput.Model
	field            int
	// notesSet and worktreesSet are whether the config file sets them
	// already: then they are not asked for.
	notesSet, worktreesSet bool
	// then is what opens once it is done: the new-tab dialog when there
	// are no tabs yet.
	then func(m *Model) tea.Cmd
	err  string
}

// The setup dialog's questions, in the order asked.
const (
	setupWorktrees = iota
	setupSkills
	setupNotes
	setupFields
)

var setupChoices = []struct{ value, label string }{
	{config.InstallHome, "In the agents' own folders in your home folder"},
	{config.InstallWorktree, "In each tab's folder — for agents in a sandbox"},
	{config.InstallOff, "Nowhere — I put them in place myself"},
}

func (m *Model) openSetup(then func(m *Model) tea.Cmd) tea.Cmd {
	d := &setupDialog{then: then, notesSet: m.cfg.DataDirSet(), worktreesSet: m.cfg.WorktreesDirSet()}
	d.worktrees = textinput.New()
	d.worktrees.Prompt = "worktrees folder: "
	d.worktrees.Placeholder = config.DefaultWorktreesDir
	d.worktrees.SetValue(config.DefaultWorktreesDir)
	d.worktrees.CharLimit = 300
	d.notes = textinput.New()
	d.notes.Prompt = "data folder: "
	d.notes.Placeholder = "(empty: the repository's .git folder)"
	d.notes.SetValue(m.cfg.DataDir)
	d.notes.CharLimit = 300
	for i, choice := range setupChoices {
		if choice.value == m.cfg.Install {
			d.choice = i
		}
	}
	m.dialog = d
	if d.worktreesSet {
		return d.focus(setupSkills)
	}
	return d.focus(setupWorktrees)
}

func (d *setupDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch k := msg.String(); k {
	case "esc", "ctrl+c":
		// Asked again at the next start.
		m.dialog = nil
		return d.next(m)
	case "tab", "shift+tab":
		step := 1
		if k == "shift+tab" {
			step = -1
		}
		f := d.field
		for range setupFields {
			f = (f + step + setupFields) % setupFields
			if f == setupSkills || d.input(f) != nil {
				break
			}
		}
		return d.focus(f)
	case "enter":
		return d.save(m)
	}
	if in := d.input(d.field); in != nil {
		var cmd tea.Cmd
		*in, cmd = in.Update(msg)
		return cmd
	}
	switch k := msg.String(); k {
	case "up", "k":
		d.choice = max(d.choice-1, 0)
	case "down", "j":
		d.choice = min(d.choice+1, len(setupChoices)-1)
	case "1", "2", "3":
		d.choice = int(k[0] - '1')
	}
	return nil
}

// input is field f's folder, nil for the skills and for a folder the
// config file sets already.
func (d *setupDialog) input(f int) *textinput.Model {
	switch {
	case f == setupWorktrees && !d.worktreesSet:
		return &d.worktrees
	case f == setupNotes && !d.notesSet:
		return &d.notes
	}
	return nil
}

// focus gives field f the keys.
func (d *setupDialog) focus(f int) tea.Cmd {
	d.field = f
	d.notes.Blur()
	d.worktrees.Blur()
	if in := d.input(f); in != nil {
		return in.Focus()
	}
	return nil
}

// save writes the answers into the config file and acts on them.
func (d *setupDialog) save(m *Model) tea.Cmd {
	location := setupChoices[d.choice].value
	enabled := []string{}
	for _, sk := range skills.All() {
		enabled = append(enabled, sk.Name)
	}
	var notes *string
	if !d.notesSet {
		v := strings.TrimSpace(d.notes.Value())
		notes = &v
	}
	if err := config.SaveSetup(location, notes, enabled); err != nil {
		d.err = "could not write " + config.Path() + ": " + err.Error()
		return nil
	}
	if !d.worktreesSet {
		v := strings.TrimSpace(d.worktrees.Value())
		if v == "" {
			v = config.DefaultWorktreesDir
		}
		if err := config.SaveWorktreesDir(v); err != nil {
			d.err = "could not write " + config.Path() + ": " + err.Error()
			return nil
		}
		// Asked before any tab is made, so the new worktrees go there
		// from now on, not only from the next start.
		m.cfg.WorktreesDir = config.ExpandHome(v)
		m.wtRoot = workspace.WorktreesRoot(m.root, m.cfg.WorktreesDir)
		d.worktreesSet = true
	}
	m.cfg.EnabledSkills = enabled
	m.cfg.Install = location
	if notes != nil {
		m.cfg.DataDir = *notes
		store.SetDataDir(*notes)
	}
	m.dialog = nil
	if note := m.applySkills(); note != "" {
		m.toast(note)
	} else {
		m.toast("saved in " + config.Path())
	}
	return d.next(m)
}

func (d *setupDialog) next(m *Model) tea.Cmd {
	if d.then != nil {
		return d.then(m)
	}
	return nil
}

func (d *setupDialog) view(m *Model, w, h int) string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	home, _ := os.UserHomeDir()
	// question is a question's heading, marked while it has the keys.
	question := func(f int, s string) string {
		if d.field == f {
			return sel.Render("❯ " + s)
		}
		return "  " + s
	}
	field := func(f int, in *textinput.Model, set string) string {
		if d.input(f) == nil {
			return "  " + set + dim("  — set in the config file")
		}
		return "  " + m.onClick(in.View(), func(m *Model) tea.Cmd { return d.focus(f) })
	}

	lines := []string{
		question(setupWorktrees, "Where should new worktrees go?"),
		dim("  Each tab gets a worktree of its own: a checkout of the repository on"),
		dim("  the tab's branch, so tabs never get in each other's way."),
		field(setupWorktrees, &d.worktrees, "worktrees folder: "+tildePath(m.cfg.WorktreesDir, home)),
		dim("    absolute, e.g. ~/worktrees-nerdr — one folder for all repositories"),
		dim("    relative, e.g. worktrees — in the folder nerdr is started in, or"),
		dim("    beside it when that is a repository"),
		"",
		question(setupSkills, "Where may nerdr install its skills, plugins and hooks?"),
		dim("  Skills teach the agents to work with nerdr: explain a change in the"),
		dim("  review panel, ask you a question, hand over to another agent, split"),
		dim("  work into new tabs. All are turned on; turn single ones off in " + m.screenKey(4) + "."),
		dim("  Plugins and hooks tell nerdr when an agent is working or done."),
	}

	var where []string
	for _, tg := range skills.Home(home, agents.Available(m.cfg.Agents)) {
		var names []string
		for _, k := range tg.Agents {
			names = append(names, string(k))
		}
		where = append(where, tildePath(tg.Dir, home)+" ("+strings.Join(names, ", ")+")")
	}
	if slices.Contains(agents.Available(m.cfg.Agents), agents.OpenCode) {
		where = append(where, tildePath(hooks.OpenCodePluginHome(home), home)+" (opencode's plugin, which says what it is doing)")
	}
	if slices.Contains(agents.Available(m.cfg.Agents), agents.Copilot) {
		where = append(where, tildePath(hooks.CopilotPluginHome(home), home)+" (copilot's plugin, which says what it is doing)")
	}
	if slices.Contains(agents.Available(m.cfg.Agents), agents.Antigravity) {
		where = append(where, tildePath(hooks.AgyHooksHome(home), home)+" (nerdr's hooks for agy, which say what it is doing, beside any others)")
	}
	if slices.Contains(agents.Available(m.cfg.Agents), agents.Codex) {
		where = append(where, tildePath(codexConfig(home), home)+" (trust for nerdr's codex hooks, which say what it is doing)")
	}
	if len(where) == 0 {
		where = []string{"none of the agents nerdr knows is on the PATH"}
	}
	hints := [][]string{
		where,
		{"<tab folder>/.claude/skills (.agents/skills for codex, gemini and agy),", "and the plugins and hooks of opencode, copilot and agy there, kept out of git"},
		{"nerdr installs nothing: copy the skills from skills/ in nerdr's repository.", "What uses one the agent cannot find is off; " + m.screenKey(4) + " says which."},
	}
	for i, c := range setupChoices {
		mark := "○"
		if i == d.choice {
			mark = "●"
		}
		row := "  " + mark + " " + string(rune('1'+i)) + "  " + c.label
		if i == d.choice && d.field == setupSkills {
			row = sel.Render(row)
		}
		lines = append(lines, m.onClick(row, func(m *Model) tea.Cmd {
			d.choice = i
			return d.focus(setupSkills)
		}))
		for _, h := range hints[i] {
			lines = append(lines, dim("         "+h))
		}
	}

	notesSet := m.cfg.DataDir
	if notesSet == "" {
		notesSet = "(the repository's .git folder)"
	}
	lines = append(lines,
		"",
		question(setupNotes, "Where should nerdr keep its data?"),
		dim("  What agents leave for the review panel: their explanations of a"),
		dim("  change, code reviews, and handoffs to another agent. Plain files,"),
		dim("  which the agents must be able to write."),
		field(setupNotes, &d.notes, "data folder: "+notesSet),
		dim("    empty — in each repository's .git folder: shared by its worktrees,"),
		dim("    kept when a worktree is deleted, never part of the repository"),
		dim("    relative, e.g. .nerdr — inside each worktree, for agents in a sandbox"),
		dim("    absolute, e.g. ~/nerdr-data — one folder, a subfolder per repository"),
		"",
		m.keys("enter", "save in "+tildePath(config.Path(), home), "tab", "next question", "esc", "ask next time"))
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	return box("Set up nerdr", lines, w)
}

func tildePath(p, home string) string {
	if home != "" {
		if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
			return "~/" + rest
		}
	}
	return p
}

// applySkills puts the skills where the install setting says, and says where it
// wrote any; "" when nothing was written. For "worktree" that is the tabs'
// folders, which each tab's start also sees to (see installProjectSkills).
func (m *Model) applySkills() string {
	home, _ := os.UserHomeDir()
	homeDirs := map[string]bool{}
	for _, k := range agents.All {
		for _, dir := range skills.Dirs(home, "", k)[1:] {
			if dir != "" {
				homeDirs[dir] = true
			}
		}
	}
	wtDirs := map[string]bool{}
	for _, t := range m.tabs {
		for _, k := range agents.All {
			wtDirs[skills.ProjectDir(t.Dir, k)] = true
		}
	}
	for dir := range homeDirs {
		if m.cfg.EnabledSkills == nil {
			continue
		}
		if err := skills.RemoveDisabled(dir, m.cfg.SkillEnabled); err != nil {
			return "skills: " + err.Error()
		}
	}
	for dir := range wtDirs {
		if m.cfg.EnabledSkills == nil {
			continue
		}
		if err := skills.RemoveDisabledProject(dir, m.cfg.SkillEnabled); err != nil {
			return "skills: " + err.Error()
		}
	}

	switch m.cfg.Install {
	case config.InstallHome:
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		var wrote []string
		for _, tg := range skills.Home(home, agents.Available(m.cfg.Agents)) {
			names, err := skills.Sync(tg.Dir, m.cfg.SkillEnabled)
			if err != nil {
				return "skills: " + err.Error()
			}
			if len(names) > 0 {
				wrote = append(wrote, tildePath(tg.Dir, home))
			}
		}
		if slices.Contains(agents.Available(m.cfg.Agents), agents.OpenCode) {
			// With the skills, nerdr's plugin, which tells nerdr what
			// opencode is doing.
			file := hooks.OpenCodePluginHome(home)
			if ok, err := hooks.InstallOpenCodePlugin(file); err != nil {
				return "opencode plugin: " + err.Error()
			} else if ok {
				wrote = append(wrote, tildePath(file, home))
			}
		}
		if slices.Contains(agents.Available(m.cfg.Agents), agents.Copilot) {
			// And nerdr's plugin, whose hooks tell nerdr what copilot is
			// doing.
			dir := hooks.CopilotPluginHome(home)
			if ok, err := hooks.InstallCopilotPlugin(dir); err != nil {
				return "copilot plugin: " + err.Error()
			} else if ok {
				wrote = append(wrote, tildePath(dir, home))
			}
		}
		if slices.Contains(agents.Available(m.cfg.Agents), agents.Antigravity) {
			// And nerdr's hooks in agy's hooks file, beside any others.
			file := hooks.AgyHooksHome(home)
			if ok, _, err := hooks.InstallAgyHooks(file); err != nil {
				return "agy hooks: " + err.Error()
			} else if ok {
				wrote = append(wrote, tildePath(file, home))
			}
		}
		if slices.Contains(agents.Available(m.cfg.Agents), agents.Codex) {
			// codex runs nerdr's hooks, which tell nerdr what it is
			// doing, once it trusts them, in its own config.
			if ok, err := m.trustCodexHooks(true); ok {
				wrote = append(wrote, tildePath(codexConfig(home), home)+" (trusts nerdr's codex hooks)")
			} else if err != nil {
				debugf("codex hooks: %v", err)
			}
		}
		if len(wrote) > 0 {
			return "skills installed in " + strings.Join(wrote, ", ")
		}
	case config.InstallWorktree:
		for _, t := range m.tabs {
			m.installProjectSkills(t)
		}
	}
	return ""
}

// installProjectSkills puts the skills in the folder t's agent starts in,
// when the setting says so, and keeps them — and a notes folder inside the
// worktrees — out of git.
func (m *Model) installProjectSkills(t *tab) {
	for _, w := range t.Worktrees {
		_ = store.ExcludeData(w.Path)
	}
	if m.cfg.Install != config.InstallWorktree {
		return
	}
	if _, err := skills.SyncProject(skills.ProjectDir(t.Dir, t.Agent), m.cfg.SkillEnabled); err != nil {
		m.toast("skills: " + err.Error())
		return
	}
	excludes := skills.ExcludePatterns()
	if t.Agent == agents.OpenCode {
		if _, err := hooks.InstallOpenCodePluginProject(t.Dir); err != nil {
			m.toast("opencode plugin: " + err.Error())
		}
		excludes = append(excludes, hooks.OpenCodePluginExclude)
	}
	if t.Agent == agents.Copilot {
		if _, err := hooks.InstallCopilotPluginProject(t.Dir); err != nil {
			m.toast("copilot plugin: " + err.Error())
		}
		excludes = append(excludes, hooks.CopilotPluginExclude)
	}
	if t.Agent == agents.Antigravity {
		// In the project's own hooks file, unless the repository keeps
		// one: that is not nerdr's to change.
		file := hooks.AgyHooksProject(t.Dir)
		if !tracked(t.Dir, file) {
			if _, made, err := hooks.InstallAgyHooksProject(t.Dir); err != nil {
				m.toast("agy hooks: " + err.Error())
			} else if made {
				excludes = append(excludes, hooks.AgyHooksExclude)
			}
		}
	}
	for _, w := range t.Worktrees {
		if filepath.Clean(w.Path) != filepath.Clean(t.Dir) {
			continue // a tab over several repositories starts above them
		}
		for _, p := range excludes {
			_ = store.Exclude(w.Path, p)
		}
	}
}

// codexConfig is where codex keeps the trust it gives hooks.
func codexConfig(home string) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "config.toml")
	}
	return filepath.Join(home, ".codex", "config.toml")
}

// tracked reports whether git keeps file in the repository dir is in.
func tracked(dir, file string) bool {
	return proc.Run(gitsafe.Command(dir, "ls-files", "--error-unmatch", "--", file)) == nil
}
