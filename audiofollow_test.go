package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// pactlScript puts a pactl on isolatePulse's PATH (dir/bin) that records each
// call, then runs body. It returns how many calls there have been.
func pactlScript(t *testing.T, dir, body string) func() int {
	t.Helper()
	calls := filepath.Join(dir, "pactl-calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n" + body
	if err := os.WriteFile(filepath.Join(dir, "bin", "pactl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return func() int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), "\n")
	}
}

// countingPactl is a pactl that prints nothing, so there is no default sink
// to follow.
func countingPactl(t *testing.T, dir string) func() int {
	t.Helper()
	return pactlScript(t, dir, "")
}

var movingSinks atomic.Int32

// movingPactl is a pactl for a server with one scterm stream and another
// default sink, so the follower moves the stream there and says so. The sink
// has a new name each time: logOnce says a line once per process.
func movingPactl(t *testing.T, dir string) (count func() int, sink string) {
	t.Helper()
	sink = fmt.Sprintf("desk-%d", movingSinks.Add(1))
	count = pactlScript(t, dir, "case \"$1\" in\n"+
		"get-default-sink) echo "+sink+" ;;\n"+
		"list) printf 'Sink Input #7\\n\\tapplication.name = \"scterm\"\\n' ;;\n"+
		"esac\n")
	return count, sink
}

// fastAudioFollower makes followAudioSink ask every 5 ms until the test ends.
func fastAudioFollower(t *testing.T) {
	old := audioFollowEvery
	audioFollowEvery = 5 * time.Millisecond
	t.Cleanup(func() { audioFollowEvery = old })
}

// startFollower starts the follower as the runs do: pactl is looked up first,
// then handed to the follower's goroutine. The channel closes when it ends.
func startFollower(t *testing.T, a *app, stop <-chan struct{}, every time.Duration) <-chan struct{} {
	t.Helper()
	pactl, ok := a.audioFollower()
	if !ok {
		t.Fatal("no follower for this sink and PATH")
	}
	ended := make(chan struct{})
	go func() { followAudioSink(pactl, stop, every); close(ended) }()
	return ended
}

// TestAudioFollowerStopsWithItsRun: the follower that moves scterm's stream
// to the desktop's sink ends when stop is closed, and asks pactl nothing more.
func TestAudioFollowerStopsWithItsRun(t *testing.T) {
	fastAudioFollower(t)
	count := countingPactl(t, isolatePulse(t))
	a := &app{audio: &audioSink{gain: 256, pct: 100}}
	stop := make(chan struct{})
	ended := startFollower(t, a, stop, audioFollowEvery)
	for deadline := time.Now().Add(5 * time.Second); count() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the follower never asked for the default sink")
		}
	}
	close(stop)
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the follower went on after stop was closed")
	}
	n := count()
	time.Sleep(50 * time.Millisecond)
	if got := count(); got != n {
		t.Errorf("pactl ran %d more times after the follower ended", got-n)
	}
}

// TestAudioFollowerStopsBeforeAnotherTick: a stop that comes while pactl runs
// ends the follower when pactl returns, though a tick is due by then too.
// Each round's pactl waits until the test, having closed stop, lets it go;
// select picks at random between a closed stop and a due tick, so a follower
// that only checked stop there would ask again in about half the rounds.
func TestAudioFollowerStopsBeforeAnotherTick(t *testing.T) {
	a := &app{audio: &audioSink{gain: 256, pct: 100}}
	for round := 1; round <= 12; round++ {
		dir := isolatePulse(t)
		release := filepath.Join(dir, "release")
		// The wait also ends when the script itself is gone, or after about
		// 10 s: a failed round's cleanup may remove the release before the
		// script, and a follower that ignored its stop could start one then.
		count := pactlScript(t, dir, "n=0\n"+
			"while [ ! -e '"+release+"' ] && [ -e \"$0\" ] && [ $n -lt 2000 ]; do\n"+
			"\tPATH=/usr/bin:/bin sleep 0.005\n"+
			"\tn=$((n+1))\n"+
			"done\n")
		stop := make(chan struct{})
		var halt sync.Once
		t.Cleanup(func() { // a failed round still lets its follower end
			halt.Do(func() { close(stop) })
			_ = os.WriteFile(release, nil, 0o600)
		})
		ended := startFollower(t, a, stop, time.Millisecond)
		eventually(t, "the follower asks for the default sink", func() bool { return count() > 0 })
		halt.Do(func() { close(stop) })
		if err := os.WriteFile(release, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatal("the follower went on after stop was closed")
		}
		if n := count(); n != 1 {
			t.Fatalf("round %d: pactl ran %d times, though stop was closed during its first run", round, n)
		}
	}
}

