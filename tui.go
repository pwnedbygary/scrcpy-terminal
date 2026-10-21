package main

import (
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// Synchronized output (DEC private mode 2026): the terminal holds what it
// receives until the end marker, so a repaint never shows half the old frame.
// Terminals that do not know the mode ignore it.
const (
	syncBegin = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
)

const statusSGR = "\x1b[38;2;200;200;200m\x1b[48;2;30;30;30m"

const placeholderSGR = "\x1b[38;2;150;150;150m"

// ---------------------------------------------------------------------------
// terminal helpers
// ---------------------------------------------------------------------------

// ap appends strings to a byte slice (compact emit helper).
func ap(out []byte, parts ...string) []byte {
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// termSizeOverride, when non-zero, replaces the ioctl-derived terminal size.
// Tests use it to exercise geometry-dependent layout (the action menu's
// status-row bound, for example) without a pty.
var termSizeOverride struct{ cols, rows int }

// termSize returns the current terminal size (cols, rows).
func termSize() (cols, rows int) {
	if termSizeOverride.cols > 0 && termSizeOverride.rows > 0 {
		return termSizeOverride.cols, termSizeOverride.rows
	}
	w, h, err := ioctlWinsize()
	if err != nil || w == 0 || h == 0 {
		return 80, 24
	}
	return int(w), int(h)
}

// ---------------------------------------------------------------------------
// tuiState: cell grid + ANSI diff rendering
// ---------------------------------------------------------------------------

type tui struct {
	mu              sync.Mutex
	fd              int
	cols            int
	rows            int
	keys            []uint64 // current cell keys (cols * (rows-1))
	prev            []uint64 // last drawn keys
	shown           bool
	frameIdx        int
	repaintInterval int // forced full redraw every N frames (desync-recovery safety net)
	status          string
	statusDrawn     string // what the status row shows now ("" after a clear or resize)
	placeholder     string // centred in the picture area until the first frame
	dirty           bool   // force full repaint
	running         bool

	// color resolution helpers
	sgrBuf []byte

	// overlay rendered over the video area (set by the app): palette text or
	// the software keyboard. Each line may carry a highlight span (cursor).
	overlay []overlayLine

	lastRGB []byte // last full canvas snapshot (cols x 2*rows RGBA), for Alt+S screenshots
	lastW   int    // canvas pixel width the snapshot was taken at
	lastH   int    // ...and height; a resize that keeps the same cell count would
	// otherwise let repaint() shear the old canvas into the new grid
}

func newTUI() *tui {
	t := &tui{fd: ttyReadFD()}
	t.resize()
	return t
}

func (t *tui) resize() {
	cols, rows := termSize()
	if rows <= 3 {
		cols, rows = 80, 24
	}
	t.cols = cols
	t.rows = rows - 1 // last line is the status bar
	if t.cols > 0 && t.rows > 0 {
		t.keys = make([]uint64, t.cols*t.rows)
		t.prev = make([]uint64, t.cols*t.rows)
		for i := range t.prev {
			t.prev[i] = ^uint64(0) // force first draw
		}
	}
	t.dirty = true
	t.statusDrawn = "" // the status row moved with the bottom edge
}

// draw renders packets of cells. rgba is the canvas (cols x 2*rows RGBA).
func (t *tui) draw(rgba []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running {
		return
	}
	w := t.cols
	h := t.rows * 2 // two vertical pixels per cell
	if !packCells(rgba, t.keys, w, h) {
		return // stale canvas (resize in flight); next frame fixes it
	}

	// keep a fresh snapshot for Alt+S screenshots (len(rgba)==w*h*4 guaranteed by packCells)
	n := w * h * 4
	if len(t.lastRGB) != n {
		t.lastRGB = make([]byte, n)
	}
	copy(t.lastRGB[:n], rgba[:n])
	t.lastW, t.lastH = w, h

	t.frameIdx++
	interval := t.repaintInterval
	if interval < 1 {
		interval = 1
	}
	forceFull := t.dirty || t.frameIdx%interval == 0
	t.dirty = false

	out := append(t.sgrBuf[:0], syncBegin...)
	mark := len(out)
	if forceFull {
		out = append(out, "\x1b[H"...)
	}
	for y := 0; y < t.rows; y++ {
		row := y * w
		if !forceFull && rowEqual(t.keys[row:row+w], t.prev[row:row+w]) {
			continue
		}
		if !forceFull || y > 0 {
			out = ap(out, "\x1b[", strconv.Itoa(y+1), ";1H")
		}
		// run-length encode the row: same (fg,bg) -> repeat '▀'
		var lastFg, lastBg uint32 = 0xFFFFFFFF, 0xFFFFFFFF
		var runCount int
		flushRun := func() {
			if runCount == 0 {
				return
			}
			// SGR: fg=top, bg=bottom (truecolor)
			if lastFg == lastBg {
				// solid cell: space with bg color, repeated runCount times
				out = ap(out, "\x1b[38;2;", rgbStr(lastFg), "m\x1b[48;2;", rgbStr(lastFg), "m")
				for i := 0; i < runCount; i++ {
					out = append(out, ' ')
				}
			} else {
				out = ap(out, "\x1b[38;2;", rgbStr(lastFg), "m\x1b[48;2;", rgbStr(lastBg), "m")
				for i := 0; i < runCount; i++ {
					out = append(out, '\xE2', '\x96', '\x80') // ▀
				}
			}
			runCount = 0
		}
		for x := 0; x < w; x++ {
			k := t.keys[row+x]
			fg := uint32(k & 0xFFFFFFFF)
			bg := uint32(k >> 32)
			if fg != lastFg || bg != lastBg {
				flushRun()
				lastFg, lastBg = fg, bg
			}
			runCount++
		}
		flushRun()
		// copy back the drawn row
		copy(t.prev[row:row+w], t.keys[row:row+w])
	}
	out = t.appendOverlay(out)
	if (forceFull && t.status != "") || t.status != t.statusDrawn {
		out = t.appendStatus(out)
	}
	t.sgrBuf = out
	if len(out) > mark {
		// A frame that changes only video rows would leave its last cell's
		// colours set on the terminal.
		out = append(out, "\x1b[0m"+syncEnd...)
		t.sgrBuf = out
		os.Stdout.Write(out)
	}
}

// appendOverlay paints the overlay's text rows over the video. EMPTY rows are
// skipped so the video stays visible above the keyboard (the keyboard only
// paints its own rows, and each of those has an opaque background).
func (t *tui) appendOverlay(out []byte) []byte {
	if len(t.overlay) > 0 {
		for i, ol := range t.overlay {
			if i >= t.rows {
				break
			}
			if ol.text == "" && (ol.hlFrom < 0 || ol.hlTo <= ol.hlFrom) &&
				(ol.flFrom < 0 || ol.flTo <= ol.flFrom) {
				continue // transparent: keep the video visible
			}
			line := truncateRunes(ol.text, t.cols)
			out = ap(out, "\x1b[", strconv.Itoa(i+1), ";1H",
				"\x1b[48;2;0;0;0m\x1b[38;2;220;220;220m")
			// Segment-based emission: cursor (reverse video) and press-flash
			// (green bg) spans are sorted and emitted without overlap.
			// Keyboard button segments (segs) paint the whole row first.
			hb := []byte(line)
			bFrom := runeByteIndex(line, ol.hlFrom)
			bTo := runeByteIndex(line, ol.hlTo)
			if bFrom < 0 {
				bFrom = len(hb)
			}
			if bTo < 0 || bTo > len(hb) {
				bTo = len(hb)
			}
			gFrom := runeByteIndex(line, ol.flFrom)
			gTo := runeByteIndex(line, ol.flTo)
			if gFrom < 0 {
				gFrom = len(hb)
			}
			if gTo < 0 || gTo > len(hb) {
				gTo = len(hb)
			}
			type span struct {
				from, to int
				code     string
				reset    string
			}
			var spans []span
			if bFrom < bTo {
				spans = append(spans, span{bFrom, bTo, "\x1b[7m", "\x1b[27m"})
			}
			if gFrom < gTo {
				spans = append(spans, span{gFrom, gTo,
					"\x1b[48;2;0;150;0m\x1b[38;2;255;255;255m",
					"\x1b[0m\x1b[48;2;0;0;0m\x1b[38;2;220;220;220m"})
			}
			// Keyboard button spans: paint with their own SGR before the
			// cursor/flash overlays (which take priority when overlapping).
			if len(ol.segs) > 0 {
				pos := 0
				for _, s := range ol.segs {
					sf := runeByteIndex(line, s.from)
					st := runeByteIndex(line, s.to)
					if sf < 0 {
						sf = len(hb)
					}
					if st < 0 || st > len(hb) {
						st = len(hb)
					}
					if sf > pos {
						out = append(out, hb[pos:sf]...)
					}
					if st > sf {
						out = append(out, s.code...)
						out = append(out, hb[sf:st]...)
						out = append(out, btnReset...)
						out = append(out, "\x1b[48;2;0;0;0m\x1b[38;2;220;220;220m"...)
					}
					pos = st
				}
				if pos < len(hb) {
					out = append(out, hb[pos:]...)
				}
			} else if len(spans) == 0 {
				out = append(out, hb...)
			} else {
				// sort by start
				if len(spans) == 2 && spans[1].from < spans[0].from {
					spans[0], spans[1] = spans[1], spans[0]
				}
				pos := 0
				for _, sp := range spans {
					if sp.from > pos {
						out = append(out, hb[pos:sp.from]...)
					}
					// clamp in case of overlap
					f := sp.from
					t := sp.to
					if f < pos {
						f = pos
					}
					if t <= f {
						continue
					}
					out = append(out, sp.code...)
					out = append(out, hb[f:t]...)
					out = append(out, sp.reset...)
					pos = t
				}
				if pos < len(hb) {
					out = append(out, hb[pos:]...)
				}
			}
			// Pad by display cells: '·', '‹' and '›' take several bytes, and
			// padding by bytes left old cells showing at the end of the row.
			for j := utf8.RuneCountInString(line); j < t.cols; j++ {
				out = append(out, ' ')
			}
			out = append(out, "\x1b[0m"...)
		}
	}
	return out
}

// appendStatus paints the status row and records what it now shows. It is cut
// by display cells, so no character is written past the row's last cell.
func (t *tui) appendStatus(out []byte) []byte {
	line := truncateCells(t.status, t.cols)
	out = ap(out, "\x1b[", strconv.Itoa(t.rows+1), ";1H", statusSGR, line)
	if cellWidth(line) < t.cols {
		// Erase what a longer status left behind. Not after a full row: the
		// cursor is then still on the row's last cell, and the erase would
		// take that cell with it.
		out = append(out, "\x1b[K"...)
	}
	out = append(out, "\x1b[0m"...)
	t.statusDrawn = t.status
	return out
}

func rowEqual(a, b []uint64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// channelLevels maps each of the eight levels the cell packer keeps per
// channel (it masks with 0xE0) to an 8-bit value. Repeating the three bits
// across the byte spreads the levels over the whole 0..255 range; writing the
// masked value itself capped white at 224 and darkened every colour.
var channelLevels = func() (s [8]string) {
	for lvl := range s {
		v := lvl << 5
		s[lvl] = strconv.Itoa(v | v>>3 | v>>6)
	}
	return s
}()

// rgbStr renders "R;G;B" for a quantized 0x00RRGGBB cell colour.
func rgbStr(c uint32) string {
	return channelLevels[c>>21&7] + ";" + channelLevels[c>>13&7] + ";" + channelLevels[c>>5&7]
}

// setOverlay replaces the overlay and marks the grid dirty. The caller is
// expected to repaint (see repaint): overlays are also drawn by the next video
// frame, but a static device screen may not produce one for a long time.
func (t *tui) setOverlay(lines []overlayLine) {
	t.mu.Lock()
	t.overlay = lines
	t.dirty = true
	t.mu.Unlock()
}

// repaint redraws the last canvas with the current overlay, so an overlay
// change does not wait for the next video frame. A static device screen can
// send no frames at all, and without this the keyboard, the action menu and
// the action bar could stay invisible (and a hide could stay on screen) until
// something on the device moved.
func (t *tui) repaint() {
	rgb := func() []byte {
		t.mu.Lock()
		defer t.mu.Unlock() // also on a panic, which handleInput recovers from
		rgb := t.lastRGB
		if t.lastW != t.cols || t.lastH != t.rows*2 || len(rgb) == 0 {
			// Nothing drawn yet (or video is off), or the snapshot is from
			// another geometry (a resize can keep the same cell count) and
			// would be sheared into the new grid.
			t.paintWithoutCanvas()
			return nil
		}
		return rgb
	}()
	if rgb == nil {
		return
	}
	// draw takes the lock itself and treats the snapshot as a canvas; the
	// copy-back is a no-op because it is the same slice.
	t.draw(rgb)
}

// paintWithoutCanvas clears the picture and paints only the overlay and the
// status row, so the menu, the keyboard and the bar work before the first
// frame and with --video=false; until the first frame, the placeholder says
// why the picture is empty. Called with t.mu held.
func (t *tui) paintWithoutCanvas() {
	waiting := t.placeholder != "" && len(t.lastRGB) == 0
	if !t.running || t.rows <= 0 || (!overlayHasContent(t.overlay) && t.status == "" && !waiting) {
		return
	}
	out := append(t.sgrBuf[:0], syncBegin...)
	out = append(out, "\x1b[0m\x1b[H\x1b[2J"...)
	if waiting {
		line := ellipsize(t.placeholder, t.cols)
		row := (t.rows + 1) / 2
		col := (t.cols-cellWidth(line))/2 + 1
		out = ap(out, "\x1b[", strconv.Itoa(row), ";", strconv.Itoa(col), "H", placeholderSGR, line, "\x1b[0m")
	}
	out = t.appendOverlay(out)
	if t.status != "" {
		out = t.appendStatus(out)
	}
	out = append(out, syncEnd...)
	t.sgrBuf = out
	t.dirty = true // the cleared rows need a full repaint from the next frame
	os.Stdout.Write(out)
}

// overlayHasContent reports whether any overlay row paints something.
func overlayHasContent(lines []overlayLine) bool {
	for _, ol := range lines {
		if ol.text != "" || (ol.hlFrom >= 0 && ol.hlTo > ol.hlFrom) || (ol.flFrom >= 0 && ol.flTo > ol.flFrom) {
			return true
		}
	}
	return false
}

// setStatus records the status line and paints its row at once: a static
// device screen sends no frames to carry it, and the fps reading changes
// twice a second, which must not cost a repaint of the whole screen.
func (t *tui) setStatus(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = s
	if !t.running || t.rows <= 0 || s == t.statusDrawn {
		return
	}
	os.Stdout.Write(t.appendStatus(make([]byte, 0, 256)))
}

// setPlaceholder sets the line shown in the empty picture before the first
// frame. The caller repaints.
func (t *tui) setPlaceholder(s string) {
	t.mu.Lock()
	t.placeholder = s
	t.mu.Unlock()
}

// hasFrame reports whether a frame has been drawn yet.
func (t *tui) hasFrame() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.lastRGB) > 0
}

// markDirty forces a full repaint on the next drawn frame.
func (t *tui) markDirty() {
	t.mu.Lock()
	t.dirty = true
	t.mu.Unlock()
}

func (t *tui) shellInit() {
	os.Stdout.WriteString("\x1b[?1049h") // alt screen
	os.Stdout.WriteString("\x1b[?25l")   // hide cursor
	// No autowrap: a character drawn wider than cellWidth counted it (an
	// emoji with a variation selector) is clipped at the edge instead of
	// wrapping, which on the bottom row would scroll the whole picture.
	os.Stdout.WriteString("\x1b[?7l")
	// Bracketed paste: the terminal marks where a paste starts and ends, so
	// it reaches the device as one paste and nothing in it is read as a key.
	os.Stdout.WriteString("\x1b[?2004h")
	os.Stdout.WriteString("\x1b[2J\x1b[H")
	t.statusDrawn = ""
	t.running = true
}

// shellCloseWait bounds how long shellClose waits for the painter's lock, so
// a holder that is not writing (a panic while it held the lock) cannot keep
// the terminal from being given back. A painter blocked in a write still
// holds the restore up: the restore's own writes queue behind it on stdout.
const shellCloseWait = 100 * time.Millisecond

// lockWithin takes t.mu if it can within d, and reports whether it did. It
// waits in Lock, queued behind the painter, rather than polling TryLock: a
// run loop drawing frame after frame takes the lock back before a poll gets
// it. A lock that comes after d is let go again at once.
func (t *tui) lockWithin(d time.Duration) bool {
	got, gaveUp := make(chan struct{}), make(chan struct{})
	go func() {
		t.mu.Lock()
		select {
		case got <- struct{}{}:
		case <-gaveUp:
			t.mu.Unlock()
		}
	}()
	timeout := time.NewTimer(d)
	defer timeout.Stop()
	select {
	case <-got:
		return true
	case <-timeout.C:
		close(gaveUp)
		return false
	}
}

func (t *tui) shellClose() {
	if t.lockWithin(shellCloseWait) {
		defer t.mu.Unlock()
	}
	t.running = false
	// A frame cut off part way leaves the terminal holding back everything
	// after it until the end of the synchronized update.
	os.Stdout.WriteString(syncEnd)
	// Always disable mouse reporting + restore the normal screen, so a crash
	// or quit never leaves SGR garbage pouring into the shell.
	os.Stdout.WriteString(mouseOffSeq)
	os.Stdout.WriteString("\x1b[0m\x1b[?25h\x1b[?7h\x1b[?2004l\x1b[?1049l")
}
