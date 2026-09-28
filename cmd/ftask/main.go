// Command ftask is the ftask CLI. main sets up the process and exits; all
// behavior is in internal/cli (implementation-spec.md, Exit and signals).
package main

import (
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/phansen314/ftask/internal/cli"
)

// startHook runs once the process is set up; test builds set it.
var startHook = func() {}

func main() {
	os.Exit(run())
}

// run is everything but the exit, so deferred cleanup always runs first.
func run() int {
	// A panic or fatal error ends by SIGABRT (exit 134, outcome unknown),
	// not Go's default exit 2, which would read as a usage error.
	debug.SetTraceback("crash")
	// Writing to a closed pipe then returns EPIPE, reported as exit 3,
	// instead of killing the process.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	startHook()
	return cli.Main(os.Args[1:])
}
