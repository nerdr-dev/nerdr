// Package gitlab fetches merge-request status (approvals, pipelines,
// discussions) and issues from GitLab, gitlab.com or a server of your own,
// via the `glab` CLI, the way the github package does with `gh`: no API
// tokens to manage, it uses whatever `glab auth` login the user already
// has.
//
// Everything goes through `glab api`, GitLab's REST API (v4), with the
// host named: its answers are GitLab's own and stay the same from one
// glab to the next, where the JSON of glab's other commands has not.
// What GitLab says is turned into forge's words, which are GitHub's: an
// "opened" merge request is OPEN, a "failed" job FAILURE.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/forge"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// glabTimeout bounds each glab call, as gh's are bounded: one that hangs
// must not keep what waits on it waiting for ever. glabLongTimeout is for
// one that reads a lot: every page of a merge request's discussions, a
// job's log.
var (
	glabTimeout     = 45 * time.Second
	glabLongTimeout = 2 * time.Minute
)

// NotInstalled is what is said when glab is not there.
const NotInstalled = "the glab CLI is not installed"

// Available reports whether the glab CLI is installed.
func Available() bool {
	_, err := exec.LookPath("glab")
	return err == nil
}

// call is one glab api call: on Host, at Path (with its query), with
// Method and the string Fields (key=value) of a POST.
type call struct {
	Host     string
	Path     string
	Method   string
	Fields   []string
	Paginate bool
	Timeout  time.Duration
}

// run runs glab in dir (none when ""), behind nerdr's screen, where
// nobody could answer a prompt: glab is told not to ask, nor to look for
// a newer glab. Its environment is that gitsafe gives for git in dir, or
// gitsafe.EnvNoRepo with noRepo, for a folder inside a repository that is
// not the one glab works on (a clone's parent).
var run = func(timeout time.Duration, dir string, noRepo bool, env []string, args ...string) ([]byte, error) {
	if !Available() {
		return nil, errors.New(NotInstalled)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "glab", args...)
	cmd.Dir = dir
	base := gitsafe.EnvNoRepo()
	if dir != "" && !noRepo {
		base = gitsafe.Env(dir)
	}
	cmd.Env = append(base, "NO_PROMPT=1", "GLAB_NO_PROMPT=1", "GLAB_CHECK_UPDATE=false",
		"GLAB_SEND_TELEMETRY=false", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
	cmd.Env = append(cmd.Env, env...)
	proc.Detach(cmd)
	out, err := proc.Output(cmd)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.New("glab timed out")
		}
		return nil, err
	}
	return out, nil
}

// outside is where glab runs when it needs no repository: in none. glab
// reads a repository's own config (<git dir>/glab-cli/config.yml), which
// may name another api_host for a host, and an agent can write to the git
// dir of its worktree: glab given the user's token for a host must not
// go by that.
const outside = "/"

// api makes c, and says what went wrong in a line when it fails. dir is
// the repository it is about, for nothing but that: c names its project
// and host in full, and glab runs outside any repository (see outside).
func api(dir string, c call) ([]byte, error) {
	args := []string{"api", c.Path, "--hostname", c.Host}
	if c.Method != "" && c.Method != "GET" {
		args = append(args, "--method", c.Method)
	}
	for _, f := range c.Fields {
		args = append(args, "--raw-field", f)
	}
	if c.Paginate {
		args = append(args, "--paginate")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = glabTimeout
	}
	out, err := run(timeout, outside, true, nil, args...)
	if err != nil {
		return nil, errors.New(errorMessage(c.Host, err))
	}
	return out, nil
}

// getJSON is a GET of path on host, its answer read into v.
func getJSON(dir, host, path string, v any) error {
	out, err := api(dir, call{Host: host, Path: path})
	if err != nil {
		return err
	}
	if json.Unmarshal(out, v) != nil {
		return errors.New("could not parse the output from glab")
	}
	return nil
}

