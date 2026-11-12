package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiedObjectsIgnoresHostDelegatesAndRewrites(t *testing.T) {
	a, _, marks, _ := pinRepo(t)
	id := revParse(t, a.Path, "HEAD")
	script := filepath.Join(a.Path, "pack-hook")
	marker := filepath.Join(marks, "pack-hook")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// packObjectsHook is protected-scope-only. An inherited command-scope
	// setting is a realistic protected delegate into editable source.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "uploadpack.packObjectsHook")
	t.Setenv("GIT_CONFIG_VALUE_0", script)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'url.ext::false.insteadOf="+a.Path+"'")
	t.Setenv("GIT_EXEC_PATH", a.Path)
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext")
	objects, done, err := verifiedObjects(a.Path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, err := os.Stat(objects); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("pack hook ran: %v", err)
	}
}

func TestVerifiedObjectsIgnoresConfiguredTemplates(t *testing.T) {
	a, _, marks, _ := pinRepo(t)
	id := revParse(t, a.Path, "HEAD")
	template := t.TempDir()
	hooks := filepath.Join(template, "hooks")
	if err := os.Mkdir(hooks, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(marks, "template-hook")
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	run(t, a.Path, "git", "config", "--file", config, "init.templateDir", template)
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_TEMPLATE_DIR", template)
	objects, done, err := verifiedObjects(a.Path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, err := os.Stat(filepath.Join(filepath.Dir(objects), "hooks", "reference-transaction")); !os.IsNotExist(err) {
		t.Fatalf("inherited template hook was installed: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("template hook ran: %v", err)
	}
}

// Exercise the real upload-pack child: client command-scope settings are
// discarded by local transport. An alternate object directory also lets us
// exercise alternateRefsCommand in the same server repository and config scope.
func TestVerifiedObjectsSuppressesServerDelegates(t *testing.T) {
	a, _, marks, _ := pinRepo(t)
	id := revParse(t, a.Path, "HEAD")
	marker := filepath.Join(marks, "server-delegate")
	script := filepath.Join(t.TempDir(), "delegate")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	alternate := t.TempDir()
	run(t, alternate, "git", "init", "--bare", "--quiet", ".")
	if err := os.WriteFile(filepath.Join(a.Path, ".git", "objects", "info", "alternates"), []byte(filepath.Join(alternate, "objects")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(t, a.Path, "git", "config", "core.alternateRefsCommand", script)
	// Ordinary upload-pack does not currently enumerate alternate refs, but
	// that delegate accepts local config. Prove it is executable and that
	// our server-side override suppresses it independently of client env.
	run(t, a.Path, "git", "rev-list", "--alternate-refs", "HEAD")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("baseline did not execute the local alternate-ref delegate: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	env := verifiedGitEnv()
	var serverEnv []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") && !strings.HasPrefix(kv, "GIT_CONFIG_KEY_") && !strings.HasPrefix(kv, "GIT_CONFIG_VALUE_") {
			serverEnv = append(serverEnv, kv)
		}
	}
	probe := strings.TrimSuffix(verifiedUploadPack, "upload-pack") + "rev-list --alternate-refs HEAD"
	cmd := exec.Command("sh", "-c", probe)
	cmd.Dir, cmd.Env = a.Path, serverEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("server alternate-ref probe: %v %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("server alternate-ref delegate ran: %v", err)
	}
	// packObjectsHook is ignored locally, but honored in global config.
	// Both must stay inert, including when the server reads config itself.
	run(t, a.Path, "git", "config", "uploadpack.packObjectsHook", script)
	global := filepath.Join(t.TempDir(), "global")
	run(t, a.Path, "git", "config", "--file", global, "uploadpack.packObjectsHook", script)
	client := t.TempDir()
	run(t, client, "git", "init", "--bare", "--quiet", ".")
	cmd = exec.Command("git", "fetch", "--quiet", "--depth=1", a.Path, id)
	cmd.Dir = client
	cmd.Env = append(verifiedGitEnv(), "GIT_CONFIG_GLOBAL="+global)
	// This hook produces no pack, so the fetch fails after executing it.
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("baseline unexpectedly accepted an empty pack: %s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("baseline did not execute the global pack hook: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_SYSTEM", global)
	objects, done, err := verifiedObjects(a.Path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, err := os.Stat(objects); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("server delegate ran: %v", err)
	}
}

// TestVerifiedObjectsAreInTheStateFolder: the verified store is made in
// the state folder's temporary folder, which a sandbox must keep its
// agent from writing, not in os.TempDir, which sandboxes tend to allow.
func TestVerifiedObjectsAreInTheStateFolder(t *testing.T) {
	a, _, _, _ := pinRepo(t)
	id := revParse(t, a.Path, "HEAD")
	tmp, state := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("XDG_STATE_HOME", state)
	objects, done, err := verifiedObjects(a.Path, id)
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Dir(objects)
	if filepath.Dir(store) != filepath.Join(state, "nerdr", "tmp") || !strings.HasPrefix(filepath.Base(store), "nerdr-objects-") {
		t.Fatalf("store made at %s", store)
	}
	if strings.HasPrefix(store, tmp) {
		t.Fatalf("store %s is in os.TempDir", store)
	}
	done()
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("store left: %v", err)
	}
}
