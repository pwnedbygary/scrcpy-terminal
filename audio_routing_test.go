package main

// Audio routing: which output plays the device sound.
//
// In web/window mode the browser plays the audio (fed by the PCM tap), so by
// default there is no host sink, and the stream is the sole source of sound.
// --audio-dup is what asks for the sound in both places. Before this, the host
// sink and the browser both played unconditionally, so every sound was doubled
// with a small offset between the two outputs.
//
// The decision is a pure function of the config so it can be pinned here: the
// sink itself needs a real PulseAudio device to observe.

import (
	"bufio"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHostAudioSilent(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config
		want bool
	}{
		{"web: stream is the sole source", config{web: true}, true},
		{"window (sets web): stream is the sole source", config{web: true, window: true}, true},
		{"web + audio-dup: both outputs play", config{web: true, audioDup: true}, false},
		{"tui: host sink is the only output", config{}, false},
		{"tui + audio-dup: host still plays", config{audioDup: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostAudioSilent(tc.cfg); got != tc.want {
				t.Errorf("hostAudioSilent(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

// TestOnlyASessionThatPlaysHereSilencesLeftovers: main silences the leftover
// streams of other scterms only for a session that plays its sound on this
// computer. Not with --no-audio; not with --web or --window without
// --audio-dup, where the browser is the only output; and not in peer mode
// when the target grants no sound, though the command line asked for it: the
// session then has no audio socket.
func TestOnlyASessionThatPlaysHereSilencesLeftovers(t *testing.T) {
	sound := &session{audio: videoSocket(t)}
	none := &session{}
	for _, tc := range []struct {
		name string
		cfg  config
		sess *session
		want bool
	}{
		{"terminal", config{audio: true}, sound, true},
		{"--no-tui", config{audio: true, noTUI: true}, sound, true},
		{"--web --audio-dup", config{audio: true, web: true, audioDup: true}, sound, true},
		{"--window --audio-dup", config{audio: true, web: true, window: true, audioDup: true}, sound, true},
		{"--no-audio", config{}, none, false},
		{"--no-audio --no-tui", config{noTUI: true}, none, false},
		{"--web", config{audio: true, web: true}, sound, false},
		{"--window", config{audio: true, web: true, window: true}, sound, false},
		{"peer mode, sound granted", config{audio: true, usePeer: true}, sound, true},
		{"peer mode, no sound granted", config{audio: true, usePeer: true}, none, false},
		{"peer mode, --web --audio-dup, no sound granted", config{audio: true, web: true, audioDup: true, usePeer: true}, none, false},
		{"peer mode, --no-audio --audio-dup", config{audioDup: true, usePeer: true}, none, false},
	} {
		if got := playsHere(tc.cfg, tc.sess); got != tc.want {
			t.Errorf("%s: playsHere = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestWebWithoutPulseAudioSaysTheBrowserPlays: with --audio-dup and without a
// PulseAudio sink the browser still plays the sound, which reaches it through
// the PCM tap and not the sink. So --web and --window say that only this
// computer is silent, and why: not "audio disabled", not "video only", and
// not that the host sink plays as well. Without --audio-dup there is no sink
// to fail (TestWebSaysWhereTheSoundPlays).
func TestWebWithoutPulseAudioSaysTheBrowserPlays(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	t.Setenv("PATH", t.TempDir()) // no adb or pactl to reach anything with
	cfg := config{web: true, audio: true, audioDup: true, webAddr: "127.0.0.1"}
	out := webStartLines(t, cfg, &audioSink{gain: 256, pct: 100, err: errNoAudio()})
	for _, wrong := range []string{"audio disabled", "video only", "both play", "pass --audio-dup"} {
		if strings.Contains(out, wrong) {
			t.Errorf("says %q:\n%s", wrong, out)
		}
	}
	said := false
	for _, line := range strings.Split(out, "\n") {
		said = said || strings.Contains(line, "browser") && strings.Contains(line, "pulseaudio not available")
	}
	if !said {
		t.Errorf("no line says the browser plays and why this computer does not:\n%s", out)
	}
}

// TestWebSaysWhereTheSoundPlays: --web and --window say once, as they start,
// where the session's sound plays. Without --audio-dup the browser is the only
// output: the line says so and points at --audio-dup, and says nothing of a
// host sink, as the session opens none. Before, it opened one and said that
// it stayed silent, or, with no sound server here, that this computer played
// nothing, and why. With --audio-dup, both play, or, when the sink did not
// open, the browser only, and why.
func TestWebSaysWhereTheSoundPlays(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	isolatePulse(t) // no sound server, and no pactl
	old := audioPause
	audioPause = func(time.Duration) {}
	t.Cleanup(func() { audioPause = old })
	sound := func() *session {
		return &session{deviceName: "Pixel 8", video: videoSocket(t), audio: videoSocket(t)}
	}
	// soundLines is what a run said of the sound.
	soundLines := func(out string) []string {
		var lines []string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "scterm: audio") {
				lines = append(lines, line)
			}
		}
		return lines
	}

	browser := newApp(sound(), config{web: true, audio: true, webAddr: "127.0.0.1"})
	got := soundLines(stderrUntil(t, browser, browser.runWeb, "web display on"))
	browser.shutdown()
	if want := []string{"scterm: audio: browser only (pass --audio-dup to play it on this computer too)"}; !slices.Equal(got, want) {
		t.Errorf("the browser only: said %q, want %q", got, want)
	}
	dup := config{web: true, audio: true, audioDup: true, webAddr: "127.0.0.1"}
	failed := newApp(sound(), dup) // with no sound server, its sink does not open
	got = soundLines(stderrUntil(t, failed, failed.runWeb, "web display on"))
	failed.shutdown()
	reason := failed.audio.errString()
	if !strings.HasPrefix(reason, "audio sink: ") {
		t.Errorf("--audio-dup: the sink's error is %q, not the open's", reason)
	}
	if want := []string{"scterm: audio: browser only; no sound on this computer: " + reason}; !slices.Equal(got, want) {
		t.Errorf("--audio-dup, the sink did not open: said %q, want %q", got, want)
	}
	got = soundLines(webLinesUntil(t, dup, sound(), &audioSink{gain: 256, pct: 100}))
	if want := []string{"scterm: audio: duplicated (host sink and browser both play)"}; !slices.Equal(got, want) {
		t.Errorf("--audio-dup: said %q, want %q", got, want)
	}
}

// TestWebWithoutSoundSaysNothingOfWhereItPlays: with --no-audio there is no
// sound, so --web and --window say nothing of where it plays: not that the
// browser plays it, with or without PulseAudio, nor to pass --audio-dup, nor,
// for --audio-dup (which peer mode accepts with --no-audio), that both play.
func TestWebWithoutSoundSaysNothingOfWhereItPlays(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	t.Setenv("PATH", t.TempDir()) // no adb or pactl to reach anything with
	for _, tc := range []struct {
		name string
		dup  bool
		sink error
	}{
		{"with PulseAudio", false, nil},
		{"without PulseAudio", false, errNoAudio()},
		{"--audio-dup", true, nil},
	} {
		cfg := config{web: true, audioDup: tc.dup, webAddr: "127.0.0.1"}
		out := webStartLines(t, cfg, &audioSink{gain: 256, pct: 100, err: tc.sink})
		for _, wrong := range []string{"audio", "browser"} {
			if strings.Contains(out, wrong) {
				t.Errorf("--no-audio, %s: says %q:\n%s", tc.name, wrong, out)
			}
		}
	}
}

// webStartLines runs the --web main loop on a device that sends nothing yet,
// until the web server prints the page's address, which it does once it
// listens; then it quits. It returns what the loop printed on stderr.
func webStartLines(t *testing.T, cfg config, audio *audioSink) string {
	t.Helper()
	return webLinesUntil(t, cfg, &session{deviceName: "Pixel 8"}, audio)
}

// webLinesUntil is webStartLines on sess, whose video sends nothing, and it
// also waits for a line holding each of wants. The run reaches no sound server
// and no pactl (isolatePulse).
func webLinesUntil(t *testing.T, cfg config, sess *session, audio *audioSink, wants ...string) string {
	t.Helper()
	isolatePulse(t)
	return webLinesHere(t, cfg, sess, audio, wants...)
}

// webLinesHere is webLinesUntil in the environment as the test left it, for a
// test that isolates the run itself and puts a pactl of its own on PATH.
func webLinesHere(t *testing.T, cfg config, sess *session, audio *audioSink, wants ...string) string {
	t.Helper()
	video, device := net.Pipe()
	defer device.Close()
	sess.video = video
	a := &app{sess: sess, cfg: cfg, audio: audio, events: make(chan inputEvent, 64)}
	a.stream = &streamState{cfg: cfg, sess: sess}
	a.web = newWebServer(cfg, sess, a.stream, audio, nil, a)
	return stderrUntil(t, a, a.runWeb, append([]string{"web display on"}, wants...)...)
}

// stderrUntil runs run, a main loop of a, and returns what it printed on
// stderr: once a line holding each of wants has come, it quits the loop, and
// reads the rest until the loop has returned.
func stderrUntil(t *testing.T, a *app, run func() error, wants ...string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := setStderr(w)
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	ran := make(chan error, 1)
	go func() { ran <- run() }()

	var out strings.Builder
	timeout := time.After(5 * time.Second)
	for missing := slices.Clone(wants); len(missing) > 0; {
		select {
		case line := <-lines:
			out.WriteString(line + "\n")
			missing = slices.DeleteFunc(missing, func(want string) bool { return strings.Contains(line, want) })
		case <-timeout:
			t.Errorf("no line said %q", missing)
			missing = nil
		}
	}
	a.events <- inputEvent{kind: evQuit}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Error("the main loop did not quit")
	}
	setStderr(old)
	w.Close()
	for line := range lines { // the rest, until the pipe is drained
		out.WriteString(line + "\n")
	}
	return out.String()
}
