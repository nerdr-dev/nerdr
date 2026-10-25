package awake

import (
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
)

// holder holds, on Linux, a logind inhibitor against sleep — kept as a
// file descriptor logind lets go of when it is closed, and when nerdr
// dies — and the desktop's screensaver inhibition, held by the session
// bus connection, which the desktop lets go of when it is closed.
type holder struct {
	inhibit *os.File
	bus     *dbus.Conn
	cookie  uint32
}

func (h *holder) hold(sleep, screen bool) error {
	var errs []error
	if sleep && h.inhibit == nil {
		f, err := inhibitSleep()
		if err != nil {
			errs = append(errs, fmt.Errorf("keeping the machine awake: %w", err))
		}
		h.inhibit = f
	}
	if !sleep && h.inhibit != nil {
		h.inhibit.Close()
		h.inhibit = nil
	}
	if screen && h.bus == nil {
		bus, cookie, err := inhibitScreen()
		if err != nil {
			errs = append(errs, fmt.Errorf("keeping the screen on: %w", err))
		}
		h.bus, h.cookie = bus, cookie
	}
	if !screen && h.bus != nil {
		h.bus.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver").
			Call("org.freedesktop.ScreenSaver.UnInhibit", 0, h.cookie)
		h.bus.Close()
		h.bus = nil
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// inhibitSleep asks logind to block sleep, and the suspend it does when
// the machine has been idle, until the file it gives is closed.
func inhibitSleep() (*os.File, error) {
	bus, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	defer bus.Close()
	var fd dbus.UnixFD
	err = bus.Object("org.freedesktop.login1", "/org/freedesktop/login1").
		Call("org.freedesktop.login1.Manager.Inhibit", 0, "sleep:idle", "nerdr", "an agent is working", "block").
		Store(&fd)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "logind-inhibit"), nil
}

// inhibitScreen asks the desktop not to blank or lock the screen for being
// idle, for as long as the connection it is asked on is open.
func inhibitScreen() (*dbus.Conn, uint32, error) {
	bus, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, 0, err
	}
	var cookie uint32
	err = bus.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver").
		Call("org.freedesktop.ScreenSaver.Inhibit", 0, "nerdr", "an agent is working").
		Store(&cookie)
	if err != nil {
		bus.Close()
		return nil, 0, err
	}
	return bus, cookie, nil
}
