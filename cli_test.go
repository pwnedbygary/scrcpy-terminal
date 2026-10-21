package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"scterm/peer"
)

func bufioReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

// ---------------------------------------------------------------------------
// The command line fails fast and says why: device states adb reports, words
// that are not commands, flags that cannot work, forgetting a pairing, and a
// device-side server that never connects.
// ---------------------------------------------------------------------------

func TestPickDeviceExplainsEachState(t *testing.T) {
	const header = "List of devices attached\n"
	cases := []struct {
		name, list, serial, errHas string
	}{
		{"one ready", header + "R5CT\tdevice\n", "R5CT", ""},
		{"daemon start lines", "* daemon not running; starting now at tcp:5037\n* daemon started successfully\n" + header + "R5CT\tdevice\n", "R5CT", ""},
		{"ready beside unauthorized", header + "A\tunauthorized\nB\tdevice\n", "B", ""},
		{"two ready", header + "A\tdevice\nB\tdevice\n", "", "choose one with -s SERIAL"},
		{"nothing", header + "\n", "", "no device found"},
		{"unauthorized", header + "R5CT\tunauthorized\n", "", "Allow USB debugging?"},
		{"no permissions", header + "R5CT\tno permissions (missing udev rules? user is in the plugdev group); see [http://developer.android.com/tools/device.html]\n", "", "udev rule"},
		// adb leaves out the udev part when the host has no plugdev group.
		{"no permissions, bare", header + "R5CT\tno permissions; see [http://developer.android.com/tools/device.html]\n", "", "udev rule"},
		{"offline", header + "R5CT\toffline\n", "", "adb kill-server"},
		{"authorizing", header + "R5CT\tauthorizing\n", "", "try again in a moment"},
		{"recovery", header + "R5CT\trecovery\n", "", "recovery mode"},
		// `adb detach` lets go of a device until `adb attach`, which needs
		// the serial when more than one device is connected.
		{"detached", header + "R5CT\tdetached\n", "", "adb -s R5CT attach"},
	}
	for _, tc := range cases {
		serial, err := pickDevice(tc.list)
		if tc.errHas == "" {
			if err != nil || serial != tc.serial {
				t.Errorf("%s: got %q, %v; want %q", tc.name, serial, err, tc.serial)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.errHas) {
			t.Errorf("%s: error %v, want one mentioning %q", tc.name, err, tc.errHas)
		}
	}
}

func TestLeftoverWordsAreRefused(t *testing.T) {
	cases := map[string]string{
		"help":       `"help" is a command`,
		"peers":      "run it on its own, as scterm peers",
		"frobnicate": `unknown command "frobnicate"; run scterm -h`,
		"peer":       "did you mean peers, or --peer NAME?",
		"versoin":    "did you mean version?",
		"upd":        "did you mean update?",
		// A flag written without its dashes.
		"takeover":    "did you mean --takeover?",
		"no-audio":    "did you mean --no-audio?",
		"audio=false": "did you mean --audio=false?",
	}
	fs := newFlagSet(&config{}, new(float64))
	for word, want := range cases {
		if got := leftoverWordsError(fs, []string{word}, []string{word}); !strings.Contains(got, want) {
			t.Errorf("%q: %q, want it to contain %q", word, got, want)
		}
	}
	// A word after a flag is explained by that flag.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--audio", "false"}, "write --audio=false"},
		{[]string{"-video", "0"}, "write -video=0"},
		{[]string{"--peer", "Pixel", "8"}, `quote a value with spaces, as --peer "Pixel 8"`},
		{[]string{"--peer", "My", "Old", "Phone"}, `as --peer "My Old Phone"`},
		{[]string{"--web", "peers"}, "run it on its own, as scterm peers"},
		{[]string{"--web-port", "9000", "frobnicate"}, `unknown command "frobnicate"`},
		{[]string{"--audio", "maybe"}, `unknown command "maybe"`},
		// The scrcpy names are flags like any other.
		{[]string{"--no-audio", "false"}, "write --no-audio=false"},
		{[]string{"-n", "0"}, "write -n=0"},
		// A flag word after a value is that flag, not more of the value.
		{[]string{"--peer", "Pixel", "takeover"}, "did you mean --takeover?"},
		{[]string{"--peer", "Pixel", "8", "takeover"}, "did you mean --takeover?"},
	} {
		words := tc.args[len(tc.args)-1:]
		if tc.args[0] == "--peer" {
			words = tc.args[2:]
		}
		if got := leftoverWordsError(fs, tc.args, words); !strings.Contains(got, tc.want) {
			t.Errorf("%q: %q, want it to contain %q", tc.args, got, tc.want)
		}
	}
}

