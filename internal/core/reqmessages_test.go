package core

import (
	"testing"

	"github.com/nerdr-dev/nerdr/internal/state"
)

// TestMessageRequestsDuplicates: of two messages the same, a request is
// about the one the user picked, not the first.
func TestMessageRequestsDuplicates(t *testing.T) {
	rep, mid := state.Message{Text: "repeat"}, state.Message{Text: "between"}
	setup := func(t *testing.T) (*Core, *Tab) {
		m := restored(t)
		a := m.Tabs[0]
		a.Messages = []state.Message{rep, mid, rep}
		return m, a
	}
	picked := func(m *Core, a *Tab) MsgRef { return m.MsgRefOf(a, TaskRef{A: a, I: 2}) }

	t.Run("toggle", func(t *testing.T) {
		m, a := setup(t)
		ToggleAuto{M: picked(m, a)}.Do(m)
		if a.Messages[0].Auto || !a.Messages[2].Auto {
			t.Errorf("toggled the wrong one: %+v", a.Messages)
		}
	})
	t.Run("move", func(t *testing.T) {
		m, a := setup(t)
		a.Messages[2].Auto = true // to tell them apart after
		MoveMessage{M: picked(m, a), By: -1}.Do(m)
		if !a.Messages[1].Auto || a.Messages[2] != mid {
			t.Errorf("moved the wrong one: %+v", a.Messages)
		}
	})
	t.Run("drop", func(t *testing.T) {
		m, a := setup(t)
		a.Messages[2].Auto = true
		DropMessage{M: picked(m, a)}.Do(m)
		if len(a.Messages) != 2 || a.Messages[0] != rep || a.Messages[1] != mid {
			t.Errorf("dropped the wrong one: %+v", a.Messages)
		}
	})
	t.Run("edit", func(t *testing.T) {
		m, a := setup(t)
		EditMessage{M: picked(m, a), To: []TabRef{m.Ref(a)}, Text: "changed"}.Do(m)
		if a.Messages[0] != rep || a.Messages[2].Text != "changed" {
			t.Errorf("changed the wrong one: %+v", a.Messages)
		}
	})
	t.Run("moved since", func(t *testing.T) {
		// The one picked moved before the request came: it is found where
		// it is now.
		m, a := setup(t)
		r := m.MsgRefOf(a, TaskRef{A: a, I: 1})
		a.Messages = []state.Message{mid, rep, rep}
		DropMessage{M: r}.Do(m)
		if len(a.Messages) != 2 || a.Messages[0] != rep {
			t.Errorf("dropped the wrong one: %+v", a.Messages)
		}
	})
}

// TestAddMessagesSaysWhatItQueued covers a message with a token: core
// tells the client whether it queued it for every agent it was for, as
// what follows (review comments taken away) depends on it.
func TestAddMessagesSaysWhatItQueued(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	kept := &keptClient{}
	tab := &Tab{Tab: state.Tab{ID: "a", Name: "a", Dir: t.TempDir()}}
	m := &Core{Client: kept, Root: t.TempDir(), Tabs: []*Tab{tab}}

	AddMessages{To: []TabRef{{Tab: "a"}}, Text: "one"}.Do(m)
	if len(kept.evs) != 0 || len(tab.Messages) != 1 {
		t.Fatalf("without a token: told %v, queued %v", kept.evs, tab.Messages)
	}
	AddMessages{To: []TabRef{{Tab: "a"}, {Tab: "gone"}}, Text: "two", Token: "t1"}.Do(m)
	AddMessages{To: []TabRef{{Tab: "a"}}, Text: "three", Token: "t2"}.Do(m)
	want := []Event{MessagesQueuedMsg{Token: "t1", Missing: 1}, MessagesQueuedMsg{Token: "t2"}}
	if len(kept.evs) != 2 || kept.evs[0] != want[0] || kept.evs[1] != want[1] {
		t.Fatalf("told %v, want %v", kept.evs, want)
	}
	// With a token, for all or none: one of its agents gone, "two" was
	// queued for neither, so sending it again reaches a only once.
	if len(tab.Messages) != 2 || tab.Messages[0].Text != "one" || tab.Messages[1].Text != "three" {
		t.Fatalf("queued %v", tab.Messages)
	}
}
