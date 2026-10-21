package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode"
)

// ---------------------------------------------------------------------------
// scterm does sound work on this computer only for a session that plays its
// sound here, and its displays say the same about the sound: the browser's
// hello and status, the terminal's status row and the statistics line of
// --no-tui. Nothing here reaches this computer's sound server: libpulse is
// isolated, and its server is a listener of the test.
// ---------------------------------------------------------------------------

// socketDir is a new directory with a short path, for unix sockets, whose
// paths are limited to 107 bytes. It is removed when the test ends.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.Getenv("GOTMPDIR"), "sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// listenPulse listens at each of paths as a sound server would, hangs up on
// every connection and counts them. It calls onConnect, if not nil, as each
// connection comes.
func listenPulse(t *testing.T, onConnect func(), paths ...string) *atomic.Int32 {
	t.Helper()
	accepted := new(atomic.Int32)
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				if onConnect != nil {
					onConnect()
				}
				accepted.Add(1)
				c.Close()
			}
		}()
	}
	return accepted
}

// fakeADBDevice is an adb for main: it drops -s SERIAL, its reverse fails, its
// forward works only for tcp:$FAKE_ADB_PORT, and the server it starts listens
// at once (fakeForward serves it) and runs until it is killed. Any other call
// succeeds.
const fakeADBDevice = `#!/bin/sh
[ "$1" = -s ] && shift 2
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
	: > "$FAKE_ADB_DIR/listening"
	exec "$FAKE_ADB_SLEEP" 30 ;;
esac
exit 0
`

