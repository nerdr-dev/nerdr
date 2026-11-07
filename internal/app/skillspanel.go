package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/skills"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The review panel's F4 screen, three lists: the skills nerdr gives the
// agents (see skills/), what each is for and whether the active tab's agent
// has it where it looks for skills; the agent's other skills, both by name
// and with their use counted in the active tab or in all tabs; and what else
// the agent reads when it starts (see skillcontext.go).

// skillsChangedNote is what a change of a skill means for the running agents.
const skillsChangedNote = "Skills saved. Claude notices by itself; other agents at their next start. Text they already read stays in their conversation."

// The lists of the F4 screen.
const (
	listNerdr = iota
	listOther
	listContext
	skillLists
)

// skillsView is the F4 screen's state: the list shown, the row picked on
// each, how far its text is scrolled, whether use is counted over all tabs,
// and the last look at what the tab's agent reads. Where it has the skills is
// core's SkillLook.
type skillsView struct {
	cursor, scroll int
	list           int
	otherCursor    int
	contextCursor  int
	allTabs        bool

	context core.ContextView
}

// panelSkills reports whether the review panel shows the skills.
func (m *Model) panelSkills() bool { return m.reviewOpen && m.review.Skills() }

// skillsKey handles a key while the skills have the keyboard.
func (m *Model) skillsKey(msg tea.KeyMsg) tea.Cmd {
	v := &m.skills
	switch msg.String() {
	case "esc", "q":
		m.setFocus(core.FocusAgent)
		return m.resize()
	case "tab", "right", "l":
		v.list, v.scroll = (v.list+1)%skillLists, 0
	case "shift+tab", "left", "h":
		v.list, v.scroll = (v.list+skillLists-1)%skillLists, 0
	case "a":
		v.allTabs, v.scroll = !v.allTabs, 0
	case "up", "k":
		switch v.list {
		case listOther:
			v.otherCursor = max(v.otherCursor-1, 0)
		case listContext:
			v.contextCursor = max(v.contextCursor-1, 0)
		default:
			v.cursor = max(v.cursor-1, 0)
		}
		v.scroll = 0
	case "down", "j":
		t := m.Cur()
		switch v.list {
		case listOther:
			v.otherCursor = min(v.otherCursor+1, max(len(m.otherSkills(t, m.skillTotals(t)))-1, 0))
		case listContext:
			v.contextCursor = min(v.contextCursor+1, max(len(m.contextItems(t))-1, 0))
		default:
			v.cursor = min(v.cursor+1, len(skills.All())-1)
		}
		v.scroll = 0
	case " ":
		switch v.list {
		case listOther:
			m.toast("nerdr can only turn its own skills on and off.")
			return nil
		case listContext:
			return nil
		}
		all := skills.All()
		enabled := []string{}
		for i, sk := range all {
			if m.Cfg.SkillEnabled(sk.Name) != (i == v.cursor) {
				enabled = append(enabled, sk.Name)
			}
		}
		if m.Cfg.Install == "" {
			return m.openSetup(nil)
		}
		if err := config.SaveSkills(enabled); err != nil {
			m.toast(err.Error())
			return nil
		}
		m.Cfg.EnabledSkills = enabled
		return tea.Batch(m.do(core.SetConfig{Cfg: m.Cfg}), m.do(core.ApplySkills{Say: skillsChangedNote}))
	case "s":
		return m.openSettingsAt("install")
	case "pgdown", "ctrl+d":
		v.scroll += 10
	case "pgup", "b", "ctrl+u":
		v.scroll = max(v.scroll-10, 0)
	}
	return nil
}

// skillsMissing is how many of the skills turned on t's agent cannot find
// when nerdr puts none in place, and so are not used.
func (m *Model) skillsMissing(t *core.Tab) int {
	if m.Cfg.Install != config.InstallOff || t == nil {
		return 0
	}
	n := 0
	for _, s := range skills.All() {
		if m.Cfg.SkillEnabled(s.Name) && !m.SkillOn(t, s.Name) {
			n++
		}
	}
	return n
}

// skillMark is the icon for a skill's state, and what it means.
func skillMark(st skills.State, agent string) (string, string) {
	c := func(colour, s string) string { return lipgloss.NewStyle().Foreground(lipgloss.Color(colour)).Render(s) }
	switch st {
	case skills.Current:
		return c("71", "✓"), "available to " + agent
	case skills.Own:
		return c("110", "✓"), "available to " + agent + ": your own copy, which nerdr leaves alone"
	case skills.Outdated:
		return c("220", "~"), "available to " + agent + ", from an older nerdr: brought up to date at the next start"
	}
	return c("167", "✗"), "not where " + agent + " looks for skills"
}

