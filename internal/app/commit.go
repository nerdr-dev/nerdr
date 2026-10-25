package app

import (
	"path/filepath"
	"strings"

	"nerdr/internal/workspace"

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
	t     *tab
	wts   []workspace.Worktree
	input textarea.Model
	// files are each worktree's changes, by its path, as git status lists
	// them; nil until they have been looked up.
	files map[string][]string
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

// commitFilesMsg carries the changes the dialog d commits.
type commitFilesMsg struct {
	d     *commitDialog
	files map[string][]string
	err   string
}

// commitFilesShown is how many of a worktree's files the dialog lists.
const commitFilesShown = 12

// openCommit opens the dialog for committing t's changes, and looks them
// up.
func (m *Model) openCommit(t *tab) tea.Cmd {
	in := newTextArea("what the change does", min(max(m.width-16, 30), 80))
	in.SetHeight(4)
	d := &commitDialog{t: t, wts: append([]workspace.Worktree(nil), t.Worktrees...), input: in}
	m.dialog = d
	return func() tea.Msg {
		files := map[string][]string{}
		var errs []string
		for _, w := range d.wts {
			fs, err := workspace.Changes(w)
			if err != nil {
				errs = append(errs, filepath.Base(w.Repo)+": "+err.Error())
				continue
			}
			files[w.Path] = fs
		}
		return commitFilesMsg{d: d, files: files, err: strings.Join(errs, "; ")}
	}
}

// onCommitFiles shows the changes in the dialog that asked for them.
func (m *Model) onCommitFiles(msg commitFilesMsg) {
	if m.dialog != msg.d {
		return
	}
	msg.d.files, msg.d.err = msg.files, msg.err
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
		var wts []workspace.Worktree
		for _, w := range d.wts {
			if len(d.files[w.Path]) > 0 {
				wts = append(wts, w)
			}
		}
		t := d.t
		p := m.curBranchPanel()
		if p == nil || p.t != t {
			p = &tabPanel{t: t}
		}
		// What follows is worked out now, from what is known of the tab
		// here; it runs once the commit has been made.
		then, busy := p.job(commitThen[d.then].key)
		p.busy = "committing…"
		if then != nil {
			p.busy = "committing, then " + busy
		}
		return func() tea.Msg {
			msg := commitAll(t, wts, message)
			if msg.err != "" || then == nil {
				return msg
			}
			after := then()
			after.note = strings.TrimSpace(msg.note + "; " + after.note)
			if after.err != "" {
				after.err = msg.note + ", but " + after.err
			}
			return after
		}
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return cmd
}

func commitAll(t *tab, wts []workspace.Worktree, message string) prActionMsg {
	var errs, done []string
	for _, w := range wts {
		name := filepath.Base(w.Repo)
		if err := workspace.Commit(w, message); err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
		done = append(done, name)
	}
	if len(errs) > 0 {
		return prActionMsg{t: t, err: "commit failed: " + strings.Join(errs, "; ")}
	}
	note := "committed " + t.Name
	if len(wts) > 1 {
		note += " (" + strings.Join(done, ", ") + ")"
	}
	return prActionMsg{t: t, note: note}
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
					lines = append(lines, dim("  … and "+plural(len(fs)-i, "more file")))
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
		m.keys("enter", strings.ToLower(commitThen[d.then].label[:1])+commitThen[d.then].label[1:], "tab", "choose", "", newLineHint, "esc", "cancel"))
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	return box("Commit · "+d.t.Name, lines, w)
}
