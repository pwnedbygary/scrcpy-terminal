package main

import (
	"encoding/binary"
	"strconv"
	"testing"
)

// ---------------------------------------------------------------------------
// The mnemonic chords (Alt+<letter>) are the primary bindings for a phone
// keyboard, so they get their own wire-level tests: the same actions the
// F-keys/README promise, reached without an F-row.
// ---------------------------------------------------------------------------

// keycoded decodes a ctrlInjectKeycode write into (action, keycode, metastate).
func keycoded(t *testing.T, w []byte) (action byte, keycode, meta uint32) {
	t.Helper()
	if len(w) != 14 || w[0] != ctrlInjectKeycode {
		t.Fatalf("not a keycode message: % x", w)
	}
	return w[1],
		binary.BigEndian.Uint32(w[2:6]),
		binary.BigEndian.Uint32(w[10:14])
}

func newTestApp(ctrl *controller) *app {
	return &app{
		events:  make(chan inputEvent, 64),
		kb:      newKeyboard(),
		ctrl:    ctrl,
		grabbed: true, // mouse reporting on, as it is by default outside Zellij
	}
}

// TestMnemonicKeyWire: Alt+<letter> resolves to the same Android keycode the
// corresponding F-key sends. (F1 home = keycode 3.)
func TestMnemonicKeyWire(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.handleInput([]byte{0x1b, 'h'}) // Alt+H = home

	if len(cc.captured) != 2 {
		t.Fatalf("want down+up writes on Alt+H, got %d", len(cc.captured))
	}
	downAction, downCode, _ := keycoded(t, cc.captured[0])
	upAction, upCode, _ := keycoded(t, cc.captured[1])
	if downCode != 3 || upCode != 3 {
		t.Errorf("Alt+H sent keycode %d/%d, want HOME (3)", downCode, upCode)
	}
	if downAction == upAction {
		t.Errorf("down and up have the same action byte (%d)", downAction)
	}
}

// TestMnemonicUppercaseIsTheSameChord: a shifted letter must not fall through
// to text or a different action.
func TestMnemonicUppercaseIsTheSameChord(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.handleInput([]byte{0x1b, 'H'})

	if len(cc.captured) != 2 {
		t.Fatalf("Alt+Shift+H wrote %d messages, want 2", len(cc.captured))
	}
	if _, code, _ := keycoded(t, cc.captured[0]); code != 3 {
		t.Errorf("Alt+Shift+H sent keycode %d, want HOME (3)", code)
	}
}

// TestMnemonicActionWire covers the chords that are control messages rather
// than keycodes: back, rotate and keyframe.
func TestMnemonicActionWire(t *testing.T) {
	cases := []struct {
		chord byte
		mtype byte
	}{
		{'b', ctrlBackOrScreenOn},
		{'r', ctrlRotateDevice},
		{'k', ctrlResetVideo},
	}
	for _, tc := range cases {
		cc := &capConn{}
		a := newTestApp(newController(cc))
		a.handleInput([]byte{0x1b, tc.chord})
		if len(cc.captured) == 0 {
			t.Errorf("Alt+%c wrote nothing", tc.chord)
			continue
		}
		if got := cc.captured[0][0]; got != tc.mtype {
			t.Errorf("Alt+%c first message type %d, want %d", tc.chord, got, tc.mtype)
		}
	}
}

// TestMnemonicLocalOps: screenshot/keyboard/quit are local. They must not
// touch the control socket, and quit must reach the run loop as evQuit.
func TestMnemonicLocalOps(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 80, rows: 24} // screenshot needs a TUI to inspect (and write nothing without a frame)
	a.handleInput([]byte{0x1b, 's'}) // Alt+S: screenshot, local
	if len(cc.captured) != 0 {
		t.Errorf("Alt+S wrote %d control messages, want 0", len(cc.captured))
	}

	a.handleInput([]byte{0x1b, 'i'}) // Alt+I: toggle software keyboard
	if !a.kb.open {
		t.Error("Alt+I did not open the software keyboard")
	}
	if len(cc.captured) != 0 {
		t.Errorf("Alt+I wrote %d control messages, want 0", len(cc.captured))
	}

	// Alt+Q in its own app: the previous Alt+I left the keyboard open, and
	// the keyboard overlay would otherwise consume the chord.
	q := newTestApp(newController(&capConn{}))
	q.handleInput([]byte{0x1b, 'q'}) // Alt+Q: quit
	select {
	case ev := <-q.events:
		if ev.kind != evQuit {
			t.Errorf("Alt+Q sent event kind %d, want evQuit", ev.kind)
		}
	default:
		t.Error("Alt+Q did not queue evQuit")
	}
}

// TestMnemonicMenuOpensNotMenuKey: Alt+/ opens the action menu; it must not be
// confused with the Android Menu key (which is Alt+O... which is not a chord:
// F2 and a menu row carry it).
func TestMnemonicMenuOpensNotMenuKey(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 80, rows: 24}

	a.handleInput([]byte{0x1b, '/'})
	if !a.menuOpen {
		t.Fatal("Alt+/ did not open the action menu")
	}
	if len(cc.captured) != 0 {
		t.Errorf("opening the menu wrote %d control messages, want 0", len(cc.captured))
	}

	// Alt+O is not a chord: it types an "o" into the focused app, never the
	// Menu key.
	a.handleInput([]byte{0x1b, 'o'})
	if len(cc.captured) != 0 {
		t.Errorf("Alt+O wrote %d control messages, want 0", len(cc.captured))
	}
}

