package core

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Confined reports whether report r, from agent a, is from an agent in a
// sandbox: from an agent started sandboxed or whose kind is now
// (kindSandboxed), or from a process nerdr cannot place under a.
func Confined(r hooks.Report, a *Agent, kindSandboxed bool) bool {
	return r.Unvouched || a.InSandbox || kindSandboxed
}

// Vouch marks r Unvouched when the process it came from is not agent a, the
// one r names, nor runs under it, or is one nerdr cannot place. a is nil when
// no agent reports under that name. Where the kernel cannot be asked at all,
// the secret is taken alone. A process with the agent's id that started at
// another time is another, given the id once the agent's had ended; one
// whose start, or the agent's, nerdr cannot tell is not taken for it.
func Vouch(r hooks.Report, a *Agent) hooks.Report {
	if !r.Peer.Known {
		return r
	}
	if a == nil || a.Pane == nil || !slices.Contains(r.Peer.Chain, a.Pane.Pid()) {
		r.Unvouched = true
		return r
	}
	if start := r.Peer.StartOf(a.Pane.Pid()); a.PidStart == 0 || start != a.PidStart {
		r.Unvouched = true
	}
	return r
}

// Refusal is why report r, naming agent a, is refused, or "" when it is not:
// one that is Unvouched is taken only for an agent started sandboxed, and
// then confined. For an agent outside one it may be from any process, one in a
// sandbox among them.
func Refusal(r hooks.Report, a *Agent) string {
	if !r.Unvouched {
		return ""
	}
	if a == nil || !a.InSandbox {
		return "nerdr does not take this from you: you are not the agent NERDR_TAB names, nor run under it. " +
			"Run nerdr from that agent's own shell, with the variables nerdr gave it"
	}
	return ""
}

// SandboxedKind reports whether the config puts kind k in a sandbox.
func (m *Core) SandboxedKind(k agents.Kind) bool {
	return m.Cfg.Agents[k].Sandboxed
}

// Confined reports whether report r, from agent a, is from an agent in a
// sandbox; see core.Confined.
func (m *Core) Confined(r hooks.Report, a *Tab) bool {
	return Confined(r, &a.AgentState, m.SandboxedKind(a.Agent))
}

// InSandbox reports whether agent a runs in a sandbox, for what a
// confined agent may reach of it: as it was started, whatever the config
// says now. One not started since is not; a message for it is held when
// it starts outside one (see holdSandboxed).
func (m *Core) InSandbox(a *Tab) bool {
	return a.InSandbox
}

// Vouch marks r Unvouched when the process it came from is not the agent
// of the tab it names; see core.Vouch.
func (m *Core) Vouch(r hooks.Report) hooks.Report {
	return Vouch(r, m.HookAgent(r.Pane))
}

// SandboxRefusal is why report r is refused, or "" when it is not; see
// core.Refusal.
func (m *Core) SandboxRefusal(r hooks.Report) string {
	return Refusal(r, m.HookAgent(r.Pane))
}

// HookAgent is the agent that reports as id, nil when none does.
func (m *Core) HookAgent(id string) *Agent {
	if t := m.AgentByHook(id); t != nil {
		return &t.AgentState
	}
	return nil
}