// skillsBody draws the skills for t's agent, w×h, for the panel's frame.
func (m *Model) skillsBody(t *core.Tab, w, h int) []string {
	tot := m.skillTotals(t)
	others := m.otherSkills(t, tot)
	items := m.contextItems(t)
	head := []string{m.skillsTabs(len(skills.All()), len(others), len(items), w), ""}
	var text, list []string
	picked := m.skills.cursor
	keys := []string{"↑↓", "pick", "tab", "list"}
	switch m.skills.list {
	case listOther:
		list, text = m.otherSkillsList(t, others, tot, max(w-2, 10))
		picked = m.skills.otherCursor
		keys = append(keys, "a", "scope")
	case listContext:
		list, text, picked = m.contextList(t, items, max(w-2, 10))
	default:
		list, text = m.nerdrSkillsList(t, tot, max(w-2, 10))
		keys = append(keys, "a", "scope", "space", "on/off")
	}
	head = append(head, windowRows(list, picked, max(h/2, 3))...)
	keys = append(keys, "pgdown", "scroll", "b", "back", "s", "settings", "esc", "agent")
	v := &m.skills
	foot := []string{"", m.skillKeys(keys...)}
	room := max(h-len(head)-1-len(foot), 1)
	v.scroll = min(v.scroll, max(len(text)-room, 0))
	shown := text[v.scroll:]
	if len(shown) > room {
		shown = shown[:room]
	}
	lines := append(append(head, ""), shown...)
	body := fitLines(lines, w, h-len(foot))
	for i := range foot {
		foot[i] = ansi.Truncate(foot[i], w, "…")
	}
	return append(body, foot...)
}

// windowRows is at most room of rows, with the row picked among them, so a
// long list leaves room for the text of what is picked.
func windowRows(rows []string, picked, room int) []string {
	if len(rows) <= room {
		return rows
	}
	start := min(max(picked-room/2, 0), len(rows)-room)
	return rows[start : start+room]
}