// TestMainSilencesLeftoversOnlyWhenItPlaysHere: main silences the leftover
// streams of other scterms only when the session plays its sound on this
// computer: not with --no-audio, nor with --web without --audio-dup, where
// the browser is the only output. With --no-audio nothing connects to the
// sound server at all. Each case runs main in a child process with a scratch
// home, a fake adb whose device this test serves through adb forward (as in
// TestForwardModeAsksTheServerToListen), and a sound server that is a
// listener of this test and hangs up. The child notes each call of the
// cleanup (killAppStreams). The cleanup must come before the session's own
// stream opens, or it would silence that stream too: a session that silences
// connects to the sound server only after the note. The device sends no
// H.264, so the session ends with status 1 right after the cleanup and the
// open of the sound stream.
func TestMainSilencesLeftoversOnlyWhenItPlaysHere(t *testing.T) {
	if log, ok := os.LookupEnv("SCTERM_TEST_CLEANUP_LOG"); ok {
		killAppStreams = func(server, app string) int {
			if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintf(f, "%s|%s\n", server, app)
				f.Close()
			}
			return 0
		}
		os.Args = append([]string{"scterm"}, strings.Split(os.Getenv("SCTERM_TEST_MAIN_ARGS"), "\n")...)
		main()
		os.Exit(0) // as when main returns, without the test's own report
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep for the fake device's server")
	}
	for _, tc := range []struct {
		name            string
		args            []string
		silences, opens bool
	}{
		{"no-tui", []string{"--no-tui"}, true, true},
		{"no-tui no-audio", []string{"--no-tui", "--no-audio"}, false, false},
		{"web", []string{"--web", "--web-port", "0"}, false, true},
		{"web audio-dup", []string{"--web", "--web-port", "0", "--audio-dup"}, true, true},
		{"web no-audio", []string{"--web", "--web-port", "0", "--no-audio"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ln net.Listener
			for port := portFirst; port <= portLast && ln == nil; port++ {
				ln, _ = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			}
			if ln == nil {
				t.Skipf("no free port in %d:%d for the fake adb forward", portFirst, portLast)
			}
			dir, sock := t.TempDir(), socketDir(t)
			fakeForward(t, ln, []bool{true, !slices.Contains(tc.args, "--no-audio"), true}, func() bool {
				_, err := os.Stat(filepath.Join(dir, "listening"))
				return err == nil
			}, false)
			server, xdg := filepath.Join(sock, "server", "native"), filepath.Join(sock, "xdg")
			cleanupLog := filepath.Join(dir, "cleanup.log")
			var early atomic.Int32 // connections that came before any cleanup call
			accepted := listenPulse(t, func() {
				if calls, _ := os.ReadFile(cleanupLog); len(calls) == 0 {
					early.Add(1)
				}
			}, server, filepath.Join(xdg, "pulse", "native"))
			bin, conf := filepath.Join(dir, "bin"), filepath.Join(dir, "client.conf")
			for _, d := range []string{bin, filepath.Join(dir, "home"), filepath.Join(dir, "tmp")} {
				if err := os.Mkdir(d, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "adb"), []byte(fakeADBDevice), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(conf, []byte("autospawn = no\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainSilencesLeftoversOnlyWhenItPlaysHere$")
			cmd.Env = []string{
				"SCTERM_TEST_MAIN_ARGS=" + strings.Join(append([]string{"-s", "FAKE"}, tc.args...), "\n"),
				"SCTERM_TEST_CLEANUP_LOG=" + cleanupLog,
				"PATH=" + bin, "HOME=" + filepath.Join(dir, "home"), "SCTERM_HOME=" + filepath.Join(dir, "scterm"),
				"TMPDIR=" + filepath.Join(dir, "tmp"), "XDG_RUNTIME_DIR=" + xdg,
				"PULSE_SERVER=unix:" + server, "PULSE_CLIENTCONFIG=" + conf, "PULSE_RUNTIME_PATH=" + filepath.Join(dir, "pulse"),
				"FAKE_ADB_DIR=" + dir, "FAKE_ADB_PORT=" + strconv.Itoa(int(portOf(ln))), "FAKE_ADB_SLEEP=" + sleep,
			}
			if d, ok := os.LookupEnv("GOCOVERDIR"); ok {
				// Under go test -cover the child warns on stderr without it.
				cmd.Env = append(cmd.Env, "GOCOVERDIR="+d)
			}
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
			var stderr strings.Builder
			cmd.Stderr = &stderr
			name := "scterm " + strings.Join(tc.args, " ")
			if status := childStatus(cmd.Run()); status != 1 || !strings.Contains(stderr.String(), "unsupported video codec") {
				t.Fatalf("%s: status %d, stderr %q; want 1, after a session that started", name, status, stderr.String())
			}
			calls, _ := os.ReadFile(cleanupLog)
			want := ""
			if tc.silences {
				want = "|scterm\n|sct\n" // both names, on the first server, which answers
			}
			if string(calls) != want {
				t.Errorf("%s: the cleanup ran with %q, want %q", name, calls, want)
			}
			if n := accepted.Load(); (n > 0) != tc.opens {
				t.Errorf("%s: %d connections to the sound server, want some: %v", name, n, tc.opens)
			}
			if n := early.Load(); tc.silences && n > 0 {
				t.Errorf("%s: %d connections to the sound server before the cleanup, which would silence the session's own stream", name, n)
			}
		})
	}
}

// TestSessionWithoutSoundStaysOffTheSoundServer: a session without sound, with
// --no-audio or from a peer target that grants no sound, opens no stream on
// the sound server. Nothing connects to it, so the session does not show in
// the desktop's mixer and does not wait for a server that is not there. Its
// sink still takes every use, the follower does not start, and no line says
// where the sound plays. The statistics line says off, and the status row
// says "audio off" when the sound was asked for, and nothing of it with
// --no-audio. A session with sound does connect, so the listeners would have
// seen it.
func TestSessionWithoutSoundStaysOffTheSoundServer(t *testing.T) {
	// runWeb and runHeadless take these signals for the app and never give them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	dir, sock := isolatePulse(t), socketDir(t)
	old := audioPause
	audioPause = func(time.Duration) {}
	t.Cleanup(func() { audioPause = old })
	server := filepath.Join(sock, "server", "native")
	t.Setenv("PULSE_SERVER", "unix:"+server)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(sock, "xdg"))
	accepted := listenPulse(t, nil, server, filepath.Join(sock, "xdg", "pulse", "native"))
	// A pactl that notes its runs: opening a sink and following the desktop's
	// sink run it.
	pactlRuns := filepath.Join(dir, "pactl-runs")
	pactl := "#!/bin/sh\necho \"$*\" >> '" + pactlRuns + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "pactl"), []byte(pactl), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		cfg  config
		off  bool // the status row says "audio off" (the sound was asked for)
	}{
		{"--no-audio --no-tui", config{noTUI: true}, false},
		{"--no-audio --web", config{web: true, webAddr: "127.0.0.1"}, false},
		{"a peer target that grants no sound, --no-tui", config{audio: true, noTUI: true, usePeer: true}, true},
		{"a peer target that grants no sound, --web --audio-dup",
			config{audio: true, web: true, audioDup: true, usePeer: true, webAddr: "127.0.0.1"}, true},
	} {
		a := newApp(&session{deviceName: "Pixel 8", video: videoSocket(t)}, tc.cfg)
		if n := accepted.Load(); n != 0 {
			t.Errorf("%s: %d connections to the sound server", tc.name, n)
		}
		a.audio.writePCM16(make([]byte, 16))
		a.toggleLocalMute()
		a.adjustLocalVolume(-10)
		a.audio.setGain(40)
		a.audio.toggleMute()
		a.audio.silent.Store(true)
		if _, ok := a.audioFollower(); ok {
			t.Errorf("%s: the follower would start", tc.name)
		}
		if got := audioState(a); got != "off" {
			t.Errorf("%s: the statistics line says audio=%s, want off", tc.name, got)
		}
		a.tui = &tui{cols: 200, rows: 23}
		if row := a.statusLine(); strings.Contains(row, "vol ") || strings.Contains(row, "audio off") != tc.off {
			t.Errorf("%s: status row %q", tc.name, row)
		}
		a.tui = nil
		run, wants := a.runHeadless, []string(nil)
		if tc.cfg.web {
			run, wants = a.runWeb, []string{"web display on"}
		}
		out := stderrUntil(t, a, run, wants...)
		for _, wrong := range []string{"audio", "browser", "sound"} {
			if strings.Contains(out, wrong) {
				t.Errorf("%s: says %q:\n%s", tc.name, wrong, out)
			}
		}
		a.shutdown()
		a.audio.close()
	}
	if runs, err := os.ReadFile(pactlRuns); err == nil {
		t.Errorf("pactl ran for sessions without sound: %q", runs)
	}

	a := newApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: videoSocket(t)}, config{audio: true, noTUI: true})
	a.shutdown()
	if accepted.Load() == 0 {
		t.Error("a session with sound did not connect either: the listeners show nothing")
	}
}

