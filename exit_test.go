package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// scterm exits as scrcpy does: 0 for a normal end, 1 when it could not start
// (a mistake in the command line included) or a command failed, and 2 when
// the device was lost during the session, in the terminal UI, with --web or
// --window, and with --no-tui.
// ---------------------------------------------------------------------------

// sessionPacket starts a video stream: the top bit set, then 1080 by 2400.
var sessionPacket = []byte{0x80, 0, 0, 0, 0, 0, 0x04, 0x38, 0, 0, 0x09, 0x60}

// closedHere is a media socket whose device sends head, after which this end
// is closed, as a peer session closes its streams when the target ends it.
func closedHere(t *testing.T, head ...[]byte) net.Conn {
	t.Helper()
	client, device := net.Pipe()
	go func() {
		for _, b := range head {
			if _, err := device.Write(b); err != nil {
				break
			}
		}
		client.Close()
	}()
	t.Cleanup(func() { device.Close() })
	return client
}

// TestOnlyALostDeviceExitsTwo: in the terminal UI, the stream it shows ending
// after it started, because reading from the device failed, exits 2: the
// device hung up, also inside a packet, or this end was closed. A stream that
// never started is a failure to start, 1: declined, a configuration error, an
// unknown codec, no session packet, or the device hanging up before any of it.
func TestOnlyALostDeviceExitsTwo(t *testing.T) {
	mediaPacket := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 100} // 100 bytes follow
	for _, tc := range []struct {
		name      string
		audioOnly bool
		head      [][]byte
		closeHere bool
		status    int
		says      string
	}{
		{"started, then gone", false, [][]byte{[]byte("h264"), sessionPacket}, false, 2, "stream ended: EOF"},
		{"gone inside a packet", false, [][]byte{[]byte("h264"), sessionPacket, mediaPacket, make([]byte, 10)}, false, 2, "packet: unexpected EOF"},
		{"closed here", false, [][]byte{[]byte("h264"), sessionPacket}, true, 2, "stream ended: io: read/write on closed pipe"},
		{"audio only, started, then gone", true, [][]byte{[]byte("opus")}, false, 2, "audio stream ended: EOF"},
		{"audio only, gone inside a packet", true, [][]byte{[]byte("opus"), mediaPacket, make([]byte, 10)}, false, 2, "audio packet: unexpected EOF"},
		{"raw audio only, started, then gone", true, [][]byte{{0, 'r', 'a', 'w'}}, false, 2, "audio raw: EOF"},
		{"raw audio only, gone inside a packet", true, [][]byte{{0, 'r', 'a', 'w'}, mediaPacket, make([]byte, 10)}, false, 2, "audio raw packet: unexpected EOF"},
		{"declined", false, [][]byte{{0, 0, 0, 0}}, false, 1, "video stream disabled by device (code=0)"},
		{"configuration error", false, [][]byte{{0, 0, 0, 1}}, false, 1, "video stream disabled by device (code=1)"},
		{"unknown codec", false, [][]byte{[]byte("h265")}, false, 1, "unsupported video codec 0x68323635"},
		{"gone before the codec", false, nil, false, 1, "video codec id: EOF"},
		{"gone before the session packet", false, [][]byte{[]byte("h264")}, false, 1, "session header: EOF"},
		{"a media packet first", false, [][]byte{[]byte("h264"), make([]byte, 12)}, false, 1, "expected session header first"},
		{"audio only, configuration error", true, [][]byte{{0, 0, 0, 1}}, false, 1, "audio config error"},
	} {
		var conn net.Conn
		if tc.closeHere {
			conn = closedHere(t, tc.head...)
		} else {
			conn = deviceStream(t, tc.head...)
		}
		sess := &session{deviceName: "Pixel 8", video: conn}
		if tc.audioOnly {
			sess = &session{deviceName: "Pixel 8", audio: conn}
		}
		err := waitLost(t, streamApp(sess).watchStreams())
		if got := exitStatus(err); got != tc.status || !strings.Contains(fmt.Sprint(err), tc.says) {
			t.Errorf("%s: %v: exit status %d, want %d and an error saying %q", tc.name, err, got, tc.status, tc.says)
		}
	}
	if got := exitStatus(nil); got != 0 {
		t.Errorf("a quit: exit status %d, want 0", got)
	}
}