// TestAudioFollowerKeepsThePactlItFound: the run looks pactl up, and takes
// the environment, before it starts the follower, which reads neither again:
// not when another pactl and runtime directory are put in place (each pactl
// runs with the XDG_RUNTIME_DIR and PULSE_SERVER it was found with), and not
// when the pactl it was given is gone. A follower that outlived its run would
// otherwise pick up the session's pactl and socket once its test had put them
// back.
func TestAudioFollowerKeepsThePactlItFound(t *testing.T) {
	fastAudioFollower(t)
	dir := isolatePulse(t)
	envs := filepath.Join(dir, "pactl-env")
	first := pactlScript(t, dir, "echo \"$XDG_RUNTIME_DIR $PULSE_SERVER\" >> '"+envs+"'\n")
	a := &app{audio: &audioSink{gain: 256, pct: 100}}
	stop := make(chan struct{})
	ended := startFollower(t, a, stop, audioFollowEvery)
	defer func() {
		close(stop)
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Error("the follower went on after stop was closed")
		}
	}()
	eventually(t, "the follower asks for the default sink", func() bool { return first() > 0 })
	later := countingPactl(t, isolatePulse(t)) // another PATH and runtime directory
	n := first()
	eventually(t, "the follower asks the pactl it started with 3 more times", func() bool { return first() >= n+3 })
	b, _ := os.ReadFile(envs)
	lines := strings.Split(string(b), "\n")
	xdg := filepath.Join(dir, "xdg")
	want := xdg + " unix:" + filepath.Join(xdg, "pulse", "native")
	for _, got := range lines[:len(lines)-1] { // the last is empty, or a line being written
		if got != want {
			t.Fatalf("pactl ran with XDG_RUNTIME_DIR and PULSE_SERVER %q, not the %q it was found with", got, want)
		}
	}
	if err := os.Remove(filepath.Join(dir, "bin", "pactl")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * audioFollowEvery) // ticks with the pactl it was given gone
	if got := later(); got != 0 {
		t.Errorf("the follower ran the pactl put on PATH after it started, %d times", got)
	}
}

// TestAudioFollowerNeedsASinkAndPactl: audioFollower, which the runs ask
// before they start the follower, says to start it only when the sink was
// opened, SCT_AUDIO_DEVICE does not pin it, and pactl is on PATH, and hands
// over that pactl. TestWebRunStartsTheFollowerOnlyWhenItShould checks that
// runWeb asks it.
func TestAudioFollowerNeedsASinkAndPactl(t *testing.T) {
	dir := isolatePulse(t)
	pactl := filepath.Join(dir, "bin", "pactl")
	working := &audioSink{gain: 256, pct: 100}
	for _, tc := range []struct {
		name     string
		sink     *audioSink
		pin      string
		hasPactl bool
		follows  bool
	}{
		{"no sink", nil, "", true, false},
		{"a sink that did not open", &audioSink{err: errPulseUnavailable{}}, "", true, false},
		{"SCT_AUDIO_DEVICE", working, "speakers", true, false},
		{"no pactl", working, "", false, false},
		{"a sink and pactl", working, "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(pactl)
			if tc.hasPactl {
				countingPactl(t, dir)
			}
			t.Setenv("SCT_AUDIO_DEVICE", tc.pin)
			got, ok := (&app{audio: tc.sink}).audioFollower()
			if ok != tc.follows {
				t.Fatalf("follows: %v, want %v", ok, tc.follows)
			}
			if ok && got.path != pactl {
				t.Errorf("handed pactl %q, want %q", got.path, pactl)
			}
		})
	}
}

// TestWebRunStartsTheFollowerOnlyWhenItShould: runWeb starts the follower
// only when audioFollower says to. With no sink, a sink that did not open, or
// SCT_AUDIO_DEVICE set, pactl never runs while the loop runs for 20 of the
// follower's ticks, though it is on PATH. A run that looked pactl up itself
// would follow the desktop's sink though SCT_AUDIO_DEVICE names one, and ask
// pactl every second for a sink that never opened.
func TestWebRunStartsTheFollowerOnlyWhenItShould(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	fastAudioFollower(t)
	for _, tc := range []struct {
		name string
		sink *audioSink
		pin  string
	}{
		{"no sink", nil, ""},
		{"a sink that did not open", &audioSink{err: errPulseUnavailable{}}, ""},
		{"SCT_AUDIO_DEVICE", &audioSink{gain: 256, pct: 100}, "speakers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := countingPactl(t, isolatePulse(t))
			t.Setenv("SCT_AUDIO_DEVICE", tc.pin)
			video, device := net.Pipe()
			defer device.Close()
			cfg := config{web: true, audio: true, webAddr: "127.0.0.1"}
			sess := &session{deviceName: "Pixel 8", video: video}
			a := &app{sess: sess, cfg: cfg, audio: tc.sink, events: make(chan inputEvent, 64)}
			a.stream = &streamState{cfg: cfg, sess: sess}
			a.web = newWebServer(cfg, sess, a.stream, tc.sink, nil, a)
			t.Cleanup(a.web.stop)
			ran := make(chan error, 1)
			go func() { ran <- a.runWeb() }()
			select {
			case <-a.web.ready:
			case <-time.After(3 * time.Second):
				t.Fatal("the web server never started listening")
			}
			time.Sleep(20 * audioFollowEvery)
			a.events <- inputEvent{kind: evQuit}
			select {
			case <-ran:
			case <-time.After(5 * time.Second):
				t.Fatal("the main loop did not quit")
			}
			if n := count(); n != 0 {
				t.Errorf("pactl ran %d times: runWeb started a follower that audioFollower refused", n)
			}
		})
	}
}

// TestWebRunStopsItsAudioFollower: the follower runWeb starts moves scterm's
// stream to the desktop's sink while the run lasts, and ends with the run. It
// used to run until the process exited, so a test that ran runWeb with a
// working sink left it asking pactl once a second, with the session's
// environment back once the test had ended, until the test binary exited.
func TestWebRunStopsItsAudioFollower(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	fastAudioFollower(t)
	count, sink := movingPactl(t, isolatePulse(t))
	cfg := config{web: true, audio: true, webAddr: "127.0.0.1"}
	webLinesUntil(t, cfg, &session{deviceName: "Pixel 8"}, &audioSink{gain: 256, pct: 100}, "audio -> "+sink)
	time.Sleep(100 * time.Millisecond) // a pactl call under way when the run ended
	n := count()
	time.Sleep(100 * time.Millisecond)
	if got := count(); got != n {
		t.Errorf("pactl ran %d more times after the run ended: its follower outlived it", got-n)
	}
}
