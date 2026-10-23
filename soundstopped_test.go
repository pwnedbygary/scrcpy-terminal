package main

import (
	"errors"
	"io"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"
)

// ---------------------------------------------------------------------------
// Once the session's sound has stopped while the picture goes on, nothing
// more comes to play on this computer. scterm closes its stream here, so that
// no silent scterm stream stays in the desktop's mixer, and stops following
// the desktop's output; the volume controls leave the volume alone; and
// --web and --window say how the sound stream ended, as the other displays
// do. Nothing here reaches this computer's sound server: the sinks were never
// opened, and pactl, where there is one, is the test's.
// ---------------------------------------------------------------------------

// sinkClosed fails the test unless sink has been closed and plays nothing.
func sinkClosed(t *testing.T, where string, sink *audioSink) {
	t.Helper()
	if !sink.closed.Load() || sink.plays() {
		t.Errorf("%s: the sink is closed %v and plays %v; want it closed, playing nothing",
			where, sink.closed.Load(), sink.plays())
	}
}

// stopsBy is each way the session's sound stops while the picture goes on:
// the device's end of the audio socket, and what the displays then say.
func stopsBy(t *testing.T) []struct {
	name  string
	audio func() net.Conn
	said  string
} {
	return []struct {
		name  string
		audio func() net.Conn
		said  string
	}{
		{"the device turns the sound off", func() net.Conn { return soundOff(t) }, "no sound from Pixel 8"},
		{"the stream ends", func() net.Conn { return deviceStream(t, []byte("opus")) }, "audio: audio stream ended: EOF"},
	}
}

// TestSoundThatStopsClosesTheStreamHere: in each display that plays the sound
// on this computer, the terminal, --no-tui, and --web or --window with
// --audio-dup, once the device has turned the sound off or its stream has
// ended, the sink is closed by the time the display says so: its stream is
// off the sound server, and it plays nothing more. A sink whose write failed
// earlier, which plays nothing already, is closed too. Before, the sink stayed
// open, silent, in the desktop's mixer until scterm quit.
func TestSoundThatStopsClosesTheStreamHere(t *testing.T) {
	// runWeb and runHeadless take these signals for the app and never give them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	isolatePulse(t) // no adb or pactl to reach anything with
	for _, way := range stopsBy(t) {
		failed := &audioSink{gain: 256, pct: 100}
		failed.writeFailed.Store(true)
		for _, tc := range []struct {
			where string
			sink  *audioSink
		}{
			{"in the terminal", &audioSink{gain: 256, pct: 100}},
			{"in the terminal, with a sink whose write failed", failed},
		} {
			startNotices(t)
			term := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: way.audio()})
			term.cfg, term.audio = config{audio: true}, tc.sink
			term.watchStreams()
			waitNotice(t, way.said)
			notices.stop(io.Discard)
			sinkClosed(t, way.name+", "+tc.where, term.audio)
		}

		h := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: way.audio()})
		h.cfg, h.audio = config{noTUI: true, audio: true}, &audioSink{gain: 256, pct: 100}
		// Its statistics line goes to stdout, and must not run into the test's output.
		captureStdout(t, func() { stderrUntil(t, h, h.runHeadless, "scterm: "+way.said) })
		sinkClosed(t, way.name+", with --no-tui", h.audio)

		sink := &audioSink{gain: 256, pct: 100}
		dup := config{web: true, audio: true, audioDup: true, webAddr: "127.0.0.1"}
		webLinesHere(t, dup, &session{deviceName: "Pixel 8", audio: way.audio()}, sink, "scterm: "+way.said)
		sinkClosed(t, way.name+", with --web --audio-dup", sink)
	}
}

