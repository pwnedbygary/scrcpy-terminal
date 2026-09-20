package main

// Regression tests for the --window launch path.
//
// The bug these exist for: scterm run from a Zellij pane inherits no DISPLAY
// and no WAYLAND_DISPLAY, and leaves XDG_SESSION_TYPE=tty behind. Chromium's
// ozone platform picker then has nothing to go on, chooses X11, finds no X
// server, and dies in about a second with
//
//	ERROR:ui/ozone/platform/x11/ozone_platform_x11.cc:257] Missing X server or $DISPLAY
//	ERROR:ui/aura/env.cc:246] The platform failed to initialize.  Exiting.
//
// before a window can exist. Brave's launcher script masks the exit status
// (`"$HERE/brave" "$@" || true`) and scterm sent the browser's stdio to
// /dev/null, so the whole failure was invisible: --window printed a cheerful
// "window: /usr/bin/brave-origin-nightly (1280x720)", opened nothing, and said
// nothing about why.

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// makeSocket creates a listening unix socket: all browserEnv inspects is that
// the socket exists and is a socket.
func makeSocket(t *testing.T, path string) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	t.Cleanup(func() { l.Close() })
}

// envValue returns the value of key as a C library would read the array: the
// FIRST match wins, so a duplicate entry is a real bug, not a curiosity.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

func countEnv(env []string, key string) int {
	prefix := key + "="
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}

// noDisplayEnv removes every trace of a display from the test environment, the
// way a Zellij pane has none, and points the X socket probe at an empty dir.
func noDisplayEnv(t *testing.T) string {
	t.Helper()
	rt := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "tty")
	empty := filepath.Join(t.TempDir(), "no-x-here")
	old := x11SocketDir
	x11SocketDir = empty
	t.Cleanup(func() { x11SocketDir = old })
	return rt
}

