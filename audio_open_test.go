package main

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// libpulse error codes, from pulse/def.h.
const (
	paTestInvalid       = 3  // PA_ERR_INVALID
	paTestNoEntity      = 5  // PA_ERR_NOENTITY
	paTestRefused       = 6  // PA_ERR_CONNECTIONREFUSED
	paTestTimeout       = 8  // PA_ERR_TIMEOUT
	paTestInvalidServer = 13 // PA_ERR_INVALIDSERVER
)

// A runtime directory and socket that cannot exist, for the tests that only
// compare server names: a real user's socket here would be one mistake away
// from a test talking to the session's sound server.
const (
	testRuntimeDir = "/nonexistent/scterm-test/xdg"
	testSocket     = "unix:" + testRuntimeDir + "/pulse/native"
)

// pulseScript stands in for libpulse in openFirst: result says how a try ends
// (0 opens), given the server, the sink and how many times this pair has been
// tried, counting this try. Every try ("server|sink") and pause is recorded.
type pulseScript struct {
	result func(server, device string, n int) (int, string)
	tries  []string
	pauses []time.Duration
}

func (s *pulseScript) try(server, device string) (int, string) {
	pair := server + "|" + device
	s.tries = append(s.tries, pair)
	n := 0
	for _, p := range s.tries {
		if p == pair {
			n++
		}
	}
	return s.result(server, device, n)
}

func (s *pulseScript) sleep(d time.Duration) { s.pauses = append(s.pauses, d) }

func refusedConnection(string, string, int) (int, string) {
	return paTestRefused, "Connection refused"
}

// TestOpenFirstTakesTheDefaultServerAtOnce: when libpulse's default server and
// sink work, the stream opens at the first try, without a pause.
func TestOpenFirstTakesTheDefaultServerAtOnce(t *testing.T) {
	s := &pulseScript{result: func(string, string, int) (int, string) { return 0, "" }}
	server, device, err := openFirst([]string{"", testSocket}, []string{""}, s.try, s.sleep)
	if err != nil || server != "" || device != "" {
		t.Fatalf("openFirst = %q, %q, %v; want the default server and sink", server, device, err)
	}
	if want := []string{"|"}; !slices.Equal(s.tries, want) {
		t.Errorf("tries %q, want %q", s.tries, want)
	}
	if len(s.pauses) != 0 {
		t.Errorf("paused %v", s.pauses)
	}
}

// TestOpenFirstFallsBackWithoutWaiting: when the default server does not
// answer (a stale PULSE_SERVER), the XDG_RUNTIME_DIR socket is tried next,
// before any pause.
func TestOpenFirstFallsBackWithoutWaiting(t *testing.T) {
	s := &pulseScript{result: func(server, device string, n int) (int, string) {
		if server == "" {
			return refusedConnection(server, device, n)
		}
		return 0, ""
	}}
	server, _, err := openFirst([]string{"", testSocket}, []string{""}, s.try, s.sleep)
	if err != nil || server != testSocket {
		t.Fatalf("openFirst = %q, %v; want %q", server, err, testSocket)
	}
	if want := []string{"|", testSocket + "|"}; !slices.Equal(s.tries, want) {
		t.Errorf("tries %q, want %q", s.tries, want)
	}
	if len(s.pauses) != 0 {
		t.Errorf("paused %v before the socket", s.pauses)
	}
}

// TestOpenFirstWaitsForAServerThatIsStarting: a pair that fails to connect is
// tried again in two more rounds, 150 ms and then 300 ms later, so a server
// that comes up meanwhile still gets the stream; nothing waits after the last
// round.
func TestOpenFirstWaitsForAServerThatIsStarting(t *testing.T) {
	s := &pulseScript{result: func(server, device string, n int) (int, string) {
		if n < 3 {
			return refusedConnection(server, device, n)
		}
		return 0, ""
	}}
	if _, _, err := openFirst([]string{""}, []string{""}, s.try, s.sleep); err != nil {
		t.Fatalf("the server up at the third try: %v", err)
	}
	wantPauses := []time.Duration{150 * time.Millisecond, 300 * time.Millisecond}
	if len(s.tries) != 3 || !slices.Equal(s.pauses, wantPauses) {
		t.Errorf("up at the third try: tries %q, pauses %v; want 3 tries, pauses %v", s.tries, s.pauses, wantPauses)
	}

	s = &pulseScript{result: refusedConnection}
	if _, _, err := openFirst([]string{"", testSocket}, []string{""}, s.try, s.sleep); err == nil {
		t.Fatal("opened with no server up")
	}
	wantTries := []string{"|", testSocket + "|", "|", testSocket + "|", "|", testSocket + "|"}
	if !slices.Equal(s.tries, wantTries) || !slices.Equal(s.pauses, wantPauses) {
		t.Errorf("never up: tries %q, pauses %v; want %q, %v", s.tries, s.pauses, wantTries, wantPauses)
	}
}

