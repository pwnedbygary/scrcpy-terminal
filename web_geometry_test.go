package main

// Regression tests for the frame-buffer/canvas geometry contract.
//
// The bug these exist for: the frame buffer is rendered ONCE per frame at the
// stream's canvas size, but each web client used to derive its canvas from its
// own viewport. The browser asks for a window-sized viewport (1777x1000), so
// the client's canvas was far larger than the frame buffer, and the native
// mjpeg encoder -- which reads exactly w*h*4 bytes from a bare pointer, with no
// length to check -- read megabytes past the end of that buffer. It crashed
// scterm with SIGSEGV, and when it did not fault it encoded heap garbage.
//
// The older tests could not catch it because they built frames that matched the
// client canvas by construction (rgb: makeCanvas(want.cw, want.ch, 7)). These
// tests deliberately use a REALISTIC frame -- a small buffer, as the terminal-
// derived stream canvas produced -- together with a large client viewport.

import (
	"bytes"
	"encoding/binary"
	"image/jpeg"
	"testing"
	"time"
)

// TestWebLargeViewportSmallFrame is the crash regression: a client with a
// window-sized viewport must not make the server encode a canvas larger than
// the frame buffer it actually has.
func TestWebLargeViewportSmallFrame(t *testing.T) {
	h := newWebTestHarness(t, config{web: true, video: true}, false)

	cl := dialWS(t, h.url())
	// A greedy viewport: this is what a maximised browser window reports, and
	// what used to size the client canvas independently of the frame.
	cl.send(webEvent{Op: "hello", W: 1777, H: 1000, Format: "auto", Caps: "audio"})
	cl.nextJSON("hello", 3*time.Second)

	// A frame exactly as the stream produced it before the fix: the video is
	// 1280x720 but the canvas is the (small) terminal-derived one, so the
	// buffer is 80x46 and nowhere near the client's 1777x1000 viewport.
	const fw, fh = 80, 46
	frame := &videoFrame{
		rgb: makeCanvas(fw, fh, 3),
		w:   1280, h: 720,
		cw: fw, ch: fh,
		fps: 60,
	}

	// The server must not crash, and must not encode more than it has.
	h.srv.frame(frame)

	msg := cl.nextBinary(webMsgVideo, 5*time.Second)
	gotW := int(binary.BigEndian.Uint16(msg[5:7]))
	gotH := int(binary.BigEndian.Uint16(msg[7:9]))
	if gotW != fw || gotH != fh {
		t.Fatalf("canvas %dx%d, want the FRAME geometry %dx%d: the client viewport must not size the canvas",
			gotW, gotH, fw, fh)
	}

	// And it must be a real JPEG of that geometry, not a truncated payload.
	body := msg[webHeaderLen:]
	if body[0] != 0xff || body[1] != 0xd8 {
		t.Fatalf("payload is not a JPEG: % x", body[:4])
	}
	img, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != fw || b.Dy() != fh {
		t.Fatalf("jpeg is %dx%d, want %dx%d", b.Dx(), b.Dy(), fw, fh)
	}
}

// TestWebFrameCanvasIsAuthoritative pins the contract itself: whatever geometry
// a frame declares, that is the canvas every client is served, and the payload
// length always matches it. This is what makes one shared frame buffer safe.
func TestWebFrameCanvasIsAuthoritative(t *testing.T) {
	h := newWebTestHarness(t, config{web: true, video: true}, false)

	cl := dialWS(t, h.url())
	cl.send(webEvent{Op: "hello", W: 320, H: 200, Format: "auto", Caps: "audio"})
	cl.nextJSON("hello", 3*time.Second)

	// Two different frame geometries in a row, neither matching the viewport.
	for _, sz := range [][2]int{{64, 48}, {120, 90}} {
		fw, fh := sz[0], sz[1]
		h.srv.frame(&videoFrame{
			rgb: makeCanvas(fw, fh, byte(fw)),
			w:   640, h: 480, cw: fw, ch: fh, fps: 30,
		})
		msg := cl.nextBinary(webMsgVideo, 5*time.Second)
		if gotW, gotH := int(binary.BigEndian.Uint16(msg[5:7])), int(binary.BigEndian.Uint16(msg[7:9])); gotW != fw || gotH != fh {
			t.Fatalf("canvas %dx%d, want %dx%d", gotW, gotH, fw, fh)
		}
	}
}

