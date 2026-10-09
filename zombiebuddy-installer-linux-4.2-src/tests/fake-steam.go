//go:build ignore

// fake-steam: test helper that masquerades as a Steam client process.
//
// Compiled to a binary literally named "steam" so that /proc/<pid>/comm
// reads "steam" and the installer's process detection finds it.
//
// Modes:
//
//	(default)  dies when SIGTERM arrives   -> tests the auto-close path
//	ignore     ignores SIGTERM entirely    -> tests the proceed-anyway path
package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "ignore" {
		signal.Ignore(syscall.SIGTERM)
	}
	time.Sleep(10 * time.Minute)
}
