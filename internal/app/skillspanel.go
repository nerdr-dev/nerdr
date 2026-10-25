package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"nerdr/internal/config"
	"nerdr/skills"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The review panel's F4 screen: the skills nerdr gives the agents (see
// skills/), what each is for, and whether the active tab's agent has it
// where it looks for skills.

// skillsCheckEvery is how long a look at the skills folders is trusted:
// the screen is drawn on every frame, and the files hardly ever change.
const skillsCheckEvery = 2 * time.Second

// skillsView is the F4 screen's state: the skill picked, how far its text
// is scrolled, and the last look at where the tab's agent has the skills.
type skillsView struct {
	cursor, scroll int

	key    string // the tab and agent looked for
	at     time.Time
	found  []string
	states []skills.State
}

// panelSkills reports whether the review panel shows the skills.
func (m *Model) panelSkills() bool { return m.reviewOpen && m.review.Skills() }

// skillsKey handles a key while the skills have the keyboard.
func (m *Model) skillsKey(msg tea.KeyMsg) tea.Cmd {
	n := len(skills.All())
	v := &m.skills
	switch msg.String() {
	case "esc", "q":
		m.setFocus(focusAgent)
		return m.resize()
	case "up", "k":
		v.cursor, v.scroll = max(v.cursor-1, 0), 0
	case "down", "j":
		v.cursor, v.scroll = min(v.cursor+1, n-1), 0
	case " ":
		all := skills.All()
		enabled := []string{}
		for i, sk := range all {
			if m.cfg.SkillEnabled(sk.Name) != (i == v.cursor) {
				enabled = append(enabled, sk.Name)
			}
		}
		if m.cfg.Install == "" {
			return m.openSetup(nil)
		}
		if err := config.SaveSkills(enabled); err != nil {
			m.toast(err.Error())
			return nil
		}
		m.cfg.EnabledSkills = enabled
		note := m.applySkills()
		v.at = time.Time{}
		m.toast("Skills saved; restart agents to refresh their loaded instructions. " + note)
	case "s":
		return m.openSettingsAt("install")
	case "pgdown", "ctrl+d":
		v.scroll += 10
	case "pgup", "b", "ctrl+u":
		v.scroll = max(v.scroll-10, 0)
	}
	return nil
}

// skillStates is where the agent of t has each skill, and in what state,
// looked up again when the last look is old or was for another tab.
func (m *Model) skillStates(t *tab) ([]string, []skills.State) {
	all := skills.All()
	v := &m.skills
	key := ""
	if t != nil {
		key = t.ID + "/" + string(t.Agent) + "/" + t.Dir
	}
	if key == v.key && time.Since(v.at) < skillsCheckEvery && len(v.states) == len(all) {
		return v.found, v.states
	}
	v.key, v.at = key, time.Now()
	v.found, v.states = make([]string, len(all)), make([]skills.State, len(all))
	if t == nil {
		return v.found, v.states
	}
	home, _ := os.UserHomeDir()
	dirs := skills.Dirs(home, t.Dir, t.Agent)
	for i, s := range all {
		file, st := s.Find(dirs)
		v.found[i], v.states[i] = tildePath(file, home), st
	}
	return v.found, v.states
}

// skillOn is whether tab t's agent can use the skill called name: it is
// turned on and, with skills "off", where the agent looks for skills, as
// nerdr then puts none in place. What uses a skill is offered only then.
func (m *Model) skillOn(t *tab, name string) bool {
	if !m.cfg.SkillEnabled(name) {
		return false
	}
	if m.cfg.Install != config.InstallOff {
		return true
	}
	if t == nil {
		return false
	}
	_, states := m.skillStates(t)
	for i, s := range skills.All() {
		if s.Name == name {
			return states[i] != skills.Missing
		}
	}
	return false
}