// TestBrowserEnvRecoversWayland is the fix itself: with no display variables in
// the environment but a compositor socket on the machine, the browser must be
// told where the compositor is -- and told the session is Wayland, because
// leaving XDG_SESSION_TYPE=tty is what makes Chromium reach for X11.
func TestBrowserEnvRecoversWayland(t *testing.T) {
	rt := noDisplayEnv(t)
	makeSocket(t, filepath.Join(rt, "wayland-0"))
	// A real compositor also leaves this file; it must not be mistaken for the
	// compositor itself.
	if err := os.WriteFile(filepath.Join(rt, "wayland-0.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	env, note := browserEnv()

	if got := envValue(env, "WAYLAND_DISPLAY"); got != "wayland-0" {
		t.Fatalf("WAYLAND_DISPLAY=%q, want wayland-0", got)
	}
	if got := envValue(env, "XDG_SESSION_TYPE"); got != "wayland" {
		t.Fatalf("XDG_SESSION_TYPE=%q, want wayland: a tty session type sends the ozone picker to X11", got)
	}
	if got := envValue(env, "XDG_RUNTIME_DIR"); got != rt {
		t.Fatalf("XDG_RUNTIME_DIR=%q, want %q (a relative WAYLAND_DISPLAY is resolved against it)", got, rt)
	}
	if n := countEnv(env, "WAYLAND_DISPLAY"); n != 1 {
		t.Fatalf("emitted %d WAYLAND_DISPLAY entries: with duplicates the winner is libc-dependent", n)
	}
	if note == "" {
		t.Fatal("recovering a display must be announced, or the terminal shows a window opening for no reason")
	}
	if !strings.Contains(note, "wayland-0") {
		t.Fatalf("note %q does not name the compositor it used", note)
	}
}

// TestBrowserEnvKeepsACompleteSession: a session that already knows its display
// must be passed through untouched.
func TestBrowserEnvKeepsACompleteSession(t *testing.T) {
	rt := noDisplayEnv(t)
	makeSocket(t, filepath.Join(rt, "wayland-0"))
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_SESSION_TYPE", "wayland")

	env, note := browserEnv()

	if note != "" {
		t.Fatalf("note %q for a session that needs no repair", note)
	}
	if got := envValue(env, "WAYLAND_DISPLAY"); got != "wayland-0" {
		t.Fatalf("WAYLAND_DISPLAY=%q, want it left alone", got)
	}
}

// TestBrowserEnvFallsBackToX11: a stale or absent WAYLAND_DISPLAY with an X
// server present (XWayland counts) must still produce a usable display.
func TestBrowserEnvFallsBackToX11(t *testing.T) {
	noDisplayEnv(t) // no Wayland socket in this runtime dir
	xsock := t.TempDir()
	makeSocket(t, filepath.Join(xsock, "X0"))
	x11SocketDir = xsock

	env, note := browserEnv()

	if got := envValue(env, "DISPLAY"); got != ":0" {
		t.Fatalf("DISPLAY=%q, want :0", got)
	}
	if got := envValue(env, "XDG_SESSION_TYPE"); got != "x11" {
		t.Fatalf("XDG_SESSION_TYPE=%q, want x11", got)
	}
	if note == "" {
		t.Fatal("the X fallback must be announced too")
	}
}

// TestBrowserEnvReportsNoDisplay: with nothing to connect to, the launch is
// hopeless and must say so instead of failing silently.
func TestBrowserEnvReportsNoDisplay(t *testing.T) {
	noDisplayEnv(t)

	env, note := browserEnv()

	if !strings.Contains(note, "no Wayland or X socket") {
		t.Fatalf("note = %q, want it to report that no display exists", note)
	}
	if got := envValue(env, "DISPLAY"); got != "" {
		t.Fatalf("invented DISPLAY=%q with no X server to point at", got)
	}
	if got := envValue(env, "WAYLAND_DISPLAY"); got != "" {
		t.Fatalf("invented WAYLAND_DISPLAY=%q with no compositor to point at", got)
	}
}

// TestBrowserEnvIgnoresADeadSocket: a WAYLAND_DISPLAY naming a socket that is
// gone is not a display; fall through to what really exists.
func TestBrowserEnvIgnoresADeadSocket(t *testing.T) {
	rt := noDisplayEnv(t)
	makeSocket(t, filepath.Join(rt, "wayland-1"))
	t.Setenv("WAYLAND_DISPLAY", "wayland-9") // never created

	env, _ := browserEnv()

	if got := envValue(env, "WAYLAND_DISPLAY"); got != "wayland-1" {
		t.Fatalf("WAYLAND_DISPLAY=%q, want the compositor that exists (wayland-1)", got)
	}
}

func TestSetEnvKVReplacesInPlace(t *testing.T) {
	in := []string{"PATH=/bin", "WAYLAND_DISPLAY=wayland-9", "HOME=/root"}

	out := setEnvKV(in, "WAYLAND_DISPLAY", "wayland-0")

	if n := countEnv(out, "WAYLAND_DISPLAY"); n != 1 {
		t.Fatalf("emitted %d WAYLAND_DISPLAY entries, want 1", n)
	}
	if got := envValue(out, "WAYLAND_DISPLAY"); got != "wayland-0" {
		t.Fatalf("WAYLAND_DISPLAY=%q, want wayland-0", got)
	}
	if envValue(out, "PATH") != "/bin" || envValue(out, "HOME") != "/root" {
		t.Fatalf("clobbered another entry: %v", out)
	}

	out = setEnvKV(out, "DISPLAY", ":0")
	if got := envValue(out, "DISPLAY"); got != ":0" || len(out) != 4 {
		t.Fatalf("append failed: DISPLAY=%q len=%d, want :0 and 4", got, len(out))
	}
}

func TestX11SocketName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{":0", "0", true},
		{":12", "12", true},
		{"cachyOS:1", "1", true},
		{"localhost:10.0", "10", true},
		{":", "", false},
		{"nonsense", "", false},
		{":x", "", false},
	} {
		got, ok := x11SocketName(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("x11SocketName(%q) = %q,%v want %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// braveMissingDisplayLog is the output of the real failure, copied from a run in
// this project's Zellij pane. Everything in it except the last three lines is
// harmless noise a browser emits on a normal start, and the noise must not be
// allowed to hide the error.
const braveMissingDisplayLog = `WARNING: radv is not a conformant Vulkan implementation, testing use only.
[526039:526039:0910/201211.417118:ERROR:components/dbus/xdg/request.cc:173] Request ended (non-user cancelled).
Fontconfig error: Cannot load default config file: File not found
[526643:526643:0910/201339.807616:ERROR:ui/ozone/platform/x11/ozone_platform_x11.cc:257] Missing X server or $DISPLAY
[526643:526643:0910/201339.807630:ERROR:ui/aura/env.cc:246] The platform failed to initialize.  Exiting.
/opt/brave.com/brave-origin-nightly/brave-origin: line 30: 526643 Segmentation fault         (core dumped) "$HERE/brave" "$@"
`

// TestWindowLogProblemsFindsTheRealError is the difference between a report the
// user can act on and a wall of browser chatter.
func TestWindowLogProblemsFindsTheRealError(t *testing.T) {
	got := strings.Join(windowLogProblems(braveMissingDisplayLog), "\n")

	for _, want := range []string{"Missing X server", "failed to initialize", "Segmentation fault"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report does not mention %q:\n%s", want, got)
		}
	}
}

// TestWindowLogProblemsFallsBackToTheTail: a browser that dies without saying
// "error" still has to hand over whatever it did say.
func TestWindowLogProblemsFallsBackToTheTail(t *testing.T) {
	got := windowLogProblems("first\nsecond\nthird\n")
	if len(got) != 3 || got[2] != "third" {
		t.Fatalf("tail = %v, want the last lines", got)
	}
	if windowLogProblems("") != nil {
		t.Fatal("an empty log must produce no lines")
	}
}

// TestWindowExitReportIsSilentForAHealthyClose: the report must not cry wolf
// when the user simply closed the window.
func TestWindowExitReportIsSilentForAHealthyClose(t *testing.T) {
	bc := browserCmd{name: "chromium"}

	if report, _ := windowExitReport(bc, 20*time.Second, nil, "", false, "http://x/"); report != nil {
		t.Fatalf("reported %v for a window that was open for 20s", report)
	}
	// A viewer connected at some point: it displayed, whatever it says now.
	if report, _ := windowExitReport(bc, time.Second, nil, braveMissingDisplayLog, true, "http://x/"); report != nil {
		t.Fatalf("reported %v for a window a viewer had already connected to", report)
	}
}

// TestWindowExitReportExplainsAMissingDisplay is the regression: a browser that
// dies in its first second must be reported, with its own words, and with the
// address of the mirror that is still running.
func TestWindowExitReportExplainsAMissingDisplay(t *testing.T) {
	bc := browserCmd{name: "chromium"}

	report, keepLog := windowExitReport(bc, 1*time.Second, nil, braveMissingDisplayLog, false, "http://127.0.0.1:6969/")

	if len(report) == 0 {
		t.Fatal("a browser that died in its first second must be reported")
	}
	all := strings.Join(report, "\n")
	if !strings.Contains(all, "Missing X server") {
		t.Fatalf("the browser's own error is missing from:\n%s", all)
	}
	if !strings.Contains(all, "1.0s") {
		t.Fatalf("the report does not say how fast it died:\n%s", all)
	}
	if !strings.Contains(all, "http://127.0.0.1:6969/") {
		t.Fatalf("the report does not point at the still-running mirror:\n%s", all)
	}
	if !keepLog {
		t.Fatal("a real failure must keep the browser log for the user")
	}
}

// TestWindowExitReportRecognisesAHandover: Chromium does not start a second
// browser on a profile that is already open, it forwards the URL to the running
// one and exits. That is not a failure, and must not be reported as one.
func TestWindowExitReportRecognisesAHandover(t *testing.T) {
	bc := browserCmd{name: "chromium", profile: t.TempDir()}

	report, keepLog := windowExitReport(bc, 62*time.Millisecond, nil, "Opening in existing browser session.\n", false, "http://x/")

	if len(report) == 0 {
		t.Fatal("a handed-over launch must still be explained")
	}
	all := strings.Join(report, "\n")
	if !strings.Contains(all, "handed over") {
		t.Fatalf("the handover is not named:\n%s", all)
	}
	if strings.Contains(all, "exited after") {
		t.Fatalf("a handover is not a failure, but was reported as one:\n%s", all)
	}
	if keepLog {
		t.Fatal("a handover has nothing to debug; the log should not be kept")
	}
}

func TestProfileLockPid(t *testing.T) {
	dir := t.TempDir()

	if pid := profileLockPid(dir); pid != 0 {
		t.Fatalf("no lock file: got pid %d, want 0", pid)
	}
	if pid := profileLockPid(""); pid != 0 {
		t.Fatalf("no profile: got pid %d, want 0", pid)
	}

	lock := filepath.Join(dir, "SingletonLock")
	if err := os.Symlink("cachyOS-"+strconv.Itoa(os.Getpid()), lock); err != nil {
		t.Fatal(err)
	}
	if pid := profileLockPid(dir); pid != os.Getpid() {
		t.Fatalf("live lock: got pid %d, want %d", pid, os.Getpid())
	}

	// A reaped pid is a dead owner, which is the common case: a previous scterm
	// was killed. Reporting that as "already held by a window" would send the
	// user looking for a window that does not exist.
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	os.Remove(lock)
	if err := os.Symlink("cachyOS-"+strconv.Itoa(dead.Process.Pid), lock); err != nil {
		t.Fatal(err)
	}
	if pid := profileLockPid(dir); pid != 0 {
		t.Fatalf("stale lock: got pid %d, want 0", pid)
	}

	// Garbage must not be read as a pid either.
	os.Remove(lock)
	if err := os.Symlink("no-pid-here", lock); err != nil {
		t.Fatal(err)
	}
	if pid := profileLockPid(dir); pid != 0 {
		t.Fatalf("unparseable lock: got pid %d, want 0", pid)
	}
}

// groupMembers lists the processes in a process group. Without waiting for the
// launcher script to have actually started the browser, a shutdown test kills
// the script before there is anything to orphan -- and then passes no matter
// how the group is signalled.
func groupMembers(t *testing.T, pgid int) []string {
	t.Helper()
	files, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		t.Fatalf("glob /proc: %v", err)
	}
	var out []string
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(f, "stat"))
		if err != nil {
			continue // the process went away while we looked at it
		}
		s := string(data)
		i := strings.LastIndex(s, ")")
		if i < 0 {
			continue
		}
		// After "(comm)" come: state ppid pgrp ...
		fields := strings.Fields(s[i+2:])
		if len(fields) < 3 {
			continue
		}
		if p, err := strconv.Atoi(fields[2]); err == nil && p == pgid {
			out = append(out, filepath.Base(f))
		}
	}
	return out
}