// TestAudioDisabledIsSaidOnlyWithSound: the "audio disabled" line, which says
// why this computer plays none of the session's sound, comes when the session
// has sound and its sink did not open, and only then: not when the sink
// opened, not with --no-audio, and not from a peer target that grants no
// sound. The terminal display prints it through the same function before its
// alternate screen, which needs a terminal; --no-tui is run here.
func TestAudioDisabledIsSaidOnlyWithSound(t *testing.T) {
	// runHeadless takes these signals for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	isolatePulse(t) // no adb or pactl to reach anything with
	failed := &audioSink{gain: 256, pct: 100, err: errNoAudio()}
	line := "scterm: audio disabled: " + failed.errString() + "\n"
	sound := config{noTUI: true, audio: true}
	for _, tc := range []struct {
		name string
		cfg  config
		sink *audioSink
		want string
	}{
		{"the sink did not open", sound, failed, line},
		{"the sink opened", sound, &audioSink{gain: 256, pct: 100}, ""},
		{"--no-audio", config{noTUI: true}, noAudioSink(), ""},
		{"a peer target that grants no sound", config{noTUI: true, audio: true, usePeer: true}, noAudioSink(), ""},
	} {
		a := &app{cfg: tc.cfg, audio: tc.sink}
		if got := captureStderr(t, a.sayAudioDisabled); got != tc.want {
			t.Errorf("%s: printed %q, want %q", tc.name, got, tc.want)
		}
	}

	// A device that sends no sound (codec id 0): the line comes first, before
	// the one that says the device sends none.
	a := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: soundOff(t)})
	a.cfg, a.audio = sound, failed
	var out string
	// Its statistics line goes to stdout, and must not run into the test's output.
	captureStdout(t, func() { out = stderrUntil(t, a, a.runHeadless, "no sound from Pixel 8") })
	if !strings.HasPrefix(out, line) {
		t.Errorf("--no-tui printed %q, want it to start with %q", out, line)
	}
}

