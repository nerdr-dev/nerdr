package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// A new tab over three repositories on one https host fetches from three
// origins, and git asks the user's credential helper for a login each
// time: with a token in the macOS keychain, or gh's, a finger on the
// reader each. So the login git got the first time is held, in nerdr's
// memory alone, for a short while (loginFresh), and given to the next
// fetch from the same place that would ask the same helpers for it.
//
// There is no socket or file for it, which an agent could reach: the
// login goes to and from the one git process that needs it through a
// pipe only that process (and what it runs) has open, as fd 3. Nor is it
// ever in argv, the environment, a log or an error.
//
//   - The first fetch runs as before, with the user's helpers, and one
//     more after them (catchLogin), which is told the login when git
//     stores it, as git does with every helper once origin took it. It
//     never answers git's ask, so it changes nothing of what git does. A
//     fetch origin asked no login for (a public repository) catches none
//     and costs none.
//   - A fetch from the same place, as git would ask the helpers for it
//     (loginPlace: protocol, host, the path with credential.useHttpPath,
//     the username the URL or the settings name), and with the same
//     credential settings, has the user's helpers taken away and one in
//     their place that gives the login held (giveLogin), and only for
//     that place: on a redirect to another host it gives nothing, as the
//     user's helper would give another login. Nor is an askpass program
//     run then, which would have the pipe open as well.
//   - When origin refuses it (an expired token, or one for some
//     repositories alone), it is dropped, rejected as git rejects a login
//     origin refused when origin said 401, and the fetch run again as the
//     first was.
//
// ssh is left as it is: a login there is a new signature each time, and
// nothing of it is there to hold.

// loginFresh is how long a login is held: a plan and its Sync.
const loginFresh = 2 * time.Minute

// loginMargin is how long before it runs out a login is no longer given:
// a fetch takes a while.
const loginMargin = 30 * time.Second

// gitTimeoutShort bounds the git that asks nothing of origin: config, and
// credential with no helper but nerdr's.
const gitTimeoutShort = 30 * time.Second

// heldLogin is a login git got from the user's helpers for a place: the
// username and password lines, as a helper gives them.
type heldLogin struct {
	secret []byte
	timer  *time.Timer
}

// loginLock keeps two fetches from one place at once from both asking the
// user; n is how many have it or wait for it.
type loginLock struct {
	sync.Mutex
	n int
}

// logins are held by a hash of their key, which has the user's
// credential settings in it, a helper's command line among them.
var logins struct {
	sync.Mutex
	held map[[32]byte]*heldLogin
	busy map[[32]byte]*loginLock
}

// withLogin runs git with args, a fetch or ls-remote from origin, in the
// main checkout repo with env, giving it a login held for origin's place
// when there is one, and holding the one it gets when there is not.
func withLogin(repo string, env, args []string) (string, error) {
	key, place, url := loginKey(repo, env)
	if place == "" {
		return gitRunEnv(fetchTimeout, env, repo, args...)
	}
	// A fetch that may fetch submodules, as the user's settings have it,
	// runs as before, neither given a login nor caught one: a submodule's
	// fetch would ask for its own, of helpers of its own, which the pipe
	// has none of to give, and which is not the repository's to hold.
	if args[0] == "fetch" && !slices.Contains(args, "--no-recurse-submodules") {
		if hasSubmodules(repo) {
			return gitRunEnv(fetchTimeout, env, repo, args...)
		}
		// With none, it changes nothing.
		args = append([]string{"fetch", "--no-recurse-submodules"}, args[1:]...)
	}
	unlock := lockLogin(key)
	defer unlock()

	if secret := takeLogin(key); secret != nil {
		out, err, erased := giveLogin(repo, env, place, secret, args)
		if err == nil || !erased && !refusedLogin(err) {
			wipe(secret)
			return out, err
		}
		dropLogin(key)
		if erased {
			rejectLogin(repo, env, url, secret)
		}
		wipe(secret)
	}
	out, err, secret, until := catchLogin(repo, env, place, args)
	if secret != nil {
		holdLogin(key, secret, until)
	}
	return out, err
}

