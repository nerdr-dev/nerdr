package config

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Server is another machine whose core the TUI shows, a [[servers]] table:
// the core of Folder on SSH, reached over ssh, named Name in the tab bar.
type Server struct {
	// Name is the machine's among the TUI's: in the tab bar, and in the
	// ref of each of its tabs.
	Name string `toml:"name"`
	// SSH is the host as ssh knows it, ~/.ssh/config applying: Name when
	// it is not given.
	SSH string `toml:"ssh,omitempty"`
	// Folder is the folder there whose core is shown: "~" when it is not
	// given.
	Folder string `toml:"folder,omitempty"`
}

// Host is the host ssh reaches s on.
func (s Server) Host() string { return cmp.Or(s.SSH, s.Name) }

// Dir is the folder on s whose core is shown.
func (s Server) Dir() string { return cmp.Or(s.Folder, "~") }

// CheckRemote says why host and folder cannot name a core reached over
// ssh, nil when they can: only a host ssh takes as one, and a folder that
// is a plain word to the shells on the way, whose quoting it is put in.
func CheckRemote(host, folder string) error {
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsFunc(host, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@", r))
	}) {
		return fmt.Errorf("not a host: %q", host)
	}
	if strings.ContainsAny(folder, "'\"$`\\\n\x00") || strings.HasPrefix(folder, "-") {
		return fmt.Errorf("a folder nerdr cannot name over ssh: %q", folder)
	}
	return nil
}

// Check says why s cannot be connected to, nil when it can.
func (s Server) Check() error { return CheckRemote(s.Host(), s.Dir()) }

// CheckServerName says why name cannot name a server among those of
// servers, nil when it can.
func CheckServerName(name string, servers []Server) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("a server needs a name")
	case strings.ContainsAny(name, ": \t\"\\\n"):
		return fmt.Errorf("a server's name has no spaces, colons or quotes: %q", name)
	}
	for _, s := range servers {
		if s.Name == name {
			return fmt.Errorf("there is a server named %s already", name)
		}
	}
	return nil
}

// AddServer adds s to [[servers]] in the config file, last.
func AddServer(s Server) error {
	c, err := Load()
	if err != nil {
		return err
	}
	if err := CheckServerName(s.Name, c.Servers); err != nil {
		return err
	}
	if err := s.Check(); err != nil {
		return err
	}
	return editConfig(func(text string) string {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n[[servers]]\nname = " + strconv.Quote(s.Name) + "\n"
		if s.SSH != "" {
			text += "ssh = " + strconv.Quote(s.SSH) + "\n"
		}
		if s.Folder != "" {
			text += "folder = " + strconv.Quote(s.Folder) + "\n"
		}
		return text
	})
}

// RemoveServer takes the server named name out of [[servers]] in the
// config file, with the comments in its table.
func RemoveServer(name string) error {
	return editConfig(func(text string) string { return removeServer(text, name) })
}

func removeServer(text, name string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		start := strings.Index(text[i:], "[[servers]]")
		if start < 0 || !lineStart(text, i+start) {
			out.WriteString(text[i:])
			break
		}
		start += i
		bodyStart := start + len("[[servers]]")
		end := bodyStart + nextTable(text[bodyStart:])
		if serverNamed(text[bodyStart:end], name) {
			out.WriteString(strings.TrimRight(text[i:start], " \t\n"))
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			if end < len(text) {
				out.WriteString("\n")
			}
		} else {
			out.WriteString(text[i:end])
		}
		i = end
	}
	return out.String()
}

// lineStart reports whether text's i-th byte starts a line, but for
// blanks before it.
func lineStart(text string, i int) bool {
	j := strings.LastIndexByte(text[:i], '\n')
	return strings.TrimLeft(text[j+1:i], " \t") == ""
}

// serverNamed reports whether body, a [[servers]] table's, names name.
func serverNamed(body, name string) bool {
	var s Server
	_, err := toml.Decode(body, &s)
	return err == nil && s.Name == name
}
