package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
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
// default sink, so the follower moves the stream there and says so.
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

// sinksPactl is a pactl for a server with three sinks, sink-a (index 1),
// sink-b (index 2) and sink-c (index 3, which takes no stream until a file
// sink-c-ready is in dir, as a Bluetooth sink that is not ready yet), and one
// scterm stream, which plays on the sink whose index is on, and which
// move-sink-input moves. The default sink is def, until setDefaultSink
// changes it. Unless tells, pactl's listing of the stream does not say which
// sink it plays on; unless lists, pactl lists no sinks. It returns pactl's
// calls so far, a line each.
func sinksPactl(t *testing.T, dir, def, on string, tells, lists bool) (calls func() string) {
	t.Helper()
	setDefaultSink(t, dir, def)
	onFile := filepath.Join(dir, "on")
	if err := os.WriteFile(onFile, []byte(on+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sinkLine := `printf '\tSink: %s\n' "$on"`
	if !tells {
		sinkLine = ":"
	}
	sinks := `printf '1\tsink-a\tmodule-alsa-card.c\ts16le 2ch 48000Hz\tRUNNING\n2\tsink-b\tmodule-alsa-card.c\ts16le 2ch 48000Hz\tIDLE\n3\tsink-c\tmodule-bluez5-device.c\ts16le 2ch 48000Hz\tIDLE\n'`
	if !lists {
		sinks = ":"
	}
	pactlScript(t, dir, fmt.Sprintf(`read -r on < '%[1]s'
case "$1" in
get-default-sink) read -r def < '%[2]s'; echo "$def" ;;
list)
	case "$2" in
	sinks) %[4]s ;;
	sink-inputs)
		printf 'Sink Input #7\n\tDriver: protocol-native.c\n\tClient: 12\n'
		%[3]s
		printf '\tSample Specification: s16le 2ch 48000Hz\n\tSink Latency: 20000 usec\n\tProperties:\n\t\tapplication.name = "scterm"\n' ;;
	esac ;;
move-sink-input)
	case "$3" in
	sink-a) echo 1 > '%[1]s' ;;
	sink-b) echo 2 > '%[1]s' ;;
	sink-c) [ -e '%[5]s' ] || exit 1; echo 3 > '%[1]s' ;;
	*) exit 1 ;;
	esac ;;
esac
`, onFile, filepath.Join(dir, "default"), sinkLine, sinks, filepath.Join(dir, "sink-c-ready")))
	return func() string {
		b, _ := os.ReadFile(filepath.Join(dir, "pactl-calls"))
		return string(b)
	}
}

// setDefaultSink makes name the default sink of sinksPactl's server in dir. A
// pactl that reads it meanwhile gets the old name or the new one, whole.
func setDefaultSink(t *testing.T, dir, name string) {
	t.Helper()
	next := filepath.Join(dir, "default.next")
	if err := os.WriteFile(next, []byte(name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, filepath.Join(dir, "default")); err != nil {
		t.Fatal(err)
	}
}

// followWhile starts the follower for a's sink as the runs do, and returns
// the func that stops it and waits for it to end, which the test's end calls
// too.
func followWhile(t *testing.T, a *app) (stop func()) {
	t.Helper()
	halt := make(chan struct{})
	ended := startFollower(t, a, halt, audioFollowEvery)
	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(halt)
			select {
			case <-ended:
			case <-time.After(5 * time.Second):
				t.Error("the follower went on after stop was closed")
			}
		})
	}
	t.Cleanup(stop)
	return stop
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
// before they start the follower, says to start it only when the sink plays
// on this computer (it opened, and the browser is not the only output),
// SCT_AUDIO_DEVICE does not pin it, and pactl is on PATH, and hands
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
		{"a browser-only session's sink", browserOnlySink(), "", true, false},
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
// only when audioFollower says to. With --audio-dup and no sink, a sink that
// did not open, or SCT_AUDIO_DEVICE set, pactl never runs while the loop runs
// for 20 of the follower's ticks, though it is on PATH; nor without
// --audio-dup, where the browser is the only output: newApp opens no sink
// then (browserOnlySink), and runWeb keeps any sink it is given silent. A run
// that looked pactl up itself would follow the desktop's sink though
// SCT_AUDIO_DEVICE names one, and ask pactl every second for a sink that
// never opened. With --audio-dup, a sink that opened and no SCT_AUDIO_DEVICE,
// pactl must run within those ticks: a runWeb that gave the follower a longer
// interval than audioFollowEvery, such as a second, would let the other cases
// pass whatever follower it started.
func TestWebRunStartsTheFollowerOnlyWhenItShould(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	fastAudioFollower(t)
	for _, tc := range []struct {
		name    string
		dup     bool
		sink    *audioSink
		pin     string
		follows bool
	}{
		{"no sink", true, nil, "", false},
		{"a sink that did not open", true, &audioSink{err: errPulseUnavailable{}}, "", false},
		{"SCT_AUDIO_DEVICE", true, &audioSink{gain: 256, pct: 100}, "speakers", false},
		{"a sink that opened", true, &audioSink{gain: 256, pct: 100}, "", true},
		{"the browser only", false, browserOnlySink(), "", false},
		{"the browser only, with a sink that opened", false, &audioSink{gain: 256, pct: 100}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := countingPactl(t, isolatePulse(t))
			t.Setenv("SCT_AUDIO_DEVICE", tc.pin)
			video, device := net.Pipe()
			defer device.Close()
			cfg := config{web: true, audio: true, audioDup: tc.dup, webAddr: "127.0.0.1"}
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
			switch n := count(); {
			case n != 0 && !tc.follows:
				t.Errorf("pactl ran %d times: runWeb started a follower that audioFollower refused", n)
			case n == 0 && tc.follows:
				t.Error("pactl did not run in 20 of the follower's ticks: runWeb started no follower, or one that asks less often")
			}
		})
	}
}