// hasSubmodules says whether the main checkout repo has submodules, or
// may have: a .gitmodules, or a .git/modules folder.
func hasSubmodules(repo string) bool {
	for _, p := range []string{filepath.Join(repo, ".gitmodules"), filepath.Join(repo, ".git", "modules")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return true
		}
	}
	return false
}

func lockLogin(key [32]byte) (unlock func()) {
	logins.Lock()
	if logins.busy == nil {
		logins.busy = map[[32]byte]*loginLock{}
	}
	l := logins.busy[key]
	if l == nil {
		l = &loginLock{}
		logins.busy[key] = l
	}
	l.n++
	logins.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		logins.Lock()
		if l.n--; l.n == 0 {
			delete(logins.busy, key)
		}
		logins.Unlock()
	}
}

// loginKey is what a login git gets for origin of repo is held by, with
// env: the place git asks the user's helpers about (loginPlace) and every
// credential setting it reads; place is "" when origin is not over https,
// or there is no telling. place is the place's lines, url origin's.
func loginKey(repo string, env []string) (key [32]byte, place, url string) {
	url, err := gitEnvOut(env, repo, "ls-remote", "--get-url", "origin")
	// The one line break git ends it with; any other is the URL's.
	url = strings.TrimSuffix(url, "\n")
	// A line break would be a line of its own to git credential.
	if err != nil || !strings.HasPrefix(strings.ToLower(url), "https://") || strings.ContainsAny(url, "\r\n\x00") {
		return key, "", ""
	}
	place = loginPlace(repo, env, url)
	if place == "" {
		return key, "", ""
	}
	// None at all is an answer, with exit status 1.
	set, err := gitEnvOut(env, repo, "config", "--null", "--get-regexp", `^credential\.`)
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return key, "", ""
	}
	// git picks the helpers of credential.<url>.helper by the whole URL,
	// path and all, though it asks them for the host alone: with such a
	// setting for a path, two repositories on one host may have other
	// helpers, and a login is held for the one URL.
	k := place + "\x00" + set
	host := string(loginFields([]byte(place))["host"])
	if byPath(set, host) {
		k += "\x00" + url
	}
	// A helper git runs in the repository may answer by it: store
	// --file=account reads the repository's own. A login is held for
	// other repositories only of helpers known to answer alike in each.
	if !helpersAlike(set, host) || askpassIn(env) {
		k += "\x00" + repo
	}
	return sha256.Sum256([]byte(k)), place, url
}

// askpassIn says whether git run with env may get a login from an
// askpass program, when no helper gives one: GIT_ASKPASS, which comes
// before the core.askPass nerdr sets to /dev/null (and with it,
// SSH_ASKPASS, which comes after). It runs in the repository, and may
// answer by it.
func askpassIn(env []string) bool {
	v := ""
	for _, kv := range env {
		if a, ok := strings.CutPrefix(kv, "GIT_ASKPASS="); ok {
			v = a
		}
	}
	return v != "" && v != os.DevNull
}

// alikeHelpers are the helpers git names git-credential-<name>, as the
// user gives them, that answer by the place they are asked about alone,
// wherever git runs them, with what options may be given them.
var alikeHelpers = map[string]func(args []string) bool{
	"osxkeychain":   noArgs,
	"libsecret":     noArgs,
	"wincred":       noArgs,
	"manager":       noArgs,
	"manager-core":  noArgs,
	"gnome-keyring": noArgs,
	"store":         func(args []string) bool { return pathArgs(args, "--file") },
	"cache":         func(args []string) bool { return pathArgs(args, "--socket", "--timeout") },
}

func noArgs(args []string) bool { return len(args) == 0 }

