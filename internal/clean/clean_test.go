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
