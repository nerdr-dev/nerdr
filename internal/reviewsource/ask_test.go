package reviewsource

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
	"github.com/nerdr-dev/nerdr/internal/why"
)

// echo is a Source whose answers say what it was asked.
type echo struct{}

func (echo) ListWorktrees(repo string) ([]git.Worktree, error) {
	return []git.Worktree{{Path: repo + "/wt", Branch: "b", Head: "h"}}, nil
}
func (echo) DetectBase(wt string) string { return "base of " + wt }
func (echo) Files(c git.Comparison) ([]git.FileStatus, map[string]git.LineCount, error) {
	return []git.FileStatus{{Path: c.Worktree + ":" + c.Base, OldPath: "o", Code: "R "}},
		map[string]git.LineCount{"x": {Add: 1, Del: 2, Binary: true}}, nil
}
func (echo) Diff(c git.Comparison, file string, untracked bool, original string) (string, error) {
	return fmt.Sprint(c, file, untracked, original), nil
}
func (echo) OldFile(c git.Comparison, file string) (string, error) {
	return "", errors.New("no old " + file)
}
func (echo) OldSource(c git.Comparison, file string) (string, error) {
	return "old source of " + file, nil
}
func (echo) FullNewFile(wt, file, diff string) (string, error) {
	return wt + file + diff, nil
}
func (echo) ReadFile(wt, file string) (string, error) { return wt + "/" + file, nil }
func (echo) Summary(wt string) (int, int, int)        { return 1, 2, 3 }
func (echo) Stamps(wt string, paths []string) map[string]string {
	return map[string]string{paths[0]: wt}
}
func (echo) GhAvailable() bool { return true }
func (echo) CurrentPR(wt string) (github.PR, bool, string) {
	return github.PR{Number: 7, Title: wt}, true, "msg"
}
func (echo) FailedRunLogs(wt string, checks []github.Check, limit int) (string, error) {
	return fmt.Sprint(wt, checks[0].Name, limit), nil
}

func (echo) Notes(wt string) why.Index {
	return why.Index{}.With([]why.Note{{File: wt, Anchor: "a", Short: "s"}}, 3)
}
func (echo) Order(wt, branch string) order.Index {
	return order.LoadFile(filepath.Join(wt, "order.jsonl"), branch)
}
func (echo) Walkthrough(wt, branch string, files []string) (walkthrough.Walkthrough, bool, bool) {
	return walkthrough.Walkthrough{Title: wt, Branch: branch, Summary: files[0]}, true, true
}
func (echo) WalkthroughPage(wt, branch string) ([]byte, bool, error) {
	return []byte("<h1>" + wt + " " + branch + "</h1>"), true, nil
}
func (echo) LoadProject(wt string) int { return 5 }
func (echo) Imports(wt string, paths []string) map[string][]string {
	return map[string][]string{paths[0]: {wt}}
}
func (echo) Relations(wt, file string) ([]analysis.Symbol, [][]analysis.Ref) {
	return []analysis.Symbol{{Name: file, Kind: "function"}}, [][]analysis.Ref{{{File: wt, Line: 2}}}
}

// loopback is an Asker whose queries and answers go as JSON, as they do
// between the client and core, to s.
func loopback(t *testing.T, s Source) *Asker {
	var k *Asker
	k = NewAsker(func(job string, q Query) bool {
		b, err := json.Marshal(q)
		if err != nil {
			t.Error(err)
			return false
		}
		go func() {
			var q Query
			if err := json.Unmarshal(b, &q); err != nil {
				t.Error(err)
				return
			}
			b, _ := json.Marshal(q.Run(s))
			var a Answer
			if err := json.Unmarshal(b, &a); err != nil {
				t.Error(err)
				return
			}
			k.Answer(job, a)
		}()
		return true
	})
	return k
}

