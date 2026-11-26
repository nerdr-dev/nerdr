//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGh is a stand-in for the gh CLI: repoA has one pull request of
// yours, #7 from feat-ci, whose one check is running while $FAKE_GH_PHASE
// holds "running" and has failed once it holds "failed"; its job log says
// why. Everything else has nothing.
const fakeGh = `#!/usr/bin/env bash
phase=$(cat "$FAKE_GH_PHASE" 2>/dev/null)
if [ "$phase" = failed ]; then
  check='{"__typename":"CheckRun","name":"test","workflowName":"CI","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://github.com/o/repoA/actions/runs/42/job/1"}'
else
  check='{"__typename":"CheckRun","name":"test","workflowName":"CI","status":"IN_PROGRESS","conclusion":"","detailsUrl":"https://github.com/o/repoA/actions/runs/42/job/1"}'
fi
pr='{"number":7,"title":"Login","url":"https://github.com/o/repoA/pull/7","state":"OPEN","isDraft":false,"author":{"login":"me"},"updatedAt":"2026-11-01T00:00:00Z","headRefName":"feat-ci","headRefOid":"c0ffee","baseRefName":"main","isCrossRepository":false,"headRepositoryOwner":{"login":"o"},"mergeable":"MERGEABLE","reviewDecision":"","latestReviews":[],"reviews":[],"comments":[],"statusCheckRollup":['"$check"']}'
case "$PWD" in *repoA*) mine=1 ;; *) mine= ;; esac
case "$1 $2" in
  "pr list") if [ -n "$mine" ]; then echo "[$pr]"; else echo "[]"; fi ;;
  "pr view") if [ -n "$mine" ]; then echo "$pr"; else echo "no pull requests found" >&2; exit 1; fi ;;
  "run view") printf 'test\tRun tests\tFAIL: TestLogin wanted 1, got 2\ntest\tRun tests\t##[error]Process completed with exit code 1.\n' ;;
  "search prs"|"issue list") echo "[]" ;;
  api*) ;;
  *) echo "fake gh: $*" >&2; exit 1 ;;
esac
`

// TestAutoFixesFailingCI covers [auto] ci_failed = "auto": a check of the
// tab's pull request that comes to fail, once the run has ended, has nerdr
// send the tab's agent the message F2's f writes, with the job's log, by
// itself, and say so.
func TestAutoFixesFailingCI(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")

	ghDir := filepath.Join(scratch, "ghbin")
	_ = os.MkdirAll(ghDir, 0o755)
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(fakeGh), 0o755); err != nil {
		t.Fatal(err)
	}
	phase := filepath.Join(scratch, "gh-phase")
	_ = os.WriteFile(phase, []byte("running"), 0o644)

	lines := filepath.Join(scratch, "lines")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\ngithub_refresh = 15\n"+
			"[auto]\nci_failed = \"auto\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)

	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "HERDR_"), strings.HasPrefix(kv, "NERDR_"):
		case strings.HasPrefix(kv, "PATH="):
			env = append(env, "PATH="+ghDir+":"+strings.TrimPrefix(kv, "PATH="))
		default:
			env = append(env, kv)
		}
	}
	env = append(env, "XDG_CONFIG_HOME="+cfgDir, "XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+filepath.Join(scratch, "agent.log"), "FAKE_GH_PHASE="+phase, "FAKE_LINES="+lines, "TERM=xterm-256color")
	h := start(t, bin, root, env)

	h.waitFor("Repositories in", 10*time.Second)
	h.keys(" ") // pick repoA
	h.keys("\r")
	h.waitFor("branch:", 2*time.Second)
	h.keys("feat-ci\r")
	h.waitFor("Agent:", 3*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 15*time.Second)

	// The first look, with the check running, only learns how it stands;
	// give it time to be had before the check fails.
	time.Sleep(5 * time.Second)
	_ = os.WriteFile(phase, []byte("failed"), 0o644)

	// The next look, at most 20 s on at github_refresh = 15, sees it fail.
	h.waitFor("a message to fix #7's failing CI", 45*time.Second)
	h.waitFor(`got: Work\ out\ the\ cause`, 10*time.Second)
	got := readFile(lines)
	for _, want := range []string{"A CI check on pull request #7 (Login) failed.", "  - CI / test (failure)", "FAIL: TestLogin wanted 1, got 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the agent was not sent %q; it got:\n%s", want, got)
		}
	}
	if !strings.Contains(h.screen(), "nerdr sent the agent, by itself, a message to fix #7's failing CI") {
		t.Fatalf("the tab's panel should say nerdr sent it:\n%s", h.screen())
	}
}
