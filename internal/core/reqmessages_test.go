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
