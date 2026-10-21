package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	}
	for name, c := range bad {
		if err := validateConfig(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
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
