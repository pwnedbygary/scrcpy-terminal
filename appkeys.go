package main

import (
	"fmt"
	"sync"
)

// ---------------------------------------------------------------------------
// app-level keys: the terminal and browser view of the action catalog
//
// The terminal reaches these through F1..F12, the browser through F1..F12 as
// well (or Alt+F1..F12 where the OS owns a bare F-key). Both end up here, so
// the same key cannot mean two different things depending on which display you
// are looking at -- which is exactly what happened while the browser sent
// Android's own F1..F12 keycodes for these keys and the terminal sent these
// actions instead.
//
// The browser also reaches these through Alt+<letter>, and so does the terminal
// (input.go). Phone keyboards (Unexpected Keyboard and friends) have Ctrl and
// Alt but no F-key row, so Alt+<letter> is the primary binding for them, not a
// fallback.
//
// The tables this section reads (appMnemonicOps, appKeyOps, appActionOps,
// appFKeyOps) are derived from protocol/fixtures/actions.json in actions.go;
// the web player has the same catalog (MNEMONIC_ACTIONS in player.js) and
// TestMnemonicOpsMatchTheBrowser in web_keymap_test.go pins the two together.
//
// Mute and Menu-as-Android-key have no letter chord: Alt+M stays the LOCAL mute
// (existing behaviour, both modes), Alt+O types an "o" in the browser, and both
// actions remain on F7/F2 and on the action bar/menu.
// ---------------------------------------------------------------------------

// mnemonicOp returns the app-level action for an Alt+<letter> chord, ignoring
// the case of a letter (Alt+B and Alt+b are the same chord). The slash that
// opens the menu is not a letter, so it is matched as-is.
func mnemonicOp(c byte) (string, bool) {
	if c >= 'A' && c <= 'Z' {
		c += 'a' - 'A'
	}
	op, ok := appMnemonicOps[c]
	return op, ok
}

// isAppOp reports whether op is an app-level action of either kind.
func isAppOp(op string) bool {
	if _, ok := appKeyOps[op]; ok {
		return true
	}
	return appActionOps[op]
}

// appFKeyOp names the action for a terminal F-key, given the Android keycode
// the terminal reports for it (F1..F12 = 131..142).
func appFKeyOp(code uint32) string {
	if code < 131 || code > 142 {
		return ""
	}
	i := int(code - 131)
	if i >= len(appFKeyOps) {
		return ""
	}
	return appFKeyOps[i]
}

// mapAppFKey dispatches F1..F12 (F12 = grab is also handled earlier, as a
// bare-key grab toggle).
func (a *app) mapAppFKey(code uint32) {
	a.applyAppOp(appFKeyOp(code))
}

// applyAppOp performs one app-level action. Both the terminal's keys and the
// browser's control events call this one function, and the headless browser
// dispatcher performs the same catalog action through performDeviceAction.
func (a *app) applyAppOp(op string) {
	if act, ok := deviceActionFor(op); ok {
		err := performDeviceAction(a.ctrl, act)
		if err != nil && a.ctrl != nil && act.Kind == kindKeycode {
			fmt.Fprintf(stderrWriter(), "scterm: inject key: %v\n", err)
		}
		return
	}
	switch op {
	case "grab":
		a.toggleGrab()
	case "quit":
		a.events <- inputEvent{kind: evQuit}
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
		fmt.Fprintf(stderrWriter(), "scterm: scroll: %v\n", err)
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
//
// This state is shared: in the terminal it is driven entirely by the run loop,
// but in --web/--window the browser's pointer events arrive on the control
// dispatcher goroutine while the run loop's tick flushes them. It is therefore
// mutex-protected, and the pending slot carries an explicit flag.
//
// The flag is not a nicety. (0,0) is a real device coordinate, so an empty
// pending slot cannot be told apart from a genuine move to the top-left corner.
// The flush used to send the empty slot on every tick while the finger was
// down, which fed the device a continuous stream of touch-moves to (0,0),
// screen size 0x0 -- far past Android's touch slop, so every tap was cancelled
// and every swipe became garbage. Web input appeared to do nothing at all.
// ---------------------------------------------------------------------------

const moveCoalesceNs = 8_000_000 // 8ms

// dragState is the pointer-drag state shared by the terminal input path (run
// loop goroutine) and the browser control dispatcher (its own goroutine).
type dragState struct {
	mu      sync.Mutex
	down    bool
	lastNs  int64
	pending position
	have    bool // pending holds a real position awaiting its flush
}

// setDown records the button press/release. Releasing also drops any queued
// move, so a coalesced move can never reach the device after the release that
// followed it.
func (d *dragState) setDown(v bool) {
	d.mu.Lock()
	d.down = v
	if !v {
		d.pending = position{}
		d.have = false
	}
	d.mu.Unlock()
}

func (d *dragState) isDown() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.down
}

// queue stores the newest position, reporting whether the caller should send it
// now or leave it for the next tick.
func (d *dragState) queue(pos position) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := timeNowUnixNano()
	if d.lastNs != 0 && now-d.lastNs < moveCoalesceNs {
		d.pending = pos // keep the newest; flush on the next tick
		d.have = true
		return false
	}
	d.lastNs = now
	d.pending = position{}
	d.have = false
	return true
}

// take returns the queued move if one is ready to send, clearing the slot.
func (d *dragState) take() (position, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.have {
		return position{}, false
	}
	if timeNowUnixNano()-d.lastNs < moveCoalesceNs {
		return position{}, false // not enough time has passed; keep waiting
	}
	pos := d.pending
	d.pending = position{}
	d.have = false
	d.lastNs = timeNowUnixNano()
	return pos, true
}

func (a *app) sendTouchMove(pos position) {
	if a.ctrl == nil {
		return
	}
	_ = a.ctrl.touchMove(pos)
}

func (a *app) coalescedMove(pos position) {
	if a.drag.queue(pos) {
		a.sendTouchMove(pos)
	}
}

// flushPendingMove sends the latest coalesced move (from the tick loop).
func (a *app) flushPendingMove() {
	pos, ok := a.drag.take()
	if !ok {
		return
	}
	if a.drag.isDown() {
		a.sendTouchMove(pos)
	}
}