// getAll is every page of the list at path on host, read into a slice
// of T. glab prints each page's array after the last, so they are read
// one after another.
func getAll[T any](dir, host, path string, timeout time.Duration) ([]T, error) {
	out, err := api(dir, call{Host: host, Path: path, Paginate: true, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	return decodePages[T](out)
}

func decodePages[T any](out []byte) ([]T, error) {
	var all []T
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var page []T
		if err := dec.Decode(&page); err != nil {
			if err.Error() == "EOF" {
				return all, nil
			}
			return nil, errors.New("could not parse the output from glab")
		}
		all = append(all, page...)
	}
}

// project is the API's name for the project at path: GitLab takes the
// whole path, its slashes escaped, where it takes an ID.
func project(path string) string { return "projects/" + url.PathEscape(path) }

// query is v as a URL's query, after a "?".
func query(path string, v url.Values) string {
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

// errorMessage is a line saying why glab failed: that it is not logged
// in to host, said with the command that fixes it, or glab's own first
// line.
func errorMessage(host string, err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(string(ee.Stderr))
		if NotLoggedIn(msg) {
			return NotLoggedInMessage(host)
		}
		if msg != "" {
			return forge.FirstLine(msg)
		}
	}
	if err.Error() == NotInstalled || strings.HasPrefix(err.Error(), "glab ") {
		return err.Error()
	}
	return "glab failed: " + err.Error()
}

// NotLoggedInMessage is what nerdr says when glab has no login on host,
// or one GitLab turned down.
func NotLoggedInMessage(host string) string {
	if host == "" || host == "gitlab.com" {
		return "glab is not logged in to GitLab: run glab auth login in a terminal"
	}
	return "glab is not logged in to " + host + ": run glab auth login --hostname " + host + " in a terminal"
}

// NotLoggedIn is whether glab's message stderr says it has no login, or
// one GitLab turned down.
func NotLoggedIn(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "glab auth login") || strings.Contains(s, "401") ||
		strings.Contains(s, "unauthorized") || strings.Contains(s, "no token") ||
		strings.Contains(s, "not authenticated")
}

// NotFound is whether err, from asking GitLab about a project, is
// because it has none at that path, or none you may see: asking again
// will not help.
func NotFound(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "404") || strings.Contains(s, "not found")
}

// forbidden is whether err is GitLab's turning down a call you may not
// make: asking again will not help.
func forbidden(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "403") || strings.Contains(s, "forbidden")
}

// partError is the failure of a call about a part of a merge request
// (its approvals, its pipeline) after its project answered: the project
// is there, whatever the error says, and the next look asks again.
type partError struct{ err error }

func (e partError) Error() string { return e.err.Error() }
func (e partError) Unwrap() error { return e.err }

// rawUser is a user as the API has it.
type rawUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

// users is who you are on each host, asked once.
var users = struct {
	sync.Mutex
	m map[string]rawUser
}{m: map[string]rawUser{}}

// me is who glab is logged in as on host.
func me(host string) (rawUser, error) {
	users.Lock()
	u, ok := users.m[host]
	users.Unlock()
	if ok {
		return u, nil
	}
	if err := getJSON("", host, "user", &u); err != nil {
		return rawUser{}, err
	}
	users.Lock()
	users.m[host] = u
	users.Unlock()
	return u, nil
}

// rawProject is a project as the API has it.
type rawProject struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	DefaultBranch     string `json:"default_branch"`
	WebURL            string `json:"web_url"`
	SSHURL            string `json:"ssh_url_to_repo"`
	HTTPURL           string `json:"http_url_to_repo"`
	Namespace         struct {
		FullPath string `json:"full_path"`
	} `json:"namespace"`
}

// projects keeps the projects asked about, by host and path, and by host
// and ID: a project's ID and default branch hardly change, and a look at
// a branch's merge request needs them every time.
var projects = struct {
	sync.Mutex
	byPath map[string]rawProject
	byID   map[string]rawProject
}{byPath: map[string]rawProject{}, byID: map[string]rawProject{}}

// projectAt is the project at path on host.
func projectAt(dir, host, path string) (rawProject, error) {
	k := host + "/" + strings.ToLower(path)
	projects.Lock()
	p, ok := projects.byPath[k]
	projects.Unlock()
	if ok {
		return p, nil
	}
	if err := getJSON(dir, host, project(path), &p); err != nil {
		return rawProject{}, err
	}
	keepProject(host, p)
	return p, nil
}

// projectByID is the project with id on host.
func projectByID(dir, host string, id int) (rawProject, error) {
	k := host + "#" + strconv.Itoa(id)
	projects.Lock()
	p, ok := projects.byID[k]
	projects.Unlock()
	if ok {
		return p, nil
	}
	if err := getJSON(dir, host, "projects/"+strconv.Itoa(id), &p); err != nil {
		return rawProject{}, err
	}
	keepProject(host, p)
	return p, nil
}

func keepProject(host string, p rawProject) {
	projects.Lock()
	projects.byPath[host+"/"+strings.ToLower(p.PathWithNamespace)] = p
	projects.byID[host+"#"+strconv.Itoa(p.ID)] = p
	projects.Unlock()
}
