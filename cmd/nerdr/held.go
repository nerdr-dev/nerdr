package main

import (
	"os"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/app"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/holder"
	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// overHolder, with NERDR_HOLDER=1 and no holder yet, has nerdr run over
// one: this process becomes `nerdr holder -- nerdr ARGS`, which starts
// nerdr again as its core, and starts it again if it crashes, the
// programs of the tabs living on in the holder. It returns only when it
// cannot.
func overHolder() error {
	if os.Getenv(core.EnvHolder) != "1" || os.Getenv(holder.EnvFD) != "" {
		return nil
	}
	bin, err := spawn.Bin()
	if err != nil {
		return err
	}
	argv := append([]string{bin, "holder", "--", bin}, os.Args[1:]...)
	return syscall.Exec(bin, argv, os.Environ())
}

// held is the holder that started this nerdr as its core, nil when none
// did.
func held() (*core.Held, error) {
	if os.Getenv(holder.EnvFD) == "" {
		return nil, nil
	}
	cl, err := holder.FromEnv()
	if err != nil {
		return nil, err
	}
	return core.NewHeld(cl)
}

// screensOverPair has the TUI draw core's programs from their screens
// sent as rows over a pair of connections, and send what the user does in
// them back as requests: the protocol of a client in a process of its own,
// in this one.
func screensOverPair(model *app.Model, p *tea.Program) error {
	coreEnd, tuiEnd, err := attach.Pair()
	if err != nil {
		return err
	}
	model.Screens = core.NewScreenServer(coreEnd, func(r core.Request) {
		model.Loop.Post(core.RequestEv{R: r})
	})
	wake := make(chan struct{}, 1)
	go func() {
		for range wake {
			p.Send(core.ScreensMsg{})
		}
	}()
	model.SetScreens(core.NewScreenClient(tuiEnd, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}))
	return nil
}
