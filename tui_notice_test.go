package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// While the terminal UI owns the screen, runtime messages are notices in the
// status row and come back on stderr once the terminal is restored; the end of
// the stream it shows ends the session with an error naming the device; and an
// empty picture says what it is waiting for.
// ---------------------------------------------------------------------------

// startNotices routes messages to the terminal UI for one test, as run does
// while it owns the screen.
func startNotices(t *testing.T) <-chan struct{} {
	t.Helper()
	wake := notices.start()
	t.Cleanup(func() { notices.stop(io.Discard) })
	select {
	case <-wake: // left over from an earlier test
	default:
	}
	return wake
}

// forgetLogOnce lets logOnce report msgs again after the test, so that a
// repeated run (-count) sees them as new.
func forgetLogOnce(t *testing.T, msgs ...string) {
	t.Cleanup(func() {
		logOnceMu.Lock()
		defer logOnceMu.Unlock()
		for _, m := range msgs {
			delete(logOnceSeen, m)
		}
	})
}

// ageNotice makes the notice on show d older, as if d had gone by.
func ageNotice(d time.Duration) {
	notices.mu.Lock()
	notices.until -= int64(d)
	notices.mu.Unlock()
}

// TestNoticeTakesTheStatusRowThenGoes: a message is not printed while the
// terminal UI runs. It replaces the state and stats after the hints, the
// newest one wins, and after about 4 s the row is back to normal.
func TestNoticeTakesTheStatusRowThenGoes(t *testing.T) {
	wake := startNotices(t)
	a := statusApp(80, "Pixel 8")

	if out := captureStderr(t, func() { noticef("scterm: saved scterm-1.ppm (80x46)\n") }); out != "" {
		t.Errorf("a message reached the terminal while the UI owns it: %q", out)
	}
	select {
	case <-wake:
	default:
		t.Error("the run loop was not asked to repaint the status row")
	}
	if got, want := a.statusLine(), statusHintsText+" | saved scterm-1.ppm (80x46)"; got != want {
		t.Errorf("with a notice:\n got %q\nwant %q", got, want)
	}

	noticef("scterm: inject key: broken pipe\n")
	if got, want := a.statusLine(), statusHintsText+" | inject key: broken pipe"; got != want {
		t.Errorf("the newest notice does not win:\n got %q\nwant %q", got, want)
	}

	ageNotice(3500 * time.Millisecond)
	if got := a.statusLine(); !strings.Contains(got, "inject key: broken pipe") {
		t.Errorf("the notice went before 4 s: %q", got)
	}
	ageNotice(600 * time.Millisecond)
	if got, want := a.statusLine(), statusHintsText+" | vol 100% · Pixel 8 · 1080x2400 16.0ms 62.5fps"; got != want {
		t.Errorf("after 4.1 s:\n got %q\nwant %q", got, want)
	}
}

// TestNoticeTextIsCleaned: a notice is shown with its control characters
// taken out, so an escape sequence in a peer's name or in an error cannot
// reach the terminal through the status row.
func TestNoticeTextIsCleaned(t *testing.T) {
	startNotices(t)
	a := statusApp(80, "Pixel 8")
	noticef("scterm: peer \"Evil\x1b]0;x\x07Phone\" took control\n")
	if got, want := a.statusLine(), statusHintsText+" | peer \"Evil ]0;x Phone\" took control"; got != want {
		t.Errorf("with control characters:\n got %q\nwant %q", got, want)
	}
}

// waitStatusRow waits for the status row to show want.
func waitStatusRow(t *testing.T, tr *tui, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		tr.mu.Lock()
		got := tr.statusDrawn
		tr.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("status row shows %q, want %q", got, want)
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRunLoopPaintsNotices: the run loop, not the goroutine that sent it,
// paints a notice at once, and its 500 ms tick takes an old one down.
func TestRunLoopPaintsNotices(t *testing.T) {
	wake := startNotices(t)
	a := statusApp(80, "Pixel 8")
	a.tui.running = true
	done := make(chan error, 1)
	out := captureStdout(t, func() {
		go func() { done <- a.loop(wake, make(chan error)) }()
		go noticef("scterm: saved scterm-2.ppm (80x46)\n")
		waitStatusRow(t, a.tui, statusHintsText+" | saved scterm-2.ppm (80x46)")
		ageNotice(5 * time.Second)
		a.events <- inputEvent{kind: evTick}
		waitStatusRow(t, a.tui, statusHintsText+" | vol 100% · Pixel 8 · 1080x2400 16.0ms 62.5fps")
		a.events <- inputEvent{kind: evQuit}
		if err := <-done; err != nil {
			t.Errorf("a quit ended the loop with %v", err)
		}
	})
	if !strings.Contains(out, "\x1b[24;1H") {
		t.Errorf("nothing was painted on the status row (24): %q", out)
	}
}

// captureTerminal runs fn with stdout and stderr on one pipe, so their order
// is kept, as on a terminal that shows both.
func captureTerminal(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); got <- b }()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := setStderr(w)
	func() {
		defer func() {
			os.Stdout = oldOut
			setStderr(oldErr)
		}()
		fn()
	}()
	_ = w.Close()
	return string(<-got)
}

