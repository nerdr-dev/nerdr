package app

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/editor"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The settings dialog (the prefix + p): every setting of the config file,
// docs/src/content/docs/configure/configuration.mdx's, and how each agent is started. A change is
// written to the file at once, the rest of it left as it was, and holds
// from then on, or from nerdr's next start for the few it reads only then.

// settingKind is how a setting is changed.
type settingKind int

const (
	sBool   settingKind = iota // on and off
	sChoice                    // one of choices
	sInt                       // a number from lo to hi
	sText                      // a string; choices are suggestions
	sList                      // an array of strings, typed as words
	sEnv                       // NAME=value words
)

// settingRow is one line of the dialog: a setting, or a heading when key
// is empty.
type settingRow struct {
	table, key string
	label      string
	kind       settingKind
	choices    []string
	// notes say what each choice means, shown after it in its line.
	notes  map[string]string
	lo, hi int
	def    string // the default, as the file would have it
	// desc says what the setting does, in plain words; a line break
	// starts a new line, one for each choice, say.
	desc string
	// example is the setting as a line of the config file.
	example string
	// restart is set for a setting nerdr reads only when it starts.
	restart bool
}

func (r settingRow) heading() bool { return r.key == "" }

// settings is the dialog's state.
type settings struct {
	rows   []settingRow
	raw    map[string]any
	cursor int
	top    int
	// editing is set while a value is typed in input.
	editing bool
	input   textinput.Model
	err     string
	// about is set while the picked setting's description is shown, over
	// its line; picking another hides it.
	about bool
}

func (m *Model) openSettings() tea.Cmd { return m.openSettingsAt("") }

// openSettingsAt opens the dialog with the cursor on the setting key of
// the top level, or on the first setting when there is none such.
func (m *Model) openSettingsAt(key string) tea.Cmd {
	d := &settings{rows: m.settingRows()}
	d.reload()
	d.cursor = d.next(-1, 1)
	for i, r := range d.rows {
		if key != "" && r.table == "" && r.key == key {
			d.cursor = i
		}
	}
	m.dialog = d
	return nil
}

// layoutNotes say what layout's choices look like.
var layoutNotes = map[string]string{
	"normal": "agent | F-menu",
	"wide":   "agent 75% | F-menu",
	"small":  "agent | F-keys, opened for a look",
	"agent":  "agent | F-keys",
	"panel":  "F-menu alone",
}