// TestOpenFirstDoesNotRetryRefusedArguments: when libpulse or the server
// refuses the server or sink it was given, no wait changes that: the pair is
// not tried again and costs no pause, while the other pairs keep their tries.
func TestOpenFirstDoesNotRetryRefusedArguments(t *testing.T) {
	for _, refusal := range []struct {
		code int
		text string
	}{{paTestInvalid, "Invalid argument"}, {paTestInvalidServer, "Invalid server"}} {
		s := &pulseScript{result: func(string, string, int) (int, string) { return refusal.code, refusal.text }}
		_, _, err := openFirst([]string{"", testSocket}, []string{"", "alsa_output.x"}, s.try, s.sleep)
		if err == nil {
			t.Fatalf("%s: opened", refusal.text)
		}
		wantTries := []string{"|", "|alsa_output.x", testSocket + "|", testSocket + "|alsa_output.x"}
		if !slices.Equal(s.tries, wantTries) || len(s.pauses) != 0 {
			t.Errorf("%s: tries %q, pauses %v; want each pair once, no pause", refusal.text, s.tries, s.pauses)
		}
	}

	s := &pulseScript{result: func(server, device string, n int) (int, string) {
		if server == "" {
			return paTestInvalid, "Invalid argument"
		}
		return refusedConnection(server, device, n)
	}}
	if _, _, err := openFirst([]string{"", testSocket}, []string{""}, s.try, s.sleep); err == nil {
		t.Fatal("opened")
	}
	wantTries := []string{"|", testSocket + "|", testSocket + "|", testSocket + "|"}
	if !slices.Equal(s.tries, wantTries) || len(s.pauses) != 2 {
		t.Errorf("tries %q, pauses %v; want %q and two pauses", s.tries, s.pauses, wantTries)
	}
}

// TestOpenFirstRetriesAMissingSink: a server that does not have the sink yet
// ("No such entity") may still be adding it while it starts, so the pair is
// tried in every round, with both pauses.
func TestOpenFirstRetriesAMissingSink(t *testing.T) {
	s := &pulseScript{result: func(string, string, int) (int, string) { return paTestNoEntity, "No such entity" }}
	if _, _, err := openFirst([]string{""}, []string{"alsa_output.x"}, s.try, s.sleep); err == nil {
		t.Fatal("opened")
	}
	wantTries := []string{"|alsa_output.x", "|alsa_output.x", "|alsa_output.x"}
	wantPauses := []time.Duration{150 * time.Millisecond, 300 * time.Millisecond}
	if !slices.Equal(s.tries, wantTries) || !slices.Equal(s.pauses, wantPauses) {
		t.Errorf("tries %q, pauses %v; want %q, %v", s.tries, s.pauses, wantTries, wantPauses)
	}
}

// TestOpenFirstGoesOnWhileAnyPairMayWork: another round follows when any pair
// in this one may still work, even when the last pair tried was refused for
// good.
func TestOpenFirstGoesOnWhileAnyPairMayWork(t *testing.T) {
	s := &pulseScript{result: func(server, device string, n int) (int, string) {
		if device == "" {
			return paTestInvalid, "Invalid argument"
		}
		if n == 2 {
			return 0, ""
		}
		return refusedConnection(server, device, n)
	}}
	_, device, err := openFirst([]string{""}, []string{"alsa_output.x", ""}, s.try, s.sleep)
	if err != nil || device != "alsa_output.x" {
		t.Fatalf("openFirst = %q, %v; want alsa_output.x at its second try", device, err)
	}
	wantTries := []string{"|alsa_output.x", "|", "|alsa_output.x"}
	wantPauses := []time.Duration{150 * time.Millisecond}
	if !slices.Equal(s.tries, wantTries) || !slices.Equal(s.pauses, wantPauses) {
		t.Errorf("tries %q, pauses %v; want %q, %v", s.tries, s.pauses, wantTries, wantPauses)
	}
}

// TestOpenFirstErrorNamesEveryServerAndSink: when nothing opens, the error
// gives each server and sink tried, "default" for libpulse's own, with the
// last error it got.
func TestOpenFirstErrorNamesEveryServerAndSink(t *testing.T) {
	const card = "alsa_output.pci-0000_00_1f.3.analog-stereo"
	s := &pulseScript{result: func(server, device string, n int) (int, string) {
		switch {
		case device != "":
			return paTestNoEntity, "No such entity"
		case n == 1:
			return paTestTimeout, "Timeout"
		}
		return refusedConnection(server, device, n)
	}}
	_, _, err := openFirst([]string{"", testSocket}, []string{card, ""}, s.try, s.sleep)
	want := `audio sink: server "default" device "` + card + `": No such entity; ` +
		`server "default" device "default": Connection refused; ` +
		`server "` + testSocket + `" device "` + card + `": No such entity; ` +
		`server "` + testSocket + `" device "default": Connection refused`
	if err == nil || err.Error() != want {
		t.Errorf("\n got %v\nwant %s", err, want)
	}
}

