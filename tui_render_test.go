package main

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// newRenderTUI is a running tui holding a blank canvas snapshot, as after a
// frame, with the periodic full repaint far away.
func newRenderTUI(cols, rows int) *tui {
	tr := &tui{cols: cols, rows: rows, running: true, lastW: cols, lastH: rows * 2, repaintInterval: 1000}
	tr.keys = make([]uint64, cols*rows)
	tr.prev = make([]uint64, cols*rows)
	for i := range tr.prev {
		tr.prev[i] = ^uint64(0)
	}
	tr.lastRGB = make([]byte, cols*rows*2*4)
	return tr
}

// TestStatusPaintsWithoutAFrame: a changed status (grab, volume, fps) reaches
// its row at once, because a static device screen sends no frame to carry it,
// and it costs that row only, not a repaint of the whole screen.
func TestStatusPaintsWithoutAFrame(t *testing.T) {
	tr := &tui{cols: 40, rows: 10, running: true}
	out := captureStdout(t, func() { tr.setStatus("GRAB · Alt+Q quit") })
	if !strings.Contains(out, "\x1b[11;1H") || !strings.Contains(out, "GRAB · Alt+Q quit") {
		t.Fatalf("status not painted on row 11: %q", out)
	}
	if tr.dirty {
		t.Error("a status change forced a full repaint")
	}
	if out := captureStdout(t, func() { tr.setStatus("GRAB · Alt+Q quit") }); out != "" {
		t.Errorf("an unchanged status was painted again: %q", out)
	}
}

// TestShorterStatusClearsTheRow: a shorter status erases what the longer one
// left behind, except after a full row, where the erase would take the last
// cell with it.
func TestShorterStatusClearsTheRow(t *testing.T) {
	tr := &tui{cols: 40, rows: 10, running: true}
	captureStdout(t, func() { tr.setStatus("a much longer status line than the next") })
	if out := captureStdout(t, func() { tr.setStatus("short") }); !strings.Contains(out, "short\x1b[K") {
		t.Fatalf("a shorter status does not erase to the end of its row: %q", out)
	}
	if out := captureStdout(t, func() { tr.setStatus(strings.Repeat("x", 40)) }); strings.Contains(out, "\x1b[K") {
		t.Errorf("a full-width status erases after its last cell: %q", out)
	}
}

// TestFrameIsOneSynchronizedUpdate: a frame is wrapped in synchronized output,
// and a frame that changes nothing writes nothing, status included.
func TestFrameIsOneSynchronizedUpdate(t *testing.T) {
	tr := newRenderTUI(20, 4)
	tr.status = "status"
	out := captureStdout(t, tr.repaint)
	if !strings.HasPrefix(out, "\x1b[?2026h") || !strings.HasSuffix(out, "\x1b[?2026l") {
		t.Fatalf("frame is not wrapped in synchronized output: %q", out)
	}
	if !strings.Contains(out, "status") {
		t.Errorf("first frame did not paint the status row: %q", out)
	}
	if out := captureStdout(t, tr.repaint); out != "" {
		t.Errorf("an unchanged frame wrote %q", out)
	}
}

// TestCellColoursUseTheFullRange: the packer keeps three bits per channel, and
// each level must come out as level*255/7, rounded, so white is 255 and black
// is 0 (writing the masked value capped white at 224).
func TestCellColoursUseTheFullRange(t *testing.T) {
	for lvl := uint32(0); lvl < 8; lvl++ {
		v := lvl << 5 // the channel value the packer produces for this level
		w := strconv.Itoa(int(math.Round(float64(lvl) * 255 / 7)))
		if got, want := rgbStr(v<<16|v<<8|v), w+";"+w+";"+w; got != want {
			t.Errorf("level %d: rgbStr = %q, want %q", lvl, got, want)
		}
	}
	for c, want := range map[uint32]string{0x00E00000: "255;0;0", 0x0000E000: "0;255;0", 0x000000E0: "0;0;255"} {
		if got := rgbStr(c); got != want {
			t.Errorf("rgbStr(%#08x) = %q, want %q", c, got, want)
		}
	}
}

// TestOverlayPaddingCountsCells: overlay rows are padded to the full width in
// display cells. '‹' and '›' take three bytes and '·' two, so padding by bytes
// left the row short, with old cells showing at its end.
func TestOverlayPaddingCountsCells(t *testing.T) {
	const text = "‹ Home · Back ›"
	tr := newRenderTUI(20, 4)
	tr.setOverlay([]overlayLine{{text: text}})
	out := captureStdout(t, tr.repaint)
	i := strings.Index(out, text)
	if i < 0 {
		t.Fatalf("overlay row not painted: %q", out)
	}
	rest := out[i+len(text):]
	pad := len(rest) - len(strings.TrimLeft(rest, " "))
	if want := 20 - utf8.RuneCountInString(text); pad != want {
		t.Errorf("row padded with %d spaces, want %d", pad, want)
	}
}

