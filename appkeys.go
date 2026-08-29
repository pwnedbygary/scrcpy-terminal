package main

import "fmt"

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
// software keyboard input: navigation + press, while the keyboard is open.
// Returns true if the event was consumed by the keyboard.
// ---------------------------------------------------------------------------

func (a *app) keyboardInput(b []byte) bool {
	if a.kb == nil || !a.kb.open {
		return false
	}
	kb := a.kb
	// mouse clicks are handled by mouseEvent via hit-test; this handles keys
	if len(b) == 1 {
		switch b[0] {
		case 0x0d, 0x0a: // Enter: press the focused key
			if key, ok := kb.currentKey(); ok {
				kb.setFlashCurrent()
				kb.act(a, key)
				a.refreshKeyboard()
			}
			return true
		case 0x1b: // bare Esc = close the keyboard
			a.closeKeyboard()
			return true
		case 0x11: // Ctrl-Q
			return false // let the global handler quit
		}
	}
	switch string(b) {
	case "\x1b[A":
		kb.navigate(0, +1) // up in overlay = next layout row (rows are bottom-up)
		a.refreshKeyboard()
		return true
	case "\x1b[B":
		kb.navigate(0, -1)
		a.refreshKeyboard()
		return true
	case "\x1b[C":
		kb.navigate(+1, 0)
		a.refreshKeyboard()
		return true
	case "\x1b[D":
		kb.navigate(-1, 0)
		a.refreshKeyboard()
		return true
	case "\x1b[H", "\x1b[1~", "\x1b[7~": // Home: top row
		kb.curRow = len(kb.rows) - 1
		kb.curCol = 0
		a.refreshKeyboard()
		return true
	case "\x1b[F", "\x1b[4~", "\x1b[8~": // End: bottom row
		kb.curRow = 0
		kb.curCol = 0
		a.refreshKeyboard()
		return true
	case "\x1b[24~":
		a.closeKeyboard() // F12 also closes
		return true
	}
	// Escape sequences that belong to the keyboard shortcuts (Ctrl-K etc.)
	// are handled before we get here; anything else falls through to device.
	return false
}

// ---------------------------------------------------------------------------
// coalesced touch-move: burst drags collapse to ~one message per 8ms, so a
// Zellij mouse flood can't queue behind the device and inflate input latency.
// ---------------------------------------------------------------------------

const moveCoalesceNs = 8_000_000 // 8ms

func (a *app) coalescedMove(pos position) {
	now := timeNowUnixNano()
	if a.lastMoveNano != 0 {
		elapsed := now - a.lastMoveNano
		if elapsed < moveCoalesceNs {
			a.pendingMove = pos // keep the newest; flush on the next tick
			return
		}
	}
	a.lastMoveNano = now
	if a.ctrl != nil {
		a.ctrl.touchMove(pos)
	}
	a.pendingMove = position{}
}

// flushPendingMove sends the latest coalesced move (from the tick loop).
func (a *app) flushPendingMove() {
	if a.lastMoveNano == 0 {
		return
	}
	elapsed := timeNowUnixNano() - a.lastMoveNano
	if elapsed < moveCoalesceNs {
		return // not enough time has passed; keep waiting
	}
	if a.ctrl != nil && a.mouseDown {
		a.ctrl.touchMove(a.pendingMove)
	}
	a.pendingMove = position{}
	a.lastMoveNano = timeNowUnixNano()
}
