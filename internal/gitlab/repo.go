package gitlab

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/forge"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// cloneTimeout bounds a clone made with glab, as git's own clone is.
const cloneTimeout = 15 * time.Minute

// CloneRepo clones the project at path on host into a folder named name
// in parent, with glab, so the login glab has reaches a private project
// too.
func CloneRepo(parent, host, path, name string) (string, error) {
	if !Available() {
		return "", errors.New(NotInstalled)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	// Not pinned to a repository parent is inside, as gh's clone is not.
	if _, err := run(cloneTimeout, parent, true, []string{"GITLAB_HOST=" + host}, "repo", "clone", path, name, "--", "--quiet"); err != nil {
		return "", errors.New(errorMessage(host, err))
	}
	return filepath.Join(parent, name), nil
}

// CreateRepo makes a private project on host, named as the folder dir
// is, in your own namespace, makes it dir's origin and pushes dir's
// branch to it. It returns the project's page. When the project is made
// but the push fails, origin is set: a push from the tab finishes it.
func CreateRepo(dir, host string) (string, error) {
	if !Available() {
		return "", errors.New(NotInstalled)
	}
	out, err := api(dir, call{Host: host, Path: "projects", Method: "POST", Fields: []string{
		"name=" + filepath.Base(dir), "visibility=private",
	}})
	if err != nil {
		return "", err
	}
	var p rawProject
	if jsonUnmarshal(out, &p) != nil || p.WebURL == "" {
		return "", errors.New("could not parse the output from glab")
	}
	keepProject(host, p)
	remote := p.HTTPURL
	if gitProtocol(host) == "ssh" && p.SSHURL != "" {
		remote = p.SSHURL
	}
	if proc.Run(gitsafe.Command(dir, "remote", "get-url", "origin")) == nil {
		err = proc.Run(gitsafe.Command(dir, "remote", "set-url", "origin", remote))
	} else {
		err = proc.Run(gitsafe.Command(dir, "remote", "add", "origin", remote))
	}
	if err != nil {
		return p.WebURL, errors.New("made " + p.WebURL + ", but could not make it origin: " + err.Error())
	}
	if _, err := proc.Output(gitsafe.Command(dir, "push", "--quiet", "--set-upstream", "origin", "HEAD")); err != nil {
		why := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(bytes.TrimSpace(ee.Stderr)) > 0 {
			why = forge.FirstLine(strings.TrimSpace(string(ee.Stderr)))
		}
		return p.WebURL, errors.New("made " + p.WebURL + ", but could not push to it: " + why)
	}
	return p.WebURL, nil
}

// gitProtocol is the protocol glab is set to clone with on host: "ssh"
// or "https".
func gitProtocol(host string) string {
	out, err := run(glabTimeout, outside, true, nil, "config", "get", "git_protocol", "--host", host)
	if err != nil {
		return "https"
	}
	return strings.TrimSpace(string(out))
}