// TestWebExitsTwoWhenTheDeviceIsLost: with --web or --window, the video
// stream ending after it started exits 2; a device declining video exits 1.
func TestWebExitsTwoWhenTheDeviceIsLost(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	for _, tc := range []struct {
		name   string
		head   [][]byte
		status int
	}{
		{"lost", [][]byte{[]byte("h264"), sessionPacket}, 2},
		{"declined", [][]byte{{0, 0, 0, 0}}, 1},
	} {
		video, device := net.Pipe()
		t.Cleanup(func() { device.Close() })
		cfg := config{web: true, webAddr: "127.0.0.1"}
		sess := &session{deviceName: "testphone", video: video}
		a := &app{sess: sess, cfg: cfg, events: make(chan inputEvent, 64)}
		a.stream = &streamState{cfg: cfg, sess: sess}
		a.web = newWebServer(cfg, sess, a.stream, nil, nil, a)
		t.Cleanup(a.web.stop)

		ran := make(chan error, 1)
		go func() { ran <- a.runWeb() }()
		select {
		case <-a.web.ready:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: web server never started listening", tc.name)
		}
		for _, b := range tc.head {
			if _, err := device.Write(b); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		}
		device.Close()
		select {
		case err := <-ran:
			if got := exitStatus(err); got != tc.status {
				t.Errorf("%s: runWeb ended with %v: exit status %d, want %d", tc.name, err, got, tc.status)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: runWeb kept running after the video stream ended", tc.name)
		}
	}
}

// TestHeadlessExitsTwoWhenTheDeviceIsLost: with --no-tui, the video stream
// ending ends the run with its error, for main to print ("scterm: video: ...")
// once: exit 2 when it ended after it started, 1 when the device declined
// video.
func TestHeadlessExitsTwoWhenTheDeviceIsLost(t *testing.T) {
	// runHeadless takes these signals for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	for _, tc := range []struct {
		name   string
		head   [][]byte
		status int
		err    string
	}{
		{"lost", [][]byte{[]byte("h264"), sessionPacket}, 2, "video: stream ended: EOF"},
		{"declined", [][]byte{{0, 0, 0, 0}}, 1, "video: video stream disabled by device (code=0)"},
	} {
		a := streamApp(&session{deviceName: "Pixel 8", video: deviceStream(t, tc.head...)})
		a.cfg.noTUI = true
		var err error
		ended := false
		stderr := captureStderr(t, func() {
			ran := make(chan error, 1)
			go func() { ran <- a.runHeadless() }()
			select {
			case err = <-ran:
				ended = true
			case <-time.After(5 * time.Second):
			}
		})
		if !ended {
			t.Fatalf("%s: runHeadless kept running after the video stream ended", tc.name)
		}
		if got := exitStatus(err); got != tc.status || fmt.Sprint(err) != tc.err || stderr != "" {
			t.Errorf("%s: runHeadless ended with %v (exit status %d) and printed %q; want %q, exit status %d and nothing printed",
				tc.name, err, got, stderr, tc.err, tc.status)
		}
	}
}

// stopSignal is a session socket that says on stderr when it is closed, so a
// child process's stderr shows whether the session stopped before the exit.
type stopSignal struct{ net.Conn }

func (c stopSignal) Close() error {
	fmt.Fprintln(os.Stderr, "(session stopped)")
	return c.Conn.Close()
}

// TestSessionEndExitsWithItsStatus: main hands how the session ended to
// endSession, which stops the session, then prints the error's line and exits
// with its status, since the exit skips main's deferred stop: 2 for a lost
// device, 1 for any other error. A quit returns and leaves the stop to main.
// Runs endSession in a child process.
func TestSessionEndExitsWithItsStatus(t *testing.T) {
	ends := map[string]error{
		"lost":   fmt.Errorf("video: %w", lostError{fmt.Errorf("stream ended: %w", io.EOF)}),
		"failed": errors.New("video: decode: invalid data found when processing input"),
		"quit":   nil,
	}
	if name, ok := os.LookupEnv("SCTERM_TEST_SESSION_END"); ok {
		video, _ := net.Pipe()
		endSession(&session{deviceName: "Pixel 8", video: stopSignal{video}}, ends[name])
		fmt.Fprintln(os.Stderr, "(endSession returned)")
		os.Exit(0)
	}
	for _, tc := range []struct {
		name   string
		status int
		stderr string
	}{
		{"lost", 2, "(session stopped)\nscterm: video: stream ended: EOF\n"},
		{"failed", 1, "(session stopped)\nscterm: video: decode: invalid data found when processing input\n"},
		{"quit", 0, "(endSession returned)\n"},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSessionEndExitsWithItsStatus$")
		cmd.Env = append(os.Environ(), "SCTERM_TEST_SESSION_END="+tc.name)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		status := 0
		var exit *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exit) {
			status = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if status != tc.status || stderr.String() != tc.stderr {
			t.Errorf("%s: status %d, stderr %q; want %d, %q", tc.name, status, stderr.String(), tc.status, tc.stderr)
		}
	}
}

