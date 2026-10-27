package app

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
	"github.com/nerdr-dev/nerdr/skills"

	"github.com/charmbracelet/x/ansi"
)

// TestSkillsGoToTheAgentsOwnFolders covers "home": the skills land where
// the agents on the PATH look, and a second start writes nothing.
func TestSkillsGoToTheAgentsOwnFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := tabsModel(t)
	m.cfg.Install = config.InstallHome
	m.cfg.Agents = map[agents.Kind]agents.Spec{
		agents.Claude:  {Command: []string{"sh"}},
		agents.Copilot: {Command: []string{"sh"}},
	}
	note := m.applySkills()
	for _, dir := range []string{".claude/skills", ".copilot/skills"} {
		if _, err := os.Stat(filepath.Join(home, dir, "nerdr-explain", "SKILL.md")); err != nil {
			t.Errorf("no skill in ~/%s: %v", dir, err)
		}
	}
	if !strings.Contains(note, "~/.claude/skills") {
		t.Errorf("the toast should say where: %q", note)
	}
	if note := m.applySkills(); note != "" {
		t.Errorf("a second start should write nothing, said %q", note)
	}
}

// Started in a folder with no repositories, the wizard says so and how to
// fix it, rather than showing an empty list.
func TestWizardWithoutRepositoriesSaysWhy(t *testing.T) {
	m := testModel(t) // rooted in an empty temporary folder
	m.openWizard()
	w := m.dialog.(*wizard)
	view := ansi.Strip(w.view(m, 100, 40))
	if !strings.Contains(view, "nothing to pick") || !strings.Contains(view, "repo_dirs") {
		t.Fatalf("view:\n%s", view)
	}
	w.key(m, keyMsg("enter"))
	if !strings.Contains(w.err, "no repositories") {
		t.Fatalf("enter: %q", w.err)
	}
}

