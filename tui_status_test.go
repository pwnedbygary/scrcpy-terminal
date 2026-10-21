package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The status row: the hints first and whole, the rest dropped or cut by
// display cells in order of importance, a sparkline with no blank cells, and
// input state in plain words. Also the Controls menu's title and the frame
// after a resize.
// ---------------------------------------------------------------------------

// statusHintsText is how the status row must start: how to quit and how to
// find every other control.
const statusHintsText = "Alt+Q quit · Alt+/ controls"

// statusApp is a terminal UI app width cells wide showing a device: volume
// 100%, a 1080x2400 picture and 32 frames 16 ms apart (16.0 ms, 62.5 fps).
func statusApp(width int, name string) *app {
	a := newTestApp(nil)
	a.tui = &tui{cols: width, rows: 23}
	a.cfg.audio = true
	a.audio = &audioSink{}
	a.audio.setGain(100)
	a.sess = &session{deviceName: name}
	a.frameW, a.frameH = 1080, 2400
	for i := 0; i < 32; i++ {
		a.recordFrameTime(16)
	}
	return a
}

// TestStatusHintsComeFirst: the hints lead the row and stay whole at any
// width they fit in; after them the sparkline goes first, then the frame
// statistics, then the device name (cut short before it goes). A pane
// narrower than the hints keeps how to quit.
func TestStatusHintsComeFirst(t *testing.T) {
	const long = "Galaxy Tab S9 Ultra in the living room by the big window, 2F"
	if len(long) != 60 {
		t.Fatalf("test setup: the long name is %d cells, want 60", len(long))
	}
	const stats = "1080x2400 16.0ms 62.5fps"
	spark := strings.Repeat("▁", 20)
	cases := []struct {
		width int
		name  string
		want  string
	}{
		{200, long, statusHintsText + " | vol 100% · " + long + " · " + stats + " " + spark},
		{120, long, statusHintsText + " | vol 100% · " + long},
		{80, long, statusHintsText + " | vol 100% · Galaxy Tab S9 Ultra in the living room…"},
		{60, long, statusHintsText + " | vol 100% · Galaxy Tab S9 Ultr…"},
		{40, long, statusHintsText + " | vol 100%"},
		{27, long, statusHintsText},
		{20, long, "Alt+Q quit"},
		{8, long, "Alt+Q qu"},
		{120, "Pixel 8", statusHintsText + " | vol 100% · Pixel 8 · " + stats + " " + spark},
		{80, "Pixel 8", statusHintsText + " | vol 100% · Pixel 8 · " + stats},
		// Wide characters take two cells: 19 of them and the ellipsis fill
		// the 39 cells left at 80, and at 81 the twentieth still does not fit.
		{80, strings.Repeat("界", 30), statusHintsText + " | vol 100% · " + strings.Repeat("界", 19) + "…"},
		{81, strings.Repeat("界", 30), statusHintsText + " | vol 100% · " + strings.Repeat("界", 19) + "…"},
	}
	for _, tc := range cases {
		if got := statusApp(tc.width, tc.name).statusLine(); got != tc.want {
			t.Errorf("%d cells, %q:\n got %q\nwant %q", tc.width, tc.name, got, tc.want)
		}
	}
}