// TestVolumeKeysLeaveTheSoundAloneOnceItStops: in the terminal, once the
// session's sound has stopped, Alt+M, Alt+- and Alt+= leave the volume here as
// it was, and the first of them says why, once: in the status row, and once in
// the replay at exit. The same when this computer's sink did not open, where
// the keys changed nothing before either, and said nothing until the sound
// stopped. Before, they went on setting the volume of a stream that played
// nothing, and said nothing.
func TestVolumeKeysLeaveTheSoundAloneOnceItStops(t *testing.T) {
	const why = "the sound is off, so its volume stays as it is"
	keys := [][]byte{{0x1b, 'm'}, {0x1b, '-'}, {0x1b, '='}}
	ways := stopsBy(t)
	for _, tc := range []struct {
		name string
		way  int // in stopsBy
		sink *audioSink
	}{
		{ways[0].name, 0, &audioSink{gain: 256, pct: 100}},
		{ways[1].name, 1, &audioSink{gain: 256, pct: 100}},
		{ways[1].name + ", with a sink that did not open", 1, &audioSink{gain: 256, pct: 100, err: errNoAudio()}},
	} {
		way := ways[tc.way]
		startNotices(t)
		a := statusApp(80, "Pixel 8")
		a.audio = tc.sink
		a.audio.setGain(70)
		a.sess = &session{deviceName: "Pixel 8", video: videoSocket(t), audio: way.audio()}
		a.stream = newStreamState(a.sess, a.cfg, nil)
		// Before the sound stops: a mute and an unmute, which a sink that did
		// not open leaves alone, and nothing said.
		a.handleInput(keys[0])
		a.handleInput(keys[0])
		if got := notices.current(timeNowUnixNano()); got != "" {
			t.Errorf("%s: before the sound stopped, the keys said %q", tc.name, got)
		}
		if got := a.audio.gainPercent(); got != 70 {
			t.Errorf("%s: before the sound stopped, a mute and an unmute left the volume at %d%%, want 70%%", tc.name, got)
		}
		lost := a.watchStreams()
		waitNotice(t, way.said)
		a.handleInput(keys[0])
		if got := notices.current(timeNowUnixNano()); got != why {
			t.Errorf("%s: after Alt+M the status row says %q, want %q", tc.name, got, why)
		}
		ageNotice(5 * time.Second)
		for range 2 {
			for _, key := range keys {
				a.handleInput(key)
			}
		}
		if got := notices.current(timeNowUnixNano()); got != "" {
			t.Errorf("%s: the keys said %q again", tc.name, got)
		}
		if got := a.audio.gainPercent(); got != 70 {
			t.Errorf("%s: the keys set the volume to %d%%, want it left at 70%%", tc.name, got)
		}
		if got, want := a.statusLine(), statusHintsText+" | audio off · Pixel 8 · 1080x2400 16.0ms 62.5fps"; got != want {
			t.Errorf("%s: the status row:\n got %q\nwant %q", tc.name, got, want)
		}
		select {
		case err := <-lost:
			t.Errorf("%s: the session ended with its sound: %v", tc.name, err)
		default:
		}
		var replay strings.Builder
		notices.stop(&replay)
		if got, want := replay.String(), "scterm: "+way.said+"\nscterm: "+why+"\n"; got != want {
			t.Errorf("%s: the replay at exit %q, want %q", tc.name, got, want)
		}
	}
}

