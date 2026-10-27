package app

// Both the mouse menu and prefix help use this order and these group names.
// run is the keys after the prefix: one, or a group's key and the
// command's in it ("i p"). An entry whose run is a group's key opens the
// group. rare entries work and are in the menu and the palette but not in
// the hint line, which keeps to the keys most used. desc says what the
// command does, in the palette, which is all the help there is.
type commandEntry struct {
	key, label, run, desc string
	rare                  bool
}
type commandGroup struct {
	title   string
	entries []commandEntry
}

func commandGroups() []commandGroup {
	return []commandGroup{
		{"Overview and navigation", []commandEntry{
			{key: "←", label: "Tab left", run: "left", desc: "the previous tab, or repository on the repository row; the prefix stays on for more"},
			{key: "→", label: "Tab right", run: "right", desc: "the next tab, or repository on the repository row; the prefix stays on for more"},
			{key: "↓", label: "Worktree tab", run: "down", desc: "the arrows move along the tab row, among the repository's tabs"},
			{key: "↑", label: "Repository tab", run: "up", desc: "the arrows move along the repository row (shown with two or more)"},
			{key: "]", label: "Next repository", run: "]", rare: true, desc: "the next repository's tab you were last in; the prefix stays on for more"},
			{key: "[", label: "Previous repository", run: "[", rare: true, desc: "the previous repository's tab you were last in; the prefix stays on for more"},
			{key: "tab", label: "Next pane / review", run: "tab", desc: "move the keyboard to the tab's next pane, then the split's other side or the review panel"},
			{key: "w", label: "Agents waiting", run: "w", desc: "the tabs whose agent is done or asks something, the longest waiting first"},
			{key: "n", label: "Next agent waiting", run: "n", desc: "go to the tab that has waited longest for you"},
			{key: "f", label: "Find tab", run: "f", desc: "find a tab by typing part of its name, across repositories (space too)"},
			{key: "1–9,0", label: "Tab in group", run: "1", desc: "go to tab 1–9 of the repository; 0 is the last"},
			{key: "h", label: "Since you were here", run: "h", rare: true, desc: "what happened in the tab while you were away: commits, PR, CI, the agent"},
		}},
		{"Tasks and tabs", []commandEntry{
			{key: "c", label: "New tab", run: "c", desc: "pick repositories, branch and agent for a new tab"},
			{key: "C", label: "New tab on this branch", run: "C", rare: true, desc: "a new branch on top of this tab's, for work that builds on it before it is merged; its pull request is against this one's"},
			{key: "x", label: "Close tab", run: "x", desc: "close the tab; asks whether to delete its worktree too"},
			{key: "r", label: "Rename tab", run: "r", desc: "give the tab a name of its own"},
			{key: "⇧←", label: "Move tab left", run: "shift+left", rare: true, desc: "move the tab one place left in the tab row"},
			{key: "⇧→", label: "Move tab right", run: "shift+right", rare: true, desc: "move the tab one place right in the tab row"},
		}},
		{"Agent and tasks", []commandEntry{
			{key: "a", label: "Answer, note, switch, restart…", run: "a"},
			{key: "m", label: "Task for agent", run: "m", desc: "what the agent should do next: sent by itself when the agent is idle, or kept in F3 until then"},
			{key: "M", label: "Task to several tabs", run: "M", desc: "one task for the agents of the tabs you tick"},
		}},
		{"Git and GitHub", []commandEntry{
			{key: "i", label: "Branches, PRs, issues, review…", run: "i"},
		}},
		{"View", []commandEntry{
			{key: "e", label: "Review panel: show, screens…", run: "e"},
			{key: "z", label: "Layouts, split, zoom…", run: "z"},
			{key: "<", label: "Wider review / split", run: "<", rare: true, desc: "make the review panel, or the right of the split, wider (, too)"},
			{key: ">", label: "Narrower review / split", run: ">", rare: true, desc: "make the review panel, or the right of the split, narrower (. too)"},
		}},
		{"Panes", []commandEntry{
			{key: "t", label: "Terminal, agent, review, panes…", run: "t"},
		}},
		{"Work tools", []commandEntry{
			{key: "o", label: "Open in editor", run: "o", desc: "the worktree in your editor: VS Code, unless worktree_editor says otherwise"},
			{key: "u", label: "Usage and rate limits", run: "u", desc: "how much of your Claude and Codex plans' limits is used, when they reset, and how full each agent's context is"},
			{key: "/", label: "Search conversations", run: "/", desc: "find what was said in the agents' conversations in these repositories and worktrees, and go to its tab or carry it on"},
			{key: "b", label: "Scroll back", run: "b", rare: true, desc: "scroll back through what the agent wrote, with the arrows and PgUp/PgDn; q leaves"},
		}},
		{"Settings and help", []commandEntry{
			{key: "?", label: "All commands", run: "?", desc: "this list: type part of a name or what it does to find it (: too)"},
			{key: "g", label: "git and gh calls", run: "g", rare: true, desc: "how many times nerdr has run git and gh for each tab, and which commands, since it started; and what of nerdr's was turned down, as in a sandbox"},
			{key: "s", label: "Settings", run: "s", desc: "every setting of the config file, and how each agent starts: model, arguments, variables"},
			{key: "p", label: "Send prefix to agent", run: "p", rare: true, desc: "the agent gets the prefix key itself, for a program that uses it"},
			{key: "q", label: "Quit", run: "q", rare: true, desc: "stop nerdr and its agents; the tabs are back next time"},
		}},
	}
}

