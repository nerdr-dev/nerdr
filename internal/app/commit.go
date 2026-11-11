package app

import (
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// F2's c commits the tab's changes: every file, new ones too, in each of
// its worktrees that has any, with the message typed in the dialog — and
// then, as picked there, pushes them, or pushes them and opens the pull
// request, as p and P do.

// commitDialog asks for the message, above the files it commits.
type commitDialog struct {
	t *core.Tab
	// job is the id of the look at the changes, which core answers.
	job   string
	wts   []workspace.Worktree
	input textarea.Model
	// files are each worktree's changes, by its path, as git status lists
	// them; nil until they have been looked up.
	files map[string][]string
	// worktreeHooks is whether any of the tab's repositories has git
	// hooks.
	worktreeHooks bool
	// then is what follows the commit: one of commitThen.
	then int
	err  string
}

var selectedChoice = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231")).Bold(true)

// commitThen are what can follow a commit, with the panel's key that does
// it on its own: none, a push, a push and the pull request.
var commitThen = []struct{ label, key string }{
	{"Commit", ""},
	{"Commit + push", "p"},
	{"Commit + push + open PR", "P"},
}

// commitFilesShown is how many of a worktree's files the dialog lists.
const commitFilesShown = 12

// openCommit opens the dialog for committing t's changes, and looks them
// up.
func (m *Model) openCommit(t *core.Tab) tea.Cmd {
	m.do(core.MarkSandboxed{T: m.fleet.Ref(t)})
	in := newTextArea("what the change does", min(max(m.width-16, 30), 80))
	in.SetHeight(4)
	d := &commitDialog{t: t, job: core.NewJobID(), wts: append([]workspace.Worktree(nil), t.Worktrees...), input: in}
	m.dialog = d
	return m.do(core.LookChanges{Job: d.job, T: m.fleet.Ref(t)})
}

// onCommitFiles shows the changes in the dialog that asked for them.
func (m *Model) onCommitFiles(msg core.ChangesMsg) {
	d, ok := m.dialog.(*commitDialog)
	if !ok || d.job != msg.Job {
		return
	}
	d.files, d.worktreeHooks, d.err = msg.Files, msg.WorktreeHooks, msg.Err
}

// changed is how many files the commit takes.
func (d *commitDialog) changed() int {
	n := 0
	for _, fs := range d.files {
		n += len(fs)
	}
	return n
}

func (d *commitDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "tab":
		d.then = (d.then + 1) % len(commitThen)
		return nil
	case "shift+tab":
		d.then = (d.then + len(commitThen) - 1) % len(commitThen)
		return nil
	case "enter":
		message := strings.TrimSpace(d.input.Value())
		switch {
		case d.files == nil:
			return nil // still looking
		case d.changed() == 0:
			d.err = "there is nothing to commit"
			return nil
		case message == "":
			d.err = "write what the change does first"
			return nil
		}
		m.dialog = nil
		var in []string
		for _, w := range d.wts {
			if len(d.files[w.Path]) > 0 {
				in = append(in, w.Path)
			}
		}
		t := d.t
		p := m.curBranchPanel()
		if p == nil || p.t != t {
			p = &tabPanel{t: t}
		}
		// What follows the commit is worked out now, from what is known of
		// the tab here; core does it once the commit has been made.
		q, busy, then := p.work(commitThen[d.then].key)
		q.T, q.Message, q.CommitIn = m.fleet.Ref(t), message, in
		p.busy = "committing…"
		if then {
			p.busy = "committing, then " + busy
		}
		return m.do(q)
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return cmd
}

func (d *commitDialog) view(m *Model, w, h int) string {
	var lines []string
	switch {
	case d.files == nil:
		lines = append(lines, dim("looking at the changes…"))
	case d.changed() == 0:
		lines = append(lines, "There is nothing to commit.")
	default:
		lines = append(lines, "Commits every change, new files too:")
		if d.t.HasSandboxed() && d.worktreeHooks {
			lines = append(lines, dim("Git hooks will not run: an agent in this tab ran in a sandbox. Git LFS files are still pushed."))
		}
		for _, wt := range d.wts {
			fs := d.files[wt.Path]
			if len(fs) == 0 {
				continue
			}
			if len(d.wts) > 1 {
				lines = append(lines, "", filepath.Base(wt.Repo)+" "+dim(wt.Branch))
			}
			for i, f := range fs {
				if i == commitFilesShown {
					lines = append(lines, dim("  … and "+core.Plural(len(fs)-i, "more file")))
					break
				}
				lines = append(lines, "  "+dim(f[:min(2, len(f))])+" "+f[min(3, len(f)):])
			}
		}
	}
	lines = append(lines, "", "Message:", d.input.View(), "")
	var choices []string
	for i, c := range commitThen {
		label := " " + c.label + " "
		if i == d.then {
			label = selectedChoice.Render(label)
		} else {
			label = dim(label)
		}
		choices = append(choices, m.onClick(label, func(m *Model) tea.Cmd {
			d.then = i
			return nil
		}))
	}
	lines = append(lines, strings.Join(choices, "  "), "",
		m.keys("enter", strings.ToLower(commitThen[d.then].label[:1])+commitThen[d.then].label[1:], "tab", "choose", "", core.NewLineHint, "esc", "cancel"))
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	return box("Commit · "+d.t.Name, lines, w)
}
