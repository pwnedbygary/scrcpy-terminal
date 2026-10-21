package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// newWindowServer runs a --window server whose last viewer may be gone for
// grace before the session ends. The app's event queue shows whether the
// server asked it to quit.
func newWindowServer(t *testing.T, grace time.Duration) (*webServer, *app) {
	t.Helper()
	cfg := config{web: true, window: true, webAddr: "127.0.0.1"}
	sess := &session{deviceName: "testphone"}
	a := &app{sess: sess, cfg: cfg, events: make(chan inputEvent, 64), grabbed: true}
	a.stream = &streamState{cfg: cfg, sess: sess}
	s := newWebServer(cfg, sess, a.stream, nil, nil, a)
	s.leaveGrace = grace
	a.web = s
	go func() { _ = s.run(false) }()
	select {
	case <-s.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("web server never started listening")
	}
	t.Cleanup(s.stop)
	return s, a
}

func serverHost(s *webServer) string { return fmt.Sprintf("127.0.0.1:%d", s.port) }

// quitWithin reports whether the server asked the app to quit within d. A quit
// already queued counts, however short d is.
func quitWithin(a *app, d time.Duration) bool {
	timeout := time.After(d)
	for {
		select {
		case ev := <-a.events:
			if ev.kind == evQuit {
				return true
			}
			continue
		default:
		}
		select {
		case ev := <-a.events:
			if ev.kind == evQuit {
				return true
			}
		case <-timeout:
			return false
		}
	}
}

