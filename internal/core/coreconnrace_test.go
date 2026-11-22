package core

import (
	"sync"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
)

// TestCoreConnReconnectWhileSending: what sends, asks or closes from a
// goroutine of its own reads the connection Reconnect replaces under the
// lock, not as it is being replaced (run with -race).
func TestCoreConnReconnectWhileSending(t *testing.T) {
	was := RunWait
	RunWait = 10 * time.Millisecond
	defer func() { RunWait = was }()
	pair := func() *attach.Conn {
		a, b, err := attach.Pair()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { a.Close(""); b.Close("") })
		return b
	}
	cc := Connect(pair(), &Core{}, func(any) {})
	conns := make([]*attach.Conn, 20)
	for i := range conns {
		conns[i] = pair()
	}
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		for _, conn := range conns {
			cc.Reconnect(conn)
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			cc.Send(StartWaiting{})
		}
	}()
	go func() {
		defer wg.Done()
		for range 5 {
			cc.Run(StartWaiting{})
		}
	}()
	go func() {
		defer wg.Done()
		for range 20 {
			cc.Close("")
		}
	}()
	wg.Wait()
}
