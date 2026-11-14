package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// apiCore is a core with one tab whose agent runs cat, which shows what it
// is given.
func apiCore(t *testing.T) *Core {
	t.Helper()
	p, err := term.Start([]string{"cat"}, t.TempDir(), os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	tab := &Tab{}
	tab.Tab = state.Tab{ID: "t1", Name: "fix-login", Dir: "/w/fix-login", Agent: agents.Claude}
	tab.Pane, tab.Light = p, StIdle
	return &Core{Root: t.TempDir(), Tabs: []*Tab{tab}, Agents: []agents.Kind{agents.Claude}, AgentsAt: time.Now()}
}

func TestAPIStateHasQuestionsAndWaiting(t *testing.T) {
	m := apiCore(t)
	m.Tabs[0].Light = StBlocked
	m.Asking = []*Question{
		{ID: 3, Kind: AskRepoSetup, Repos: []string{"/r/api"}},
		{ID: 4, Kind: AskPluginRun, A: TabRef{Tab: "t1"}, P: plugin.Plugin{Name: "deploy"}, Confined: true, Until: time.Now()},
	}
	s := m.APIState()
	if len(s.Waiting) != 1 || s.Waiting[0] != "t1" {
		t.Fatalf("waiting %v", s.Waiting)
	}
	if q := s.Questions[0]; q.Kind != "repo_setup" || !strings.Contains(q.Text, "api") {
		t.Fatalf("question %+v", q)
	}
	if q := s.Questions[1]; q.Kind != "plugin_run" || q.Agent != "t1" || q.Plugin != "deploy" ||
		!strings.Contains(q.Text, "fix-login would run the plugin deploy, outside its sandbox") || q.Until == nil {
		t.Fatalf("question %+v", q)
	}
}

// TestAPITypeAndRead: what is typed reaches the agent's program, keys by
// name, and read shows its screen.
func TestAPITypeAndRead(t *testing.T) {
	m := apiCore(t)
	if err := m.APIType("t1", "hello", []string{"enter", "y"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		text, err := m.APIRead("t1", 5)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "hello") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen %q", text)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := m.APIType("nope", "x", nil); err == nil || !strings.Contains(err.Error(), `no agent or terminal "nope"`) {
		t.Fatalf("err %v", err)
	}
}

func TestAPIKeyBytes(t *testing.T) {
	b, err := APIKeyBytes([]string{"1", "Enter", "esc", "ctrl-c", "up", "å"})
	if err != nil || string(b) != "1\r\x1b\x03\x1b[Aå" {
		t.Fatalf("%q %v", b, err)
	}
	for _, k := range []string{"", "ab", "\x1b", "f13"} {
		if _, err := APIKeyBytes([]string{k}); err == nil {
			t.Errorf("%q taken", k)
		}
	}
}

func TestAPIAnswer(t *testing.T) {
	m := apiCore(t)
	var got string
	m.Asking = []*Question{{ID: 5, Kind: AskRepoSetup, then: func(a string) Cmd { got = a; return nil }}}
	if _, err := m.APIAnswer(5, false); err != nil || got != RunDeclined || len(m.Asking) != 0 {
		t.Fatalf("answer %q %v", got, err)
	}
	if _, err := m.APIAnswer(5, true); err == nil {
		t.Fatal("answered twice")
	}
}

func TestAPIQuitting(t *testing.T) {
	m := apiCore(t)
	m.Quitting = true
	if err := m.APISend("t1", "x"); !errors.Is(err, ErrAPIQuitting) {
		t.Fatalf("send %v", err)
	}
	if _, err := m.APIMakeTab(APINewTab{Repos: []string{"r"}, Branch: "b"}); !errors.Is(err, ErrAPIQuitting) {
		t.Fatalf("new tab %v", err)
	}
}

// TestAPIMakeTabChecksFirst: a tab asked for by the API is checked as one
// an agent asks for, before anything is made.
func TestAPIMakeTabChecksFirst(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	m := apiCore(t)
	repo := filepath.Join(m.Root, "web")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if repos := m.APIRepos(); len(repos) != 1 || repos[0].Name != "web" {
		t.Fatalf("repos %+v", repos)
	}
	for _, c := range []struct {
		q    APINewTab
		want string
	}{
		{APINewTab{Repos: []string{"web"}, Branch: "bad..name"}, "branch"},
		{APINewTab{Branch: "fix"}, "name at least one repository"},
		{APINewTab{Repos: []string{"api"}, Branch: "fix"}, `no repository "api"; those there are: web`},
		{APINewTab{Repos: []string{"web"}, Branch: "fix", Agent: "nope"}, `no agent "nope"`},
	} {
		if _, err := m.APIMakeTab(c.q); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.q, err, c.want)
		}
	}
	cmd, err := m.APIMakeTab(APINewTab{Repos: []string{"web"}, Branch: "fix"})
	if err != nil || cmd == nil {
		t.Fatalf("cmd %v, err %v", cmd != nil, err)
	}
}

// TestTabMadeForTheUser: a tab the user asked for by the API, which could
// not be made, is told of with no agent to tell.
func TestTabMadeForTheUser(t *testing.T) {
	m := apiCore(t)
	var told []string
	m.Observe = func(ev Event) {
		if s, ok := ev.(ToastMsg); ok {
			told = append(told, string(s))
		}
	}
	m.TabMade(TabMadeMsg{Branch: "fix", Err: errors.New("a branch fix is there already")})
	if len(told) != 1 || !strings.Contains(told[0], "tab fix: a branch fix is there already") {
		t.Fatalf("told %v", told)
	}
}

func TestAPISendAndSeen(t *testing.T) {
	m := apiCore(t)
	if err := m.APISend("t1", "  "); err == nil {
		t.Fatal("empty message sent")
	}
	if err := m.APISend("t1", "next"); err != nil || len(m.Tabs[0].Messages) != 1 {
		t.Fatalf("messages %+v, %v", m.Tabs[0].Messages, err)
	}
	m.Tabs[0].QueuePaused = true
	if err := m.APISeen("t1"); err != nil || m.Tabs[0].QueuePaused {
		t.Fatalf("still paused, %v", err)
	}
}