// TestAskerAsks: every Source method goes as a Query and comes back as
// the Answer to it, its arguments and results as they were.
func TestAskerAsks(t *testing.T) {
	var e echo
	k := loopback(t, e)
	c := git.Comparison{Worktree: "/wt", Base: "main"}
	same := func(what string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %#v, want %#v", what, got, want)
		}
	}
	both := func(a, b any) []any { return []any{a, b} }

	same("ListWorktrees", both(k.ListWorktrees("/r")), both(e.ListWorktrees("/r")))
	same("DetectBase", k.DetectBase("/wt"), e.DetectBase("/wt"))
	f1, s1, err1 := k.Files(c)
	f2, s2, err2 := e.Files(c)
	same("Files", []any{f1, s1, err1}, []any{f2, s2, err2})
	same("Diff", both(k.Diff(c, "a", true, "b")), both(e.Diff(c, "a", true, "b")))
	if _, err := k.OldFile(c, "a"); err == nil || err.Error() != "no old a" {
		t.Errorf("OldFile's error: %v", err)
	}
	same("OldSource", both(k.OldSource(c, "a")), both(e.OldSource(c, "a")))
	same("FullNewFile", both(k.FullNewFile("/wt", "a", "d")), both(e.FullNewFile("/wt", "a", "d")))
	same("ReadFile", both(k.ReadFile("/wt", "a")), both(e.ReadFile("/wt", "a")))
	a, b, d := k.Summary("/wt")
	same("Summary", [3]int{a, b, d}, [3]int{1, 2, 3})
	same("Stamps", k.Stamps("/wt", []string{"p"}), e.Stamps("/wt", []string{"p"}))
	same("GhAvailable", k.GhAvailable(), true)
	pr, ok, msg := k.CurrentPR("/wt")
	pr2, ok2, msg2 := e.CurrentPR("/wt")
	same("CurrentPR", []any{pr, ok, msg}, []any{pr2, ok2, msg2})
	checks := []github.Check{{Name: "ci"}}
	same("FailedRunLogs", both(k.FailedRunLogs("/wt", checks, 9)), both(e.FailedRunLogs("/wt", checks, 9)))
	same("Notes", k.Notes("/wt").All(), e.Notes("/wt").All())
	same("Notes read", k.Notes("/wt").Read(), 3)
	w1, ok1, old1 := k.Walkthrough("/wt", "b", []string{"f"})
	w2, ok2, old2 := e.Walkthrough("/wt", "b", []string{"f"})
	same("Walkthrough", []any{w1, ok1, old1}, []any{w2, ok2, old2})
	p1, ok1, err1 := k.WalkthroughPage("/wt", "b")
	p2, ok2, err2 := e.WalkthroughPage("/wt", "b")
	same("WalkthroughPage", []any{p1, ok1, err1}, []any{p2, ok2, err2})
	same("LoadProject", k.LoadProject("/wt"), 5)
	same("Imports", k.Imports("/wt", []string{"a"}), e.Imports("/wt", []string{"a"}))
	sy1, r1 := k.Relations("/wt", "a")
	sy2, r2 := e.Relations("/wt", "a")
	same("Relations", []any{sy1, r1}, []any{sy2, r2})
	if len(k.jobs) != 0 {
		t.Errorf("jobs left waiting: %v", k.jobs)
	}
}

// TestAskerFailsWhatWillNotBeAnswered: a query that cannot go, or whose
// answer will not come, ends with an error, not a wait.
func TestAskerFailsWhatWillNotBeAnswered(t *testing.T) {
	k := NewAsker(func(string, Query) bool { return false })
	if _, err := k.ReadFile("/wt", "a"); err == nil {
		t.Error("a query that could not go should fail")
	}

	sent := make(chan struct{}, 1)
	k = NewAsker(func(string, Query) bool { sent <- struct{}{}; return true })
	got := make(chan error, 1)
	go func() {
		_, err := k.ReadFile("/wt", "a")
		got <- err
	}()
	<-sent
	k.FailAll("core has gone")
	if err := <-got; err == nil || !strings.Contains(err.Error(), "core has gone") {
		t.Errorf("FailAll: %v", err)
	}
	k.Answer("review-1", Answer{}) // late: nobody waits, and it does not block

	old := AskWait
	AskWait = 10 * time.Millisecond
	defer func() { AskWait = old }()
	if _, err := k.ReadFile("/wt", "a"); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("unanswered: %v", err)
	}
	if len(k.jobs) != 0 {
		t.Errorf("jobs left waiting: %v", k.jobs)
	}
}
