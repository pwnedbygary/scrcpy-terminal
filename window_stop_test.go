package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A stop closes the --window browser it finds (killWindow). These tests check
// that no browser starts once the stop has looked: it would outlive scterm and
// hold the private profile's lock against the next --window run. The browser
// is a stand-in, started through windowStart.

// standInWindow makes openWindow find a stand-in browser through SCTERM_CHROME,
// a script that sleeps, with its private profile made already, no display, and
// its log in a scratch TMPDIR, which it returns; and makes openWindow start it
// with start instead of cmd.Start (with start nil, nothing starts). starts says
// how many starts openWindow asked for. A stand-in still running when the test
// ends is killed, with its process group.
func standInWindow(t *testing.T, start func(*exec.Cmd) error) (starts func() int, tmp string) {
	t.Helper()
	noDisplayEnv(t)
	dir := t.TempDir()
	browser := filepath.Join(dir, "browser")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cache, tmp := filepath.Join(dir, "cache"), filepath.Join(dir, "tmp")
	for _, d := range []string{filepath.Join(cache, "scterm", "chromium"), tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SCTERM_CHROME", browser)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("TMPDIR", tmp)
	var mu sync.Mutex
	asked := 0
	var started []*exec.Cmd
	old := windowStart
	windowStart = func(cmd *exec.Cmd) error {
		mu.Lock()
		asked++
		mu.Unlock()
		if start == nil {
			return nil
		}
		err := start(cmd)
		if cmd.Process != nil {
			mu.Lock()
			started = append(started, cmd)
			mu.Unlock()
		}
		return err
	}
	t.Cleanup(func() {
		windowStart = old
		mu.Lock()
		defer mu.Unlock()
		for _, cmd := range started {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return asked
	}, tmp
}

// windowServer is a --window server that has not started serving.
func windowServer() *webServer {
	cfg := config{web: true, window: true, webAddr: "127.0.0.1"}
	return newWebServer(cfg, &session{deviceName: "testphone"}, &streamState{cfg: cfg}, nil, nil, nil)
}

// windowOf is the window s would close.
func windowOf(s *webServer) *exec.Cmd {
	s.winMu.Lock()
	defer s.winMu.Unlock()
	return s.win
}

// browserLogs lists the browser logs left in tmp.
func browserLogs(tmp string) []string {
	logs, _ := filepath.Glob(filepath.Join(tmp, "scterm-browser-*.log"))
	return logs
}

// stopping reports whether the stop has taken its list of viewers to close,
// after which it closes the window.
func stopping(s *webServer) bool {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	return s.stopBye != ""
}

// TestWebWindowNotOpenedAfterAStop: a stop that comes after run has published
// its server and before it opens the window closes the window it finds, none.
// The window then does not open: no browser starts, nothing is printed and no
// log is left. Before, the browser opened after the stop and outlived scterm.
func TestWebWindowNotOpenedAfterAStop(t *testing.T) {
	starts, tmp := standInWindow(t, nil)
	s := windowServer()
	s.stop()
	var err error
	if out := captureStderr(t, func() { err = s.openWindow("http://127.0.0.1:6969/") }); err != nil || out != "" {
		t.Errorf("openWindow after a stop: %v, and printed %q; want nil and nothing", err, out)
	}
	if n := starts(); n != 0 || windowOf(s) != nil {
		t.Errorf("openWindow after a stop started %d browsers; want none", n)
	}
	if logs := browserLogs(tmp); len(logs) != 0 {
		t.Errorf("openWindow after a stop left %q", logs)
	}
}

// TestWebWindowNotOpenedOnceAStopHasBegun: a stop that begins while openWindow
// gets the browser ready keeps it from starting, though the stop may not have
// looked for a window yet: openWindow looks for a stop under winMu, which the
// stop's killWindow takes. Here the test holds winMu until the stop has begun.
// Before, openWindow started the browser first, and then told the stop of it,
// whether or not the stop had looked already.
func TestWebWindowNotOpenedOnceAStopHasBegun(t *testing.T) {
	starts, tmp := standInWindow(t, nil)
	s := windowServer()
	let := stall(t, &s.winMu)
	opened := make(chan error, 1)
	go func() { opened <- s.openWindow("http://127.0.0.1:6969/") }()
	// openWindow makes the browser's log just before it starts the browser.
	eventually(t, "openWindow has made the browser's log", func() bool { return len(browserLogs(tmp)) > 0 })
	stopped := make(chan struct{})
	go func() { s.stop(); close(stopped) }()
	eventually(t, "the stop has begun", s.closing.Load)
	let()
	select {
	case err := <-opened:
		if err != nil {
			t.Errorf("openWindow: %v; want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openWindow never returned")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop never returned")
	}
	if n := starts(); n != 0 || windowOf(s) != nil {
		t.Errorf("%d browsers started after the stop had begun; want none", n)
	}
	if logs := browserLogs(tmp); len(logs) != 0 {
		t.Errorf("openWindow left %q", logs)
	}
}

// TestWebStopClosesAWindowBeingStarted: a stop that begins while the browser
// is being started waits for it, then closes it with its process group, as it
// closes a window that was open before. The stand-in start begins the stop, and
// gives it time to finish without the window, as it could if it did not wait,
// before it starts the browser.
func TestWebStopClosesAWindowBeingStarted(t *testing.T) {
	s := windowServer()
	// As if a viewer had connected, so that the stand-in's end is not
	// reported on stderr after the test.
	s.hadClient.Store(true)
	stopped := make(chan struct{})
	pgid := 0
	starts, _ := standInWindow(t, func(cmd *exec.Cmd) error {
		go func() { s.stop(); close(stopped) }()
		for deadline := time.Now().Add(5 * time.Second); !stopping(s); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				return errors.New("the stop never got to the window")
			}
		}
		select {
		case <-stopped:
		case <-time.After(200 * time.Millisecond):
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		pgid = cmd.Process.Pid
		return nil
	})
	if err := s.openWindow("http://127.0.0.1:6969/"); err != nil {
		t.Fatalf("openWindow: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop never returned")
	}
	if n := starts(); n != 1 || pgid == 0 {
		t.Fatalf("%d browsers started, want 1", n)
	}
	if syscall.Kill(-pgid, 0) == nil {
		t.Error("the browser that started as the stop began outlived the stop, and holds the profile's lock")
	}
	if windowOf(s) != nil {
		t.Error("the stop left the window registered")
	}
}
