package app

import (
	"os"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
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
	// saving is the job of the answers core is saving: the dialog stays
	// until core says they are, and says why when they are not.
	saving string
}

var setupChoices = []struct{ value, label string }{
	{config.InstallHome, "In the agents' own folders in your home folder"},
	{config.InstallWorktree, "In each tab's folder — for agents in a sandbox"},
	{config.InstallOff, "Nowhere — I put them in place myself"},
}

func (m *Model) openSetup(then func(m *Model) tea.Cmd) tea.Cmd {
	d := &setupDialog{then: then, notesSet: m.Core.Cfg.DataDirSet(), worktreesSet: m.Core.Cfg.WorktreesDirSet()}
	d.worktrees = textinput.New()
	d.worktrees.Prompt = "worktrees folder: "
	d.worktrees.Placeholder = config.DefaultWorktreesDir
	d.worktrees.SetValue(config.DefaultWorktreesDir)
	d.worktrees.CharLimit = 300
	d.notes = textinput.New()
	d.notes.Prompt = "data folder: "
	d.notes.Placeholder = "(empty: the repository's .git folder)"
	d.notes.SetValue(m.Core.Cfg.DataDir)
	d.notes.CharLimit = 300
	for i, choice := range setupChoices {
		if choice.value == m.Core.Cfg.Install {
			d.choice = i
		}
	}
	m.dialog = d
	if d.worktreesSet {
		return d.focus(core.SetupSkills)
	}
	return d.focus(core.SetupWorktrees)
}

