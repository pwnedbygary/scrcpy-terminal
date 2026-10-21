package main

import (
	"math"
	"strconv"
	"strings"
	"testing"
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

// TestOverlayPaintsWithoutACanvas: with no picture yet (or video off), and
// with a snapshot from another geometry, the overlay and the status still
// paint on a cleared screen, no video cell is drawn, and the next frame
// repaints every row.
func TestOverlayPaintsWithoutACanvas(t *testing.T) {
	for _, stale := range []bool{false, true} {
		tr := &tui{cols: 20, rows: 4, running: true}
		tr.keys = make([]uint64, 20*4)
		tr.prev = make([]uint64, 20*4)
		if stale {
			tr.lastRGB = make([]byte, 16*5*2*4) // same cell count, other shape
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
