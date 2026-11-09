package core

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// What core tells a client in a process of its own goes as JSON, named by
// its type, as a Request does the other way: the events of Client.Tell, and
// those the loop hands the client (see Loop), to draw again above all.
// Every type core tells is here; see TestEveryEventRegistered.

var eventTypes = map[string]reflect.Type{}

func registerEvents(evs ...Event) {
	for _, ev := range evs {
		ty := reflect.TypeOf(ev)
		eventTypes[ty.Name()] = ty
	}
}

func init() {
	registerEvents(
		ToastMsg(""), NoticeMsg(""), LightMsg{}, ReviewNewsMsg{}, ReviewMsg{},
		PaneOpenedMsg{}, PaneClosedMsg{}, PanesMsg{}, LayoutMsg{}, StartedMsg{},
		TermsMsg{}, SwitchedMsg{}, SpinoffAskMsg{}, SwitchAskMsg{}, IssuesMsg{},
		IssueNewsMsg{}, IssuesDueMsg{}, PRsMsg{}, PRNewsMsg{}, PRMarkMsg{},
		OverlapsMsg{}, DetectStartMsg{}, DetectDoneMsg{}, PluginUIStartedMsg{},
		PluginUIGoneMsg{}, PluginRunMsg{}, AskMsg{}, AnsweredMsg{}, TypedInMsg{},
		UnseenMsg{}, SetupAnsweredMsg{}, SpinoffFailedMsg{}, SpinoffNextMsg{},
		ReposDoneMsg{}, ChangesMsg{}, BranchDoneMsg{}, CloseInfoMsg{}, ReposScannedMsg{}, RepoMadeMsg{}, RepoCheckedMsg{}, PlanMsg{}, IssueMsg{}, SetupProgressMsg{},
		WorktreesMadeMsg{}, RepoPRsMsg{}, CheckoutMsg{}, ForkFetchedMsg{},
		PRFetchedMsg{}, ReviewReadMsg{}, WatchingMsg{}, WorktreeChangedMsg{},

		ChangedMsg{}, RedrawMsg{}, FrameMsg{}, InfoTickMsg{}, WirePanic{},
	)
}

// WirePanic is a PanicMsg as it goes: its value as text.
type WirePanic struct {
	Value string
	Stack []byte
}

func (p WirePanic) String() string { return p.Value + "\n" + string(p.Stack) }

type eventJSON struct {
	Ev   string          `json:"ev"`
	Body json.RawMessage `json:"body"`
}

// EncodeEvent is ev as JSON, named by its type. A PanicMsg goes as a
// WirePanic.
func EncodeEvent(ev Event) ([]byte, error) {
	if p, ok := ev.(PanicMsg); ok {
		ev = WirePanic{Value: fmt.Sprint(p.Value), Stack: p.Stack}
	}
	ty := reflect.TypeOf(ev)
	if ty == nil || eventTypes[ty.Name()] != ty {
		return nil, fmt.Errorf("event %v is not registered", ty)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	return json.Marshal(eventJSON{Ev: ty.Name(), Body: b})
}

// DecodeEvent is the event EncodeEvent made b of.
func DecodeEvent(b []byte) (Event, error) {
	var j eventJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	ty := eventTypes[j.Ev]
	if ty == nil {
		return nil, fmt.Errorf("no event %q", j.Ev)
	}
	v := reflect.New(ty)
	if err := json.Unmarshal(j.Body, v.Interface()); err != nil {
		return nil, fmt.Errorf("%s: %w", j.Ev, err)
	}
	return v.Elem().Interface(), nil
}

// Sendable reports whether ev goes to a client in another process.
func Sendable(ev Event) bool {
	if _, ok := ev.(PanicMsg); ok {
		return true
	}
	ty := reflect.TypeOf(ev)
	return ty != nil && eventTypes[ty.Name()] == ty
}