// settingRows are the dialog's lines: nerdr's settings in groups, the
// review's, then each agent that can be started.
func (m *Model) settingRows() []settingRow {
	b := func(key, def, desc, example string) settingRow {
		return settingRow{key: key, kind: sBool, def: def, desc: desc, example: example}
	}
	t := func(key, desc, example string) settingRow {
		return settingRow{key: key, kind: sText, desc: desc, example: example}
	}
	rows := []settingRow{
		{label: "Keys and mouse"},
		{key: "prefix", kind: sText, def: `"ctrl+q"`, restart: true,
			desc: "The key you press before every nerdr command. Press it, let go, then press the command's key: with ctrl+q, ctrl+q and then t opens a shell in the tab's worktree.\n" +
				"Any ctrl+<letter> works. Pick one your agents do not use themselves, or they never get it; herdr uses ctrl+b.",
			example: `prefix = "ctrl+a"`},
		{key: "mouse", kind: sBool, def: "true", restart: true,
			desc: "Lets you use the mouse in nerdr: click a tab to go to it, click menus and buttons, scroll with the wheel and drag to select text.\n" +
				"Turn it off if you want your terminal to handle the mouse the way it does outside nerdr.",
			example: `mouse = false`},
		b("function_keys", "true",
			"Whether the review panel's screens are called F1 to F5 in its menu and hints, and opened with those keys.\n"+
				"Turn it off if your F-keys are hard to reach, as on a Mac laptop, where they change brightness and volume unless you hold fn. The menu and hints then show the prefix keys that do the same: the prefix and e 1 for F1, up to e 4 for F4.",
			`function_keys = false`),

		{label: "Screen"},
		{key: "layout", kind: sChoice, choices: []string{"normal", "wide", "small", "agent", "panel"}, def: `"normal"`, notes: layoutNotes, restart: true,
			desc: "How the window is split between the agent and the F-menu (the review panel's screens F1-F5) when nerdr starts. The column of F-keys on the right is always there; the prefix and z l, or z k, change the rest while nerdr runs.\n" +
				"normal: the agent on the left, the F-menu open beside the column, as wide as panel_width.\n" +
				"wide: the same, but the agent gets 75% of the width.\n" +
				"small: the F-menu closed. An F-key opens it, and it folds again when you go back to the agent.\n" +
				"agent: the F-menu closed until an F-key opens it, and open until you close it.\n" +
				"panel: the F-menu alone, over the whole window.",
			example: `layout = "wide"`},
		{key: "panel_width", kind: sInt, lo: 10, hi: 89, def: "50",
			desc: "How much of the window the F-menu takes when it is beside the agent, in percent. The agent gets the rest.\n" +
				"50 splits the window in two halves; 35 gives the agent about two thirds.",
			example: `panel_width = 35`},
		b("panel_labels", "true",
			"Shows a short name beside each F-key in the column on the right: F1 changes, F2 github, F3 tasks, F4 skills.\n"+
				"Turn it off for a narrower column with the F-keys alone; resting the pointer on one still says what it is.",
			`panel_labels = false`),
		{key: "pane_min_width", kind: sInt, lo: 20, hi: 400, def: "60",
			desc: "The narrowest, in columns, the panes of a tab are shown side by side: its agent, and the terminals, agents and reviews opened beside it.\n" +
				"When there are more panes than fit at this width, the F-menu kept open is made small first, and opens again once they fit beside it. Past that, the ones you used longest ago are minimised into the row above them. Click one there to bring it back; the one you are in is always shown.",
			example: `pane_min_width = 80`},
		{key: "tab_bar", kind: sChoice, choices: []string{"top", "left", "left-small"}, def: `"left"`,
			desc: "Where the repositories and their tabs are shown. The prefix and z t switch between top and left while nerdr runs, z m between left and left-small.\n" +
				"top: two rows above the agent, the repositories in the first and the active repository's tabs in the second.\n" +
				"left: two lists to the left of the agent, the repositories above the active repository's tabs.\n" +
				"left-small: the same lists made narrow, showing only each tab's status light, so the agent gets more room. Click a light to go to that tab; rest the pointer on it to see its name.",
			example: `tab_bar = "top"`},
		b("tab_details", "true",
			"Shows more about each tab next to its name: how many lines are added and removed, whether it has a pull request, and whether its CI checks pass.\n"+
				"Turn it off for a quieter tab list with names and status lights only.",
			`tab_details = false`),
		{key: "diff_colors", kind: sChoice, choices: []string{"nerdr", "github"}, def: `"nerdr"`,
			desc: "The colours F1 shows changed lines in.\n" +
				"nerdr: soft tints that are easy on the eyes.\n" +
				"github: the dark colours of GitHub's diffs, with code coloured by its syntax.\n" +
				"c in F1 switches between them.",
			example: `diff_colors = "github"`},
		{key: "status_lights", kind: sChoice, choices: []string{"dots", "symbols"}, def: `"dots"`,
			desc: "How the status lights of tabs and agents are drawn.\n" +
				"dots: coloured circles, yellow ● working, red ● needs input, green ● done, ○ idle.\n" +
				"symbols: a shape of its own for each, in the same colours: ◐ working, × needs input, ✓ done, ○ idle. For telling them apart without the colours.",
			example: `status_lights = "symbols"`},
		{key: "fps", kind: sInt, lo: 1, hi: 120, def: "60", restart: true,
			desc: "The most times a second nerdr redraws the screen, from 1 to 120.\n" +
				"A lower number uses less CPU: at 30, an idle nerdr uses about a third less. What you type and what the agent writes can then show up to one frame later, about 33 ms at 30. Above 60 changes little, since agents' output is drawn at most about 60 times a second.",
			example: `fps = 30`},

		{label: "Folders"},
		{key: "repo_dirs", kind: sList,
			desc: "More folders where nerdr looks for repositories, besides the folder you start it in. Each repository found there can get tabs, just like those in the start folder.\n" +
				"Type the folders with a space between them; put a folder with spaces in its name in \"quotes\". ~/ is your home folder.",
			example: `repo_dirs = ["~/code", "~/work"]`},
		{key: "worktrees_dir", kind: sText, def: `"` + config.DefaultWorktreesDir + `"`, restart: true,
			desc: "The folder where nerdr makes a new worktree for each new tab: a checkout of the tab's branch, where its agent works.\n" +
				"~/ is your home folder. A relative path is taken from the folder you start nerdr in.",
			example: `worktrees_dir = "~/code/worktrees"`},
		{key: "data_dir", kind: sText, restart: true,
			desc: "Where nerdr keeps what agents write down about a change: explanations of the code (nerdr-explain's notes and the role of each file), code reviews and handoffs to another agent.\n" +
				"Empty: in the repository's .git/nerdr. Shared by all its worktrees, never committed, and kept when a worktree is deleted.\n" +
				"A relative path, such as .nerdr: a folder inside each worktree, kept out of git. Use this when the agent runs in a sandbox that can only reach its worktree. It is deleted with the worktree.\n" +
				"An absolute path, such as ~/nerdr-data: one folder for every repository, with a folder inside it for each.",
			example: `data_dir = ".nerdr"`},
		{key: "install", kind: sChoice, choices: []string{"home", "worktree", "off"}, restart: true,
			desc: "Where nerdr may install what it gives the agents: its skills, instructions that teach agents to explain their code, ask you questions, hand work over and more, and the plugins and hooks that tell nerdr when an agent is working or done. Which skills are on is chosen in F4. Older config files call this setting skills.\n" +
				"home: in the agents' own folders in your home folder, such as ~/.claude/skills. Agents you run outside nerdr see them too.\n" +
				"worktree: in each worktree, kept out of git. Use this when the agent runs in a sandbox that cannot reach your home folder.\n" +
				"off: nerdr installs nothing anywhere. Copy the skills yourself if you want them.",
			example: `install = "worktree"`},

		{label: "Tabs"},
		{key: "max_tabs", kind: sInt, lo: 0, hi: 999, def: "20",
			desc: "The most tabs that can be open at once. When this many are open, nerdr will not open another until you close one. It keeps too many agents from running at the same time.\n" +
				"0 means no limit.",
			example: `max_tabs = 10`},
		{key: "cold_days", kind: sInt, lo: 0, hi: 365, def: "5",
			desc: "After this many days with nothing done in a tab, by you or its agent, nerdr marks it as gone cold and lists it among the tabs that want a look. It reminds you of work you may have forgotten, to finish it or close the tab.\n" +
				"0 means tabs never go cold.",
			example: `cold_days = 14`},
		{key: "sleep_after", kind: sInt, lo: 0, hi: 1440, def: "0",
			desc: "After this many minutes with a tab's agents done, looked at and the tab out of sight, nerdr stops them, to save the memory and processes they hold. Their light shows z. When you go to the tab, they carry their conversations on, as a restart does; it takes a few seconds.\n" +
				"A tab is left running while it has a task waiting to be sent, a question, a code review going on, or an agent that would start a new conversation. Terminals beside the agent keep running. Tabs gone cold sleep from the start, whatever this says.\n" +
				"0 means agents are never put to sleep.",
			example: `sleep_after = 60`},
		b("close_merged", "true",
			"When a tab's pull request is merged, its panel says so: the work is in main, and x closes the tab. The tab is never closed for you.\n"+
				"Turn it off if you do not want that line.",
			`close_merged = false`),

		{label: "Notifications"},
		{key: "notify", kind: sChoice, choices: []string{"all", "blocked", "done", "off"}, def: `"all"`,
			desc: "When a tab tells you it needs you. While nerdr's window is not in front, you get a desktop notification; while you are in another tab, a message in the bar.\n" +
				"all: when an agent asks you something and when it has finished.\n" +
				"blocked: only when an agent asks you something and is waiting for an answer.\n" +
				"done: only when an agent has finished.\n" +
				"off: never.",
			example: `notify = "blocked"`},
		b("pr_notify", "true",
			"Tells you when a CI check fails on one of your open pull requests, or when someone reviews one. nerdr asks GitHub through gh.\n"+
				"Turn it off and GitHub is not asked.",
			`pr_notify = false`),
		b("issue_notify", "true",
			"Tells you when an issue on GitHub is newly assigned to you. nerdr asks GitHub through gh.\n"+
				"Turn it off and GitHub is not asked.",
			`issue_notify = false`),
		b("claude_status_line", "true",
			"Starts claude with nerdr's status line, which tells nerdr how much of your Claude plan's rate limits is used, for the usage dialog. It then shows your own status line, if you have set one in claude's settings, or a short one of nerdr's.\n"+
				"Turn it off to leave claude's status line alone; the usage dialog then has codex's limits only.",
			`claude_status_line = false`),
		b("herdr_focus_jump", "true",
			"Only when nerdr runs inside herdr: when herdr moves you to nerdr's pane, nerdr goes straight to the tab that needs you.\n"+
				"Turn it off to stay in the tab you were in.",
			`herdr_focus_jump = false`),
		{key: "github_refresh", kind: sInt, lo: 15, hi: 3600, def: "90",
			desc: "How many seconds apart nerdr asks GitHub about the pull request of the tab on screen, and of a tab whose agent is working: its CI checks, reviews and whether it is merged.\n" +
				"nerdr's other questions to GitHub follow along, in the same proportion: at 90, tabs out of sight are asked about every 10 minutes, the lists of pull requests and issues in F2 and the watch on your pull requests for notifications every 2 minutes, and new issues assigned to you every 5 minutes. At 180, all of them are twice as far apart.\n" +
				"A lower number shows a finished CI run sooner; a higher one uses less of GitHub's limit on requests, which matters with many tabs.",
			example: `github_refresh = 180`},

		{label: "Programs"},
		t("shell",
			"The shell a terminal pane runs in the tab's worktree: the prefix and t t, or + terminal, open one. Empty: the one in $SHELL.",
			`shell = "/bin/zsh"`),
		t("browser",
			"The program that opens a pull request or a comment on GitHub. nerdr adds the web address at the end. Empty: the one in $BROWSER, or else the system's own.",
			`browser = "open -a Safari"`),
		t("editor",
			"The program that o in the review panel opens a file in. Empty: the one in $VISUAL, else $EDITOR, else vim.",
			`editor = "nvim"`),
		t("worktree_editor",
			"The program that the prefix and o open the tab's whole worktree in. Empty: code (Visual Studio Code).",
			`worktree_editor = "cursor"`),

		{label: "Keep awake"},
		{key: "keep_awake", kind: sChoice, choices: []string{"busy", "always", "off"}, def: `"busy"`,
			desc: "Keeps the machine from going to sleep, since agents stop when it sleeps. A locked screen does not stop them.\n" +
				"busy: while an agent or reviewer works, and a minute after, so messages waiting for it get through.\n" +
				"always: as long as nerdr runs.\n" +
				"off: never; the machine sleeps as it is set to.",
			example: `keep_awake = "always"`},
		b("keep_screen_on", "false",
			"Also keeps the screen on and unlocked while the machine is kept awake, so you can follow the agents from across the room. It stops keep_screen_max minutes after your last key or click in nerdr.",
			`keep_screen_on = true`),
		{key: "keep_screen_max", kind: sInt, lo: 1, hi: 1440, def: "60",
			desc:    "With keep_screen_on: how many minutes after your last key or click in nerdr the screen is kept on. After that, the screen turns off and locks as your desktop is set to, while the machine stays awake. Your next key or click starts the count again.",
			example: `keep_screen_max = 120`},

		{label: "Plugins"},
		b("confirm_plugin_runs", "false",
			"Asks you before every run of a plugin an agent wants, showing the values it gives it: y runs it, n does not. Unanswered in about a minute and a half, it does not run.\n"+
				"Off, plugins you have allowed run when an agent asks, but for a plugin whose plugin.toml says confirm = true.",
			`confirm_plugin_runs = true`),

		{label: "Code review"},
		{table: "review", key: "auto", kind: sBool, def: "false",
			desc: "A second agent reviews the work by itself, without you asking, when the tab's agent has changed files. when says at which moment.\n" +
				"Off, a review starts only when you ask for one, with the prefix and t r in the agent's pane.",
			example: `auto = true`},
		{table: "review", key: "when", kind: sChoice, choices: []string{"ready", "turn"}, def: `"ready"`,
			desc: "With auto on: when the review starts.\n" +
				"ready: when the agent says the work is ready to be looked at. This skips reviews of half-done work.\n" +
				"turn: every time the agent finishes a turn in which it changed files.",
			example: `when = "turn"`},
		{table: "review", key: "agent", kind: sChoice, choices: m.reviewAgents(), def: `"other"`,
			desc: "Which agent does the review.\n" +
				"other: an agent of another kind than the one that did the work, for a fresh pair of eyes: Codex reviews Claude, and Claude reviews the rest.\n" +
				"Or name one, and that agent always reviews.",
			example: `agent = "codex"`},
		{table: "review", key: "rounds", kind: sInt, lo: 1, hi: maxRounds, def: "2",
			desc:    "How many times the same work is reviewed. After this many rounds of review and fixes, nerdr stops and leaves the rest to you, so the agents do not go on forever.",
			example: `rounds = 3`},
		{table: "review", key: "send_findings", kind: sChoice, choices: []string{"ask", "auto"}, def: `"ask"`,
			desc: "What happens to what the reviewer finds.\n" +
				"ask: it waits in F3 until you have read it and choose what to send to the agent.\n" +
				"auto: it goes straight to the agent, which starts fixing it.",
			example: `send_findings = "auto"`},
	}
	for _, k := range m.settingAgents() {
		table := "agents." + string(k)
		rows = append(rows, settingRow{label: "Agent: " + string(k)})
		if k == agents.OpenCode {
			rows = append(rows, settingRow{table: table, key: "config_model", kind: sText,
				desc:    "The model opencode uses, written as provider/model. nerdr puts it in opencode's OPENCODE_CONFIG_CONTENT and runs it with --standalone, since opencode 2 has no --model. Empty: opencode's own default.",
				example: `config_model = "anthropic/claude-opus-5-5"`})
		}
		rows = append(rows,
			settingRow{table: table, key: "args", kind: sList,
				desc: "Arguments added every time the agent starts, in a new tab or when a conversation is resumed. Use the agent's own options, such as which model it uses.\n" +
					"Type them with a space between; put one with spaces in \"quotes\".",
				example: `args = ["--model", "opus"]`},
			settingRow{table: table, key: "env", kind: sEnv,
				desc:    "Environment variables the agent gets, on top of those nerdr has. Type them as NAME=value with a space between. NERDR_ and HERDR_ variables are nerdr's own and cannot be set here.",
				example: `env = { HTTPS_PROXY = "http://proxy:8080" }`},
			settingRow{table: table, key: "command", kind: sList, def: fmt.Sprintf("%q", agents.DefaultSpec(k).Command),
				desc:    "The program that starts the agent. Change it to run the agent through a wrapper, such as a sandbox, or a copy installed elsewhere. The first word must be a program on your PATH. nerdr adds args, and then its own arguments for the conversation, at the end; a wrapper must pass them on.",
				example: fmt.Sprintf(`command = ["my-sandbox", "--", %q]`, string(k))},
			settingRow{table: table, key: "sandboxed", kind: sBool, def: "false",
				desc: "Say that command runs the agent in a sandbox, so nerdr is not its way out: " +
					"nerdr does not open terminals for it, start agents or tabs that are not sandboxed, pass its messages to them, or let it read their panes or the other tabs.\n" +
					"Reviews of its work also run only in sandboxed agents.\n" +
					"It takes effect when the agent next starts. It does not make a sandbox: command must.",
				example: `sandboxed = true`},
			settingRow{table: "review.args", key: string(k), label: "review args", kind: sList,
				desc:    "Arguments added when this agent reviews another's work, after its args. Reviewers are told not to change files; this is where you make sure they cannot, with the agent's own read-only mode.",
				example: reviewArgsExample(k)},
		)
		if agents.TakesModel(k) {
			rows = append(rows, settingRow{table: "review.models", key: string(k), label: "review model", kind: sText,
				desc: "The model this agent reviews with, in its own names; empty: the one it starts with otherwise. " +
					"A model picked in the review pane (m), or given to nerdr request-review, goes for that review instead.",
				example: string(k) + ` = "` + reviewModelExample(k) + `"`})
		}
	}
	return rows
}