// TestSubcommandHelp: every command answers -h or --help with its usage on
// stdout instead of a usage error.
func TestSubcommandHelp(t *testing.T) {
	t.Setenv("SCTERM_HOME", t.TempDir()) // a command that ran anyway stays in here
	for _, args := range [][]string{{"pair", "-h"}, {"peers", "--help"}, {"forget", "-h"}, {"version", "--help"}, {"update", "-h"}} {
		var handled bool
		out := captureStdout(t, func() {
			handled = runPeerCommand(args) || runUpdateCommand(args)
		})
		if !handled || !strings.HasPrefix(out, "usage: scterm "+args[0]) {
			t.Errorf("%q: handled=%v, output %q", args, handled, out)
		}
	}
}

func TestValidateConfigRefusesWhatCannotWork(t *testing.T) {
	ok := config{video: true, audio: true, control: true}
	if err := validateConfig(ok); err != nil {
		t.Fatalf("defaults refused: %v", err)
	}
	for _, src := range []string{"output", "playback", "mic", "voice-performance"} {
		c := ok
		c.audioSource = src
		if err := validateConfig(c); err != nil {
			t.Errorf("--audio-source %s refused: %v", src, err)
		}
	}
	bad := map[string]config{
		"all streams off": {},
		"unknown codec":   {video: true, audio: true, control: true, audioCodec: "mp3"},
		"unknown source":  {video: true, audio: true, control: true, audioSource: "speaker"},
		// --no-tui shows nothing, and --web and --window are displays.
		"web without a display":    {video: true, audio: true, control: true, web: true, noTUI: true},
		"window without a display": {video: true, audio: true, control: true, window: true, noTUI: true},
	}
	for name, c := range bad {
		if err := validateConfig(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// parse runs scterm's flag parsing on args, from the configuration main
// starts with, without exiting.
func parse(t *testing.T, args ...string) (config, error) {
	t.Helper()
	cfg := defaultConfig()
	err := parseArgs(newFlagSet(&cfg, new(float64)), &cfg, args)
	return cfg, err
}

// helpEntry is one entry of scterm -h: the heading it is under, and its
// lines, the first starting with what to type.
type helpEntry struct {
	heading string
	lines   []string
}

// readHelp renders scterm -h as -h, --help and help print it, and splits it
// into entries.
func readHelp(t *testing.T) (*flag.FlagSet, string, []helpEntry) {
	t.Helper()
	cfg := defaultConfig()
	fs := newFlagSet(&cfg, new(float64))
	var b strings.Builder
	fs.SetOutput(&b)
	fs.Usage()
	var entries []helpEntry
	heading := ""
	for _, line := range strings.Split(b.String(), "\n") {
		switch {
		case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " "):
			heading = strings.TrimSuffix(line, ":")
		case heading != "" && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   "):
			entries = append(entries, helpEntry{heading, []string{line}})
		case len(entries) > 0 && strings.HasPrefix(line, "   "):
			e := &entries[len(entries)-1]
			e.lines = append(e.lines, line)
		}
	}
	return fs, b.String(), entries
}

// flagNames lists the flags a help entry names before its description, as
// s and serial for "-s, --serial SERIAL", and the word standing for the value.
func flagNames(e helpEntry) (names []string, value string) {
	col, _, _ := strings.Cut(strings.TrimSpace(e.lines[0]), "  ")
	for _, form := range strings.Split(col, ", ") {
		name, v, _ := strings.Cut(form, " ")
		names = append(names, strings.TrimLeft(name, "-"))
		value = v
	}
	return names, value
}

// TestHelpListsEveryFlagOnce: scterm -h is made from the flag definitions, so
// a flag cannot be left out of it. Each flag is on exactly one line, scrcpy's
// names on the line of the flag they stand for, and --mirror-fps on none.
func TestHelpListsEveryFlagOnce(t *testing.T) {
	fs, help, entries := readHelp(t)
	aliasOf := map[string]string{"serial": "s", "n": "no-control", "no-control": "control",
		"no-audio": "audio", "no-video": "video", "b": "video-bit-rate", "m": "max-size"}
	mainFlag := func(name string) string {
		for aliasOf[name] != "" {
			name = aliasOf[name]
		}
		return name
	}
	named := map[string]int{} // flag → lines naming it
	lines := map[string]int{} // flag → lines naming it or an alias of it
	for _, e := range entries {
		if !strings.HasPrefix(strings.TrimSpace(e.lines[0]), "-") {
			continue
		}
		names, _ := flagNames(e)
		onLine := map[string]bool{}
		for _, n := range names {
			if fs.Lookup(n) == nil {
				t.Errorf("the help lists -%s, which is not a flag", n)
			}
			named[n]++
			onLine[mainFlag(n)] = true
		}
		for n := range onLine {
			lines[n]++
		}
	}
	fs.VisitAll(func(f *flag.Flag) {
		switch {
		case f.Name == "mirror-fps":
		case lines[mainFlag(f.Name)] != 1:
			t.Errorf("--%s is on %d lines of the help, want 1", f.Name, lines[mainFlag(f.Name)])
		case aliasOf[f.Name] != "" && named[f.Name] != 1:
			t.Errorf("--%s is named on %d lines of the help, want 1", f.Name, named[f.Name])
		}
	})
	if strings.Contains(help, "mirror-fps") {
		t.Error("the help mentions --mirror-fps, which does nothing")
	}
}

// TestHelpIsGrouped: scterm -h puts each flag under a heading, with a word
// for its value and its default said once, then the commands (one line
// each), the environment, the exit statuses and a few examples, all within
// 80 columns.
func TestHelpIsGrouped(t *testing.T) {
	fs, help, entries := readHelp(t)
	for i, line := range strings.Split(help, "\n") {
		if n := utf8.RuneCountInString(line); n > 80 {
			t.Errorf("help line %d is %d columns: %q", i+1, n, line)
		}
	}
	var headings []string
	for _, e := range entries {
		if len(headings) == 0 || headings[len(headings)-1] != e.heading {
			headings = append(headings, e.heading)
		}
	}
	want := []string{"Device", "Video", "Audio", "Display", "Peer", "Debugging", "Commands", "Environment", "Exit status", "Examples"}
	if strings.Join(headings, ", ") != strings.Join(want, ", ") {
		t.Errorf("headings %q, want %q", headings, want)
	}

	under := map[string]string{} // flag, command or variable → its heading
	text := map[string]string{}  // flag, command or variable → its entry
	value := map[string]string{} // flag → the word for its value
	examples := 0
	for _, e := range entries {
		all := strings.Join(e.lines, "\n")
		if strings.Count(strings.ToLower(all), "default") > 1 {
			t.Errorf("%q gives a default more than once", all)
		}
		first := strings.TrimSpace(e.lines[0])
		if strings.HasPrefix(first, "-") {
			names, v := flagNames(e)
			if f := fs.Lookup(names[0]); f != nil && isBoolFlag(f) != (v == "") {
				t.Errorf("%q: want a word for the value exactly when the flag takes one", first)
			}
			for _, n := range names {
				under[n], text[n], value[n] = e.heading, all, v
			}
			continue
		}
		word, _, _ := strings.Cut(first, " ")
		word, _, _ = strings.Cut(word, "=")
		under[word], text[word] = e.heading, all
		if e.heading == "Commands" && len(e.lines) != 1 {
			t.Errorf("command %s takes %d lines, want 1", word, len(e.lines))
		}
		if e.heading == "Examples" {
			examples++
		}
	}
	for name, heading := range map[string]string{
		"s": "Device", "serial": "Device", "n": "Device", "no-control": "Device",
		"no-video": "Video", "m": "Video", "max-size": "Video", "max-fps": "Video", "b": "Video", "video-bit-rate": "Video",
		"no-audio": "Audio", "audio-bit-rate": "Audio", "audio-codec": "Audio", "audio-source": "Audio", "audio-dup": "Audio",
		"no-tui": "Display", "web": "Display", "window": "Display", "web-port": "Display", "web-addr": "Display",
		"web-quality": "Display", "window-size": "Display",
		"peer": "Peer", "takeover": "Peer",
		"keys": "Debugging", "dump-frames": "Debugging", "dump-audio": "Debugging", "repaint-interval": "Debugging",
		"SCT_DEBUG_WEB": "Debugging", "SCT_DEBUG_CONTROL": "Debugging",
		"pair": "Commands", "peers": "Commands", "forget": "Commands", "update": "Commands", "version": "Commands", "help": "Commands",
		"ANDROID_SERIAL": "Environment", "SCTERM_HOME": "Environment", "SCTERM_CHROME": "Environment",
		"SCTERM_FIREFOX_KIOSK": "Environment", "SCT_AUDIO_DEVICE": "Environment",
		"0": "Exit status", "1": "Exit status", "2": "Exit status",
	} {
		if under[name] != heading {
			t.Errorf("%s is under %q, want %q", name, under[name], heading)
		}
	}
	// scrcpy's exit statuses, a mistake in the command line being a failure
	// to start, and Go's status for a panic sharing 2.
	for status, says := range map[string][]string{
		"0": {"normal end"},
		"1": {"could not start", "command line", "session failed for another reason"},
		"2": {"device was lost during the session", "crash"},
	} {
		entry := strings.Join(strings.Fields(text[status]), " ")
		for _, s := range says {
			if !strings.Contains(entry, s) {
				t.Errorf("exit status %s does not say %q: %q", status, s, entry)
			}
		}
	}
	for _, v := range []string{"CHROME_BIN", "CHROMIUM_BIN"} {
		if !strings.Contains(text["SCTERM_CHROME"], v) {
			t.Errorf("the help does not mention %s beside SCTERM_CHROME", v)
		}
	}
	for name, v := range map[string]string{"s": "SERIAL", "max-size": "PIXELS", "video-bit-rate": "RATE", "web-port": "PORT", "peer": "NAME"} {
		if value[name] != v {
			t.Errorf("-%s shows its value as %q, want %q", name, value[name], v)
		}
	}
	if !strings.Contains(text["max-size"], "longer side") {
		t.Errorf("--max-size does not say it caps the longer side: %q", text["max-size"])
	}
	if !strings.Contains(text["audio-codec"], "PCM") || strings.Contains(help, "PCI") {
		t.Errorf("raw audio is PCM: %q", text["audio-codec"])
	}
	if examples < 3 || examples > 4 {
		t.Errorf("%d examples, want 3 or 4", examples)
	}
}

// TestHelpIsAskedForThreeWays: -h, --help and the help command ask for the
// help, which parseFlags prints on stdout before exiting 0; nothing is
// printed on the way.
func TestHelpIsAskedForThreeWays(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}, {"--web", "-h"}} {
		var err error
		var stderr string
		stdout := captureStdout(t, func() {
			stderr = captureStderr(t, func() { _, err = parse(t, args...) })
		})
		if !errors.Is(err, flag.ErrHelp) || stdout != "" || stderr != "" {
			t.Errorf("%q: %v, stdout %.60q, stderr %.60q; want flag.ErrHelp and nothing printed", args, err, stdout, stderr)
		}
	}
}