// soundTold is what a browser is told about the sound: the hello's audio, and
// the status's audio and hostAudio.
type soundTold struct {
	hello     bool
	audio     string
	hostAudio bool
}

// browserTold connects a browser to srv and returns what the first status and
// the hello say about the sound. Each field must have the JSON type readers
// expect.
func browserTold(t *testing.T, srv *webServer) soundTold {
	t.Helper()
	cl := dialWS(t, serverHost(srv))
	st := cl.nextJSON("status", 3*time.Second)
	cl.send(webEvent{Op: "hello", W: 480, H: 1080, Caps: "audio"})
	h := cl.nextJSON("hello", 3*time.Second)
	hello, okHello := h["audio"].(bool)
	audio, okAudio := st["audio"].(string)
	hostAudio, okHost := st["hostAudio"].(bool)
	if !okHello || !okAudio || !okHost {
		t.Fatalf("hello %v, status %v: want audio a boolean in the hello and a string in the status, and hostAudio a boolean", h, st)
	}
	return soundTold{hello, audio, hostAudio}
}

// webRunTells runs the --web main loop with sink as this computer's sound
// output, on sess, whose video sends nothing, and returns what a browser is
// told about the sound. With ended, the browser connects once ended is
// closed: once the device's audio stream has ended.
func webRunTells(t *testing.T, cfg config, sess *session, sink *audioSink, ended <-chan struct{}) soundTold {
	t.Helper()
	cfg.web, cfg.webAddr = true, "127.0.0.1"
	video, device := net.Pipe()
	defer device.Close()
	sess.video = video
	a := &app{sess: sess, cfg: cfg, audio: sink, events: make(chan inputEvent, 64)}
	a.stream = &streamState{cfg: cfg, sess: sess}
	a.web = newWebServer(cfg, sess, a.stream, sink, nil, a)
	t.Cleanup(a.web.stop)
	ran := make(chan error, 1)
	go func() { ran <- a.runWeb() }()
	select {
	case <-a.web.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the web server never started listening")
	}
	if ended != nil {
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatal("the device's audio stream did not end")
		}
	}
	told := browserTold(t, a.web)
	a.events <- inputEvent{kind: evQuit}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Error("the main loop did not quit")
	}
	return told
}