// stripedCanvas is a w by h canvas of white and black pixel rows, so every
// cell drawn from it has a different top and bottom: a half block, ▀. (An
// all-black canvas draws spaces, which no check for ▀ can catch.)
func stripedCanvas(w, h int) []byte {
	rgb := make([]byte, w*h*4)
	for y := 0; y < h; y += 2 {
		for i := y * w * 4; i < (y+1)*w*4; i++ {
			rgb[i] = 0xff
		}
	}
	return rgb
}

// TestOverlayPaintsWithoutACanvas: with no picture yet (or video off), and
// with a snapshot from another geometry, the overlay and the status still
// paint on a cleared screen, no video cell is drawn, and the next frame
// repaints every row.
func TestOverlayPaintsWithoutACanvas(t *testing.T) {
	probe := newRenderTUI(16, 5)
	if out := captureStdout(t, func() { probe.draw(stripedCanvas(16, 10)) }); !strings.Contains(out, "▀") {
		t.Fatalf("test setup: the striped snapshot draws no half blocks: %q", out)
	}
	for _, stale := range []bool{false, true} {
		tr := &tui{cols: 20, rows: 4, running: true}
		tr.keys = make([]uint64, 20*4)
		tr.prev = make([]uint64, 20*4)
		if stale {
			tr.lastRGB = stripedCanvas(16, 10) // same cell count, other shape
			tr.lastW, tr.lastH = 16, 10
		}
		tr.status = "waiting for video"
		tr.setOverlay([]overlayLine{{text: "Controls"}})
		tr.dirty = false
		out := captureStdout(t, tr.repaint)
		for _, want := range []string{"\x1b[2J", "Controls", "waiting for video"} {
			if !strings.Contains(out, want) {
				t.Errorf("stale=%v: %q missing from %q", stale, want, out)
			}
		}
		if strings.Contains(out, "▀") {
			t.Errorf("stale=%v: video cells drawn without a canvas: %q", stale, out)
		}
		if !tr.dirty {
			t.Errorf("stale=%v: the next frame would not repaint the cleared rows", stale)
		}
	}
}

// TestStatusRowIsPaintedAgainAfterAClear: with no frame to carry it, the
// status row is painted again, same text and all, after a resize (the row
// moved with the bottom edge) and after shellInit cleared the screen.
func TestStatusRowIsPaintedAgainAfterAClear(t *testing.T) {
	withTermSize(t, 40, 10)
	tr := &tui{running: true}
	tr.resize()
	captureStdout(t, func() { tr.setStatus("status") })

	termSizeOverride = struct{ cols, rows int }{40, 12} // taller only: the same text
	tr.resize()
	if out := captureStdout(t, func() { tr.setStatus("status") }); !strings.Contains(out, "\x1b[12;1H") || !strings.Contains(out, "status") {
		t.Errorf("after a resize the status is not painted on the new bottom row (12): %q", out)
	}

	captureStdout(t, tr.shellClose)
	captureStdout(t, tr.shellInit)
	if out := captureStdout(t, func() { tr.setStatus("status") }); !strings.Contains(out, "\x1b[12;1H") || !strings.Contains(out, "status") {
		t.Errorf("after shellInit cleared the screen the status is not painted: %q", out)
	}
}

// TestFullRepaintPaintsTheStatus: a forced full repaint, after a clear or the
// periodic one that recovers from a desync, paints the status row again even
// though its text did not change. A frame that is not forced and changes
// nothing writes nothing, and with no status the row is left alone.
func TestFullRepaintPaintsTheStatus(t *testing.T) {
	tr := newRenderTUI(20, 4)
	tr.status = "status"
	captureStdout(t, tr.repaint) // the first frame paints the status

	tr.markDirty()
	if out := captureStdout(t, tr.repaint); !strings.Contains(out, "\x1b[5;1H") || !strings.Contains(out, "status") {
		t.Errorf("a full repaint after a clear left the status row out: %q", out)
	}

	tr.frameIdx, tr.repaintInterval = 0, 2
	if out := captureStdout(t, tr.repaint); out != "" {
		t.Errorf("an unforced frame with nothing changed wrote %q", out)
	}
	if out := captureStdout(t, tr.repaint); !strings.Contains(out, "\x1b[5;1H") || !strings.Contains(out, "status") {
		t.Errorf("the periodic full repaint left the status row out: %q", out)
	}

	blank := newRenderTUI(20, 4)
	blank.markDirty()
	if out := captureStdout(t, blank.repaint); strings.Contains(out, "\x1b[5;1H") {
		t.Errorf("a full repaint with no status painted the status row: %q", out)
	}
}

