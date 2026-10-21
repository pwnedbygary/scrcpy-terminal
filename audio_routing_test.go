package main

// Audio routing: which output plays the device sound.
//
// In web/window mode the browser plays the audio (fed by the PCM tap), so by
// default the host sink is muted and the stream is the sole source of sound.
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

// TestWebWithoutPulseAudioSaysTheBrowserPlays: without a PulseAudio sink the
// browser still plays the sound, which reaches it through the PCM tap and not
// the sink. So --web and --window say that only this computer is silent, and
// why: not "audio disabled", not "video only", and not that the host sink
// plays as well.
func TestWebWithoutPulseAudioSaysTheBrowserPlays(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	t.Setenv("PATH", t.TempDir()) // no adb or pactl to reach anything with
	for _, dup := range []bool{false, true} {
		cfg := config{web: true, audio: true, audioDup: dup, webAddr: "127.0.0.1"}
		out := webStartLines(t, cfg, &audioSink{gain: 256, pct: 100, err: errNoAudio()})
		for _, wrong := range []string{"audio disabled", "video only", "both play", "pass --audio-dup"} {
			if strings.Contains(out, wrong) {
				t.Errorf("--audio-dup=%v: says %q:\n%s", dup, wrong, out)
			}
		}
		said := false
		for _, line := range strings.Split(out, "\n") {
			said = said || strings.Contains(line, "browser") && strings.Contains(line, "pulseaudio not available")
		}
		if !said {
			t.Errorf("--audio-dup=%v: no line says the browser plays and why this computer does not:\n%s", dup, out)
		}
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
// also waits for a line holding each of wants.
func webLinesUntil(t *testing.T, cfg config, sess *session, audio *audioSink, wants ...string) string {
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
	old := os.Stderr
	os.Stderr = w
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
	os.Stderr = old
	w.Close()
	for line := range lines { // the rest, until the pipe is drained
		out.WriteString(line + "\n")
	}
	return out.String()
}