// TestBrowserIsToldTheSessionsSound: the browser's hello and status say
// whether the session's sound is on as the browser gets it: on while the
// session has a sound stream that the device has not turned off, whether or
// not this computer's sink opened, and off otherwise. hostAudio is true only
// when this computer plays the sound too. The same without an app, as the web
// tests run the server.
func TestBrowserIsToldTheSessionsSound(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	isolatePulse(t) // no pactl for a follower to run
	working := func() *audioSink { return &audioSink{gain: 256, pct: 100} }
	failed := func() *audioSink { return &audioSink{gain: 256, pct: 100, err: errNoAudio()} }
	on, off := soundTold{true, "on", false}, soundTold{false, "off", false}
	for _, tc := range []struct {
		name  string
		cfg   config
		sound bool // the session has a sound stream, which sends nothing yet
		sink  *audioSink
		want  soundTold
	}{
		{"this computer's sink did not open", config{audio: true}, true, failed(), on},
		{"--audio-dup, this computer's sink did not open", config{audio: true, audioDup: true}, true, failed(), on},
		{"--audio-dup", config{audio: true, audioDup: true}, true, working(), soundTold{true, "on", true}},
		{"the browser only", config{audio: true}, true, working(), on},
		{"--no-audio", config{}, false, working(), off},
		{"a peer target that grants no sound", config{audio: true, usePeer: true}, false, working(), off},
	} {
		sess := &session{deviceName: "Pixel 8"}
		if tc.sound {
			sess.audio = videoSocket(t)
		}
		if got := webRunTells(t, tc.cfg, sess, tc.sink, nil); got != tc.want {
			t.Errorf("%s: told %+v, want %+v", tc.name, got, tc.want)
		}
	}
	for _, dup := range []bool{false, true} {
		stream := &closeSignal{Conn: soundOff(t), closed: make(chan struct{})}
		sess := &session{deviceName: "Pixel 8", audio: stream}
		if got := webRunTells(t, config{audio: true, audioDup: dup}, sess, working(), stream.closed); got != off {
			t.Errorf("--audio-dup=%v, the device turned the sound off: told %+v, want %+v", dup, got, off)
		}
	}

	headless := func(sess *session, sink *audioSink, before func(*streamState)) soundTold {
		cfg := config{web: true, audio: true, webAddr: "127.0.0.1"}
		stream := &streamState{cfg: cfg, sess: sess}
		if before != nil {
			before(stream)
		}
		srv := newWebServer(cfg, sess, stream, sink, nil, nil)
		go func() { _ = srv.run(false) }()
		select {
		case <-srv.ready:
		case <-time.After(3 * time.Second):
			t.Fatal("the web server never started listening")
		}
		t.Cleanup(srv.stop)
		return browserTold(t, srv)
	}
	if got := headless(&session{deviceName: "Pixel 8", audio: videoSocket(t)}, nil, nil); got != on {
		t.Errorf("without an app, nor a sink on this computer: told %+v, want %+v", got, on)
	}
	got := headless(&session{deviceName: "Pixel 8", audio: soundOff(t)}, working(), func(s *streamState) {
		if err := s.runAudio(nil); !errors.Is(err, errAudioOff) {
			t.Errorf("runAudio: %v, want the device to turn the sound off", err)
		}
	})
	if got != off {
		t.Errorf("without an app, the device turned the sound off: told %+v, want %+v", got, off)
	}
}

