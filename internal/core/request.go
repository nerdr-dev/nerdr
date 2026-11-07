package core

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Request is a change a client asks of core: what the user did that
// changes core's state, rather than only what the client shows. A client
// in core's own process does it there and then (Do, with Mu held); one in
// a process of its own sends it, and core does it on its loop. Either way
// the client sees what came of it in core's state, and in what core tells
// it.
//
// A request names tabs and panes by TabRef and PaneRef, never by pointer,
// and goes as JSON (see EncodeRequest): a tab closed meanwhile is not
// found, and the request does nothing. One that makes a tab or a pane
// carries the ID it is to have, made by the client, so the client can go
// to it once it is there.
type Request interface {
	Do(m *Core) Cmd
}

// requestTypes are the requests by name, as they go as JSON. Every
// Request type is here; see TestEveryRequestRegistered.
var requestTypes = map[string]reflect.Type{}

func registerRequests(rs ...Request) {
	for _, r := range rs {
		ty := reflect.TypeOf(r)
		requestTypes[ty.Name()] = ty
	}
}

type requestJSON struct {
	Op  string          `json:"op"`
	Req json.RawMessage `json:"req"`
}

// EncodeRequest is r as JSON, named by its type.
func EncodeRequest(r Request) ([]byte, error) {
	ty := reflect.TypeOf(r)
	if requestTypes[ty.Name()] != ty {
		return nil, fmt.Errorf("request %s is not registered", ty)
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return json.Marshal(requestJSON{Op: ty.Name(), Req: b})
}

// DecodeRequest is the request EncodeRequest made b of.
func DecodeRequest(b []byte) (Request, error) {
	var j requestJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	ty := requestTypes[j.Op]
	if ty == nil {
		return nil, fmt.Errorf("no request %q", j.Op)
	}
	v := reflect.New(ty)
	if err := json.Unmarshal(j.Req, v.Interface()); err != nil {
		return nil, fmt.Errorf("%s: %w", j.Op, err)
	}
	return v.Elem().Interface().(Request), nil
}

// RequestEv is a request from a client in a process of its own, for the
// loop to do; Done, when set, is called once it is done, or dropped.
type RequestEv struct {
	R    Request
	Done func()
}

// Run does the request, with Mu held, and calls Done.
func (ev RequestEv) Run(m *Core) Cmd {
	if ev.Done != nil {
		defer ev.Done()
	}
	return ev.R.Do(m)
}