// pathArgs says whether args are each --name=value of names, the value a
// path from / or ~, or a number for --timeout.
func pathArgs(args []string, names ...string) bool {
	for _, a := range args {
		name, value, ok := strings.Cut(a, "=")
		if !ok || !slices.Contains(names, name) {
			return false
		}
		if name == "--timeout" {
			if _, err := strconv.Atoi(value); err != nil {
				return false
			}
		} else if !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "~/") || filepath.Clean(value) != value {
			return false
		}
	}
	return true
}

// helpersAlike says whether every credential helper of set, as git config
// --null --get-regexp lists them, that may be for host is one that answers
// alike wherever git runs it: one of alikeHelpers, by name or by its whole
// path, or gh's (gh auth setup-git). Not glab's: glab takes a token from
// the repository's own config too (glab config set without --global), so
// it may answer each repository otherwise.
func helpersAlike(set, host string) bool {
	for _, entry := range strings.Split(set, "\x00") {
		name, value, _ := strings.Cut(entry, "\n")
		if !strings.HasSuffix(name, ".helper") {
			continue
		}
		if at, _, scoped := settingFor(name); scoped && !mayBe(at, host) {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cmd, bang := strings.CutPrefix(value, "!")
		// git runs it through the shell, which would make what it may
		// of anything but plain words ($PWD, `pwd`, a quote).
		if !literal(cmd) {
			return false
		}
		f := strings.Fields(cmd)
		if bang {
			if len(f) != 3 || f[1] != "auth" || f[2] != "git-credential" ||
				f[0] != "gh" && !(installed(f[0]) && filepath.Base(f[0]) == "gh") {
				return false
			}
			continue
		}
		helper := f[0]
		if strings.HasPrefix(helper, "/") {
			h, ok := strings.CutPrefix(filepath.Base(helper), "git-credential-")
			if !ok || !installed(helper) {
				return false
			}
			helper = h
		}
		ok, known := alikeHelpers[helper]
		if !known || !ok(f[1:]) {
			return false
		}
	}
	return true
}

// literal says whether s is words the shell takes as they are, but for a
// ~ for the user's home.
func literal(s string) bool {
	for _, c := range s {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.ContainsRune(" ./_-+=:,@~", c)) {
			return false
		}
	}
	// ~ only as the home: a word's start, or a value's, before a /.
	for i := strings.IndexByte(s, '~'); i >= 0; i = strings.IndexByte(s, '~') {
		if i > 0 && s[i-1] != ' ' && s[i-1] != '=' || i+1 < len(s) && s[i+1] != '/' {
			return false
		}
		s = s[i+1:]
	}
	return true
}

// installedIn are the folders programs are installed in, by the system
// or a package manager, as a helper by its whole path is trusted to be
// what its name says only there: not a program of the user's that may
// answer by the repository it is run in.
var installedIn = []string{
	"/usr/bin/", "/bin/", "/usr/lib/git-core/", "/usr/libexec/git-core/",
	"/usr/local/bin/", "/usr/local/libexec/git-core/", "/usr/local/Cellar/",
	"/opt/homebrew/bin/", "/opt/homebrew/Cellar/", "/opt/homebrew/opt/",
	"/home/linuxbrew/.linuxbrew/bin/", "/home/linuxbrew/.linuxbrew/Cellar/",
	"/Library/Developer/CommandLineTools/usr/", "/Applications/Xcode.app/Contents/Developer/usr/",
	"/nix/store/", "/run/current-system/sw/bin/",
}

func installed(path string) bool {
	if filepath.Clean(path) != path {
		return false
	}
	for _, dir := range installedIn {
		if strings.HasPrefix(path, dir) {
			return true
		}
	}
	return false
}

// byPath says whether a credential setting of set, as git config --null
// --get-regexp lists them, is for URLs under a path alone on host, or may
// be: one for host by its name, whatever the port (git reads :0443 as
// 443), one with a wildcard or an escape in the host, or with no host.
func byPath(set, host string) bool {
	for _, entry := range strings.Split(set, "\x00") {
		name, _, _ := strings.Cut(entry, "\n")
		if at, path, scoped := settingFor(name); scoped && path != "" && mayBe(at, host) {
			return true
		}
	}
	return false
}