// TestNoAutowrapWhileTheUIOwnsTheScreen: autowrap is off from shellInit to
// shellClose, so a character whose cells are miscounted (an emoji with a
// variation selector, say) is clipped at the edge of its row instead of
// wrapping, which on the bottom row would scroll the picture. Giving the
// terminal back turns it on again, also after a panic on another goroutine.
func TestNoAutowrapWhileTheUIOwnsTheScreen(t *testing.T) {
	tr := &tui{cols: 20, rows: 4}
	if out := captureStdout(t, tr.shellInit); !strings.Contains(out, "\x1b[?7l") {
		t.Errorf("shellInit leaves autowrap on: %q", out)
	}
	if out := captureStdout(t, tr.shellClose); !strings.Contains(out, "\x1b[?7h") {
		t.Errorf("shellClose leaves autowrap off: %q", out)
	}

	a := newTestApp(nil)
	a.tui = &tui{cols: 20, rows: 4, running: true}
	a.restoreTerm = a.terminalRestorer(nil)
	out := captureTerminal(t, func() {
		defer func() { _ = recover() }()
		defer a.restoreTerminalOnPanic()
		panic("boom")
	})
	if !strings.Contains(out, "\x1b[?7h") {
		t.Errorf("the restore after a panic leaves autowrap off: %q", out)
	}
}

// TestFrameEndsWithTheColoursReset: a frame ends with an SGR reset before the
// end of its synchronized update, also one that changes only video rows, so
// no colour of the picture is left set on the terminal.
func TestFrameEndsWithTheColoursReset(t *testing.T) {
	const end = "\x1b[0m\x1b[?2026l"
	tr := newRenderTUI(4, 2)
	canvas := make([]byte, 4*2*2*4)
	if out := captureStdout(t, func() { tr.draw(canvas) }); !strings.HasSuffix(out, end) {
		t.Errorf("the first frame does not end with the reset: %q", out)
	}
	for i := range canvas {
		canvas[i] = 0xff
	}
	out := captureStdout(t, func() { tr.draw(canvas) })
	if !strings.Contains(out, "\x1b[1;1H") {
		t.Fatalf("the changed picture was not drawn: %q", out)
	}
	if !strings.HasSuffix(out, end) {
		t.Errorf("a frame that changes only video rows does not end with the reset: %q", out)
	}

	blank := &tui{cols: 20, rows: 4, running: true, status: "status"}
	if out := captureStdout(t, blank.repaint); !strings.HasSuffix(out, end) {
		t.Errorf("a repaint with no picture does not end with the reset: %q", out)
	}
}

// TestRestoreEndsAnUnfinishedFrame: a frame cut off part way (a panic while it
// was being written) leaves the terminal holding back what follows, so giving
// the terminal back ends the synchronized update before it leaves the
// alternate screen.
func TestRestoreEndsAnUnfinishedFrame(t *testing.T) {
	out := captureStdout(t, (&tui{cols: 20, rows: 4, running: true}).shellClose)
	end, leave := strings.Index(out, "\x1b[?2026l"), strings.Index(out, "\x1b[?1049l")
	if end < 0 || leave < 0 || end > leave {
		t.Errorf("the restore does not end the synchronized update first: %q", out)
	}
}

// TestRestoreWaitsForTheFrameBeingWritten: giving the terminal back takes the
// painter's lock, so a frame being written ends before the restore starts. A
// holder that never lets go without writing (gone in a panic while it held
// the lock) holds the restore up for about 100 ms, not for ever.
func TestRestoreWaitsForTheFrameBeingWritten(t *testing.T) {
	tr := &tui{cols: 8, rows: 2, running: true}
	tr.mu.Lock()
	out := captureTerminal(t, func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			tr.shellClose()
		}()
		time.Sleep(15 * time.Millisecond)
		os.Stdout.WriteString("<frame>")
		tr.mu.Unlock()
		<-done
	})
	if frame, leave := strings.Index(out, "<frame>"), strings.Index(out, "\x1b[?1049l"); frame < 0 || leave < frame {
		t.Errorf("the restore did not wait for the frame being written: %q", out)
	}

	stuck := &tui{cols: 8, rows: 2, running: true}
	stuck.mu.Lock() // a painter that does not let go while the restore waits
	defer stuck.mu.Unlock()
	out = captureTerminal(t, func() {
		done := make(chan struct{})
		start := time.Now()
		go func() {
			defer close(done)
			stuck.shellClose()
		}()
		select {
		case <-done:
			if took := time.Since(start); took < 90*time.Millisecond || took > 300*time.Millisecond {
				t.Errorf("behind a painter that keeps its lock, the restore took %v, want about 100 ms", took)
			}
		case <-time.After(2 * time.Second):
			t.Error("a painter holding its lock kept the terminal from being given back")
		}
	})
	if !strings.Contains(out, "\x1b[?1049l") {
		t.Errorf("behind a stuck painter the terminal was not given back: %q", out)
	}
}

