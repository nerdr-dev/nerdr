// Package detect classifies what a coding agent is doing from what it has
// drawn on its screen: working, blocked on a question, or idle at its prompt.
//
// It is a port of herdr's screen-manifest engine (Apache-2.0,
// github.com/herdrdev/herdr, src/detect/manifest.rs), and reads the same
// TOML manifests. The ones herdr keeps up to date in its state directory are
// preferred when they exist, so the rules follow new agent releases without
// a new nerdr; the copies bundled here are the fallback.
package detect

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// State is an agent's lifecycle state as read from its screen.
type State int

const (
	Unknown State = iota
	Idle
	Working
	Blocked
)

func (s State) String() string {
	switch s {
	case Idle:
		return "idle"
	case Working:
		return "working"
	case Blocked:
		return "blocked"
	}
	return "unknown"
}

// Input is what one classification looks at.
type Input struct {
	// Screen is the visible screen, one line per row, right-trimmed.
	Screen string
	// Title is the last OSC 0/2 window title the agent set.
	Title string
	// Progress is the last OSC 9;4 progress report, without the leading "9;".
	Progress string
}

// FallbackRule is the rule of a Result no rule matched.
const FallbackRule = "fallback"

// Result is the outcome of one classification.
type Result struct {
	State State
	Rule  string
	// Skip means the matched rule says the screen is showing something
	// (a transcript viewer, a model picker) that says nothing about the
	// agent's state, so the previous state should be kept.
	Skip bool
	// Visible means the rule recognised the state's own UI on the screen
	// (the prompt box, a permission dialog, a spinner) and not merely
	// something that tends to go with it: herdr's visible_idle,
	// visible_blocker and visible_working. Only such a screen is clear
	// enough to overrule what an agent's hooks reported.
	Visible bool
}

//go:embed manifests/*.toml
var bundled embed.FS

// EngineVersion is the herdr manifest engine version this port matches. A
// manifest that asks for a newer one may use regions or gates it does not
// know, so it is passed over for the bundled copy.
const EngineVersion = 3

type manifestFile struct {
	ID               string   `toml:"id"`
	Version          string   `toml:"version"`
	MinEngineVersion int      `toml:"min_engine_version"`
	Aliases          []string `toml:"aliases"`
	Rules            []rule   `toml:"rules"`
}

type gate struct {
	All       []gate   `toml:"all"`
	Any       []gate   `toml:"any"`
	Not       []gate   `toml:"not"`
	Contains  []string `toml:"contains"`
	Regex     []string `toml:"regex"`
	LineRegex []string `toml:"line_regex"`
}

type rule struct {
	gate
	ID       string `toml:"id"`
	State    string `toml:"state"`
	Priority int    `toml:"priority"`
	Region   string `toml:"region"`
	Skip     bool   `toml:"skip_state_update"`

	VisibleIdle    bool `toml:"visible_idle"`
	VisibleBlocker bool `toml:"visible_blocker"`
	VisibleWorking bool `toml:"visible_working"`
}

type compiledGate struct {
	all, any, not []compiledGate
	contains      []string
	regex         []*regexp.Regexp
	lineRegex     []*regexp.Regexp
}

type compiledRule struct {
	id       string
	state    State
	priority int
	region   string
	skip     bool
	visible  bool
	gate     compiledGate
}

// Manifest is a compiled set of rules for one agent.
type Manifest struct {
	ID      string
	Version string
	Source  string
	rules   []compiledRule
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*Manifest{}
)

// For returns the manifest for an agent kind ("claude", "opencode",
// "copilot"), or nil when there is none — Classify then says Unknown.
func For(kind string) *Manifest {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if m, ok := cache[kind]; ok {
		return m
	}
	m := load(kind)
	cache[kind] = m
	return m
}

