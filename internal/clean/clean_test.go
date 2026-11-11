package clean

import "testing"

func TestText(t *testing.T) {
	for in, want := range map[string]string{
		"plain\n\tindented":            "plain\n\tindented",
		"a\r\nb\rc":                    "a\nb\nc",
		"red \x1b[31mtext\x1b[0m":      "red [31mtext[0m",
		"clip \x1b]52;c;aGk=\x07 done": "clip ]52;c;aGk= done",
		"c1 \u009b6n":                  "c1 6n",
		"del\x7f":                      "del",
		"æøå ✓":                        "æøå ✓",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLine(t *testing.T) {
	if got := Line("one\ntwo\tthree\r\x1b[2J"); got != "one two three [2J" {
		t.Errorf("Line = %q", got)
	}
}

func TestMarked(t *testing.T) {
	mark := func(s string) string { return "<" + s + ">" }
	for in, want := range map[string]struct {
		out string
		n   int
	}{
		"npm ci":                        {"npm ci", 0},
		"æøå ✓ 日本":                      {"æøå ✓ 日本", 0},
		"a\x1b[8mhid\x1b[0m":            {`a<\u001b>[8mhid<\u001b>[0m`, 2},
		"x\ty\nz\r":                     {`x<\t>y<\n>z<\r>`, 3},
		"c1 \u009b6n del\x7f":           {`c1 <\u009b>6n del<\u007f>`, 2},
		"rm \u202erf- \u2066x\u2069":    {`rm <\u202e>rf- <\u2066>x<\u2069>`, 3},
		"a\u200bb\u200dc\u200fd\ufeffe": {`a<\u200b>b<\u200d>c<\u200f>d<\ufeff>e`, 4},
		"sep\u2028par\u2029":            {`sep<\u2028>par<\u2029>`, 2},
		"tag\U000e0041 fill\u3164":      {`tag<\U000e0041> fill<\u3164>`, 2},
		"bad\xff":                       {`bad<\xff>`, 1},
	} {
		if got, n := Marked(in, mark); got != want.out || n != want.n {
			t.Errorf("Marked(%q) = %q, %d, want %q, %d", in, got, n, want.out, want.n)
		}
	}
}
