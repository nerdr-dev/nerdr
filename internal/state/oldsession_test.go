package state_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/state/statetest"
)

// TestOldSessionsAreRead: the session files of released versions are
// read, and nothing in them is lost: written again, each keeps every
// value the old one had.
func TestOldSessionsAreRead(t *testing.T) {
	files := append(statetest.Old(t, "tabs"), statetest.Old(t, "full")...)
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			root := t.TempDir()
			old := statetest.Put(t, f, root)
			s, err := state.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Tabs) == 0 {
				t.Fatal("no tabs")
			}
			now, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			var was, is any
			_ = json.Unmarshal(old, &was)
			_ = json.Unmarshal(now, &is)
			if lost := lostFrom(was, is, ""); len(lost) > 0 {
				t.Errorf("lost in reading:\n%s", strings.Join(lost, "\n"))
			}
		})
	}
}

// lostFrom is what of was, a value read from JSON, is not in is, by path.
func lostFrom(was, is any, path string) []string {
	switch w := was.(type) {
	case map[string]any:
		i, ok := is.(map[string]any)
		if !ok {
			return []string{path + ": gone"}
		}
		var lost []string
		for k, v := range w {
			lost = append(lost, lostFrom(v, i[k], path+"."+k)...)
		}
		return lost
	case []any:
		i, ok := is.([]any)
		if !ok || len(i) != len(w) {
			return []string{fmt.Sprintf("%s: %v, now %v", path, was, is)}
		}
		var lost []string
		for n, v := range w {
			lost = append(lost, lostFrom(v, i[n], fmt.Sprintf("%s[%d]", path, n))...)
		}
		return lost
	}
	if !reflect.DeepEqual(was, is) {
		return []string{fmt.Sprintf("%s: %v, now %v", path, was, is)}
	}
	return nil
}
