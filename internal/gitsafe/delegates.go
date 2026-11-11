package gitsafe

import "strings"

// delegateEnv removes executable overrides before both the configuration
// inspection and the actual Git invocation. Git's protocol allowlist is an
// environment restriction, so URL rewrites and protocol.*.allow cannot open
// another transport. No transport is safe by name alone: even SSH can load a
// worktree's ProxyCommand, and HTTP uses an external remote helper.
func delegateEnv(env []string) []string {
	out := make([]string, 0, len(env)+5)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "GIT_PROXY_COMMAND", "GIT_SSH_COMMAND", "GIT_SSH", "GIT_SSH_VARIANT", "GIT_EXEC_PATH",
			"GIT_ASKPASS", "SSH_ASKPASS", "GIT_EXTERNAL_DIFF", "GIT_DIFF_OPTS",
			"GIT_ALLOW_PROTOCOL", "GIT_PROTOCOL_FROM_USER", "GIT_PAGER", "PAGER":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_ALLOW_PROTOCOL=", "GIT_PROTOCOL_FROM_USER=0",
		"GIT_ASKPASS=/dev/null", "SSH_ASKPASS=/dev/null", "GIT_PAGER=")
}

// These settings suppress default executables too. Do not set commit.gpgSign
// or tag.gpgSign false: a user requiring signatures must get a failure, not
// an unsigned commit. filterRefusal exempts only these exact signing keys.
var delegateSettings = [][2]string{
	{"core.sshCommand", "/dev/null"},
	{"credential.helper", ""},
	{"gpg.program", "/dev/null"},
	{"gpg.openpgp.program", "/dev/null"},
	{"gpg.x509.program", "/dev/null"},
	{"gpg.ssh.program", "/dev/null"},
	{"gpg.ssh.defaultKeyCommand", "/dev/null"},
	{"maintenance.auto", "false"},
	{"gc.auto", "0"},
}