// settingFor is the host and path of the URL the setting name
// (credential.<url>.<key>) is for; scoped is false for one for all.
func settingFor(name string) (host, path string, scoped bool) {
	name = strings.TrimPrefix(name, "credential.")
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return "", "", false
	}
	sub := name[:i]
	if _, rest, ok := strings.Cut(sub, "://"); ok {
		sub = rest
	}
	host, path, _ = strings.Cut(sub, "/")
	if _, h, ok := strings.Cut(host, "@"); ok {
		host = h
	}
	return host, path, true
}

// mayBe says whether at, the host of a setting, may be host as git
// matches them: by name, whatever the port (git reads :0443 as 443), and
// any with a wildcard or an escape, or none.
func mayBe(at, host string) bool {
	return at == "" || strings.ContainsAny(at, "*%") || hostName(at) == hostName(host)
}

// hostName is host without its port, in lower case.
func hostName(host string) string {
	host = strings.ToLower(host)
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]"); i > 0 {
			return host[:i+1]
		}
		return host
	}
	h, _, _ := strings.Cut(host, ":")
	return h
}

// loginPlace is what git asks the user's helpers about for a fetch from
// url in repo, as it tells them: the protocol, host and port, the path
// with credential.useHttpPath, and the username the URL or the settings
// name, each a line; "" when that is not https or there is no telling. git
// credential is asked for it, with no helper but one that writes what it
// is asked to fd 3 and tells git to stop there, and no askpass.
func loginPlace(repo string, env []string, url string) string {
	r, w, err := os.Pipe()
	if err != nil {
		return ""
	}
	defer r.Close()
	got := readPipe(r, 64<<10)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeoutShort)
	defer cancel()
	cmd := proc.Detach(exec.CommandContext(ctx, "git", "-C", repo,
		"-c", "credential.helper=",
		"-c", `credential.helper=!f() { test "$1" = get && cat >&3 && echo quit=1; }; f`,
		"credential", "fill"))
	cmd.Env = noAskpass(env)
	cmd.Stdin = strings.NewReader("url=" + url + "\n")
	cmd.ExtraFiles = []*os.File{w}
	// It fails, the helper giving nothing; what it writes is not kept.
	_ = cmd.Run()
	w.Close()
	asked := got()
	defer wipe(asked)
	fields := loginFields(asked)
	if len(fields["password"]) > 0 {
		return ""
	}
	return loginPlaceOf(fields, true)
}

// noAskpass is env with no askpass program to run: with the user's
// helpers taken away, one would be asked in their place, and would have
// the pipe open.
func noAskpass(env []string) []string {
	return append(slices.Clip(env), "GIT_ASKPASS="+os.DevNull, "SSH_ASKPASS="+os.DevNull)
}

// giveLogin runs git with args with the login secret held for place as
// the only helper's answer, through a pipe. erased is git telling the
// helper to erase it, as git does when origin refused it with 401 and
// nothing else: what git or origin writes may say anything.
func giveLogin(repo string, env []string, place string, secret []byte, args []string) (out string, err error, erased bool) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err, false
	}
	er, ew, err := os.Pipe()
	if err != nil {
		r.Close()
		w.Close()
		return "", err, false
	}
	defer er.Close()
	told := readPipe(er, 64)
	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		_, _ = w.Write(secret)
		w.Close()
	}()
	// The helper answers an ask for place alone; it reads the ask to the
	// end first, as git writes all of it.
	f := loginFields([]byte(place))
	helper := `!f() { if test "$1" = erase; then cat >/dev/null; echo erase >&4; fi; test "$1" = get || exit 0; p=; h=; a=; u=; while IFS= read -r l; do case "$l" in ` +
		`protocol=*) p=${l#protocol=};; host=*) h=${l#host=};; path=*) a=${l#path=};; username=*) u=${l#username=};; esac; done; ` +
		`test "$p" = ` + shellQuote(string(f["protocol"])) + ` && test "$h" = ` + shellQuote(string(f["host"])) +
		` && test "$a" = ` + shellQuote(string(f["path"])) + ` && test "$u" = ` + shellQuote(string(f["username"])) +
		` && cat <&3; }; f`
	// git's messages in English, as refusedLogin reads them.
	out, err = gitRunWith(fetchTimeout, append(noAskpass(env), "LC_ALL=C"), repo,
		[]string{"credential.helper=", "credential.helper=" + helper}, []*os.File{r, ew}, args...)
	ew.Close()
	erased = len(told()) > 0
	// What git did not read is read here, and gone.
	b := make([]byte, 512)
	for {
		n, err := r.Read(b)
		wipe(b[:n])
		if err != nil {
			break
		}
	}
	r.Close()
	<-wrote
	return out, err, erased
}

