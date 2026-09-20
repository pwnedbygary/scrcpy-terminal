package main

// Regression tests for the TUI/headless frame path.
//
// Two things went wrong here once, and both are user-visible:
//
//  1. The web work replaced the stream's `deliver` channel with a frameSink
//     interface, but only runWeb ever installed a sink. In plain TUI mode
//     s.sink stayed nil, deliverFrame recycled each frame, and the TUI entered
//     the alternate screen and never painted a single pixel (measured: 713
//     bytes of output, zero truecolor runs, versus ~41 KB and ~380 runs for the
//     same run before the refactor).
//
//  2. The TUI's sink drew inline (`a.tui.draw`) on the video DEMUX goroutine.
//     tui.draw ends in os.Stdout.Write, so a slow terminal stalled the h264
//     decoder and backed the device stream up. The original design deliberately
//     avoided this: a one-slot, drop-old channel drained by the run loop.
//
// These tests pin both properties.

import (
	"testing"
	"time"
)

// TestDisplaySinkIsWiredForEveryMode: every run mode must have a frame sink
// before runVideo starts, or the display silently renders nothing.
func TestDisplaySinkIsWiredForEveryMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     config
		wantWeb bool
	}{
		{"tui", config{}, false},
		{"headless (--no-tui)", config{noTUI: true}, false},
		{"web/window", config{web: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(&session{}, tc.cfg)
			if a.stream == nil {
				t.Fatal("no stream")
			}
			if a.stream.sink == nil {
				t.Fatal("stream has no frame sink: the display would stay black")
			}
			_, isWeb := a.stream.sink.(*webServer)
			if isWeb != tc.wantWeb {
				t.Fatalf("sink is web=%v, want web=%v", isWeb, tc.wantWeb)
			}
			if tc.wantWeb && a.web == nil {
				t.Fatal("web mode has no web server")
			}
			if !tc.wantWeb && a.frames == nil {
				t.Fatal("no display mailbox for the run loop to drain")
			}
		})
	}
}

// TestAppFrameSinkNeverBlocks is the invariant that protects the decoder: the
// sink runs on the demux goroutine, so a display that never consumes frames
// must cost nothing but dropped frames.
func TestAppFrameSinkNeverBlocks(t *testing.T) {
	a := &app{
		stream: &streamState{},
		frames: make(chan *videoFrame, 1),
	}

	// Nobody ever drains a.frames, exactly like a terminal that stopped reading.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			a.frame(&videoFrame{rgb: make([]byte, 2*2*4), w: 2, h: 2, cw: 2, ch: 2})
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("app.frame blocked with a stalled display: a slow terminal would stall the decoder")
	}

	// Drop-old: one slot, and the newest frame wins.
	if n := len(a.frames); n != 1 {
		t.Fatalf("mailbox holds %d frames, want exactly 1 (drop-old)", n)
	}
	got := <-a.frames
	if got == nil || got.cw != 2 {
		t.Fatalf("mailbox holds %+v, want the last frame offered", got)
	}
}

// TestDroppedFrameBufferIsRecycled: a frame evicted from the mailbox must have
// its pooled buffer returned, otherwise the pool starves and every frame
// allocates a fresh canvas.
func TestDroppedFrameBufferIsRecycled(t *testing.T) {
	st := &streamState{}
	a := &app{stream: st, frames: make(chan *videoFrame, 1)}

	// Fill the single slot from the pool, then offer more frames: each offer
	// must evict and recycle the previous buffer.
	first := st.takePooled(4096)
	a.frame(&videoFrame{rgb: first, cw: 32, ch: 32})
	for i := 0; i < 8; i++ {
		a.frame(&videoFrame{rgb: st.takePooled(4096), cw: 32, ch: 32})
	}

	// The pool has two slots. If evicted buffers were leaked, takePooled would
	// be allocating fresh backing arrays every time; with recycling, the slots
	// stay in rotation, so the two we can take are the ones we already had.
	if n := len(a.frames); n != 1 {
		t.Fatalf("mailbox holds %d frames, want 1", n)
	}
	drained := <-a.frames
	st.returnPooled(drained.rgb)

	for i := 0; i < 2; i++ {
		buf := st.takePooled(4096)
		if buf == nil || len(buf) != 4096 {
			t.Fatalf("pool slot %d is %v, want a 4096-byte buffer", i, buf)
		}
		st.returnPooled(buf)
	}
}
