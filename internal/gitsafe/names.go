package gitsafe

import (
	"os"
	"path/filepath"
	"strings"
)

// namesFileIn reports whether command, an ssh command or a credential
// helper as git's config has it, may run or read a file in one of roots
// (the main checkout first, where git runs it), which a sandboxed agent
// may have written. git gives both to sh, so command is read as sh would
// read it. What nerdr cannot read for certain counts as naming such a
// file: shell syntax beyond plain words (;, |, redirections, $VAR other
// than $HOME and the arguments git passes, `...`, globs), a relative path
// (git runs it in the repository), the operand of a shell or interpreter,
// and a file option's relative value (-F config, -ikey, --file=.creds).
// A path is judged both as written and with its links resolved, against
// roots with theirs resolved.
func namesFileIn(command string, roots []string) bool {
	c := names{home: homeDir()}
	for _, r := range roots {
		if r != "" {
			c.roots = append(c.roots, filepath.Clean(r), canonical(r))
		}
	}
	return c.command(strings.TrimPrefix(strings.TrimSpace(command), "!"), 0)
}

// programIn reports whether GIT_SSH's program, which git runs itself
// rather than through sh, is a file in one of roots.
func programIn(program string, roots []string) bool {
	c := names{home: homeDir()}
	for _, r := range roots {
		if r != "" {
			c.roots = append(c.roots, filepath.Clean(r), canonical(r))
		}
	}
	return c.path(program)
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

type names struct {
	roots []string
	home  string
}

// command is whether the sh command s may name a file in the roots.
func (c names) command(s string, depth int) bool {
	if depth > 4 {
		return true
	}
	words, ok := shellWords(s, c.home)
	if !ok {
		return true
	}
	return c.argv(words, depth)
}

// argv is command for a command line split into words.
func (c names) argv(words []string, depth int) bool {
	for len(words) > 0 && isAssignment(words[0]) {
		_, v, _ := strings.Cut(words[0], "=")
		if c.value("", v, depth) {
			return true
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return false
	}
	prog := words[0]
	if c.path(prog) {
		return true
	}
	base := filepath.Base(prog)
	if wrappers[base] {
		rest := words[1:]
		for len(rest) > 0 && (strings.HasPrefix(rest[0], "-") || isAssignment(rest[0]) || isNumber(rest[0])) {
			if c.arg(rest[0], false, depth) {
				return true
			}
			rest = rest[1:]
		}
		return c.argv(rest, depth)
	}
	interp := isInterpreter(base)
	file := false
	for _, w := range words[1:] {
		if file {
			file = false
			if c.file(w) {
				return true
			}
			continue
		}
		if c.arg(w, interp, depth) {
			return true
		}
		file = fileFlags[w]
	}
	return false
}

// arg is whether one argument w of a command may name a file in the
// roots; interp is whether the command is a shell or an interpreter,
// whose operands are files or code.
func (c names) arg(w string, interp bool, depth int) bool {
	if strings.ContainsAny(w, " \t") && !c.absolute(w) {
		// sh -c '…', ProxyCommand=…: a command of its own.
		if k, v, ok := strings.Cut(w, "="); ok && !strings.ContainsAny(k, " \t/") {
			return c.value(optionKey(k), v, depth)
		}
		return c.command(w, depth+1)
	}
	if strings.HasPrefix(w, "-") {
		if k, v, ok := strings.Cut(w, "="); ok {
			return c.value(optionKey(k), v, depth) // --file=x, -oIdentityFile=x
		}
		if !strings.HasPrefix(w, "--") && len(w) > 2 {
			if fileFlags[w[:2]] {
				return c.file(w[2:]) // -F/path, -ikey
			}
			return c.path(w[2:]) // -vvv, -p2222, or one nerdr does not know
		}
		return false
	}
	if k, v, ok := strings.Cut(w, "="); ok && k != "" && !strings.Contains(k, "/") {
		return c.value(optionKey(k), v, depth) // IdentityFile=x after -o
	}
	if interp {
		return c.file(w)
	}
	return c.path(w)
}

// value is whether the value v of the option key may name a file.
func (c names) value(key, v string, depth int) bool {
	switch {
	case commandKeys[key]:
		return c.command(v, depth+1)
	case fileKeys[key]:
		return c.file(v)
	case strings.ContainsAny(v, " \t") && !c.absolute(v):
		return c.command(v, depth+1)
	}
	return c.path(v)
}

// file is whether w, which the command reads as a file, may be one in
// the roots: an absolute path in them, or any relative one.
func (c names) file(w string) bool {
	if w == "" || w == "none" {
		return false
	}
	w = c.expand(w)
	if !filepath.IsAbs(w) {
		return true
	}
	return c.in(w)
}

// path is whether the word w, which may or may not be a path, names a
// file in the roots: an absolute path in them, or a relative path
// (./x, x/y, .hidden). A bare name is a program on PATH or a plain
// argument.
func (c names) path(w string) bool {
	if w == "" || strings.Contains(w, "://") {
		return false
	}
	w = c.expand(w)
	if filepath.IsAbs(w) {
		return c.in(w)
	}
	return strings.HasPrefix(w, ".") || strings.Contains(w, "/")
}

func (c names) absolute(w string) bool {
	return filepath.IsAbs(c.expand(w))
}

func (c names) expand(w string) string {
	if c.home != "" && (w == "~" || strings.HasPrefix(w, "~/")) {
		// Preserve ".." components rather than Clean-ing them with
		// filepath.Join: a link before ".." resolves from its target, not
		// lexically.
		return strings.TrimRight(c.home, "/") + w[1:]
	}
	return w
}

// in is whether the absolute path p, as written or resolved component by
// component as the OS opens it, is in one of the roots (or has an
// ambiguous ".." whose target cannot be resolved).
func (c names) in(p string) bool {
	resolved, ok := resolvePath(p)
	if !ok {
		return true
	}
	for _, q := range []string{filepath.Clean(p), resolved} {
		for _, root := range c.roots {
			if rel, err := filepath.Rel(root, q); err == nil && filepath.IsLocal(rel) {
				return true
			}
			if q == root {
				return true
			}
		}
	}
	return false
}

// canonical is p with the links in its longest existing part resolved,
// walking component by component before applying "..".
func canonical(p string) string {
	if !filepath.IsAbs(p) {
		p = clean(p)
	}
	if resolved, ok := resolvePath(p); ok {
		return resolved
	}
	return clean(p)
}

// resolvePath resolves an absolute path p in the same order the OS opens
// it: each component is resolved through EvalSymlinks before any ".."
// after it is applied. ok is false when a ".." follows a component that
// cannot be resolved, so the caller fails closed.
func resolvePath(p string) (string, bool) {
	if !filepath.IsAbs(p) {
		return "", false
	}
	cur := string(filepath.Separator)
	missing := false
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if missing {
				return "", false
			}
			cur = filepath.Dir(cur)
		default:
			next := filepath.Join(cur, part)
			if missing {
				cur = next
				continue
			}
			if real, err := filepath.EvalSymlinks(next); err == nil {
				cur = real
			} else {
				cur = next
				missing = true
			}
		}
	}
	return cur, true
}

