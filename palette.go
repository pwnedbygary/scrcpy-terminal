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
	if a.stream == nil {
		return position{}
	}
	return a.stream.mapCell(cellX, cellY)
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

const paletteRows = 10

func (a *app) openPalette() {
	if a.ctrl == nil {
		return
	}
	a.paletteOpen = true
	a.paletteIdx = 0
	a.refreshPalette()
}

func (a *app) closePalette() {
	a.paletteOpen = false
	if a.tui != nil {
		a.tui.setOverlay(nil)
		a.tui.setStatus(a.statusLine())
		a.tui.dirty = true
	}
}

func (a *app) refreshPalette() {
	if a.tui == nil {
		return
	}
	a.tui.setOverlay(a.paletteLines())
	a.tui.setStatus(a.statusLine())
}

func (a *app) paletteLines() []string {
	if !a.paletteOpen || a.ctrl == nil {
		return nil
	}
	n := paletteRows
	if len(a.paletteKeys) < n {
		n = len(a.paletteKeys)
	}
	start := (a.paletteIdx / paletteRows) * paletteRows
	if start+n > len(a.paletteKeys) {
		start = len(a.paletteKeys) - n
	}
	if start < 0 {
		start = 0
	}
	lines := make([]string, 0, n+1)
	for i := 0; i < n; i++ {
		idx := start + i
		cursor := "  "
		if idx == a.paletteIdx {
			cursor = "> "
		}
		if idx < len(a.paletteKeys) {
			code := a.paletteKeys[idx]
			name := androidKeycodes[code]
			lines = append(lines, fmt.Sprintf("%s%3d %s", cursor, code, name))
		} else {
			lines = append(lines, "")
		}
	}
	lines = append(lines, fmt.Sprintf("key palette: %d/%d · ↑↓ move, ←→ ±10, Enter send, [a-z] jump, Ctrl-K/Esc close",
		a.paletteIdx+1, len(a.paletteKeys)))
	return lines
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
		case "\x1b[C":
			a.paletteIdx += 10
		case "\x1b[D":
			a.paletteIdx -= 10
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
	if len(a.paletteKeys) > 0 {
		if a.paletteIdx < 0 {
			a.paletteIdx += len(a.paletteKeys)
		}
		if a.paletteIdx >= len(a.paletteKeys) {
			a.paletteIdx -= len(a.paletteKeys)
		}
	}
	a.refreshPalette()
}

var _ = strconv.Itoa
