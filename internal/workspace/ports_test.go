package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPortsEnv(t *testing.T) {
	p := Ports{First: 20010, Count: 10}
	if got, want := p.Env(), []string{"NERDR_PORT=20010", "NERDR_PORT_END=20019"}; !slices.Equal(got, want) {
		t.Fatalf("env = %v, want %v", got, want)
	}
	if env := (Ports{}).Env(); env != nil {
		t.Fatalf("no range, env = %v", env)
	}
	if p.String() != "20010–20019" {
		t.Fatalf("string = %q", p.String())
	}
}

func TestPortsOverlap(t *testing.T) {
	a := Ports{First: 20000, Count: 10}
	for _, c := range []struct {
		b    Ports
		want bool
	}{
		{Ports{First: 20010, Count: 10}, false},
		{Ports{First: 20009, Count: 1}, true},
		{Ports{First: 19995, Count: 6}, true},
		{Ports{First: 19995, Count: 5}, false},
		{Ports{}, false},
	} {
		if got := a.Overlaps(c.b); got != c.want || c.b.Overlaps(a) != c.want {
			t.Errorf("%v overlaps %v = %v, want %v", a, c.b, got, c.want)
		}
	}
}

func TestPrepareTellsSetupPorts(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	if err := os.WriteFile(filepath.Join(a.Path, RepoConfigFile),
		[]byte("setup = \"echo $NERDR_PORT $NERDR_PORT_END > ports\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Dir != PlanDir(filepath.Join(root, "worktrees"), "feat", []Repo{a}) {
		t.Fatalf("plan dir %s is not PlanDir's", plan.Dir)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(t.TempDir(), "allowed.json")
	c, _ := LoadRepoConfig(a.Path)
	if err := Allow(allowed, a.Path, c); err != nil {
		t.Fatal(err)
	}
	if w := PrepareWith(wts, plan.SetupIn(), allowed, Ports{First: 21000, Count: 10}, func(string) {}); len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}
	got, err := os.ReadFile(filepath.Join(wts[0].Path, "ports"))
	if err != nil || strings.TrimSpace(string(got)) != "21000 21009" {
		t.Fatalf("setup saw %q, %v", got, err)
	}
}

// Setup is not told the ports of the tab of a nerdr this one was started
// in: with none of its own, it has none.
func TestSetupDoesNotInheritPorts(t *testing.T) {
	t.Setenv("NERDR_PORT", "20000")
	t.Setenv("NERDR_PORT_END", "20009")
	dir := t.TempDir()
	if err := runSetup(dir, `echo "[$NERDR_PORT][$NERDR_PORT_END]" > ports`, Ports{}.Env()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "ports"))
	if err != nil || strings.TrimSpace(string(got)) != "[][]" {
		t.Fatalf("setup saw %q, %v", got, err)
	}
}
