package core

// Both the mouse menu and prefix help use this order and these group names.
// run is the keys after the prefix: one, or a group's key and the
// command's in it ("i p"). An entry whose run is a group's key opens the
// group. rare entries work and are in the menu and the palette but not in
// the hint line, which keeps to the keys most used. desc says what the
// command does, in the palette, which is all the help there is.
type CommandEntry struct {
	Key, Label, Run, Desc string
	Rare                  bool
}

type CommandGroup struct {
	Title   string
	Entries []CommandEntry
}

func CommandGroups() []CommandGroup {
	return []CommandGroup{
		{"Overview and navigation", []CommandEntry{
			{Key: "←", Label: "Tab left", Run: "left", Desc: "the previous tab, or repository on the repository row; the prefix stays on for more"},
			{Key: "→", Label: "Tab right", Run: "right", Desc: "the next tab, or repository on the repository row; the prefix stays on for more"},
			{Key: "↓", Label: "Worktree tab", Run: "down", Desc: "the arrows move along the tab row, among the repository's tabs"},
			{Key: "↑", Label: "Repository tab", Run: "up", Desc: "the arrows move along the repository row (shown with two or more)"},
			{Key: "]", Label: "Next repository", Run: "]", Rare: true, Desc: "the next repository's tab you were last in; the prefix stays on for more"},
			{Key: "[", Label: "Previous repository", Run: "[", Rare: true, Desc: "the previous repository's tab you were last in; the prefix stays on for more"},
			{Key: "tab", Label: "Next pane / review", Run: "tab", Desc: "move the keyboard to the tab's next pane, then the split's other side or the review panel"},
			{Key: "w", Label: "Agents waiting", Run: "w", Desc: "the tabs whose agent is done or asks something, the longest waiting first"},
			{Key: "n", Label: "Next agent waiting", Run: "n", Desc: "go to the tab that has waited longest for you"},
			{Key: "f", Label: "Find tab", Run: "f", Desc: "find a tab by typing part of its name, across repositories (space too)"},
			{Key: "1–9,0", Label: "Tab in group", Run: "1", Desc: "go to tab 1–9 of the repository; 0 is the last"},
			{Key: "h", Label: "Since you were here", Run: "h", Rare: true, Desc: "what happened in the tab while you were away: commits, PR, CI, the agent"},
		}},
		{"Tasks and tabs", []CommandEntry{
			{Key: "c", Label: "New tab", Run: "c", Desc: "pick repositories, branch and agent for a new tab"},
			{Key: "C", Label: "New tab on this branch", Run: "C", Rare: true, Desc: "a new branch on top of this tab's, for work that builds on it before it is merged; its pull request is against this one's"},
			{Key: "x", Label: "Close tab", Run: "x", Desc: "close the tab; asks whether to delete its worktree too"},
			{Key: "r", Label: "Rename tab", Run: "r", Desc: "give the tab a name of its own"},
			{Key: "⇧←", Label: "Move tab left", Run: "shift+left", Rare: true, Desc: "move the tab one place left in the tab row"},
			{Key: "⇧→", Label: "Move tab right", Run: "shift+right", Rare: true, Desc: "move the tab one place right in the tab row"},
		}},
		{"Agent and tasks", []CommandEntry{
			{Key: "a", Label: "Answer, note, switch, restart…", Run: "a"},
			{Key: "m", Label: "Task for agent", Run: "m", Desc: "what the agent should do next: sent by itself when the agent is idle, or kept in F3 until then"},
			{Key: "M", Label: "Task to several tabs", Run: "M", Desc: "one task for the agents of the tabs you tick"},
			{Key: "l", Label: "Saved prompts", Run: "l", Desc: "pick a prompt you keep for typing into agents again and again, such as \"commit, push and open a pull request\"; sent with enter, or put in"},
		}},
		{"Git and GitHub", []CommandEntry{
			{Key: "i", Label: "Branches, PRs, issues, review…", Run: "i"},
		}},
		{"View", []CommandEntry{
			{Key: "e", Label: "Review panel: show, screens…", Run: "e"},
			{Key: "z", Label: "Layouts, split, zoom…", Run: "z"},
			{Key: "<", Label: "Wider review / split", Run: "<", Rare: true, Desc: "make the review panel, or the right of the split, wider (, too)"},
			{Key: ">", Label: "Narrower review / split", Run: ">", Rare: true, Desc: "make the review panel, or the right of the split, narrower (. too)"},
		}},
		{"Panes", []CommandEntry{
			{Key: "t", Label: "Terminal, agent, review, panes…", Run: "t"},
		}},
		{"Work tools", []CommandEntry{
			{Key: "o", Label: "Open in editor", Run: "o", Desc: "the worktree in your editor: VS Code, unless worktree_editor says otherwise"},
			{Key: "u", Label: "Usage and rate limits", Run: "u", Desc: "how much of your Claude and Codex plans' limits is used, when they reset, and how full each agent's context is"},
			{Key: "/", Label: "Search conversations", Run: "/", Desc: "find what was said in the agents' conversations in these repositories and worktrees, and go to its tab or carry it on"},
			{Key: "b", Label: "Scroll back", Run: "b", Rare: true, Desc: "scroll back through what the agent wrote, with the arrows and PgUp/PgDn; q leaves"},
		}},
		{"Settings and help", []CommandEntry{
			{Key: "?", Label: "All commands", Run: "?", Desc: "this list: type part of a name or what it does to find it (: too)"},
			{Key: "g", Label: "git and gh calls", Run: "g", Rare: true, Desc: "how many times nerdr has run git and gh for each tab, and which commands, since it started; and what of nerdr's was turned down, as in a sandbox"},
			{Key: "s", Label: "Settings", Run: "s", Desc: "every setting of the config file, and how each agent starts: model, arguments, variables"},
			{Key: "p", Label: "Send prefix to agent", Run: "p", Rare: true, Desc: "the agent gets the prefix key itself, for a program that uses it"},
			{Key: "q", Label: "Quit", Run: "q", Rare: true, Desc: "stop nerdr and its agents; the tabs are back next time"},
		}},
	}
}