// skillsTabs is the row that picks the list, and says where use is counted.
func (m *Model) skillsTabs(nerdr, other, context, w int) string {
	tab := func(label string, list int) string {
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
		if m.skills.list == list {
			style = lipgloss.NewStyle().Foreground(accent).Bold(true).Underline(true)
		}
		return m.onClick(style.Render(label), func(m *Model) tea.Cmd {
			if m.skills.list != list {
				m.skills.list, m.skills.scroll = list, 0
			}
			m.setFocus(core.FocusReview)
			return nil
		})
	}
	left := " " + tab(fmt.Sprintf("nerdr %d", nerdr), listNerdr) + "  " + tab(fmt.Sprintf("other %d", other), listOther) + "  " + tab(fmt.Sprintf("context %d", context), listContext)
	if m.skills.list == listContext {
		return left
	}
	scope := "use in this tab"
	if m.skills.allTabs {
		scope = "use in all tabs"
	}
	right := m.onClick(dim(scope), func(m *Model) tea.Cmd { return m.skillsKey(keyMsg("a")) })
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// skillRow is a row of F4's lists, picking the skill when clicked.
func (m *Model) skillRow(picked bool, mark, name string, u *core.SkillTotal, pick func(*Model)) string {
	cursor := "  "
	if picked && m.focus == core.FocusReview {
		cursor, name = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("❯ "), lipgloss.NewStyle().Foreground(accent).Bold(true).Render(name)
	} else if picked {
		name = lipgloss.NewStyle().Bold(true).Render(name)
	}
	if u != nil {
		name += dim(fmt.Sprintf("  %d×", u.N))
	}
	return m.onClick(cursor+mark+name, func(m *Model) tea.Cmd {
		pick(m)
		m.skills.scroll = 0
		m.setFocus(core.FocusReview)
		return nil
	})
}

// nerdrSkillsList is the list of nerdr's skills, and the text of the one
// picked.
func (m *Model) nerdrSkillsList(t *core.Tab, tot map[string]*core.SkillTotal, textW int) (head, text []string) {
	all := skills.All()
	v := &m.skills
	v.cursor = min(max(v.cursor, 0), len(all)-1)
	found, states := m.SkillStates(t)
	agent := "the agent"
	if t != nil {
		agent = string(t.Agent)
	}
	for i, s := range all {
		mark, _ := skillMark(states[i], agent)
		if t == nil {
			mark = dim("·")
		}
		name := "[ ] " + s.Name
		if m.Cfg.SkillEnabled(s.Name) {
			name = "[✓] " + s.Name
		}
		head = append(head, m.skillRow(i == v.cursor, mark+" ", name, tot[s.Name], func(m *Model) { m.skills.cursor = i }))
	}

	if n := m.skillsMissing(t); n > 0 {
		head = append(head, "")
		msg := fmt.Sprintf("nerdr puts no skills in place (install = \"off\"), and %s cannot find %d of the ones turned on (✗): skill-based guidance is unavailable; F1 explanations and agent-switch handoffs still work. Copy them from skills/ in nerdr's repository, turn them off here, or press s to let nerdr put them in place.", agent, n)
		for _, l := range wrapRows(msg, textW, 6) {
			head = append(head, warn(l))
		}
	}

	s := all[v.cursor]
	text = append(text, dim("── "+s.Name+" "+strings.Repeat("─", max(textW-lipgloss.Width(s.Name)-4, 0))))
	if t != nil {
		mark, what := skillMark(states[v.cursor], agent)
		if !m.Cfg.SkillEnabled(s.Name) {
			what = "turned off; claude notices by itself, other agents at their next start; text they already read stays"
			if states[v.cursor] == skills.Own {
				what += "; your own copy is still discoverable — remove it yourself"
			}
		}
		text = append(text, wrapLines(mark+" "+what, textW, 10)...)
		if found[v.cursor] != "" {
			text = append(text, dim("  "+found[v.cursor]))
		} else {
			why := "nerdr puts the skills in place at start, as the install setting says."
			if m.Cfg.Install == config.InstallOff {
				why = "nerdr puts no skills in place (install = \"off\" in the config): copy this one from skills/ in nerdr's repository into a folder " + agent + " reads skills from, or turn it off here."
			}
			for _, l := range wrapRows(why, textW-2, 3) {
				text = append(text, dim("  "+l))
			}
		}
		text = append(text, "")
	}
	text = append(text, dim(skillUseLine(tot[s.Name], time.Now())), "")
	text = append(text, lipgloss.NewStyle().Bold(true).Render("Without this skill"))
	text = append(text, wrapLines(s.Without(), textW, 1<<20)...)
	text = append(text, "")
	text = append(text, wrapLines(skills.CLIHelp, textW, 1<<20)...)
	text = append(text, "", lipgloss.NewStyle().Bold(true).Render("Tokens and usage"))
	text = append(text, wrapLines(skills.CostHelp, textW, 1<<20)...)
	text = append(text, "", lipgloss.NewStyle().Bold(true).Render("Skill instructions"))
	for _, l := range wrapRows(s.Description(), textW, 1<<20) {
		text = append(text, lipgloss.NewStyle().Italic(true).Render(l))
	}
	text = append(text, "")
	text = append(text, wrapLines(s.Body(), textW, 1<<20)...)
	return head, text
}

// otherSkillsList is the list of the skills that are not nerdr's, and the
// text of the one picked.
func (m *Model) otherSkillsList(t *core.Tab, others []skills.Local, tot map[string]*core.SkillTotal, textW int) (head, text []string) {
	v := &m.skills
	if len(others) == 0 {
		return []string{dim("  No other skills found or used here.")}, nil
	}
	v.otherCursor = min(max(v.otherCursor, 0), len(others)-1)
	for i, s := range others {
		head = append(head, m.skillRow(i == v.otherCursor, "", s.Name, tot[s.Name], func(m *Model) { m.skills.otherCursor = i }))
	}
	s := others[v.otherCursor]
	home, _ := os.UserHomeDir()
	text = append(text, dim("── "+s.Name+" "+strings.Repeat("─", max(textW-lipgloss.Width(s.Name)-4, 0))))
	if s.File != "" {
		text = append(text, dim("  "+core.TildePath(s.File, home)))
	} else {
		for _, l := range wrapRows("Not in a folder this tab's agent reads: built into an agent, from a plugin, or used elsewhere.", textW-2, 3) {
			text = append(text, dim("  "+l))
		}
	}
	text = append(text, "", dim(skillUseLine(tot[s.Name], time.Now())))
	if s.File == "" {
		return head, text
	}
	text = append(text, "", lipgloss.NewStyle().Bold(true).Render("Skill instructions"))
	for _, l := range wrapRows(s.Description(), textW, 1<<20) {
		text = append(text, lipgloss.NewStyle().Italic(true).Render(l))
	}
	text = append(text, "")
	text = append(text, wrapLines(s.Body(), textW, 1<<20)...)
	return head, text
}

// skillKeys is the F4 screen's row of keys, each one a button.
func (m *Model) skillKeys(pairs ...string) string {
	var out string
	for i := 0; i+1 < len(pairs); i += 2 {
		if out != "" {
			out += dim(" · ")
		}
		k, what := pairs[i], pairs[i+1]
		key := map[string]string{"↑↓": "down", "space": " "}[k]
		if key == "" {
			key = k
		}
		out += m.onClick(keyHint(k)+" "+dim(what), func(m *Model) tea.Cmd { return m.skillsKey(keyMsg(key)) })
	}
	return " " + out
}

// skillUseLine says how much the agents have used a skill, by agent, and
// when last.
func skillUseLine(u *core.SkillTotal, now time.Time) string {
	if u == nil || u.N == 0 {
		return "not used yet, as far as the agents' logs say"
	}
	agents := make([]string, 0, len(u.ByAgent))
	for a := range u.ByAgent {
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool { return u.ByAgent[agents[i]] > u.ByAgent[agents[j]] })
	var by []string
	for _, a := range agents {
		by = append(by, fmt.Sprintf("%s %d", a, u.ByAgent[a]))
	}
	line := fmt.Sprintf("used %d× · %s", u.N, strings.Join(by, ", "))
	if !u.Last.IsZero() {
		line += " · last " + core.Duration(now.Sub(u.Last)) + " ago"
	}
	return line
}
