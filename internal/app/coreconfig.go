package app

import (
	"cmp"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// Core's own settings (config.ScopeCore: the agents, where worktrees go,
// what is installed for the agents, the reviews) are in the config file
// on the machine core runs on, which may not be this one. The TUI asks
// core to write them (core.EditConfig), and has them from core's state;
// it writes only its own, here.

// editCoreConfig has core set e's keys in its config file. what names the
// change in what the TUI says should it not be saved.
func (m *Model) editCoreConfig(what string, e core.EditConfig) tea.Cmd {
	m.cfgJob++
	e.Job = "config-" + strconv.Itoa(m.cfgJob)
	if m.cfgWhat == nil {
		m.cfgWhat = map[string]string{}
	}
	m.cfgWhat[e.Job] = what
	return m.do(e)
}

// onConfigSaved says when core could not save a change of its settings,
// and has the settings dialog read core's file again.
func (m *Model) onConfigSaved(ev core.ConfigSavedMsg) tea.Cmd {
	what, ok := m.cfgWhat[ev.Job]
	if !ok {
		return nil // another client's
	}
	delete(m.cfgWhat, ev.Job)
	if ev.Err != "" {
		m.toast(cmp.Or(what, "settings") + ": not saved in " + m.ConfigPath() + ": " + ev.Err)
	}
	if d, ok := m.dialog.(*settings); ok {
		d.reload()
		return m.readCoreConfig(d)
	}
	return nil
}

// readCoreConfig has d show what core's config file sets of core's own
// settings: the same file as the TUI's when core runs in this process,
// else read by core where it runs.
func (m *Model) readCoreConfig(d *settings) tea.Cmd {
	if m.remote == nil {
		d.coreRaw, d.coreErr = d.raw, ""
		return nil
	}
	m.cfgJob++
	d.coreJob = "config-" + strconv.Itoa(m.cfgJob)
	m.tellCore(core.ReadConfig{Job: d.coreJob})
	return nil
}

// onConfigText takes in core's config file, as the settings dialog asked.
func (m *Model) onConfigText(ev core.ConfigTextMsg) {
	d, ok := m.dialog.(*settings)
	if !ok || d.coreJob != ev.Job {
		return
	}
	raw, err := config.RawText(ev.Text)
	d.coreRaw, d.coreErr = raw, ev.Err
	if err != nil && ev.Err == "" {
		d.coreErr = err.Error()
	}
}
