package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The adb link puts each socket where it belongs, whichever streams are on and
// whichever tunnel adb gives, and a device side that never answers ends the
// wait with a reason. The fakes play the scrcpy server, which opens its
// sockets in the order video, audio, control, the enabled ones only
// (DesktopConnection.open in third_party/scrcpy-server-src).
// ---------------------------------------------------------------------------

const fakeDeviceName = "Fake Pixel"

var streamNames = []string{"video", "audio", "control"}

// serveSockets sends what the server sends on its sockets, in its order: the
// device name on the first, then, so a test can tell them apart, each
// socket's own name.
func serveSockets(conns []net.Conn, names []string) {
	var meta [64]byte
	copy(meta[:], fakeDeviceName)
	conns[0].Write(meta[:])
	for i, c := range conns {
		fmt.Fprintf(c, "%-8s", names[i])
	}
}

// dialReverse connects to ln as the server does through adb reverse, one
// socket per enabled stream, each already holding what the server sends.
func dialReverse(t *testing.T, ln net.Listener, enabled []bool) {
	t.Helper()
	var conns []net.Conn
	var names []string
	for i, on := range enabled {
		if !on {
			continue
		}
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		conns = append(conns, c)
		names = append(names, streamNames[i])
	}
	serveSockets(conns, names)
}

// fakeForward plays adb forward on ln, with the server behind it. adb accepts
// a connection even while nothing listens on the device, then closes it;
// listening says whether the server listens yet. Once it does, the server
// accepts one socket per enabled stream, sends the dummy byte on the first as
// soon as it has it (unless silent), and serves them.
func fakeForward(t *testing.T, ln net.Listener, enabled []bool, listening func() bool, silent bool) {
	t.Helper()
	var mu sync.Mutex
	var accepted []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range accepted {
			c.Close()
		}
	})
	accept := func() net.Conn {
		c, err := ln.Accept()
		if err != nil {
			return nil
		}
		mu.Lock()
		accepted = append(accepted, c)
		mu.Unlock()
		return c
	}
	go func() {
		var conns []net.Conn
		var names []string
		for i, on := range enabled {
			if !on {
				continue
			}
			c := accept()
			for len(conns) == 0 && c != nil && !listening() {
				c.Close()
				c = accept()
			}
			if c == nil {
				return
			}
			if len(conns) == 0 {
				if silent {
					return
				}
				c.Write([]byte{0})
			}
			conns = append(conns, c)
			names = append(names, streamNames[i])
		}
		serveSockets(conns, names)
	}()
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func portOf(ln net.Listener) uint16 { return uint16(ln.Addr().(*net.TCPAddr).Port) }

// connectWithin runs s.connect, failing the test if it still waits after d.
func connectWithin(t *testing.T, s *session, enabled []bool, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.connect(enabled[0], enabled[1], enabled[2]) }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("connect still waited after %s", d)
		return nil
	}
}

// checkSlots fails the test unless each enabled stream's slot holds that
// stream's socket and each disabled one's slot is empty.
func checkSlots(t *testing.T, s *session, enabled []bool) {
	t.Helper()
	for i, slot := range []net.Conn{s.video, s.audio, s.control} {
		switch {
		case !enabled[i] && slot != nil:
			t.Errorf("%s is off, but its slot holds the %s socket", streamNames[i], socketIn(slot))
		case enabled[i] && slot == nil:
			t.Errorf("the %s slot is empty", streamNames[i])
		case enabled[i]:
			if got := socketIn(slot); got != streamNames[i] {
				t.Errorf("the %s slot holds the %s socket", streamNames[i], got)
			}
		}
	}
	if s.deviceName != fakeDeviceName {
		t.Errorf("device name %q, want %q", s.deviceName, fakeDeviceName)
	}
}

// socketIn says which socket conn is, by the name the fake server sent on it.
func socketIn(conn net.Conn) string {
	buf := make([]byte, 8)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "unknown (" + err.Error() + ")"
	}
	return strings.TrimSpace(string(buf))
}

func closeSlots(s *session) {
	for _, c := range []net.Conn{s.video, s.audio, s.control} {
		if c != nil {
			c.Close()
		}
	}
}

func shortConnectTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := connectTimeout
	t.Cleanup(func() { connectTimeout = old })
	connectTimeout = d
}

