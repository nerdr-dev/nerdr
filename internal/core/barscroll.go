package core

// StatusRank orders the lights by how much they want a look.
func StatusRank(s Status) int {
	switch s {
	case StBlocked:
		return 7
	case StDone:
		return 6
	case StWorking:
		return 5
	case StBackground:
		return 4
	case StIdle:
		return 3
	case StStarting, StUnknown:
		return 2
	}
	return 1
}
