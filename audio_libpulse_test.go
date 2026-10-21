package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// isolatePulse gives libpulse and pactl a directory of their own for the
// test, so nothing reaches the session's sound server: PULSE_SERVER names a
// socket nobody listens on, XDG_RUNTIME_DIR is empty, autospawn is off, the
// configuration and cookie are not the user's, pactl is not on PATH and no
// sink is named. It returns the directory.
func isolatePulse(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"xdg", "pulse", "home", "bin"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	conf := filepath.Join(dir, "client.conf")
	if err := os.WriteFile(conf, []byte("autospawn = no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_CLIENTCONFIG", conf)
	t.Setenv("PULSE_SERVER", "unix:"+filepath.Join(dir, "none", "native"))
	t.Setenv("PULSE_RUNTIME_PATH", filepath.Join(dir, "pulse"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(dir, "xdg"))
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "home", ".config"))
	t.Setenv("PATH", filepath.Join(dir, "bin"))
	for _, key := range []string{"SCT_AUDIO_DEVICE", "PULSE_SINK", "PULSE_COOKIE", "DISPLAY"} {
		unsetenvForTest(t, key)
	}
	return dir
}

// unsetenvForTest unsets key until the test ends.
func unsetenvForTest(t *testing.T, key string) {
	t.Setenv(key, "") // puts the old value back when the test ends
	os.Unsetenv(key)
}

// TestAudioSinkConnectsToTheDefaultServer: libpulse gets NULL, not "", for its
// default server and sink, so with nothing listening there the open fails on
// the connection ("Connection refused") rather than on libpulse refusing an
// empty name ("Invalid argument"). The XDG_RUNTIME_DIR socket reaches libpulse
// by name: a listener there sees its tries (and hangs up, so they fail too).
// A sink named in SCT_AUDIO_DEVICE is tried by that name, as the error shows;
// whether libpulse receives it needs a real server, which only the builder's
// PipeWire probe checked.
func TestAudioSinkConnectsToTheDefaultServer(t *testing.T) {
	old := audioPause
	audioPause = func(time.Duration) {}
	t.Cleanup(func() { audioPause = old })

	dir := isolatePulse(t)
	path := filepath.Join(dir, "xdg", "pulse", "native")
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()

	for _, tc := range []struct{ env, shown string }{
		{"", "default"},
		{"scterm_test_sink", "scterm_test_sink"},
	} {
		t.Setenv("SCT_AUDIO_DEVICE", tc.env)
		before := accepted.Load()
		a := newAudioSink()
		if a.err == nil {
			a.close()
			t.Fatalf("SCT_AUDIO_DEVICE=%q: a stream opened on a listener that hangs up", tc.env)
		}
		want := `audio sink: server "default" device "` + tc.shown + `": Connection refused; ` +
			`server "unix:` + path + `" device "` + tc.shown + `": `
		if got := a.err.Error(); !strings.HasPrefix(got, want) {
			t.Errorf("SCT_AUDIO_DEVICE=%q:\n got %s\nwant %s…", tc.env, got, want)
		}
		if accepted.Load() == before {
			t.Errorf("SCT_AUDIO_DEVICE=%q: libpulse never connected to %s", tc.env, path)
		}
	}
}

// TestAudioSinkGivesUpAtOnceOnARefusedServer: an error no wait can change
// ends the open at once, without the pauses kept for a server that is still
// starting (450 ms in all). libpulse refuses a PULSE_SERVER that is set but
// empty as an invalid server.
func TestAudioSinkGivesUpAtOnceOnARefusedServer(t *testing.T) {
	isolatePulse(t)
	t.Setenv("PULSE_SERVER", "")
	unsetenvForTest(t, "XDG_RUNTIME_DIR")
	start := time.Now()
	a := newAudioSink()
	took := time.Since(start)
	if a.err == nil {
		a.close()
		t.Fatal("a stream opened on an empty PULSE_SERVER")
	}
	if got, want := a.err.Error(), `audio sink: server "default" device "default": Invalid server`; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	if took >= 300*time.Millisecond {
		t.Errorf("gave up after %v: it waited to try again", took)
	}
}
