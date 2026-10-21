package main

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The TUI software keyboard: what each key sends, where the cursor starts,
// that every row fits a standard terminal, and how clicks are routed.
// ---------------------------------------------------------------------------

// TestKeyboardKeysSendTheirAndroidKeycodes checks every key that types or
// presses something against the AKEYCODE_* names generated from scrcpy's
// android/keycodes.h (keycodes_gen.go), never against the layout itself.
func TestKeyboardKeysSendTheirAndroidKeycodes(t *testing.T) {
	names := map[string]string{
		",": "COMMA", ".": "PERIOD", "/": "SLASH", ";": "SEMICOLON", "'": "APOSTROPHE",
		"[": "LEFT_BRACKET", "]": "RIGHT_BRACKET", "-": "MINUS", "=": "EQUALS", "\\": "BACKSLASH",
		"SPACE": "SPACE", "TAB": "TAB", "BCK": "DEL", "ENT": "ENTER",
		"HOME": "HOME", "BACK": "BACK", "MENU": "MENU", "REC": "APP_SWITCH", "PWR": "POWER",
		"UP": "DPAD_UP", "DN": "DPAD_DOWN", "LT": "DPAD_LEFT", "RT": "DPAD_RIGHT", "OK": "DPAD_CENTER",
		"VOL+": "VOLUME_UP", "VOL-": "VOLUME_DOWN", "MUTE": "VOLUME_MUTE",
	}
	for _, row := range kbLayout() {
		for _, key := range row {
			if key.kind != kbText {
				continue
			}
			want, ok := names[key.label]
			if !ok && len(key.label) == 1 {
				want = key.label // AKEYCODE_A..Z and AKEYCODE_0..9 are named by their character
			}
			if want == "" {
				t.Errorf("key %q has no expected keycode name in this test", key.label)
				continue
			}
			if got := androidKeycodes[int(key.key)]; got != want {
				t.Errorf("key %q sends keycode %d (%s), want %s", key.label, key.key, got, want)
			}
		}
	}
}

// TestKeyboardRowsFitEightyColumns: a key past the right edge cannot be seen
// or clicked.
func TestKeyboardRowsFitEightyColumns(t *testing.T) {
	k := newKeyboard()
	for ri, row := range k.rows {
		last := row.cells[len(row.cells)-1]
		if end := last.col + last.width; end > 80 {
			t.Errorf("row %d ends at column %d (last key %q)", ri, end, last.key.label)
		}
	}
}

// TestKeyboardEnterStillMeansEnter: the cursor starts on ENT, and is drawn, so
// pressing Enter right after opening the keyboard does not rotate the device.
func TestKeyboardEnterStillMeansEnter(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.kb.open = true

	line := a.kb.renderRow(a.kb.curRow)
	cursorDrawn := false
	for _, s := range line.segs {
		if s.code == btnBgCursor {
			cursorDrawn = true
		}
	}
	if !cursorDrawn {
		t.Error("the keyboard cursor is not drawn")
	}

	a.handleInput([]byte{0x0d})
	if len(cc.captured) != 2 {
		t.Fatalf("Enter sent %d messages, want a key down and up", len(cc.captured))
	}
	if _, code, _ := keycoded(t, cc.captured[0]); code != 66 {
		t.Errorf("Enter pressed keycode %d, want ENTER (66)", code)
	}
}

// kbKeyCell returns the 1-based terminal cell at the middle of the key with
// this label.
func kbKeyCell(t *testing.T, k *keyboard, label string, termRows int) (x, y int) {
	t.Helper()
	for ri, row := range k.rows {
		for _, c := range row.cells {
			if c.key.label == label {
				return c.col + c.width/2 + 1, k.top(termRows) + (len(k.rows) - 1 - ri) + 1
			}
		}
	}
	t.Fatalf("no key %q", label)
	return 0, 0
}