// claude is told to leave its tab's worktree on its branch, and to ask
// nerdr for new ones, when the skill that does so is installed.
func TestClaudeIsToldAboutSpinoff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := testModel(t)
	tb := m.cur()
	for _, set := range []string{config.InstallHome, config.InstallWorktree} {
		m.cfg.Install = set
		args := m.claudeNote(tb)
		if len(args) != 2 || args[0] != "--append-system-prompt" || !strings.Contains(args[1], "nerdr-spinoff") {
			t.Fatalf("skills %s: %q", set, args)
		}
	}
	// Off, nerdr installs nothing: the note only when the agent has the
	// skill where it looks, put there by the user.
	m.cfg.Install = config.InstallOff
	if args := m.claudeNote(tb); args != nil {
		t.Fatalf("skills off, spinoff nowhere: %q", args)
	}
	if !strings.Contains(ansi.Strip(strings.Join(m.skillsBody(tb, 100, 60), "\n")), "cannot find") {
		t.Fatal("F4 should say the skills turned on are not found")
	}
	if _, err := skills.Install(filepath.Join(home, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	m.skills.at = time.Time{} // look again
	if args := m.claudeNote(tb); len(args) != 2 {
		t.Fatalf("skills off, spinoff in place: %q", args)
	}
	if strings.Contains(ansi.Strip(strings.Join(m.skillsBody(tb, 100, 60), "\n")), "cannot find") {
		t.Fatal("F4 still says the skills are missing")
	}
	m.cfg.Install = ""
	if args := m.claudeNote(tb); args != nil {
		t.Fatalf("not set up yet: no skill to point to, %q", args)
	}
	m.cfg.Install = config.InstallHome
	m.cfg.EnabledSkills = []string{"nerdr-explain"}
	if args := m.claudeNote(tb); args != nil {
		t.Fatalf("disabled spinoff still injected: %v", args)
	}
}

// The first start turns every skill on, to be turned off one by one in F4.
func TestFirstStartTurnsAllSkillsOnAndF6ChangesThem(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := testModel(t)
	for _, tb := range m.tabs {
		tb.Dir = t.TempDir()
		tb.Worktrees = nil
	}
	m.openSetup(nil)
	d := m.dialog.(*setupDialog)
	d.key(m, keyMsg("tab")) // to the skills
	d.key(m, keyMsg("2"))
	d.key(m, keyMsg("enter"))
	if m.dialog != nil {
		t.Fatalf("enter should save and close, dialog %T", m.dialog)
	}
	cfg, err := config.Load()
	if err != nil || cfg.Install != config.InstallWorktree {
		t.Fatalf("location not saved: %q %v", cfg.Install, err)
	}
	for _, sk := range []string{"nerdr-explain", "nerdr-spinoff"} {
		if !m.cfg.SkillEnabled(sk) || !cfg.SkillEnabled(sk) {
			t.Fatalf("%s should be on after the first start", sk)
		}
	}
	// Use real temporary paths before synchronising skills to the tabs.
	for _, tb := range m.tabs {
		tb.Dir = t.TempDir()
		tb.Worktrees = nil
	}
	m.skillsKey(keyMsg(" ")) // first skill: explain
	cfg, err = config.Load()
	if err != nil || cfg.SkillEnabled("nerdr-explain") {
		t.Fatalf("F4 did not disable: %v", err)
	}
	// Off, F4 still turns them on and off: they are taken to be in place.
	m.cfg.Install = config.InstallOff
	m.skillsKey(keyMsg(" "))
	if m.dialog != nil || !m.cfg.SkillEnabled("nerdr-explain") {
		t.Fatalf("F4 with skills off: dialog %T, explain on %v", m.dialog, m.cfg.SkillEnabled("nerdr-explain"))
	}
	m.skillsKey(keyMsg("s"))
	sd, ok := m.dialog.(*settings)
	if !ok || sd.rows[sd.cursor].key != "install" || sd.rows[sd.cursor].table != "" {
		t.Fatal("p in F4 must open the settings at skills")
	}
}

// The worktrees folder is asked first, and tab and shift+tab go round all
// three questions, the skills' choice too, which shows while another
// question has the keys.
func TestFirstStartAsksWhereWorktreesGo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := testModel(t)
	for _, tb := range m.tabs {
		tb.Dir = t.TempDir()
		tb.Worktrees = nil
	}
	m.openSetup(nil)
	d := m.dialog.(*setupDialog)
	if d.field != setupWorktrees || d.worktrees.Value() != config.DefaultWorktreesDir {
		t.Fatalf("field %d, value %q", d.field, d.worktrees.Value())
	}
	d.worktrees.SetValue("~/wt")
	for _, step := range []struct {
		key  string
		want int
	}{{"tab", setupSkills}, {"tab", setupNotes}, {"shift+tab", setupSkills}, {"shift+tab", setupWorktrees}, {"shift+tab", setupNotes}} {
		d.key(m, keyMsg(step.key))
		if d.field != step.want {
			t.Fatalf("%s: field %d, want %d", step.key, d.field, step.want)
		}
	}
	d.choice = 2
	view := ansi.Strip(d.view(m, 120, 60))
	if !strings.Contains(view, "● 3  Nowhere") || !strings.Contains(view, "○ 1  In the agents' own") {
		t.Fatalf("the skills' choice should show:\n%s", view)
	}
	if strings.Index(view, "worktrees go?") > strings.Index(view, "plugins and hooks?") {
		t.Fatalf("worktrees should be asked first:\n%s", view)
	}
	d.key(m, keyMsg("enter"))
	want := filepath.Join(home, "wt")
	if m.wtRoot != want {
		t.Fatalf("wtRoot %q, want %q", m.wtRoot, want)
	}
	if cfg, err := config.Load(); err != nil || !cfg.WorktreesDirSet() || cfg.WorktreesDir != want || cfg.Install != config.InstallOff {
		t.Fatalf("not saved: %q %q %v", cfg.WorktreesDir, cfg.Install, err)
	}
}

// TestOpenCodePluginGoesWithTheSkills covers nerdr's opencode plugin: put
// where opencode loads it, in the home folder or the tab's, as the skills
// are, and kept out of git there.
func TestOpenCodePluginGoesWithTheSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := tabsModel(t)
	m.cfg.Install = config.InstallHome
	m.cfg.Agents = map[agents.Kind]agents.Spec{agents.OpenCode: {Command: []string{"sh"}}}
	note := m.applySkills()
	if _, err := os.Stat(hooks.OpenCodePluginHome(home)); err != nil {
		t.Fatalf("no plugin in the home folder: %v", err)
	}
	if !strings.Contains(note, "~/.config/opencode/plugins/nerdr.js") {
		t.Errorf("the toast should say where: %q", note)
	}
	if note := m.applySkills(); note != "" {
		t.Errorf("a second start should write nothing, said %q", note)
	}

	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	m = tabsModel(t, state.Tab{ID: "1", Name: "w", Agent: "opencode", Dir: dir,
		Worktrees: []workspace.Worktree{{Repo: dir, Path: dir, Branch: "main"}}})
	m.cfg.Install = config.InstallWorktree
	tb := m.cur()
	m.installProjectSkills(tb)
	if _, err := os.Stat(hooks.OpenCodePluginProject(tb.Dir)); err != nil {
		t.Fatalf("no plugin in the tab's folder: %v", err)
	}
	if out, err := exec.Command("git", "-C", tb.Dir, "status", "--porcelain", "--untracked-files=all").Output(); err != nil || strings.Contains(string(out), ".opencode") {
		t.Errorf("the plugin is not kept out of git: %q %v", out, err)
	}
}

