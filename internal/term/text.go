package term

import "strings"

// Printable renders control characters as text before terminal styling.
// Newlines and tabs remain for callers that lay out multiline content.
//
// It also takes out what makes an emoji one wide character of several
// code points — a variation selector, a skin tone, a zero-width joiner.
// nerdr measures such a sequence as one character two columns wide, as
// it is meant to be; a terminal that draws it code point by code point
// makes it up to six, and a line nerdr cut to fit then wraps and scrolls
// the whole screen. Without them each character is as wide to both.
//
// The invisible characters that only hint at a break or a direction, as
// the zero-width space Copilot puts after each / of a path, go too: nerdr
// measures them as nothing, and Terminal.app draws a column for each.
func Printable(s string) string {
	isControl := func(r rune) bool {
		return r != '\n' && r != '\t' && (r < 0x20 || r >= 0x7f && r < 0xa0)
	}
	if strings.IndexFunc(s, func(r rune) bool { return isControl(r) || emojiJoiner(r) || invisible(r) }) < 0 {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case emojiJoiner(r), invisible(r):
			return -1
		case !isControl(r):
			return r
		case r == 0x7f:
			return '␡'
		case r < 0x20:
			return 0x2400 + r
		default:
			return '�'
		}
	}, s)
}

// emojiJoiner is whether r only joins or changes the emoji before it:
// the text and emoji variation selectors, the zero-width joiner and the
// skin tones.
func emojiJoiner(r rune) bool {
	return r == 0xfe0e || r == 0xfe0f || r == 0x200d || r >= 0x1f3fb && r <= 0x1f3ff
}

// invisible is whether r is a format character with no width of its own
// but the joiner: a soft hyphen, the zero-width space and non-joiner, the
// direction marks and embeddings, the word joiner and the BOM.
func invisible(r rune) bool {
	switch {
	case r == 0xad, r == 0x200b, r == 0x200c, r == 0x200e, r == 0x200f, r == 0xfeff:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// PrintableLine also prevents metadata from adding rows to a terminal pane.
func PrintableLine(s string) string {
	return strings.NewReplacer("\n", "␊", "\t", " ").Replace(Printable(s))
}
