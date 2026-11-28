package forge

import (
	"strings"
	"sync"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// Remote is a repository on a service, as a git remote's URL names it:
// the host, and the path on it, "owner/name" on GitHub, and on GitLab
// "group/name" or "group/subgroup/…/name".
type Remote struct {
	Name string // the git remote's name: origin, upstream
	Host string // lower case; with the port for https
	Path string // as the URL has it, without .git
}

// GitHubHost is github.com, the host GitHub's own searches cover.
const GitHubHost = "github.com"

// Kind is the service the remote is on; see KindOf.
func (r Remote) Kind() Kind { return KindOf(r.Host) }

// Key names the repository the way the searches for your pull requests
// and issues do: "owner/name" on github.com, as nerdr always has, and
// "host/path" on any other host, lower case. A GitHub key has one slash,
// any other at least two.
func (r Remote) Key() string { return RepoKey(r.Host, r.Path) }

// RepoKey is Remote.Key for host and path.
func RepoKey(host, path string) string {
	if host == GitHubHost {
		return strings.ToLower(path)
	}
	return strings.ToLower(host + "/" + path)
}

// KeyHost is the host of a key RepoKey made (a RequestKey's too).
func KeyHost(key string) string {
	key, _, _ = strings.Cut(key, "#")
	if strings.Count(key, "/") <= 1 {
		return GitHubHost
	}
	host, _, _ := strings.Cut(key, "/")
	return host
}

// URL is the repository's page.
func (r Remote) URL() string { return "https://" + r.Host + "/" + r.Path }

// ParseRemote reads a git remote URL, in any of the forms git takes:
// https://host/o/r.git, git@host:o/r.git, ssh://git@host:22/o/r. ok is
// false for a local path, or a URL with no owner and name in it.
func ParseRemote(url string) (Remote, bool) {
	var host, path string
	if scheme, rest, ok := strings.Cut(url, "://"); ok {
		if scheme == "file" {
			return Remote{}, false
		}
		host, path, _ = strings.Cut(rest, "/")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if scheme != "https" && scheme != "http" {
			host, _, _ = strings.Cut(host, ":") // ssh's port is not the web's
		}
	} else if before, after, ok := strings.Cut(url, ":"); ok && !strings.Contains(before, "/") {
		// scp-like: [user@]host:owner/name
		host, path = before, after
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	host = strings.ToLower(host)
	path = strings.Trim(strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git"), "/")
	if host == "" || strings.Count(path, "/") < 1 || strings.Contains(path, "//") {
		return Remote{}, false
	}
	if host == GitHubHost && strings.Count(path, "/") != 1 {
		return Remote{}, false
	}
	return Remote{Host: host, Path: path}, true
}

// hosts is the services of hosts nerdr cannot tell by name, from the
// config's [forges]: host → kind.
var hosts = struct {
	sync.RWMutex
	m map[string]Kind
}{m: map[string]Kind{}}

// KindWith is KindOf by forges, a config's [forges], alone: for what
// another core's config says, which SetHosts has not taken in here.
func KindWith(host string, forges map[string]string) Kind {
	return kindBy(host, parseHosts(forges))
}

// parseHosts is a config's [forges] as kinds, by lower-case host.
func parseHosts(m map[string]string) map[string]Kind {
	k := map[string]Kind{}
	for h, v := range m {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "gitlab":
			k[strings.ToLower(h)] = GitLab
		case "github":
			k[strings.ToLower(h)] = GitHub
		}
	}
	return k
}

// SetHosts takes the config's [forges]: which service each of the hosts
// it names runs ("github" or "gitlab").
func SetHosts(m map[string]string) {
	k := parseHosts(m)
	hosts.Lock()
	hosts.m = k
	hosts.Unlock()
}

// KindOf is the service host runs: as the config says, else GitLab on
// gitlab.com and on a host with gitlab in its name (gitlab.example.com),
// else GitHub, on github.com or GitHub Enterprise.
func KindOf(host string) Kind {
	hosts.RLock()
	defer hosts.RUnlock()
	return kindBy(host, hosts.m)
}

// kindBy is KindOf with by for the config's hosts. A host with a port
// is the host without it, unless by names it with the port.
func kindBy(host string, by map[string]Kind) Kind {
	host = strings.ToLower(host)
	k, ok := by[host]
	if !ok {
		name, _, _ := strings.Cut(host, ":")
		k, ok = by[name]
	}
	if ok {
		return k
	}
	if host == "gitlab.com" || strings.Contains(host, "gitlab") {
		return GitLab
	}
	return GitHub
}

// Remotes is the remotes of the repository at dir that name a repository
// on a service, each once, origin first; odd is whether it has one that
// names none git can be told apart from (a local path, say).
func Remotes(dir string) (remotes []Remote, odd bool, err error) {
	out, err := proc.Output(gitsafe.Command(dir, "remote", "-v"))
	if err != nil {
		return nil, false, err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		r, ok := ParseRemote(fields[1])
		if !ok {
			odd = true
			continue
		}
		r.Name = fields[0]
		if k := r.Name + "\x00" + r.Key(); !seen[k] {
			seen[k] = true
			remotes = append(remotes, r)
		}
	}
	for i, r := range remotes {
		if r.Name == "origin" && i > 0 {
			remotes = append(append([]Remote{r}, remotes[:i]...), remotes[i+1:]...)
			break
		}
	}
	return remotes, odd, nil
}