// terminalUntil is captureTerminal for output that another goroutine writes:
// after fn it reads the pipe until want has come through (or 5 s have gone
// by), so that goroutine is done with stdout and stderr before they are put
// back. It returns what it read.
func terminalUntil(t *testing.T, want string, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := setStderr(w)
	defer func() {
		os.Stdout = oldOut
		setStderr(oldErr)
		_ = w.Close()
	}()
	fn()
	_ = r.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got []byte
	buf := make([]byte, 4096)
	for !strings.Contains(string(got), want) {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	return string(got)
}

// TestReplayAfterTheTerminalIsBack: restoring the terminal prints the messages
// the UI showed, once each with a count for repeats, after the normal screen
// is back (printed on the alternate screen, they would go with it); later
// messages go straight to stderr. TestReplayGoesToStderr pins the stream.
func TestReplayAfterTheTerminalIsBack(t *testing.T) {
	startNotices(t)
	a := newTestApp(nil)
	a.tui = &tui{cols: 80, rows: 23, running: true}
	restore := a.terminalRestorer(nil)
	forgetLogOnce(t, "scterm: audio -> replay-test-sink\n")

	noticef("scterm: saved scterm-3.ppm (80x46)\n")
	for i := 0; i < 3; i++ {
		noticef("scterm: inject key: broken pipe\n")
	}
	logOnce("scterm: audio -> replay-test-sink\n")
	logOnce("scterm: audio -> replay-test-sink\n")

	out := captureTerminal(t, restore)
	want := "scterm: saved scterm-3.ppm (80x46)\n" +
		"scterm: inject key: broken pipe (3 times)\n" +
		"scterm: audio -> replay-test-sink\n"
	leave := strings.Index(out, "\x1b[?1049l")
	if leave < 0 {
		t.Fatalf("the normal screen was not restored: %q", out)
	}
	if !strings.HasSuffix(out, want) || strings.Index(out, "scterm:") < leave {
		t.Errorf("the replay is not what follows the normal screen's return (at %d):\n got %q\nwant it to end with %q",
			leave, out, want)
	}

	stderr := captureStderr(t, func() {
		noticef("scterm: after the end\n")
		restore()
	})
	if stderr != "scterm: after the end\n" {
		t.Errorf("after the restore: %q", stderr)
	}
}

// TestReplayGoesToStderr: the replay is written to stderr, not stdout, so a
// redirect such as `2>log` keeps it.
func TestReplayGoesToStderr(t *testing.T) {
	startNotices(t)
	a := newTestApp(nil)
	a.tui = &tui{cols: 80, rows: 23, running: true}
	restore := a.terminalRestorer(nil)
	noticef("scterm: saved scterm-4.ppm (80x46)\n")

	var stderr string
	stdout := captureStdout(t, func() { stderr = captureStderr(t, restore) })
	if want := "scterm: saved scterm-4.ppm (80x46)\n"; stderr != want {
		t.Errorf("stderr:\n got %q\nwant %q", stderr, want)
	}
	if strings.Contains(stdout, "scterm:") {
		t.Errorf("the replay reached stdout: %q", stdout)
	}
}

// TestReplayKeepsTheLatestLines: the replay is capped at 50 lines; the oldest
// go first and are counted.
func TestReplayKeepsTheLatestLines(t *testing.T) {
	startNotices(t)
	for i := 1; i <= 60; i++ {
		noticef("scterm: message %d\n", i)
	}
	var b strings.Builder
	notices.stop(&b)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 51 {
		t.Fatalf("replay has %d lines, want 51: %q", len(lines), b.String())
	}
	if lines[0] != "scterm: 10 earlier messages not shown" || lines[1] != "scterm: message 11" || lines[50] != "scterm: message 60" {
		t.Errorf("replay starts %q, %q and ends %q", lines[0], lines[1], lines[50])
	}
}

// TestLogOnceGoesWhereTheScreenIs: logOnce prints on stderr when no terminal
// UI runs, as it always has, and is a notice (still once) while one does.
func TestLogOnceGoesWhereTheScreenIs(t *testing.T) {
	forgetLogOnce(t, "scterm: logonce-test before\n", "scterm: logonce-test during\n")
	if got := captureStderr(t, func() { logOnce("scterm: logonce-test before\n") }); got != "scterm: logonce-test before\n" {
		t.Errorf("without the terminal UI: %q", got)
	}
	wake := startNotices(t)
	got := captureStderr(t, func() {
		logOnce("scterm: logonce-test during\n")
		logOnce("scterm: logonce-test during\n")
	})
	if got != "" {
		t.Errorf("with the terminal UI, stderr got %q", got)
	}
	select {
	case <-wake:
	default:
		t.Error("the run loop was not asked to repaint the status row")
	}
	if n := notices.current(timeNowUnixNano()); n != "logonce-test during" {
		t.Errorf("notice %q", n)
	}
	var b strings.Builder
	notices.stop(&b)
	if b.String() != "scterm: logonce-test during\n" {
		t.Errorf("replay %q", b.String())
	}
}

// deviceStream is the device's end of a media socket: it sends head, as the
// server does when the stream starts, then hangs up like an unplugged device.
func deviceStream(t *testing.T, head ...[]byte) net.Conn {
	t.Helper()
	client, device := net.Pipe()
	go func() {
		for _, b := range head {
			if _, err := device.Write(b); err != nil {
				break
			}
		}
		device.Close()
	}()
	t.Cleanup(func() { client.Close() })
	return client
}

// waitLost waits for the end of the stream to reach the run loop.
func waitLost(t *testing.T, lost <-chan error) error {
	t.Helper()
	select {
	case err := <-lost:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the stream never reached the run loop")
		return nil
	}
}

// streamApp is a terminal UI app on sess.
func streamApp(sess *session) *app {
	a := newTestApp(nil)
	a.sess = sess
	a.audio = &audioSink{}
	a.stream = newStreamState(sess, a.cfg, nil)
	return a
}

// TestLostStreamEndsTheSession: the video stream ending, or the audio stream
// when there is no video, ends the session with an error naming the device
// and the cause; the audio stream ending under a picture is only a notice.
func TestLostStreamEndsTheSession(t *testing.T) {
	// The stream opens with its codec ("h264") and a session packet (top bit
	// set, then 1080 by 2400), and then the device goes away.
	header := []byte{0x80, 0, 0, 0, 0, 0, 0x04, 0x38, 0, 0, 0x09, 0x60}

	err := waitLost(t, streamApp(&session{deviceName: "Pixel 8", video: deviceStream(t, []byte("h264"), header)}).watchStreams())
	if got, want := fmt.Sprint(err), "lost the video stream from Pixel 8: stream ended: EOF"; got != want {
		t.Errorf("video: %q, want %q", got, want)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("video: the cause is not kept: %v", err)
	}

	err = waitLost(t, streamApp(&session{deviceName: "Pixel\x1b[2J 8", audio: deviceStream(t, []byte("opus"))}).watchStreams())
	if got, want := fmt.Sprint(err), "lost the audio stream from Pixel [2J 8: audio stream ended: EOF"; got != want {
		t.Errorf("audio only: %q, want %q", got, want)
	}

	startNotices(t)
	video, device := net.Pipe() // the picture goes on
	t.Cleanup(func() { device.Close() })
	lost := streamApp(&session{deviceName: "Pixel 8", video: video, audio: deviceStream(t, []byte("opus"))}).watchStreams()
	deadline := time.Now().Add(5 * time.Second)
	for notices.current(timeNowUnixNano()) != "audio: audio stream ended: EOF" {
		if time.Now().After(deadline) {
			t.Fatalf("no notice for the audio stream ending; showing %q", notices.current(timeNowUnixNano()))
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-lost:
		t.Errorf("the session ended with the audio stream under a picture: %v", err)
	default:
	}
}

// closeSignal is a connection whose Close says so. A demuxer defers it, so it
// marks the demuxer's return.
type closeSignal struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *closeSignal) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// demuxerEnded waits for the demuxer reading c to return, and gives its
// goroutine a moment to report how it ended.
func demuxerEnded(t *testing.T, c *closeSignal) {
	t.Helper()
	select {
	case <-c.closed:
		time.Sleep(100 * time.Millisecond)
	case <-time.After(5 * time.Second):
		t.Error("the audio demuxer did not end")
	}
}

// TestTeardownSocketErrorsAreQuiet: once the terminal UI has given the
// terminal back, on a quit or on the loss of the picture, sess.stop closes
// the sockets, and the audio demuxer's "use of closed network connection" is
// not printed. Another audio error after that still is.
func TestTeardownSocketErrorsAreQuiet(t *testing.T) {
	startNotices(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Write([]byte("opus")) // then nothing, like a quiet device
		}
		accepted <- c
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if device := <-accepted; device != nil {
		t.Cleanup(func() { device.Close() })
	}
	audio := &closeSignal{Conn: conn, closed: make(chan struct{})}
	a := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: audio})
	a.tui = &tui{cols: 80, rows: 23, running: true}
	restore := a.terminalRestorer(nil)
	a.watchStreams()
	out := captureTerminal(t, func() {
		restore()    // the run loop has ended
		conn.Close() // sess.stop
		demuxerEnded(t, audio)
	})
	if strings.Contains(out, "audio") {
		t.Errorf("the socket closed on the way out was reported: %q", out)
	}

	broken := deviceStream(t, []byte{0, 0, 0, 1}) // codec 1: a config error
	b := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: broken})
	b.tui = &tui{cols: 80, rows: 23, running: true}
	restore = b.terminalRestorer(nil)
	const want = "scterm: audio: audio config error\n"
	if out := terminalUntil(t, want, func() {
		restore()
		b.watchStreams()
	}); !strings.Contains(out, want) {
		t.Errorf("an audio error after the terminal was given back was not reported: %q", out)
	}
}