// TestKeyboardClickPressesOnceAndFlashes: one left click is one press, the
// key stays green after it is pressed, and the green goes on the tick after
// its duration.
func TestKeyboardClickPressesOnceAndFlashes(t *testing.T) {
	withTermSize(t, 80, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.kb.open = true
	if a.kb.flashRow != -1 || a.kb.flashAt != 0 {
		t.Fatalf("a new keyboard shows a flash at row %d", a.kb.flashRow)
	}

	x, y := kbKeyCell(t, a.kb, "K", 30)
	a.handleInput(mouseSeq(0, x, y, true))
	a.handleInput(mouseSeq(0, x, y, false))
	if len(cc.captured) != 2 {
		t.Fatalf("one click sent %d messages, want a key down and up", len(cc.captured))
	}
	if _, code, _ := keycoded(t, cc.captured[0]); code != 39 {
		t.Errorf("K pressed keycode %d, want K (39)", code)
	}
	if a.kb.flashAt == 0 {
		t.Fatal("the pressed key is not flashed")
	}
	a.kbTick(a.kb.flashAt + int64(kbFlashDuration) - 1)
	if a.kb.flashAt == 0 {
		t.Error("the flash cleared before its duration")
	}
	a.kbTick(a.kb.flashAt + int64(kbFlashDuration))
	if a.kb.flashAt != 0 || a.kb.flashRow != -1 {
		t.Error("the flash survived its duration")
	}
}

// TestKeyboardOnlyALeftPressActs: drag motion and the wheel carry the same low
// button bits as a left press and must not press keys; a click between two
// keys is the keyboard's, not a tap on the device behind it.
func TestKeyboardOnlyALeftPressActs(t *testing.T) {
	withTermSize(t, 80, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.kb.open = true

	x, y := kbKeyCell(t, a.kb, "K", 30)
	a.handleInput(mouseSeq(32, x, y, true)) // motion with the left button held
	a.handleInput(mouseSeq(64, x, y, true)) // wheel up
	a.handleInput(mouseSeq(65, x, y, true)) // wheel down
	if len(cc.captured) != 0 {
		t.Fatalf("motion and wheel over a key sent %d messages", len(cc.captured))
	}

	space := a.kb.rows[0].cells[0]
	gapX := space.col + space.width + 1 // first of the two cells after SPACE
	_, gapY := kbKeyCell(t, a.kb, "SPACE", 30)
	if _, ok := a.kb.hitTest(gapX, gapY, 30); ok {
		t.Fatalf("cell %d,%d is a key, not a gap", gapX, gapY)
	}
	a.handleInput(mouseSeq(0, gapX, gapY, true))
	a.handleInput(mouseSeq(0, gapX, gapY, false))
	if len(cc.captured) != 0 {
		t.Errorf("a click between two keys reached the device: %d messages", len(cc.captured))
	}
}

// TestPanicOnAGoroutineRestoresTheTerminal: a panic on one of the terminal
// UI's goroutines gives the terminal back first and still ends in that panic;
// without a panic nothing is restored early.
func TestPanicOnAGoroutineRestoresTheTerminal(t *testing.T) {
	restored := 0
	a := &app{restoreTerm: func() { restored++ }}

	func() {
		defer a.restoreTerminalOnPanic()
	}()
	if restored != 0 {
		t.Fatalf("restored %d times without a panic", restored)
	}

	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("the panic did not carry on: recovered %v", r)
			}
		}()
		defer a.restoreTerminalOnPanic()
		panic("boom")
	}()
	if restored != 1 {
		t.Errorf("restored %d times after a panic, want 1", restored)
	}
}