// TestFlagMistakeIsOneLine: an unknown or malformed flag gets the flag
// package's own message and a pointer to scterm -h, in one line, instead of
// the whole help after it.
func TestFlagMistakeIsOneLine(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"--web-port"}, "flag needs an argument: -web-port"},
		{[]string{"--web-port", "x"}, `invalid value "x" for flag -web-port`},
		{[]string{"--audio=maybe"}, `invalid boolean value "maybe" for -audio`},
	} {
		var err error
		stderr := captureStderr(t, func() { _, err = parse(t, tc.args...) })
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) ||
			!strings.Contains(err.Error(), "scterm -h") || strings.Contains(err.Error(), "\n") {
			t.Errorf("%q: %v; want one line starting %q and pointing to scterm -h", tc.args, err, tc.want)
		}
		if stderr != "" {
			t.Errorf("%q printed %d bytes besides: %.80q", tc.args, len(stderr), stderr)
		}
	}
}

// TestScrcpyFlagNamesWork: scrcpy's names for scterm's flags do the same.
func TestScrcpyFlagNamesWork(t *testing.T) {
	for _, tc := range []struct{ scrcpy, scterm []string }{
		{[]string{"--no-audio"}, []string{"--audio=false"}},
		{[]string{"--no-video"}, []string{"--video=false"}},
		{[]string{"--no-control"}, []string{"--control=false"}},
		{[]string{"-n"}, []string{"--control=false"}},
		{[]string{"--serial", "R5CT"}, []string{"-s", "R5CT"}},
		{[]string{"-b", "16000000"}, []string{"--video-bit-rate", "16000000"}},
		{[]string{"-m", "1920"}, []string{"--max-size", "1920"}},
	} {
		got, err1 := parse(t, tc.scrcpy...)
		want, err2 := parse(t, tc.scterm...)
		switch {
		case err1 != nil || err2 != nil:
			t.Errorf("%q, %q: %v, %v", tc.scrcpy, tc.scterm, err1, err2)
		case got != want:
			t.Errorf("%q gave %+v; %q gave %+v", tc.scrcpy, got, tc.scterm, want)
		case got == defaultConfig():
			t.Errorf("%q changed nothing", tc.scrcpy)
		}
	}
}

