package main

import (
	"io"
	"os"
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
	a.refreshOverlays()

	top, bottom := a.barLines()
	if top == nil || bottom == nil {
		t.Fatal("bar did not render")
	}
	if _, _, left, right := a.barLayout(80); left || !right {
		t.Fatalf("at offset 0 want left=false right=true, got left=%v right=%v", left, right)
	}
	if got := len([]rune(bottom.text)); got > 80 {
		t.Fatalf("bar is %d cells wide in an 80-cell pane: %q", got, bottom.text)
	}
	if got := len([]rune(top.text)); got != len([]rune(bottom.text)) {
		t.Fatalf("cap row is %d cells, label row is %d", got, len([]rune(bottom.text)))
	}
	// The right arrow is clickable and scrolls.
	var arrow barHit
	for _, h := range a.barHits {
		if h.scroll == +1 {
			arrow = h
		}
	}
	if arrow.to == 0 {
		t.Fatal("no right-scroll hit found")
	}
	if !a.barClick(arrow.from+1, a.barBotRow, true) {
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
	if !a.barClick(mid+1, a.barBotRow, true) {
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
	// click is visible on screen and not just in a field. The style is shared
	// by both rows.
	flashed := false
	for _, seg := range a.tui.overlay[a.barBotRow-1].segs {
		if seg.from == back.from && seg.to == back.to && seg.code == btnBgFlash {
			flashed = true
		}
	}
	if !flashed {
		t.Error("clicked button was not painted with the flash style")
	}
}

// TestBarClickOnStripDoesNotTapTheDevice: a click on the bar (either row)
// outside any button must be swallowed, not forwarded as a touch.
func TestBarClickOnStripDoesNotTapTheDevice(t *testing.T) {
	withTermSize(t, 120, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	// A column past the last button, on each of the bar's two rows.
	last := a.barHits[len(a.barHits)-1]
	a.barClick(last.to+5, a.barBotRow, true)
	a.barClick(last.to+5, a.barTopRow, true)
	if len(cc.captured) != 0 {
		t.Fatalf("a click on the empty strip reached the device: %d messages", len(cc.captured))
	}
}

// TestBarClickAboveTheBarTapsThrough: the rows above the bar are video, so
// clicking there is a normal device tap; the bar's own top row is consumed.
func TestBarClickAboveTheBarTapsThrough(t *testing.T) {
	withTermSize(t, 120, 30)
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	// A cell in the bar's leading margin (not on a pill) on the cap row.
	strip := a.barHits[0].from - 1
	if !a.barClick(strip, a.barTopRow, true) {
		t.Fatal("click on the bar's cap row was not consumed")
	}
	if len(cc.captured) != 0 {
		t.Fatalf("cap-row click reached the device: %d messages", len(cc.captured))
	}
	if a.barClick(10, a.barTopRow-1, true) {
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
	a.refreshOverlays()

	a.drag.setDown(true)
	if a.barClick(10, a.barBotRow, false) {
		t.Fatal("release after a video drag was swallowed by the bar")
	}
	a.drag.setDown(false)
	if !a.barClick(10, a.barBotRow, false) {
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

// TestBarOverlayOnlyOnItsRows: the bar paints two lines -- the cap and the
// label row, just above the status line -- and clears the recorded hits when
// it hides.
func TestBarOverlayOnlyOnItsRows(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(nil)
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	ov := a.tui.overlay
	if len(ov) != 30 {
		t.Fatalf("overlay has %d lines, want one per terminal row", len(ov))
	}
	topIdx, botIdx := a.barTopRow-1, a.barBotRow-1
	if topIdx != 27 || botIdx != 28 {
		t.Fatalf("bar rows are %d/%d, want 27/28 (above the status line)",
			a.barTopRow, a.barBotRow)
	}
	for i, line := range ov {
		if i != topIdx && i != botIdx && line.text != "" {
			t.Errorf("overlay line %d painted while only the bar should be: %q", i, line.text)
		}
	}
	if !strings.Contains(ov[botIdx].text, "Home") {
		t.Errorf("bar text does not contain Home: %q", ov[botIdx].text)
	}
	if len(ov[botIdx].segs) == 0 || len(ov[topIdx].segs) == 0 {
		t.Error("bar rows have no button segments (would render as plain text)")
	}
	if strings.TrimSpace(ov[topIdx].text) != "" {
		t.Errorf("cap row should be background only, got %q", ov[topIdx].text)
	}

	a.barHideCheck(timeNowUnixNano() + int64(barIdle) + 1)
	if a.barHits != nil {
		t.Error("bar hits survived the hide: a click could still run a button")
	}
	if a.barTopRow != 0 || a.barBotRow != 0 {
		t.Errorf("bar rows survived the hide: %d/%d", a.barTopRow, a.barBotRow)
	}
}

// TestBarIsCentered: the strip is centered in the pane, not left-justified.
func TestBarIsCentered(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(nil)
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	if len(a.barHits) == 0 {
		t.Fatal("bar painted no buttons")
	}
	// Centered means the empty space left of the first pill and right of the
	// last one are the same, and the strip touches neither edge.
	left := a.barHits[0].from
	right := 120 - a.barHits[len(a.barHits)-1].to
	if left == 0 {
		t.Fatal("bar is not centered: it starts at the left edge")
	}
	if right == 0 {
		t.Fatal("bar is not centered: it runs to the right edge")
	}
	if diff := left - right; diff < -1 || diff > 1 {
		t.Errorf("bar is off-center: %d cells left, %d right", left, right)
	}
}

// TestBarStaysWhilePointerRestsOnIt: a pointer resting on a pill keeps the bar
// up, so the button cannot vanish under the cursor and turn the next click
// into a device tap.
func TestBarStaysWhilePointerRestsOnIt(t *testing.T) {
	withTermSize(t, 120, 30)
	a := newTestApp(nil)
	a.tui = &tui{cols: 120, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	a.barPointerY = a.barBotRow
	now := timeNowUnixNano()
	a.barHideCheck(now + int64(barIdle)*10)
	if !a.barVisible() {
		t.Fatal("bar hid while the pointer was resting on it")
	}
	if a.barLastActivity != now+int64(barIdle)*10 {
		t.Errorf("idle clock not refreshed while resting on the bar: %d", a.barLastActivity)
	}

	a.barPointerY = a.barTopRow - 1 // moved off onto the video
	a.barHideCheck(now + int64(barIdle)*20)
	if a.barVisible() {
		t.Fatal("bar did not hide after the pointer moved off")
	}
	if a.barPointerY != 0 {
		t.Errorf("stale pointer row survived the hide: %d", a.barPointerY)
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
	if _, bot := a.barLines(); bot == nil { // paints at the narrow width
		t.Fatal("bar did not render at the narrow width")
	}
	if a.barOffset != 8 {
		t.Fatalf("offset changed while items remain: %d", a.barOffset)
	}

	// Widen so everything fits at offset 0.
	termSizeOverride = struct{ cols, rows int }{300, 24}
	_, bot := a.barLines()
	if bot == nil {
		t.Fatal("bar did not render at the wide width")
	}
	if a.barOffset != 0 {
		t.Errorf("offset did not snap back on a wide pane: %d", a.barOffset)
	}
	if strings.Contains(bot.text, "‹") {
		t.Errorf("bar still shows a left arrow after snapping back: %q", bot.text)
	}
}

// TestRepaintWithoutAFrame: an overlay change must reach the screen on its
// own, not wait for the next video frame. A static device screen can send no
// frames at all, and before repaint() the action bar (and the keyboard) could
// stay invisible -- or a hidden bar stay on screen -- until the device moved.
func TestRepaintWithoutAFrame(t *testing.T) {
	const tw, th = 40, 5
	tr := &tui{cols: tw, rows: th, running: true, lastW: tw, lastH: th * 2}
	tr.keys = make([]uint64, tw*th)
	tr.prev = make([]uint64, tw*th)
	for i := range tr.prev {
		tr.prev[i] = ^uint64(0)
	}
	tr.lastRGB = make([]byte, tw*th*2*4) // cols x 2*rows RGBA
	tr.setOverlay([]overlayLine{{text: "Hi"}})

	out := captureStdout(t, tr.repaint)
	if !strings.Contains(out, "Hi") {
		t.Fatalf("repaint did not paint the overlay; output %q", out)
	}
}

// TestRepaintRejectsAStaleCanvas: a resize can keep the same cell count
// (120x29 and 116x30 are both 3480 cells), so the old snapshot has the right
// length but the wrong shape. repaint must not shear it into the new grid.
func TestRepaintRejectsAStaleCanvas(t *testing.T) {
	const tw, th = 120, 29
	tr := &tui{cols: tw, rows: th, running: true, lastW: tw, lastH: th * 2}
	tr.keys = make([]uint64, tw*th)
	tr.prev = make([]uint64, tw*th)
	tr.lastRGB = make([]byte, tw*th*2*4)

	// Same cell count, different shape.
	tr.cols, tr.rows = 116, 30
	if len(tr.lastRGB) != tr.cols*tr.rows*2*4 {
		t.Fatalf("test setup is wrong: lengths differ (%d vs %d)",
			len(tr.lastRGB), tr.cols*tr.rows*2*4)
	}
	out := captureStdout(t, tr.repaint)
	if len(out) != 0 {
		t.Fatalf("repaint drew a stale canvas: %q", out)
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what
// it wrote. Tests are not parallel in this package, so the swap is safe.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

// TestBarMuteHasNoChordButWorks: Mute has no Alt chord on purpose, and the bar
// is the path that reaches it (keycode 164, VOLUME_MUTE).
func TestBarMuteHasNoChordButWorks(t *testing.T) {
	withTermSize(t, 200, 30) // wide enough that Mute is on screen
	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.tui = &tui{cols: 200, rows: 29}
	a.wakeBar()
	a.refreshOverlays()

	for _, h := range a.barHits {
		if h.scroll == 0 && barItemAt(h).Short == "Mute" {
			a.barClick((h.from+h.to)/2+1, a.barBotRow, true)
			if len(cc.captured) != 2 {
				t.Fatalf("Mute wrote %d messages, want down+up", len(cc.captured))
			}
			_, code, _ := keycoded(t, cc.captured[0])
			if code != 164 {
				t.Errorf("Mute sent keycode %d, want 164", code)
			}
			return
		}
	}
	t.Fatalf("Mute button not found in a 200-cell bar: %q", a.tui.overlay[a.barBotRow-1].text)
}