// TestKeyboardDragFromTheVideoEndsOnTheDevice: a finger put down on the video
// must be lifted on the device even when the pointer is released over a key.
func TestKeyboardDragFromTheVideoEndsOnTheDevice(t *testing.T) {
	withTermSize(t, 80, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.kb.open = true
	a.frameW, a.frameH = 1080, 1920

	a.handleInput(mouseSeq(0, 40, 5, true)) // down on the video
	x, y := kbKeyCell(t, a.kb, "K", 30)
	a.handleInput(mouseSeq(32, x, y, true)) // drag onto the keyboard
	a.handleInput(mouseSeq(0, x, y, false)) // release over a key
	if len(cc.captured) < 2 {
		t.Fatalf("the drag sent %d messages", len(cc.captured))
	}
	last := cc.captured[len(cc.captured)-1]
	if last[0] != ctrlInjectTouch || last[1] != 1 {
		t.Errorf("the drag did not end with a touch up on the device: % x", last)
	}
	for _, w := range cc.captured {
		if w[0] == ctrlInjectKeycode {
			t.Errorf("the drag pressed a key: % x", w)
		}
	}
}

// TestKeyboardClickAfterALostRelease: a drag on the video whose release never
// arrived leaves the device finger down. The next click on a key lifts that
// finger and then presses the key; it does not tap the device.
func TestKeyboardClickAfterALostRelease(t *testing.T) {
	withTermSize(t, 80, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.kb.open = true

	a.handleInput(mouseSeq(0, 40, 5, true)) // down on the video; the release is lost
	cc.captured = nil
	x, y := kbKeyCell(t, a.kb, "K", 30)
	a.handleInput(mouseSeq(0, x, y, true))
	a.handleInput(mouseSeq(0, x, y, false))
	if len(cc.captured) != 3 {
		t.Fatalf("the click sent % x, want a touch up, then K down and up", cc.captured)
	}
	// AMOTION_EVENT_ACTION_UP is 1.
	if up := cc.captured[0]; up[0] != ctrlInjectTouch || up[1] != 1 {
		t.Errorf("the first message is % x, want the touch up", up)
	}
	for _, w := range cc.captured[1:] {
		if _, code, _ := keycoded(t, w); code != 39 {
			t.Errorf("the click sent keycode %d, want K (39)", code)
		}
	}
	if a.drag.isDown() {
		t.Error("the device finger is still believed down")
	}
}

// TestOverlaysIgnoreButtonsEightToEleven: buttons 8 to 11 (the side buttons
// many mice have) report as 128 to 131. A press of one is not a left click, so
// it must not press a key or run a menu row.
func TestOverlaysIgnoreButtonsEightToEleven(t *testing.T) {
	withTermSize(t, 80, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.kb.open = true
	x, y := kbKeyCell(t, a.kb, "K", 30)
	a.handleInput(mouseSeq(128, x, y, true))
	a.handleInput(mouseSeq(128, x, y, false))
	if len(cc.captured) != 0 {
		t.Errorf("button 8 on a key sent % x", cc.captured)
	}

	mc := &capConn{}
	m := newTestApp(newController(mc))
	m.tui = &tui{cols: 80, rows: 29}
	m.toggleMenu()
	m.handleInput(mouseSeq(128, 5, menuRow(t, m, "Home"), true))
	if !m.menuOpen || len(mc.captured) != 0 {
		t.Errorf("button 8 on the Home row: menu open=%v, sent % x", m.menuOpen, mc.captured)
	}
}

// TestKeyboardQuitTakesTwoPresses: the first press on QUIT arms it and the key
// reads QUIT?; a second press within 2 s quits, and so does Enter on the key.
// Once the arm has run out, the next tick puts the label back and a press arms
// it again instead of quitting.
func TestKeyboardQuitTakesTwoPresses(t *testing.T) {
	withTermSize(t, 80, 30)
	a := newTestApp(newController(&capConn{}))
	a.tui = &tui{cols: 80, rows: 29}
	a.openKeyboard()
	x, y := kbKeyCell(t, a.kb, "QUIT", 30)
	click := func() {
		a.handleInput(mouseSeq(0, x, y, true))
		a.handleInput(mouseSeq(0, x, y, false))
	}
	row := func() string { return a.tui.overlay[y-1].text }

	click()
	if quitQueued(a) {
		t.Fatal("one press on QUIT quit")
	}
	if !strings.Contains(row(), "QUIT?") {
		t.Fatalf("armed QUIT is painted as %q, want QUIT?", row())
	}
	click()
	if !quitQueued(a) {
		t.Fatal("a second press at once did not quit")
	}

	click()
	a.kbTick(timeNowUnixNano() + int64(2100*time.Millisecond))
	if strings.Contains(row(), "QUIT?") {
		t.Errorf("QUIT? is still painted after the tick 2.1 s on: %q", row())
	}
	click()
	if quitQueued(a) {
		t.Fatal("a press after the arm ran out quit")
	}

	// That press armed it again; Enter on the focused QUIT is the second press.
	a.kb.curRow = len(a.kb.rows) - 1
	for ci, c := range a.kb.rows[a.kb.curRow].cells {
		if c.key.label == "QUIT" {
			a.kb.curCol = ci
		}
	}
	a.handleInput([]byte{0x0d})
	if !quitQueued(a) {
		t.Error("Enter on the armed QUIT did not quit")
	}
}
