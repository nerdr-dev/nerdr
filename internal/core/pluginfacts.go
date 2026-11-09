package core

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/plugin"
)

// What F5 reads and writes of the user's plugins, where core is: the
// plugins run here. A client in core's process calls these itself; one
// with a copy of core's state (Copy) asks core, by a Job of its own, and
// is told the answer by it.

func init() {
	registerRequests(LookForPlugins{}, AskPluginFacts{}, AllowPluginKey{}, DisallowPlugin{})
}

// PluginsLook is the user's plugins as last looked for (at At), and
// whether the user allowed each as it was then, by folder.
type PluginsLook struct {
	At      time.Time
	List    []plugin.Plugin
	Allowed map[string]bool
}

// ReadPlugins looks for the user's plugins, and whether each is allowed.
// It reads every plugin's folder, for its Key.
func ReadPlugins() PluginsLook {
	l := PluginsLook{At: time.Now(), List: plugin.Find(plugin.UserDir(config.Path())), Allowed: map[string]bool{}}
	for _, p := range l.List {
		l.Allowed[p.Dir] = plugin.Allowed(PluginsAllowedFile(), p)
	}
	return l
}

// LookForPlugins has core look for the user's plugins again, away from
// its loop, for a client's copy: they come in Core.Plugins.
type LookForPlugins struct{}

// pluginsLooked is LookForPlugins' look.
type pluginsLooked struct{ look PluginsLook }

func (LookForPlugins) Do(m *Core) Cmd {
	return func() Event { return pluginsLooked{ReadPlugins()} }
}

// PluginFacts is plugin P as it is now, its folder read again: Key is
// what allowing it allows (see plugin.Fresh), Err why it could not be
// read, AllowedKey whether it is allowed as it is, AllowedDir whether
// some version of it in its folder is, and Preview what is in its folder,
// for the user to read before allowing it.
type PluginFacts struct {
	P          plugin.Plugin
	Key        string
	Err        string
	AllowedKey bool
	AllowedDir bool
	Preview    []PreviewFile
}

// ReadPluginFacts reads p as it is now.
func ReadPluginFacts(p plugin.Plugin) PluginFacts {
	q, key, err := plugin.Fresh(p)
	file := PluginsAllowedFile()
	f := PluginFacts{P: q, Key: key, AllowedDir: plugin.AllowedDir(file, q.Dir)}
	if err != nil {
		f.Err = err.Error()
		return f
	}
	f.AllowedKey = plugin.AllowedKey(file, key)
	f.Preview = ReadPreview(q.Dir)
	return f
}

// AskPluginFacts asks for the facts of plugin P, by Job: told as a
// PluginFactsMsg.
type AskPluginFacts struct {
	Job string
	P   plugin.Plugin
}

// PluginFactsMsg is AskPluginFacts' answer, for Job.
type PluginFactsMsg struct {
	Job   string
	Facts PluginFacts
}

func (q AskPluginFacts) Do(m *Core) Cmd {
	return Later(func() Event { return PluginFactsMsg{Job: q.Job, Facts: ReadPluginFacts(q.P)} })
}

// AllowPlugin keeps that the user allowed p as Key, once its folder is
// still as it was when they were asked: changed reports that it is not,
// and nothing is kept.
func AllowPlugin(p plugin.Plugin, key string) (changed bool, err error) {
	if now, err := p.Key(); err != nil || now != key {
		return true, nil
	}
	return false, plugin.Allow(PluginsAllowedFile(), p, key)
}

// AllowPluginKey is AllowPlugin asked by Job: told as a PluginAllowedMsg.
type AllowPluginKey struct {
	Job string
	P   plugin.Plugin
	Key string
}

// PluginAllowedMsg is what came of AllowPluginKey or DisallowPlugin of
// Job: Changed, the plugin changed since the user was asked, or Err.
type PluginAllowedMsg struct {
	Job     string
	Changed bool
	Err     string
}

func (q AllowPluginKey) Do(m *Core) Cmd {
	return Later(func() Event {
		changed, err := AllowPlugin(q.P, q.Key)
		return PluginAllowedMsg{Job: q.Job, Changed: changed, Err: errText(err)}
	})
}

// DisallowPluginNow takes every allowance of the plugin in dir away, and
// of key, a version of it, wherever it was.
func DisallowPluginNow(dir, key string) error {
	file := PluginsAllowedFile()
	if _, err := plugin.DisallowDir(file, dir); err != nil {
		return err
	}
	if key != "" {
		return plugin.Disallow(file, key)
	}
	return nil
}

// DisallowPlugin is DisallowPluginNow asked by Job: told as a
// PluginAllowedMsg.
type DisallowPlugin struct {
	Job string
	Dir string
	Key string
}

func (q DisallowPlugin) Do(m *Core) Cmd {
	return Later(func() Event {
		return PluginAllowedMsg{Job: q.Job, Err: errText(DisallowPluginNow(q.Dir, q.Key))}
	})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// PreviewFile is a file in a plugin's folder, as the user is shown it
// before allowing the plugin: Rel its path in the folder; Link where it
// points, for a link; else Text its lines, or why there are none.
type PreviewFile struct {
	Rel  string
	Link string `json:",omitempty"`
	Size int64  `json:",omitempty"`
	// Why is why the file is not shown: "special" (not a regular file),
	// "large" (more than MaxPreviewFile), "unreadable" or "binary".
	Why  string   `json:",omitempty"`
	Text []string `json:",omitempty"`
}

// ReadPreview reads what is in the plugin folder dir, for the user to read
// before allowing the plugin: no special file, which could hold the
// reader, and no file larger than MaxPreviewFile.
func ReadPreview(dir string) []PreviewFile {
	var out []PreviewFile
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		f := PreviewFile{Rel: clean.Line(rel)}
		if d.Type()&fs.ModeSymlink != 0 {
			to, _ := os.Readlink(path)
			f.Link = clean.Line(to)
			out = append(out, f)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		f.Size = info.Size()
		switch {
		case !info.Mode().IsRegular():
			f.Why = "special"
		case info.Size() > MaxPreviewFile:
			f.Why = "large"
		default:
			b, err := os.ReadFile(path)
			switch {
			case err != nil:
				f.Why = "unreadable"
			case bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b):
				f.Why, f.Size = "binary", int64(len(b))
			default:
				f.Text = strings.Split(strings.TrimRight(clean.Text(string(b)), "\n"), "\n")
			}
		}
		out = append(out, f)
		return nil
	})
	return out
}

func (m *Core) onPluginsLooked(ev pluginsLooked) {
	if ev.look.At.After(m.Plugins.At) {
		m.Plugins = ev.look
	}
}