// TestStatisticsLineSaysOffWithoutSound: the statistics line of --no-tui,
// where this computer's sink is what plays, says off with --no-audio, once
// the device has turned the sound off, when the sink did not open, and from a
// peer target that grants no sound; and muted or the volume otherwise. The
// PCM decoded, its peak and how far the audio lags the video come only while
// sound plays: not when it is off, nor before the first audio packet; and the
// lag only once a video packet has come too. Before, the line always had
// them, and without sound the lag was the video's own timestamp
// (avlag=214994453ms on the first real-device run).
func TestStatisticsLineSaysOffWithoutSound(t *testing.T) {
	headless := func(cfg config, audio net.Conn, sink *audioSink) *app {
		a := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: audio})
		a.cfg, a.audio = cfg, sink
		return a
	}
	sink := func(pct int, err error) *audioSink {
		s := &audioSink{gain: 256, pct: 100, err: err}
		s.setGain(pct)
		return s
	}
	sound := config{noTUI: true, audio: true}
	turnedOff := headless(sound, soundOff(t), sink(100, nil))
	captureStderr(t, func() { // the line that says so
		if err := turnedOff.playAudio(); err != nil {
			t.Errorf("playAudio: %v", err)
		}
	})
	const pcm = " pcm=8KB peak=1234"
	for _, tc := range []struct {
		name  string
		a     *app
		state string
	}{
		{"the sink plays", headless(sound, videoSocket(t), sink(100, nil)), "100%"},
		{"turned down", headless(sound, videoSocket(t), sink(40, nil)), "40%"},
		{"muted", headless(sound, videoSocket(t), sink(0, nil)), "muted"},
		{"the sink did not open", headless(sound, videoSocket(t), sink(100, errNoAudio())), "off"},
		{"--no-audio", headless(config{noTUI: true}, nil, sink(100, nil)), "off"},
		{"a peer target that grants no sound", headless(config{noTUI: true, audio: true, usePeer: true}, nil, noAudioSink()), "off"},
		{"the device turned the sound off", turnedOff, "off"},
	} {
		a, s := tc.a, tc.a.stream
		if got := audioState(a); got != tc.state {
			t.Errorf("%s: audio=%s, want %s", tc.name, got, tc.state)
		}
		plays := func(more string) string {
			if tc.state == "off" {
				return ""
			}
			return more
		}
		check := func(when, want string) {
			t.Helper()
			if got := a.statistics(); got != want {
				t.Errorf("%s, %s: %q, want %q", tc.name, when, got, want)
			}
		}
		check("before any packet", "0x0 fps=0 audio="+tc.state)
		// A video packet and its frame, with the stream's rate, on the device's
		// clock (microseconds since it started).
		s.lastVideoPts.Store(214994453000)
		s.currentFPS = 10
		a.frameW, a.frameH = 576, 1280
		check("after a video packet", "576x1280 fps=10 audio="+tc.state)
		// An audio packet, 120 ms before the video one, and its PCM.
		s.lastAudioPts.Store(214994333000)
		s.audioBytes = 8*1024 + 100
		s.audioPeak.Store(1234)
		check("after an audio packet too", "576x1280 fps=10 audio="+tc.state+plays(pcm+" avlag=120ms"))
		s.lastVideoPts.Store(0)
		check("after an audio packet, before any video", "576x1280 fps=10 audio="+tc.state+plays(pcm))
	}
}

// TestStatisticsLinePrinted: --no-tui prints the statistics line in brackets,
// over the one before it. Without sound, as on the first real-device run, it
// holds the size, the rate and audio=off. A shorter line, once the sound stops
// (here the sink's write fails), is padded to the longer one before it, so
// that nothing of that one is left on the screen.
func TestStatisticsLinePrinted(t *testing.T) {
	// runHeadless takes these signals for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	old := statisticsEvery
	statisticsEvery = 5 * time.Millisecond
	t.Cleanup(func() { statisticsEvery = old })
	// printed runs runHeadless on a until enough says it has printed enough,
	// and returns each line it printed.
	printed := func(a *app, enough func(lines []string) bool) []string {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		oldOut := os.Stdout
		os.Stdout = w
		lines := make(chan string, 1024)
		go func() {
			sc := bufio.NewScanner(r)
			sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
				if i := bytes.IndexByte(data, '\r'); i >= 0 {
					return i + 1, data[:i], nil
				}
				if atEOF && len(data) > 0 {
					return len(data), data, nil
				}
				return 0, nil, nil
			})
			for sc.Scan() {
				if sc.Text() != "" {
					lines <- sc.Text()
				}
			}
			close(lines)
		}()
		ran := make(chan error, 1)
		go func() { ran <- a.runHeadless() }()
		var got []string
		timeout := time.After(10 * time.Second)
	read:
		for !enough(got) {
			select {
			case l := <-lines:
				got = append(got, l)
			case <-timeout:
				t.Errorf("printed %q, and then nothing more that was asked for", got)
				break read
			}
		}
		a.events <- inputEvent{kind: evQuit}
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Error("runHeadless did not quit")
		}
		os.Stdout = oldOut
		w.Close()
		for range lines { // the rest, until the pipe is drained
		}
		r.Close()
		return got
	}

	quiet := streamApp(&session{deviceName: "NX769J", video: videoSocket(t)})
	quiet.cfg, quiet.audio = config{noTUI: true}, noAudioSink()
	quiet.frameW, quiet.frameH = 576, 1280
	quiet.stream.currentFPS = 10
	quiet.stream.lastVideoPts.Store(214994453000)
	for _, l := range printed(quiet, func(lines []string) bool { return len(lines) >= 2 }) {
		if want := "[576x1280 fps=10 audio=off]   "; l != want {
			t.Errorf("--no-audio: printed %q, want %q", l, want)
		}
	}

	loud := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t)})
	loud.cfg, loud.audio = config{noTUI: true, audio: true}, &audioSink{gain: 256, pct: 100}
	loud.frameW, loud.frameH = 576, 1280
	loud.stream.currentFPS = 10
	loud.stream.lastVideoPts.Store(214994453000)
	loud.stream.lastAudioPts.Store(214994333000)
	loud.stream.audioBytes = 8*1024 + 100
	loud.stream.audioPeak.Store(1234)
	failed := false
	lines := printed(loud, func(lines []string) bool {
		if len(lines) > 0 && !failed {
			failed = true
			loud.audio.writeFailed.Store(true)
		}
		return len(lines) > 0 && strings.Contains(lines[len(lines)-1], "audio=off")
	})
	const long, short = "[576x1280 fps=10 audio=100% pcm=8KB peak=1234 avlag=120ms]", "[576x1280 fps=10 audio=off]"
	if len(lines) < 2 {
		t.Fatalf("printed %q, want a line with sound and one without", lines)
	}
	if want := long + "   "; lines[0] != want {
		t.Errorf("with sound: printed %q, want %q", lines[0], want)
	}
	if got, want := lines[len(lines)-1], short+strings.Repeat(" ", len(long)-len(short))+"   "; got != want {
		t.Errorf("once the sound stopped: printed %q, want %q", got, want)
	}
}