func load(kind string) *Manifest {
	var bundledM *Manifest
	if data, err := bundled.ReadFile("manifests/" + kind + ".toml"); err == nil {
		if m, err := Parse(data); err == nil {
			m.Source = "bundled"
			bundledM = m
		}
	}
	// A local override is the user's own word, and wins outright.
	for _, dir := range herdrOverrideDirs() {
		if m := loadFile(filepath.Join(dir, kind+".toml")); m != nil {
			return m
		}
	}
	// herdr's cached copy is refreshed from its release channel, so it
	// knows about agent UIs newer than this binary — but it is only
	// refreshed while herdr runs, and can be older than the bundled one.
	// As herdr does, the newer of the two is used.
	for _, dir := range herdrRemoteDirs() {
		m := loadFile(filepath.Join(dir, kind+".toml"))
		if m == nil {
			continue
		}
		if bundledM != nil && olderVersion(m.Version, bundledM.Version) {
			break
		}
		return m
	}
	return bundledM
}

func loadFile(path string) *Manifest {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m, err := Parse(data)
	if err != nil {
		return nil
	}
	m.Source = path
	return m
}

// olderVersion compares herdr's dotted numeric manifest versions
// (2026.10.01.1); one that does not parse counts as older.
func olderVersion(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		var err error
		if i < len(pa) {
			if x, err = strconv.Atoi(pa[i]); err != nil {
				return true
			}
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func herdrOverrideDirs() []string {
	var dirs []string
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "herdr", "agent-detection"))
	}
	if home, _ := os.UserHomeDir(); home != "" {
		dirs = append(dirs, filepath.Join(home, ".config", "herdr", "agent-detection"))
		// Where macOS programs keep their files, as for the remote copies.
		if lib := filepath.Join(home, "Library", "Application Support", "herdr", "agent-detection"); dirExists(lib) {
			dirs = append(dirs, lib)
		}
	}
	return dirs
}

func herdrRemoteDirs() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	dirs := []string{filepath.Join(state, "herdr", "agent-detection", "remote")}
	if lib := filepath.Join(home, "Library", "Application Support", "herdr", "agent-detection", "remote"); dirExists(lib) {
		dirs = append(dirs, lib)
	}
	return dirs
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Parse compiles a manifest from TOML.
func Parse(data []byte) (*Manifest, error) {
	var f manifestFile
	if _, err := toml.Decode(string(data), &f); err != nil {
		return nil, err
	}
	if f.MinEngineVersion > EngineVersion {
		return nil, fmt.Errorf("manifest %s needs engine %d, this is %d", f.ID, f.MinEngineVersion, EngineVersion)
	}
	m := &Manifest{ID: f.ID, Version: f.Version}
	for _, r := range f.Rules {
		g, err := compile(r.gate)
		if err != nil {
			return nil, err
		}
		region := r.Region
		if region == "" {
			region = "whole_recent"
		}
		state := parseState(r.State)
		// A flag counts only for the state the rule gives, as in herdr.
		visible := (state == Idle && r.VisibleIdle) || (state == Blocked && r.VisibleBlocker) ||
			(state == Working && r.VisibleWorking)
		m.rules = append(m.rules, compiledRule{
			id: r.ID, state: state, priority: r.Priority,
			region: region, skip: r.Skip, visible: visible, gate: g,
		})
	}
	return m, nil
}

func parseState(s string) State {
	switch s {
	case "idle":
		return Idle
	case "working":
		return Working
	case "blocked":
		return Blocked
	}
	return Unknown
}

func compile(g gate) (compiledGate, error) {
	var c compiledGate
	for _, list := range []struct {
		src []gate
		dst *[]compiledGate
	}{{g.All, &c.all}, {g.Any, &c.any}, {g.Not, &c.not}} {
		for _, sub := range list.src {
			cg, err := compile(sub)
			if err != nil {
				return c, err
			}
			*list.dst = append(*list.dst, cg)
		}
	}
	for _, s := range g.Contains {
		c.contains = append(c.contains, strings.ToLower(s))
	}
	for _, p := range g.Regex {
		re, err := regexp.Compile(goRegex(p))
		if err != nil {
			return c, err
		}
		c.regex = append(c.regex, re)
	}
	for _, p := range g.LineRegex {
		re, err := regexp.Compile(goRegex(p))
		if err != nil {
			return c, err
		}
		c.lineRegex = append(c.lineRegex, re)
	}
	return c, nil
}

