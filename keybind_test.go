package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	a.screenshot()
	files, _ := filepath.Glob("scterm-*.ppm")
	if len(files) != 0 {
		t.Fatalf("headless screenshot wrote files: %v", files)
	}
}

// TestScreenshotNoFrameYet: TUI running but draw() never rendered a frame —
// no file, "no frame yet" on stderr.
func TestScreenshotNoFrameYet(t *testing.T) {
	t.Chdir(t.TempDir())
	a := &app{tui: &tui{cols: 80, rows: 39}}
	a.tui.running = true
	a.screenshot()
	files, _ := filepath.Glob("scterm-*.ppm")
	if len(files) != 0 {
		t.Fatalf("wrote PPM with no frame yet: %v", files)
	}
}

// TestScreenshotWritesPPM: happy path — a rendered canvas snapshot is written
// as binary PPM at terminal-canvas resolution (cols x rows*2), alpha stripped.
func TestScreenshotWritesPPM(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	pw, ph := 80, 78 // cols=80, rows=39 → canvas 80x78
	n := pw * ph * 4
	pattern := make([]byte, n)
	for i := range pattern {
		pattern[i] = byte(i % 251)
	}

	a := &app{tui: &tui{cols: 80, rows: 39}}
	a.tui.running = true
	a.tui.lastRGB = pattern
	a.screenshot()

	files, _ := filepath.Glob("scterm-*.ppm")
	if len(files) != 1 {
		t.Fatalf("want exactly one PPM in %s, got %v", dir, files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	const header = "P6\n80 78\n255\n"
	if !strings.HasPrefix(string(data), header) {
		t.Fatalf("bad PPM header: %q", data[:14])
	}
	body := data[len(header):]
	wantBody := pw * ph * 3
	if len(body) != wantBody {
		t.Fatalf("body length %d, want %d", len(body), wantBody)
	}
	// spot-check: first pixel RGB and last pixel RGB survive the alpha strip
	if body[0] != pattern[0] || body[1] != pattern[1] || body[2] != pattern[2] {
		t.Fatalf("first pixel mismatch: %v", body[:3])
	}
	last := n - 4 // last RGBA quad
	if body[wantBody-3] != pattern[last] || body[wantBody-2] != pattern[last+1] || body[wantBody-1] != pattern[last+2] {
		t.Fatalf("last pixel mismatch: %v", body[wantBody-3:])
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