// TestStatusSaysWhereInputGoes: no "UNGRAB". With the mouse released the row
// says so and names F12; under Zellij, where typing is dropped while
// ungrabbed (F1-F11, Alt+letters and Esc still reach the device), it says
// typing goes to Zellij and F12 gives control, whole at 80 columns. The
// dropping itself is unchanged.
func TestStatusSaysWhereInputGoes(t *testing.T) {
	const tail = " · vol 100% · Pixel 8 · 1080x2400 16.0ms 62.5fps"
	spark := " " + strings.Repeat("▁", 20)
	cases := []struct {
		name            string
		width           int
		zellij, grabbed bool
		want            string
	}{
		{"grabbed", 120, false, true, statusHintsText + " | vol 100% · Pixel 8 · 1080x2400 16.0ms 62.5fps" + spark},
		{"released", 120, false, false, statusHintsText + " | mouse free (F12)" + tail + spark},
		{"Zellij, grabbed", 120, true, true, statusHintsText + " | vol 100% · Pixel 8 · 1080x2400 16.0ms 62.5fps" + spark},
		{"Zellij, released", 120, true, false, statusHintsText + " | typing goes to Zellij · F12 gives control" + tail},
		{"Zellij, released, 80 columns", 80, true, false, statusHintsText + " | typing goes to Zellij · F12 gives control"},
	}
	for _, tc := range cases {
		a := statusApp(tc.width, "Pixel 8")
		a.inZellij, a.grabbed = tc.zellij, tc.grabbed
		got := a.statusLine()
		if got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
		if strings.Contains(got, "UNGRAB") {
			t.Errorf("%s: the row still says UNGRAB: %q", tc.name, got)
		}
	}

	cc := &capConn{}
	a := newTestApp(newController(cc))
	a.inZellij, a.grabbed = true, false
	a.handleInput([]byte{'a'})
	if len(cc.captured) != 0 {
		t.Errorf("typing reached the device while released under Zellij: %d messages", len(cc.captured))
	}
	a.grabbed = true
	a.handleInput([]byte{'a'})
	if len(cc.captured) != 2 {
		t.Errorf("typing after F12 wrote %d messages, want a key down and up", len(cc.captured))
	}
}

// TestSparklineHasNoBlankCells: a steady 60 fps draws the lowest bar in every
// cell (it drew 20 blank cells), no frame time draws a blank, and 100 ms or
// more is the seventh, top bar.
func TestSparklineHasNoBlankCells(t *testing.T) {
	a := newTestApp(nil)
	for i := 0; i < 30; i++ {
		a.recordFrameTime(1000.0 / 60)
	}
	if got, want := a.sparkline(20), strings.Repeat("▁", 20); got != want {
		t.Errorf("steady 60 fps: %q, want %q", got, want)
	}
	for ms := 0.0; ms <= 200; ms += 0.5 {
		b := newTestApp(nil)
		b.recordFrameTime(ms)
		if s := b.sparkline(1); s == "" || strings.ContainsRune(s, ' ') {
			t.Fatalf("%.1f ms draws %q", ms, s)
		}
	}
	for _, ms := range []float64{100, 250} {
		c := newTestApp(nil)
		c.recordFrameTime(ms)
		if got := c.sparkline(1); got != "▇" {
			t.Errorf("%.0f ms draws %q, want the top bar ▇", ms, got)
		}
	}
}

// TestStatusRowCountsCells: the status row is cut and erased by display
// cells. Five wide characters fill a 10-cell row (so no erase after it, which
// in the pending-wrap state would take the last cell), and eight are cut to
// the five that fit instead of running off the bottom row.
func TestStatusRowCountsCells(t *testing.T) {
	tr := &tui{cols: 10, rows: 3, running: true}
	five := strings.Repeat("界", 5)
	out := captureStdout(t, func() { tr.setStatus(five) })
	if !strings.Contains(out, five) || strings.Contains(out, "\x1b[K") {
		t.Errorf("a full row of wide characters: %q", out)
	}
	out = captureStdout(t, func() { tr.setStatus(strings.Repeat("界", 8)) })
	if !strings.Contains(out, five) || strings.Contains(out, five+"界") {
		t.Errorf("eight wide characters in 10 cells: %q", out)
	}
}

// TestMenuTitleSaysControls: Alt+/ opens "Controls", as the browser calls it.
func TestMenuTitleSaysControls(t *testing.T) {
	lines := newTestApp(nil).menuLines(30)
	if got := strings.TrimSpace(lines[0].text); got != "scterm · Controls" {
		t.Errorf("menu title %q, want %q", got, "scterm · Controls")
	}
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l.text), "actions") {
			t.Errorf("menu line still says actions: %q", l.text)
		}
	}
}