// TestRunLoopEndsWithTheLostStream: the run loop returns the end of the stream
// as its error; a quit is not one.
func TestRunLoopEndsWithTheLostStream(t *testing.T) {
	a := newTestApp(nil)
	want := errors.New("lost the video stream from Pixel 8: stream ended: EOF")
	lost := make(chan error, 1)
	lost <- want
	if err := a.loop(nil, lost); err != want {
		t.Errorf("loop returned %v, want %v", err, want)
	}
	a.events <- inputEvent{kind: evQuit}
	if err := a.loop(nil, make(chan error)); err != nil {
		t.Errorf("a quit ended the loop with %v", err)
	}
}

// TestLostDeviceIsPrintedAndExitsTwo: the error main gets from run is printed
// on stderr. The device going away after the stream started exits 2, as
// scrcpy does when the device disconnects; a device that declines video at
// the start is a failure to start, status 1. Runs main's exitWith in a child
// process, on what the terminal UI's stream watcher returns.
func TestLostDeviceIsPrintedAndExitsTwo(t *testing.T) {
	// "h264", then a session packet (top bit set, then 1080 by 2400), then
	// the device goes away; or a zero codec id, the device declining video.
	heads := map[string][][]byte{
		"lost":     {[]byte("h264"), {0x80, 0, 0, 0, 0, 0, 0x04, 0x38, 0, 0, 0x09, 0x60}},
		"declined": {{0, 0, 0, 0}},
	}
	if name := os.Getenv("SCTERM_TEST_LOST_DEVICE"); name != "" {
		a := streamApp(&session{deviceName: "Pixel 8", video: deviceStream(t, heads[name]...)})
		exitWith(waitLost(t, a.watchStreams()))
	}
	for _, tc := range []struct {
		name   string
		status int
		stderr string
	}{
		{"lost", 2, "scterm: lost the video stream from Pixel 8: stream ended: EOF\n"},
		{"declined", 1, "scterm: lost the video stream from Pixel 8: video stream disabled by device (code=0)\n"},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLostDeviceIsPrintedAndExitsTwo$")
		cmd.Env = append(os.Environ(), "SCTERM_TEST_LOST_DEVICE="+tc.name)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != tc.status {
			t.Errorf("%s: exit: %v, want status %d", tc.name, err, tc.status)
		}
		if got := stderr.String(); got != tc.stderr {
			t.Errorf("%s: stderr %q, want %q", tc.name, got, tc.stderr)
		}
	}
}

