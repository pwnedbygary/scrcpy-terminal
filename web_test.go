package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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

// TestWebAddrNameIsServed: when --web-addr is a name, the page is served by
// that name, since scterm prints its address with it and --window opens that
// address, whether the name stands for a loopback address (as this computer's
// name does in /etc/hosts, 127.0.1.1) or not. Names around it stay refused.
func TestWebAddrNameIsServed(t *testing.T) {
	for _, ip := range []string{"127.0.1.1", "192.0.2.7"} {
		names, _ := webHostNames(net.ParseIP(ip), "", "Mirror.Box.lan", "")
		for host, want := range map[string]bool{
			"mirror.box.lan:6969":     true,
			"MIRROR.BOX.LAN":          true,
			"box.lan:6969":            false,
			"www.mirror.box.lan:6969": false,
			"evil.example:6969":       false,
		} {
			if got := webHostAllowed(host, names); got != want {
				t.Errorf("with --web-addr Mirror.Box.lan on %s, %q allowed = %v, want %v", ip, host, got, want)
			}
		}
	}
}

// TestWebServesTheListenerItIsGiven: given a listener, as main gives it the one
// it opened before it touched a device, the server serves that listener and
// does not listen again. It prints, and --window opens, that listener's port,
// which the kernel picked for --web-port 0, and it accepts the names that suit
// that listener's address (allowedNames): here 192.0.2.10, an address on the
// network, where this computer's host name is served too.
func TestWebServesTheListenerItIsGiven(t *testing.T) {
	t.Setenv(webHostsEnv, "")
	listen, hostname := webListen, osHostname
	t.Cleanup(func() { webListen, osHostname = listen, hostname })
	webListen = func(network, address string) (net.Listener, error) {
		t.Errorf("the server listened again, on %s", address)
		return nil, errors.New("listened again")
	}
	osHostname = func() (string, error) { return "Scterm-Box", nil }
	startNotices(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cfg := config{web: true, webAddr: "127.0.0.1", webPort: 0}
	srv := newWebServer(cfg, &session{deviceName: "testphone"}, &streamState{cfg: cfg}, nil, nil, nil)
	srv.ln = addrListener{ln, &net.TCPAddr{IP: net.IPv4(192, 0, 2, 10), Port: port}}
	ran := make(chan error, 1)
	go func() { ran <- srv.run(false) }()
	select {
	case <-srv.ready:
	case err := <-ran:
		t.Fatalf("the server ended before it served: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("the server never started serving")
	}
	t.Cleanup(srv.stop)
	if want := fmt.Sprintf("http://127.0.0.1:%d/", port); srv.port != port || srv.url != want {
		t.Errorf("the server is on port %d and prints %s; want port %d and %s", srv.port, srv.url, port, want)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for _, host := range []string{addr, "scterm-box:" + strconv.Itoa(port)} {
		resp, body := askAs(t, addr, "/", host, "", false)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, `<canvas id="screen"`) {
			t.Errorf("GET / as %q: status %d, want the page", host, resp.StatusCode)
		}
	}
}

// TestWebStopBeforeRunServesNothing: a stop that comes before the server is
// up, as a signal at start-up does, leaves run nothing to do: run returns nil
// at once, prints no address and leaves the listener closed.
func TestWebStopBeforeRunServesNothing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{web: true, webAddr: "127.0.0.1"}
	srv := newWebServer(cfg, &session{deviceName: "testphone"}, &streamState{cfg: cfg}, nil, nil, nil)
	srv.ln = ln
	srv.stop()
	var ran error
	if out := captureStderr(t, func() { ran = srv.run(false) }); ran != nil || out != "" {
		t.Errorf("run after a stop: %v, and printed %q; want nil and nothing", ran, out)
	}
	if c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		c.Close()
		t.Error("the listener still took a connection after the stop")
	}
}

// TestWebStopFromAnotherGoroutine: a stop can come at any time from another
// goroutine, a signal's, while run sets the server up. The stops come from at
// once to about 2 ms in, so before and after the server is up; the race
// detector checks that each is ordered with run, and run returns either way.
func TestWebStopFromAnotherGoroutine(t *testing.T) {
	for i := range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		cfg := config{web: true, webAddr: "127.0.0.1"}
		srv := newWebServer(cfg, &session{deviceName: "testphone"}, &streamState{cfg: cfg}, nil, nil, nil)
		srv.ln = ln
		ran := make(chan error, 1)
		go func() { ran <- srv.run(false) }()
		time.Sleep(time.Duration(i) * 100 * time.Microsecond)
		srv.stop()
		select {
		case err := <-ran:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("run did not return after the stop")
		}
	}
}

