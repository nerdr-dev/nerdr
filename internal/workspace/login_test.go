package workspace

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

// httpsOrigin serves the bare repositories under root's origins folder over
// https with git http-backend, to the login u and whatever password says,
// and says what it was asked.
type httpsOrigin struct {
	*httptest.Server
	mu sync.Mutex
	// password is the one origin takes; forbid, repositories it answers
	// 403 for when logged in, as for a token for other repositories.
	password string
	forbid   map[string]bool
	// bad is how many requests came with a login origin refused; users,
	// whom each repository was fetched for last.
	bad   int
	users map[string]string
}

func serveOrigins(t *testing.T, root string) *httpsOrigin {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	o := &httpsOrigin{password: "s3cret-token", forbid: map[string]bool{}, users: map[string]string{}}
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Join(root, "origins"), "GIT_HTTP_EXPORT_ALL=1"},
	}
	o.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		name := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)[0]
		u, p, ok := r.BasicAuth()
		// u's password is the one set; anyone else's is pw-<name>.
		forbidden := o.forbid[name]
		right := ok && (u == "u" && p == o.password || u != "u" && p == "pw-"+u)
		if right {
			o.users[name] = u
		}
		if ok && !right {
			o.bad++
		}
		o.mu.Unlock()
		switch {
		case !right:
			w.Header().Set("WWW-Authenticate", `Basic realm="origin"`)
			http.Error(w, "log in", http.StatusUnauthorized)
		case forbidden:
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			backend.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(o.Close)
	return o
}

// setPassword changes the password origin takes, as a token that runs out.
func (o *httpsOrigin) setPassword(p string) {
	o.mu.Lock()
	o.password = p
	o.mu.Unlock()
}

