package ops

import (
	"time"

	"github.com/phansen314/ftask/internal/store"
)

// Env is an invocation's environment, passed in, never global
// (implementation-spec.md, Environment): the filesystem and the home and
// config directories store needs, and the clock.
type Env struct {
	store.Env
	Clock Clock
}

// Clock returns the current time. Operations stamp files with it, so tests
// use a fixed one and compare written files byte for byte.
type Clock func() time.Time

// RealClock is the system time in UTC, truncated to whole seconds
// (design-spec.md, Timestamps).
func RealClock() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}