// reviewArgsExample is review args' example for agent k: its own
// read-only mode, where nerdr knows it.
func reviewArgsExample(k agents.Kind) string {
	switch k {
	case agents.Codex:
		return `codex = ["--sandbox", "read-only"]`
	case agents.Claude:
		return `claude = ["--permission-mode", "plan"]`
	}
	return ""
}

// reviewModelExample is review model's example for agent k.
func reviewModelExample(k agents.Kind) string {
	switch k {
	case agents.Claude:
		return "opus"
	case agents.OpenCode:
		return "anthropic/claude-opus-5-5"
	}
	return "a-model"
}

// settingAgents are the agents the dialog has a part for: those nerdr
// knows, the installed first, and those of the config's own.
func (m *Model) settingAgents() []agents.Kind {
	out := agents.Available(m.cfg.Agents)
	for _, k := range agents.All {
		if !contains(out, k) {
			out = append(out, k)
		}
	}
	var own []agents.Kind
	for k := range m.cfg.Agents {
		if !contains(out, k) {
			own = append(own, k)
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i] < own[j] })
	return append(out, own...)
}

// reviewAgents are review.agent's choices.
func (m *Model) reviewAgents() []string {
	out := []string{config.ReviewOther}
	for _, k := range agents.Available(m.cfg.Agents) {
		out = append(out, string(k))
	}
	return out
}