// TestResizeTakesTheNextFrameAtTheNewSize: a resize asks the demuxer for a
// canvas of the new size; a frame still sized for the old grid is skipped
// rather than sheared, and the first one at the new size repaints every row.
// No keyframe is involved: the decoder never sees the terminal size.
func TestResizeTakesTheNextFrameAtTheNewSize(t *testing.T) {
	withTermSize(t, 20, 6)
	tr := &tui{running: true, repaintInterval: 1000}
	tr.resize() // 20 columns, 5 picture rows
	if out := captureStdout(t, func() { tr.draw(make([]byte, 20*5*2*4)) }); out == "" {
		t.Fatal("the first frame was not drawn")
	}

	termSizeOverride = struct{ cols, rows int }{30, 8}
	s := &streamState{videoW: 1080, videoH: 2400}
	a := newTestApp(nil)
	a.tui, a.stream = tr, s
	captureStdout(t, a.onResize)
	if !s.geometryDirty.Load() {
		t.Fatal("a resize did not ask the demuxer for a new canvas")
	}
	s.updateGeometry()
	if w, h := s.canvasSize(); w != 30 || h != 14 {
		t.Fatalf("canvas after the resize is %dx%d, want 30x14", w, h)
	}

	if out := captureStdout(t, func() { tr.draw(make([]byte, 20*5*2*4)) }); out != "" {
		t.Errorf("a frame for the old size was drawn into the new grid: %q", out)
	}
	out := captureStdout(t, func() { tr.draw(make([]byte, 30*7*2*4)) })
	if !strings.HasPrefix(out, "\x1b[?2026h\x1b[H") {
		t.Errorf("the first frame at the new size is not a full repaint: %q", out)
	}
	for y := 2; y <= 7; y++ {
		if !strings.Contains(out, "\x1b["+strconv.Itoa(y)+";1H") {
			t.Errorf("row %d not repainted after the resize", y)
		}
	}
}

// statusQuitPromptText is what the status row says while an overlay's Quit
// waits for its second press.
const statusQuitPromptText = "press Quit again to quit"

// TestStatusAsksForTheSecondQuit: while an overlay's Quit is armed, the status
// row says so first after the hints, cut short on a narrow pane; the words go
// when the arm runs out or another press drops it. It is not a notice, so it
// is not printed again after the terminal UI ends.
func TestStatusAsksForTheSecondQuit(t *testing.T) {
	startNotices(t)
	const normal = statusHintsText + " | vol 100% · Pixel 8 · 1080x2400 16.0ms 62.5fps"
	const armed = statusHintsText + " | " + statusQuitPromptText + " · vol 100% · Pixel 8"
	a := statusApp(80, "Pixel 8")
	for _, from := range []int{quitFromBar, quitFromMenu, quitFromKeyboard} {
		a.pressQuit(from)
		if got := a.statusLine(); got != armed {
			t.Errorf("armed from overlay %d:\n got %q\nwant %q", from, got, armed)
		}
		ageQuitArm(a, 2100*time.Millisecond)
		if got := a.statusLine(); got != normal {
			t.Errorf("a 2.1 s old arm from overlay %d:\n got %q\nwant %q", from, got, normal)
		}
	}
	a.pressQuit(quitFromBar)
	a.dropQuitArm(a.pendingQuit())
	if got := a.statusLine(); got != normal {
		t.Errorf("after another press dropped the arm:\n got %q\nwant %q", got, normal)
	}

	narrow := statusApp(50, "Pixel 8")
	narrow.pressQuit(quitFromBar)
	if got, want := narrow.statusLine(), statusHintsText+" | press Quit again to…"; got != want {
		t.Errorf("50 cells:\n got %q\nwant %q", got, want)
	}

	if n := notices.current(timeNowUnixNano()); n != "" {
		t.Errorf("the prompt is a notice: %q", n)
	}
	var replay strings.Builder
	notices.stop(&replay)
	if replay.Len() != 0 {
		t.Errorf("the prompt is printed after the terminal UI ends: %q", replay.String())
	}
}

