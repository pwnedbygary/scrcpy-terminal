package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// capConn captures control socket writes for assertions. Only Write is used by
// the controller; other net.Conn methods are unreachable in these tests.
type capConn struct {
	net.Conn // nil — embedding documents "unused, do not call"
	captured [][]byte
}

func (c *capConn) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	c.captured = append(c.captured, cp)
	return len(p), nil
}

// TestScreenshotHeadless: with a.tui == nil (headless mode) screenshot() is a
// silent no-op — no file written.
func TestScreenshotHeadless(t *testing.T) {
	t.Chdir(t.TempDir())
	a := &app{}
	out := stderrOf(t, func() { waitShot(t, a.takeShot(shotTime)) })
	if files := shotFiles(t); len(files) != 0 || out != "" {
		t.Fatalf("headless screenshot wrote files %v and said %q", files, out)
	}
}

// TestScreenshotNoFrameYet: TUI running but no frame decoded or drawn yet —
// no file, "no frame yet" at once.
func TestScreenshotNoFrameYet(t *testing.T) {
	t.Chdir(t.TempDir())
	a := &app{tui: &tui{cols: 80, rows: 39}, stream: &streamState{}}
	a.tui.running = true
	out := stderrOf(t, func() { waitShot(t, a.takeShot(shotTime)) })
	if want := "scterm: screenshot: no frame yet\n"; out != want {
		t.Errorf("notice %q, want %q", out, want)
	}
	if files := shotFiles(t); len(files) != 0 {
		t.Fatalf("wrote %v with no frame yet", files)
	}
}

// TestScreenshotKeyRepeat: a terminal sends Alt+S held down as more presses,
// here the first repeat half a second after the press and the rest 33 ms
// apart. A press within 200 ms of the Alt+S key press before it is the key's
// repeat and takes no screenshot, so the hold takes two: the press's and the
// first repeat's. Once the key is let go, a press 199 ms after the last
// repeat is taken for one too, and a press 200 ms after that takes a
// screenshot. The clock is the test's: each press says when it came. With no
// frame yet, a press that takes a screenshot says so at once.
func TestScreenshotKeyRepeat(t *testing.T) {
	a := &app{tui: &tui{cols: 80, rows: 39, running: true}, stream: &streamState{}}
	start := time.Date(2026, 10, 21, 4, 55, 0, 0, time.Local)
	var at time.Duration
	press := func(after time.Duration, shot bool, what string) {
		t.Helper()
		at += after
		want := ""
		if shot {
			want = "scterm: screenshot: no frame yet\n"
		}
		if out := stderrOf(t, func() { a.screenshotKey(start.Add(at)) }); out != want {
			t.Errorf("%s, %v after the first press, said %q, want %q", what, at, out, want)
		}
	}
	press(0, true, "the press")
	press(500*time.Millisecond, true, "the first repeat")
	for range 30 {
		press(33*time.Millisecond, false, "a repeat")
	}
	press(199*time.Millisecond, false, "a press 199 ms after the last repeat")
	press(200*time.Millisecond, true, "a press 200 ms after the Alt+S before it")
}