// TestWebSoundThatStopsLeavesThisComputerAlone: with --audio-dup, once the
// device's sound stream ends while the picture goes on, this computer's
// stream is closed and the follower stops asking pactl, though the run goes
// on. The browser's status says audio off, with hostAudio false, and gain and
// muted 100 and false, as their comments say; the gain op leaves the volume as
// it was; and the run says once how the stream ended, and nothing more at the
// quit. The browser's goroutines and the follower read the sink while it
// closes (run with -race). Before, the stream stayed open and the follower
// went on with it, the status kept the volume and the gain op set it, and
// --web said nothing of a stream that simply ended.
func TestWebSoundThatStopsLeavesThisComputerAlone(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	fastAudioFollower(t)
	count := countingPactl(t, isolatePulse(t))
	cfg := config{web: true, audio: true, audioDup: true, webAddr: "127.0.0.1"}
	audio, device := net.Pipe()
	t.Cleanup(func() { device.Close() })
	sess := &session{deviceName: "Pixel 8", video: videoSocket(t), audio: audio}
	sink := &audioSink{gain: 256, pct: 100}
	a := &app{sess: sess, cfg: cfg, audio: sink, events: make(chan inputEvent, 64)}
	a.stream = &streamState{cfg: cfg, sess: sess, quitting: &a.quitting}
	a.web = newWebServer(cfg, sess, a.stream, sink, nil, a)
	t.Cleanup(a.web.stop)

	said := stderrOf(t, func() {
		ran := make(chan error, 1)
		go func() { ran <- a.runWeb() }()
		select {
		case <-a.web.ready:
		case <-time.After(5 * time.Second):
			t.Fatal("the web server never started listening")
		}
		cl := dialWS(t, serverHost(a.web))
		cl.send(webEvent{Op: "hello", W: 480, H: 1080, Caps: "audio"})
		// statusWhere reads the status messages until one is as ok wants, and
		// logs the last one it read if none comes.
		statusWhere := func(ok func(st map[string]any) bool) map[string]any {
			t.Helper()
			var last map[string]any
			found := false
			defer func() {
				if !found {
					t.Logf("the last status read: %v", last)
				}
			}()
			for deadline := time.Now().Add(5 * time.Second); ; {
				if last = cl.nextJSON("status", time.Until(deadline)); ok(last) {
					found = true
					return last
				}
			}
		}
		cl.send(map[string]any{"op": "gain", "gain": 40})
		// The volume was 100% before the op, so a status at 40% was worked
		// out after it.
		if st := statusWhere(func(st map[string]any) bool { return st["gain"] == 40.0 }); st["audio"] != "on" || st["hostAudio"] != true {
			t.Errorf("at 40%% while the sound plays: audio %v, hostAudio %v; want on, true", st["audio"], st["hostAudio"])
		}
		eventually(t, "the follower asks pactl", func() bool { return count() > 0 })

		if _, err := device.Write([]byte("opus")); err != nil { // returns once the demuxer has it
			t.Fatal(err)
		}
		device.Close()
		for deadline := time.Now().Add(5 * time.Second); !sink.closed.Load(); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Error("this computer's stream is still open after the sound stopped")
				break
			}
		}
		// A pactl call under way as the sink closed may end after it; the
		// follower then ends at its next tick.
		n := count()
		time.Sleep(20 * audioFollowEvery)
		if got := count(); got > n+1 {
			t.Errorf("pactl ran %d more times in 20 of the follower's ticks after the stream closed", got-n)
		}
		off := func(st map[string]any) bool { return st["audio"] == "off" }
		if st := statusWhere(off); st["hostAudio"] != false || st["gain"] != 100.0 || st["muted"] != false {
			t.Errorf("once the sound stopped: hostAudio %v, gain %v, muted %v; want false, 100, false",
				st["hostAudio"], st["gain"], st["muted"])
		}
		// One dispatcher applies a browser's ops in order, so once the quit
		// has ended the run, the gain ops before it have been handled.
		cl.send(map[string]any{"op": "gain", "gain": 70})
		cl.send(map[string]any{"op": "gain", "mute": true})
		cl.send(map[string]any{"op": "quit"})
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Fatal("the quit did not end the run")
		}
		if got := sink.gainPercent(); got != 40 {
			t.Errorf("the gain op set the volume of a sound that stopped to %d%%", got)
		}
	})
	var lines []string
	for _, line := range strings.Split(said, "\n") {
		if strings.HasPrefix(line, "scterm: audio") {
			lines = append(lines, line)
		}
	}
	if want := []string{"scterm: audio: duplicated (host sink and browser both play)", "scterm: audio: audio stream ended: EOF"}; !slices.Equal(lines, want) {
		t.Errorf("said %q, want %q", lines, want)
	}
}

// failingConn is the audio socket of a device that sends head and then fails
// with err, as a connection that is reset does.
type failingConn struct {
	net.Conn
	head []byte
	err  error
}

func (c *failingConn) Read(p []byte) (int, error) {
	if len(c.head) == 0 {
		return 0, c.err
	}
	n := copy(p, c.head)
	c.head = c.head[n:]
	return n, nil
}