// TestWebRunStopsItsAudioFollower: the follower runWeb starts for a session
// that plays here too (--audio-dup) moves scterm's stream to the desktop's
// sink while the run lasts, and ends with the run. It used to run until the
// process exited, so a test that ran runWeb with a working sink left it
// asking pactl once a second, with the session's environment back once the
// test had ended, until the test binary exited.
func TestWebRunStopsItsAudioFollower(t *testing.T) {
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })
	fastAudioFollower(t)
	count, sink := movingPactl(t, isolatePulse(t))
	cfg := config{web: true, audio: true, audioDup: true, webAddr: "127.0.0.1"}
	webLinesHere(t, cfg, &session{deviceName: "Pixel 8"}, &audioSink{gain: 256, pct: 100}, "audio -> "+sink)
	time.Sleep(100 * time.Millisecond) // a pactl call under way when the run ended
	n := count()
	time.Sleep(100 * time.Millisecond)
	if got := count(); got != n {
		t.Errorf("pactl ran %d more times after the run ended: its follower outlived it", got-n)
	}
}

// TestAudioFollowerMovesOnlyAStreamThatIsElsewhere: the follower moves
// scterm's stream to the default sink, and says so, only when the stream
// plays on another sink, as when its sink opened elsewhere. At start the
// stream is usually on the default already, its sink having opened there: the
// follower then moves nothing and says nothing (on the second real-device run
// it moved the stream to the sink it played on, and said "audio -> SINK"),
// and it asks pactl for the sinks once, not at every tick. When pactl does not
// say where the stream plays, or lists no sinks either, the follower moves
// it, as before.
func TestAudioFollowerMovesOnlyAStreamThatIsElsewhere(t *testing.T) {
	fastAudioFollower(t)
	a := &app{audio: &audioSink{gain: 256, pct: 100}}
	for _, tc := range []struct {
		name         string
		def, on      string // the default sink, and the index of the sink the stream plays on
		tells, lists bool   // pactl's listing says which sink the stream plays on; pactl lists the sinks
		moves        int
		says         string
	}{
		{"on the default", "sink-a", "1", true, true, 0, ""},
		{"on another sink", "sink-b", "1", true, true, 1, "scterm: audio -> sink-b\n"},
		{"pactl does not say where", "sink-a", "1", false, true, 1, "scterm: audio -> sink-a\n"},
		{"pactl does not say where, nor list the sinks", "sink-a", "1", false, false, 1, "scterm: audio -> sink-a\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			startNotices(t)
			dir := isolatePulse(t)
			calls := sinksPactl(t, dir, tc.def, tc.on, tc.tells, tc.lists)
			stop := followWhile(t, a)
			eventually(t, "the follower looks for scterm's stream 10 times", func() bool {
				return strings.Count(calls(), "list sink-inputs\n") >= 10
			})
			stop()
			var said strings.Builder
			notices.stop(&said)
			got := calls()
			if n := strings.Count(got, "move-sink-input "); n != tc.moves || said.String() != tc.says {
				t.Errorf("moved the stream %d times and said %q; want %d and %q", n, said.String(), tc.moves, tc.says)
			}
			if n := strings.Count(got, "list sinks short\n"); n != 1 {
				t.Errorf("asked pactl for the sinks %d times in %d ticks, want once", n, strings.Count(got, "get-default-sink\n"))
			}
		})
	}
}