// placeholderAt matches the placeholder painted at a cursor position, allowing
// for colour codes in between.
func placeholderAt(row, col int, text string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("\x1b[%d;%dH", row, col)) + `(\x1b\[[0-9;]*m)*` + regexp.QuoteMeta(text))
}

// videoSocket stands in for a session's video connection.
func videoSocket(t *testing.T) net.Conn {
	c, other := net.Pipe()
	t.Cleanup(func() { c.Close(); other.Close() })
	return c
}

// TestPlaceholderBeforeTheFirstFrame: with no frame yet the picture area says
// what it waits for, centred in the 40x9 picture (row 5): the device name with
// its control characters taken out and cut to the width, that video is off,
// or that the session has none (a peer pairing without view). The first frame
// replaces it.
func TestPlaceholderBeforeTheFirstFrame(t *testing.T) {
	cases := []struct {
		name    string
		video   bool
		socket  bool
		device  string
		col     int
		want    string
		mustNot string
	}{
		{"waiting", true, true, "Pixel 8", 5, "Waiting for video from Pixel 8…", ""},
		{"video off", false, false, "Pixel 8", 7, "Video is off (--video=false)", ""},
		{"no video in the session", true, false, "Pixel 8", 10, "No video from Pixel 8", ""},
		{"control characters", true, true, "Evil\x1b]0;x\x07Phone", 1, "Waiting for video from Evil ]0;x Phone…", "\x1b]"},
		{"wide name", true, true, strings.Repeat("界", 30), 1, "Waiting for video from " + strings.Repeat("界", 8) + "…", ""},
	}
	for _, tc := range cases {
		a := newTestApp(nil)
		a.cfg.video = tc.video
		a.sess = &session{deviceName: tc.device}
		if tc.socket {
			a.sess.video = videoSocket(t)
		}
		a.tui = &tui{cols: 40, rows: 9, running: true}
		out := captureStdout(t, a.paintPlaceholder)
		if !placeholderAt(5, tc.col, tc.want).MatchString(out) {
			t.Errorf("%s: want %q at 5;%d in %q", tc.name, tc.want, tc.col, out)
		}
		if strings.Contains(out, "\x07") || tc.mustNot != "" && strings.Contains(out, tc.mustNot) {
			t.Errorf("%s: a control sequence from the name reached the screen: %q", tc.name, out)
		}
	}

	a := newTestApp(nil)
	a.cfg.video = true
	a.sess = &session{deviceName: "Pixel 8", video: videoSocket(t)}
	a.tui = newRenderTUI(40, 9) // a frame has been drawn
	if out := captureStdout(t, a.paintPlaceholder); strings.Contains(out, "Waiting") {
		t.Errorf("the placeholder covered a frame: %q", out)
	}
}

