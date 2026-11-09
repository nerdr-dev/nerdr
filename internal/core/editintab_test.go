package core

import (
	"slices"
	"testing"
)

// TestEditInTabRunsTheEditorWhereTheFilesAre: a client on another machine
// has core open a file in a terminal pane of the tab, with the editor the
// client names, else the one of the environment the programs core starts
// have; a window editor, which cannot show there, gives way to vim.
func TestEditInTabRunsTheEditorWhereTheFilesAre(t *testing.T) {
	rec := &recorder{}
	m := &Core{Client: NoClient{}, Spawner: rec, ClientEnv: []string{"EDITOR=hx"}}
	tab := &Tab{}
	tab.ID, tab.Dir = "t1", t.TempDir()
	m.Tabs = []*Tab{tab}
	ref := m.Ref(tab)

	for _, c := range []struct {
		q    EditInTab
		want []string
	}{
		{EditInTab{T: ref, File: tab.Dir + "/a.go", Line: 4}, []string{"hx", "+4", tab.Dir + "/a.go"}},
		{EditInTab{T: ref, File: tab.Dir + "/a.go", Setting: "nvim"}, []string{"nvim", tab.Dir + "/a.go"}},
		{EditInTab{T: ref, File: tab.Dir + "/a.go", Setting: "code"}, []string{"vim", tab.Dir + "/a.go"}},
		{EditInTab{T: ref, Setting: "nvim"}, []string{"nvim", tab.Dir}},
	} {
		n := len(rec.specs)
		c.q.Do(m)
		if len(rec.specs) != n+1 {
			t.Fatalf("%+v started nothing", c.q)
		}
		s := rec.specs[n]
		if !slices.Equal(s.Argv, c.want) || s.Dir != tab.Dir {
			t.Errorf("%+v ran %q in %s, want %q", c.q, s.Argv, s.Dir, c.want)
		}
	}
	for _, p := range tab.Panes() {
		if p.Shell != nil {
			p.Shell.Close()
		}
	}

	n := len(rec.specs)
	EditInTab{T: ref, Setting: "code"}.Do(m)
	if len(rec.specs) != n {
		t.Error("a window editor was started for the worktree, where it cannot show")
	}
}