// optionKey is an option's name, lower case, without its dashes or ssh's
// -o: "--file" and "-oIdentityFile" are "file" and "identityfile".
func optionKey(k string) string {
	if strings.HasPrefix(k, "-o") && !strings.HasPrefix(k, "--") {
		k = k[2:]
	}
	return strings.ToLower(strings.TrimLeft(k, "-"))
}

func isAssignment(w string) bool {
	k, _, ok := strings.Cut(w, "=")
	if !ok || k == "" || (k[0] >= '0' && k[0] <= '9') {
		return false
	}
	for _, r := range k {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// isNumber is whether w is a number or a duration (timeout 5s).
func isNumber(w string) bool {
	return w != "" && w[0] >= '0' && w[0] <= '9' && strings.TrimRight(w, "0123456789.smhd") == ""
}

func isInterpreter(base string) bool {
	for _, p := range []string{"python", "perl", "ruby", "node", "php", "lua", "pwsh"} {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	return interpreters[base]
}

// Flags whose next word, or rest of the word (-ikey), is a file: ssh's
// config, identity, log, control socket and PKCS#11 library, and the
// usual long ones.
var fileFlags = map[string]bool{
	"-F": true, "-i": true, "-E": true, "-S": true, "-I": true,
	"--file": true, "--config": true, "--config-file": true, "--key": true, "--identity": true, "--include": true,
}

// Options (--key=value, ssh's -o Key=value) whose value is a file.
var fileKeys = map[string]bool{
	"file": true, "config": true, "config-file": true, "key": true, "identity": true, "include": true,
	"identityfile": true, "certificatefile": true, "userknownhostsfile": true, "globalknownhostsfile": true,
	"controlpath": true, "pkcs11provider": true, "securitykeyprovider": true, "revokedhostkeys": true,
	"xauthlocation": true,
}

// ssh options whose value is a command.
var commandKeys = map[string]bool{
	"proxycommand": true, "localcommand": true, "knownhostscommand": true, "remotecommand": true,
}

// Programs that run the command after their own options.
var wrappers = map[string]bool{
	"env": true, "exec": true, "command": true, "nohup": true, "nice": true, "time": true,
	"timeout": true, "xargs": true, "sudo": true, "doas": true, "setsid": true, "stdbuf": true,
}

// Programs whose operands are scripts or code.
var interpreters = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true, "mksh": true, "fish": true,
	"csh": true, "tcsh": true, "busybox": true, "deno": true, "bun": true, "tclsh": true,
	"osascript": true, "awk": true, "gawk": true, "source": true, ".": true, "eval": true,
}

// shellWords splits s into words as sh would, with quotes removed and
// $HOME expanded. The arguments git passes ($@, $*, $1…) become "get".
// ok is false for anything else sh would do with s: run more than one
// command, redirect, substitute a command or another variable, expand a
// glob or a brace, or start a comment.
func shellWords(s, home string) (words []string, ok bool) {
	var b strings.Builder
	in := false // inside a word
	end := func() {
		if in {
			words = append(words, b.String())
			b.Reset()
			in = false
		}
	}
	// dollar expands $… at s[i] (s[i] == '$'), returning the index after it.
	dollar := func(i int) (int, bool) {
		rest := s[i+1:]
		switch {
		case strings.HasPrefix(rest, "{HOME}"):
			b.WriteString(home)
			return i + 7, home != ""
		case strings.HasPrefix(rest, "HOME") && (len(rest) == 4 || !isNameByte(rest[4])):
			b.WriteString(home)
			return i + 5, home != ""
		case rest != "" && (rest[0] == '@' || rest[0] == '*' || (rest[0] >= '1' && rest[0] <= '9')):
			b.WriteString("get")
			return i + 2, true
		}
		return i, false
	}
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t':
			end()
			i++
		case ch == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			b.WriteString(s[i+1 : i+1+j])
			in = true
			i += j + 2
		case ch == '"':
			in = true
			i++
			for {
				if i >= len(s) {
					return nil, false
				}
				c := s[i]
				if c == '"' {
					i++
					break
				}
				switch c {
				case '\\':
					if i+1 < len(s) && strings.IndexByte("$`\"\\", s[i+1]) >= 0 {
						b.WriteByte(s[i+1])
						i += 2
						continue
					}
					b.WriteByte(c)
					i++
				case '$':
					var ok bool
					if i, ok = dollar(i); !ok {
						return nil, false
					}
				case '`', '\n':
					return nil, false
				default:
					b.WriteByte(c)
					i++
				}
			}
		case ch == '\\':
			if i+1 >= len(s) || s[i+1] == '\n' {
				return nil, false
			}
			b.WriteByte(s[i+1])
			in = true
			i += 2
		case ch == '$':
			var ok bool
			if i, ok = dollar(i); !ok {
				return nil, false
			}
			in = true
		case ch == '#' && !in:
			return nil, false
		case strings.IndexByte(";&|<>()`*?[]{}\n\r", ch) >= 0:
			return nil, false
		default:
			b.WriteByte(ch)
			in = true
			i++
		}
	}
	end()
	return words, true
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