// goRegex turns the bits of Rust's regex syntax herdr's manifests use and
// Go's lacks into Go's: \u2800 and \u{2800} into \x{2800}, and the
// Alphabetic property into letters (\p{L}, which leaves out the few
// letter-like marks and numerals Alphabetic also has).
func goRegex(p string) string {
	if !strings.Contains(p, `\u`) && !strings.Contains(p, "Alphabetic}") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '\\' || i+1 == len(p) {
			b.WriteByte(p[i])
			continue
		}
		rest := p[i+1:]
		switch {
		case strings.HasPrefix(rest, "u{"):
			b.WriteString(`\x`)
			i++
		case rest[0] == 'u' && len(rest) >= 5 && isHex(rest[1:5]):
			b.WriteString(`\x{` + rest[1:5] + "}")
			i += 5
		case strings.HasPrefix(rest, "p{Alphabetic}"), strings.HasPrefix(rest, "P{Alphabetic}"):
			b.WriteString(`\` + rest[:1] + "{L}")
			i += len("p{Alphabetic}")
		default:
			// Any other escape, \\ among them, is passed on whole, so
			// the character after it is not read as the start of one.
			b.WriteString(p[i : i+2])
			i++
		}
	}
	return b.String()
}

func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// Classify runs the manifest's rules over in; the highest-priority rule
// that matches decides. With no match the agent is taken to be idle, as
// herdr does: an agent that shows neither work nor a question is waiting.
func (m *Manifest) Classify(in Input) Result {
	if m == nil {
		return Result{State: Unknown}
	}
	var best *compiledRule
	// Most rules read one of the same few regions: each is cut out of the
	// screen, and lowered, once.
	type cut struct{ text, lower string }
	cuts := map[string]cut{}
	for i := range m.rules {
		r := &m.rules[i]
		c, ok := cuts[r.region]
		if !ok {
			text := region(in, r.region)
			c = cut{text, strings.ToLower(text)}
			cuts[r.region] = c
		}
		if !r.gate.matches(c.text, c.lower) {
			continue
		}
		if best == nil || r.priority > best.priority {
			best = r
		}
	}
	if best == nil {
		return Result{State: Idle, Rule: FallbackRule}
	}
	return Result{State: best.state, Rule: best.id, Skip: best.skip, Visible: best.visible}
}

// KnowsIdle is whether the manifest has a rule for the agent's idle
// screen. Without one (opencode, copilot) every idle screen is a fallback,
// and a fallback is the only way such an agent is seen to be done.
func (m *Manifest) KnowsIdle() bool {
	if m == nil {
		return false
	}
	for _, r := range m.rules {
		if r.state == Idle {
			return true
		}
	}
	return false
}

func (g *compiledGate) matches(text, lower string) bool {
	for _, n := range g.contains {
		if !strings.Contains(lower, n) {
			return false
		}
	}
	for _, re := range g.regex {
		if !re.MatchString(text) {
			return false
		}
	}
	if len(g.lineRegex) > 0 {
		lines := strings.Split(text, "\n")
		for _, re := range g.lineRegex {
			found := false
			for _, l := range lines {
				if re.MatchString(l) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	for i := range g.all {
		if !g.all[i].matches(text, lower) {
			return false
		}
	}
	if len(g.any) > 0 {
		ok := false
		for i := range g.any {
			if g.any[i].matches(text, lower) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for i := range g.not {
		if g.not[i].matches(text, lower) {
			return false
		}
	}
	return true
}

func region(in Input, spec string) string {
	spec = strings.TrimSpace(spec)
	switch spec {
	case "osc_title":
		return in.Title
	case "osc_progress":
		return in.Progress
	case "whole_recent":
		return in.Screen
	case "prompt_box_body":
		return promptBoxBody(in.Screen)
	case "above_prompt_box":
		return abovePromptBox(in.Screen)
	case "last_non_empty_above_prompt_box":
		return lastNonEmpty(abovePromptBox(in.Screen))
	case "after_last_horizontal_rule":
		return afterLastRule(in.Screen)
	case "after_last_prompt_marker":
		return afterLastPromptMarker(in.Screen)
	case "before_current_prompt_marker":
		return beforeCurrentPromptMarker(in.Screen)
	case "whole_recent_without_current_prompt_marker":
		if currentPromptIndex(strings.Split(in.Screen, "\n")) >= 0 {
			return ""
		}
		return in.Screen
	case "current_prompt_block_marker":
		return currentPromptBlockMarker(in.Screen)
	case "after_current_prompt_block_marker":
		return afterCurrentPromptBlockMarker(in.Screen)
	}
	if n, ok := regionCount(spec, "bottom_non_empty_lines"); ok {
		return bottomNonEmpty(in.Screen, n)
	}
	if n, ok := regionCount(spec, "bottom_lines"); ok {
		lines := strings.Split(in.Screen, "\n")
		return strings.Join(lines[max(0, len(lines)-n):], "\n")
	}
	if n, ok := regionCount(spec, "top_non_empty_lines"); ok {
		return topNonEmpty(in.Screen, n)
	}
	return ""
}

func regionCount(spec, name string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, name+"(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0
}

func bottomNonEmpty(s string, n int) string {
	lines := strings.Split(s, "\n")
	count := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			count++
			if count == n {
				return strings.Join(lines[i:], "\n")
			}
		}
	}
	if count == 0 {
		return ""
	}
	return s
}

func topNonEmpty(s string, n int) string {
	lines := strings.Split(s, "\n")
	count := 0
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			count++
			if count == n {
				return strings.Join(lines[:i+1], "\n")
			}
		}
	}
	return s
}

func isRule(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	n := 0
	for _, r := range t {
		if r != '─' {
			break
		}
		n++
	}
	if n == 0 {
		return false
	}
	suffix := strings.TrimLeft(t[n*len("─"):], " \t")
	return suffix == "" || n >= 3
}

// promptBoxTop is the second-to-last horizontal rule: the top border of
// the input box the agent draws at the bottom.
func promptBoxTop(lines []string) int {
	seen := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if isRule(lines[i]) {
			seen++
			if seen == 2 {
				return i
			}
		}
	}
	return -1
}

func promptBoxBody(s string) string {
	lines := strings.Split(s, "\n")
	top := promptBoxTop(lines)
	if top < 0 {
		return ""
	}
	end := len(lines)
	for i := top + 1; i < len(lines); i++ {
		if isRule(lines[i]) {
			end = i
			break
		}
	}
	return strings.Join(lines[top+1:end], "\n")
}

func abovePromptBox(s string) string {
	lines := strings.Split(s, "\n")
	top := promptBoxTop(lines)
	if top < 0 {
		return s
	}
	return strings.Join(lines[:top], "\n")
}

func afterLastRule(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if isRule(lines[i]) {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return s
}

func lastNonEmpty(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// The prompt-marker regions are codex's: its input line starts with "›",
// and each block of its transcript with "•", "■", "✗" or "✓".

func isPromptMarker(line string) bool {
	return line == "›" || strings.HasPrefix(line, "› ")
}

func isBlockMarker(line string) bool {
	for _, p := range []string{"•", "■", "✗", "✓"} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// currentPromptIndex is the line of the last prompt marker, or -1 when
// there is none or a transcript block follows it — a prompt scrolled up
// out of the way, not the one being typed into.
func currentPromptIndex(lines []string) int {
	at := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if isPromptMarker(lines[i]) {
			at = i
			break
		}
	}
	if at < 0 {
		return -1
	}
	for _, l := range lines[at+1:] {
		if isBlockMarker(l) {
			return -1
		}
	}
	return at
}

func afterLastPromptMarker(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if isPromptMarker(lines[i]) {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return s
}

func beforeCurrentPromptMarker(s string) string {
	lines := strings.Split(s, "\n")
	at := currentPromptIndex(lines)
	if at < 0 {
		return s
	}
	return strings.Join(lines[:at], "\n")
}

func currentPromptBlock(lines []string) int {
	at := currentPromptIndex(lines)
	for i := at - 1; i >= 0; i-- {
		if isBlockMarker(lines[i]) {
			return i
		}
	}
	return -1
}

func currentPromptBlockMarker(s string) string {
	lines := strings.Split(s, "\n")
	if i := currentPromptBlock(lines); i >= 0 {
		return lines[i]
	}
	return ""
}

func afterCurrentPromptBlockMarker(s string) string {
	lines := strings.Split(s, "\n")
	if i := currentPromptBlock(lines); i >= 0 {
		return strings.Join(lines[i:], "\n")
	}
	return ""
}