// TestBitRatesTakeKAndM: bit rates take scrcpy's K (x1000) and M (x1000000)
// suffixes, in either case, as well as plain numbers; anything else is a
// usage error naming the flag.
func TestBitRatesTakeKAndM(t *testing.T) {
	good := map[string]int{"8M": 8000000, "8m": 8000000, "128K": 128000, "128k": 128000,
		"1.5M": 1500000, "2.25K": 2250, "8000000": 8000000, "96000": 96000}
	// The server reads a bit rate into a Java int (Options.java), so 2148M
	// is too much.
	bad := []string{"12x", "8MB", "8 M", "M", "", "-1", "1e6", "0x10", "1.5", ".5M", "1.M", "1.2.3M", "2148M"}
	for _, name := range []string{"--video-bit-rate", "-b", "--audio-bit-rate"} {
		for in, want := range good {
			cfg, err := parse(t, name, in)
			got := cfg.videoBitRate
			if name == "--audio-bit-rate" {
				got = cfg.audioBitRate
			}
			if err != nil || got != want {
				t.Errorf("%s %s: %d, %v; want %d", name, in, got, err, want)
			}
		}
		for _, in := range bad {
			if _, err := parse(t, name, in); err == nil || !strings.Contains(err.Error(), "-"+strings.TrimLeft(name, "-")+":") {
				t.Errorf("%s %q: %v; want an error naming %s", name, in, err, name)
			}
		}
	}
}

