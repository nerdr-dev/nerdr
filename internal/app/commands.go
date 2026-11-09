package app

// commandOff says why command run cannot do anything now, or "". z v
// needs a second tab in the active tab's repository.
func (m *Model) commandOff(run string) string {
	switch run {
	case "t r", "a c":
		return m.reviewOff()
	case "C":
		if tabRepo(m.Cur()) == "" {
			return "a branch is stacked on a tab with one repository"
		}
	case "i r":
		return m.Core.ReposOff(m.Cur())
	case "z v":
		if m.splitTab() == nil && m.neighbour(m.Cur()) == nil {
			return "two tabs need a second tab in this repository"
		}
	}
	return ""
}
