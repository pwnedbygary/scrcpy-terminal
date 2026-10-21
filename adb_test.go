package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMissingADBIsReportedFirst: -s and ANDROID_SERIAL name the device
// without asking adb, but every later step runs adb, so a missing adb is
// reported up front, with the way out, however the device was named.
func TestMissingADBIsReportedFirst(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct{ how, serial, env string }{
		{"-s", "R5CT", ""},
		{"ANDROID_SERIAL", "", "R5CT"},
		{"neither", "", ""},
	} {
		t.Setenv("ANDROID_SERIAL", tc.env)
		serial, err := findDevice(tc.serial)
		if err == nil || !strings.Contains(err.Error(), "install Android platform-tools") || !strings.Contains(err.Error(), "--peer") {
			t.Errorf("%s: %q, %v; want the missing-adb hint", tc.how, serial, err)
		}
	}
}

// TestADBRunCarriesTheMissingADBHint: an adb step that cannot find adb says
// the same, instead of the raw exec error.
func TestADBRunCarriesTheMissingADBHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := newADB("R5CT").run("push", "scrcpy-server", "/data/local/tmp/scrcpy-server.jar")
	if err == nil || !strings.Contains(err.Error(), "install Android platform-tools") || strings.Contains(err.Error(), "$PATH") {
		t.Errorf("run without adb: %v; want the missing-adb hint", err)
	}
}

// TestNamedDeviceSkipsTheLookup: with adb there, -s and ANDROID_SERIAL still
// name the device without running it.
func TestNamedDeviceSkipsTheLookup(t *testing.T) {
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	script := "#!/bin/sh\necho \"$*\" >> " + ran + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "adb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("ANDROID_SERIAL", "")
	if serial, err := findDevice("R5CT"); err != nil || serial != "R5CT" {
		t.Errorf("-s R5CT: %q, %v", serial, err)
	}
	t.Setenv("ANDROID_SERIAL", "emulator-5554")
	if serial, err := findDevice(""); err != nil || serial != "emulator-5554" {
		t.Errorf("ANDROID_SERIAL=emulator-5554: %q, %v", serial, err)
	}
	if calls, err := os.ReadFile(ran); err == nil {
		t.Errorf("adb ran: %s", calls)
	}
}

// serverTrace is what the server prints when its encoder fails: an error
// line, the exception, a trace more than twice tailBuffer's 4 KB, its cause
// with more frames, and a last line after it.
func serverTrace(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("[server] INFO: Device: [Google] Pixel 8 (Android 15)\n")
	b.WriteString("[server] ERROR: Exception on thread Thread[main,5,main]\n")
	b.WriteString("java.lang.IllegalStateException: Could not create the video encoder\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "\tat com.genymobile.scrcpy.video.SurfaceEncoder.streamCapture(SurfaceEncoder.java:%d)\n", 100+i)
	}
	b.WriteString("Caused by: android.media.MediaCodec$CodecException: Error 0xfffffff4\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "\tat android.media.MediaCodec.native_configure(MediaCodec.java:%d)\n", 2000+i)
	}
	b.WriteString("\t... 12 more\n")
	b.WriteString("[server] INFO: Cleaning up\n")
	if b.Len() < 2*4096 {
		t.Fatalf("the trace is only %d bytes", b.Len())
	}
	return b.String()
}

// TestTailBufferKeepsTheErrorPastALongTrace: the error line and the
// exception say why the server stopped; a trace longer than the buffer after
// them must not push them out, however the output arrives, and the lines
// keep their order.
func TestTailBufferKeepsTheErrorPastALongTrace(t *testing.T) {
	trace := serverTrace(t)
	want := []string{
		"[server] ERROR: Exception on thread Thread[main,5,main]",
		"java.lang.IllegalStateException: Could not create the video encoder",
		"Caused by: android.media.MediaCodec$CodecException: Error 0xfffffff4",
		"[server] INFO: Cleaning up",
	}
	for _, chunk := range []int{len(trace), 1, 7, 100} {
		b := &tailBuffer{max: 4096}
		for rest := trace; rest != ""; {
			n := min(chunk, len(rest))
			fmt.Fprint(b, rest[:n])
			rest = rest[n:]
		}
		if got := b.lastLines(6); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("written %d bytes at a time: lastLines(6) =\n  %s\nwant\n  %s", chunk, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
		}
	}
}

// TestTailBufferPrefersWhyItStopped: given more lines than asked for, the
// plain ones go first, oldest first; errors and warnings stay, in order.
func TestTailBufferPrefersWhyItStopped(t *testing.T) {
	b := &tailBuffer{max: 4096}
	fmt.Fprint(b, "[server] WARN: Audio capture is not supported before Android 11\n")
	fmt.Fprint(b, "[server] ERROR: Encoding error: java.lang.IllegalStateException\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(b, "[server] INFO: Retrying %d\n", i)
	}
	want := []string{
		"[server] WARN: Audio capture is not supported before Android 11",
		"[server] ERROR: Encoding error: java.lang.IllegalStateException",
		"[server] INFO: Retrying 4", "[server] INFO: Retrying 5", "[server] INFO: Retrying 6", "[server] INFO: Retrying 7",
	}
	if got := b.lastLines(6); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lastLines(6) = %q, want %q", got, want)
	}

	b = &tailBuffer{max: 4096}
	for i := 0; i < 10; i++ {
		fmt.Fprintf(b, "[server] ERROR: failure %d\n", i)
	}
	want = []string{"[server] ERROR: failure 7", "[server] ERROR: failure 8", "[server] ERROR: failure 9"}
	if got := b.lastLines(3); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("with only errors, lastLines(3) = %q, want the newest %q", got, want)
	}
}

// TestTailBufferStaysSmall: neither endless warnings nor a line with no end
// make the buffer grow, and a line with no end keeps only its start, so it
// does not push out the line before it.
func TestTailBufferStaysSmall(t *testing.T) {
	b := &tailBuffer{max: 4096}
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(b, "[server] WARN: frame %d dropped\n", i)
	}
	// Each warning is over 30 bytes, so 4 KB of them is fewer than 140.
	if got := b.lastLines(1000); len(got) > 200 {
		t.Errorf("after 100000 warnings, lastLines(1000) gave %d lines", len(got))
	}
	fmt.Fprint(b, strings.Repeat("x", 1<<20))
	got := b.lastLines(2)
	if len(got) != 2 || got[0] != "[server] WARN: frame 99999 dropped" || len(got[1]) > 4096 || strings.Trim(got[1], "x") != "" {
		t.Errorf("after a 1 MB line with no end, lastLines(2) = %.80q (%d lines)", got, len(got))
	}
}