// TestSocketsLandInTheirSlots: with --video=false the first socket the server
// opens is audio, and so on; each must still land in its own slot, through
// adb reverse and through adb forward alike.
func TestSocketsLandInTheirSlots(t *testing.T) {
	for _, mode := range []string{"reverse", "forward"} {
		for mask := 1; mask < 8; mask++ {
			enabled := []bool{mask&1 != 0, mask&2 != 0, mask&4 != 0}
			var on []string
			for i, e := range enabled {
				if e {
					on = append(on, streamNames[i])
				}
			}
			t.Run(mode+"/"+strings.Join(on, "+"), func(t *testing.T) {
				ln := listenLoopback(t)
				s := &session{serverOut: &tailBuffer{max: 4096}, serverDone: make(chan struct{})}
				if mode == "reverse" {
					s.listener = ln
					dialReverse(t, ln, enabled)
				} else {
					s.forwardMode, s.forwardPort = true, portOf(ln)
					fakeForward(t, ln, enabled, func() bool { return true }, false)
				}
				defer closeSlots(s)
				if err := connectWithin(t, s, enabled, 5*time.Second); err != nil {
					t.Fatalf("connect: %v", err)
				}
				checkSlots(t, s, enabled)
			})
		}
	}
}

// TestForwardWaitsForTheServerToListen: through adb forward, a dial succeeds
// before the server listens and adb then hangs up; the link tries again
// until the server's dummy byte comes, instead of failing on the first.
func TestForwardWaitsForTheServerToListen(t *testing.T) {
	ln := listenLoopback(t)
	enabled := []bool{false, true, true}
	hangups := 0
	fakeForward(t, ln, enabled, func() bool { hangups++; return hangups > 3 }, false)
	s := &session{forwardMode: true, forwardPort: portOf(ln), serverOut: &tailBuffer{max: 4096}, serverDone: make(chan struct{})}
	defer closeSlots(s)
	if err := connectWithin(t, s, enabled, 5*time.Second); err != nil {
		t.Fatalf("connect: %v", err)
	}
	checkSlots(t, s, enabled)
}

// TestForwardDummyByteWaitIsBounded: a server that accepts the first socket
// but never sends its dummy byte ends the wait at the connect deadline, with
// a reason.
func TestForwardDummyByteWaitIsBounded(t *testing.T) {
	shortConnectTimeout(t, 200*time.Millisecond)
	ln := listenLoopback(t)
	enabled := []bool{true, true, true}
	fakeForward(t, ln, enabled, func() bool { return true }, true)
	s := &session{forwardMode: true, forwardPort: portOf(ln), serverOut: &tailBuffer{max: 4096}, serverDone: make(chan struct{})}
	defer closeSlots(s)
	before := linkGoroutines()
	err := connectWithin(t, s, enabled, 3*time.Second)
	if err == nil || !strings.Contains(err.Error(), "did not connect its video socket within 200ms") {
		t.Fatalf("connect: %v", err)
	}
	checkNothingLeftRunning(t, before)
}

// TestForwardDialIsBounded: with nothing on the forwarded port, dialing
// stops at the connect deadline too, and leaves nothing of connect running.
func TestForwardDialIsBounded(t *testing.T) {
	shortConnectTimeout(t, 200*time.Millisecond)
	ln := listenLoopback(t)
	port := portOf(ln)
	ln.Close()
	s := &session{forwardMode: true, forwardPort: port, serverOut: &tailBuffer{max: 4096}, serverDone: make(chan struct{})}
	before := linkGoroutines()
	err := connectWithin(t, s, []bool{true, false, true}, 3*time.Second)
	if err == nil || !strings.Contains(err.Error(), "dial 127.0.0.1:"+strconv.Itoa(int(port))) {
		t.Fatalf("connect: %v", err)
	}
	checkNothingLeftRunning(t, before)
}