// TestEncodeWithRejectsShortFrame is the guard at the cgo boundary: even if a
// caller gets the geometry wrong, an undersized buffer must come back as an
// error instead of being read out of bounds by the encoder.
func TestEncodeWithRejectsShortFrame(t *testing.T) {
	h := newWebTestHarness(t, config{web: true, video: true}, false)

	cv := computeWebCanvas(640, 480, 640, 480) // 480x480-ish canvas
	need := cv.cw * cv.ch * 4
	short := make([]byte, need-4) // one pixel shy
	dst := make([]byte, webHeaderLen+1<<20)

	for _, tc := range []struct {
		name string
		buf  []byte
	}{
		{"short", short},
		{"empty", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := h.srv.encodeWith(cv, tc.buf, dst, webHeaderLen)
			if err == nil {
				t.Fatalf("encodeWith accepted a %d-byte buffer for a %dx%d canvas (needs %d) and returned n=%d",
					len(tc.buf), cv.cw, cv.ch, need, n)
			}
		})
	}

	// The exact-size buffer must still work: the guard must not be so strict
	// that it rejects valid frames.
	exact := makeCanvas(cv.cw, cv.ch, 5)
	n, err := h.srv.encodeWith(cv, exact, dst, webHeaderLen)
	if err != nil {
		t.Fatalf("encodeWith rejected an exact-size buffer: %v", err)
	}
	if n <= 0 {
		t.Fatalf("encode produced %d bytes", n)
	}
}

// TestWebModeCanvasFollowsVideoGeomety checks the other half of the fix: in web
// mode the stream canvas is the video's own size, not the terminal's cell grid.
// With stdout redirected (not a tty) the terminal size defaults to 80x24, which
// used to make a 1280x720 session render into an 80x46 canvas.
func TestWebModeCanvasFollowsVideoGeomety(t *testing.T) {
	s := &streamState{cfg: config{}, webMode: true}
	s.videoW, s.videoH = 1280, 720
	s.updateGeometry()

	if s.canvasW != 1280 || s.canvasH != 720 {
		t.Fatalf("web canvas %dx%d, want the video size 1280x720", s.canvasW, s.canvasH)
	}
	if s.fitW != 1280 || s.fitH != 720 || s.fitOffX != 0 || s.fitOffY != 0 {
		t.Fatalf("web fit is %dx%d at %d,%d; want the full canvas with no letterbox",
			s.fitW, s.fitH, s.fitOffX, s.fitOffY)
	}

	// Odd dimensions must be rounded down: JPEG 4:2:0 cannot encode them.
	s.videoW, s.videoH = 1281, 721
	s.updateGeometry()
	if s.canvasW != 1280 || s.canvasH != 720 {
		t.Fatalf("odd video size gave canvas %dx%d, want 1280x720", s.canvasW, s.canvasH)
	}

	// Before the first session header there is no geometry to render into:
	// the canvas must stay zero so no frame buffer is allocated.
	s.videoW, s.videoH = 0, 0
	s.updateGeometry()
	if s.canvasW != 0 || s.canvasH != 0 {
		t.Fatalf("canvas %dx%d before the video size is known, want 0x0", s.canvasW, s.canvasH)
	}

	// The TUI path must be unchanged: the canvas is the terminal cell grid
	// (cols x 2*(rows-1), one row for the status bar) with a letterboxed fit.
	// Under `go test` there is no tty, so termSize() falls back to 80x24.
	tui := &streamState{cfg: config{}}
	tui.videoW, tui.videoH = 1280, 720
	tui.updateGeometry()
	cols, rows := termSize()
	if tui.canvasW != cols || tui.canvasH != 2*(rows-1) {
		t.Fatalf("tui canvas %dx%d, want %dx%d (terminal cell grid)",
			tui.canvasW, tui.canvasH, cols, 2*(rows-1))
	}
	if tui.canvasW == 1280 || tui.canvasH == 720 {
		t.Fatalf("tui canvas took the video size (%dx%d); webMode leaked into the TUI path",
			tui.canvasW, tui.canvasH)
	}
	if tui.fitW > tui.canvasW || tui.fitH > tui.canvasH {
		t.Fatalf("tui fit %dx%d does not fit inside canvas %dx%d",
			tui.fitW, tui.fitH, tui.canvasW, tui.canvasH)
	}
}