// TestScreenshotRepeatOnlyFromTheKey: Alt+S from the terminal is checked for
// a repeat; the action bar's Shot and the menu's Screenshot row, chosen by its
// letter or a click, are not, nor do they count as the Alt+S key press before
// one. With every key press after the first a repeat (shotKeyRepeat an hour):
// the bar and the menu take one screenshot each time, the first Alt+S after
// them takes one too, a second Alt+S takes none, the bar and the menu still
// take one each, and the next Alt+S is still a repeat, with the menu open too.
func TestScreenshotRepeatOnlyFromTheKey(t *testing.T) {
	withTermSize(t, 200, 30) // every button on one page
	old := shotKeyRepeat
	shotKeyRepeat = time.Hour
	t.Cleanup(func() { shotKeyRepeat = old })
	a := newTestApp(nil)
	a.tui = &tui{cols: 200, rows: 29, running: true}
	a.stream = &streamState{} // no frame yet: a screenshot says so at once
	shots := func(fn func()) int {
		t.Helper()
		var out string
		captureStdout(t, func() { out = stderrOf(t, fn) }) // the overlays paint
		return strings.Count(out, "scterm: screenshot: no frame yet\n")
	}
	altS := func() { a.handleInput([]byte{0x1b, 's'}) }
	barAndMenu := func() {
		t.Helper()
		captureStdout(t, func() { a.wakeBar(); a.refreshOverlays() })
		x := barCell(t, a, "Shot")
		if n := shots(func() { a.handleInput(mouseSeq(0, x, a.barBotRow, true)) }); n != 1 {
			t.Errorf("the bar's Shot took %d screenshots, want 1", n)
		}
		captureStdout(t, a.toggleMenu)
		if n := shots(func() { a.handleInput([]byte{'s'}) }); n != 1 {
			t.Errorf("the menu's Screenshot row, by its letter, took %d screenshots, want 1", n)
		}
		captureStdout(t, a.toggleMenu)
		row := 0
		for _, h := range a.menuHits {
			if h.item.Op == "screenshot" {
				row = h.row
			}
		}
		if row == 0 {
			t.Fatal("the menu has no Screenshot row")
		}
		if n := shots(func() { a.handleInput(mouseSeq(0, 5, row, true)) }); n != 1 {
			t.Errorf("the menu's Screenshot row, clicked, took %d screenshots, want 1", n)
		}
	}
	barAndMenu()
	if n := shots(altS); n != 1 {
		t.Fatalf("Alt+S after the bar and the menu took %d screenshots, want 1: neither is a key press", n)
	}
	if n := shots(altS); n != 0 {
		t.Errorf("Alt+S again took %d screenshots, want none: it is the key's repeat", n)
	}
	barAndMenu()
	if n := shots(altS); n != 0 {
		t.Errorf("Alt+S after the bar and the menu took %d screenshots, want none: it is still a repeat", n)
	}
	captureStdout(t, a.toggleMenu)
	if !a.menuOpen {
		t.Fatal("the menu did not open")
	}
	if n := shots(altS); n != 0 {
		t.Errorf("Alt+S with the menu open took %d screenshots, want none: it is still a repeat", n)
	}
}

// TestScreenshotKeyWithTheMenuOpen: Alt+S with the menu open is a key press
// like any other. As the first Alt+S it takes a screenshot (the menu neither
// drops the chord nor closes on it), and it is recorded, so the next Alt+S is
// its repeat (shotKeyRepeat an hour).
func TestScreenshotKeyWithTheMenuOpen(t *testing.T) {
	withTermSize(t, 200, 30)
	old := shotKeyRepeat
	shotKeyRepeat = time.Hour
	t.Cleanup(func() { shotKeyRepeat = old })
	a := newTestApp(nil)
	a.tui = &tui{cols: 200, rows: 29, running: true}
	a.stream = &streamState{} // no frame yet: a screenshot says so at once
	shots := func(fn func()) int {
		t.Helper()
		var out string
		captureStdout(t, func() { out = stderrOf(t, fn) }) // the menu paints
		return strings.Count(out, "scterm: screenshot: no frame yet\n")
	}
	altS := func() { a.handleInput([]byte{0x1b, 's'}) }
	captureStdout(t, a.toggleMenu)
	if !a.menuOpen {
		t.Fatal("the menu did not open")
	}
	if n := shots(altS); n != 1 {
		t.Errorf("the first Alt+S, with the menu open, took %d screenshots, want 1", n)
	}
	if !a.menuOpen {
		t.Error("the menu closed on the first Alt+S, want it left open")
	}
	if n := shots(altS); n != 0 {
		t.Errorf("Alt+S after the one with the menu open took %d screenshots, want none: its repeat", n)
	}
}

