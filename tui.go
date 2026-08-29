package main

import (
	"os"
	"strconv"
	"sync"
	"time"
	"unsafe"
)

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

// termSize returns the current terminal size (cols, rows).
func termSize() (cols, rows int) {
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
	mu       sync.Mutex
	fd       int
	cols     int
	rows     int
	keys     []uint64 // current cell keys (cols * (rows-1))
	prev     []uint64 // last drawn keys
	shown    bool
	frameIdx int
	status   string
	dirty    bool // force full repaint
	running  bool

	// color resolution helpers
	sgrBuf []byte
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
	if len(rgba) < w*h*4 {
		return // stale canvas (resize in flight); next frame fixes it
	}
	packCells(rgba, t.keys, w, h)

	t.frameIdx++
	forceFull := t.dirty || t.frameIdx%24 == 0
	t.dirty = false

	out := t.sgrBuf[:0]
	if forceFull {
		// move home + clear
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
	// status bar (redraw only when the status string changed)
	if t.status != "" {
		line := truncateRunes(t.status, t.cols)
		out = ap(out, "\x1b[", strconv.Itoa(t.rows+1), ";1H")
		out = append(out, "\x1b[38;2;200;200;200m\x1b[48;2;30;30;30m"...)
		out = append(out, line...)
		out = append(out, "\x1b[0m"...)
	}
	t.sgrBuf = out
	if len(out) > 0 {
		os.Stdout.Write(out)
	}
}

func rowEqual(a, b []uint64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rgbStr renders "R;G;B" for a 0x00RRGGBB value (already quantized).
func rgbStr(c uint32) string {
	return strconv.Itoa(int(c>>16)&0xFF) + ";" + strconv.Itoa(int(c>>8)&0xFF) + ";" + strconv.Itoa(int(c)&0xFF)
}

func (t *tui) setStatus(s string) {
	t.mu.Lock()
	if s != t.status {
		t.status = s
		t.dirty = true
	}
	t.mu.Unlock()
}

func (t *tui) shellInit() {
	os.Stdout.WriteString("\x1b[?1049h") // alt screen
	os.Stdout.WriteString("\x1b[?25l")   // hide cursor
	os.Stdout.WriteString("\x1b[2J\x1b[H")
	t.running = true
}

func (t *tui) shellClose() {
	t.running = false
	os.Stdout.WriteString("\x1b[0m\x1b[?25h\x1b[?1049l")
}

var _ = unsafe.Pointer(nil)
var _ = time.Second
