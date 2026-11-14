package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubagentsFileKeepsTheSecret: the tab's secret is not in the name of
// the file its hooks keep in the temporary folder, which on Linux every
// user and agent can list; two tabs still have a file each.
func TestSubagentsFileKeepsTheSecret(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	file := subagentsFile("claude", secret)
	if strings.Contains(file, secret) || strings.Contains(file, secret[:8]) {
		t.Fatalf("%s names the tab's secret", file)
	}
	if file == subagentsFile("claude", secret[1:]+"0") || file == subagentsFile("copilot", secret) {
		t.Fatal("two agents share a file")
	}
	if file != subagentsFile("claude", secret) {
		t.Fatal("an agent's hooks do not find the same file")
	}
}

// TestHookStateIsNotReadOrWrittenThroughALink: a link left where a hook
// keeps its state, or its lock, is neither read through nor written
// through, and what it points at is left as it was.
func TestHookStateIsNotReadOrWrittenThroughALink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	const was = `{"going":{"planted":true},"waiting":true}`
	if err := os.WriteFile(victim, []byte(was), 0o600); err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		t.Helper()
		if b, err := os.ReadFile(victim); err != nil || string(b) != was {
			t.Fatalf("the file linked to is now %q (%v)", b, err)
		}
	}

	// The state is a link: not read, and replaced by a file of its own.
	file := filepath.Join(dir, "state.json")
	if err := os.Symlink(victim, file); err != nil {
		t.Fatal(err)
	}
	if w := seeSubagents(file, "SubagentStart", "a", false); w.Going != 1 {
		t.Fatalf("what the link points at was read: %+v", w)
	}
	unchanged()
	if fi, err := os.Lstat(file); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the state is still not a plain file: %v %v", fi, err)
	}

	// The lock is a link: nothing is done, and nothing written.
	locked := filepath.Join(dir, "locked.json")
	if err := os.Symlink(victim, locked+".lock"); err != nil {
		t.Fatal(err)
	}
	if w := seeSubagents(locked, "SubagentStart", "a", false); w.Going != 0 {
		t.Fatalf("went on through a lock that is a link: %+v", w)
	}
	unchanged()
	if _, err := os.Lstat(locked); !os.IsNotExist(err) {
		t.Fatalf("state was written beside a lock that is a link: %v", err)
	}

	// agy's turn, the same.
	turn := filepath.Join(dir, "turn.json")
	planted := filepath.Join(dir, "planted")
	if err := os.WriteFile(planted, []byte(`{"main":"theirs"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, turn); err != nil {
		t.Fatal(err)
	}
	if !seeAgy(turn, AgyEvent{Event: "PreInvocation", ConversationID: "mine"}) {
		t.Fatal("a turn planted through a link was taken for the agent's")
	}
	if b, _ := os.ReadFile(planted); string(b) != `{"main":"theirs"}` {
		t.Fatalf("the file linked to is now %q", b)
	}
}

// TestForgetAgentRemovesItsHooksState: what an agent's hooks kept goes
// when the agent does, and nothing of another's.
func TestForgetAgentRemovesItsHooksState(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for _, pane := range []string{"gone", "stays"} {
		for _, kind := range subagentKinds {
			seeSubagents(subagentsFile(kind, pane), "SubagentStart", "a", false)
		}
	}
	ForgetAgent("gone")
	ForgetAgent("")
	for _, kind := range subagentKinds {
		for _, f := range []string{subagentsFile(kind, "gone"), subagentsFile(kind, "gone") + ".lock"} {
			if _, err := os.Lstat(f); !os.IsNotExist(err) {
				t.Errorf("%s is left: %v", f, err)
			}
		}
		if _, err := os.Lstat(subagentsFile(kind, "stays")); err != nil {
			t.Errorf("another agent's state went too: %v", err)
		}
	}
}

// TestListenDoesNotWriteThroughALinkedLock: a link left where the socket's
// lock is is not written through.
func TestListenDoesNotWriteThroughALinkedLock(t *testing.T) {
	base, err := os.MkdirTemp("", "nh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	victim := filepath.Join(base, "victim")
	if err := os.WriteFile(victim, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "n.sock")
	if err := os.Symlink(victim, path+".lock"); err != nil {
		t.Fatal(err)
	}
	if s, err := Listen(path, func(Report) {}); err == nil {
		s.Close()
		t.Fatal("listened with a lock that is a link")
	}
	if b, _ := os.ReadFile(victim); string(b) != "mine\n" {
		t.Fatalf("the file linked to is now %q", b)
	}
}

// TestListenTightensAFolderOthersCanWrite: a folder of the user's that
// others may write in, where they could put a socket of their own in
// place of nerdr's, has that taken away before nerdr listens in it; that
// they may read it is left.
func TestListenTightensAFolderOthersCanWrite(t *testing.T) {
	base, err := os.MkdirTemp("", "nh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	dir := filepath.Join(base, "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	s, err := Listen(filepath.Join(dir, "n.sock"), func(Report) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("the folder has mode %o (%v), want 755", fi.Mode().Perm(), err)
	}
}