// TestCommandLineExitStatus: a mistake in the command line is a failure to
// start, status 1, with its one line on stderr, as are adb missing and a
// device that has not allowed USB debugging; the help and the version exit 0.
// Each runs main in a child process with nothing, or only a fake adb, on PATH
// and a scratch home, so none can reach a real device.
func TestCommandLineExitStatus(t *testing.T) {
	if args, ok := os.LookupEnv("SCTERM_TEST_MAIN_ARGS"); ok {
		os.Args = []string{"scterm"}
		if args != "" {
			os.Args = append(os.Args, strings.Split(args, "\n")...)
		}
		main()
		os.Exit(0) // as when main returns, without the test's own report
	}
	unauthorized := t.TempDir()
	script := "#!/bin/sh\nprintf 'List of devices attached\\nR5CT\\tunauthorized\\n'\n"
	if err := os.WriteFile(filepath.Join(unauthorized, "adb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	const hint = "; run scterm -h for the commands and flags\n"
	for _, tc := range []struct {
		args   []string
		path   string // PATH for the run; empty has no adb
		status int
		stdout string // stdout's first line
		stderr string
	}{
		{[]string{"--bogus"}, "", 1, "", "scterm: flag provided but not defined: -bogus" + hint},
		{[]string{"frobnicate"}, "", 1, "", `scterm: unknown command "frobnicate"` + hint},
		{[]string{"--web", "peers"}, "", 1, "", "scterm: \"peers\" is a command, not a flag value: run it on its own, as scterm peers\n"},
		{[]string{"--video=false", "--audio=false", "--control=false"}, "", 1, "",
			"scterm: --video, --audio and --control are all off, so there is nothing to show or control\n"},
		{[]string{"--no-tui", "--web"}, "", 1, "", "scterm: --web/--window and --no-tui are mutually exclusive\n"},
		{[]string{"version", "x"}, "", 1, "", "scterm: usage: scterm version (it takes no arguments)\n"},
		{[]string{"update", "--bogus"}, "", 1, "", "scterm: usage: scterm update (it takes no arguments)\n"},
		{[]string{"peers", "--bogus"}, "", 1, "", "scterm: usage: scterm peers [--check]\n"},
		{[]string{"forget"}, "", 1, "", "scterm: usage: scterm forget NAME (scterm peers lists the names)\n"},
		{nil, "", 1, "", "scterm dev\nscterm: adb not found: install Android platform-tools (often packaged as android-tools or adb), " +
			"or use --peer to reach the scterm Android app without it\n"},
		{nil, unauthorized, 1, "", "scterm dev\nscterm: R5CT has not allowed USB debugging from this computer: unlock it, " +
			"accept the \"Allow USB debugging?\" prompt, and run scterm again\n"},
		{[]string{"-h"}, "", 0, "scterm - scrcpy in your terminal", ""},
		{[]string{"help"}, "", 0, "scterm - scrcpy in your terminal", ""},
		{[]string{"version"}, "", 0, "scterm dev", ""},
	} {
		home := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=^TestCommandLineExitStatus$")
		cmd.Env = []string{"SCTERM_TEST_MAIN_ARGS=" + strings.Join(tc.args, "\n"),
			"PATH=" + tc.path, "HOME=" + home, "SCTERM_HOME=" + filepath.Join(home, "scterm")}
		if dir, ok := os.LookupEnv("GOCOVERDIR"); ok {
			// Under go test -cover the child warns on stderr without it.
			cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
		}
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		status := 0
		var exit *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exit) {
			status = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("scterm %s: %v", strings.Join(tc.args, " "), err)
		}
		first, _, _ := strings.Cut(stdout.String(), "\n")
		if status != tc.status || first != tc.stdout || stderr.String() != tc.stderr {
			t.Errorf("scterm %s: status %d, stdout %q, stderr %q; want %d, %q, %q",
				strings.Join(tc.args, " "), status, first, stderr.String(), tc.status, tc.stdout, tc.stderr)
		}
	}
}
