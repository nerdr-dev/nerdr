package core

// Neediest is the tab that most wants the user — blocked first, then
// finished, then working, then waiting on a background process, the most
// recent change winning a tie — or nil
// when every agent is idle.
func (l TabList) Neediest() (int, *Tab) {
	best, bi := (*Tab)(nil), -1
	for i, t := range l {
		if t.Attention() == 0 {
			continue
		}
		if best == nil || t.Attention() > best.Attention() ||
			(t.Attention() == best.Attention() && t.Changed.After(best.Changed)) {
			best, bi = t, i
		}
	}
	return bi, best
}