// keyGroups are the commands two keys after the prefix: the group's key,
// then the command's. Each group's own key again runs its most used one,
// so the old single key is the same key twice; a group without an entry
// of its own key runs its first entry then (e e is F1).
func keyGroups() map[string]commandGroup {
	return map[string]commandGroup{
		"i": {"Git and GitHub", []commandEntry{
			{key: "i", label: "Branches, PR, push and conflicts", run: "i i", desc: "the tab's branches: changes, ahead/behind, PR and CI; push, open a PR"},
			{key: "p", label: "All my pull requests (F2)", run: "i p", desc: "yours across the repositories, or m: everyone's, those asking your review marked; enter goes to, or opens, the branch's tab"},
			{key: "b", label: "Open the PR in the browser", run: "i b", desc: "the tab's pull request on GitHub, in your browser"},
			{key: "o", label: "Open issues (F2)", run: "i o", desc: "the issues assigned to you, or all open (a); enter starts a tab for one"},
		}},
		"a": {"Agent", []commandEntry{
			{key: "a", label: "Focus agent", run: "a a", desc: "give the keyboard back to the agent"},
			{key: "n", label: "Note for yourself", run: "a n", desc: "a note for the tab, shown in the panel above the agent"},
			{key: "s", label: "Switch agent", run: "a s", desc: "carry on the work of the agent with the keyboard with another kind (claude, opencode, …)"},
			{key: "r", label: "Restart agent", run: "a r", desc: "restart the agent, continuing its conversation"},
			{key: "c", label: "Code review", run: "a c", desc: "another agent reviews this agent's work, in a pane beside it (as t r)"},
			{key: "x", label: "Stop code review", run: "a x", rare: true, desc: "stop the code review of this agent's work"},
		}},
		"e": {"Review panel", []commandEntry{
			{key: "1", label: "Code changes (F1)", run: "e 1", desc: "the tab's changes against main, file by file"},
			{key: "2", label: "GitHub (F2)", run: "e 2", desc: "the branch's pull request; again for all yours, then your issues"},
			{key: "3", label: "Tasks (F3)", run: "e 3", desc: "the tasks waiting for the agent: yours, and the review's findings"},
			{key: "4", label: "Skills (F4)", run: "e 4", desc: "nerdr's skills for the agents: which the agent has, and switch them on or off"},
			{key: "5", label: "Plugins (F5)", run: "e 5", desc: "the plugins there are for the tab: what each does, and the screen of one that has its own"},
		}},
		"t": {"Panes", []commandEntry{
			{key: "t", label: "New terminal", run: "t t", desc: "a terminal in the worktree, in a new pane, last"},
			{key: "a", label: "New agent…", run: "t a", desc: "another agent in the same worktree, in a new conversation, in a new pane, last"},
			{key: "r", label: "New code review", run: "t r", desc: "a second agent reviews the work of the agent whose pane has the keyboard; its findings go back to that agent"},
			{key: "x", label: "Close pane", run: "t x", desc: "stop what runs in the pane with the keyboard and close it; the tab's first agent stays as long as the tab"},
			{key: "m", label: "Minimise / show pane", run: "t m", desc: "fold the pane into a row over the others, or show it again"},
			{key: "←", label: "Pane left", run: "t ←", desc: "the keyboard to the pane on the left"},
			{key: "→", label: "Pane right", run: "t →", desc: "the keyboard to the pane on the right"},
			{key: "⇧←", label: "Move pane left", run: "t ⇧←", desc: "move the pane with the keyboard one place left; more arrows move it on"},
			{key: "⇧→", label: "Move pane right", run: "t ⇧→", desc: "move the pane with the keyboard one place right; more arrows move it on"},
		}},
		"z": {"View", []commandEntry{
			{key: "z", label: "Review full width", run: "z z", desc: "the review panel over the whole window, and back"},
			{key: "e", label: "Show / hide review panel", run: "z e", desc: "the panel beside the agent: changes, PR, tasks, skills"},
			{key: "k", label: "Small / open review panel", run: "z k", desc: "small, the panel opens on an F-key and folds again when the keyboard goes back to the agent; layout small in the settings for every start"},
			{key: "l", label: "Layouts", run: "z l", desc: "a sketch of each layout, with its key"},
			{key: "=", label: "Agent and review", run: "z =", desc: "agent and review panel side by side"},
			{key: "+", label: "Agent 75%", run: "z +", desc: "the agent with three quarters of the window"},
			{key: "a", label: "Agent alone", run: "z a", desc: "only the agent, no review panel"},
			{key: "v", label: "Two tabs / one tab", run: "z v", desc: "this tab on the right, another tab of its repository on the left; again for one"},
			{key: "s", label: "Swap sides", run: "z s", desc: "swap the two sides of the split"},
			{key: "d", label: "Tab bar details", run: "z d", desc: "show / hide the details in the tab bar: changes, PR, CI…"},
			{key: "b", label: "Show / hide repository row", run: "z b", desc: "the row of repositories above the tabs"},
			{key: "t", label: "Tabs on top / on the left", run: "z t", desc: "the repositories and their tabs as two rows on top, or two lists left of the agent; tab_bar in the settings for every start"},
			{key: "m", label: "Small / wide sidebar", run: "z m", desc: "the tabs on the left as their lights alone, or with their names; tab_bar = \"left-small\" in the settings for every start"},
		}},
	}
}