// TestMirrorFPSOnlyWarns: --mirror-fps never did anything. Old command lines
// still run, with one line saying so.
func TestMirrorFPSOnlyWarns(t *testing.T) {
	var err error
	stderr := captureStderr(t, func() { _, err = parse(t, "--mirror-fps", "30") })
	if err != nil || stderr != "scterm: --mirror-fps does nothing and will be removed\n" {
		t.Errorf("--mirror-fps 30: %v, stderr %q", err, stderr)
	}
	stderr = captureStderr(t, func() { _, err = parse(t, "--max-fps", "30") })
	if err != nil || stderr != "" {
		t.Errorf("--max-fps 30: %v, stderr %q", err, stderr)
	}
}

// pairedStore makes a store under a fresh SCTERM_HOME holding these names.
func pairedStore(t *testing.T, names ...string) *peer.Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SCTERM_HOME", home)
	store := peer.OpenStore(home)
	for i, n := range names {
		rec := peer.Record{Fingerprint: fmt.Sprintf("%064x", i+1), Name: n, Host: "192.0.2.1", Port: 27300, Grants: []string{"view"}, PairedAtMs: 1}
		if err := store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func withInteractive(t *testing.T, on bool) {
	t.Helper()
	old := interactive
	t.Cleanup(func() { interactive = old })
	interactive = func() bool { return on }
}

func TestForgetNeedsTheWholeName(t *testing.T) {
	withInteractive(t, false)
	store := pairedStore(t, "Pixel 8")

	if err := forgetCommand(nil); err == nil {
		t.Fatal("forget with no name removed something")
	} else if _, ok := err.(usageErr); !ok {
		t.Errorf("forget with no name: %v is not a usage error", err)
	}
	if err := forgetCommand([]string{"pix"}); err == nil || !strings.Contains(err.Error(), "only part of") {
		t.Errorf("forget pix: %v, want a refusal", err)
	}
	if recs, _ := store.All(); len(recs) != 1 {
		t.Fatalf("a refused forget removed the pairing: %d left", len(recs))
	}
	captureStdout(t, func() {
		if err := forgetCommand([]string{"pixel", "8"}); err != nil {
			t.Errorf("forget by the whole name: %v", err)
		}
	})
	if recs, _ := store.All(); len(recs) != 0 {
		t.Errorf("forget by the whole name kept the pairing")
	}
}

func TestForgetConfirmsAPartialName(t *testing.T) {
	withInteractive(t, true)
	store := pairedStore(t, "Pixel 8")
	old := terminalInput
	t.Cleanup(func() { terminalInput = old })

	terminalInput = bufioReader("n\n")
	captureStdout(t, func() {
		if err := forgetCommand([]string{"pix"}); err != nil {
			t.Fatal(err)
		}
	})
	if recs, _ := store.All(); len(recs) != 1 {
		t.Fatal("answering no still forgot the device")
	}
	terminalInput = bufioReader("y\n")
	captureStdout(t, func() {
		if err := forgetCommand([]string{"pix"}); err != nil {
			t.Fatal(err)
		}
	})
	if recs, _ := store.All(); len(recs) != 0 {
		t.Error("answering yes kept the device")
	}
}

// TestServerThatStopsBeforeConnectingIsReported: a server that refuses an
// option exits without connecting; the wait ends at once and says what the
// server printed, instead of waiting for a socket that never comes.
func TestServerThatStopsBeforeConnectingIsReported(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	out := &tailBuffer{max: 4096}
	fmt.Fprintln(out, "[server] ERROR: Exception on thread main")
	fmt.Fprintln(out, "java.lang.IllegalArgumentException: Audio codec mp3 not supported")
	s := &session{listener: ln, serverOut: out, serverDone: make(chan struct{})}
	time.AfterFunc(50*time.Millisecond, func() { close(s.serverDone) })

	start := time.Now()
	err = s.connect(true, true, true)
	if err == nil || !strings.Contains(err.Error(), "stopped before connecting") ||
		!strings.Contains(err.Error(), "Audio codec mp3 not supported") {
		t.Fatalf("connect: %v", err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("connect waited %s after the server stopped", waited)
	}
}

func TestDeviceThatNeverConnectsTimesOut(t *testing.T) {
	old := connectTimeout
	t.Cleanup(func() { connectTimeout = old })
	connectTimeout = 100 * time.Millisecond

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	s := &session{listener: ln, serverOut: &tailBuffer{max: 4096}, serverDone: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- s.connect(true, false, false) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "did not connect its video socket") {
			t.Errorf("connect: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connect never gave up waiting for the device")
	}
}

// TestServerOutputDoesNotHoldUpWait: something adb leaves running can keep
// the server's output pipe open; once adb has exited, Wait must not wait for
// it (stop() would hang on the way out).
func TestServerOutputDoesNotHoldUpWait(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho '[server] ERROR: gone'\nsleep 5 &\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "adb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out := &tailBuffer{max: 4096}
	cmd, err := newADB("").startServer(nil, out)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = cmd.Wait()
	if waited := time.Since(start); waited > 3*time.Second {
		t.Errorf("Wait took %s: it waited for the child holding the pipe", waited)
	}
	if got := out.lastLines(1); len(got) != 1 || got[0] != "[server] ERROR: gone" {
		t.Errorf("server output = %q", got)
	}
}

// TestTailBufferLeavesOutStackFrames: the error line and the exception say why
// the server stopped; the frames under them would push both out.
func TestTailBufferLeavesOutStackFrames(t *testing.T) {
	b := &tailBuffer{max: 4096}
	fmt.Fprint(b, "[server] ERROR: Exception on thread main\n"+
		"java.lang.IllegalArgumentException: Audio codec mp3 not supported\n"+
		"\tat com.genymobile.scrcpy.Options.parse(Options.java:370)\n"+
		"\tat com.genymobile.scrcpy.Server.internalMain(Server.java:240)\n"+
		"\t... 2 more\n")
	want := []string{"[server] ERROR: Exception on thread main", "java.lang.IllegalArgumentException: Audio codec mp3 not supported"}
	if got := b.lastLines(6); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lastLines(6) = %q, want %q", got, want)
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 32}
	for i := 0; i < 20; i++ {
		fmt.Fprintf(b, "line %d\n", i)
	}
	lines := b.lastLines(2)
	if len(lines) != 2 || lines[1] != "line 19" || lines[0] != "line 18" {
		t.Errorf("lastLines(2) = %q", lines)
	}
}