// catchLogin runs git with args as the user's helpers have it, and is
// the login git stored when origin took one for place, to be held until
// until: one more helper, last, writes what it is told to store to fd 3.
// A login that runs out (password_expiry_utc) is held till a little
// before it does, or not at all.
func catchLogin(repo string, env []string, place string, args []string) (out string, err error, secret []byte, until time.Time) {
	r, w, err := os.Pipe()
	if err != nil {
		out, err := gitRunEnv(fetchTimeout, env, repo, args...)
		return out, err, nil, until
	}
	defer r.Close()
	got := readPipe(r, 64<<10)
	out, err = gitRunWith(fetchTimeout, env, repo,
		[]string{`credential.helper=!f() { test "$1" = store || exit 0; cat >&3; printf '\000' >&3; }; f`},
		[]*os.File{w}, args...)
	w.Close()
	stored := got()
	defer wipe(stored)
	want := loginFields([]byte(place))
	for _, one := range bytes.Split(stored, []byte{0}) {
		fields := loginFields(one)
		user, password := fields["username"], fields["password"]
		// The username git asked for, when it asked for one, as the
		// helper that gives the login checks it.
		if loginPlaceOf(fields, false) != loginPlaceOf(want, false) || len(user) == 0 || len(password) == 0 ||
			len(want["username"]) > 0 && !bytes.Equal(user, want["username"]) {
			continue
		}
		end := time.Now().Add(loginFresh)
		if e, ok := fields["password_expiry_utc"]; ok {
			n, perr := strconv.ParseInt(string(e), 10, 64)
			if perr != nil {
				continue
			}
			if t := time.Unix(n, 0).Add(-loginMargin); t.Before(end) {
				end = t
			}
			if !end.After(time.Now()) {
				continue
			}
		}
		wipe(secret)
		until = end
		secret = make([]byte, 0, len(user)+len(password)+20)
		secret = append(append(append(secret, "username="...), user...), "\npassword="...)
		secret = append(append(secret, password...), '\n')
	}
	return out, err, secret, until
}

// rejectLogin tells the user's helpers origin refused the login secret
// for origin's url, as git does when origin refuses one with 401: a helper
// that keeps it (the keychain, a cache) forgets it. git is given the
// whole URL, for it picks the helpers by it, and asks them as for the
// fetch.
func rejectLogin(repo string, env []string, url string, secret []byte) {
	in := []byte("url=" + url + "\n")
	in = append(in, secret...)
	defer wipe(in)
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeoutShort)
	defer cancel()
	cmd := proc.Detach(exec.CommandContext(ctx, "git", "-C", repo, "credential", "reject"))
	cmd.Env = append(slices.Clip(env), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = bytes.NewReader(in)
	_ = cmd.Run()
}

