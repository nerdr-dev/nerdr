package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// A part of core's state that changed goes to a client that can take it
// (attach.FeaturePartPatch) as how it differs from the one sent before: a
// tab's agent going from working to done changes a few bytes of the tab,
// and the client is sent those, not the whole tab again. The client keeps
// the last of each part, and makes the new one from it and the patch.

// jsonPatch is how one JSON value differs from another: V the new value in
// its place; or, of an object, O how each field that changed differs and D
// the fields gone; or, of an array of as many elements, A how each element
// that changed differs, by its index.
type jsonPatch struct {
	V json.RawMessage       `json:"v,omitempty"`
	O map[string]*jsonPatch `json:"o,omitempty"`
	D []string              `json:"d,omitempty"`
	A map[string]*jsonPatch `json:"a,omitempty"`
}

// diffJSON is how new differs from old, both valid JSON as json.Marshal
// writes it; nil when they are the same.
func diffJSON(old, new json.RawMessage) *jsonPatch {
	if bytes.Equal(old, new) {
		return nil
	}
	whole := &jsonPatch{V: new}
	switch {
	case kindOf(old) == '{' && kindOf(new) == '{':
		var o, n map[string]json.RawMessage
		if json.Unmarshal(old, &o) != nil || json.Unmarshal(new, &n) != nil {
			return whole
		}
		p := &jsonPatch{}
		for k, nv := range n {
			if d := diffJSON(o[k], nv); d != nil {
				if p.O == nil {
					p.O = map[string]*jsonPatch{}
				}
				p.O[k] = d
			}
		}
		for k := range o {
			if _, ok := n[k]; !ok {
				p.D = append(p.D, k)
			}
		}
		if p.O == nil && p.D == nil {
			return whole // the same but for how it is written
		}
		return p
	case kindOf(old) == '[' && kindOf(new) == '[':
		var o, n []json.RawMessage
		if json.Unmarshal(old, &o) != nil || json.Unmarshal(new, &n) != nil || len(o) != len(n) {
			return whole
		}
		p := &jsonPatch{A: map[string]*jsonPatch{}}
		for i := range n {
			if d := diffJSON(o[i], n[i]); d != nil {
				p.A[strconv.Itoa(i)] = d
			}
		}
		if len(p.A) == 0 {
			return whole
		}
		return p
	}
	return whole
}

// kindOf is the first byte of v: '{' for an object, '[' for an array.
func kindOf(v json.RawMessage) byte {
	v = bytes.TrimLeft(v, " \t\r\n")
	if len(v) == 0 {
		return 0
	}
	return v[0]
}

// applyJSON is old changed as p says.
func applyJSON(old json.RawMessage, p *jsonPatch) (json.RawMessage, error) {
	switch {
	case p == nil:
		return old, nil
	case p.V != nil:
		return p.V, nil
	case p.A != nil:
		var a []json.RawMessage
		if err := json.Unmarshal(old, &a); err != nil {
			return nil, fmt.Errorf("patch of an array: %w", err)
		}
		for k, d := range p.A {
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(a) {
				return nil, fmt.Errorf("patch of element %q of %d", k, len(a))
			}
			if a[i], err = applyJSON(a[i], d); err != nil {
				return nil, err
			}
		}
		return json.Marshal(a)
	case p.O != nil || p.D != nil:
		var o map[string]json.RawMessage
		if err := json.Unmarshal(old, &o); err != nil {
			return nil, fmt.Errorf("patch of an object: %w", err)
		}
		if o == nil {
			o = map[string]json.RawMessage{}
		}
		for k, d := range p.O {
			v, err := applyJSON(o[k], d)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			o[k] = v
		}
		for _, k := range p.D {
			delete(o, k)
		}
		return json.Marshal(o)
	}
	return nil, errors.New("an empty patch")
}