func (d *setupDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if d.saving != "" && k != "esc" && k != "ctrl+c" {
		return nil // the answers are being saved
	}
	switch k {
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
		for range core.SetupFields {
			f = (f + step + core.SetupFields) % core.SetupFields
			if f == core.SetupSkills || d.input(f) != nil {
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
	case f == core.SetupWorktrees && !d.worktreesSet:
		return &d.worktrees
	case f == core.SetupNotes && !d.notesSet:
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
	moveWtRoot := false
	if !d.notesSet {
		v := strings.TrimSpace(d.notes.Value())
		notes = &v
	}
	keys := []config.Edit{
		{Key: "install", Value: config.Quote(location)},
		{Key: "enabled_skills", Value: config.List(enabled)},
	}
	if notes != nil {
		keys = append(keys, config.Edit{Key: "data_dir", Value: config.Quote(*notes)})
	}
	if !d.worktreesSet {
		v := strings.TrimSpace(d.worktrees.Value())
		if v == "" {
			v = config.DefaultWorktreesDir
		}
		keys = append(keys, config.Edit{Key: "worktrees_dir", Value: config.Quote(v)})
		// Asked before any tab is made, so the new worktrees go there
		// from now on, not only from the next start.
		moveWtRoot = true
	}
	// Core's to write, in the file where it runs, and to go by once it
	// has: what comes next waits for it to say so (saved).
	d.err = ""
	var save tea.Cmd
	d.saving, save = m.editCoreConfigJob("the setup", core.EditConfig{Keys: keys, MoveWtRoot: moveWtRoot, Now: true,
		Apply: &core.ApplySkills{Otherwise: "saved in " + m.Core.ConfigPath()}})
	return save
}

// saved is core's word on the answers being saved: the dialog closes and
// what follows opens, or it stays, saying why they were not.
func (d *setupDialog) saved(m *Model, ev core.ConfigSavedMsg) tea.Cmd {
	d.saving = ""
	if ev.Err != "" {
		d.err = "could not write " + m.Core.ConfigPath() + ": " + ev.Err
		return nil
	}
	m.dialog = nil
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
		question(core.SetupWorktrees, "Where should new worktrees go?"),
		dim("  Each tab gets a worktree of its own: a checkout of the repository on"),
		dim("  the tab's branch, so tabs never get in each other's way."),
		field(core.SetupWorktrees, &d.worktrees, "worktrees folder: "+core.TildePath(m.Core.Cfg.WorktreesDir, home)),
		dim("    absolute, e.g. ~/worktrees-nerdr — one folder for all repositories"),
		dim("    relative, e.g. worktrees — in the folder nerdr is started in, or"),
		dim("    beside it when that is a repository"),
		"",
		question(core.SetupSkills, "Where may nerdr install its skills, plugins and hooks?"),
		dim("  Skills teach the agents to work with nerdr: explain a change in the"),
		dim("  review panel, ask you a question, hand over to another agent, split"),
		dim("  work into new tabs. All are turned on; turn single ones off in " + m.screenKey(4) + "."),
		dim("  Plugins and hooks tell nerdr when an agent is working or done."),
	}

	var where []string
	for _, tg := range skills.Home(home, m.Core.Available()) {
		var names []string
		for _, k := range tg.Agents {
			names = append(names, string(k))
		}
		where = append(where, core.TildePath(tg.Dir, home)+" ("+strings.Join(names, ", ")+")")
	}
	if slices.Contains(m.Core.Available(), agents.OpenCode) {
		where = append(where, core.TildePath(hooks.OpenCodePluginHome(home), home)+" (opencode's plugin, which says what it is doing)")
	}
	if slices.Contains(m.Core.Available(), agents.Copilot) {
		where = append(where, core.TildePath(hooks.CopilotPluginHome(home), home)+" (copilot's plugin, which says what it is doing)")
	}
	if slices.Contains(m.Core.Available(), agents.Antigravity) {
		where = append(where, core.TildePath(hooks.AgyHooksHome(home), home)+" (nerdr's hooks for agy, which say what it is doing, beside any others)")
	}
	if slices.Contains(m.Core.Available(), agents.Grok) {
		where = append(where, core.TildePath(hooks.GrokHooksHome(home), home)+" (nerdr's hooks for grok, which say what it is doing)")
	}
	if slices.Contains(m.Core.Available(), agents.Codex) {
		where = append(where, core.TildePath(core.CodexConfig(home), home)+" (trust for nerdr's codex hooks, which say what it is doing)")
	}
	if len(where) == 0 {
		where = []string{"none of the agents nerdr knows is on the PATH"}
	}
	hints := [][]string{
		where,
		{"<tab folder>/.claude/skills (.agents/skills for codex, gemini and agy),", "and the plugins and hooks of opencode, copilot and agy there, kept out of git"},
		{"nerdr installs nothing: copy the skills from skills/ in nerdr's repository.", "F1 explanations and agent-switch handoffs still work; " + m.screenKey(4) + " shows missing skills."},
	}
	for i, c := range setupChoices {
		mark := "○"
		if i == d.choice {
			mark = "●"
		}
		row := "  " + mark + " " + string(rune('1'+i)) + "  " + c.label
		if i == d.choice && d.field == core.SetupSkills {
			row = sel.Render(row)
		}
		lines = append(lines, m.onClick(row, func(m *Model) tea.Cmd {
			d.choice = i
			return d.focus(core.SetupSkills)
		}))
		for _, h := range hints[i] {
			lines = append(lines, dim("         "+h))
		}
	}

	notesSet := m.Core.Cfg.DataDir
	if notesSet == "" {
		notesSet = "(the repository's .git folder)"
	}
	lines = append(lines,
		"",
		question(core.SetupNotes, "Where should nerdr keep its data?"),
		dim("  What agents leave for the review panel: their explanations of a"),
		dim("  change, code reviews, and handoffs to another agent. Plain files,"),
		dim("  which the agents must be able to write."),
		field(core.SetupNotes, &d.notes, "data folder: "+notesSet),
		dim("    empty — in each repository's .git folder: shared by its worktrees,"),
		dim("    kept when a worktree is deleted, never part of the repository"),
		dim("    relative, e.g. .nerdr — inside each worktree, for agents in a sandbox"),
		dim("    absolute, e.g. ~/nerdr-data — one folder, a subfolder per repository"),
		"",
		m.keys("enter", "save in "+core.TildePath(m.Core.ConfigPath(), home), "tab", "next question", "esc", "ask next time"))
	if d.saving != "" {
		lines = append(lines, "", dim("saving…"))
	}
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	return box("Set up nerdr", lines, w)
}
