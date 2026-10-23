package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestCLocaleEnvChangesOnlyTheLocale: the environment pactl runs in is the
// user's in the C locale: LC_ALL=C in place of the user's LC_ALL, no
// LANGUAGE, and every other variable as it was, in its order, those whose
// names only begin like these two included. The environment it is given
// stays as it was.
func TestCLocaleEnvChangesOnlyTheLocale(t *testing.T) {
	env := []string{"LANG=ja_JP.UTF-8", "LC_ALL=ja_JP.UTF-8", "LANGUAGE=ja:en", "LC_MESSAGES=ja_JP.UTF-8",
		"PULSE_SERVER=unix:/run/user/1000/pulse/native", "LANGUAGES=kept", "LC_ALL_X=kept", "PULSE_SINK=speakers", "LANGUAGE="}
	given := slices.Clone(env)
	want := []string{"LANG=ja_JP.UTF-8", "LC_MESSAGES=ja_JP.UTF-8", "PULSE_SERVER=unix:/run/user/1000/pulse/native",
		"LANGUAGES=kept", "LC_ALL_X=kept", "PULSE_SINK=speakers", "LC_ALL=C"}
	if got := cLocaleEnv(env); !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if !slices.Equal(env, given) {
		t.Errorf("changed the environment it was given to %q", env)
	}
}

// TestEveryPactlRunIsInTheCLocale: each way scterm runs pactl runs it in the
// C locale and without LANGUAGE, with the rest of the user's environment: the
// choice of the sink at start (audioDevices, which asks pulseDefaultSink and
// pulseCmd), the follower, with the pactl audioFollower hands it, and scterm
// doctor (doctorOutput), at the server PULSE_SERVER names and at the one it
// falls back to.
func TestEveryPactlRunIsInTheCLocale(t *testing.T) {
	fastAudioFollower(t)
	dir := isolatePulse(t)
	t.Setenv("LANG", "ja_JP.UTF-8")
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	t.Setenv("LANGUAGE", "ja")
	t.Setenv("PULSE_SINK", "speakers")
	runs := filepath.Join(dir, "pactl-env")
	pactlScript(t, dir, `printf '%s|LC_ALL=%s LANGUAGE=%s LANG=%s PULSE_SINK=%s PULSE_SERVER=%s\n' "$*" \
	"${LC_ALL-unset}" "${LANGUAGE-unset}" "${LANG-unset}" "${PULSE_SINK-unset}" "${PULSE_SERVER-unset}" >> '`+runs+`'
case "$1" in
get-default-sink) echo speakers ;;
list) printf 'Sink Input #7\n\tapplication.name = "scterm"\n' ;;
esac
`)
	ran := func() []string {
		b, _ := os.ReadFile(runs)
		return strings.Split(string(b), "\n")
	}
	socket := "unix:" + filepath.Join(dir, "xdg", "pulse", "native")
	for _, tc := range []struct {
		name   string
		run    func()
		server string // the PULSE_SERVER pactl runs with
	}{
		{"the sink at start", func() { audioDevices() }, socket},
		{"the follower", func() {
			stop := followWhile(t, &app{audio: &audioSink{gain: 256, pct: 100}})
			eventually(t, "the follower moves the stream", func() bool {
				return slices.ContainsFunc(ran(), func(l string) bool { return strings.HasPrefix(l, "move-sink-input ") })
			})
			stop()
		}, socket},
		{"scterm doctor", func() { doctorOutput(nil, "pactl", "info") }, os.Getenv("PULSE_SERVER")},
		{"scterm doctor, at the user's socket", func() { doctorOutput([]string{"PULSE_SERVER=" + socket}, "pactl", "info") }, socket},
	} {
		before := len(ran()) - 1 // the last line is empty
		tc.run()
		lines := ran()
		got := lines[before : len(lines)-1]
		if len(got) == 0 {
			t.Errorf("%s: ran no pactl", tc.name)
		}
		want := "LC_ALL=C LANGUAGE=unset LANG=ja_JP.UTF-8 PULSE_SINK=speakers PULSE_SERVER=" + tc.server
		for _, line := range got {
			if args, env, _ := strings.Cut(line, "|"); env != want {
				t.Errorf("%s: pactl %s ran with\n%s\nwant\n%s", tc.name, args, env, want)
			}
		}
	}
}

// frenchPactl is pactl for a server with two sinks, sink-a (index 1) and
// sink-b (index 2, the default), and one scterm stream, on sink-a, which
// move-sink-input moves to sink-b. As pactl with a French catalog does, it
// translates its listing of the stream unless it runs in the C locale without
// LANGUAGE (the French words are the test's own).
const frenchPactl = `case "$1" in
get-default-sink) echo sink-b ;;
list)
	case "$2" in
	sinks) printf '1\tsink-a\tmodule-alsa-card.c\ts16le 2ch 48000Hz\tIDLE\n2\tsink-b\tmodule-alsa-card.c\ts16le 2ch 48000Hz\tIDLE\n' ;;
	sink-inputs)
		if [ "$LC_ALL" = C ] && [ -z "${LANGUAGE+set}" ]; then
			printf 'Sink Input #7\n\tDriver: PipeWire\n\tSink: 1\n\tProperties:\n\t\tapplication.name = "scterm"\n'
		else
			printf 'Entrée de destination #7\n\tPilote : PipeWire\n\tDestination : 1\n\tPropriétés :\n\t\tapplication.name = "scterm"\n'
		fi ;;
	esac ;;
move-sink-input) [ "$3" = sink-b ] ;;
esac
`

// TestAudioFollowerFollowsInAnyLanguage: on a desktop in another language,
// the follower moves scterm's stream to the default sink, and says so, once.
// pactl translates the listing the follower reads ("Sink Input #7", "Sink:
// 1"). Run in the user's locale, it printed the listing in French, where the
// follower found no stream of scterm's, and the sound stayed on the old
// output.
func TestAudioFollowerFollowsInAnyLanguage(t *testing.T) {
	fastAudioFollower(t)
	startNotices(t)
	dir := isolatePulse(t)
	t.Setenv("LANG", "fr_FR.UTF-8")
	t.Setenv("LANGUAGE", "fr_FR:fr")
	unsetenvForTest(t, "LC_ALL")
	pactlScript(t, dir, frenchPactl)
	calls := func() string {
		b, _ := os.ReadFile(filepath.Join(dir, "pactl-calls"))
		return string(b)
	}
	stop := followWhile(t, &app{audio: &audioSink{gain: 256, pct: 100}})
	eventually(t, "the follower looks for scterm's stream 5 times", func() bool {
		return strings.Count(calls(), "list sink-inputs\n") >= 5
	})
	stop()
	var said strings.Builder
	notices.stop(&said)
	const says = "scterm: audio -> sink-b\n"
	if n := strings.Count(calls(), "move-sink-input 7 sink-b\n"); n != 1 || said.String() != says {
		t.Errorf("moved the stream %d times and said %q; want once, and %q", n, said.String(), says)
	}
}
