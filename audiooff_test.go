package main

import (
	"net"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// When the device turns the audio stream off (codec id 0, which the server
// sends when it cannot record the sound: playback capture before Android 13,
// any sound before Android 11), the session goes on without sound, as in
// scrcpy, and scterm says so once, with the server's reason when it gave one.
// ---------------------------------------------------------------------------

// Lines of the bundled server (AudioPlaybackCapture.java, AudioEncoder.java,
// ScreenCapture.java, Server.java), as they reach the session's server output.
const (
	playbackWarning  = "[server] WARN: Audio disabled: audio playback capture source not supported before Android 13"
	android10Warning = "[server] WARN: Audio disabled: it is not supported before Android 11"
	displayWarning   = "[server] WARN: Display doesn't have FLAG_SUPPORTS_PROTECTED_BUFFERS flag, mirroring can be restricted"
	deviceInfo       = "[server] INFO: Device: [Google] google Pixel 6 (Android 12)"
)

// serverSaid is a session's server output after lines.
func serverSaid(lines ...string) *tailBuffer {
	b := &tailBuffer{max: 4096}
	for _, l := range lines {
		b.Write([]byte(l + "\n"))
	}
	return b
}

// soundOff is the audio socket of a device that turns the audio stream off.
func soundOff(t *testing.T) net.Conn { return deviceStream(t, []byte{0, 0, 0, 0}) }

// shortAudioOffWait makes the wait for the server's reason d for one test.
func shortAudioOffWait(t *testing.T, d time.Duration) {
	old := audioOffWait
	t.Cleanup(func() { audioOffWait = old })
	audioOffWait = d
}

// waitNotice waits for the status row's notice to be want.
func waitNotice(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for notices.current(timeNowUnixNano()) != want {
		if time.Now().After(deadline) {
			t.Fatalf("the notice is %q, want %q", notices.current(timeNowUnixNano()), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNoSoundFromTheDeviceIsSaid: in the terminal UI, the device turning the
// audio stream off is a notice, in the status row and once in the replay at
// exit, with the server's warning about audio rather than the later one about
// the display. The session goes on, and the status row then shows no volume.
func TestNoSoundFromTheDeviceIsSaid(t *testing.T) {
	const want = "no sound from Pixel 8: Audio disabled: audio playback capture source not supported before Android 13"
	startNotices(t)
	a := statusApp(80, "Pixel 8")
	a.sess = &session{deviceName: "Pixel 8", video: videoSocket(t), audio: soundOff(t),
		serverOut: serverSaid(deviceInfo, playbackWarning, displayWarning)}
	a.stream = newStreamState(a.sess, a.cfg, nil)
	if got := a.statusLine(); !strings.Contains(got, "vol 100%") {
		t.Fatalf("before the stream starts: %q", got)
	}
	lost := a.watchStreams()
	waitNotice(t, want)
	select {
	case err := <-lost:
		t.Errorf("the session ended without sound: %v", err)
	default:
	}
	ageNotice(5 * time.Second)
	if got, want := a.statusLine(), statusHintsText+" | audio off · Pixel 8 · 1080x2400 16.0ms 62.5fps"; got != want {
		t.Errorf("status row once the notice is gone:\n got %q\nwant %q", got, want)
	}
	var replay strings.Builder
	notices.stop(&replay)
	if got := replay.String(); got != "scterm: "+want+"\n" {
		t.Errorf("replay %q, want the line once", got)
	}
}

// TestSoundThatStartedIsNotSaidToBeOff: in the terminal UI, an audio stream
// that started and then ended is reported as it was before, and is not said to
// be off; the status row keeps its volume.
func TestSoundThatStartedIsNotSaidToBeOff(t *testing.T) {
	startNotices(t)
	a := statusApp(80, "Pixel 8")
	a.sess = &session{deviceName: "Pixel 8", video: videoSocket(t), audio: deviceStream(t, []byte("opus")),
		serverOut: serverSaid(deviceInfo, displayWarning)}
	a.stream = newStreamState(a.sess, a.cfg, nil)
	a.watchStreams()
	waitNotice(t, "audio: audio stream ended: EOF")
	ageNotice(5 * time.Second)
	if got := a.statusLine(); !strings.Contains(got, "vol 100%") {
		t.Errorf("status row: %q", got)
	}
	var replay strings.Builder
	notices.stop(&replay)
	if got := replay.String(); got != "scterm: audio: audio stream ended: EOF\n" {
		t.Errorf("replay %q", got)
	}
}

// TestNoSoundFromTheDeviceSaysWhy: the line gives the server's last warning or
// error about audio, or no reason when there is none, as from a peer target.
// When --audio-dup chose the playback source, the line says that it needs
// Android 13; not when the source was named, nor in peer mode, where no source
// is sent.
func TestNoSoundFromTheDeviceSaysWhy(t *testing.T) {
	shortAudioOffWait(t, 50*time.Millisecond)
	const dupNote = " (--audio-dup implies --audio-source playback, which needs Android 13 or later)"
	for _, tc := range []struct {
		name string
		cfg  config
		out  *tailBuffer // nil: no server, as in peer mode
		want string
	}{
		{"--audio-dup on Android 12", config{audio: true, audioDup: true}, serverSaid(deviceInfo, playbackWarning, displayWarning),
			"no sound from Pixel 8: Audio disabled: audio playback capture source not supported before Android 13" + dupNote},
		{"--audio-dup --audio-source playback", config{audio: true, audioDup: true, audioSource: "playback"}, serverSaid(playbackWarning),
			"no sound from Pixel 8: Audio disabled: audio playback capture source not supported before Android 13"},
		{"--audio-source playback", config{audio: true, audioSource: "playback"}, serverSaid(playbackWarning),
			"no sound from Pixel 8: Audio disabled: audio playback capture source not supported before Android 13"},
		{"--audio-dup, nothing said about audio", config{audio: true, audioDup: true}, serverSaid(deviceInfo, displayWarning),
			"no sound from Pixel 8" + dupNote},
		{"Android 10", config{audio: true}, serverSaid(deviceInfo, android10Warning),
			"no sound from Pixel 8: Audio disabled: it is not supported before Android 11"},
		{"Android 11, locked", config{audio: true}, serverSaid("[server] ERROR: Failed to start audio capture",
			"[server] ERROR: On Android 11, audio capture must be started in the foreground, make sure that the device is unlocked when starting scrcpy."),
			"no sound from Pixel 8: On Android 11, audio capture must be started in the foreground, make sure that the device is unlocked when starting scrcpy."},
		{"peer mode, --audio-dup", config{audio: true, audioDup: true, usePeer: true}, nil, "no sound from Pixel 8"},
	} {
		startNotices(t)
		a := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: soundOff(t), serverOut: tc.out})
		a.cfg = tc.cfg
		a.watchStreams()
		waitNotice(t, tc.want)
		var replay strings.Builder
		notices.stop(&replay)
		if got := replay.String(); got != "scterm: "+tc.want+"\n" {
			t.Errorf("%s: replay %q, want %q", tc.name, got, "scterm: "+tc.want+"\n")
		}
	}
}

// TestNoSoundWaitsForTheServersReason: the server prints its reason before it
// turns the audio stream off, but its output comes on another adb stream, so
// the reason can come after the codec id; the line waits for it.
func TestNoSoundWaitsForTheServersReason(t *testing.T) {
	shortAudioOffWait(t, 5*time.Second)
	startNotices(t)
	out := serverSaid(deviceInfo)
	audio, device := net.Pipe()
	t.Cleanup(func() { device.Close() })
	a := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: audio, serverOut: out})
	a.cfg = config{audio: true}
	a.watchStreams()
	if _, err := device.Write([]byte{0, 0, 0, 0}); err != nil { // returns once the demuxer has it
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the demuxer has looked for the reason by now
	out.Write([]byte(android10Warning + "\n"))
	waitNotice(t, "no sound from Pixel 8: Audio disabled: it is not supported before Android 11")
}

// TestNoSoundWaitsAboutASecondForTheReason: left as it is, the wait for the
// server's reason is about a second (part 26's judgement): a reason that comes
// half a second after the codec id is given, and when none comes, the line
// comes about a second after the codec id, neither at once nor much later.
func TestNoSoundWaitsAboutASecondForTheReason(t *testing.T) {
	soundTurnedOff := func(out *tailBuffer) time.Time {
		t.Helper()
		audio, device := net.Pipe()
		t.Cleanup(func() { device.Close() })
		a := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: audio, serverOut: out})
		a.cfg = config{audio: true}
		a.watchStreams()
		// Read before the write: the demuxer starts its wait once it has the
		// codec id, which can be before Write returns.
		start := time.Now()
		if _, err := device.Write([]byte{0, 0, 0, 0}); err != nil { // returns once the demuxer has it
			t.Fatal(err)
		}
		return start
	}
	startNotices(t)
	late := serverSaid(deviceInfo)
	soundTurnedOff(late)
	time.Sleep(500 * time.Millisecond)
	late.Write([]byte(android10Warning + "\n"))
	waitNotice(t, "no sound from Pixel 8: Audio disabled: it is not supported before Android 11")

	off := soundTurnedOff(serverSaid(deviceInfo))
	waitNotice(t, "no sound from Pixel 8")
	if took := time.Since(off); took < 900*time.Millisecond || took > 2*time.Second {
		t.Errorf("with no reason, the line came %v after the codec id, want about a second", took)
	}
}