// TestStatusPromptComesBeforeANotice: a notice shows for 4 s and an arm lasts
// 2 s, so a Quit pressed while a notice shows still asks for the second press
// first after the hints, with the notice after it.
func TestStatusPromptComesBeforeANotice(t *testing.T) {
	startNotices(t)
	a := statusApp(80, "Pixel 8")
	notices.post("saved shot.ppm", timeNowUnixNano())
	a.pressQuit(quitFromBar)
	if got, want := a.statusLine(), statusHintsText+" | "+statusQuitPromptText+" · saved shot.ppm"; got != want {
		t.Errorf("a Quit pressed while a notice shows:\n got %q\nwant %q", got, want)
	}
}

// TestOverlayQuitShowsOnTheStatusRow: the action bar's Quit, the menu's (on an
// 18-row pane, where the menu's own "Press again to quit" row is cut off) and
// the keyboard's QUIT each put the prompt on the status row the painter
// shows; another press, or the tick after the arm ran out, takes it away.
func TestOverlayQuitShowsOnTheStatusRow(t *testing.T) {
	withTermSize(t, 200, 30) // every bar button on one page
	bar := newTestApp(newController(&capConn{}))
	bar.audio = &audioSink{}
	bar.tui = &tui{cols: 200, rows: 29}
	bar.wakeBar()
	y := bar.barBotRow
	click := func(a *app, x, y int) {
		a.handleInput(mouseSeq(0, x, y, true))
		a.handleInput(mouseSeq(0, x, y, false))
	}
	click(bar, barCell(t, bar, "Quit"), y)
	if !strings.Contains(bar.tui.status, statusQuitPromptText) {
		t.Errorf("the bar's Quit is armed but the status row reads %q", bar.tui.status)
	}
	click(bar, barCell(t, bar, "Home"), y)
	if strings.Contains(bar.tui.status, statusQuitPromptText) {
		t.Errorf("after Home the status row still reads %q", bar.tui.status)
	}

	withTermSize(t, 80, 18)
	menu := newTestApp(newController(&capConn{}))
	menu.audio = &audioSink{}
	menu.tui = &tui{cols: 80, rows: 17}
	menu.toggleMenu()
	menu.handleInput([]byte{'q'})
	if !strings.Contains(menu.tui.status, statusQuitPromptText) {
		t.Errorf("the menu's Quit is armed but the status row reads %q", menu.tui.status)
	}

	withTermSize(t, 80, 30)
	k := newTestApp(newController(&capConn{}))
	k.audio = &audioSink{}
	k.tui = &tui{cols: 80, rows: 29}
	k.openKeyboard()
	x, ky := kbKeyCell(t, k.kb, "QUIT", 30)
	click(k, x, ky)
	if !strings.Contains(k.tui.status, statusQuitPromptText) {
		t.Errorf("the keyboard's QUIT is armed but the status row reads %q", k.tui.status)
	}
	k.kbTick(timeNowUnixNano() + int64(2100*time.Millisecond))
	if strings.Contains(k.tui.status, statusQuitPromptText) {
		t.Errorf("the tick after the arm ran out left the status row reading %q", k.tui.status)
	}
}

// TestStatusLeavesOutAnUnknownName: with no name for the device (or one that
// is nothing but control characters), the status row has no name part rather
// than "the device"; the sentences that name it still say "the device".
func TestStatusLeavesOutAnUnknownName(t *testing.T) {
	want := statusHintsText + " | vol 100% · 1080x2400 16.0ms 62.5fps " + strings.Repeat("▁", 20)
	for _, name := range []string{"", "\x1b\x07"} {
		if got := statusApp(120, name).statusLine(); got != want {
			t.Errorf("name %q:\n got %q\nwant %q", name, got, want)
		}
	}
	noSession := statusApp(120, "")
	noSession.sess = nil
	if got := noSession.statusLine(); got != want {
		t.Errorf("no session:\n got %q\nwant %q", got, want)
	}

	a := statusApp(120, "")
	a.cfg.video = true
	a.sess.video = videoSocket(t)
	if got, want := a.placeholderText(), "Waiting for video from the device…"; got != want {
		t.Errorf("placeholder %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(a.lostStream("video", io.EOF)), "lost the video stream from the device: EOF"; got != want {
		t.Errorf("lost stream %q, want %q", got, want)
	}
}