// KeyGroups are the commands two keys after the prefix: the group's key,
// then the command's. Each group's own key again runs its most used one,
// so the old single key is the same key twice; a group without an entry
// of its own key runs its first entry then (e e is F1).
func KeyGroups() map[string]CommandGroup {
	return map[string]CommandGroup{
		"i": {"Git and GitHub", []CommandEntry{
			{Key: "i", Label: "Branches, PR, push and conflicts", Run: "i i", Desc: "the tab's branches: changes, ahead/behind, PR and CI; push, open a PR"},
			{Key: "p", Label: "All my pull requests (F2)", Run: "i p", Desc: "yours across the repositories, or m: everyone's, those asking your review marked; enter goes to, or opens, the branch's tab"},
			{Key: "b", Label: "Open the PR in the browser", Run: "i b", Desc: "the tab's pull request on GitHub, in your browser"},
			{Key: "r", Label: "Repositories of the tab", Run: "i r", Desc: "add repositories to the tab, each a worktree on its branch, or take them away; its last one stays"},
			{Key: "o", Label: "Open issues (F2)", Run: "i o", Desc: "the issues assigned to you, or all open (a); enter starts a tab for one"},
		}},
		"a": {"Agent", []CommandEntry{
			{Key: "a", Label: "Focus agent", Run: "a a", Desc: "give the keyboard back to the agent"},
			{Key: "n", Label: "Note for yourself", Run: "a n", Desc: "a note for the tab, shown in the panel above the agent"},
			{Key: "h", Label: "What you sent the agent", Run: "a h", Desc: "what you sent the agent, the latest first, each in full when picked; a click on last: above the agent too"},
			{Key: "s", Label: "Switch agent", Run: "a s", Desc: "carry on the work of the agent with the keyboard with another kind (claude, opencode, …)"},
			{Key: "r", Label: "Restart agent", Run: "a r", Desc: "restart the agent, continuing its conversation"},
			{Key: "c", Label: "Code review", Run: "a c", Desc: "another agent reviews this agent's work, in a pane beside it (as t r)"},
			{Key: "x", Label: "Stop code review", Run: "a x", Rare: true, Desc: "stop the code review of this agent's work"},
		}},
		"e": {"Review panel", []CommandEntry{
			{Key: "1", Label: "Code changes (F1)", Run: "e 1", Desc: "the tab's changes against main, file by file"},
			{Key: "2", Label: "GitHub (F2)", Run: "e 2", Desc: "the branch's pull request; again for all yours, then your issues"},
			{Key: "3", Label: "Tasks (F3)", Run: "e 3", Desc: "the tasks waiting for the agent: yours, and the review's findings"},
			{Key: "4", Label: "Skills (F4)", Run: "e 4", Desc: "the agents' skills, nerdr's and others: which the agent has, how much they are used, and switch nerdr's on or off"},
			{Key: "5", Label: "Plugins (F5)", Run: "e 5", Desc: "the plugins there are for the tab: what each does, and the screen of one that has its own"},
		}},
		"t": {"Panes", []CommandEntry{
			{Key: "t", Label: "New terminal", Run: "t t", Desc: "a terminal in the worktree, in a new pane, last"},
			{Key: "a", Label: "New agent…", Run: "t a", Desc: "another agent in the same worktree, in a new conversation, in a new pane, last"},
			{Key: "r", Label: "New code review", Run: "t r", Desc: "a second agent reviews the work of the agent whose pane has the keyboard; its findings go back to that agent"},
			{Key: "x", Label: "Close pane", Run: "t x", Desc: "stop what runs in the pane with the keyboard and close it; the tab's first agent stays as long as the tab"},
			{Key: "m", Label: "Minimise / show pane", Run: "t m", Desc: "fold the pane into a row over the others, or show it again"},
			{Key: "←", Label: "Pane left", Run: "t ←", Desc: "the keyboard to the pane on the left"},
			{Key: "→", Label: "Pane right", Run: "t →", Desc: "the keyboard to the pane on the right"},
			{Key: "⇧←", Label: "Move pane left", Run: "t ⇧←", Desc: "move the pane with the keyboard one place left; more arrows move it on"},
			{Key: "⇧→", Label: "Move pane right", Run: "t ⇧→", Desc: "move the pane with the keyboard one place right; more arrows move it on"},
		}},
		"z": {"View", []CommandEntry{
			{Key: "z", Label: "Review full width", Run: "z z", Desc: "the review panel over the whole window, and back"},
			{Key: "e", Label: "Show / hide review panel", Run: "z e", Desc: "the panel beside the agent: changes, PR, tasks, skills"},
			{Key: "k", Label: "Small / open review panel", Run: "z k", Desc: "small, the panel opens on an F-key and folds again when the keyboard goes back to the agent; layout small in the settings for every start"},
			{Key: "l", Label: "Layouts", Run: "z l", Desc: "a sketch of each layout, with its key"},
			{Key: "=", Label: "Agent and review", Run: "z =", Desc: "agent and review panel side by side"},
			{Key: "+", Label: "Agent 75%", Run: "z +", Desc: "the agent with three quarters of the window"},
			{Key: "a", Label: "Agent alone", Run: "z a", Desc: "only the agent, no review panel"},
			{Key: "v", Label: "Two tabs / one tab", Run: "z v", Desc: "this tab on the right, another tab of its repository on the left; again for one"},
			{Key: "s", Label: "Swap sides", Run: "z s", Desc: "swap the two sides of the split"},
			{Key: "d", Label: "Tab bar details", Run: "z d", Desc: "show / hide the details in the tab bar: changes, PR, CI…"},
			{Key: "b", Label: "Show / hide repository row", Run: "z b", Desc: "the row of repositories above the tabs"},
			{Key: "t", Label: "Tabs on top / on the left", Run: "z t", Desc: "the repositories and their tabs as two rows on top, or two lists left of the agent; tab_bar in the settings for every start"},
			{Key: "m", Label: "Small / wide sidebar", Run: "z m", Desc: "the tabs on the left as their lights alone, or with their names; tab_bar = \"left-small\" in the settings for every start"},
		}},
	}
}

// AllCommands is every command, a group's entries in its place, with the
// keys in full ("i p"): for the menu and the command palette.
func AllCommands() []CommandGroup {
	groups := KeyGroups()
	var out []CommandGroup
	for _, g := range CommandGroups() {
		var entries []CommandEntry
		for _, e := range g.Entries {
			sub, ok := groups[e.Run]
			if !ok {
				entries = append(entries, e)
				continue
			}
			for _, s := range sub.Entries {
				s.Key = e.Run + " " + s.Key
				entries = append(entries, s)
			}
		}
		out = append(out, CommandGroup{g.Title, entries})
	}
	return out
}

// GroupCommand is the command key k runs in group g, if any.
func GroupCommand(g, k string) (string, bool) {
	entries := KeyGroups()[g].Entries
	for _, e := range entries {
		if e.Key == k {
			return e.Run, true
		}
	}
	if k == g && len(entries) > 0 {
		return entries[0].Run, true
	}
	return "", false
}

// IsGroup is whether k, after the prefix, opens a group of commands.
func IsGroup(k string) bool {
	_, ok := KeyGroups()[k]
	return ok
}
