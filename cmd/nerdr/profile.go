package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"syscall"
)

// envProfile names a folder nerdr's long-lived processes write profiles
// to, for finding what they spend their time on: the client, the core and
// the holder each take a CPU profile from their start until SIGUSR2, and
// then write it with a heap and an allocation profile beside it, as
// <role>-<pid>.{cpu,heap,allocs}. They run on after it.
const envProfile = "NERDR_PROFILE"

// profile starts the profiles of this process, when asked for and it is
// one of the long-lived ones.
func profile() {
	dir := os.Getenv(envProfile)
	if dir == "" {
		return
	}
	role := "client"
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "core", "holder":
			role = os.Args[1]
		case "connect":
		default:
			if os.Args[1] != "--nerdr-runs-its-programs" {
				return
			}
		}
	}
	base := filepath.Join(dir, fmt.Sprintf("%s-%d", role, os.Getpid()))
	f, err := os.Create(base + ".cpu")
	if err != nil {
		return
	}
	runtime.MemProfileRate = 64 << 10
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR2)
	go func() {
		<-sig
		pprof.StopCPUProfile()
		f.Close()
		for _, name := range []string{"heap", "allocs"} {
			if out, err := os.Create(base + "." + name); err == nil {
				_ = pprof.Lookup(name).WriteTo(out, 0)
				out.Close()
			}
		}
	}()
}