// TestWebServesThisComputersNamesOnlyOnTheNetwork: this computer's host name,
// with and without .local, and for a host name with dots its first label with
// .local, are served only when the server listens where other computers can
// reach it: on an address that is not a loopback address, 0.0.0.0 and ::
// included. On a loopback address, or when the address is not known, they are
// refused like any other name, while the --web-addr name and the names in
// SCTERM_WEB_HOSTS are served either way.
func TestWebServesThisComputersNamesOnlyOnTheNetwork(t *testing.T) {
	for _, c := range []struct {
		hostname string
		own      []string // its names
		others   []string // never its names
	}{
		{"Box.Example.COM",
			[]string{"box.example.com", "BOX.Example.com.:6969", "box.example.com.local", "box.local", "Box.Local:6969"},
			[]string{"box", "box:6969", "example.com", "www.box.example.com", "box.example.local", "box.lan", "other.local"}},
		{"Box.local", // with .local already
			[]string{"box", "BOX:6969", "box.", "box.local", "Box.Local.:6969"},
			[]string{"box.local.local", "www.box", "box.lan", "other.local", "local"}},
	} {
		for _, l := range []struct {
			ip      string
			network bool
		}{
			{"192.0.2.10", true}, {"2001:db8::10", true}, {"0.0.0.0", true}, {"::", true},
			{"127.0.0.1", false}, {"127.0.1.1", false}, {"::1", false}, {"::ffff:127.0.0.1", false}, {"", false},
		} {
			names, _ := webHostNames(net.ParseIP(l.ip), c.hostname, "mirror.lan", "proxy.example.org")
			for _, host := range c.own {
				if got := webHostAllowed(host, names); got != l.network {
					t.Errorf("host name %s, listening on %q: %q allowed = %v, want %v", c.hostname, l.ip, host, got, l.network)
				}
			}
			for _, host := range c.others {
				if webHostAllowed(host, names) {
					t.Errorf("host name %s, listening on %q: %q allowed, want refused", c.hostname, l.ip, host)
				}
			}
			for _, host := range []string{"mirror.lan", "proxy.example.org:443"} {
				if !webHostAllowed(host, names) {
					t.Errorf("host name %s, listening on %q: %q refused, want allowed", c.hostname, l.ip, host)
				}
			}
		}
	}
}

// TestWebHostsEntriesMustBeNames: an entry of SCTERM_WEB_HOSTS is a name, or a
// name and a port, with spaces around it allowed and empty entries skipped.
// Anything else, and a name that starts with a dot or has two dots in a row,
// which no request could match either, is handed back to be reported, in
// order, and is not served, nor are its parts. One final dot is allowed.
func TestWebHostsEntriesMustBeNames(t *testing.T) {
	names, ignored := webHostNames(net.ParseIP("127.0.0.1"), "", "",
		" Mirror.lan ,, http://phone.lan,phone.lan/,phone.lan:8443/,*.lan,a.lan b.lan,a.lan;b.lan,user@phone.lan,"+
			":8443,::1,[::1,proxy.example.org:8443 ,my_box.lan,.example.lan,box..lan,box.lan..,..,b\u00fccher.lan,box.lan.,")
	want := []string{"http://phone.lan", "phone.lan/", "phone.lan:8443/", "*.lan", "a.lan b.lan", "a.lan;b.lan",
		"user@phone.lan", ":8443", "::1", "[::1", ".example.lan", "box..lan", "box.lan..", "..", "b\u00fccher.lan"}
	if !slices.Equal(ignored, want) {
		t.Errorf("ignored entries %q, want %q", ignored, want)
	}
	for _, host := range []string{"mirror.lan", "proxy.example.org", "my_box.lan:6969", "box.lan"} {
		if !webHostAllowed(host, names) {
			t.Errorf("%q refused, want allowed", host)
		}
	}
	for _, host := range []string{"phone.lan", "a.lan", "b.lan", "lan", "user", ".example.lan", "example.lan",
		"box..lan", "box.lan..", "..", "xn--bcher-kva.lan"} {
		if webHostAllowed(host, names) {
			t.Errorf("%q allowed, want refused", host)
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
