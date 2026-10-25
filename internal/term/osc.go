package term

import (
	"strings"
	"unicode/utf8"
)

// The emulator's parser takes the byte 0x9C as ST, the 8-bit string
// terminator, even inside an OSC string, where in UTF-8 it is just as
// likely the middle of a character: ✳, which Claude Code puts before its
// title when it is done, is E2 9C B3. The OSC would then end at 0x9C and
// the rest of the title be printed where the cursor is — in the agent's
// prompt, where the agent does not know it is and so never clears it.
//
// oscFilter sits between the program and the emulator and holds each OSC
// back until it has ended. One with such a character in it is a title
// nerdr sets itself, or for any other OSC, has the character replaced, so
// the emulator never sees the 0x9C; all else goes through as it came.
type oscFilter struct {
	state   int
	buf     []byte // the OSC held back, from its ESC ]
	pending int    // continuation bytes the UTF-8 character in it still needs
	split   bool   // whether a 0x9C is inside a character in it
}

const (
	oscGround = iota
	oscEsc    // after an ESC, held back until it is known whether ] follows
	oscString // inside an OSC, held back
	oscRaw    // inside an OSC too long to hold back, passed through
)

// oscMax is how much of an OSC is held back; a longer one, such as a large
// clipboard copy, is passed through as it comes.
const oscMax = 64 << 10

// write is in filtered onto out, with the title of any OSC 0 or 2 nerdr
// handles itself given to title.
func (f *oscFilter) write(out, in []byte, title func(string)) []byte {
	for _, b := range in {
		switch f.state {
		case oscGround:
			if b == 0x1b {
				f.state = oscEsc
				continue
			}
			out = append(out, b)
		case oscEsc:
			switch b {
			case ']':
				f.state, f.buf, f.pending, f.split = oscString, append(f.buf[:0], 0x1b, ']'), 0, false
			case 0x1b:
				out = append(out, 0x1b)
			default:
				f.state = oscGround
				out = append(out, 0x1b, b)
			}
		case oscString:
			switch {
			case b == 0x07 || b == 0x18 || b == 0x1a || (b == 0x9c && f.pending == 0):
				out = f.end(out, []byte{b}, title)
				f.state = oscGround
			case b == 0x1b:
				// ESC \ ends it; whatever follows the ESC is the
				// emulator's to make sense of, as it would have been.
				out = f.end(out, nil, title)
				f.state = oscEsc
			default:
				f.buf = append(f.buf, b)
				switch {
				case b >= 0x80 && b <= 0xbf && f.pending > 0:
					f.pending--
					if b == 0x9c {
						f.split = true
					}
				case b >= 0xc2 && b <= 0xdf:
					f.pending = 1
				case b >= 0xe0 && b <= 0xef:
					f.pending = 2
				case b >= 0xf0 && b <= 0xf4:
					f.pending = 3
				default:
					f.pending = 0
				}
				if len(f.buf) > oscMax {
					out = append(out, f.buf...)
					f.state = oscRaw
				}
			}
		case oscRaw:
			switch b {
			case 0x1b:
				f.state = oscEsc
				continue
			case 0x07, 0x18, 0x1a, 0x9c:
				f.state = oscGround
			}
			out = append(out, b)
		}
	}
	return out
}

// end is the OSC held back, ended by term (none for an ESC, which the
// caller passes on itself), onto out.
func (f *oscFilter) end(out, term []byte, title func(string)) []byte {
	if !f.split {
		out = append(out, f.buf...)
		return append(out, term...)
	}
	data := string(f.buf[2:])
	if ps, text, ok := strings.Cut(data, ";"); ok && (ps == "0" || ps == "2") {
		title(text)
		return out
	}
	var b strings.Builder
	for _, r := range data {
		if strings.IndexByte(string(r), 0x9c) >= 0 {
			r = utf8.RuneError
		}
		b.WriteRune(r)
	}
	out = append(out, 0x1b, ']')
	out = append(out, b.String()...)
	if len(term) == 0 {
		// The ESC that ended it goes through after it.
		return out
	}
	return append(out, term...)
}