// SandboxedKinds are the kinds a confined agent may start, by name.
func (m *Core) SandboxedKinds() string {
	var out []string
	for _, k := range m.Available() {
		if m.SandboxedKind(k) {
			out = append(out, string(k))
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// StartRefusal is why a confined agent may not have nerdr start kind k,
// or "".
func (m *Core) StartRefusal(k agents.Kind) string {
	if m.SandboxedKind(k) {
		return ""
	}
	return fmt.Sprintf("you run in a sandbox, and %s does not: nerdr starts only sandboxed agents for you (%s)",
		k, m.SandboxedKinds())
}

// SpinoffKinds are the agents the spinoff dialog offers for part pt:
// only the sandboxed when an agent in a sandbox sent it.
func (m *Core) SpinoffKinds(pt spinoff.Part) []agents.Kind {
	all := m.Available()
	if !pt.Sandboxed {
		return all
	}
	return slices.DeleteFunc(all, func(k agents.Kind) bool { return !m.SandboxedKind(k) })
}

// MarkSandboxed records on t's tab and in m.sandboxedDirs when any of its
// agents or reviewers has run in a sandbox, or when the tab's worktree
// previously ran one.
func (m *Core) MarkSandboxed(t *Tab) {
	if t == nil {
		return
	}
	o := t.Owner()
	if m.SandboxedDirs[o.Dir] {
		o.Sandboxed = true
	}
	for _, w := range o.Worktrees {
		if m.SandboxedDirs[w.Path] {
			o.Sandboxed = true
		}
	}
	for _, a := range o.AgentsIn() {
		if a.InSandbox || a.Sandboxed {
			a.Sandboxed = true
			o.Sandboxed = true
		}
	}
	if o.Sandboxed {
		if m.SandboxedDirs == nil {
			m.SandboxedDirs = map[string]bool{}
		}
		if o.Dir != "" {
			m.markSandboxedDir(o.Dir)
		}
		for _, w := range o.Worktrees {
			if w.Path != "" {
				m.markSandboxedDir(w.Path)
			}
			if w.Repo != "" {
				m.markSandboxedRepo(w.Repo, sandboxSource(o.Name, w.Path))
			}
		}
	}
}

// markSandboxedRepo records that a sandboxed agent has run in the main
// checkout repo or a worktree of it, and from, where: what the user is told
// when nerdr holds back there. The first source known is kept.
func (m *Core) markSandboxedRepo(repo, from string) {
	if m.SandboxedRepos == nil {
		m.SandboxedRepos = map[string]bool{}
	}
	if m.SandboxSources == nil {
		m.SandboxSources = map[string]string{}
	}
	m.SandboxedRepos[repo] = true
	if m.SandboxSources[repo] == "" && from != "" {
		m.SandboxSources[repo] = from
	}
	gitsafe.SandboxedRepoFrom(repo, m.SandboxSources[repo])
}

// sandboxSource names the tab and folder a repository's sandbox history is
// from.
func sandboxSource(tab, dir string) string {
	switch {
	case tab != "" && dir != "":
		return "tab " + tab + ", " + dir
	case tab != "":
		return "tab " + tab
	}
	return dir
}

// Register only newly recorded paths. Restored paths are registered once in
// restoreSandboxed, rather than rescanning all historical paths per tab.
func (m *Core) markSandboxedDir(dir string) {
	if !m.SandboxedDirs[dir] {
		m.SandboxedDirs[dir] = true
		gitsafe.Sandboxed(dir)
	}
}

// ImportSandboxed records provenance before imported code or objects are
// inspected, even if the import or the destination agent later fails.
func (m *Core) ImportSandboxed(wts []workspace.Worktree) {
	if m.SandboxedDirs == nil {
		m.SandboxedDirs = map[string]bool{}
	}
	for _, w := range wts {
		if w.Path != "" {
			m.markSandboxedDir(w.Path)
		}
		if w.Repo != "" {
			m.markSandboxedRepo(w.Repo, "work brought in from an agent in a sandbox, "+w.Path)
		}
	}
	m.SaveSession()
}

// restoreSandboxed takes up where agents have run in a sandbox, as the
// session has it, and tells gitsafe.
//
// Old sessions kept only the folders (SandboxHistoryVersion 0), not the
// repositories. A repository is marked for such a folder only when the
// folder can be tied to it: by a tab of the session whose worktree it is,
// or by the repository itself, whose .git/worktrees names the folder. A
// folder that is gone, of a tab that is closed, belongs to no repository
// nerdr can name, and marks none: marking every repository for it would
// stop host fetches and pushes in repositories no sandboxed agent has
// been in, for a user who may have tried a sandbox once.
func (m *Core) restoreSandboxed(sess state.Session) {
	m.SandboxedDirs = maps.Clone(sess.SandboxedDirs)
	if m.SandboxedDirs == nil {
		m.SandboxedDirs = map[string]bool{}
	}
	m.SandboxedRepos = maps.Clone(sess.SandboxedRepos)
	if m.SandboxedRepos == nil {
		m.SandboxedRepos = map[string]bool{}
	}
	m.SandboxSources = maps.Clone(sess.SandboxSources)
	if m.SandboxSources == nil {
		m.SandboxSources = map[string]string{}
	}
	// Recover known associations before dropping missing worktree paths.
	// Repo paths come from the saved session, never a worktree's .git file.
	for _, t := range sess.Tabs {
		for _, w := range t.Worktrees {
			if w.Repo != "" && (t.Sandboxed || m.SandboxedDirs[t.Dir] || m.SandboxedDirs[w.Path]) {
				m.SandboxedRepos[w.Repo] = true
				if m.SandboxSources[w.Repo] == "" {
					m.SandboxSources[w.Repo] = sandboxSource(t.Name, w.Path)
				}
			}
		}
	}
	if sess.SandboxHistoryVersion == 0 && len(sess.SandboxedDirs) > 0 {
		// ScanAll only inspects directory metadata.
		repos, _ := workspace.ScanAll(m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs)
		for dir := range sess.SandboxedDirs {
			for _, r := range repos {
				if !m.SandboxedRepos[r.Path] && sandboxedDirOf(r, dir) {
					m.SandboxedRepos[r.Path] = true
					m.SandboxSources[r.Path] = dir + ", from an earlier session"
				}
			}
		}
	}
	for repo := range m.SandboxedRepos {
		gitsafe.SandboxedRepoFrom(repo, m.SandboxSources[repo])
	}
	for dir := range m.SandboxedDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			delete(m.SandboxedDirs, dir)
			continue
		}
		gitsafe.Sandboxed(dir)
	}
}

// sandboxedDirOf reports whether dir, a folder an agent ran in a sandbox
// in, is of repository r: its main checkout, a worktree of it, or a tab's
// folder of several repositories with r's worktree in it under r's name
// (see workspace.MakePlan). The worktree is asked of r's own
// .git/worktrees, not of the folder's .git file, which the agent wrote.
func sandboxedDirOf(r workspace.Repo, dir string) bool {
	return SamePath(r.Path, dir) || gitsafe.Belongs(r.Path, dir) ||
		gitsafe.Belongs(r.Path, filepath.Join(dir, r.Name))
}

// ForgetSandboxed drops the sandbox history of the main checkout repo, as
// the user asked: nerdr fetches, pushes and runs hooks and setup there
// again as in a repository no sandboxed agent has worked in. The tabs with
// a worktree of it lose theirs too, or the next look at one would mark the
// repository again. It is refused, with why, while an agent runs in a
// sandbox there: what it writes next is not what the user looked at.
func (m *Core) ForgetSandboxed(repo string) (why string) {
	var tabs []*Tab
	for _, t := range m.Tabs {
		o := t.Owner()
		if slices.Contains(tabs, o) || !slices.ContainsFunc(o.Worktrees, func(w workspace.Worktree) bool { return SamePath(w.Repo, repo) }) {
			continue
		}
		for _, a := range o.AgentsIn() {
			if a.InSandbox {
				return fmt.Sprintf("%s runs in a sandbox in tab %s now: close or restart it outside the sandbox first", a.Agent, o.Name)
			}
		}
		tabs = append(tabs, o)
	}
	for _, o := range tabs {
		o.Sandboxed = false
		for _, a := range o.AgentsIn() {
			a.Sandboxed = false
		}
		delete(m.SandboxedDirs, o.Dir)
		for _, w := range o.Worktrees {
			delete(m.SandboxedDirs, w.Path)
		}
	}
	for dir := range m.SandboxedDirs {
		if SamePath(dir, repo) || gitsafe.Belongs(repo, dir) {
			delete(m.SandboxedDirs, dir)
		}
	}
	for r := range m.SandboxedRepos {
		if SamePath(r, repo) {
			delete(m.SandboxedRepos, r)
			delete(m.SandboxSources, r)
		}
	}
	gitsafe.ForgetSandboxed(repo)
	m.SaveSession()
	return ""
}

// ForgetSandboxedIn is ForgetSandboxed on a saved session, for nerdr
// sandbox clear, run while no nerdr has the session: no agent runs then.
// With repo "", all the session's sandbox history goes. It reports whether
// there was any to drop.
func ForgetSandboxedIn(sess *state.Session, repo string) bool {
	of := func(t state.Tab) bool {
		return repo == "" || slices.ContainsFunc(t.Worktrees, func(w workspace.Worktree) bool { return SamePath(w.Repo, repo) })
	}
	found := false
	for i := range sess.Tabs {
		t := &sess.Tabs[i]
		if !of(*t) {
			continue
		}
		found = found || t.Sandboxed
		t.Sandboxed = false
		for j := range t.Agents {
			found = found || t.Agents[j].Sandboxed
			t.Agents[j].Sandboxed = false
		}
		delete(sess.SandboxedDirs, t.Dir)
		for _, w := range t.Worktrees {
			delete(sess.SandboxedDirs, w.Path)
		}
	}
	for dir := range sess.SandboxedDirs {
		if repo == "" || SamePath(dir, repo) || gitsafe.Belongs(repo, dir) {
			delete(sess.SandboxedDirs, dir)
			found = true
		}
	}
	for r := range sess.SandboxedRepos {
		if repo == "" || SamePath(r, repo) {
			delete(sess.SandboxedRepos, r)
			delete(sess.SandboxSources, r)
			found = true
		}
	}
	return found
}

// NoTerminal is what a confined agent is told when it asks for a
// terminal pane.
const NoTerminal = "you run in a sandbox, and a terminal pane would not: " +
	"nerdr does not run commands outside the sandbox for you. Run it in your own shell, " +
	"or ask the user to run it"

// holdSandboxed keeps the messages agents in a sandbox wrote for t from
// being typed in by themselves, t having started outside one: they wait
// in F3 for the user.
func (m *Core) HoldSandboxed(t *Tab) {
	held := 0
	for i, msg := range t.Messages {
		if msg.Sandboxed && msg.Auto {
			t.Messages[i].Auto = false
			held++
		}
	}
	if held > 0 {
		m.SaveSession()
		m.Toast(fmt.Sprintf("%s: %d message(s) from an agent in a sandbox wait in F3, %s not being in one",
			t.Name, held, t.Agent))
	}
}

// HasSandboxed reports whether any agent of t's tab runs or ran in a sandbox.
func (t *Tab) HasSandboxed() bool {
	if t == nil {
		return false
	}
	o := t.Owner()
	if o.Sandboxed || o.InSandbox {
		return true
	}
	for _, a := range o.AgentsIn() {
		if a.InSandbox || a.Sandboxed {
			return true
		}
	}
	return false
}
