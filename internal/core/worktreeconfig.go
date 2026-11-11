package core

import (
	"sort"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

// noticeRefusedGit tells the user, once for each, of the repositories
// nerdr has stopped running git in because they have
// extensions.worktreeConfig on and a sandboxed agent has run in them (see
// gitsafe.Sandboxed). Their tabs show no git changes, and commit, push and
// new worktrees fail, until the extension is off; the notice is the only
// place that says why.
func (m *Core) noticeRefusedGit() Cmd {
	refused := gitsafe.Refusals()
	paths := make([]string, 0, len(refused))
	for p := range refused {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var cmds []Cmd
	for _, p := range paths {
		cmds = append(cmds, m.Tell(NoticeMsg(refused[p])))
	}
	return Batch(cmds...)
}
