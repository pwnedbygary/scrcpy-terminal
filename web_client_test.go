package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The browser client is as much a part of this program as the Go code, and it
// has already shipped two faults that no Go test could see: a worklet that
// threw on every audio burst (so the page was silent while claiming to be
// connected) and a hello that was sent before the socket opened (so the server
// never turned audio on for it). web/*.mjs drives the real player.js and the
// real pcm-worklet.js with the DOM and AudioWorklet stubbed, so running them
// from `go test ./...` is what puts them in front of CI.
func TestWebClientSuites(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed: skipping the browser-client suites")
	}
	for _, suite := range []string{
		"web/input_test.mjs",   // pointer/keyboard mapping, terminal key parity
		"web/worklet_test.mjs", // the PCM sink itself: buffering, sizes, stats
		"web/pointer_test.mjs", // the visible pointer: ghost, drag, touch ripples
	} {
		suite := suite
		t.Run(filepath.Base(suite), func(t *testing.T) {
			cmd := exec.Command(node, suite)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", suite, err, out)
			}
			t.Logf("%s\n%s", suite, out)
		})
	}
}