// TestAudioServersTriesTheDefaultFirst: libpulse's default server comes first,
// then the XDG_RUNTIME_DIR socket by name; without XDG_RUNTIME_DIR there is
// only the default, once.
func TestAudioServersTriesTheDefaultFirst(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDir)
	if got, want := audioServers(), []string{"", testSocket}; !slices.Equal(got, want) {
		t.Errorf("with XDG_RUNTIME_DIR: %q, want %q", got, want)
	}
	unsetenvForTest(t, "XDG_RUNTIME_DIR")
	if got, want := audioServers(), []string{""}; !slices.Equal(got, want) {
		t.Errorf("without XDG_RUNTIME_DIR: %q, want %q", got, want)
	}
}

// TestAudioSinkPausesOnlyForAServerThatMayStart runs the real open (libpulse,
// isolated): with nothing listening it pauses between its rounds, 150 and
// 300 ms, and with a server libpulse refuses it gives up without one.
func TestAudioSinkPausesOnlyForAServerThatMayStart(t *testing.T) {
	var pauses []time.Duration
	old := audioPause
	audioPause = func(d time.Duration) { pauses = append(pauses, d) }
	t.Cleanup(func() { audioPause = old })

	isolatePulse(t)
	a := newAudioSink()
	want := []time.Duration{150 * time.Millisecond, 300 * time.Millisecond}
	if a.err == nil || !slices.Equal(pauses, want) {
		t.Errorf("nothing listening: err %v, pauses %v; want an error and pauses %v", a.err, pauses, want)
	}

	pauses = nil
	t.Setenv("PULSE_SERVER", "")
	unsetenvForTest(t, "XDG_RUNTIME_DIR")
	a = newAudioSink()
	if a.err == nil || len(pauses) != 0 {
		t.Errorf("empty PULSE_SERVER: err %v, pauses %v; want an error and no pause", a.err, pauses)
	}
}

// TestStaleStreamsCleanupTriesTheServersOpenTries: cleanupStaleStreams looks
// for leftover streams on the servers the stream may open on, in open's order.
// libpulse is isolated too, so a cleanup that went around killAppStreams
// would find no session server to kill streams on.
func TestStaleStreamsCleanupTriesTheServersOpenTries(t *testing.T) {
	dir := isolatePulse(t)
	socket := "unix:" + filepath.Join(dir, "xdg", "pulse", "native")
	var calls []string
	old := killAppStreams
	killAppStreams = func(server, app string) int {
		calls = append(calls, server+"|"+app)
		return -1
	}
	t.Cleanup(func() { killAppStreams = old })
	cleanupStaleStreams()
	if want := []string{"|scterm", socket + "|scterm"}; !slices.Equal(calls, want) {
		t.Errorf("calls %q, want %q", calls, want)
	}
}

// TestStaleStreamsAreSilencedOnTheFirstServerThatAnswers: the leftover streams
// of both application names are killed on the first server that answers, in
// the order open tries them, and on no other.
func TestStaleStreamsAreSilencedOnTheFirstServerThatAnswers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		killed    map[string]map[string]int // server, application: streams killed; missing: no answer
		want      int
		wantCalls []string
	}{
		{"the default answers", map[string]map[string]int{"": {"scterm": 2, "sct": 1}, testSocket: {"scterm": 5, "sct": 5}},
			3, []string{"|scterm", "|sct"}},
		{"only the socket answers", map[string]map[string]int{testSocket: {"scterm": 1, "sct": 0}},
			1, []string{"|scterm", testSocket + "|scterm", testSocket + "|sct"}},
		{"nothing answers", nil,
			0, []string{"|scterm", testSocket + "|scterm"}},
		{"the server goes away after the first kill", map[string]map[string]int{"": {"scterm": 0}},
			0, []string{"|scterm", "|sct"}},
	} {
		var calls []string
		n := killStaleStreams([]string{"", testSocket}, func(server, app string) int {
			calls = append(calls, server+"|"+app)
			if k, ok := tc.killed[server][app]; ok {
				return k
			}
			return -1
		})
		if n != tc.want || !slices.Equal(calls, tc.wantCalls) {
			t.Errorf("%s: %d killed, calls %q; want %d, %q", tc.name, n, calls, tc.want, tc.wantCalls)
		}
	}
}