// waitClients waits until the server counts n viewers, on its list and in
// nClients.
func waitClients(t *testing.T, s *webServer, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for s.clientCount() != n || s.nClients.Load() != int64(n) {
		if time.Now().After(deadline) {
			t.Fatalf("the server has %d viewers (nClients %d) after 3s, want %d",
				s.clientCount(), s.nClients.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestWebWindowSurvivesAReload: reloading the --window page (F5, Ctrl+R) drops
// its socket for a moment, and the session must still be there when the
// reloaded page connects.
func TestWebWindowSurvivesAReload(t *testing.T) {
	const grace = time.Second
	s, a := newWindowServer(t, grace)
	page := helloClient(t, serverHost(s))
	page.conn.Close()
	waitClients(t, s, 0)
	helloClient(t, serverHost(s))
	if quitWithin(a, grace+500*time.Millisecond) {
		t.Fatal("reloading the page ended the --window session")
	}
}

// TestWebWindowReloadCancelsTheGrace: the reloaded page's connection drops the
// pending end of the session at once, rather than leaving it to fire and find
// a viewer.
func TestWebWindowReloadCancelsTheGrace(t *testing.T) {
	s, _ := newWindowServer(t, time.Minute)
	page := helloClient(t, serverHost(s))
	page.conn.Close()
	waitClients(t, s, 0)
	s.clientsMu.Lock()
	pending := s.leaveTimer
	s.clientsMu.Unlock()
	if pending == nil {
		t.Fatal("no grace started when the last viewer left")
	}
	helloClient(t, serverHost(s))
	if pending.Stop() {
		t.Fatal("a viewer connected but the grace was left pending")
	}
}

// TestWebWindowEndsAfterItsLastViewer: closing the window still ends the
// session, once its last viewer has been gone for the whole grace.
func TestWebWindowEndsAfterItsLastViewer(t *testing.T) {
	const grace = 300 * time.Millisecond
	s, a := newWindowServer(t, grace)
	page := helloClient(t, serverHost(s))
	left := time.Now()
	page.conn.Close()
	if !quitWithin(a, 5*time.Second) {
		t.Fatal("the --window session kept running after its last viewer left")
	}
	if waited := time.Since(left); waited < grace {
		t.Fatalf("the session ended %v after its last viewer left, inside the %v grace", waited, grace)
	}
}

// TestWebStopCancelsTheWindowGrace: a stop while the grace is running leaves no
// timer behind to fire on the stopped server.
func TestWebStopCancelsTheWindowGrace(t *testing.T) {
	s, a := newWindowServer(t, time.Minute)
	page := helloClient(t, serverHost(s))
	page.conn.Close()
	waitClients(t, s, 0)
	s.clientsMu.Lock()
	pending := s.leaveTimer
	s.clientsMu.Unlock()
	if pending == nil {
		t.Fatal("no grace started when the last viewer left")
	}
	s.stop()
	if pending.Stop() {
		t.Fatal("stop left the grace pending, to fire on the stopped server")
	}
	s.clientsMu.Lock()
	left := s.leaveTimer
	s.clientsMu.Unlock()
	if left != nil {
		t.Error("stop left a grace timer registered")
	}
	if quitWithin(a, 50*time.Millisecond) {
		t.Error("the server asked the app to quit although it was stopped")
	}
}

// TestWebFailedWriteClosesTheClient: a viewer whose status write fails is
// closed for good: off the client list, out of the count and with its socket
// shut. Before, it stayed registered, and window mode counted it as a viewer
// for the rest of the session.
func TestWebFailedWriteClosesTheClient(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := helloClient(t, h.url())
	sc := onlyClient(t, h.srv)
	tcp, ok := sc.ws.conn.(*net.TCPConn)
	if !ok {
		t.Fatalf("the server's socket is a %T, not a TCP connection", sc.ws.conn)
	}
	// Writes to the viewer fail from here on while reads still work, so
	// nothing but the failed write can notice.
	if err := tcp.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	cl.send(webEvent{Op: "ping"}) // answered with a status: the write that fails
	select {
	case <-sc.closeCh:
	case <-time.After(3 * time.Second):
		t.Fatal("the client whose write failed was never closed")
	}
	waitClients(t, h.srv, 0)
	if err := sc.ws.conn.SetDeadline(time.Time{}); !errors.Is(err, net.ErrClosed) {
		t.Errorf("the failed client's socket is still open (SetDeadline: %v)", err)
	}
}

// drainedClient is a viewer that reads, and throws away, everything it is sent.
func drainedClient(t *testing.T, host string) *testClient {
	t.Helper()
	cl := helloClient(t, host)
	_ = cl.conn.SetReadDeadline(time.Time{})
	go func() {
		for {
			if _, _, err := cl.ws.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return cl
}

// TestWebClientListChurn: frames and PCM fan out over a snapshot of the client
// list taken under the lock and walked without it, so viewers arriving and
// leaving must never rewrite a slice a walk may still be reading. It needs
// -race: a removal in place races the walk even when no frame goes astray.
func TestWebClientListChurn(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	var viewers []*testClient
	for range 4 {
		viewers = append(viewers, drainedClient(t, h.url()))
	}
	waitClients(t, h.srv, 4)

	stop := make(chan struct{})
	walked := make(chan struct{})
	go func() {
		defer close(walked)
		pcm := make([]byte, 960)
		// Small enough that no pooled output buffer can ever be one of these.
		cv := computeWebCanvas(16, 16, 16, 16)
		for {
			select {
			case <-stop:
				return
			default:
			}
			h.srv.pushPCM(pcm)
			h.srv.frame(&videoFrame{rgb: make([]byte, cv.cw*cv.ch*4), w: 16, h: 16, cw: cv.cw, ch: cv.ch, fps: 60})
			time.Sleep(100 * time.Microsecond)
		}
	}()
	for range 30 {
		// The oldest viewer leaves, from the front of the list, and a new one
		// arrives at its end.
		viewers[0].conn.Close()
		viewers = append(viewers[1:], drainedClient(t, h.url()))
		waitClients(t, h.srv, 4)
	}
	close(stop)
	<-walked
}

// stallSender feeds raw frames to a viewer that reads nothing until the
// server's sender is stuck writing to it, holding its socket's write lock.
func stallSender(t *testing.T, s *webServer, sc *webClient) {
	t.Helper()
	cv := computeWebCanvas(480, 1080, 480, 1080)
	held := 0
	for deadline := time.Now().Add(10 * time.Second); held < 30; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the sender never got stuck writing to a viewer that reads nothing")
		}
		if !sc.ws.wmu.TryLock() {
			held++
			continue
		}
		sc.ws.wmu.Unlock()
		held = 0
		s.frame(&videoFrame{rgb: make([]byte, cv.cw*cv.ch*4), w: 480, h: 1080, cw: cv.cw, ch: cv.ch, fps: 60})
	}
}

// TestWebStopWithAStalledViewerClosesTheWindow: a viewer that stopped reading
// must not hold the first stop past the point where a second stop, the app
// loop's on its way out of the process, gives up waiting for it. The process
// would then exit before the first stop closed the --window browser, leaving it
// running and holding its private profile lock. Viewers still reading get
// their bye as before.
func TestWebStopWithAStalledViewerClosesTheWindow(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	stalled := dialWS(t, h.url())
	stalled.send(webEvent{Op: "hello", W: 480, H: 1080, Format: "raw"})
	stalled.nextJSON("hello", 3*time.Second)
	sc := onlyClient(t, h.srv)

	reading := dialWS(t, h.url())
	reading.send(webEvent{Op: "hello", W: 480, H: 1080, Format: "raw"})
	reading.nextJSON("hello", 3*time.Second)
	bye := make(chan error, 1)
	go func() {
		m, err := reading.readByeBeforeClose(30 * time.Second)
		if err == nil && m["reason"] != "Quit from the browser." {
			err = fmt.Errorf("bye reason %q, want %q", m["reason"], "Quit from the browser.")
		}
		bye <- err
	}()

	win := exec.Command("sleep", "60")
	win.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := win.Start(); err != nil {
		t.Fatalf("start the stand-in window: %v", err)
	}
	pgid := win.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	exited := make(chan struct{})
	go func() { _ = win.Wait(); close(exited) }()
	h.srv.setWindow(win, "")

	stallSender(t, h.srv, sc)

	start := time.Now()
	first := make(chan struct{})
	go func() { h.srv.stopWith("Quit from the browser."); close(first) }()
	for !h.srv.closing.Load() {
		time.Sleep(time.Millisecond)
	}
	h.srv.stop()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatalf("the window was still open %v after the stop began: a viewer that stopped reading held up the first stop",
			time.Since(start).Round(time.Millisecond))
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("the first stop was still running after the window closed")
	}
	t.Logf("stopped with a stalled viewer in %v", time.Since(start).Round(time.Millisecond))
	if err := <-bye; err != nil {
		t.Errorf("the viewer that kept reading: %v", err)
	}
}

// TestWSCloseBoundsARacingWrite: a writer that took the write lock just as a
// close began must not push the socket's deadline back out to writeTimeout,
// or the close waits behind its write for all of it.
func TestWSCloseBoundsARacingWrite(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	peer, err := net.Dial("tcp", ln.Addr().String()) // never reads
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	ws := &wsConn{conn: conn, br: bufio.NewReader(conn), bw: bufio.NewWriter(conn), writeTimeout: 30 * time.Second}

	ws.wmu.Lock() // a writer has the lock and is about to write
	closed := make(chan struct{})
	go func() { _ = ws.CloseWith(`{"op":"bye"}`); close(closed) }()
	for deadline := time.Now().Add(3 * time.Second); ws.closeBy.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			ws.wmu.Unlock()
			t.Fatal("the close never set its deadline")
		}
	}
	wrote := make(chan error, 1)
	go func() { wrote <- ws.writeFrame(wsOpBinary, make([]byte, 64<<20)) }()
	select {
	case err := <-wrote:
		if err == nil {
			t.Error("a 64 MiB write to a peer that reads nothing succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a write that began as the close did kept the full writeTimeout, so the close waits behind it")
	}
	ws.wmu.Unlock()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the close never finished")
	}
}
