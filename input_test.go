package main

import (
	"encoding/binary"
	"testing"
)

// ---------------------------------------------------------------------------
// Terminal input: where a drag whose release was lost is lifted, and CR LF
// from one Enter.
// ---------------------------------------------------------------------------

// touchAt decodes a captured touch message: its action (Android's 0 down,
// 1 up, 2 move) and the position it carries.
func touchAt(t *testing.T, w []byte) (action byte, x, y int32) {
	t.Helper()
	if len(w) != 32 || w[0] != ctrlInjectTouch {
		t.Fatalf("not a touch message: % x", w)
	}
	return w[1], int32(binary.BigEndian.Uint32(w[10:14])), int32(binary.BigEndian.Uint32(w[14:18]))
}

// TestLostReleaseLiftsWhereTheFingerIs: a press while a drag is still down
// means that drag's release never arrived. Its finger is lifted where the
// device last saw it (the press, then each move sent), not at the new press,
// which would drag it across the screen on the way up; then the new press
// goes down as before.
func TestLostReleaseLiftsWhereTheFingerIs(t *testing.T) {
	withTermSize(t, 80, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.stream = &streamState{videoW: 1080, videoH: 1920}
	a.stream.updateGeometry()

	a.handleInput(mouseSeq(0, 30, 5, true))   // down
	a.handleInput(mouseSeq(32, 40, 10, true)) // drag: the first move is sent at once
	a.handleInput(mouseSeq(32, 41, 11, true)) // the next, within 8 ms, waits in the coalescer
	a.handleInput(mouseSeq(0, 10, 25, true))  // the release was lost; a press elsewhere

	n := len(cc.captured)
	if n < 4 {
		t.Fatalf("sent %d messages, want a down, a move, an up and a down", n)
	}
	_, lastX, lastY := touchAt(t, cc.captured[n-3])
	upAction, upX, upY := touchAt(t, cc.captured[n-2])
	downAction, downX, downY := touchAt(t, cc.captured[n-1])
	if upAction != 1 || downAction != 0 {
		t.Fatalf("the press sent actions %d then %d, want an up (1) then a down (0)", upAction, downAction)
	}
	if upX != lastX || upY != lastY {
		t.Errorf("the lost drag was lifted at %d,%d; the device last saw its finger at %d,%d", upX, upY, lastX, lastY)
	}
	if upX == downX && upY == downY {
		t.Errorf("the lost drag was lifted at the new press, %d,%d", downX, downY)
	}
	if !a.drag.isDown() {
		t.Error("the new press is not down")
	}

	// That press is lost too, before any move: it is lifted where it went
	// down.
	a.handleInput(mouseSeq(0, 60, 20, true))
	n = len(cc.captured)
	if _, upX, upY = touchAt(t, cc.captured[n-2]); upX != downX || upY != downY {
		t.Errorf("a lost press with no move was lifted at %d,%d, not where it went down, %d,%d", upX, upY, downX, downY)
	}
}

// feedTerminal hands b to the app as the terminal does: decoded into events
// the way inputLoop decodes what it reads, each handled as the run loop does.
func feedTerminal(a *app, b []byte) {
	for len(b) > 0 {
		ev, n := a.decodeInput(b)
		if n == 0 {
			return
		}
		b = b[n:]
		if ev != nil {
			a.handleInput(ev)
		}
	}
}

// TestCRLFIsOneEnter: a terminal that sends CR LF for Enter presses once
// wherever Enter is handled: the keyboard's focused key (one Enter on QUIT
// arms it, it does not arm and confirm it), the device (Enter, not Enter and
// Ctrl+J) and the menu (which closes, and the LF does not then reach the
// device). CR and LF on their own, and LF CR, decode as before.
func TestCRLFIsOneEnter(t *testing.T) {
	a := newTestApp(nil)
	if ev, n := a.decodeInput([]byte("\r\n")); string(ev) != "\r" || n != 2 {
		t.Errorf("CR LF decodes as %q from %d bytes, want one CR from both", ev, n)
	}
	for _, in := range []string{"\r", "\n", "\n\r", "\r\r", "\rx"} {
		if ev, n := a.decodeInput([]byte(in)); string(ev) != in[:1] || n != 1 {
			t.Errorf("%q decodes as %q from %d bytes, want %q from 1", in, ev, n, in[:1])
		}
	}

	withTermSize(t, 80, 30)
	k := newTestApp(newController(&capConn{}))
	k.tui = &tui{cols: 80, rows: 29}
	k.openKeyboard()
	k.kb.curRow = len(k.kb.rows) - 1
	for ci, c := range k.kb.rows[k.kb.curRow].cells {
		if c.key.label == "QUIT" {
			k.kb.curCol = ci
		}
	}
	feedTerminal(k, []byte("\r\n"))
	if quitQueued(k) {
		t.Error("one CR LF Enter on the focused QUIT armed and confirmed it")
	}
	if !k.kb.quitArmed(quitFromKeyboard, timeNowUnixNano()) {
		t.Error("one CR LF Enter on the focused QUIT did not arm it")
	}

	cc := &capConn{}
	d := newTestApp(newController(cc))
	feedTerminal(d, []byte("\r\n"))
	if len(cc.captured) != 2 {
		t.Errorf("CR LF sent %d messages to the device, want one Enter down and up", len(cc.captured))
	} else {
		for _, w := range cc.captured {
			if _, code, meta := keycoded(t, w); code != 66 || meta != 0 {
				t.Errorf("CR LF sent keycode %d meta %#x, want ENTER (66)", code, meta)
			}
		}
	}

	mc := &capConn{}
	m := newTestApp(newController(mc))
	m.tui = &tui{cols: 80, rows: 29}
	m.toggleMenu()
	feedTerminal(m, []byte("\r\n"))
	if m.menuOpen || len(mc.captured) != 0 {
		t.Errorf("CR LF in the menu: open=%v, sent % x to the device", m.menuOpen, mc.captured)
	}
}
