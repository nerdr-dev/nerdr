package app

import "github.com/nerdr-dev/nerdr/internal/core"

func (m *Model) focusFiles(msg core.FocusFilesMsg) {
	t := msg.T
	if m.TabIndex(t) < 0 || t.FocusVisit != msg.Visit {
		return
	}
	v := msg.Visit
	if msg.Returning {
		v.After = msg.Files
	} else {
		v.Before = msg.Files
	}
	if v.Before == nil || v.After == nil {
		return
	}
	t.Unseen = map[string]map[string]bool{}
	for p, before := range v.Before {
		after, ok := v.After[p]
		if !ok {
			continue
		}
		t.Unseen[p] = map[string]bool{}
		for f, stamp := range after {
			if before[f] != stamp {
				t.Unseen[p][f] = true
			}
		}
		for f := range before {
			if _, ok := after[f]; !ok {
				t.Unseen[p][f] = true
			}
		}
	}
	if t == m.Cur() {
		m.review = m.review.SetUnseen(t.Unseen)
	}
}
