package main

import (
	"strings"
	"testing"
	"time"
)

// TestStatusShowsTheStreamsFrameRate: the status row's frame rate is the
// stream's, as the frames the run loop draws carry it (videoFrame.fps), the
// rate --no-tui prints and the browser display's status messages carry: not
// the rate the run loop drew them at. On a still screen the device sends a
// frame and up to ten repeats 100 ms apart, then nothing until something
// changes; the mean of the run loop's last 32 frame times counted those
// pauses, and on the first real-device run it read 5.1 fps where the stream
// gave 10. Until the stream has measured a rate (its first half second), the
// row shows none.
func TestStatusShowsTheStreamsFrameRate(t *testing.T) {
	withTermSize(t, 40, 10)
	drawing := func() *app {
		a := streamApp(&session{deviceName: "Pixel 8"})
		a.tui = &tui{running: true, repaintInterval: 300}
		a.tui.resize() // 40x9 cells: a 40x18 canvas
		a.frames = make(chan *videoFrame, 1)
		return a
	}
	frame := func(fps float64) *videoFrame {
		return &videoFrame{rgb: make([]byte, 40*18*4), w: 1080, h: 2400, cw: 40, ch: 18, fps: fps}
	}
	// stats has the run loop of a draw frames, one at a time, and returns the
	// picture's size and rate as the status row then shows them.
	stats := func(a *app, frames ...*videoFrame) string {
		t.Helper()
		done := make(chan error, 1)
		captureStdout(t, func() {
			go func() { done <- a.loop(nil, nil) }()
			for _, f := range frames {
				a.frames <- f
				deadline := time.Now().Add(5 * time.Second)
				for len(a.frames) > 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond) // taken, so drawn before the quit below
				}
			}
			a.events <- inputEvent{kind: evQuit}
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("the run loop ended with %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("the run loop did not quit")
			}
		})
		for _, p := range a.statusParts() {
			if strings.HasPrefix(p.text, "1080x2400") {
				return p.text
			}
		}
		return ""
	}

	// A still screen as the run loop drew it: three changes, each with ten
	// repeats 100 ms apart, and a second between them. The mean of those 32
	// frame times is 156.2 ms, 6.4 fps.
	still := drawing()
	for change := 0; change < 3; change++ {
		if change > 0 {
			still.recordFrameTime(1000)
		}
		for i := 0; i < 10; i++ {
			still.recordFrameTime(100)
		}
	}
	if got, want := stats(still, frame(10)), "1080x2400 100.0ms 10.0fps"; got != want {
		t.Errorf("a still screen whose stream gives 10 fps: %q, want %q", got, want)
	}

	// Frames that reach the run loop back to back, from a stream of 10 fps.
	if got, want := stats(drawing(), frame(10), frame(10), frame(10)), "1080x2400 100.0ms 10.0fps"; got != want {
		t.Errorf("frames back to back from a stream of 10 fps: %q, want %q", got, want)
	}

	// The stream's first half second: no rate yet.
	if got, want := stats(drawing(), frame(0), frame(0)), "1080x2400"; got != want {
		t.Errorf("before the stream has a rate: %q, want %q", got, want)
	}
}