// TestWebSaysWhenTheSoundStreamEnds: with --web or --window, as in the
// terminal and with --no-tui, a sound stream that ends while the picture goes
// on is said once, with how it ended: a stream that simply ends (EOF) and a
// connection that is reset, which --web left out as sockets closed on the way
// out, and an error that comes with the stream, without its control
// characters. Its end at the quit, as its socket closes on the way out, is not
// said.
func TestWebSaysWhenTheSoundStreamEnds(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	isolatePulse(t) // no adb or pactl to reach anything with
	cfg := config{web: true, audio: true, webAddr: "127.0.0.1"}
	once := func(what, out, line string) {
		t.Helper()
		if strings.Count(out, line+"\n") != 1 {
			t.Errorf("%s: stderr %q, want the line %q once", what, out, line)
		}
		if i := strings.IndexFunc(out, func(r rune) bool { return r != '\n' && unicode.IsControl(r) }); i >= 0 {
			t.Errorf("%s: a control character at %d: %q", what, i, out)
		}
	}

	sess := &session{deviceName: "Pixel 8", audio: deviceStream(t, []byte("opus"))}
	once("a stream that ends", webLinesUntil(t, cfg, sess, browserOnlySink(), "audio stream ended"),
		"scterm: audio: audio stream ended: EOF")

	// failing is the audio socket of a device that sends the codec id and then
	// fails with err.
	failing := func(err error) net.Conn {
		conn, other := net.Pipe()
		t.Cleanup(func() { conn.Close(); other.Close() })
		return &failingConn{Conn: conn, head: []byte("opus"), err: err}
	}
	sess = &session{deviceName: "Pixel 8", audio: failing(errors.New("reset by \x1b]0;owned\x07the \x1b[31mdevice\x1b[0m\r\n!"))}
	once("a stream that fails", webLinesUntil(t, cfg, sess, browserOnlySink(), "audio stream ended"),
		"scterm: audio: audio stream ended: reset by ]0;owned the [31mdevice [0m !")

	reset := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	if !isClosedConnErr(reset) {
		t.Fatalf("isClosedConnErr does not take %q for a socket that went away", reset)
	}
	sess = &session{deviceName: "Pixel 8", audio: failing(reset)}
	once("a connection that is reset", webLinesUntil(t, cfg, sess, browserOnlySink(), "audio stream ended"),
		"scterm: audio: audio stream ended: read tcp: read: connection reset by peer")

	playing, device := net.Pipe()
	t.Cleanup(func() { device.Close() })
	go device.Write([]byte("opus")) // returns once the demuxer has it, or once the socket closes
	stream := &closeSignal{Conn: playing, closed: make(chan struct{})}
	var during string
	after := stderrOf(t, func() {
		during = webLinesUntil(t, cfg, &session{deviceName: "Pixel 8", audio: stream}, browserOnlySink())
		playing.Close() // sess.stop
		demuxerEnded(t, stream)
	})
	if out := during + after; strings.Contains(out, "scterm: audio: audio") {
		t.Errorf("the stream's end at the quit was said: %q", out)
	}
}

// TestSinkClosesUnderItsReaders: the sink closes on the audio demuxer's
// goroutine, once the sound has stopped, while the run loop, the status row
// and the statistics line read its state from another. No read races the
// close (run with -race), and once it is closed, the sink plays nothing.
func TestSinkClosesUnderItsReaders(t *testing.T) {
	a := statusApp(80, "Pixel 8")
	a.sess = &session{deviceName: "Pixel 8", video: videoSocket(t), audio: deviceStream(t, []byte("opus"))}
	a.stream = newStreamState(a.sess, a.cfg, nil)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		a.playAudio()
	}()
	for {
		a.audio.plays()
		a.audio.failure()
		a.audio.gainPercent()
		audioState(a)
		a.statusLine()
		select {
		case <-ended:
			sinkClosed(t, "once the sound stopped", a.audio)
			return
		default:
		}
	}
}