// canvasApp is a terminal UI app on an 80x40 canvas (80 columns, 20 rows) that
// has drawn a frame of frameW x frameH: black letterbox bars, and in the picture
// at (x, y), w x h, a pattern of BGR0 pixels (bytes B, G, R, unused, FFmpeg's
// AV_PIX_FMT_BGR0) that differ in every channel. Its stream has decoded a
// frame, but no video goroutine answers a screenshot request.
func canvasApp(frameW, frameH, x, y, w, h int) *app {
	a := &app{tui: &tui{cols: 80, rows: 20, running: true}, stream: &streamState{}}
	a.stream.frameCount.Store(1)
	a.frameW, a.frameH = frameW, frameH
	canvas := make([]byte, 80*40*4)
	for py := y; py < y+h; py++ {
		for px := x; px < x+w; px++ {
			p := canvas[(py*80+px)*4:]
			p[0], p[1], p[2] = byte(1+px), byte(101+py), byte(201-px-py)
		}
	}
	a.tui.lastRGB = canvas
	a.tui.lastW, a.tui.lastH = 80, 40 // as a real draw records them
	return a
}

// TestScreenshotWritesPNG: when no frame comes within shotWait, the canvas the
// terminal shows is saved as a PNG: only the picture, without the letterbox
// bars, each pixel's B, G, R as the PNG's R, G, B, and the notice says so.
// A 1280x2560 frame fits the 80x40 canvas at 1/64, a 2560x640 one at 1/32.
func TestScreenshotWritesPNG(t *testing.T) {
	for _, tc := range []struct {
		name           string
		frameW, frameH int
		x, y, w, h     int // the picture on the canvas
	}{
		{"bars at the sides", 1280, 2560, 30, 0, 20, 40},
		{"bars above and below", 2560, 640, 0, 10, 80, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			withShotWait(t, 20*time.Millisecond)
			a := canvasApp(tc.frameW, tc.frameH, tc.x, tc.y, tc.w, tc.h)
			out := stderrOf(t, func() { waitShot(t, a.takeShot(shotTime)) })
			want := fmt.Sprintf("scterm: saved %s (%dx%d) at the terminal's size, because no frame came from the device in time\n", shotName, tc.w, tc.h)
			if out != want {
				t.Errorf("notice %q, want %q", out, want)
			}
			img := readShot(t, shotName)
			if b := img.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
				t.Fatalf("the screenshot is %dx%d, want the %dx%d picture", b.Dx(), b.Dy(), tc.w, tc.h)
			}
			for py := 0; py < tc.h; py++ {
				for px := 0; px < tc.w; px++ {
					cx, cy := tc.x+px, tc.y+py
					want := [3]uint32{uint32(byte(201 - cx - cy)), uint32(byte(101 + cy)), uint32(byte(1 + cx))}
					r, g, b, _ := img.At(px, py).RGBA()
					if got := [3]uint32{r >> 8, g >> 8, b >> 8}; got != want {
						t.Fatalf("pixel (%d,%d) is %v, want %v", px, py, got, want)
					}
				}
			}
		})
	}
}

// TestScreenshotKeepsColours: a pure red, green and blue pixel as the scaler
// lays them out (B, G, R, unused) are red, green and blue in the PNG.
func TestScreenshotKeepsColours(t *testing.T) {
	t.Chdir(t.TempDir())
	canvas := []byte{
		0, 0, 255, 0, // red
		0, 255, 0, 0, // green
		255, 0, 0, 0, // blue
	}
	name, err := writeShotPNG(shotTime, canvas, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	img := readShot(t, name)
	if b := img.Bounds(); b.Dx() != 3 || b.Dy() != 1 {
		t.Fatalf("the PNG is %dx%d, want 3x1", b.Dx(), b.Dy())
	}
	for x, want := range [][4]uint32{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}} {
		r, g, b, a := img.At(x, 0).RGBA()
		if got := [4]uint32{r >> 8, g >> 8, b >> 8, a >> 8}; got != want {
			t.Errorf("pixel %d is %v, want %v", x, got, want)
		}
	}
}

