package main

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestCanvasPoolSharedWithTheRunLoop: the video goroutine takes each frame's
// canvas from the stream's pool, scales the picture into it and gives back the
// canvases its sink evicts from the display mailbox, while the run loop gives
// back the canvas of each frame it took: the terminal UI's once it has drawn
// it, --no-tui's at once. A canvas has one user at a time: under -race this
// fails on a pool without a lock, and on a canvas given back while it is still
// in use. And the canvases must still go round, not be made anew for each
// frame.
func TestCanvasPoolSharedWithTheRunLoop(t *testing.T) {
	// runHeadless takes these signals for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	old := statisticsEvery
	statisticsEvery = time.Hour // no statistics line in the test's output
	t.Cleanup(func() { statisticsEvery = old })

	const frames, cw, ch = 2000, 2, 6
	for _, tc := range []struct {
		name  string
		draws bool // the run loop draws each frame it takes
		run   func(a *app) error
	}{
		{"terminal", true, func(a *app) error { return a.loop(nil, nil) }},
		{"--no-tui", false, (*app).runHeadless},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := streamApp(&session{})
			a.frames = make(chan *videoFrame, 1)
			a.stream.setSink(a)
			restore := func() {}
			if tc.draws {
				withTermSize(t, cw, ch/2+1) // two canvas rows to a cell, and the status row
				a.tui = &tui{running: true}
				a.tui.resize()
				// The terminal UI draws on os.Stdout, which goes nowhere until
				// the run loop has returned.
				null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				stdout := os.Stdout
				os.Stdout = null
				restore = func() {
					os.Stdout = stdout
					null.Close()
				}
			}
			ran := make(chan error, 1)
			go func() { ran <- tc.run(a) }()

			// runVideo's pool calls, in its order, writing each canvas it
			// takes as its scaler does. The first half of the frames come
			// faster than the run loop takes them, so the sink evicts; each of
			// the rest waits until the run loop has taken the one before,
			// which it gives back while this one is taken. A geometry change
			// now and then empties the pool.
			canvases := make(chan int, 1)
			go func() {
				seen := map[*byte]bool{}
				deadline := time.Now().Add(10 * time.Second)
				for i := range frames {
					for i >= frames/2 && len(a.frames) > 0 && time.Now().Before(deadline) {
						runtime.Gosched()
					}
					if i%100 == 99 {
						a.stream.resetPool()
					}
					buf := a.stream.takePooled(cw * ch * 4)
					for j := range buf {
						buf[j] = byte(i)
					}
					seen[&buf[0]] = true
					a.stream.deliverFrame(&videoFrame{rgb: buf, w: cw, h: ch, cw: cw, ch: ch})
				}
				canvases <- len(seen)
			}()
			var made int
			select {
			case made = <-canvases:
			case <-time.After(10 * time.Second):
				t.Fatal("the video goroutine's frames did not all get a canvas")
			}

			deadline := time.Now().Add(5 * time.Second)
			for len(a.frames) > 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond) // taken, so given back before the quit below
			}
			a.events <- inputEvent{kind: evQuit}
			select {
			case err := <-ran:
				restore()
				if err != nil {
					t.Fatalf("the run loop ended with %v", err)
				}
			case <-time.After(5 * time.Second):
				// A run loop that has not returned may still draw: os.Stdout
				// stays as it is.
				t.Fatal("the run loop did not quit")
			}
			// Each frame of the second half reaches the run loop.
			if tc.draws && a.tui.frameIdx < frames/2 {
				t.Errorf("the run loop drew %d of %d frames, want at least %d", a.tui.frameIdx, frames, frames/2)
			}
			// A few per geometry (one in the mailbox, one drawn, one scaled),
			// far fewer than one per frame.
			if made > frames/10 {
				t.Errorf("%d frames took %d canvases: the pool does not hand them round", frames, made)
			}
		})
	}
}

// TestCanvasPoolAllocatesNothingInSteadyState: once the pool holds two
// canvases, taking them for the frame in the mailbox and the next one being
// scaled, and giving both back, allocates nothing.
func TestCanvasPoolAllocatesNothingInSteadyState(t *testing.T) {
	st := &streamState{}
	const size = 80 * 78 * 4
	st.returnPooled(make([]byte, size))
	st.returnPooled(make([]byte, size))
	allocs := testing.AllocsPerRun(100, func() {
		shown, next := st.takePooled(size), st.takePooled(size)
		st.returnPooled(shown)
		st.returnPooled(next)
	})
	if allocs != 0 {
		t.Errorf("a frame's two canvases cost %v allocations, want 0", allocs)
	}
}
