package fuzz

import (
	"testing"
	"time"
)

// An input that does not return is stopped while it runs: the watchdog fires while
// the work is blocked, and the work is released by it (no sleeping: the work waits
// for the watchdog).
func TestGuardStopsAnInputThatDoesNotReturn(t *testing.T) {
	t.Parallel()
	fired := make(chan struct{})
	finished := false
	guard(10*time.Millisecond, func() { close(fired) }, func() {
		<-fired // blocked until the watchdog stops it
		finished = true
	})
	if !finished {
		t.Fatal("the work did not return")
	}
}

// An input that returns in time is left alone, and the watchdog is stopped.
func TestGuardLeavesAnInputThatReturns(t *testing.T) {
	t.Parallel()
	guard(time.Hour, func() { t.Error("the watchdog fired for an input that returned") }, func() {})
}