// skillsMissing is how many of the skills turned on t's agent cannot find
// when nerdr puts none in place, and so are not used.
func (m *Model) skillsMissing(t *tab) int {
	if m.cfg.Install != config.InstallOff || t == nil {
		return 0
	}
	n := 0
	for _, s := range skills.All() {
		if m.cfg.SkillEnabled(s.Name) && !m.skillOn(t, s.Name) {
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
func (m *Model) skillsBody(t *tab, w, h int) []string {
	all := skills.All()
	v := &m.skills
	v.cursor = min(max(v.cursor, 0), len(all)-1)
	found, states := m.skillStates(t)
	agent := "the agent"
	if t != nil {
		agent = string(t.Agent)
	}
	textW := max(w-2, 10)
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)

	tot := m.skillTotals()
	var head []string
	for i, s := range all {
		mark, _ := skillMark(states[i], agent)
		if t == nil {
			mark = dim("·")
		}
		name := s.Name
		if m.cfg.SkillEnabled(s.Name) {
			name = "[✓] " + name
		} else {
			name = "[ ] " + name
		}
		cursor := "  "
		if i == v.cursor && m.focus == focusReview {
			cursor, name = sel.Render("❯ "), sel.Render(name)
		} else if i == v.cursor {
			name = lipgloss.NewStyle().Bold(true).Render(name)
		}
		if u := tot[s.Name]; u != nil {
			name += dim(fmt.Sprintf("  %d×", u.n))
		}
		head = append(head, m.onClick(cursor+mark+" "+name, func(m *Model) tea.Cmd {
			m.skills.cursor, m.skills.scroll = i, 0
			m.setFocus(focusReview)
			return nil
		}))
	}

	own := map[string]bool{}
	for _, s := range all {
		own[s.Name] = true
	}
	if other := m.otherSkills(own); len(other) > 0 {
		var parts []string
		for _, name := range other {
			parts = append(parts, fmt.Sprintf("%s %d×", name, tot[name].n))
		}
		head = append(head, "")
		for _, l := range wrapRows("Other skills used: "+strings.Join(parts, " · "), textW, 2) {
			head = append(head, dim(l))
		}
	}

	if n := m.skillsMissing(t); n > 0 {
		head = append(head, "")
		msg := fmt.Sprintf("nerdr puts no skills in place (install = \"off\"), and %s cannot find %d of the ones turned on (✗): what uses them is off. Copy them from skills/ in nerdr's repository, turn them off here, or press p to let nerdr put them in place.", agent, n)
		for _, l := range wrapRows(msg, textW, 6) {
			head = append(head, warn(l))
		}
	}

	s := all[v.cursor]
	var text []string
	text = append(text, dim("── "+s.Name+" "+strings.Repeat("─", max(textW-lipgloss.Width(s.Name)-4, 0))))
	if t != nil {
		mark, what := skillMark(states[v.cursor], agent)
		if !m.cfg.SkillEnabled(s.Name) {
			what = "disabled for new starts; existing conversation text is retained"
			if states[v.cursor] == skills.Own {
				what += "; your own copy is still discoverable — remove it yourself"
			}
		}
		text = append(text, wrapLines(mark+" "+what, textW, 10)...)
		if found[v.cursor] != "" {
			text = append(text, dim("  "+found[v.cursor]))
		} else {
			why := "nerdr puts the skills in place at start, as the install setting says."
			if m.cfg.Install == config.InstallOff {
				why = "nerdr puts no skills in place (install = \"off\" in the config): copy this one from skills/ in nerdr's repository into a folder " + agent + " reads skills from, or turn it off here."
			}
			for _, l := range wrapRows(why, textW-2, 3) {
				text = append(text, dim("  "+l))
			}
		}
		text = append(text, "")
	}
	text = append(text, dim(skillUseLine(tot[s.Name], time.Now())), "")
	for _, l := range wrapRows(s.Description(), textW, 1<<20) {
		text = append(text, lipgloss.NewStyle().Italic(true).Render(l))
	}
	text = append(text, "")
	text = append(text, wrapLines(s.Body(), textW, 1<<20)...)

	foot := []string{"", m.skillKeys("↑↓", "pick", "space", "on/off", "pgdown", "scroll", "b", "back", "s", "settings", "esc", "agent")}
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
func skillUseLine(u *skillTotal, now time.Time) string {
	if u == nil || u.n == 0 {
		return "not used yet, as far as the agents' logs say"
	}
	agents := make([]string, 0, len(u.byAgent))
	for a := range u.byAgent {
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool { return u.byAgent[agents[i]] > u.byAgent[agents[j]] })
	var by []string
	for _, a := range agents {
		by = append(by, fmt.Sprintf("%s %d", a, u.byAgent[a]))
	}
	line := fmt.Sprintf("used %d× · %s", u.n, strings.Join(by, ", "))
	if !u.last.IsZero() {
		line += " · last " + duration(now.Sub(u.last)) + " ago"
	}
	return line
}