// TestAudioFollowerSaysEachSwitch: each time the default sink changes, the
// follower moves scterm's stream there and says so, a switch back included:
// from sink-a, where the stream starts, to sink-b, back to sink-a and to
// sink-b again, it moves the stream three times and says each move (the
// replay at exit counts the repeat). Said with logOnce, once per process, the
// second move to sink-b went unsaid, and a user switching outputs could not
// tell it from a follower that had stopped.
func TestAudioFollowerSaysEachSwitch(t *testing.T) {
	fastAudioFollower(t)
	startNotices(t)
	dir := isolatePulse(t)
	calls := sinksPactl(t, dir, "sink-a", "1", true, true)
	stop := followWhile(t, &app{audio: &audioSink{gain: 256, pct: 100}})
	eventually(t, "the follower looks for scterm's stream 3 times", func() bool {
		return strings.Count(calls(), "list sink-inputs\n") >= 3
	})
	for _, sw := range []struct{ to, index string }{{"sink-b", "2"}, {"sink-a", "1"}, {"sink-b", "2"}} {
		setDefaultSink(t, dir, sw.to)
		eventually(t, "the stream plays on "+sw.to, func() bool {
			b, _ := os.ReadFile(filepath.Join(dir, "on"))
			return strings.TrimSpace(string(b)) == sw.index
		})
	}
	stop()
	var said strings.Builder
	notices.stop(&said)
	if want := "scterm: audio -> sink-b (2 times)\nscterm: audio -> sink-a\n"; said.String() != want {
		t.Errorf("said %q, want %q", said.String(), want)
	}
	var moves []string
	for _, call := range strings.Split(calls(), "\n") {
		if strings.HasPrefix(call, "move-sink-input ") {
			moves = append(moves, call)
		}
	}
	if want := []string{"move-sink-input 7 sink-b", "move-sink-input 7 sink-a", "move-sink-input 7 sink-b"}; !slices.Equal(moves, want) {
		t.Errorf("moved the stream with %q, want %q", moves, want)
	}
}

// TestAudioFollowerRetriesAFailedMove: when the move to the default sink
// fails (a Bluetooth sink that is not ready yet, say), the follower says
// nothing and tries again at each tick, asking pactl for the sinks each time,
// until a move works. That move it says, once, and from then on it asks pactl
// only for the default sink and scterm's streams.
func TestAudioFollowerRetriesAFailedMove(t *testing.T) {
	fastAudioFollower(t)
	startNotices(t)
	dir := isolatePulse(t)
	calls := sinksPactl(t, dir, "sink-c", "1", true, true)
	stop := followWhile(t, &app{audio: &audioSink{gain: 256, pct: 100}})
	// The fake writes down each call before it looks for sink-c-ready, and
	// the follower waits for each pactl to end before it runs the next: once
	// 6 moves are written down, the first 5 have failed, and the 6th may yet
	// find the file.
	eventually(t, "the follower tries the move 6 times", func() bool {
		return strings.Count(calls(), "move-sink-input ") >= 6
	})
	if err := os.WriteFile(filepath.Join(dir, "sink-c-ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the stream plays on sink-c", func() bool {
		b, _ := os.ReadFile(filepath.Join(dir, "on"))
		return strings.TrimSpace(string(b)) == "3"
	})
	seen := strings.Count(calls(), "get-default-sink\n")
	eventually(t, "the follower asks for the default sink 3 times more", func() bool {
		return strings.Count(calls(), "get-default-sink\n") >= seen+3
	})
	stop()
	var said strings.Builder
	notices.stop(&said)
	if want := "scterm: audio -> sink-c\n"; said.String() != want {
		t.Errorf("said %q, want %q", said.String(), want)
	}
	lines := strings.Split(strings.TrimSuffix(calls(), "\n"), "\n")
	last := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "move-sink-input ") {
			last = i
		}
	}
	head, tail := lines[:last+1], lines[last+1:]
	// Up to the move that worked, each tick asks for the default sink,
	// scterm's streams and the sinks, then tries the move.
	tick := []string{"get-default-sink", "list sink-inputs", "list sinks short", "move-sink-input 7 sink-c"}
	if n := len(head) / len(tick); n < 6 || !slices.Equal(head, slices.Repeat(tick, n)) {
		t.Errorf("up to the move that worked, pactl was asked %q; want %q at each tick, 6 ticks or more", head, tick)
	}
	settled, others := 0, 0
	for _, line := range tail {
		switch line {
		case "get-default-sink":
			settled++
		case "list sink-inputs":
		default:
			others++
		}
	}
	if settled < 3 || others > 0 {
		t.Errorf("after the move that worked, pactl was asked %q; want only the default sink and scterm's streams, at 3 ticks or more", tail)
	}
}
