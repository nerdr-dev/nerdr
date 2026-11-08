package spawn

import (
	"strconv"
	"strings"
)

// LocalArg is how a nerdr that runs its programs itself, with no holder
// (NERDR_HOLDER=0, or started by a program in a tab), runs: it execs
// itself with it at its start, so that its command line says what it is
// to a core that finds it among the processes a client runs under.
const LocalArg = "--nerdr-runs-its-programs"

// IsHolder reports whether args are the command line of a nerdr holder,
// `nerdr holder -- CORE`, or one an upgrade or a watch started, however
// nerdr was named and from whichever version.
func IsHolder(args []string) bool {
	return len(args) > 2 && args[1] == "holder" && (args[2] == "--" || args[2] == "--resume" || args[2] == "--watch")
}

// IsNerdr reports whether args are the command line of a nerdr that runs
// programs: a holder, or a nerdr that runs them itself (see LocalArg).
func IsNerdr(args []string) bool {
	return IsHolder(args) || len(args) == 2 && args[1] == LocalArg
}

// Process is a process, as its id and when it started (see ProcInfo) have it:
// one given the id of one that has ended has another start.
type Process struct {
	Pid   int
	Start uint64
}

// Branch is pid and the processes it runs under, up to the first that is
// a nerdr that runs programs (see IsNerdr), that one left out; under says
// whether there is one. Where nerdr cannot read the process table, there
// is none.
func Branch(pid int) (branch []Process, under bool) {
	for i := 0; i < 64 && pid > 1; i++ {
		if args, err := Args(pid); err == nil && IsNerdr(args) {
			return branch, true
		}
		next, start, err := ProcInfo(pid)
		if err != nil {
			return branch, false
		}
		branch = append(branch, Process{pid, start})
		if next == pid {
			break
		}
		pid = next
	}
	return branch, false
}

// FormatProcs is ps as "pid.start pid.start ...", for an environment
// variable; ParseProcs reads it back, leaving out what it cannot read.
func FormatProcs(ps []Process) string {
	var b strings.Builder
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.Itoa(p.Pid) + "." + strconv.FormatUint(p.Start, 10))
	}
	return b.String()
}

// ParseProcs reads what FormatProcs wrote.
func ParseProcs(s string) []Process {
	var ps []Process
	for _, f := range strings.Fields(s) {
		pid, start, ok := strings.Cut(f, ".")
		p, err1 := strconv.Atoi(pid)
		st, err2 := strconv.ParseUint(start, 10, 64)
		if ok && err1 == nil && err2 == nil && p > 0 && st > 0 {
			ps = append(ps, Process{p, st})
		}
	}
	return ps
}