// TestStartErrorsCleanWhatTheServerSaid: the errors that end a start quote the
// server's last lines, which are the device's text, and main prints them on
// stderr: each line comes without the escape sequences and control characters
// in it, a line of nothing else is left out, and clean lines read as before.
// The note is the same in each error that quotes it: here, a server that
// stopped, a socket adb reverse never brought, and a dial through adb forward.
func TestStartErrorsCleanWhatTheServerSaid(t *testing.T) {
	shortConnectTimeout(t, 100*time.Millisecond)
	for _, tc := range terminalTricks {
		out := &tailBuffer{max: 4096}
		fmt.Fprintf(out, "[server] INFO: Device: %s\n\x1b\a\u009b\n[server] ERROR: %s\n", tc.text, tc.text)
		note := "; it said:\n  [server] INFO: Device: " + tc.clean + "\n  [server] ERROR: " + tc.clean

		stopped := &session{serverOut: out, serverDone: make(chan struct{})}
		close(stopped.serverDone)
		if got, want := stopped.serverStoppedError().Error(), "the scrcpy server on the device stopped before connecting"+note; got != want {
			t.Errorf("%s: server stopped:\n got %q\nwant %q", tc.name, got, want)
		}

		reverse := &session{listener: listenLoopback(t), serverOut: out, serverDone: make(chan struct{})}
		err := connectWithin(t, reverse, []bool{true, false, false}, 3*time.Second)
		if want := "the device did not connect its video socket within 100ms" + note; err == nil || err.Error() != want {
			t.Errorf("%s: adb reverse:\n got %q\nwant %q", tc.name, err, want)
		}

		ln := listenLoopback(t)
		port := portOf(ln)
		ln.Close()
		forward := &session{forwardMode: true, forwardPort: port, serverOut: out, serverDone: make(chan struct{})}
		err = connectWithin(t, forward, []bool{true, false, false}, 3*time.Second)
		if dial := fmt.Sprintf("dial 127.0.0.1:%d: ", port); err == nil || !strings.HasPrefix(err.Error(), dial) || !strings.HasSuffix(err.Error(), note) {
			t.Errorf("%s: adb forward:\n got %q\nwant %q, the dial's error, then %q", tc.name, err, dial, note)
		}
	}
}

// TestForwardStopsWhenTheServerStops: while adb keeps hanging up, a server
// that exits ends the wait at once and says what it printed.
func TestForwardStopsWhenTheServerStops(t *testing.T) {
	ln := listenLoopback(t)
	enabled := []bool{true, true, true}
	fakeForward(t, ln, enabled, func() bool { return false }, false)
	out := &tailBuffer{max: 4096}
	fmt.Fprintln(out, "[server] ERROR: Could not create the listening socket")
	s := &session{forwardMode: true, forwardPort: portOf(ln), serverOut: out, serverDone: make(chan struct{})}
	time.AfterFunc(100*time.Millisecond, func() { close(s.serverDone) })
	err := connectWithin(t, s, enabled, 3*time.Second)
	if err == nil || !strings.Contains(err.Error(), "stopped before connecting") ||
		!strings.Contains(err.Error(), "Could not create the listening socket") {
		t.Fatalf("connect: %v", err)
	}
}

// TestForwardDummyByteWaitEndsWhenTheServerStops: the server has the first
// socket but has not sent the dummy byte, and the connection stays open; a
// server that exits then ends the wait at once, not at the deadline, says
// what it printed, and leaves nothing of connect running.
func TestForwardDummyByteWaitEndsWhenTheServerStops(t *testing.T) {
	shortConnectTimeout(t, 5*time.Second)
	ln := listenLoopback(t)
	enabled := []bool{true, true, true}
	fakeForward(t, ln, enabled, func() bool { return true }, true)
	out := &tailBuffer{max: 4096}
	fmt.Fprintln(out, "[server] ERROR: Exception on thread Thread[main,5,main]")
	s := &session{forwardMode: true, forwardPort: portOf(ln), serverOut: out, serverDone: make(chan struct{})}
	defer closeSlots(s)
	before := linkGoroutines()
	time.AfterFunc(100*time.Millisecond, func() { close(s.serverDone) })
	err := connectWithin(t, s, enabled, time.Second)
	if err == nil || !strings.Contains(err.Error(), "stopped before connecting") ||
		!strings.Contains(err.Error(), "Exception on thread Thread[main,5,main]") {
		t.Fatalf("connect: %v", err)
	}
	checkNothingLeftRunning(t, before)
}

// TestForwardSocketsOutliveTheWait: once connect has returned the sockets,
// nothing it started is left running, and the server exiting later does not
// close them from this side.
func TestForwardSocketsOutliveTheWait(t *testing.T) {
	ln := listenLoopback(t)
	enabled := []bool{true, true, true}
	fakeForward(t, ln, enabled, func() bool { return true }, false)
	s := &session{forwardMode: true, forwardPort: portOf(ln), serverOut: &tailBuffer{max: 4096}, serverDone: make(chan struct{})}
	defer closeSlots(s)
	before := linkGoroutines()
	if err := connectWithin(t, s, enabled, 5*time.Second); err != nil {
		t.Fatalf("connect: %v", err)
	}
	checkNothingLeftRunning(t, before)
	close(s.serverDone)
	time.Sleep(100 * time.Millisecond)
	checkSlots(t, s, enabled)
}

