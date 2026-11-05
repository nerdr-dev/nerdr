package core

import (
	"os"
	"time"

	"github.com/nerdr-dev/nerdr/internal/term"
)

// pluginsCheckEvery is how long a look at the plugins' folders is
// trusted: the screen is drawn on every frame.
const PluginsCheckEvery = 2 * time.Second

// pluginUI is a plugin's screen, started in tab t.
type PluginUI struct {
	T    *Tab
	Pane *term.Pane
	// events is where it is told of agents' runs of the plugin.
	Events *os.File
	// dir is the plugin's folder, and key the content it was started from.
	Dir, Key string
}

// close stops u's program, and takes its events file away.
func (u *PluginUI) Close() {
	if u.Events != nil {
		_ = u.Events.Close()
		_ = os.Remove(u.Events.Name())
	}
	go u.Pane.Close()
}

// pluginGoneAfter is how long a plugin's folder may be there without its
// plugin.toml before its screens and runs are taken to be orphaned: an
// editor writes plugin.toml by rename.
const PluginGoneAfter = 5 * time.Second

// running reports whether u's program still runs.
func (u *PluginUI) Running() bool {
	if u == nil || u.Pane == nil {
		return false
	}
	exited, _, _ := u.Pane.Exited()
	return !exited
}

// maxPreviewLines is the most of a plugin's folder the user is shown to
// scroll through; one with more says to read the rest in the folder.
const MaxPreviewLines = 5000

// maxPreviewFile is the most of one file the question reads to show: a
// large one is read in its folder instead.
const MaxPreviewFile = 1 << 20

func HomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}
