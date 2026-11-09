package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/skills"
)

// The setup dialog's questions, in the order asked.
const (
	SetupWorktrees = iota
	SetupSkills
	SetupNotes
	SetupFields
)

func TildePath(p, home string) string {
	if home != "" {
		if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
			return "~/" + rest
		}
	}
	return p
}

// InstallProjectSkills puts the skills in the folder t's agent starts in,
// when the setting says so, and keeps them — and a notes folder inside the
// worktrees — out of git.
func (m *Core) InstallProjectSkills(t *Tab) {
	for _, w := range t.Worktrees {
		_ = store.ExcludeData(w.Path)
	}
	if m.Cfg.Install != config.InstallWorktree {
		return
	}
	if _, err := skills.SyncProject(skills.ProjectDir(t.Dir, t.Agent), m.Cfg.SkillEnabled); err != nil {
		m.Toast("skills: " + err.Error())
		return
	}
	excludes := skills.ExcludePatterns()
	if t.Agent == agents.OpenCode {
		if _, err := hooks.InstallOpenCodePluginProject(t.Dir); err != nil {
			m.Toast("opencode plugin: " + err.Error())
		}
		excludes = append(excludes, hooks.OpenCodePluginExclude)
	}
	if t.Agent == agents.Copilot {
		if _, err := hooks.InstallCopilotPluginProject(t.Dir); err != nil {
			m.Toast("copilot plugin: " + err.Error())
		}
		excludes = append(excludes, hooks.CopilotPluginExclude)
	}
	if t.Agent == agents.Antigravity {
		// In the project's own hooks file, unless the repository keeps
		// one: that is not nerdr's to change.
		file := hooks.AgyHooksProject(t.Dir)
		if !Tracked(t.Dir, file) {
			if _, made, err := hooks.InstallAgyHooksProject(t.Dir); err != nil {
				m.Toast("agy hooks: " + err.Error())
			} else if made {
				excludes = append(excludes, hooks.AgyHooksExclude)
			}
		}
	}
	if t.Agent == agents.Grok {
		// Unless the repository keeps a file by that name.
		file := hooks.GrokHooksProject(t.Dir)
		if !Tracked(t.Dir, file) {
			if _, made, err := hooks.InstallGrokHooksProject(t.Dir); err != nil {
				m.Toast("grok hooks: " + err.Error())
			} else if made {
				excludes = append(excludes, hooks.GrokHooksExclude)
			}
		}
	}
	for _, w := range t.Worktrees {
		if filepath.Clean(w.Path) != filepath.Clean(t.Dir) {
			continue // a tab over several repositories starts above them
		}
		for _, p := range excludes {
			_ = store.Exclude(w.Path, p)
		}
	}
}

// CodexConfig is where codex keeps the trust it gives hooks.
func CodexConfig(home string) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "config.toml")
	}
	return filepath.Join(home, ".codex", "config.toml")
}

// Tracked reports whether git keeps file in the repository dir is in.
func Tracked(dir, file string) bool {
	return proc.Run(gitsafe.Command(dir, "ls-files", "--error-unmatch", "--", file)) == nil
}

// applySkills puts the skills where the install setting says, and says where it
// wrote any; "" when nothing was written. For "worktree" that is the tabs'
// folders, which each tab's start also sees to (see installProjectSkills).
//
// It asks codex whether it trusts nerdr's hooks, which runs it: not with
// m.Mu held, outside the tests. The request ApplySkills asks it away.
func (m *Core) ApplySkills() string {
	return m.applySkills(m.TrustCodexHooks)
}

// applySkills is ApplySkills, with trust to have codex trust nerdr's
// hooks; it says whether that wrote codex's config.
func (m *Core) applySkills(trust func(write bool) (bool, error)) string {
	home, _ := os.UserHomeDir()
	homeDirs := map[string]bool{}
	for _, k := range agents.All {
		for _, dir := range skills.Dirs(home, "", k)[1:] {
			if dir != "" {
				homeDirs[dir] = true
			}
		}
	}
	wtDirs := map[string]bool{}
	for _, t := range m.Tabs {
		for _, k := range agents.All {
			wtDirs[skills.ProjectDir(t.Dir, k)] = true
		}
	}
	for dir := range homeDirs {
		if m.Cfg.EnabledSkills == nil {
			continue
		}
		if err := skills.RemoveDisabled(dir, m.Cfg.SkillEnabled); err != nil {
			return "skills: " + err.Error()
		}
	}
	for dir := range wtDirs {
		if m.Cfg.EnabledSkills == nil {
			continue
		}
		if err := skills.RemoveDisabledProject(dir, m.Cfg.SkillEnabled); err != nil {
			return "skills: " + err.Error()
		}
	}

	switch m.Cfg.Install {
	case config.InstallHome:
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		var wrote []string
		for _, tg := range skills.Home(home, m.Available()) {
			names, err := skills.Sync(tg.Dir, m.Cfg.SkillEnabled)
			if err != nil {
				return "skills: " + err.Error()
			}
			if len(names) > 0 {
				wrote = append(wrote, TildePath(tg.Dir, home))
			}
		}
		if slices.Contains(m.Available(), agents.OpenCode) {
			// With the skills, nerdr's plugin, which tells nerdr what
			// opencode is doing.
			file := hooks.OpenCodePluginHome(home)
			if ok, err := hooks.InstallOpenCodePlugin(file); err != nil {
				return "opencode plugin: " + err.Error()
			} else if ok {
				wrote = append(wrote, TildePath(file, home))
			}
		}
		if slices.Contains(m.Available(), agents.Copilot) {
			// And nerdr's plugin, whose hooks tell nerdr what copilot is
			// doing.
			dir := hooks.CopilotPluginHome(home)
			if ok, err := hooks.InstallCopilotPlugin(dir); err != nil {
				return "copilot plugin: " + err.Error()
			} else if ok {
				wrote = append(wrote, TildePath(dir, home))
			}
		}
		if slices.Contains(m.Available(), agents.Antigravity) {
			// And nerdr's hooks in agy's hooks file, beside any others.
			file := hooks.AgyHooksHome(home)
			if ok, _, err := hooks.InstallAgyHooks(file); err != nil {
				return "agy hooks: " + err.Error()
			} else if ok {
				wrote = append(wrote, TildePath(file, home))
			}
		}
		if slices.Contains(m.Available(), agents.Grok) {
			// And nerdr's own hooks file among grok's.
			file := hooks.GrokHooksHome(home)
			if ok, _, err := hooks.InstallGrokHooks(file); err != nil {
				return "grok hooks: " + err.Error()
			} else if ok {
				wrote = append(wrote, TildePath(file, home))
			}
		}
		if slices.Contains(m.Available(), agents.Codex) {
			// codex runs nerdr's hooks, which tell nerdr what it is
			// doing, once it trusts them, in its own config.
			if ok, err := trust(true); ok {
				wrote = append(wrote, TildePath(CodexConfig(home), home)+" (trusts nerdr's codex hooks)")
			} else if err != nil {
				Debugf("codex hooks: %v", err)
			}
		}
		if len(wrote) > 0 {
			return "skills installed in " + strings.Join(wrote, ", ")
		}
	case config.InstallWorktree:
		for _, t := range m.Tabs {
			m.InstallProjectSkills(t)
		}
	}
	return ""
}