func (d *settings) reload() {
	raw, err := config.Raw()
	d.raw, d.err = raw, ""
	if err != nil {
		d.err = err.Error()
	}
}

// value is what the file sets for r, and whether it sets it.
func (d *settings) value(r settingRow) (any, bool) {
	cur := d.raw
	if r.table != "" {
		for _, part := range strings.Split(r.table, ".") {
			next, ok := cur[part].(map[string]any)
			if !ok {
				return nil, false
			}
			cur = next
		}
	}
	v, ok := cur[r.key]
	return v, ok
}

// text is r's value as it is typed: words for a list, NAME=value for
// variables.
func (d *settings) text(r settingRow) string {
	v, ok := d.value(r)
	if !ok {
		return ""
	}
	switch v := v.(type) {
	case string:
		return v
	case []any:
		var words []string
		for _, w := range v {
			words = append(words, quoteWord(fmt.Sprint(w)))
		}
		return strings.Join(words, " ")
	case map[string]any:
		var names []string
		for k := range v {
			names = append(names, k)
		}
		sort.Strings(names)
		var words []string
		for _, k := range names {
			words = append(words, quoteWord(k+"="+fmt.Sprint(v[k])))
		}
		return strings.Join(words, " ")
	}
	return fmt.Sprint(v)
}