// ---------------------------------------------------------------------------
// The action menu itself: modal key routing and click-to-run.
// ---------------------------------------------------------------------------

// TestMenuModalRouting: while open, a row letter runs that action; Esc closes
// without running anything; other keys close and are swallowed.
func TestMenuModalRouting(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 80, rows: 24}

	a.toggleMenu()
	if !a.menuOpen {
		t.Fatal("menu did not open")
	}

	// 'h' runs Home while the menu is open, then closes it.
	a.handleInput([]byte{'h'})
	if a.menuOpen {
		t.Error("menu stayed open after a row letter")
	}
	if len(cc.captured) != 2 {
		t.Fatalf("row 'h' wrote %d messages, want down+up", len(cc.captured))
	}
	if _, code, _ := keycoded(t, cc.captured[0]); code != 3 {
		t.Errorf("row 'h' sent keycode %d, want HOME (3)", code)
	}

	// Esc closes and runs nothing.
	cc.captured = nil
	a.toggleMenu()
	a.handleInput([]byte{0x1b})
	if a.menuOpen {
		t.Error("Esc did not close the menu")
	}
	if len(cc.captured) != 0 {
		t.Errorf("Esc wrote %d control messages, want 0", len(cc.captured))
	}

	// A key that is not a row letter closes the menu and is not typed.
	cc.captured = nil
	a.toggleMenu()
	a.handleInput([]byte{'z'})
	if a.menuOpen {
		t.Error("stray key did not close the menu")
	}
	if len(cc.captured) != 0 {
		t.Errorf("stray key wrote %d control messages, want 0", len(cc.captured))
	}
}

// TestMenuClickRunsTheRow: a left click on a menu row runs that row, using the
// same recorded layout the painter uses.
func TestMenuClickRunsTheRow(t *testing.T) {
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 80, rows: 30}

	a.toggleMenu()
	if len(a.menuHits) == 0 {
		t.Fatal("menu recorded no clickable rows")
	}
	row := a.menuHits[0] // first row: Home
	// SGR mouse press on that row, column 5.
	seq := []byte("\x1b[<0;" + strconv.Itoa(5) + ";" + strconv.Itoa(row.row) + "M")
	a.handleInput(seq)

	if a.menuOpen {
		t.Error("clicking a row did not close the menu")
	}
	if len(cc.captured) != 2 {
		t.Fatalf("click wrote %d messages, want down+up", len(cc.captured))
	}
	if _, code, _ := keycoded(t, cc.captured[0]); code != 3 {
		t.Errorf("click on the Home row sent keycode %d, want 3", code)
	}
}

// TestMenuNeverCoversTheStatusBar: the last terminal line is the status bar
// (tui rows = height-1), so no clickable row may land on it. At 18 rows the
// row at that line would be Quit, and a click there used to run it invisibly.
// This exercises the unrecorded-layout fallback, whose bound must match
// refreshMenu's.
func TestMenuNeverCoversTheStatusBar(t *testing.T) {
	a := newTestApp(nil)
	a.tui = &tui{cols: 80, rows: 17} // visible overlay rows 1..17, status at 18

	const rows = 18
	last := rows - 1 // last paintable line: 17
	if _, ok := a.menuHitTest(5, last, rows); !ok {
		t.Errorf("the last paintable row (%d) should be clickable", last)
	}
	if _, ok := a.menuHitTest(5, rows, rows); ok {
		t.Errorf("row %d is the status bar and must not run an action", rows)
	}
	if _, ok := a.menuHitTest(5, rows+1, rows); ok {
		t.Errorf("row %d is past the terminal and must not run an action", rows+1)
	}
}

// TestMenuRefreshSkipsTheStatusRow: refreshMenu records only rows the painter
// draws. At 18 terminal rows the status bar is row 18 and only 15 of the 16
// items fit above it (lines 0..16, items from line 2); the 16th item would be
// line 17 = status row. The bound is exercised here rather than at the default
// 24 rows, where every item fits and the bound never truncates.
func TestMenuRefreshSkipsTheStatusRow(t *testing.T) {
	old := termSizeOverride
	t.Cleanup(func() { termSizeOverride = old })
	termSizeOverride = struct{ cols, rows int }{80, 18}

	a := newTestApp(nil)
	a.tui = &tui{cols: 80, rows: 17}
	a.toggleMenu()

	if len(a.menuHits) != 15 {
		t.Fatalf("recorded %d rows, want 15 (only lines 0..16 are paintable)", len(a.menuHits))
	}
	for _, h := range a.menuHits {
		if h.row >= 18 {
			t.Errorf("recorded row %d, which is the status bar or past the screen", h.row)
		}
	}
	if last := a.menuHits[len(a.menuHits)-1]; last.row != 17 {
		t.Errorf("last clickable row %d, want 17 (row 18 is the status bar)", last.row)
	}
}

// TestMenuAndKeyboardAreExclusive: opening one closes the other, so two
// overlays can never share the screen or fight over the keyboard.
func TestMenuAndKeyboardAreExclusive(t *testing.T) {
	a := newTestApp(nil)
	a.tui = &tui{cols: 80, rows: 24}

	a.openKeyboard()
	if !a.kb.open {
		t.Fatal("keyboard did not open")
	}
	a.toggleMenu()
	if a.kb.open {
		t.Error("opening the menu left the keyboard open")
	}
	if !a.menuOpen {
		t.Fatal("menu did not open")
	}
	a.toggleKeyboard()
	if a.menuOpen {
		t.Error("opening the keyboard left the menu open")
	}
}
