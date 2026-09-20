package main

import (
	"encoding/binary"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// web pointer input: what actually reaches the device
//
// The browser and the TUI share one drag coalescer (app.coalescedMove /
// app.flushPendingMove). The run loop's ~16ms tick calls flushPendingMove to
// release a rate-limited move. These tests drive a real websocket client and
// assert on the control messages that reach the device, because that is the
// only thing that matters: a stray event is a broken tap or a broken swipe on
// a real phone.
// ---------------------------------------------------------------------------

// touchParts decodes an inject-touch control message as it arrived at the device.
func touchParts(t *testing.T, data []byte) (action byte, x, y int32, sw, sh uint16) {
	t.Helper()
	if len(data) < 22 {
		t.Fatalf("short control message: % x", data)
	}
	if data[0] != ctrlInjectTouch {
		t.Fatalf("control type %d, want %d (inject touch): % x", data[0], ctrlInjectTouch, data)
	}
	return data[1],
		int32(binary.BigEndian.Uint32(data[10:14])),
		int32(binary.BigEndian.Uint32(data[14:18])),
		binary.BigEndian.Uint16(data[18:20]),
		binary.BigEndian.Uint16(data[20:22])
}

// readDev returns the next control message the server wrote to the device.
func (h *webTestHarness) readDev(t *testing.T, timeout time.Duration) []byte {
	t.Helper()
	select {
	case b := <-h.devMsgs:
		return b
	case <-time.After(timeout):
		t.Fatal("no control message reached the device")
		return nil
	}
}

// webDragHarness builds a harness with an app, a connected client that has
// received one frame, so pointer events map through a real canvas.
func webDragHarness(t *testing.T) (*webTestHarness, *testClient, webCanvas) {
	t.Helper()
	h := newWebTestHarness(t, config{web: true}, true)
	cl := dialWS(t, h.url())
	cl.send(webEvent{Op: "hello", W: 480, H: 1080})
	cl.nextJSON("hello", 3*time.Second)

	cv := computeWebCanvas(480, 1080, 480, 1080)
	h.srv.frame(&videoFrame{rgb: makeCanvas(cv.cw, cv.ch, 1), w: 480, h: 1080,
		cw: cv.cw, ch: cv.ch, fps: 60})
	cl.nextBinary(webMsgVideo, 5*time.Second)
	return h, cl, cv
}

// tick drives the run loop's coalescer flush the way evTickFast does.
func (h *webTestHarness) tick(t *testing.T) {
	t.Helper()
	time.Sleep(12 * time.Millisecond) // outrun moveCoalesceNs (8ms)
	h.app.flushPendingMove()
}

// assertNoStrayTouch fails if any control message reached the device during
// the window. A stray touch-move is not cosmetic: a move to (0,0) between the
// down and the up of a tap is far past Android's touch slop, so the tap is
// cancelled and the swipe is garbage.
func (h *webTestHarness) assertNoStrayTouch(t *testing.T, window time.Duration) {
	t.Helper()
	select {
	case b := <-h.devMsgs:
		action, x, y, sw, sh := touchParts(t, b)
		t.Fatalf("stray control message reached the device: action=%d pos=(%d,%d) screen=%dx%d\n% x",
			action, x, y, sw, sh, b)
	case <-time.After(window):
	}
}

// TestWebDragEmitsNoStrayMoves is the regression test for the web input bug:
// after the browser's move was sent, the coalescer held an empty pending
// position while the finger was still down, and every run-loop tick flushed
// that empty position to the device as a real touch-move to (0,0).
func TestWebDragEmitsNoStrayMoves(t *testing.T) {
	h, cl, cv := webDragHarness(t)

	cl.send(webEvent{Op: "down", X: 32767, Y: 32767})
	if action, x, y, sw, sh := touchParts(t, h.readDev(t, 2*time.Second)); action != motionActionDown {
		t.Fatalf("down action=%d pos=(%d,%d) screen=%dx%d", action, x, y, sw, sh)
	}

	// One browser move: it is sent immediately (nothing pending yet).
	cl.send(webEvent{Op: "move", X: 40000, Y: 40000})
	action, mx, my, msw, msh := touchParts(t, h.readDev(t, 2*time.Second))
	if action != motionActionMove {
		t.Fatalf("first move action=%d, want %d", action, motionActionMove)
	}
	if msw != uint16(cv.vw) || msh != uint16(cv.vh) {
		t.Fatalf("move carried screen %dx%d, want %dx%d", msw, msh, cv.vw, cv.vh)
	}
	if mx <= 0 || my <= 0 {
		t.Fatalf("move mapped to (%d,%d), expected inside the frame", mx, my)
	}

	// The finger is still down and the browser is silent (no new pointermove).
	// Ticks must not invent a move.
	for i := 0; i < 5; i++ {
		h.tick(t)
	}
	h.assertNoStrayTouch(t, 60*time.Millisecond)

	cl.send(webEvent{Op: "up", X: 40000, Y: 40000})
	if action, x, y, _, _ := touchParts(t, h.readDev(t, 2*time.Second)); action != motionActionUp {
		t.Fatalf("up action=%d pos=(%d,%d)", action, x, y)
	}
	h.assertNoStrayTouch(t, 60*time.Millisecond)
}

// TestWebTapAfterDragIsClean covers the same defect from the user's side: a
// tap that follows a drag must still be a tap. Previously the tick between the
// down and the up injected a touch-move to (0,0), which Android treats as the
// pointer leaving the target, cancelling the tap.
func TestWebTapAfterDragIsClean(t *testing.T) {
	h, cl, _ := webDragHarness(t)

	// Establish a drag so the coalescer has a live timestamp.
	cl.send(webEvent{Op: "down", X: 20000, Y: 20000})
	h.readDev(t, 2*time.Second)
	cl.send(webEvent{Op: "move", X: 21000, Y: 21000})
	h.readDev(t, 2*time.Second)
	cl.send(webEvent{Op: "up", X: 21000, Y: 21000})
	h.readDev(t, 2*time.Second)

	// Now a plain tap: down, a tick, up.
	cl.send(webEvent{Op: "down", X: 30000, Y: 30000})
	_, dx, dy, dsw, dsh := touchParts(t, h.readDev(t, 2*time.Second))
	h.tick(t)
	h.assertNoStrayTouch(t, 60*time.Millisecond)
	cl.send(webEvent{Op: "up", X: 30000, Y: 30000})
	action, ux, uy, usw, ush := touchParts(t, h.readDev(t, 2*time.Second))
	if action != motionActionUp {
		t.Fatalf("up action=%d, want %d", action, motionActionUp)
	}
	if ux != dx || uy != dy || usw != dsw || ush != dsh {
		t.Fatalf("tap released at (%d,%d) %dx%d but pressed at (%d,%d) %dx%d",
			ux, uy, usw, ush, dx, dy, dsw, dsh)
	}
}