// countingHelper is the user's credential helper, in their global config,
// set up as gh auth setup-git does, a helper known to answer alike in
// every repository: it gives the login u with the password in the file it returns, or for
// a username git names, pw-<name>, and adds a line to its log for every
// time it is asked, each a finger on the reader with a token in the
// keychain. An erase is logged as well.
func countingHelper(t *testing.T) (log, password string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "asked")
	password = filepath.Join(dir, "password")
	if err := os.WriteFile(password, []byte("s3cret-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "gh")
	// git asks it gh auth git-credential <operation>. While the file
	// daemon is there, it leaves a program running with what git gave it
	// open, as git credential-cache's daemon.
	body := "#!/bin/sh\nfor a; do op=$a; done\n" +
		"u=\nwhile IFS= read -r l; do case \"$l\" in username=*) u=${l#username=};; esac; done\n" +
		"echo \"$op\" >> " + log + "\n" +
		"test -f " + filepath.Join(dir, "daemon") + " && { sleep 5 </dev/null >/dev/null 2>&1 & }\n" +
		"test \"$op\" = get || exit 0\n" +
		"if test -n \"$u\" && test \"$u\" != u; then echo password=pw-$u; exit 0; fi\n" +
		"echo username=u\necho password=$(cat " + password + ")\n" +
		// A token that runs out, in this many seconds, while the file is there.
		"test -f " + filepath.Join(dir, "expiry") + " && echo password_expiry_utc=$(( $(date +%s) + $(cat " + filepath.Join(dir, "expiry") + ") ))\n" +
		"exit 0\n"
	if err := os.WriteFile(helper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte("[credential]\n\thelper = !gh auth git-credential\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	// Not the system's helpers either: git on macOS has osxkeychain there,
	// which would answer before this one and keep what it was given.
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The test's origin has a certificate of its own.
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	return log, password
}

// asked is how many times the helper was asked for a login since the last
// call, and what else it was told.
func asked(t *testing.T, log string) (gets int, other []string) {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Fields(string(b)) {
		if l == "get" {
			gets++
		} else {
			other = append(other, l)
		}
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	return gets, other
}

// httpsRepos makes clones of names under root, each with its origin
// served by o.
func httpsRepos(t *testing.T, root string, o *httpsOrigin, names ...string) []Repo {
	t.Helper()
	var repos []Repo
	for _, n := range names {
		r, _ := cloneWithOrigin(t, root, n)
		run(t, r.Path, "git", "remote", "set-url", "origin", o.URL+"/"+n+".git")
		repos = append(repos, r)
	}
	return repos
}

func newTab(t *testing.T, root string, repos ...Repo) {
	t.Helper()
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", repos)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
}

// TestOneLoginForATabOverRepositories: a new tab over three repositories
// on one https host asks the user's credential helper once, not once a
// repository; once a host, over two.
func TestOneLoginForATabOverRepositories(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	// The helper that gives the login has variables of these names.
	for _, v := range []string{"a", "h", "p", "u"} {
		t.Setenv(v, "1")
	}
	root := t.TempDir()
	log, _ := countingHelper(t)
	ran := noAskpassRuns(t)
	// GIT_ASKPASS may answer by the repository: with one, no login is
	// held for another. SSH_ASKPASS git never runs, core.askPass set.
	t.Setenv("GIT_ASKPASS", "")
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b", "c")
	newTab(t, root, repos...)
	if n, _ := asked(t, log); n != 1 {
		t.Fatalf("the helper was asked %d times for three repositories on one host, want 1", n)
	}

	forgetLogins()
	other := serveOrigins(t, root)
	d := httpsRepos(t, root, other, "d")[0]
	run(t, repos[2].Path, "git", "remote", "set-url", "origin", other.URL+"/c.git")
	repos = append(repos, d)
	plan, err := MakePlan(filepath.Join(root, "worktrees2"), "feat2", repos)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	if n, _ := asked(t, log); n != 2 {
		t.Fatalf("the helper was asked %d times for two hosts, want 2", n)
	}
	if marked(ran, "askpass") {
		t.Fatal("an askpass program was run")
	}
}

// noAskpassRuns sets the user's askpass programs to one that marks it ran
// in the folder it returns, and copies what fd 3 has there.
func noAskpassRuns(t *testing.T) string {
	t.Helper()
	marks := t.TempDir()
	script := filepath.Join(marks, "ask")
	body := "#!/bin/sh\ntouch " + filepath.Join(marks, "askpass") + "\ncat <&3 >> " + filepath.Join(marks, "fd3") + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_ASKPASS", script)
	t.Setenv("SSH_ASKPASS", script)
	t.Setenv("DISPLAY", ":0")
	return marks
}

// TestLoginIsForTheUsernameAsked: origins with a username of their own on
// one host are each asked for that user's login, and each fetched as it.
func TestLoginIsForTheUsernameAsked(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, _ := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b", "c")
	host := strings.TrimPrefix(o.URL, "https://")
	run(t, repos[0].Path, "git", "remote", "set-url", "origin", "https://alice@"+host+"/a.git")
	run(t, repos[1].Path, "git", "remote", "set-url", "origin", "https://bob@"+host+"/b.git")
	run(t, repos[2].Path, "git", "remote", "set-url", "origin", "https://alice@"+host+"/c.git")
	for _, r := range repos {
		if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := asked(t, log); n != 2 {
		t.Fatalf("the helper was asked %d times for two users, want 2", n)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.users["a.git"] != "alice" || o.users["b.git"] != "bob" || o.users["c.git"] != "alice" {
		t.Fatalf("fetched as %v", o.users)
	}
}

// TestLoginIsNotGivenWhereOtherHelpersAre: git picks a helper set for a
// URL's path by the whole URL, though it asks it for the host alone; a
// repository there is asked its own helpers.
func TestLoginIsNotGivenWhereOtherHelpersAre(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, _ := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b")
	marks := t.TempDir()
	other := filepath.Join(marks, "other")
	body := "#!/bin/sh\ncat >/dev/null\necho \"$1\" >> " + filepath.Join(marks, "asked") + "\n" +
		"test \"$1\" = get || exit 0\necho username=u\necho password=s3cret-token\n"
	if err := os.WriteFile(other, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "config", "--global", "credential."+o.URL+"/b.git.helper", "")
	run(t, root, "git", "config", "--global", "--add", "credential."+o.URL+"/b.git.helper", other)
	for _, r := range repos {
		if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := asked(t, log); n != 1 {
		t.Fatalf("the user's helper was asked %d times, want 1, for a", n)
	}
	if n, _ := asked(t, filepath.Join(marks, "asked")); n != 1 {
		t.Fatalf("b's own helper was asked %d times, want 1", n)
	}
}

// TestHelperThatLivesOnHoldsNoFetch: a helper that leaves a program
// running with the pipe open (git credential-cache's daemon) keeps no
// fetch waiting, and the login is held all the same.
func TestHelperThatLivesOnHoldsNoFetch(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, password := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b")
	if err := os.WriteFile(filepath.Join(filepath.Dir(password), "daemon"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := gitOrigin(repos[0].Path, "fetch", "--quiet", "origin"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the fetch took %s", d)
	}
	if _, err := gitOrigin(repos[1].Path, "fetch", "--quiet", "origin"); err != nil {
		t.Fatal(err)
	}
	if n, _ := asked(t, log); n != 1 {
		t.Fatalf("the helper was asked %d times, want 1", n)
	}
}

// TestLoginIsHeldOnlyForTheSamePlaceAndHelpers: with credential.useHttpPath
// a login is for one repository; with other credential settings in a
// repository, git would ask other helpers, and is let.
func TestLoginIsHeldOnlyForTheSamePlaceAndHelpers(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, _ := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b", "c")
	run(t, repos[2].Path, "git", "config", "credential.username", "u")
	for _, r := range repos {
		if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := asked(t, log); n != 2 {
		t.Fatalf("the helper was asked %d times, want 2: c has a setting of its own", n)
	}

	forgetLogins()
	run(t, root, "git", "config", "--global", "credential.useHttpPath", "true")
	for _, r := range repos[:2] {
		for range 2 {
			if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n, _ := asked(t, log); n != 2 {
		t.Fatalf("with useHttpPath the helper was asked %d times for two repositories, want 2", n)
	}
}

// TestHeldLoginGoesNowhereElse: the helper that gives a login held gives
// it for its place alone, so a redirect to another host gets none.
func TestHeldLoginGoesNowhereElse(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	countingHelper(t)
	ran := noAskpassRuns(t)
	o := serveOrigins(t, root)
	a := httpsRepos(t, root, o, "a")[0]
	env, _ := gitsafe.OriginEnv(a.Path)
	_, err, _ := giveLogin(a.Path, env, "protocol=https\nhost=elsewhere.example\n",
		[]byte("username=u\npassword=s3cret-token\n"), []string{"fetch", "--quiet", "origin"})
	if err == nil || !refusedLogin(err) {
		t.Fatalf("a login for another host was taken: %v", err)
	}
	if marked(ran, "askpass") {
		t.Fatal("an askpass program was run with the login on fd 3")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.bad != 0 || len(o.users) != 0 {
		t.Fatal("a login was sent")
	}
}

// TestRefusedLoginIsAskedForAgain: a token that ran out since it was held
// is rejected, as git does, and the helper asked again; one origin
// forbids is not rejected, but asked for again for that repository.
func TestRefusedLoginIsAskedForAgain(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, password := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b", "c")
	fetch := func(r Repo) error {
		_, err := gitOrigin(r.Path, "fetch", "--quiet", "origin")
		return err
	}
	if err := fetch(repos[0]); err != nil {
		t.Fatal(err)
	}
	asked(t, log)

	o.setPassword("new-token")
	if err := os.WriteFile(password, []byte("new-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fetch(repos[1]); err != nil {
		t.Fatal(err)
	}
	n, other := asked(t, log)
	if n != 1 || !strings.Contains(strings.Join(other, " "), "erase") {
		t.Fatalf("after a refused login the helper was asked %d times and told %v, want once and erase", n, other)
	}
	if err := fetch(repos[2]); err != nil {
		t.Fatal(err)
	}
	if n, _ := asked(t, log); n != 0 {
		t.Fatalf("the new login was not held: the helper was asked %d times", n)
	}

	o.mu.Lock()
	o.forbid["b.git"] = true
	o.mu.Unlock()
	if err := fetch(repos[1]); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a forbidden fetch: %v", err)
	}
	n, other = asked(t, log)
	if n != 1 || strings.Contains(strings.Join(other, " "), "erase") {
		t.Fatalf("after a 403 the helper was asked %d times and told %v, want once and no erase", n, other)
	}
}

// TestLoginIsInNoArgvOrEnvironment: the login held reaches git through a
// pipe alone, never its arguments or environment.
func TestLoginIsInNoArgvOrEnvironment(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, _ := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b", "c")

	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	seen := filepath.Join(bin, "seen")
	wrapper := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + seen + "\nenv >> " + seen + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	newTab(t, root, repos...)
	if n, _ := asked(t, log); n != 1 {
		t.Fatalf("the helper was asked %d times, want 1", n)
	}
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "credential.helper=!f()") {
		t.Fatal("git was not run through the wrapper")
	}
	if strings.Contains(string(b), "s3cret-token") {
		t.Fatal("the login is in the arguments or environment of a git nerdr ran")
	}
}

// TestSandboxedRepositoryStaysGuarded: where an agent has run in a
// sandbox, a credential helper in the repository is run neither to fetch
// nor to find where a login is for, a login held or not; a repository
// with nothing of the kind is given the login held.
func TestSandboxedRepositoryStaysGuarded(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, _ := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b", "c")
	if _, err := gitOrigin(repos[0].Path, "fetch", "--quiet", "origin"); err != nil {
		t.Fatal(err)
	}
	asked(t, log)

	gitsafe.Sandboxed(repos[1].Path)
	if _, err := gitOrigin(repos[1].Path, "fetch", "--quiet", "origin"); err != nil {
		t.Fatal(err)
	}
	if n, _ := asked(t, log); n != 0 {
		t.Fatalf("a sandboxed repository's fetch asked the helper %d times, with a login held", n)
	}

	marks := t.TempDir()
	evil := filepath.Join(repos[2].Path, "helper")
	if err := os.WriteFile(evil, []byte("#!/bin/sh\ntouch "+filepath.Join(marks, "ran")+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repos[2].Path, "git", "config", "--add", "credential.helper", evil)
	gitsafe.Sandboxed(repos[2].Path)
	if _, err := gitOrigin(repos[2].Path, "fetch", "--quiet", "origin"); err == nil {
		t.Fatal("a fetch ran with a credential helper in the repository")
	}
	forgetLogins()
	if _, err := gitOrigin(repos[2].Path, "fetch", "--quiet", "origin"); err == nil {
		t.Fatal("a fetch ran with a credential helper in the repository")
	}
	if marked(marks, "ran") {
		t.Fatal("the repository's credential helper ran")
	}
}

// TestRefusedLoginInTheUsersLanguage: git that speaks the user's
// language tells of a refused login all the same.
func TestRefusedLoginInTheUsersLanguage(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	out, _ := exec.Command("git", "-C", t.TempDir(), "fetch", "nowhere").CombinedOutput()
	if strings.Contains(string(out), "not a git repository") {
		t.Skip("git here does not speak German")
	}
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, password := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b")
	if _, err := gitOrigin(repos[0].Path, "fetch", "--quiet", "origin"); err != nil {
		t.Fatal(err)
	}
	asked(t, log)
	o.setPassword("new-token")
	if err := os.WriteFile(password, []byte("new-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOrigin(repos[1].Path, "fetch", "--quiet", "origin"); err != nil {
		t.Fatalf("a refused login was not asked for again: %v", err)
	}
	// Refused with 403, git erases nothing: only its words say so.
	asked(t, log)
	o.mu.Lock()
	o.forbid["a.git"] = true
	o.mu.Unlock()
	if _, err := gitOrigin(repos[0].Path, "fetch", "--quiet", "origin"); err == nil {
		t.Fatal("a forbidden fetch went through")
	}
	if n, _ := asked(t, log); n != 1 {
		t.Fatalf("after a 403 the helper was asked %d times, want 1", n)
	}
}

// TestRefusedLoginIsRejectedWhereItCameFrom: a login origin refused is
// rejected to the helpers git took it from, picked by the whole URL, and
// no others.
func TestRefusedLoginIsRejectedWhereItCameFrom(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, password := countingHelper(t)
	o := serveOrigins(t, root)
	b := httpsRepos(t, root, o, "b")[0]
	marks := t.TempDir()
	own := filepath.Join(marks, "own")
	ownLog := filepath.Join(marks, "asked")
	body := "#!/bin/sh\ncat >/dev/null\necho \"$1\" >> " + ownLog + "\n" +
		"test \"$1\" = get || exit 0\necho username=u\necho password=$(cat " + password + ")\n"
	if err := os.WriteFile(own, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "config", "--global", "credential."+o.URL+"/b.git.helper", "")
	run(t, root, "git", "config", "--global", "--add", "credential."+o.URL+"/b.git.helper", own)
	fetch := func() {
		t.Helper()
		if _, err := gitOrigin(b.Path, "fetch", "--quiet", "origin"); err != nil {
			t.Fatal(err)
		}
	}
	fetch()
	asked(t, ownLog)
	o.setPassword("new-token")
	if err := os.WriteFile(password, []byte("new-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	fetch()
	if _, other := asked(t, ownLog); !strings.Contains(strings.Join(other, " "), "erase") {
		t.Fatalf("b's own helper was told %v, not to erase", other)
	}
	if _, other := asked(t, log); strings.Contains(strings.Join(other, " "), "erase") {
		t.Fatal("the user's other helper was told to erase b's login")
	}
}

// TestPathSettingElsewhereLeavesLoginsShared: a credential setting for a
// path on another host changes nothing here.
func TestPathSettingElsewhereLeavesLoginsShared(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, _ := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b")
	run(t, root, "git", "config", "--global", "credential.https://elsewhere.example/org.helper", "/nonexistent")
	for _, r := range repos {
		if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := asked(t, log); n != 1 {
		t.Fatalf("the helper was asked %d times, want 1", n)
	}
	if !byPath("credential.https://"+strings.TrimPrefix(o.URL, "https://")+"/b.git.helper\nx\x00", strings.TrimPrefix(o.URL, "https://")) ||
		!byPath("credential.https://*.example.com/org.helper\nx\x00", "git.example.com") {
		t.Fatal("a setting for a path here was not seen")
	}
}

// TestOriginsWordsRejectNoLogin: what git or origin writes is no word of
// a refused login, though it says so: the fetch is run again with the
// user's helpers, and the login is not rejected. Nor is a URL with a
// line break of its own taken for another.
func TestOriginsWordsRejectNoLogin(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, _ := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b", "c")
	if _, err := gitOrigin(repos[0].Path, "fetch", "--quiet", "origin"); err != nil {
		t.Fatal(err)
	}
	// A git that ends a fetch with words origin might have written, while
	// the mark is there, though the fetch went well.
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	mark := filepath.Join(bin, "mark")
	wrapper := "#!/bin/sh\n" + real + " \"$@\"\ns=$?\ntest -e " + mark + " || exit $s\n" +
		"case \" $* \" in *\" fetch \"*) ;; *) exit $s;; esac\n" +
		"echo \"fatal: Authentication failed for 'https://elsewhere/'\" >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = gitOrigin(repos[1].Path, "fetch", "--quiet", "origin")
	if err == nil || !strings.Contains(err.Error(), "Authentication failed") {
		t.Fatalf("the fetch did not fail with those words: %v", err)
	}
	if err := os.Remove(mark); err != nil {
		t.Fatal(err)
	}
	if _, other := asked(t, log); strings.Contains(strings.Join(other, " "), "erase") {
		t.Fatalf("origin's words had the login erased: %v", err)
	}
	if _, err := gitOrigin(repos[2].Path, "fetch", "--quiet", "origin"); err != nil {
		t.Fatal(err)
	}
	asked(t, log)

	run(t, repos[2].Path, "git", "config", "remote.origin.url", o.URL+"/c.git\n")
	env, _ := gitsafe.OriginEnv(repos[2].Path)
	if _, place, _ := loginKey(repos[2].Path, env); place != "" {
		t.Fatalf("a URL ending in a line break was taken for %q", place)
	}
}

// TestLoginThatRunsOutIsHeldNoLonger: a token the helper says runs out
// soon is held till a little before, or not at all.
func TestLoginThatRunsOutIsHeldNoLonger(t *testing.T) {
	if !hasPorcelain() {
		t.Skip("git is older than 2.41, which has password_expiry_utc")
	}
	for _, tc := range []struct {
		seconds string
		asks    int
	}{{"10", 2}, {"3600", 1}} {
		t.Run(tc.seconds, func(t *testing.T) {
			forgetLogins()
			t.Cleanup(forgetLogins)
			root := t.TempDir()
			log, password := countingHelper(t)
			if err := os.WriteFile(filepath.Join(filepath.Dir(password), "expiry"), []byte(tc.seconds), 0o600); err != nil {
				t.Fatal(err)
			}
			o := serveOrigins(t, root)
			for _, r := range httpsRepos(t, root, o, "a", "b") {
				if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
					t.Fatal(err)
				}
			}
			if n, _ := asked(t, log); n != tc.asks {
				t.Fatalf("with a token for %s seconds the helper was asked %d times, want %d", tc.seconds, n, tc.asks)
			}
		})
	}
}

// TestRepositoryWithSubmodulesIsGivenNoLogin: a fetch that may fetch
// submodules, as the user's settings say, is run as before: given no
// login, and its submodule's, of a helper of its own on the same host,
// is not held for the next repository.
func TestRepositoryWithSubmodulesIsGivenNoLogin(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	log, _ := countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b", "c")
	httpsRepos(t, root, o, "s")
	b := repos[1].Path
	run(t, b, "git", "-c", "protocol.allow=always", "submodule", "add", "-q", o.URL+"/s.git", "sub")
	run(t, b, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "sub")
	run(t, b, "git", "config", "fetch.recurseSubmodules", "true")
	// The submodule's own helper logs in as another user.
	marks := t.TempDir()
	own := filepath.Join(marks, "own")
	if err := os.WriteFile(own, []byte("#!/bin/sh\ncat >/dev/null\ntest \"$1\" = get || exit 0\necho username=subu\necho password=pw-subu\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(b, ".git", "modules", "sub")
	run(t, sub, "git", "config", "--file", filepath.Join(sub, "config"), "credential.helper", "")
	run(t, sub, "git", "config", "--file", filepath.Join(sub, "config"), "--add", "credential.helper", own)
	asked(t, log)

	for _, r := range repos {
		if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := asked(t, log); n != 2 {
		t.Fatalf("the helper was asked %d times, want 2: once more for b", n)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.users["s.git"] != "subu" {
		t.Fatalf("the submodule was not fetched with its own login: %v", o.users)
	}
	if o.users["c.git"] != "u" {
		t.Fatalf("c was fetched as %q, the submodule's login", o.users["c.git"])
	}
}

// TestHelperOfTheRepositorysOwnIsAskedInEach: a helper git runs in the
// repository may answer by it (store with a file of the repository's
// own), and is asked in each, its login held for none of the others.
func TestHelperOfTheRepositorysOwnIsAskedInEach(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b")
	run(t, root, "git", "config", "--global", "credential.helper", "store --file=account")
	host := strings.TrimPrefix(o.URL, "https://")
	for _, r := range []struct{ repo, user string }{{repos[0].Path, "alice"}, {repos[1].Path, "bob"}} {
		line := "https://" + r.user + ":pw-" + r.user + "@" + host + "\n"
		if err := os.WriteFile(filepath.Join(r.repo, "account"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range repos {
		if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
			t.Fatal(err)
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.users["a.git"] != "alice" || o.users["b.git"] != "bob" {
		t.Fatalf("fetched as %v", o.users)
	}
}

func TestHelpersAlike(t *testing.T) {
	for set, want := range map[string]bool{
		"":                                   true,
		"credential.helper\nosxkeychain\x00": true,
		"credential.helper\n\x00credential.https://github.com.helper\n!/opt/homebrew/bin/gh auth git-credential\x00": true,
		"credential.helper\nstore --file=~/.git-credentials\x00":                                                     true,
		"credential.helper\ncache --timeout=900\x00":                                                                 true,
		"credential.helper\n/usr/lib/git-core/git-credential-libsecret\x00":                                          true,
		"credential.helper\n!gh auth git-credential\x00":                                                             true,
		"credential.helper\n!/opt/team/gh auth git-credential\x00":                                                   false,
		"credential.helper\n!'/opt/homebrew/bin/gh' auth git-credential\x00":                                         false,
		"credential.helper\n/home/u/bin/git-credential-store\x00":                                                    false,
		"credential.helper\nstore --file=/$PWD/account\x00":                                                          false,
		"credential.helper\nstore --file=/a/../b\x00":                                                                false,
		"credential.helper\nstore --file=~/a~b\x00":                                                                  false,
		"credential.helper\nstore --file=account\x00":                                                                false,
		"credential.helper\n!f() { cat creds; }; f\x00":                                                              false,
		"credential.helper\n/home/u/bin/my-helper\x00":                                                               false,
		"credential.helper\nosxkeychain --extra\x00":                                                                 false,
		"credential.usehttppath\ntrue\x00":                                                                           true,
	} {
		if got := helpersAlike(set, "github.com"); got != want {
			t.Errorf("helpersAlike(%q) = %v, want %v", set, got, want)
		}
	}
}

func TestByPathPorts(t *testing.T) {
	for _, tc := range []struct {
		set, host string
		want      bool
	}{
		{"credential.https://example.com:0443/team.helper\nx\x00", "example.com", true},
		{"credential.https://EXAMPLE.com/team.helper\nx\x00", "example.com:443", true},
		{"credential.https://example.com/team.helper\nx\x00", "example.com:8443", true},
		{"credential.https://ex%61mple.com/team.helper\nx\x00", "example.com", true},
		{"credential.https://elsewhere.com/team.helper\nx\x00", "example.com", false},
		{"credential.https://example.com.helper\nx\x00", "example.com", false},
	} {
		if got := byPath(tc.set, tc.host); got != tc.want {
			t.Errorf("byPath(%q, %q) = %v, want %v", tc.set, tc.host, got, tc.want)
		}
	}
}

// TestAskpassIsAskedInEach: an askpass program may answer by the
// repository it runs in, and is asked in each.
func TestAskpassIsAskedInEach(t *testing.T) {
	forgetLogins()
	t.Cleanup(forgetLogins)
	root := t.TempDir()
	countingHelper(t)
	o := serveOrigins(t, root)
	repos := httpsRepos(t, root, o, "a", "b")
	run(t, root, "git", "config", "--global", "--unset-all", "credential.helper")
	ask := filepath.Join(t.TempDir(), "ask")
	body := "#!/bin/sh\ncase \"$(basename \"$PWD\")\" in a) u=alice;; *) u=bob;; esac\n" +
		"case \"$1\" in Username*) echo $u;; *) echo pw-$u;; esac\n"
	if err := os.WriteFile(ask, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_ASKPASS", ask)
	for _, r := range repos {
		if _, err := gitOrigin(r.Path, "fetch", "--quiet", "origin"); err != nil {
			t.Fatal(err)
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.users["a.git"] != "alice" || o.users["b.git"] != "bob" {
		t.Fatalf("fetched as %v", o.users)
	}
}
