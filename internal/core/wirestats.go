package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// NERDR_WIRE_STATS names a file a core writes, every wireStatsEvery, what
// it sent its client since it started: the bytes of each kind of message,
// and of the parts of its state, which fields changed to have them sent.
// For finding what a slow connection is filled with.

var wireStatsPath = os.Getenv("NERDR_WIRE_STATS")

const wireStatsEvery = 10 * time.Second

type wireStat struct{ n, bytes int }

var wireStats = struct {
	sync.Mutex
	start  time.Time
	dumped time.Time
	kinds  map[string]*wireStat
	// fields counts, for each part key's kind, the top-level fields
	// that differed from the part sent before.
	fields map[string]*wireStat
	last   map[string]map[string]json.RawMessage
}{kinds: map[string]*wireStat{}, fields: map[string]*wireStat{}, last: map[string]map[string]json.RawMessage{}}

func wireStatsOn() bool { return wireStatsPath != "" }

func addStat(m map[string]*wireStat, k string, n int) {
	s := m[k]
	if s == nil {
		s = &wireStat{}
		m[k] = s
	}
	s.n++
	s.bytes += n
}

// noteWire counts a message of kind sent, of n bytes.
func noteWire(kind string, n int) {
	if !wireStatsOn() {
		return
	}
	wireStats.Lock()
	defer wireStats.Unlock()
	if wireStats.start.IsZero() {
		wireStats.start, wireStats.dumped = time.Now(), time.Now()
	}
	addStat(wireStats.kinds, kind, n)
	if time.Since(wireStats.dumped) >= wireStatsEvery {
		wireStats.dumped = time.Now()
		dumpWireStats()
	}
}

// noteParts counts the parts sent, and which of their fields changed.
func noteParts(parts []Part) {
	if !wireStatsOn() {
		return
	}
	for _, p := range parts {
		kind := p.Key
		if strings.HasPrefix(kind, partTabPre) {
			kind = "tab"
		}
		enc, _ := json.Marshal(p)
		noteWire("part:"+kind, len(enc))
		if p.Body == nil {
			continue // a patch: its fields are those in it
		}
		var fields map[string]json.RawMessage
		if kind == "tab" {
			// The tab's agents' fields, the tab's own first.
			var tp struct {
				Agents []map[string]json.RawMessage `json:"agents"`
			}
			if json.Unmarshal(p.Body, &tp) != nil {
				continue
			}
			fields = map[string]json.RawMessage{}
			for i, a := range tp.Agents {
				for k, v := range a {
					fields[fmt.Sprintf("%d.%s", min(i, 1), k)+"#"+string(a["ID"])] = v
				}
			}
		} else if json.Unmarshal(p.Body, &fields) != nil {
			continue
		}
		wireStats.Lock()
		old := wireStats.last[p.Key]
		for k, v := range fields {
			if o, ok := old[k]; ok && bytes.Equal(o, v) {
				continue
			}
			name, _, _ := strings.Cut(k, "#")
			addStat(wireStats.fields, kind+"."+name, len(v))
		}
		wireStats.last[p.Key] = fields
		wireStats.Unlock()
	}
}

// dumpWireStats writes the counts out. wireStats is held.
func dumpWireStats() {
	secs := time.Since(wireStats.start).Seconds()
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s, %.0fs\n", time.Now().Format("15:04:05"), secs)
	write := func(m map[string]*wireStat, top int) {
		keys := slices.SortedFunc(maps.Keys(m), func(a, c string) int { return m[c].bytes - m[a].bytes })
		for i, k := range keys {
			if i == top {
				break
			}
			s := m[k]
			fmt.Fprintf(&b, "%10d B %8.0f B/s %7d msgs %8d B/msg  %s\n", s.bytes, float64(s.bytes)/secs, s.n, s.bytes/max(s.n, 1), k)
		}
	}
	write(wireStats.kinds, 40)
	b.WriteString("changed fields (bytes of the field):\n")
	write(wireStats.fields, 40)
	_ = os.WriteFile(wireStatsPath, []byte(b.String()), 0o600)
}