// current is r's value, the file's or the default, as text.
func (d *settings) current(r settingRow) string {
	if _, ok := d.value(r); ok {
		return d.text(r)
	}
	if s, err := strconv.Unquote(r.def); err == nil {
		return s
	}
	return r.def
}

func (d *settings) next(from, step int) int {
	for i := from + step; i >= 0 && i < len(d.rows); i += step {
		if !d.rows[i].heading() {
			return i
		}
	}
	if from < 0 {
		return 0
	}
	return from
}

func (d *settings) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	if d.editing {
		switch msg.String() {
		case "esc":
			d.editing = false
			return nil
		case "enter":
			d.editing = false
			return m.saveSetting(d, d.rows[d.cursor], d.input.Value())
		}
		var cmd tea.Cmd
		d.input, cmd = d.input.Update(msg)
		return cmd
	}
	r := d.rows[d.cursor]
	from := d.cursor
	defer func() {
		if d.cursor != from {
			d.about = false
		}
	}()
	switch msg.String() {
	case "?", "i":
		d.about = !d.about
	case "esc", "q", "ctrl+c":
		m.dialog = nil
	case "up", "k":
		d.cursor = d.next(d.cursor, -1)
	case "down", "j":
		d.cursor = d.next(d.cursor, 1)
	case "pgup":
		for range 10 {
			d.cursor = d.next(d.cursor, -1)
		}
	case "pgdown":
		for range 10 {
			d.cursor = d.next(d.cursor, 1)
		}
	case "home", "g":
		d.cursor = d.next(-1, 1)
	case "end", "G":
		d.cursor = d.next(len(d.rows), -1)
	case "d", "delete", "backspace":
		return m.saveSetting(d, r, "")
	case "left", "-":
		return m.stepSetting(d, r, -1)
	case "right", "+", "=":
		return m.stepSetting(d, r, 1)
	case "enter", " ":
		switch r.kind {
		case sBool, sChoice:
			return m.stepSetting(d, r, 1)
		}
		d.edit(r)
		return textinput.Blink
	}
	return nil
}