// The codex on the PATH is never asked to trust nerdr's hooks by a test:
// it would write the user's config. A fake one, by its path, is.
func init() {
	trustCodex = func(argv, env []string, write bool) (bool, bool, error) {
		if len(argv) > 0 && !filepath.IsAbs(argv[0]) {
			return false, false, errors.New("no codex in tests")
		}
		return hooks.TrustCodexHooks(argv, env, write)
	}
}

// fakeCodexWithHooks is a codex that knows --no-daemon, writes the
// arguments it is started with to the file it returns, and whose app
// server lists nerdr's hooks: untrusted until it is asked to trust them.
func fakeCodexWithHooks(t *testing.T) (cmd, started string) {
	t.Helper()
	dir := t.TempDir()
	started, trusted := filepath.Join(dir, "started"), filepath.Join(dir, "trusted")
	list := func(trust string) string {
		var hs []map[string]any
		for _, a := range hooks.CodexArgs() {
			if ev, _, ok := strings.Cut(strings.TrimPrefix(a, "hooks."), "="); ok {
				hs = append(hs, map[string]any{"key": "/<session-flags>/config.toml:" + ev + ":0:0",
					"command": `"$NERDR_BIN" hook codex >/dev/null 2>&1 || true`, "source": "sessionFlags",
					"enabled": true, "currentHash": "sha256:" + ev, "trustStatus": trust})
			}
		}
		b, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"hooks": hs}}})
		return string(b)
	}
	script := `#!/bin/sh
case "$*" in
*--help*) echo '  --no-daemon  run without the daemon'; exit 0 ;;
app-server*)
  while IFS= read -r line; do
    case "$line" in
    *'"method":"initialize"'*) echo '{"id":1,"result":{}}' ;;
    *'"method":"hooks/list"'*)
      if [ -f '` + trusted + `' ]; then printf '{"id":2,"result":%s}\n' '` + list("trusted") + `'
      else printf '{"id":2,"result":%s}\n' '` + list("untrusted") + `'; fi ;;
    *'"method":"config/batchWrite"'*) : > '` + trusted + `'; echo '{"id":3,"result":{"status":"ok"}}' ;;
    esac
  done; exit 0 ;;
esac
printf '%s\n' "$@" > '` + started + `'
exec cat
`
	cmd = filepath.Join(dir, "codex")
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cmd, started
}

// TestCodexGetsNerdrsHooksOnceTrusted covers nerdr's hooks in codex:
// left out while codex does not trust them, as it would stop at start to
// ask, trusted with the skills in the home folder, and passed from then.
func TestCodexGetsNerdrsHooksOnceTrusted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	cmd, started := fakeCodexWithHooks(t)
	startedWith := func() string {
		t.Helper()
		for i := 0; i < 100; i++ {
			if b, err := os.ReadFile(started); err == nil && len(b) > 0 {
				_ = os.Remove(started)
				return string(b)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("codex did not start")
		return ""
	}

	m := paneModel(t)
	m.cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{cmd}}}
	m.SetHookSocket("/run/nerdr.sock")
	tb := m.cur()
	m.cfg.Install = config.InstallWorktree
	m.newAgent(tb, agents.Codex)
	if args := startedWith(); !strings.Contains(args, "--no-daemon") || strings.Contains(args, "hook codex") {
		t.Fatalf("untrusted hooks passed, or not a codex of its own: %q", args)
	}

	m.cfg.Install = config.InstallHome
	if note := m.applySkills(); !strings.Contains(note, "~/.codex/config.toml") {
		t.Errorf("the toast should say codex's config was written: %q", note)
	}
	if note := m.applySkills(); note != "" {
		t.Errorf("a second start should write nothing, said %q", note)
	}
	m.newAgent(tb, agents.Codex)
	if args := startedWith(); !strings.Contains(args, "hooks.Stop=") || !strings.Contains(args, "hook codex") {
		t.Fatalf("trusted hooks not passed: %q", args)
	}
}