// waitForGroup polls until the process group has at least n members.
func waitForGroup(t *testing.T, pgid, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if got := groupMembers(t, pgid); len(got) >= n {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("process group %d has %d members after %v, want %d", pgid, len(got), timeout, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestKillWindowClosesTheWholeGroup is the orphan guard: closing scterm must
// close the window, because an orphaned browser keeps the private profile
// locked and the next --window run is handed over to a window nobody wants.
func TestKillWindowClosesTheWholeGroup(t *testing.T) {
	// The shape of a real launch: a launcher script that keeps the browser as
	// its own child (`"$HERE/brave" "$@"`), all in one process group.
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pgid := cmd.Process.Pid
	// Reap as soon as it exits: an unreaped zombie is still a member of its
	// process group, so leaving it for the end of the test would make the group
	// look alive no matter what killWindow did.
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }()
	// The script must have started the browser first, or the survivor this test
	// is about does not exist yet.
	waitForGroup(t, pgid, 2, 5*time.Second)

	s := &webServer{}
	s.setWindow(cmd, "")
	start := time.Now()
	s.killWindow()
	elapsed := time.Since(start)

	if s.win != nil {
		t.Error("killWindow left the window registered, so it would be killed twice")
	}
	// SIGTERM has to reach every process in the group, not just the launcher
	// script. The script's child outlives its parent, so signalling the leader
	// alone would leave the browser running into the force-kill path -- a
	// second and a half of a shutdown that looks hung, and an orphan whenever
	// the process exits before that timer fires.
	if elapsed > windowKillPoll*8 {
		t.Fatalf("killWindow took %v for a child that dies on SIGTERM; the group is not being signalled", elapsed)
	}
	if err := syscall.Kill(-pgid, 0); err == nil {
		t.Fatal("the window process group outlived killWindow: the browser would be orphaned")
	}
	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher script was never reaped")
	}
}

// TestKillWindowWithoutAWindow: --web and TUI runs have no window, and
// shutdown must not care.
func TestKillWindowWithoutAWindow(t *testing.T) {
	(&webServer{}).killWindow()
	(&webServer{win: &exec.Cmd{}}).killWindow() // started but no process
}
