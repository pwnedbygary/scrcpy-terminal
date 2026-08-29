package main

import (
	"fmt"
	"strconv"
)

// mapAppFKey dispatches F1..F11 (F12 handled separately as grab).
func (a *app) mapAppFKey(code uint32) {
	switch code {
	case 131:
		a.sendAndroidKeyMeta(3, 0) // HOME
	case 132:
		a.sendAndroidKeyMeta(82, 0) // MENU
	case 133:
		a.sendAndroidKeyMeta(187, 0) // APP_SWITCH
	case 134:
		a.sendAndroidKeyMeta(26, 0) // POWER
	case 135:
		a.sendAndroidKeyMeta(25, 0) // VOLUME_DOWN
	case 136:
		a.sendAndroidKeyMeta(24, 0) // VOLUME_UP
	case 137:
		a.sendAndroidKeyMeta(91, 0) // MUTE
	case 138:
		if a.ctrl != nil {
			a.ctrl.rotate()
		}
	case 139:
		if a.ctrl != nil {
			a.ctrl.expandNotifications()
		}
	case 140:
		if a.ctrl != nil {
			a.ctrl.expandSettings()
		}
	case 141:
		if a.ctrl != nil {
			a.ctrl.collapsePanels()
		}
	case 142:
		a.toggleGrab() // F12
	}
}

// posAt maps terminal cell (1-based) to an Android position using the frame
// geometry currently displayed (screen_size = frame size, as the C client does).
func (a *app) posAt(cellX, cellY int) position {
	cols, rows := termSize()
	rows--
	if a.frameW == 0 || a.frameH == 0 {
		a.frameW = a.stream.videoW
		a.frameH = a.stream.videoH
	}
	fw, fh := a.frameW, a.frameH
	// center of the cell in canvas pixels
	px := float64(cellX-1) + 0.5
	py := float64(cellY-1) + 0.5
	x := int32(px * float64(fw) / float64(cols))
	y := int32(py * float64(fh) / float64(rows))
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if int(x) >= fw {
		x = int32(fw - 1)
	}
	if int(y) >= fh {
		y = int32(fh - 1)
	}
	return position{x: x, y: y, screenW: uint16(fw), screenH: uint16(fh)}
}

// scrollAt sends a scroll wheel event at the cell position.
func (a *app) scrollAt(cellX, cellY int, delta int) {
	if a.ctrl == nil || !a.grabbed {
		return
	}
	err := a.ctrl.scroll(a.posAt(cellX, cellY), float32(delta), 0)
	if err != nil {
		fmt.Fprintf(stderrWriter(), "sct: scroll: %v\n", err)
	}
}

// ---------------------------------------------------------------------------
// key palette: Ctrl-K opens a scrollable list of every Android key code
// ---------------------------------------------------------------------------

func (a *app) openPalette() {
	if a.ctrl == nil {
		return
	}
	a.paletteOpen = true
	a.paletteIdx = 0
	a.tui.setStatus(a.statusLine())
	a.drawPalette()
}

func (a *app) closePalette() {
	a.paletteOpen = false
	a.tui.setStatus(a.statusLine())
	a.tui.dirty = true
	a.tui.resize() // force full redraw of underlying frame next draw
}

func (a *app) paletteInput(b []byte) {
	if len(b) == 0 {
		return
	}
	switch {
	case b[0] == 0x1b:
		// Esc or arrow sequences
		s := string(b)
		switch s {
		case "\x1b[A":
			a.paletteIdx--
		case "\x1b[B":
			a.paletteIdx++
		case "\x1b[C", "\x1b[D":
			// left/right: jump by 10
			if s == "\x1b[C" {
				a.paletteIdx += 10
			} else {
				a.paletteIdx -= 10
			}
		default:
			a.closePalette()
			return
		}
	case b[0] == 0x0b: // Ctrl-K close
		a.closePalette()
		return
	case b[0] == 0x0d || b[0] == 0x0a: // Enter: send
		if len(a.paletteKeys) > 0 {
			idx := a.paletteIdx % len(a.paletteKeys)
			if idx < 0 {
				idx += len(a.paletteKeys)
			}
			code := a.paletteKeys[idx]
			a.sendAndroidKeyMeta(uint32(code), 0)
		}
		return
	case b[0] >= 0x20 && b[0] <= 0x7e:
		// jump to first key starting with this letter (A-Z)
		ch := byte(b[0])
		if ch >= 'a' && ch <= 'z' {
			ch -= 'a' - 'A'
		}
		for i, v := range a.paletteKeys {
			name := androidKeycodes[v]
			if len(name) > 0 && name[0] == ch {
				a.paletteIdx = i
				break
			}
		}
	}
	if a.paletteIdx < 0 {
		a.paletteIdx += len(a.paletteKeys)
	}
	if a.paletteIdx >= len(a.paletteKeys) {
		a.paletteIdx -= len(a.paletteKeys)
	}
	a.drawPalette()
}

// drawPalette renders the palette in the text area (10 rows near the top).
func (a *app) drawPalette() {
	if a.tui == nil || a.ctrl == nil {
		return
	}
	cols := a.tui.cols
	rows := a.tui.rows
	n := 10
	if rows < n {
		n = rows
	}
	start := (a.paletteIdx / n) * n
	if start+n > len(a.paletteKeys) {
		start = len(a.paletteKeys) - n
	}
	if start < 0 {
		start = 0
	}
	out := make([]byte, 0, 4096)
	out = append(out, "\x1b[1;1H"...)
	for i := 0; i < n; i++ {
		out = append(out, "\x1b[K"...)
		idx := start + i
		var line string
		if idx < len(a.paletteKeys) {
			code := a.paletteKeys[idx]
			name := androidKeycodes[code]
			cursor := "  "
			if idx == a.paletteIdx {
				cursor = "> "
			}
			line = fmt.Sprintf("%s%3d %s", cursor, code, name)
		}
		// pad to width for a clean overlay
		for len(line) < cols-1 && idx < len(a.paletteKeys) {
			line += " "
		}
		out = append(out, line...)
		if i < n-1 {
			out = append(out, "\r\n"...)
		}
	}
	out = append(out, "\x1b[K"...)
	out = append(out, "\r\n\u2500 key palette: \u2191\u2193 move \u2698 \u2190\u2192 \u00b110, Enter send, [a-z] jump, Ctrl-K/Esc close"...)
	_ = strconv.Itoa
	osStdoutWrite(out)
}

func osStdoutWrite(b []byte) {
	// direct write, bypassing tui mutex (input goroutine only calls this when
	// palette is active; the draw mutex is held elsewhere)
	writeStdout(b)
}