// edit starts typing r's value.
func (d *settings) edit(r settingRow) {
	in := textinput.New()
	in.Prompt = ""
	in.SetValue(d.text(r))
	if r.def != "" {
		in.Placeholder = d.current(settingRow{def: r.def})
	}
	in.Focus()
	in.CursorEnd()
	d.input, d.editing = in, true
}

// stepSetting turns r on or off, or to the next (step 1) or previous of its
// choices, or its number up or down.
func (m *Model) stepSetting(d *settings, r settingRow, step int) tea.Cmd {
	cur := d.current(r)
	switch r.kind {
	case sBool:
		on := cur == "true"
		return m.saveSetting(d, r, strconv.FormatBool(!on))
	case sInt:
		n, _ := strconv.Atoi(cur)
		return m.saveSetting(d, r, strconv.Itoa(min(max(n+step, r.lo), r.hi)))
	case sChoice, sText:
		if len(r.choices) == 0 {
			return nil
		}
		i := 0
		for j, c := range r.choices {
			if c == cur {
				i = j
			}
		}
		return m.saveSetting(d, r, r.choices[(i+step+len(r.choices))%len(r.choices)])
	}
	return nil
}

// saveSetting writes text as r's value to the config file, "" removing it
// so the default holds, and takes the file in.
func (m *Model) saveSetting(d *settings, r settingRow, text string) tea.Cmd {
	value, err := tomlValue(r, strings.TrimSpace(text))
	if err != nil {
		d.err = r.key + ": " + err.Error()
		return nil
	}
	if err := config.SetKey(r.table, r.key, value); err != nil {
		d.err = r.key + ": not saved: " + err.Error()
		return nil
	}
	d.reload()
	return m.applySettings(r)
}

// tomlValue is text, as typed for r, as the config file writes it; ""
// for none.
func tomlValue(r settingRow, text string) (string, error) {
	if text == "" {
		return "", nil
	}
	switch r.kind {
	case sBool:
		b, err := strconv.ParseBool(text)
		if err != nil {
			return "", fmt.Errorf("true or false")
		}
		return strconv.FormatBool(b), nil
	case sInt:
		n, err := strconv.Atoi(text)
		if err != nil || n < r.lo || n > r.hi {
			return "", fmt.Errorf("a number from %d to %d", r.lo, r.hi)
		}
		return strconv.Itoa(n), nil
	case sList:
		words, err := splitWords(text)
		if err != nil {
			return "", err
		}
		return config.List(words), nil
	case sEnv:
		words, err := splitWords(text)
		if err != nil {
			return "", err
		}
		env := map[string]string{}
		for _, w := range words {
			name, value, ok := strings.Cut(w, "=")
			if !ok || name == "" {
				return "", fmt.Errorf("%q is not NAME=value", w)
			}
			env[name] = value
		}
		return config.Table(env), nil
	}
	return config.Quote(text), nil
}

// applySettings takes in the config file after r was changed in it.
func (m *Model) applySettings(settingRow) tea.Cmd { return m.reloadConfig() }

