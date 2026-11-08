package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/editor"
	"github.com/nerdr-dev/nerdr/internal/holder"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// errStarting is a core that could not start: starting it again would not
// help.
type errStarting struct{ error }

// startUnsized is how long the core waits for a client to say how large
// it shows the agents, before it starts them anyway.
const startUnsized = 5 * time.Second

// runCore is nerdr's core with no terminal, over holder h: it keeps the
// tabs and their agents in the folder it was started in, and a client in a
// process of its own shows them, by the client socket. It returns once a
// client has had it quit (core.QuitAll), or it is told to end.
func runCore(h *core.Held) error {
	if h == nil {
		return errStarting{errors.New("nerdr core runs only under its holder: start nerdr with NERDR_HOLDER=1")}
	}
	cfg, err := config.Load()
	if err != nil {
		return errStarting{err}
	}
	browser.Setting = cfg.Browser
	editor.File, editor.Worktree = cfg.Editor, cfg.WorktreeEditor
	cwd, err := os.Getwd()
	if err != nil {
		return errStarting{err}
	}
	root, _ := filepath.EvalSymlinks(cwd)
	if root == "" {
		root = cwd
	}
	release, err := state.Lock(root)
	if err != nil {
		return errStarting{err}
	}
	defer release()
	sess, loadErr := state.Load(root)

	core.Logf = func(format string, args ...any) {
		core.Debugf(format, args...)
		fmt.Fprintf(os.Stderr, "%s nerdr core: %s\n", time.Now().Format(time.TimeOnly), fmt.Sprintf(format, args...))
	}
	core.Logf("started for %s, %d tabs from the saved session", root, len(sess.Tabs))

	c := &core.Core{
		Cfg:    cfg,
		Root:   root,
		WtRoot: workspace.WorktreesRoot(root, cfg.WorktreesDir),
		Wake:   make(chan struct{}, 1),
		Held:   h,
	}
	now := time.Now()
	c.Restore(sess, now)
	var front, beside core.TabRef
	if sess.Active >= 0 && sess.Active < len(c.Tabs) {
		front = c.Ref(c.Tabs[sess.Active])
	}
	for _, t := range c.Tabs {
		if sess.Split != "" && t.ID == sess.Split {
			beside = c.Ref(t)
		}
	}
	c.Client = core.Unattached(core.Layout{Active: sess.Active, Split: sess.Split, SplitLeft: sess.SplitLeft, SideWidth: sess.SideWidth}, front)

	var loop *core.Loop
	post := func(ev core.Event) {
		if loop != nil {
			loop.Post(ev)
		}
	}
	hookSrv, err := hooks.Listen(hooks.SocketPath(), func(r hooks.Report) { post(r) })
	var taken error
	if errors.Is(err, hooks.ErrTaken) {
		taken = err
		hookSrv, err = hooks.Listen(hooks.ForRoot(hooks.SocketPath(), root), func(r hooks.Report) { post(r) })
	}
	if err == nil {
		defer hookSrv.Close()
		c.SetHookSocket(hookSrv.Path())
		if taken != nil {
			c.SetSocketShared(taken)
		}
	} else {
		c.SetHookSocketError(err)
	}
	if _, err := exec.LookPath("gpg-connect-agent"); err == nil {
		_ = exec.Command("gpg-connect-agent", "/bye").Run()
	}
	if loadErr != nil {
		c.Tell(core.NoticeMsg("could not read the saved session, started fresh: " + loadErr.Error() +
			" — earlier copies are in " + state.SnapshotDir()))
	}

	quit := make(chan struct{})
	panicked := make(chan core.PanicMsg, 1)
	c.OnQuit = func() { close(quit) }
	loop = core.NewLoop(c, func(ev core.Event) {
		if p, ok := ev.(core.PanicMsg); ok {
			select {
			case panicked <- p:
			default:
			}
			return
		}
		c.Mu.Lock()
		rc, _ := c.Client.(*core.RemoteClient)
		c.Mu.Unlock()
		if rc != nil {
			rc.Forward(ev)
		}
	})
	attachPost := func(ev core.RequestEv) { loop.Post(ev) }

	c.Mu.Lock()
	c.TakeUpHeld()
	c.NoteUnseen()
	if n := len(c.Unseen); n > 0 {
		c.Tell(core.NoticeMsg(core.Plural(n, "program") + " from before nerdr started again run on in the holder, in no tab: " +
			cfg.Prefix + " j shows them, and stops them"))
	}
	c.Mu.Unlock()
	stopCk := make(chan struct{})
	defer close(stopCk)
	go c.Checkpoints(stopCk)

	sock, keyFile, err := attach.Paths(root)
	if err != nil {
		return errStarting{err}
	}
	srv, err := attach.Listen(sock, keyFile, attach.Config{
		Version: version(),
		Check: func(p hooks.Peer) string {
			c.Mu.Lock()
			why := c.ClientRefusal(p)
			c.Mu.Unlock()
			if why != "" {
				core.Logf("client refused (%v): %s", p.Chain, why)
			}
			return why
		},
		Client: func(conn *attach.Conn, _ attach.Hello) {
			core.Logf("client: connected")
			c.Mu.Lock()
			c.AttachClient(conn, attachPost)
			c.Mu.Unlock()
		},
	})
	if err != nil {
		return errStarting{err}
	}
	defer srv.Close(attach.ByeQuit)

	loop.Start()
	defer loop.Stop()
	// The client that started the core connects at once, and says how
	// large it shows the agents; without one they start after a while.
	loop.Post(core.RequestEv{R: core.StartWhenSized{Front: front, Beside: beside}})
	time.AfterFunc(startUnsized, func() { loop.Post(core.RequestEv{R: core.StartWaiting{}}) })
	if cfg.Install != "" {
		loop.Post(core.RequestEv{R: core.ApplySkills{}})
	}
	c.Mu.Lock()
	prune := core.PruneOrders(c.Tabs)
	c.Mu.Unlock()
	loop.Run(prune)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	signal.Ignore(syscall.SIGHUP)
	select {
	case <-quit:
		core.Logf("quit by its client")
		return nil
	case p := <-panicked:
		fmt.Fprintln(os.Stderr, "nerdr core: panic:", p.String())
		return errors.New("panic")
	case <-h.Client.Gone():
		// The holder crashed, and its watch has ended the programs: a
		// core of its own has nothing left to run, and would keep the
		// folder from the nerdr started next. That one resumes the
		// agents from the session, as after any crash.
		c.Mu.Lock()
		c.SaveSession()
		c.Mu.Unlock()
		srv.Close(attach.ByeHolderGone)
		return errors.New("its holder is gone")
	case s := <-sig:
		// Told to end: the programs live on in the holder, for the core
		// started again; SIGTERM to the holder itself stops them.
		return fmt.Errorf("ended by %v", s)
	}
}

// coreMain runs the core and is its exit status: holder.ExitDone when it
// could not start or was had to quit, for the holder to end too; anything
// else when it crashed, for the holder to start it again.
func coreMain() int {
	h, err := held()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr core: holder:", err)
		return holder.ExitDone
	}
	err = runCore(h)
	var starting errStarting
	switch {
	case err == nil:
		return holder.ExitDone
	case errors.As(err, &starting):
		fmt.Fprintln(os.Stderr, "nerdr core:", err)
		return holder.ExitDone
	}
	fmt.Fprintln(os.Stderr, "nerdr core:", err)
	return 2
}
