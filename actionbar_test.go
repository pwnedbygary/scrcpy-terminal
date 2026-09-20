package main

import (
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The TUI action bar: layout, click routing and auto-hide. The bar is driven
// by mouse activity, so the tests feed it the same SGR events the terminal
// would and assert on the wire bytes and on the painted overlay.
// ---------------------------------------------------------------------------

func withTermSize(t *testing.T, cols, rows int) {
	t.Helper()
	old := termSizeOverride
	t.Cleanup(func() { termSizeOverride = old })
	termSizeOverride = struct{ cols, rows int }{cols, rows}
}

// mouseSeq builds an SGR press/release/motion sequence.
func mouseSeq(btn, x, y int, press bool) []byte {
	end := "m"
	if press {
		end = "M"
	}
	return []byte("\x1b[<" + strconv.Itoa(btn) + ";" + strconv.Itoa(x) + ";" + strconv.Itoa(y) + end)
}

// TestBarHiddenUntilMouseMoves: the bar does not exist until there is mouse
// activity, and then it hides itself after the idle timeout.
func TestBarHiddenUntilMouseMoves(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(nil)
	a.tui = &tui{cols: 120, rows: 29}

	if a.barVisible() {
		t.Fatal("bar visible before any mouse activity")
	}
	a.wakeBar()
	if !a.barVisible() {
		t.Fatal("bar did not wake on motion")
	}
	a.barHideCheck(timeNowUnixNano() + int64(barIdle) + 1)
	if a.barVisible() {
		t.Fatal("bar stayed past its idle timeout")
	}
}

// TestBarLayoutFitsAndScrolls: the bar never draws past the pane, exposes a ›
// when items remain, and a click on it scrolls the window.
func TestBarLayoutFitsAndScrolls(t *testing.T) {
	withTermSize(t, 80, 24)
	a := newTestApp(nil)
	a.tui = &tui{cols: 80, rows: 23}
	a.wakeBar()

	text, hits, left, right := a.barLayout(80)
	if len([]rune(text)) > 80 {
		t.Fatalf("bar is %d cells wide in an 80-cell pane: %q", len([]rune(text)), text)
	}
	if left || !right {
		t.Fatalf("at offset 0 want left=false right=true, got left=%v right=%v", left, right)
	}
	// The right arrow is clickable and scrolls.
	var arrow barHit
	for _, h := range hits {
		if h.scroll == +1 {
			arrow = h
		}
	}
	if arrow.to == 0 {
		t.Fatal("no right-scroll hit found")
	}
	if !a.barClick(arrow.from+1, 23, 24, true) {
		t.Fatal("click on the scroll arrow was not consumed")
	}
	if a.barOffset == 0 {
		t.Fatal("scroll arrow did not move the offset")
	}
}

// TestBarClickRunsTheButton: clicking Back sends BACK, exactly like the F-key
// and the menu row; the click never becomes a device tap and the button is
// flashed.
func TestBarClickRunsTheButton(t *testing.T) {
	withTermSize(t, 120, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	// Second hit is Back (index 1); clicking it must send Back's wire op. Back
	// is a control message (backOrScreenOn), not a keycode.
	back := a.barHits[1]
	if barItemAt(back).Short != "Back" {
		t.Fatalf("second button is %q, want Back", barItemAt(back).Short)
	}
	mid := (back.from + back.to) / 2
	if !a.barClick(mid+1, 29, 30, true) {
		t.Fatal("click on Back was not consumed")
	}
	if len(cc.captured) != 2 ||
		cc.captured[0][0] != ctrlBackOrScreenOn || cc.captured[1][0] != ctrlBackOrScreenOn {
		t.Fatalf("Back click wrote %d messages, want a %#x down+up pair",
			len(cc.captured), ctrlBackOrScreenOn)
	}
	if a.barFlashIdx != 1 {
		t.Errorf("clicked button not flashed: idx=%d, want 1", a.barFlashIdx)
	}
	// The repaint must paint the clicked pill with the flash style, so the
	// click is visible on screen and not just in a field.
	flashed := false
	for _, seg := range a.tui.overlay[28].segs {
		if seg.from == back.from && seg.to == back.to && seg.code == btnBgFlash {
			flashed = true
		}
	}
	if !flashed {
		t.Error("clicked button was not painted with the flash style")
	}
}

// TestBarClickOnStripDoesNotTapTheDevice: a click on the bar row outside any
// button must be swallowed, not forwarded as a touch.
func TestBarClickOnStripDoesNotTapTheDevice(t *testing.T) {
	withTermSize(t, 120, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	// A column past the last button on the bar row.
	last := a.barHits[len(a.barHits)-1]
	a.barClick(last.to+5, 29, 30, true)
	if len(cc.captured) != 0 {
		t.Fatalf("a click on the empty strip reached the device: %d messages", len(cc.captured))
	}
}

// TestBarClickBelowTheBarTapsThrough: the row under the bar is the video, so
// clicking there is a normal device tap.
func TestBarClickBelowTheBarTapsThrough(t *testing.T) {
	withTermSize(t, 120, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	if a.barClick(10, 28, 30, true) { // row 28 is video; bar is row 29
		t.Fatal("click above the bar was consumed by it")
	}
}

// TestBarReleaseAfterVideoDragStillReachesTheDevice: a drag that starts on the
// video and ends over the bar must still deliver its touch-release; only a
// release with no device touch in flight is swallowed.
func TestBarReleaseAfterVideoDragStillReachesTheDevice(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(newController(&capConn{}))
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()

	a.drag.setDown(true)
	if a.barClick(10, 29, 30, false) {
		t.Fatal("release after a video drag was swallowed by the bar")
	}
	a.drag.setDown(false)
	if !a.barClick(10, 29, 30, false) {
		t.Fatal("release without a device touch should be swallowed on the bar row")
	}
}

// TestBarMotionWakesViaMouseEvent: the SGR motion sequence itself wakes the
// bar, which is what on-screen auto-hide is built around.
func TestBarMotionWakesViaMouseEvent(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(newController(&capConn{}))
	a.tui = &tui{cols: 120, rows: 29}

	a.mouseEvent(mouseSeq(35, 40, 10, true)) // motion, no button
	if !a.barVisible() {
		t.Fatal("mouse motion did not wake the bar")
	}
}

// TestBarOverlayOnlyOnItsRow: the bar paints one line, the last video row, and
// clears the recorded hits when it hides.
func TestBarOverlayOnlyOnItsRow(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(nil)
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	ov := a.tui.overlay
	if len(ov) != 30 {
		t.Fatalf("overlay has %d lines, want one per terminal row", len(ov))
	}
	if len(ov[28].text) == 0 {
		t.Fatal("bar row 28 (0-based) is empty")
	}
	for i, line := range ov {
		if i != 28 && line.text != "" {
			t.Errorf("overlay line %d painted while only the bar should be: %q", i, line.text)
		}
	}
	if !strings.Contains(ov[28].text, "Home") {
		t.Errorf("bar text does not contain Home: %q", ov[28].text)
	}
	if len(ov[28].segs) == 0 {
		t.Error("bar has no button segments (would render as plain text)")
	}

	a.barHideCheck(timeNowUnixNano() + int64(barIdle) + 1)
	if a.barHits != nil {
		t.Error("bar hits survived the hide: a click could still run a button")
	}
}

// TestBarHiddenBehindOverlays: opening the menu or the keyboard hides the bar,
// so the overlays can never share the screen.
func TestBarHiddenBehindOverlays(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(nil)
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	if !a.barVisible() {
		t.Fatal("bar did not wake")
	}
	a.toggleMenu()
	if a.barVisible() {
		t.Error("bar still visible with the menu open")
	}
	a.closeMenu()
	a.openKeyboard()
	if a.barVisible() {
		t.Error("bar still visible with the keyboard open")
	}
}

// TestBarFlashClears: the green flash on a clicked button does not linger
// after its short duration.
func TestBarFlashClears(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(nil)
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()
	a.barFlashIdx = 2
	a.barFlashAt = timeNowUnixNano()
	a.barHideCheck(a.barFlashAt + int64(barFlashDuration) + 1)
	if a.barFlashIdx != -1 {
		t.Errorf("flash survived its duration: idx=%d", a.barFlashIdx)
	}
}

// TestResizeRerecordsMenuHits: shrinking the terminal while the action menu is
// open must re-record its click rows. The old geometry could point past the
// new paintable area and let a click on the status row run Quit invisibly.
func TestResizeRerecordsMenuHits(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(nil)
	a.tui = &tui{cols: 120, rows: 29}
	a.toggleMenu()
	if _, ok := a.menuHitTest(5, 5, 30); !ok {
		t.Fatal("menu row not clickable before the resize")
	}

	termSizeOverride = struct{ cols, rows int }{80, 18}
	a.onResize()
	if _, ok := a.menuHitTest(5, 18, 18); ok {
		t.Error("click on the status row hit a stale menu row after resize")
	}
	if _, ok := a.menuHitTest(5, 17, 18); !ok {
		t.Error("the last paintable menu row is no longer clickable after resize")
	}
}

// TestBarWheelAndRightClickAreNotButtons: the bar owns left clicks only.
// Wheel must scroll and right must go Back even when they land on a pill; the
// bar must not swallow them or run the button under the pointer.
func TestBarWheelAndRightClickAreNotButtons(t *testing.T) {
	withTermSize(t, 120, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	home := a.barHits[0]
	mid := (home.from+home.to)/2 + 1

	// Wheel up over the Home pill: a scroll message, not HOME keycodes.
	a.handleInput(mouseSeq(64, mid, 29, true))
	if len(cc.captured) == 0 {
		t.Fatal("wheel over the bar produced no control message")
	}
	if cc.captured[0][0] != ctrlInjectScroll {
		t.Errorf("wheel over the bar sent message type %#x, want scroll %#x",
			cc.captured[0][0], ctrlInjectScroll)
	}

	// Right click over the Home pill: Back down+up, not Home.
	cc.captured = nil
	a.handleInput(mouseSeq(2, mid, 29, true))
	if len(cc.captured) != 2 ||
		cc.captured[0][0] != ctrlBackOrScreenOn || cc.captured[1][0] != ctrlBackOrScreenOn {
		t.Errorf("right click over the bar wrote %d messages (first %#x), want a %#x pair",
			len(cc.captured), firstByte(cc.captured), ctrlBackOrScreenOn)
	}

	// And a left click still runs the button.
	cc.captured = nil
	a.handleInput(mouseSeq(0, mid, 29, true))
	if len(cc.captured) != 2 {
		t.Fatalf("left click over the bar wrote %d messages, want down+up", len(cc.captured))
	}
	if _, code, _ := keycoded(t, cc.captured[0]); code != 3 {
		t.Errorf("left click sent keycode %d, want HOME (3)", code)
	}
}

func firstByte(msgs [][]byte) byte {
	if len(msgs) == 0 {
		return 0
	}
	return msgs[0][0]
}

// TestBarSnapsBackWhenItFits: after widening the pane, a stale scroll offset
// must not pin a needless ‹ on screen.
func TestBarSnapsBackWhenItFits(t *testing.T) {
	withTermSize(t, 80, 24)
	a := newTestApp(nil)
	a.tui = &tui{cols: 80, rows: 23}
	a.wakeBar()
	a.barOffset = 8
	_ = a.barLine() // paints at the narrow width, offset stays (items remain)
	if a.barOffset != 8 {
		t.Fatalf("offset changed while items remain: %d", a.barOffset)
	}

	// Widen so everything fits at offset 0.
	termSizeOverride = struct{ cols, rows int }{300, 24}
	line := a.barLine()
	if line == nil {
		t.Fatal("bar did not render at the wide width")
	}
	if a.barOffset != 0 {
		t.Errorf("offset did not snap back on a wide pane: %d", a.barOffset)
	}
	if strings.Contains(line.text, "‹") {
		t.Errorf("bar still shows a left arrow after snapping back: %q", line.text)
	}
}

// TestBarMuteHasNoChordButWorks: Mute has no Alt chord on purpose, and the bar
// is the path that reaches it (keycode 91, MUTE).
func TestBarMuteHasNoChordButWorks(t *testing.T) {
	withTermSize(t, 200, 30) // wide enough that Mute is on screen
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 200, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	for _, h := range a.barHits {
		if h.scroll == 0 && barItemAt(h).Short == "Mute" {
			a.barClick((h.from+h.to)/2+1, 29, 30, true)
			if len(cc.captured) != 2 {
				t.Fatalf("Mute wrote %d messages, want down+up", len(cc.captured))
			}
			_, code, _ := keycoded(t, cc.captured[0])
			if code != 91 {
				t.Errorf("Mute sent keycode %d, want 91", code)
			}
			return
		}
	}
	t.Fatalf("Mute button not found in a 200-cell bar: %q", a.tui.overlay[28].text)
}