// TestCopilotPluginGoesWithTheSkills covers nerdr's copilot plugin: put in
// the home folder or the tab's, as the skills are, kept out of git there,
// given to copilot with --plugin-dir, and neither with the skills off.
func TestCopilotPluginGoesWithTheSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	cmd := filepath.Join(dir, "copilot")
	script := "#!/bin/sh\ncase \"$*\" in *--help*) echo '  --plugin-dir <directory>'; exit 0 ;; esac\nprintf '%s\\n' \"$@\" > '" + started + "'\nexec cat\n"
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	startedWith := func() string {
		t.Helper()
		for i := 0; i < 100; i++ {
			if b, err := os.ReadFile(started); err == nil && len(b) > 0 {
				_ = os.Remove(started)
				return string(b)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("copilot did not start")
		return ""
	}

	m := paneModel(t)
	m.cfg.Agents = map[agents.Kind]agents.Spec{agents.Copilot: {Command: []string{cmd}}}
	m.SetHookSocket("/run/nerdr.sock")
	m.cfg.Install = config.InstallHome
	note := m.applySkills()
	if !hooks.HasCopilotPlugin(hooks.CopilotPluginHome(home)) {
		t.Fatal("no plugin in the home folder")
	}
	if !strings.Contains(note, "~/.copilot/nerdr-plugin") {
		t.Errorf("the toast should say where: %q", note)
	}
	if note := m.applySkills(); note != "" {
		t.Errorf("a second start should write nothing, said %q", note)
	}
	tb := m.cur()
	m.newAgent(tb, agents.Copilot)
	if args := startedWith(); !strings.Contains(args, "--plugin-dir\n"+hooks.CopilotPluginHome(home)+"\n") {
		t.Fatalf("not given the plugin: %q", args)
	}

	m.cfg.Install = config.InstallOff
	m.newAgent(tb, agents.Copilot)
	if args := startedWith(); strings.Contains(args, "--plugin-dir") {
		t.Fatalf("given the plugin with the skills off: %q", args)
	}

	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	m = tabsModel(t, state.Tab{ID: "1", Name: "w", Agent: "copilot", Dir: repo,
		Worktrees: []workspace.Worktree{{Repo: repo, Path: repo, Branch: "main"}}})
	m.cfg.Install = config.InstallWorktree
	tb = m.cur()
	tb.sandboxed = true
	m.installProjectSkills(tb)
	if got := m.copilotPlugin(tb); got != hooks.CopilotPluginProject(repo) {
		t.Fatalf("plugin for the sandboxed tab: %q", got)
	}
	tb.sandboxed = false
	if got := m.copilotPlugin(tb); got == hooks.CopilotPluginProject(repo) || got == "" {
		t.Fatalf("unsandboxed copilot should use state-dir plugin, not worktree: %q", got)
	}
	if out, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=all").Output(); err != nil || strings.Contains(string(out), "nerdr-plugin") {
		t.Errorf("the plugin is not kept out of git: %q %v", out, err)
	}
}

// TestAgyHooksGoWithTheSkills covers nerdr's agy hooks: in agy's hooks
// file in the home folder, or the tab's, as the skills are; a file nerdr
// made in the tab kept out of git, and one the repository keeps not
// touched.
func TestAgyHooksGoWithTheSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := tabsModel(t)
	m.cfg.Install = config.InstallHome
	m.cfg.Agents = map[agents.Kind]agents.Spec{agents.Antigravity: {Command: []string{"sh"}}}
	note := m.applySkills()
	if b, err := os.ReadFile(hooks.AgyHooksHome(home)); err != nil || !strings.Contains(string(b), "hook agy") {
		t.Fatalf("no hooks in the home folder: %v %s", err, b)
	}
	if !strings.Contains(note, "~/.gemini/config/hooks.json") {
		t.Errorf("the toast should say where: %q", note)
	}
	if note := m.applySkills(); note != "" {
		t.Errorf("a second start should write nothing, said %q", note)
	}

	repo := func() string {
		dir := t.TempDir()
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
		return dir
	}
	tabIn := func(dir string) (*Model, *tab) {
		m := tabsModel(t, state.Tab{ID: "1", Name: "w", Agent: "agy", Dir: dir,
			Worktrees: []workspace.Worktree{{Repo: dir, Path: dir, Branch: "main"}}})
		m.cfg.Install = config.InstallWorktree
		return m, m.cur()
	}
	dir := repo()
	m, tb := tabIn(dir)
	m.installProjectSkills(tb)
	if b, err := os.ReadFile(hooks.AgyHooksProject(dir)); err != nil || !strings.Contains(string(b), "hook agy") {
		t.Fatalf("no hooks in the tab's folder: %v %s", err, b)
	}
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=all").Output(); err != nil || strings.Contains(string(out), ".agents") {
		t.Errorf("the hooks are not kept out of git: %q %v", out, err)
	}

	dir = repo()
	kept := hooks.AgyHooksProject(dir)
	_ = os.MkdirAll(filepath.Dir(kept), 0o755)
	_ = os.WriteFile(kept, []byte("{}\n"), 0o644)
	if out, err := exec.Command("git", "-C", dir, "add", ".agents/hooks.json").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	m, tb = tabIn(dir)
	m.installProjectSkills(tb)
	if b, _ := os.ReadFile(kept); string(b) != "{}\n" {
		t.Fatalf("the repository's hooks file was changed:\n%s", b)
	}
}
