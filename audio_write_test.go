package main

import (
	"encoding/binary"
	"net"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// writingSink is a sink whose writes reach the C code's write call and fail
// there: its C sink is a zero one, which no open made valid (an open one fails
// there in pa_simple_write, when the sound server goes away). That C sink is
// Go memory, so it must never be closed: the test takes it back at its end.
func writingSink(t *testing.T) *audioSink {
	a := &audioSink{gain: 256, pct: 100}
	zeroed(&a.sink)
	t.Cleanup(func() {
		a.mu.Lock()
		a.sink = nil
		a.mu.Unlock()
	})
	return a
}

// zeroed points *p at a new zero T: a C struct, say, which a test cannot name
// (cgo does not work in test files).
func zeroed[T any](p **T) { *p = new(T) }

// TestSinkThatFailsWhilePlaying: a write that fails stops this computer's
// sound, from the goroutine that plays it, while the run loop, the status row
// and the browser read the sink's state from others. No read races the write
// (run with -race), and once it has failed, each of them sees it: the sink no
// longer plays and says why, the statistics line and the status row say audio
// off, the volume keys leave it alone, no follower starts for it, and it takes
// no more sound.
func TestSinkThatFailsWhilePlaying(t *testing.T) {
	const stopped = "scterm: audio sink write failed; audio stopped. Check `pactl list sinks` and your audio server.\n"
	forgetLogOnce(t, stopped)
	bin := t.TempDir()
	fakeProgram(t, bin, "pactl", "exit 1\n") // found on PATH, never run
	t.Setenv("PATH", bin)
	t.Setenv("SCT_AUDIO_DEVICE", "")
	a := statusApp(80, "Pixel 8")
	a.audio = writingSink(t)

	stderr := captureStderr(t, func() {
		wrote := make(chan struct{})
		go func() {
			defer close(wrote)
			a.audio.writePCM16(make([]byte, 16))
		}()
		for {
			a.audio.plays()
			a.audio.errString()
			audioState(a)
			a.statusLine()
			select {
			case <-wrote:
				return
			default:
			}
		}
	})

	if stderr != stopped {
		t.Errorf("stderr %q, want %q", stderr, stopped)
	}
	if a.audio.plays() {
		t.Error("the sink still plays")
	}
	if got, want := a.audio.errString(), "pulseaudio write failed (check: pipewire or pulseaudio running? `pactl info`)"; got != want {
		t.Errorf("why: %q, want %q", got, want)
	}
	if got := audioState(a); got != "off" {
		t.Errorf("statistics line: audio=%s, want audio=off", got)
	}
	if got := a.statusLine(); !strings.Contains(got, "audio off") || strings.Contains(got, "vol ") {
		t.Errorf("status row %q, want it to say audio off", got)
	}
	a.toggleLocalMute()
	a.adjustLocalVolume(-10)
	if got := a.audio.gainPercent(); got != 100 {
		t.Errorf("the volume keys set the volume of a sink that stopped to %d%%", got)
	}
	if _, ok := a.audioFollower(); ok {
		t.Error("a follower would start for a sink that stopped")
	}
	a.audio.setGain(50)
	pcm := []byte{0x00, 0x10, 0x00, 0xf0, 0x00, 0x20, 0x00, 0xe0}
	sent := slices.Clone(pcm)
	a.audio.writePCM16(pcm)
	if !slices.Equal(pcm, sent) {
		t.Errorf("a sink that stopped took more sound: %x became %x", sent, pcm)
	}
}

// TestBrowserSeesTheSinkFailWhilePlaying: with --audio-dup, a sink here that
// fails while it plays (a write fails, on the audio demuxer's goroutine) shows
// in the browser's status as a sink that does not play, as one that did not
// open does: hostAudio false, the volume 100% and not muted, while the sound
// stays on for the browser. From then on the gain op leaves its volume alone,
// as the terminal's volume keys do. The browser's goroutines read the sink
// while the write fails (run with -race). Before, the status and the gain op
// saw only a sink that did not open: the status kept the volume set before
// the write failed, and the gain op went on setting it.
func TestBrowserSeesTheSinkFailWhilePlaying(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	isolatePulse(t) // no pactl for a follower to run
	const stopped = "scterm: audio sink write failed; audio stopped. Check `pactl list sinks` and your audio server.\n"
	forgetLogOnce(t, stopped)
	cfg := config{web: true, audio: true, audioDup: true, webAddr: "127.0.0.1"}
	audio, device := net.Pipe()
	t.Cleanup(func() { device.Close() })
	sess := &session{deviceName: "Pixel 8", video: videoSocket(t), audio: audio}
	sink := writingSink(t)
	a := &app{sess: sess, cfg: cfg, audio: sink, events: make(chan inputEvent, 64)}
	a.stream = &streamState{cfg: cfg, sess: sess}
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
		// logs the last one it read if none comes. Nothing orders the status
		// the 500 ms ticker works out and the one a gain op sends: a tick's,
		// worked out before the op, can arrive after the op's. So each wait
		// names what tells the status it wants from an older one.
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
		// out after the op.
		if st := statusWhere(func(st map[string]any) bool { return st["gain"] == 40.0 }); st["hostAudio"] != true || st["muted"] != false {
			t.Errorf("at 40%% while the sink plays: hostAudio %v, muted %v; want true, false", st["hostAudio"], st["muted"])
		}
		// The device's stream is raw PCM, and the sink's write of its first
		// packet fails. logOnce prints that under the lock it notes it with,
		// so once it is noted it has been said.
		header := binary.BigEndian.AppendUint64(nil, 20000) // its timestamp, in microseconds
		packet := append(binary.BigEndian.AppendUint32(header, 960), make([]byte, 960)...)
		for _, b := range [][]byte{{0, 'r', 'a', 'w'}, packet} {
			if _, err := device.Write(b); err != nil { // returns once the demuxer has it
				t.Fatal(err)
			}
		}
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			logOnceMu.Lock()
			failed := logOnceSeen[stopped]
			logOnceMu.Unlock()
			if failed {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the sink's write did not fail")
			}
		}
		// A tick worked out before the op said 100% too, with hostAudio true.
		failed := func(st map[string]any) bool { return st["gain"] == 100.0 && st["hostAudio"] == false }
		if st := statusWhere(failed); st["audio"] != "on" || st["muted"] != false {
			t.Errorf("once the sink failed: audio %v, muted %v; want on, false", st["audio"], st["muted"])
		}
		// One dispatcher applies a browser's ops in order, so once the quit
		// has ended the run, the gain op before it has been handled.
		cl.send(map[string]any{"op": "gain", "gain": 70})
		cl.send(map[string]any{"op": "quit"})
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Fatal("the quit did not end the run")
		}
		if got := sink.gainPercent(); got != 40 {
			t.Errorf("the gain op set the volume of a sink that failed to %d%%", got)
		}
	})
	if strings.Count(said, stopped) != 1 {
		t.Errorf("stderr %q, want the failure said once", said)
	}
}