// TestPlaceholderFollowsAResize: before the first frame a resize puts the
// placeholder back in the middle; once there is a picture, a resize waits for
// the next frame as before.
func TestPlaceholderFollowsAResize(t *testing.T) {
	withTermSize(t, 40, 10)
	a := newTestApp(nil)
	a.cfg.video = true
	a.sess = &session{deviceName: "Pixel 8", video: videoSocket(t)}
	a.tui = &tui{running: true}
	a.tui.resize()
	captureStdout(t, a.paintPlaceholder)

	termSizeOverride = struct{ cols, rows int }{60, 20} // a 60x19 picture
	out := captureStdout(t, a.onResize)
	if !placeholderAt(10, 15, "Waiting for video from Pixel 8…").MatchString(out) {
		t.Errorf("placeholder not re-centred after a resize: %q", out)
	}

	a.tui.lastRGB = make([]byte, 60*19*2*4)
	a.tui.lastW, a.tui.lastH = 60, 38
	termSizeOverride = struct{ cols, rows int }{50, 20}
	if out := captureStdout(t, a.onResize); strings.Contains(out, "Waiting") || strings.Contains(out, "\x1b[2J") {
		t.Errorf("a resize after the first frame cleared the picture: %q", out)
	}
}

// terminalTricks is text from a device that could drive the terminal it is
// printed on: a title change (OSC ... BEL), a screen clear (CSI), the CSI
// that is one C1 character (U+009B), and a bare Esc. Each is shown as clean
// is; text without them stays as it was.
var terminalTricks = []struct{ name, text, clean string }{
	{"OSC", "Pixel\x1b]0;x\x07 8", "Pixel ]0;x 8"},
	{"CSI", "Pixel\x1b[2J 8", "Pixel [2J 8"},
	{"C1", "Pixel\u009b2J 8", "Pixel 2J 8"},
	{"Esc", "Pixel\x1b 8", "Pixel 8"},
	{"clean", "Pixel 8 (2)", "Pixel 8 (2)"},
}