// restartSettings are what nerdr reads only when it starts, each as a
// config has it: they keep their value until then. The prefix is the key
// the terminal's input is split by, say.
var restartSettings = []struct {
	key string
	get func(config.Config) string
}{
	{"prefix", func(c config.Config) string { return c.Prefix }},
	{"worktrees_dir", func(c config.Config) string { return c.WorktreesDir }},
	{"layout", func(c config.Config) string { return c.Layout }},
	{"mouse", func(c config.Config) string { return fmt.Sprint(c.Mouse != nil && *c.Mouse) }},
	{"install", func(c config.Config) string { return c.Install }},
	{"data_dir", func(c config.Config) string { return c.DataDir }},
	{"fps", func(c config.Config) string { return strconv.Itoa(c.FPS) }},
}

// reloadConfig takes in the config file, however it was changed. What
// restartSettings has keeps its value, and a toast says which of those
// changed, once for each new value.
func (m *Model) reloadConfig() tea.Cmd {
	c, err := config.Load()
	if err != nil {
		m.toast(err.Error())
		return nil
	}
	old := m.cfg
	var pending []string
	if m.restartNoted == nil {
		m.restartNoted = map[string]string{}
	}
	for _, rs := range restartSettings {
		v := rs.get(c)
		switch {
		case v == rs.get(old):
			delete(m.restartNoted, rs.key)
		case m.restartNoted[rs.key] != v:
			m.restartNoted[rs.key] = v
			pending = append(pending, rs.key)
		}
	}
	c.Prefix, c.WorktreesDir, c.Layout, c.Mouse = old.Prefix, old.WorktreesDir, old.Layout, old.Mouse
	c.Install, c.DataDir, c.EnabledSkills, c.FPS = old.Install, old.DataDir, old.EnabledSkills, old.FPS
	m.cfg = c
	browser.Setting = c.Browser
	lightSymbols = c.StatusLights == "symbols"
	editor.File, editor.Worktree = c.Editor, c.WorktreeEditor
	switch len(pending) {
	case 0:
	case 1:
		m.toast(pending[0] + " changed: restart nerdr to use it")
	default:
		m.toast(strings.Join(pending, ", ") + " changed: restart nerdr to use them")
	}
	// Only what changed in the file: the panel's width and the details
	// are changed in nerdr too, and a reload must not undo that.
	if old.TabDetails == nil || *c.TabDetails != *old.TabDetails {
		m.showDetails = *c.TabDetails
	}
	resize := false
	if c.TabBar != old.TabBar {
		m.sideBar, m.sideSmall = strings.HasPrefix(c.TabBar, "left"), c.TabBar == "left-small"
		resize = true
	}
	if old.FunctionKeys == nil || *c.FunctionKeys != *old.FunctionKeys {
		m.review = m.review.SetScreenKeys(m.screenMenuKeys())
	}
	if c.GitHubRefresh != old.GitHubRefresh {
		m.review = m.review.SetPRRefresh(m.githubEvery(prPanelEvery))
	}
	var colors tea.Cmd
	if c.DiffColors != old.DiffColors {
		m.review, colors = m.review.SetDiffColors(c.DiffColors)
	}
	if c.PanelWidth != old.PanelWidth {
		m.reviewWidth = c.PanelWidth
		resize = true
	}
	if c.PaneMinWidth != old.PaneMinWidth || isOn(c.PanelLabels) != isOn(old.PanelLabels) {
		resize = true
	}
	if resize {
		return tea.Batch(colors, m.resize())
	}
	return colors
}

// configCheck takes in the config file when it has changed since the
// last look: edited by hand, or by nerdr itself, which reloading again
// does no harm.
func (m *Model) configCheck() tea.Cmd {
	st, err := os.Stat(config.Path())
	if err != nil {
		return nil
	}
	if st.ModTime().Equal(m.cfgMod) {
		return nil
	}
	m.cfgMod = st.ModTime()
	return m.reloadConfig()
}

func (d *settings) view(m *Model, w, h int) string {
	inner := min(max(w-8, 40), 110)
	keyW := 0
	for _, r := range d.rows {
		keyW = max(keyW, lipgloss.Width(rowName(r)))
	}
	foot := d.footer(m, inner)
	var about []string
	if d.about {
		about = d.aboutLines(inner)
	}
	listH := max(h-len(foot)-6, 3)
	rowsH := max(listH-len(about), 1)
	// Two lines of what is around the cursor stay in view, where there are.
	margin := min(2, (rowsH-1)/2)
	if d.cursor-margin < d.top {
		d.top = max(d.cursor-margin, 0)
	}
	if d.cursor+margin >= d.top+rowsH {
		d.top = min(d.cursor+margin-rowsH+1, max(len(d.rows)-rowsH, 0))
	}
	var lines []string
	for i := d.top; i < len(d.rows) && i < d.top+rowsH; i++ {
		r := d.rows[i]
		if i == d.cursor {
			lines = append(lines, about...)
		}
		if r.heading() {
			lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("110")).Render(r.label))
			continue
		}
		mark := "  "
		if i == d.cursor {
			mark = keyHint("▸ ")
		}
		name := rowName(r)
		val := d.shown(r)
		if i == d.cursor && d.editing {
			d.input.Width = max(inner-keyW-6, 10)
			val = d.input.View()
		}
		line := mark + name + strings.Repeat(" ", keyW-lipgloss.Width(name)+2) + val
		line = ansi.Truncate(line, inner, "…")
		i := i
		lines = append(lines, m.onClick(line, func(m *Model) tea.Cmd {
			if d.editing {
				return nil
			}
			if d.cursor == i {
				return d.key(m, keyMsg("enter"))
			}
			d.cursor, d.about = i, false
			return nil
		}))
	}
	// A blank line as wide as the dialog keeps the box from narrowing to
	// whichever lines happen to be in view; box leaves 6 columns for its
	// border and padding.
	lines = append(lines, strings.Repeat(" ", inner))
	return box("Settings", append(lines, foot...), inner+6)
}

