package review

import (
	"strings"
	"testing"
)

// A bot's comment of badges, links and a folded table reads as its words.
func TestReadableComment(t *testing.T) {
	body := "<!-- ccr-overview-v2 -->\n### 🟢 Approval recommended\n\nThe implementation matches. **0 open findings**\n\n" +
		"<details>\n<summary><strong>What changed</strong></summary>\n\n| File | Description |\n| ---- | ----------- |\n" +
		"| `Form.tsx` | Uses the plan |\n</details>\n\n" +
		"## [![Quality Gate Passed](https://sonar/qg.png 'Quality Gate Passed')](https://sonarcloud.io/dashboard?id=x)\n" +
		"**Issues**\n![](https://sonar/passed-16px.png) [0 New issues](https://sonarcloud.io/project/issues?id=x)\n" +
		"<!-- sqra-placement-anchor -->\n[See analysis details on SonarQube Cloud](https://sonarcloud.io/dashboard?id=x)\n" +
		"> quoted &amp; kept\n- a point"
	lines := readable(body, 60)
	got := stripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{
		"🟢 Approval recommended", "The implementation matches. 0 open findings", "▸ What changed",
		"File · Description", "Form.tsx · Uses the plan", "Quality Gate Passed", "0 New issues",
		"See analysis details on SonarQube Cloud", "│ quoted & kept", "• a point",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, gone := range []string{"<!--", "https://", "<details>", "**", "----", "`"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q left in:\n%s", gone, got)
		}
	}
	for _, l := range lines {
		if w := len([]rune(stripANSI(l))); w > 60 {
			t.Errorf("line %d wide: %q", w, l)
		}
	}
}