// TestScreenshotNameTaken: a screenshot never replaces a file. Names that are
// taken get -2, -3 and so on, and the files there are left as they were.
func TestScreenshotNameTaken(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{shotName, "scterm-20261021-045500-2.png"} {
		if err := os.WriteFile(name, []byte("keep"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	name, err := writeShotPNG(shotTime, make([]byte, 4), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := "scterm-20261021-045500-3.png"; name != want {
		t.Errorf("saved as %s, want %s", name, want)
	}
	readShot(t, name)
	for _, kept := range []string{shotName, "scterm-20261021-045500-2.png"} {
		if b, err := os.ReadFile(kept); err != nil || string(b) != "keep" {
			t.Errorf("%s now holds %q (%v)", kept, b, err)
		}
	}
}

// TestDumpFrameKeepsColours: --dump-frames writes PPM. A pure red, green and
// blue pixel as the scaler lays them out (B, G, R, unused) are red, green and
// blue in the PPM.
func TestDumpFrameKeepsColours(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rgb.ppm")
	canvas := []byte{
		0, 0, 255, 0, // red
		0, 255, 0, 0, // green
		255, 0, 0, 0, // blue
	}
	if err := writePPMFile(path, canvas, 3, 1); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const header = "P6\n3 1\n255\n"
	want := header + "\xff\x00\x00" + "\x00\xff\x00" + "\x00\x00\xff"
	if string(data) != want {
		t.Errorf("PPM = %q, want %q", data, want)
	}
}

// TestScreenshotStaleCanvas: a snapshot from a different geometry must not be
// saved as a sheared picture. A resize can keep the same cell count (120x29
// and 116x30 are both 3480 cells), so the length check alone is not enough.
func TestScreenshotStaleCanvas(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, 20*time.Millisecond)
	a := &app{tui: &tui{cols: 80, rows: 39, running: true}, stream: &streamState{}}
	a.stream.frameCount.Store(1) // no video goroutine answers: the canvas would be saved
	a.frameW, a.frameH = 1280, 720
	// 80x39 and 120x26 are both 3120 cells, so the snapshot has exactly the
	// length the current geometry wants -- only the dimensions differ.
	a.tui.lastW, a.tui.lastH = 120, 52
	a.tui.lastRGB = make([]byte, 80*39*2*4)
	if len(a.tui.lastRGB) != 120*52*4 {
		t.Fatalf("test setup is wrong: %d vs %d", len(a.tui.lastRGB), 120*52*4)
	}
	out := stderrOf(t, func() { waitShot(t, a.takeShot(shotTime)) })
	if want := "scterm: screenshot: no frame yet\n"; out != want {
		t.Errorf("notice %q, want %q", out, want)
	}
	if files := shotFiles(t); len(files) != 0 {
		t.Fatalf("wrote a stale screenshot: %v", files)
	}
}

// TestNilConnGuard: every controller method must return errNoControl (never
// panic) when conn is nil — the &controller{} literal in tests and the
// pre-connection window in production both hit this path.
func TestNilConnGuard(t *testing.T) {
	c := &controller{} // conn never set
	var c2 *controller // nil receiver too
	for _, fn := range []func() error{
		func() error { return c.touch(true, position{}) },
		func() error { return c.touchMove(position{}) },
		func() error { return c.back() },
		func() error { return c.resetVideo() },
		func() error { return c.rotate() },
		func() error { return c.injectText("hi") },
		func() error { return c2.send(ctrlResetVideo, nil) },
	} {
		err := fn()
		if err != errNoControl {
			t.Fatalf("nil-conn guard returned %v, want errNoControl", err)
		}
	}
}

// TestAltKNoControl: Alt-K with control disabled must log, not panic.
func TestAltKNoControl(t *testing.T) {
	a := &app{events: make(chan inputEvent, 64), kb: newKeyboard(), ctrl: nil}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Alt-K panicked without control: %v", r)
		}
	}()
	a.handleInput([]byte{0x1b, 'k'}) // ESC k = Alt+K (raw escape prefix)
}

// TestAltKNoControlSaysWhy: without control, Alt-K says why nothing happened,
// in words that hold for --no-control and for a peer that allows viewing only
// (no old flag spelling).
func TestAltKNoControlSaysWhy(t *testing.T) {
	a := &app{events: make(chan inputEvent, 64), kb: newKeyboard()}
	out := captureStderr(t, func() { a.handleInput([]byte{0x1b, 'k'}) })
	if want := "scterm: Refresh video needs control, and this session is view only\n"; out != want {
		t.Errorf("Alt-K without control said %q, want %q", out, want)
	}
}

// TestAltKResetVideoWire: Alt-K with a controller sends exactly one wire byte —
// message type ctrlResetVideo(17), empty payload.
func TestAltKResetVideoWire(t *testing.T) {
	cc := &capConn{}
	a := &app{events: make(chan inputEvent, 64), kb: newKeyboard(), ctrl: newController(cc)}
	a.handleInput([]byte{0x1b, 'k'})

	if len(cc.captured) != 1 {
		t.Fatalf("want exactly 1 control write on Alt-K, got %d", len(cc.captured))
	}
	w := cc.captured[0]
	if len(w) != 1 || w[0] != ctrlResetVideo {
		t.Fatalf("wire bytes: % x (want single byte %#x)", w, ctrlResetVideo)
	}
}

// TestMouseModesPreferSGR: the enable sequence must leave the terminal in SGR
// encoding. On a terminal that implements both SGR (1006) and urxvt (1015),
// the mode set last wins; enabling 1015 after 1006 makes it send
// "CSI b;x;yM" (no "<"), which this client does not parse -- so the mouse
// worked through Zellij (which re-encodes to SGR) but did nothing in a plain
// terminal tab.
func TestMouseModesPreferSGR(t *testing.T) {
	if !strings.Contains(mouseOnSeq, "?1006h") {
		t.Fatal("mouseOnSeq does not enable SGR (1006)")
	}
	if strings.Contains(mouseOnSeq, "?1015h") {
		t.Fatal("mouseOnSeq enables urxvt (1015): the last mode set wins and its events are unparseable")
	}
	for _, mode := range []string{"?1000h", "?1002h", "?1003h"} {
		if !strings.Contains(mouseOnSeq, mode) {
			t.Errorf("mouseOnSeq does not enable %s", mode)
		}
	}
	// The off sequence still clears 1015, in case something else set it.
	if !strings.Contains(mouseOffSeq, "?1015l") {
		t.Error("mouseOffSeq does not disable urxvt (1015)")
	}
}

// TestLocalVolumeIsWholePercent: Alt+- steps the local volume from 100% to 90,
// 80 and 70 (not 89, 78, 67), the status line says so, and Alt+M mutes and
// then brings back 70%, not 100%. Playback follows the percentage.
func TestLocalVolumeIsWholePercent(t *testing.T) {
	a := newTestApp(nil)
	a.tui = &tui{cols: 80, rows: 29}
	a.cfg.audio = true
	a.audio = &audioSink{}
	a.audio.setGain(100)

	for _, want := range []int{90, 80, 70} {
		a.handleInput([]byte{0x1b, '-'})
		if got := a.audio.gainPercent(); got != want {
			t.Fatalf("Alt+- reads %d%%, want %d%%", got, want)
		}
		if s := a.statusLine(); !strings.Contains(s, "vol "+strconv.Itoa(want)+"%") {
			t.Errorf("at %d%% the status line reads %q", want, s)
		}
	}
	a.handleInput([]byte{0x1b, 'm'})
	if got := a.audio.gainPercent(); got != 0 {
		t.Fatalf("Alt+M left the volume at %d%%, want 0", got)
	}
	a.handleInput([]byte{0x1b, 'm'})
	if got := a.audio.gainPercent(); got != 70 {
		t.Fatalf("Alt+M again brought back %d%%, want the 70%% from before the mute", got)
	}

	// Two samples of 10000 come out at 70% of that, to within q8 resolution.
	pcm := binary.LittleEndian.AppendUint16(binary.LittleEndian.AppendUint16(nil, 10000), 10000)
	a.audio.writePCM16(pcm)
	if got := int16(binary.LittleEndian.Uint16(pcm)); got < 6950 || got > 7050 {
		t.Errorf("a 10000 sample plays as %d at 70%%, want about 7000", got)
	}
}