// TestNoSoundFromTheDeviceIsPrinted: with --web or --window, and with
// --no-tui, the line goes to stderr, once; a stream that started is not said
// to be off.
func TestNoSoundFromTheDeviceIsPrinted(t *testing.T) {
	// runWeb and runHeadless take these signals for the app and never give them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	t.Setenv("PATH", t.TempDir()) // no adb or pactl to reach anything with
	const want = "scterm: no sound from Pixel 8: Audio disabled: audio playback capture source not supported before Android 13\n"
	// An Opus stream cut inside its first packet, an error both modes print
	// (--web leaves out a stream that simply ends).
	started := [][]byte{[]byte("opus"), {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 100}, make([]byte, 10)}
	const cut = "scterm: audio: audio packet: unexpected EOF\n"
	cfg := config{web: true, audio: true, webAddr: "127.0.0.1"}
	audio := &audioSink{gain: 256, pct: 100}

	sess := &session{deviceName: "Pixel 8", audio: soundOff(t), serverOut: serverSaid(deviceInfo, playbackWarning)}
	if out := webLinesUntil(t, cfg, sess, audio, "no sound"); strings.Count(out, want) != 1 {
		t.Errorf("--web: stderr %q, want %q once", out, want)
	}
	sess = &session{deviceName: "Pixel 8", audio: deviceStream(t, started...), serverOut: serverSaid(deviceInfo)}
	if out := webLinesUntil(t, cfg, sess, audio, "audio packet"); strings.Contains(out, "no sound") || !strings.Contains(out, cut) {
		t.Errorf("--web, a stream that started: stderr %q", out)
	}

	headless := func(conn net.Conn, out *tailBuffer, wants ...string) (stderr string) {
		a := streamApp(&session{deviceName: "Pixel 8", video: videoSocket(t), audio: conn, serverOut: out})
		a.cfg = config{noTUI: true, audio: true}
		// Its statistics line goes to stdout, and must not run into the test's output.
		captureStdout(t, func() { stderr = stderrUntil(t, a, a.runHeadless, wants...) })
		return stderr
	}
	if out := headless(soundOff(t), serverSaid(deviceInfo, playbackWarning), "no sound"); strings.Count(out, want) != 1 {
		t.Errorf("--no-tui: stderr %q, want %q once", out, want)
	}
	if out := headless(deviceStream(t, started...), serverSaid(deviceInfo), "audio packet"); strings.Contains(out, "no sound") || !strings.Contains(out, cut) {
		t.Errorf("--no-tui, a stream that started: stderr %q", out)
	}
}