// allCommands is every command, a group's entries in its place, with the
// keys in full ("i p"): for the menu and the command palette.
func allCommands() []commandGroup {
	groups := keyGroups()
	var out []commandGroup
	for _, g := range commandGroups() {
		var entries []commandEntry
		for _, e := range g.entries {
			sub, ok := groups[e.run]
			if !ok {
				entries = append(entries, e)
				continue
			}
			for _, s := range sub.entries {
				s.key = e.run + " " + s.key
				entries = append(entries, s)
			}
		}
		out = append(out, commandGroup{g.title, entries})
	}
	return out
}

// groupCommand is the command key k runs in group g, if any.
func groupCommand(g, k string) (string, bool) {
	entries := keyGroups()[g].entries
	for _, e := range entries {
		if e.key == k {
			return e.run, true
		}
	}
	if k == g && len(entries) > 0 {
		return entries[0].run, true
	}
	return "", false
}

// isGroup is whether k, after the prefix, opens a group of commands.
func isGroup(k string) bool {
	_, ok := keyGroups()[k]
	return ok
}

// commandOff says why command run cannot do anything now, or "". z v
// needs a second tab in the active tab's repository.
func (m *Model) commandOff(run string) string {
	switch run {
	case "t r", "a c":
		return m.reviewOff()
	case "C":
		if tabRepo(m.cur()) == "" {
			return "a branch is stacked on a tab with one repository"
		}
	case "z v":
		if m.splitTab() == nil && m.neighbour(m.cur()) == nil {
			return "two tabs need a second tab in this repository"
		}
	}
	return ""
}