// talkingTarget is a paired target on loopback that welcomes the first control
// session as welcome says, then hands its connection to the test, which sends
// the target's envelopes on it (docs/PEER_PROTOCOL.md).
type talkingTarget struct {
	id    *peer.Identity
	ln    net.Listener
	conns chan net.Conn
}

func newTalkingTarget(t *testing.T, welcome peer.Message) *talkingTarget {
	t.Helper()
	id, err := peer.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{id.Certificate}, ClientAuth: tls.RequireAnyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	target := &talkingTarget{id: id, ln: ln, conns: make(chan net.Conn, 1)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(target.conns)
			return
		}
		defer conn.Close()
		if _, err := peer.ReadFrame(conn); err != nil || peer.WriteFrame(conn, &welcome) != nil {
			close(target.conns)
			return
		}
		target.conns <- conn
		io.Copy(io.Discard, conn) // pings, and the bye
	}()
	return target
}

func (target *talkingTarget) port() int { return target.ln.Addr().(*net.TCPAddr).Port }

// conn is the connection of the session the target welcomed.
func (target *talkingTarget) conn(t *testing.T) net.Conn {
	t.Helper()
	select {
	case conn, ok := <-target.conns:
		if !ok {
			t.Fatal("the fake target did not welcome the session")
		}
		return conn
	case <-time.After(5 * time.Second):
		t.Fatal("no session came to the fake target")
		return nil
	}
}

// say sends the target's envelope m: the byte 0xFE, then a frame.
func say(t *testing.T, conn net.Conn, m peer.Message) {
	t.Helper()
	if _, err := conn.Write([]byte{0xfe}); err != nil {
		t.Fatal(err)
	}
	if err := peer.WriteFrame(conn, &m); err != nil {
		t.Fatal(err)
	}
}

// TestPeerNoticesAreCleaned: the notices of a peer session carry the target's
// own text: its name, the name of the device that took control, and the words
// of a refusal. The status row always showed them cleaned; stderr and the
// replay at exit, which print each line as it is, now get them without the
// escape sequences and control characters in them too, and get clean text as
// before.
func TestPeerNoticesAreCleaned(t *testing.T) {
	for _, tc := range terminalTricks {
		t.Run(tc.name, func(t *testing.T) {
			welcome := controlWelcome
			welcome.Lease = peer.LeaseViewer
			welcome.Device = &peer.DeviceInfo{Name: tc.text}
			target := newTalkingTarget(t, welcome)
			pairedAt(t, target.id.Fingerprint.Hex(), "127.0.0.1", target.port())
			searchFinds(t)
			name := tc.clean
			lines := []string{
				"another device has control of " + name + "; this computer is viewing (--takeover takes it)",
				tc.clean + " took control of " + name + "; this computer is viewing (--takeover takes it back)",
				"this computer has control of " + name,
				name + " refused " + tc.clean + ": " + tc.clean,
				"input ignored: another device has control of " + name + " (--takeover takes it)",
			}
			forgetLogOnce(t, "scterm: "+lines[0]+"\n", "scterm: "+lines[4]+"\n")
			startNotices(t)

			s, err := newPeerSession("Pixel 8", controlOnly())
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer s.stop()
			waitNotice(t, lines[0])
			conn := target.conn(t)
			say(t, conn, peer.Message{Type: peer.TypeLease, State: peer.LeaseViewer, Holder: tc.text})
			waitNotice(t, lines[1])
			say(t, conn, peer.Message{Type: peer.TypeLease, State: peer.LeaseHeld})
			waitNotice(t, lines[2])
			say(t, conn, peer.Message{Type: peer.TypeError, Code: "denied", Message: tc.text, ControlType: tc.text})
			waitNotice(t, lines[3])
			say(t, conn, peer.Message{Type: peer.TypeError, Code: "no_lease"})
			waitNotice(t, lines[4])

			var replay strings.Builder
			notices.stop(&replay)
			if want := "scterm: " + strings.Join(lines, "\nscterm: ") + "\n"; replay.String() != want {
				t.Errorf("replay:\n%q\nwant\n%q", replay.String(), want)
			}
		})
	}
}
