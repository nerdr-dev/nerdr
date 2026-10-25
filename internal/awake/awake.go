// Package awake keeps the machine from going to sleep, and its screen
// from going dark, while nerdr asks it to: while an agent works, say. A
// locked screen stops no agent; a machine asleep stops them all.
package awake

import "sync"

// Keeper holds the machine awake, and the screen on, as Set last asked.
// Set only records what is wanted: the holding is done by a goroutine of
// its own, so a bus slow to answer never holds up the caller.
type Keeper struct {
	mu            sync.Mutex
	sleep, screen bool // wanted
	err           string
	wake          chan struct{}
	closed        bool
}

// New starts a Keeper holding nothing.
func New() *Keeper {
	k := &Keeper{wake: make(chan struct{}, 1)}
	go k.run()
	return k
}

// Set asks for the machine to be kept from sleeping, and with screen its
// screen kept on as well.
func (k *Keeper) Set(sleep, screen bool) {
	k.mu.Lock()
	changed := k.sleep != sleep || k.screen != (sleep && screen)
	k.sleep, k.screen = sleep, sleep && screen
	k.mu.Unlock()
	if changed {
		k.poke()
	}
}

// Err is why the last hold could not be had, "" when it was.
func (k *Keeper) Err() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.err
}

// Close lets go of everything held.
func (k *Keeper) Close() {
	k.mu.Lock()
	k.closed, k.sleep, k.screen = true, false, false
	k.mu.Unlock()
	k.poke()
}

func (k *Keeper) poke() {
	select {
	case k.wake <- struct{}{}:
	default:
	}
}

func (k *Keeper) run() {
	h := &holder{}
	for range k.wake {
		k.mu.Lock()
		sleep, screen, closed := k.sleep, k.screen, k.closed
		k.mu.Unlock()
		err := h.hold(sleep, screen)
		k.mu.Lock()
		k.err = ""
		if err != nil {
			k.err = err.Error()
		}
		k.mu.Unlock()
		if closed {
			return
		}
	}
}
