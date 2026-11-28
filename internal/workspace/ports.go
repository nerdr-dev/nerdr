package workspace

import "strconv"

// Ports is the range of ports a tab is given, Count of them from First,
// for what runs in it to listen on: two tabs running the same dev server
// would otherwise both want its port. Its programs are told them in
// NERDR_PORT and NERDR_PORT_END (Env); an agent whose sandbox lets those
// through no more than the others asks nerdr (nerdr ports).
type Ports struct {
	First int `json:"first"`
	Count int `json:"count"`
}

// IsZero reports whether p is no range: the tab has none.
func (p Ports) IsZero() bool { return p.First <= 0 || p.Count <= 0 }

// Last is the last port of p.
func (p Ports) Last() int { return p.First + p.Count - 1 }

// Overlaps reports whether p and q share a port.
func (p Ports) Overlaps(q Ports) bool {
	return !p.IsZero() && !q.IsZero() && p.First <= q.Last() && q.First <= p.Last()
}

func (p Ports) String() string {
	if p.IsZero() {
		return ""
	}
	return strconv.Itoa(p.First) + "–" + strconv.Itoa(p.Last())
}

// Env is the variables that tell a program its tab's ports: NERDR_PORT,
// the first, and NERDR_PORT_END, the last. None when p is zero.
func (p Ports) Env() []string {
	if p.IsZero() {
		return nil
	}
	return []string{"NERDR_PORT=" + strconv.Itoa(p.First), "NERDR_PORT_END=" + strconv.Itoa(p.Last())}
}