// TestRestoreTakesAFreeLockAtOnce: with nobody painting, giving the terminal
// back takes the painter's lock at once, rather than missing it and waiting
// out the bound (about 100 ms) meant for a stuck painter.
func TestRestoreTakesAFreeLockAtOnce(t *testing.T) {
	for i := 0; i < 200; i++ {
		tr := &tui{cols: 8, rows: 2, running: true}
		start := time.Now()
		captureStdout(t, tr.shellClose)
		if took := time.Since(start); took >= 90*time.Millisecond {
			t.Fatalf("try %d: with the lock free the restore took %v", i, took)
		}
	}
}

// TestRestoreLetsGoOfALateLock: when the painter lets go after the restore
// has stopped waiting, the lock the restore asked for is let go again at
// once, so the next draw does not hang on it.
func TestRestoreLetsGoOfALateLock(t *testing.T) {
	for i := 0; i < 20; i++ {
		tr := &tui{cols: 8, rows: 2, running: true}
		tr.mu.Lock()
		if tr.lockWithin(time.Millisecond) {
			t.Fatalf("try %d: took a lock that was held", i)
		}
		tr.mu.Unlock()
		// TryLock straight away would take the mutex ahead of the waiting
		// helper, which then takes it later unseen.
		time.Sleep(20 * time.Millisecond)
		deadline := time.Now().Add(time.Second)
		for !tr.mu.TryLock() {
			if time.Now().After(deadline) {
				t.Fatalf("try %d: the lock asked for after the wait was given up is still held", i)
			}
			time.Sleep(time.Millisecond)
		}
		tr.mu.Unlock()
	}
}

// TestRestoreWhileThePainterRuns: a panic on another goroutine gives the
// terminal back while the run loop draws. The restore does not race the
// painter (run with -race), and no frame follows it.
func TestRestoreWhileThePainterRuns(t *testing.T) {
	tr := newRenderTUI(8, 2)
	tr.repaintInterval = 1 // every frame writes the whole picture
	canvas := stripedCanvas(8, 4)
	stop := make(chan struct{})
	out := captureTerminal(t, func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
				}
				tr.draw(canvas)
			}
		}()
		time.Sleep(10 * time.Millisecond)
		tr.shellClose()
		time.Sleep(10 * time.Millisecond)
		close(stop)
		<-done
	})
	leave := strings.LastIndex(out, "\x1b[?1049l")
	if leave < 0 {
		t.Fatalf("the terminal was not given back: %d bytes written", len(out))
	}
	if after := out[leave:]; strings.Contains(after, syncBegin) {
		t.Errorf("a frame was written after the terminal was given back: %q", after[:min(len(after), 200)])
	}
}

// TestRestoreWhileAScreenshotIsTaken: the run loop also asks whether the
// terminal UI runs when it takes a screenshot (Alt+S). A restore from another
// goroutine meanwhile does not race it (run with -race).
func TestRestoreWhileAScreenshotIsTaken(t *testing.T) {
	a := newTestApp(nil)
	a.tui = &tui{cols: 8, rows: 2, running: true} // no frame: each try says so
	stop := make(chan struct{})
	out := captureTerminal(t, func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
				}
				a.screenshot()
				time.Sleep(100 * time.Microsecond)
			}
		}()
		time.Sleep(10 * time.Millisecond)
		a.tui.shellClose()
		time.Sleep(10 * time.Millisecond)
		close(stop)
		<-done
	})
	leave := strings.LastIndex(out, "\x1b[?1049l")
	if leave < 0 || !strings.Contains(out[:leave], "screenshot: no frame yet") {
		t.Errorf("test setup: no screenshot was tried before the restore: %q", out[:min(len(out), 200)])
	}
}

// TestRepaintPanicLeavesTheLockFree: handleInput recovers from a panic in what
// a key does, and the run loop goes on. A panic while repaint paints with the
// UI lock held must not leave the lock held, or the next frame or status row
// would wait for it forever. (Here the write panics: a zero os.File has no
// descriptor behind it.)
func TestRepaintPanicLeavesTheLockFree(t *testing.T) {
	tr := &tui{cols: 20, rows: 4, running: true, status: "status"} // no frame yet: it paints without one
	old := os.Stdout
	os.Stdout = new(os.File)
	var r any
	func() {
		defer func() {
			os.Stdout = old
			r = recover()
		}()
		tr.repaint()
	}()
	if r == nil {
		t.Fatal("test setup: repaint did not panic")
	}
	if !tr.mu.TryLock() {
		t.Fatal("after a panic in repaint the UI lock is still held")
	}
	tr.mu.Unlock()
}