// refusedLogin says whether git's err is origin refusing a login, or git
// having none it took: an expired token (401), one for some repositories
// alone (403, or, as GitHub has it, a repository not found), or a
// redirect elsewhere the login held is not for. It is read from what git
// and origin wrote, which may say anything: it has the fetch run again
// with the user's helpers, never a login rejected (see giveLogin).
func refusedLogin(err error) bool {
	msg := err.Error()
	for _, s := range []string{
		"Authentication failed", "returned error: 401", "returned error: 403",
		"could not read Username", "could not read Password", "terminal prompts disabled",
		"' not found",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func takeLogin(key [32]byte) []byte {
	logins.Lock()
	defer logins.Unlock()
	if h := logins.held[key]; h != nil {
		return bytes.Clone(h.secret)
	}
	return nil
}

func holdLogin(key [32]byte, secret []byte, until time.Time) {
	logins.Lock()
	defer logins.Unlock()
	if logins.held == nil {
		logins.held = map[[32]byte]*heldLogin{}
	}
	if old := logins.held[key]; old != nil {
		old.timer.Stop()
		wipe(old.secret)
	}
	h := &heldLogin{secret: secret}
	h.timer = time.AfterFunc(time.Until(until), func() {
		logins.Lock()
		defer logins.Unlock()
		if logins.held[key] == h {
			delete(logins.held, key)
		}
		wipe(h.secret)
	})
	logins.held[key] = h
}

func dropLogin(key [32]byte) {
	logins.Lock()
	defer logins.Unlock()
	if h := logins.held[key]; h != nil {
		h.timer.Stop()
		wipe(h.secret)
		delete(logins.held, key)
	}
}

// forgetLogins drops every login held; for the tests.
func forgetLogins() {
	logins.Lock()
	keys := make([][32]byte, 0, len(logins.held))
	for k := range logins.held {
		keys = append(keys, k)
	}
	logins.Unlock()
	for _, k := range keys {
		dropLogin(k)
	}
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// loginFields are the key=value lines of what git tells a helper, or a
// helper git, as slices of b: a password is never made a string, which
// could not be wiped. A key named twice keeps the first.
func loginFields(b []byte) map[string][]byte {
	fields := map[string][]byte{}
	for _, l := range bytes.Split(b, []byte("\n")) {
		k, v, ok := bytes.Cut(l, []byte("="))
		if _, seen := fields[string(k)]; ok && !seen {
			fields[string(k)] = v
		}
	}
	return fields
}

// loginPlaceOf is the place lines of fields, with the username when user
// is set and there is one; "" when it is not https.
func loginPlaceOf(fields map[string][]byte, user bool) string {
	if string(fields["protocol"]) != "https" || len(fields["host"]) == 0 {
		return ""
	}
	place := "protocol=https\nhost=" + string(fields["host"]) + "\n"
	if p, ok := fields["path"]; ok {
		place += "path=" + string(p) + "\n"
	}
	if u := fields["username"]; user && len(u) > 0 {
		place += "username=" + string(u) + "\n"
	}
	return place
}

// readPipe reads r, up to max bytes, until got is called: then what is
// left in the pipe, or till a little while, for a program git ran that
// lives on (git credential-cache's daemon) may hold the other end open.
func readPipe(r *os.File, max int) (got func() []byte) {
	// Room for all of it from the start: a buffer that grew would leave
	// a copy of a login behind, not wiped.
	buf := make([]byte, max)
	n := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for n < max {
			m, err := r.Read(buf[n:])
			n += m
			if err != nil {
				return
			}
		}
		// More than any login: keep none of it.
		wipe(buf)
		n = 0
		b := make([]byte, 512)
		for {
			m, err := r.Read(b)
			wipe(b[:m])
			if err != nil {
				return
			}
		}
	}()
	return func() []byte {
		_ = r.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		<-done
		return buf[:n]
	}
}

// gitEnvOut runs git with args in repo with env, for its output: none
// that has a login in it.
func gitEnvOut(env []string, repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeoutShort)
	defer cancel()
	cmd := proc.Detach(exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...))
	cmd.Env = append(slices.Clip(env), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return string(out), err
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