// linkGoroutines lists the goroutines running code from link.go, by their
// "goroutine N" headers.
func linkGoroutines() map[string]string {
	buf := make([]byte, 64<<10)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	found := map[string]string{}
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "/link.go:") {
			header, _, _ := strings.Cut(g, " [")
			found[header] = g
		}
	}
	return found
}

// checkNothingLeftRunning fails the test if a goroutine that was not in
// before still runs link.go code a moment after connect returned.
func checkNothingLeftRunning(t *testing.T, before map[string]string) {
	t.Helper()
	var left []string
	for start := time.Now(); time.Since(start) < time.Second; time.Sleep(10 * time.Millisecond) {
		left = left[:0]
		for header, stack := range linkGoroutines() {
			if _, ok := before[header]; !ok {
				left = append(left, stack)
			}
		}
		if len(left) == 0 {
			return
		}
	}
	t.Errorf("connect returned, but left running:\n%s", strings.Join(left, "\n\n"))
}

// fakeADBForward is an adb whose reverse fails and whose forward works only
// for tcp:$FAKE_ADB_PORT. Its server listens, as the real one does, only when
// started with tunnel_forward=true, a moment after it starts; without it, the
// server connects out, finds nothing there and stops.
const fakeADBForward = `#!/bin/sh
echo "$*" >> "$FAKE_ADB_DIR/calls"
case "$1" in
reverse)
	[ "$2" = --remove ] && exit 0
	echo "error: closed" >&2
	exit 1 ;;
forward)
	[ "$2" = --remove ] && exit 0
	[ "$2" = "tcp:$FAKE_ADB_PORT" ] && exit 0
	echo "error: cannot bind listener" >&2
	exit 1 ;;
shell)
	case "$*" in
	*tunnel_forward=true*)
		sleep 0.3
		: > "$FAKE_ADB_DIR/listening"
		exec sleep 30 ;;
	esac
	echo "[server] ERROR: Exception on thread Thread[main,5,main]"
	echo "java.io.IOException: Connection refused"
	echo "	at android.net.LocalSocketImpl.connectLocal(Native Method)"
	exit 1 ;;
esac
exit 0
`

// TestForwardModeAsksTheServerToListen: when adb reverse fails, the link
// tunnels with adb forward, so the server must listen for the sockets
// instead of connecting out; and the sockets still land in their slots.
func TestForwardModeAsksTheServerToListen(t *testing.T) {
	var ln net.Listener
	for port := portFirst; port <= portLast && ln == nil; port++ {
		ln, _ = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}
	if ln == nil {
		t.Skipf("no free port in %d:%d for the fake adb forward", portFirst, portLast)
	}
	t.Cleanup(func() { ln.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "adb"), []byte(fakeADBForward), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_ADB_DIR", dir)
	t.Setenv("FAKE_ADB_PORT", strconv.Itoa(int(portOf(ln))))
	enabled := []bool{false, true, true}
	fakeForward(t, ln, enabled, func() bool {
		_, err := os.Stat(filepath.Join(dir, "listening"))
		return err == nil
	}, false)

	s, err := newSession("")
	if err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() { err = s.start(false, true, true, []string{"video=false"}) })
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.stop()
	if !strings.Contains(stderr, "using adb forward") {
		t.Errorf("stderr %q does not say it fell back to adb forward", stderr)
	}
	if !s.forwardMode {
		t.Error("the link did not fall back to adb forward")
	}
	checkSlots(t, s, enabled)
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	var launch string
	for _, line := range strings.Split(string(calls), "\n") {
		if strings.HasPrefix(line, "shell ") {
			launch = line
		}
	}
	if !strings.Contains(launch, " tunnel_forward=true") {
		t.Errorf("server launched as %q, without tunnel_forward=true", launch)
	}
	if !strings.Contains(string(calls), fmt.Sprintf("forward --remove tcp:%d\n", portOf(ln))) {
		t.Errorf("the forward was not removed once connected; adb calls:\n%s", calls)
	}
}
