package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// readByeBeforeClose reads frames until the server's close frame and returns
// the bye that came right before it. A socket that ends without a close frame,
// closes without a bye, or sends anything between the bye and the close frame
// is an error.
func (c *testClient) readByeBeforeClose(timeout time.Duration) (map[string]any, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	var bye map[string]any
	for {
		_, op, payload, err := c.ws.readFrame()
		if err != nil {
			return nil, fmt.Errorf("the socket ended without a close frame (bye: %v): %v", bye, err)
		}
		switch op {
		case wsOpText, wsOpBinary:
			if bye != nil {
				return nil, fmt.Errorf("a message came between the bye and the close frame: %.60q", payload)
			}
			var m map[string]any
			if op == wsOpText && json.Unmarshal(payload, &m) == nil && m["op"] == "bye" {
				bye = m
			}
		case wsOpClose:
			if bye == nil {
				return nil, errors.New("the server closed the socket without a bye")
			}
			return bye, nil
		}
	}
}

// byeBeforeClose is readByeBeforeClose, failing the test on an error.
func (c *testClient) byeBeforeClose(timeout time.Duration) map[string]any {
	c.t.Helper()
	bye, err := c.readByeBeforeClose(timeout)
	if err != nil {
		c.t.Fatal(err)
	}
	return bye
}

// onlyClient is the server's side of its one viewer.
func onlyClient(t *testing.T, s *webServer) *webClient {
	t.Helper()
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if len(s.clients) != 1 {
		t.Fatalf("%d clients, want 1", len(s.clients))
	}
	return s.clients[0]
}

func helloClient(t *testing.T, host string) *testClient {
	t.Helper()
	cl := dialWS(t, host)
	cl.send(webEvent{Op: "hello", W: 480, H: 1080})
	cl.nextJSON("hello", 3*time.Second)
	return cl
}

// TestWebStopSaysBye: a server that stops tells every viewer why before it
// closes the socket, so the page can stop retrying a session that is over.
func TestWebStopSaysBye(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	clients := []*testClient{helloClient(t, h.url()), helloClient(t, h.url())}
	h.srv.stop()
	for i, cl := range clients {
		if got := cl.byeBeforeClose(3 * time.Second)["reason"]; got != "scterm stopped." {
			t.Errorf("client %d: bye reason %q, want %q", i, got, "scterm stopped.")
		}
	}
}

// TestWebQuitSaysBye: Quit in one browser ends the session for every viewer,
// and each is told it was quit from the browser. Both dispatch paths: the
// app's dispatcher and the headless one.
func TestWebQuitSaysBye(t *testing.T) {
	for _, withApp := range []bool{true, false} {
		t.Run(fmt.Sprintf("app=%v", withApp), func(t *testing.T) {
			h := newWebTestHarness(t, config{web: true}, withApp)
			quitter := helloClient(t, h.url())
			other := helloClient(t, h.url())
			quitter.send(webEvent{Op: "quit"})
			for name, cl := range map[string]*testClient{"quitter": quitter, "other viewer": other} {
				if got := cl.byeBeforeClose(3 * time.Second)["reason"]; got != "Quit from the browser." {
					t.Errorf("%s: bye reason %q, want %q", name, got, "Quit from the browser.")
				}
			}
		})
	}
}

// TestWebByeIsTheLastMessage: whatever the client's sender is writing when the
// server stops, nothing lands between the bye and the close frame.
func TestWebByeIsTheLastMessage(t *testing.T) {
	for range 20 {
		h := newWebTestHarness(t, config{web: true}, false)
		cl := helloClient(t, h.url())
		sc := onlyClient(t, h.srv)
		go func() {
			for sc.ws.WriteText(`{"op":"status"}`) == nil {
			}
		}()
		read := make(chan error, 1)
		go func() {
			_, err := cl.readByeBeforeClose(5 * time.Second)
			read <- err
		}()
		h.srv.stopWith("Quit from the browser.")
		if err := <-read; err != nil {
			t.Fatal(err)
		}
	}
}

// TestWebSecondStopWaitsForTheFirst: Quit from the browser stops the server on
// the dispatcher's goroutine, and the app loop then calls stop again on its way
// out of the process. That second call must wait for the first, or the process
// can exit before the bye is written.
func TestWebSecondStopWaitsForTheFirst(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := helloClient(t, h.url())
	sc := onlyClient(t, h.srv)

	sc.ws.wmu.Lock() // the first stop now waits to write the bye
	first := make(chan struct{})
	go func() { h.srv.stopWith("Quit from the browser."); close(first) }()
	for deadline := time.Now().Add(3 * time.Second); !h.srv.closing.Load(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			sc.ws.wmu.Unlock()
			t.Fatal("the first stop never started")
		}
	}
	second := make(chan struct{})
	go func() { h.srv.stop(); close(second) }()
	select {
	case <-second:
		sc.ws.wmu.Unlock()
		t.Fatal("a second stop returned while the first was still writing the bye")
	case <-time.After(200 * time.Millisecond):
	}
	sc.ws.wmu.Unlock()
	for name, done := range map[string]chan struct{}{"first": first, "second": second} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("the %s stop never returned", name)
		}
	}
	if got := cl.byeBeforeClose(3 * time.Second)["reason"]; got != "Quit from the browser." {
		t.Errorf("bye reason %q, want %q", got, "Quit from the browser.")
	}
}

// TestWebStreamEndSaysBye: when the device's video stream ends, runWeb stops
// the server and the viewers are told so, rather than just being dropped.
func TestWebStreamEndSaysBye(t *testing.T) {
	video, device := net.Pipe()
	t.Cleanup(func() { device.Close() })
	// runWeb takes SIGINT/SIGTERM/SIGHUP for the app and never gives them back.
	t.Cleanup(func() { signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP) })

	cfg := config{web: true, webAddr: "127.0.0.1"}
	sess := &session{deviceName: "testphone", video: video}
	a := &app{sess: sess, cfg: cfg, events: make(chan inputEvent, 64)}
	a.stream = &streamState{cfg: cfg, sess: sess}
	a.web = newWebServer(cfg, sess, a.stream, nil, nil, a)
	t.Cleanup(a.web.stop)

	ran := make(chan error, 1)
	go func() { ran <- a.runWeb() }()
	select {
	case <-a.web.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("web server never started listening")
	}
	cl := helloClient(t, fmt.Sprintf("127.0.0.1:%d", a.web.port))

	// A zero codec id is the device declining to stream video, which ends
	// runVideo with an error.
	if _, err := device.Write([]byte{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if got := cl.byeBeforeClose(5 * time.Second)["reason"]; got != "The device stream ended." {
		t.Errorf("bye reason %q, want %q", got, "The device stream ended.")
	}
	select {
	case err := <-ran:
		if err == nil {
			t.Error("runWeb returned no error after the video stream ended")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWeb kept running after the video stream ended")
	}
}