// TestNoSoundReasonKeepsNoControlCharacters: the reason the server gives for
// turning the sound off is the device's text. The "no sound from" line keeps
// none of its control characters wherever it goes: the terminal's status row
// and its replay at exit, and stderr with --web or --window and with --no-tui.
func TestNoSoundReasonKeepsNoControlCharacters(t *testing.T) {
	// runWeb and runHeadless take these signals for the app and never give them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	isolatePulse(t) // no adb or pactl to reach anything with
	shortAudioOffWait(t, 50*time.Millisecond)
	const said = "[server] WARN: Audio disabled: \x1b]0;owned\x07not \x1b[31msupported\x1b[0m\r\b here\x7f\u0085!"
	const want = "no sound from Pixel 8: Audio disabled: ]0;owned not [31msupported [0m here !"
	clean := func(where, out string) {
		t.Helper()
		if strings.Count(out, "scterm: "+want+"\n") != 1 {
			t.Errorf("%s: %q, want the line %q once", where, out, "scterm: "+want)
		}
		if i := strings.IndexFunc(out, func(r rune) bool { return r != '\n' && unicode.IsControl(r) }); i >= 0 {
			t.Errorf("%s: a control character at %d: %q", where, i, out)
		}
	}

	startNotices(t)
	a := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: soundOff(t), serverOut: serverSaid(said)})
	a.cfg = config{audio: true}
	a.watchStreams()
	waitNotice(t, want)
	var replay strings.Builder
	notices.stop(&replay)
	clean("the replay at exit", replay.String())

	cfg := config{web: true, audio: true, webAddr: "127.0.0.1"}
	sess := &session{deviceName: "Pixel 8", audio: soundOff(t), serverOut: serverSaid(said)}
	clean("--web", webLinesUntil(t, cfg, sess, &audioSink{gain: 256, pct: 100}, "no sound"))

	b := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: soundOff(t), serverOut: serverSaid(said)})
	b.cfg = config{noTUI: true, audio: true}
	var out string
	// Its statistics line goes to stdout, and must not run into the test's output.
	captureStdout(t, func() { out = stderrUntil(t, b, b.runHeadless, "no sound") })
	clean("--no-tui", out)
}
