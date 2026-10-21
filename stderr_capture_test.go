package main

import (
	"fmt"
	"os"
	"testing"
)

// setStderr makes f os.Stderr and returns the file it replaces. It holds the
// notice board's lock, which a notice holds while it writes to os.Stderr, so
// the swap is ordered with a notice from a goroutine that an earlier test left
// behind, such as a web viewer saying that it disconnected. Every test helper
// that captures stderr swaps it through here.
func setStderr(f *os.File) (old *os.File) {
	notices.mu.Lock()
	defer notices.mu.Unlock()
	old, os.Stderr = os.Stderr, f
	return old
}

// TestStderrCapturesAreOrderedWithNotices: a notice can come from a goroutine
// that an earlier test left running while a test captures stderr: a web
// viewer that is closing says that it disconnected. Each capture swaps
// os.Stderr in a way that is ordered with the notice's look at it. It needs
// -race, which reports a swap that is not.
func TestStderrCapturesAreOrderedWithNotices(t *testing.T) {
	a := &app{events: make(chan inputEvent, 1)}
	for _, tc := range []struct {
		name    string
		capture func(t *testing.T)
	}{
		{"captureStderr", func(t *testing.T) { captureStderr(t, func() {}) }},
		{"stderrOf", func(t *testing.T) { stderrOf(t, func() {}) }},
		{"captureTerminal", func(t *testing.T) { captureTerminal(t, func() {}) }},
		{"terminalUntil", func(t *testing.T) { terminalUntil(t, "done", func() { fmt.Print("done") }) }},
		{"stderrUntil", func(t *testing.T) {
			stderrUntil(t, a, func() error {
				fmt.Fprintln(os.Stderr, "running")
				<-a.events
				return nil
			}, "running")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posted := make(chan struct{})
			go func() {
				defer close(posted)
				for range 50 {
					notice("") // writes nothing, but looks up os.Stderr
				}
			}()
			tc.capture(t)
			<-posted
		})
	}
}
