package main

import (
	"net/http"
	"testing"
)

// TestComputeWebCanvas covers the letterbox geometry shared by the scaler, the
// status message and the pointer mapper.
func TestComputeWebCanvas(t *testing.T) {
	cases := []struct {
		name                   string
		tw, th, vw, vh         int
		cw, ch, fw, fh, ox, oy int
	}{
		{"exact fit", 1080, 1920, 1080, 1920, 1080, 1920, 1080, 1920, 0, 0},
		{"half scale", 540, 960, 1080, 1920, 540, 960, 540, 960, 0, 0},
		{"letterbox top/bottom", 1280, 1280, 1080, 1920, 1280, 1280, 720, 1280, 280, 0},
		{"pillarbox", 1920, 720, 1080, 1920, 1920, 720, 404, 720, 758, 0},
		{"never upscale", 4000, 4000, 480, 1080, 4000, 4000, 480, 1080, 1760, 1460},
		{"tiny viewport", 1, 1, 1080, 1920, 2, 2, 2, 2, 0, 0},
		{"no video yet", 800, 600, 0, 0, 800, 600, 800, 600, 0, 0},
	}
	for _, c := range cases {
		got := computeWebCanvas(c.tw, c.th, c.vw, c.vh)
		if got.cw != c.cw || got.ch != c.ch || got.fw != c.fw || got.fh != c.fh ||
			got.ox != c.ox || got.oy != c.oy {
			t.Errorf("%s: got canvas %dx%d fit %dx%d+%d+%d, want %dx%d fit %dx%d+%d+%d",
				c.name, got.cw, got.ch, got.fw, got.fh, got.ox, got.oy,
				c.cw, c.ch, c.fw, c.fh, c.ox, c.oy)
		}
		if got.fw%2 != 0 || got.fh%2 != 0 {
			t.Errorf("%s: fit %dx%d is not even (JPEG 4:2:0 needs even)", c.name, got.fw, got.fh)
		}
	}
}

// TestWebVideoAt checks the two properties the device will not forgive: the
// reported screen size must equal the video size, and coordinates must stay
// inside it even when the pointer is on the letterbox.
func TestWebVideoAt(t *testing.T) {
	// 480x1080 device in a 1000x1000 viewport -> 444x1000 fit, 278px bars.
	cv := computeWebCanvas(1000, 1000, 480, 1080)
	if cv.fw != 444 || cv.fh != 1000 || cv.ox != 278 {
		t.Fatalf("unexpected fit: %+v", cv)
	}

	check := func(nx, ny uint32, wantX, wantY int32) {
		p := cv.videoAt(nx, ny)
		if p.screenW != 480 || p.screenH != 1080 {
			t.Fatalf("screen size %dx%d, want 480x1080", p.screenW, p.screenH)
		}
		if p.x != wantX || p.y != wantY {
			t.Errorf("videoAt(%d,%d) = (%d,%d), want (%d,%d)", nx, ny, p.x, p.y, wantX, wantY)
		}
	}
	// centre of the canvas is (very nearly) the centre of the device: the
	// rounding is one canvas pixel, i.e. half a device pixel at this scale
	check(32767, 32767, 238, 538)
	// left edge of the canvas lands on video x=0 (letterbox clamps)
	check(0, 32767, 0, 538)
	// top-left corner of the *video* region (x = ox = 278/1000 -> 18219)
	check(18219, 0, 0, 0)
	// bottom-right of the canvas clamps to the last device pixel
	check(65535, 65535, 479, 1079)
	// right edge of the fitted region maps just short of the final pixel
	check(65535, 0, 479, 0)
}

func TestWebVideoAtNoVideo(t *testing.T) {
	cv := computeWebCanvas(800, 600, 0, 0)
	p := cv.videoAt(32767, 32767)
	if p != (position{}) {
		t.Errorf("expected zero position without video geometry, got %+v", p)
	}
}

func TestOriginOK(t *testing.T) {
	cases := []struct {
		origin, host string
		ok           bool
	}{
		{"", "127.0.0.1:8080", true},                                // native client
		{"http://127.0.0.1:8080", "127.0.0.1:8080", true},           // our own page
		{"http://localhost:8080", "localhost:8080", true},           // our own page
		{"https://evil.example", "127.0.0.1:8080", false},           // attacker page
		{"http://127.0.0.1:9999", "127.0.0.1:8080", false},          // other port
		{"http://127.0.0.1:8080/", "127.0.0.1:8080", true},          // trailing slash
		{"null", "127.0.0.1:8080", false},                           // sandboxed iframe
		{"http://[::1]:8080", "[::1]:8080", true},                   // ipv6 literal
		{"http://127.0.0.1:8080.evil.com", "127.0.0.1:8080", false}, // lookalike
		{"http://LOCALHOST:8080", "localhost:8080", true},           // case
		{"http://127.0.0.1", "127.0.0.1:8080", false},               // missing port
		{"http://127.0.0.1:8080", "127.0.0.1:8080", true},           // control
		{"http://user@127.0.0.1:8080", "127.0.0.1:8080", false},     // userinfo trick
		{"https://127.0.0.1:8080", "127.0.0.1:8080", true},          // scheme ignored
		{"wss://127.0.0.1:8080", "127.0.0.1:8080", true},            // ws scheme
		{"http://127.0.0.1:8081", "127.0.0.1:8080", false},          // port mismatch
		{"http://[::1]:8080", "localhost:8080", false},              // ipv6 vs name
		{"http://127.0.0.1:8080", "localhost:8080", false},          // ip vs name
		{"http://localhost", "localhost:8080", false},               // no port
		{"ftp://127.0.0.1:8080", "127.0.0.1:8080", true},            // scheme ignored
	}
	for _, c := range cases {
		r := &http.Request{Host: c.host, Header: http.Header{}}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := originOK(r); got != c.ok {
			t.Errorf("originOK(origin=%q host=%q) = %v, want %v", c.origin, c.host, got, c.ok)
		}
	}
}

func TestAsciiKey(t *testing.T) {
	cases := []struct {
		c    byte
		code uint32
		meta uint32
		ok   bool
	}{
		{'a', 29, 0, true},
		{'z', 54, 0, true},
		{'A', 29, metaShiftOn, true},
		{'0', 7, 0, true},
		{'9', 16, 0, true},
		{' ', 62, 0, true},
		{'!', 0, 0, false},
		{0x1b, 0, 0, false},
	}
	for _, c := range cases {
		code, meta, ok := asciiKey(c.c)
		if ok != c.ok || code != c.code || meta != c.meta {
			t.Errorf("asciiKey(%q) = (%d,%#x,%v), want (%d,%#x,%v)",
				c.c, code, meta, ok, c.code, c.meta, c.ok)
		}
	}
}

func TestHeadlessKey(t *testing.T) {
	want := map[string]uint32{
		"home": 3, "menu": 82, "appswitch": 187, "power": 26,
		"volup": 24, "voldown": 25, "mute": 91, "nope": 0,
	}
	for op, code := range want {
		if got := headlessKey(op); got != code {
			t.Errorf("headlessKey(%q) = %d, want %d", op, got, code)
		}
	}
}
