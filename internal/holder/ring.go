package holder

// DefaultRing is how much of a program's output the holder keeps when a
// spawn does not say: what a core started after a crash or update reads
// from its last checkpoint on. A busy agent's screen is some tens of KB a
// second, so 4 MiB lasts a core's restart of half a minute or more; a
// redraw of top needed 256 KB. The ring grows as output comes, so a quiet
// shell costs little, and fifteen busy panes stay near 60 MiB.
const DefaultRing = 4 << 20

// MaxRing is the most a spawn can ask the holder to keep.
const MaxRing = 64 << 20

// ring is the last bytes of a stream, at most max of them, by their
// position in it from 0. When it is full the oldest go: a reader that
// asks for them is told how many it lost, and the writer never waits.
type ring struct {
	max  int
	buf  []byte // grows to max
	head int    // where in buf the oldest byte is
	n    int    // how many bytes are kept
	end  int64  // the position after the last byte
}

func newRing(max int) *ring {
	if max <= 0 {
		max = DefaultRing
	}
	return &ring{max: max}
}

// start is the position of the oldest byte kept.
func (r *ring) start() int64 { return r.end - int64(r.n) }

func (r *ring) write(p []byte) {
	if len(p) > r.max {
		// Only the last max bytes can be kept.
		r.end += int64(len(p) - r.max)
		r.n = 0
		r.head = 0
		p = p[len(p)-r.max:]
	}
	if grow := min(r.max-len(r.buf), len(p)); grow > 0 && r.head == 0 && r.n == len(r.buf) {
		r.buf = append(r.buf, p[:grow]...)
		r.n += grow
		r.end += int64(grow)
		p = p[grow:]
	}
	if len(p) > 0 && len(r.buf) < r.max {
		// Wrapped before growing all the way; make room once.
		r.buf = append(r.buf, make([]byte, r.max-len(r.buf))...)
	}
	for len(p) > 0 {
		i := (r.head + r.n) % len(r.buf)
		k := copy(r.buf[i:min(len(r.buf), i+len(p))], p)
		p = p[k:]
		r.end += int64(k)
		if over := r.n + k - len(r.buf); over > 0 {
			r.head = (r.head + over) % len(r.buf)
			r.n = len(r.buf)
		} else {
			r.n += k
		}
	}
}

// read copies into b what is kept from pos on, and says where what it
// copied begins: pos, or later when bytes from pos on are gone.
func (r *ring) read(pos int64, b []byte) (n int, from int64) {
	from = max(pos, r.start())
	if from >= r.end {
		return 0, min(from, r.end)
	}
	for n < len(b) && from+int64(n) < r.end {
		off := int(from + int64(n) - r.start())
		i := (r.head + off) % len(r.buf)
		seg := r.buf[i:min(len(r.buf), i+r.n-off)]
		n += copy(b[n:], seg)
	}
	return n, from
}

// bytes is all that is kept, oldest first.
func (r *ring) bytes() []byte {
	b := make([]byte, r.n)
	n, _ := r.read(r.start(), b)
	return b[:n]
}

// restore makes an empty ring hold b, the bytes before end.
func (r *ring) restore(b []byte, end int64) {
	r.buf, r.head, r.n = nil, 0, 0
	r.end = end - int64(len(b))
	r.write(b)
}
