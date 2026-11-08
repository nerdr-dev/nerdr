package spawn

// IsHolder reports whether args are the command line of a nerdr holder,
// `nerdr holder ...`, however nerdr was named and from whichever version.
func IsHolder(args []string) bool { return len(args) > 1 && args[1] == "holder" }

// UnderHolder reports whether pid, or a process it runs under, is a nerdr
// holder; false where nerdr cannot read the process table.
func UnderHolder(pid int) bool {
	for i := 0; i < 64 && pid > 1; i++ {
		if args, err := Args(pid); err == nil && IsHolder(args) {
			return true
		}
		next, _, err := ProcInfo(pid)
		if err != nil || next == pid {
			return false
		}
		pid = next
	}
	return false
}