// aboutLines are what the picked setting does, shown over its line.
func (d *settings) aboutLines(w int) []string {
	r := d.rows[d.cursor]
	desc := r.desc
	if r.restart {
		desc += "\nTakes effect from nerdr's next start."
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Italic(true)
	var out []string
	for _, para := range strings.Split(desc, "\n") {
		for _, l := range wrapWords(para, w-4) {
			out = append(out, "    "+style.Render(l))
		}
	}
	if r.example != "" {
		where := "Example: "
		if r.table != "" {
			where = "Example, under [" + r.table + "]: "
		}
		out = append(out, "    "+style.Render(where)+ansi.Truncate(r.example, w-4-lipgloss.Width(where), "…"))
	}
	return out
}

// rowName is the setting's name in its line.
func rowName(r settingRow) string {
	if r.label != "" {
		return r.label
	}
	return r.key
}

// shown is r's value in its line: the file's, or the default dimmed.
func (d *settings) shown(r settingRow) string {
	_, set := d.value(r)
	s := d.current(r)
	switch r.kind {
	case sBool:
		if s == "true" {
			s = "on"
		} else {
			s = "off"
		}
	case sChoice, sText:
		if s == "" {
			s = "–"
		}
	case sList, sEnv:
		if s == "" {
			s = "–"
		}
		if r.key == "command" && !set {
			s = strings.Trim(r.def, `[]"`)
		}
	}
	note := ""
	if n, ok := r.notes[d.current(r)]; ok {
		note = dim("  " + n)
	}
	if !set {
		return dim(s) + note
	}
	return lipgloss.NewStyle().Bold(true).Render(s) + note
}

// footer is where changes go, and the keys.
func (d *settings) footer(m *Model, w int) []string {
	r := d.rows[d.cursor]
	var out []string
	home, _ := os.UserHomeDir()
	if d.err != "" {
		out = append(out, warn(ansi.Truncate(d.err, w, "…")))
	} else {
		out = append(out, dim(ansi.Truncate("Saved to "+tildePath(config.Path(), home)+" as you go; dimmed is the default.", w, "…")))
	}
	out = append(out, "")
	if d.editing {
		hint := "enter save · esc cancel"
		switch r.kind {
		case sList:
			hint = "words, space between; \"quote\" one with spaces · " + hint
		case sEnv:
			hint = "NAME=value, space between · " + hint
		}
		return append(out, m.keys("enter", "save", "esc", "cancel")+dim("  "+strings.TrimSuffix(hint, " · enter save · esc cancel")))
	}
	act := "edit"
	switch r.kind {
	case sBool:
		act = "on/off"
	case sChoice:
		act = "next"
	}
	pairs := []string{"↑↓", "pick", "enter", act}
	if r.kind == sInt || r.kind == sChoice || len(r.choices) > 0 {
		pairs = append(pairs, "← →", "change")
	}
	pairs = append(pairs, "?", "about", "d", "default", "esc", "close")
	return append(out, m.keys(pairs...))
}

// wrapWords breaks s into lines of at most w columns.
func wrapWords(s string, w int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && lipgloss.Width(line)+1+lipgloss.Width(word) > w {
			out = append(out, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// splitWords splits s at spaces, as a shell would, with "…" and '…' for a
// word with spaces in it.
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	in, quote := false, rune(0)
	for _, c := range s {
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(c)
		case c == '"' || c == '\'':
			quote, in = c, true
		case unicode.IsSpace(c):
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(c)
			in = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("a %c is not closed", quote)
	}
	if in {
		words = append(words, cur.String())
	}
	return words, nil
}

// quoteWord is w as splitWords reads it back.
func quoteWord(w string) string {
	if w != "" && !strings.ContainsAny(w, " \t\"'") {
		return w
	}
	if strings.Contains(w, `"`) {
		return "'" + w + "'"
	}
	return `"` + w + `"`
}
