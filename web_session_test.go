package main

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
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

// churnDial opens a viewer's socket and waits for the server to accept the
// handshake. Unlike dialWS it may run off the test's goroutine.
func churnDial(host string) (net.Conn, error) {
	conn, err := net.Dial("tcp", host)
	if err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString([]byte("scterm-test-key"))
	req := "GET /ws HTTP/1.1\r\nHost: " + host + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		conn.Close()
		return nil, fmt.Errorf("handshake: %q, %v", strings.TrimSpace(status), err)
	}
	return conn, nil
}

// TestWebViewerCountFollowsTheList: the viewer count is stored with the list
// it counts, under the same lock, so whoever holds that lock finds the two
// agree, and viewers arriving and leaving together cannot leave the count
// stale. Before, each connect and each leave stored the count after letting go
// of the lock: for a moment the count lagged the list, and the later of two
// stores could carry the older length for good. Eight viewers at a time come
// and go while a checker takes the lock whenever it can and compares. The old
// gap is a few instructions wide, so this is a stress test, not a
// deterministic one.
func TestWebViewerCountFollowsTheList(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	s := h.srv
	stop := make(chan struct{})
	found := make(chan string, 1)
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if !s.clientsMu.TryLock() {
				continue
			}
			n, count := len(s.clients), s.nClients.Load()
			s.clientsMu.Unlock()
			if int64(n) != count {
				found <- fmt.Sprintf("%d viewers on the list and a count of %d", n, count)
				return
			}
		}
	}()
	stopChecker := sync.OnceFunc(func() { close(stop); <-checked })
	defer stopChecker()
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 25 {
				conn, err := churnDial(h.url())
				if err != nil {
					errs <- err
					return
				}
				conn.Close()
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	waitClients(t, s, 0)
	stopChecker()
	select {
	case m := <-found:
		t.Fatalf("while viewers came and went, the server had %s", m)
	default:
	}
}

// hijackOnCue is the ResponseWriter of a WebSocket handshake whose upgrade
// completes only when the test closes cue.
type hijackOnCue struct {
	conn net.Conn
	cue  chan struct{}
}

func (w *hijackOnCue) Header() http.Header         { return http.Header{} }
func (w *hijackOnCue) Write(b []byte) (int, error) { return len(b), nil }
func (w *hijackOnCue) WriteHeader(int)             {}

func (w *hijackOnCue) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	<-w.cue
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

// lateViewer starts a viewer's handshake on s whose upgrade completes when
// upgrade is called; upgrade returns the viewer's end once the server has
// answered the handshake. served is closed when the server's handler returns.
func lateViewer(t *testing.T, s *webServer) (upgrade func() *testClient, served <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	viewer, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { viewer.Close() })
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	r, err := http.NewRequest(http.MethodGet, "http://"+serverHost(s)+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString([]byte("scterm-test-key")))
	r.Header.Set("Sec-WebSocket-Version", "13")
	w := &hijackOnCue{conn: conn, cue: make(chan struct{})}
	done := make(chan struct{})
	go func() { s.handleWS(w, r); close(done) }()
	cue := sync.OnceFunc(func() { close(w.cue) })
	t.Cleanup(cue)

	upgrade = func() *testClient {
		t.Helper()
		cue()
		if err := viewer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		br := bufio.NewReader(viewer)
		status, err := br.ReadString('\n')
		if err != nil || !strings.Contains(status, "101") {
			t.Fatalf("the late handshake was answered %q, %v", strings.TrimSpace(status), err)
		}
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				t.Fatalf("the late handshake's headers: %v", err)
			}
			if strings.TrimRight(line, "\r\n") == "" {
				break
			}
		}
		return &testClient{t: t, conn: viewer, br: br,
			ws: &wsConn{conn: viewer, br: br, bw: bufio.NewWriter(viewer), client: true, writeTimeout: 5 * time.Second}}
	}
	return upgrade, done
}

// TestWebLateViewerGetsTheBye: a viewer whose upgrade completes once the stop
// has taken its list of viewers to close, or after the stop, is told the
// session is over and closed, like the viewers the stop found. Before, it was
// added to the list after the stop had copied it: no bye, never closed.
func TestWebLateViewerGetsTheBye(t *testing.T) {
	const reason = "Quit from the browser."
	served := func(t *testing.T, served <-chan struct{}) {
		t.Helper()
		select {
		case <-served:
		case <-time.After(3 * time.Second):
			t.Fatal("the late viewer's handler never returned")
		}
	}
	t.Run("while the stop says bye to the others", func(t *testing.T) {
		h := newWebTestHarness(t, config{web: true}, false)
		other := helloClient(t, h.url())
		so := onlyClient(t, h.srv)
		upgrade, done := lateViewer(t, h.srv)
		letBye := stall(t, &so.ws.wmu) // the stop now waits to write the other viewer's bye
		stopped := make(chan struct{})
		go func() { h.srv.stopWith(reason); close(stopped) }()
		eventually(t, "the stop is closing the viewers it found", so.closed.Load)
		late := upgrade()
		served(t, done)
		letBye()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatal("the stop never returned")
		}
		for name, cl := range map[string]*testClient{"the late viewer": late, "the other viewer": other} {
			if got := cl.byeBeforeClose(3 * time.Second)["reason"]; got != reason {
				t.Errorf("%s: bye reason %q, want %q", name, got, reason)
			}
		}
		if n := h.srv.clientCount(); n != 0 {
			t.Errorf("%d viewers still listed after the stop, want 0", n)
		}
	})
	t.Run("after the stop", func(t *testing.T) {
		h := newWebTestHarness(t, config{web: true}, false)
		upgrade, done := lateViewer(t, h.srv)
		h.srv.stopWith(reason)
		late := upgrade()
		served(t, done)
		if got := late.byeBeforeClose(3 * time.Second)["reason"]; got != reason {
			t.Errorf("bye reason %q, want %q", got, reason)
		}
		if n := h.srv.clientCount(); n != 0 {
			t.Errorf("%d viewers listed after the stop, want 0", n)
		}
	})
}

// TestWebWindowEndsWithAFullEventQueue: the end of a --window session reaches
// the app even when the app's event queue is full as the grace runs out: the
// quit waits for room. Before, it was dropped, and the session outlived its
// window.
func TestWebWindowEndsWithAFullEventQueue(t *testing.T) {
	const grace = 50 * time.Millisecond
	s, a := newWindowServer(t, grace)
	page := helloClient(t, serverHost(s))
	for len(a.events) < cap(a.events) {
		a.events <- inputEvent{kind: evTick}
	}
	page.conn.Close()
	waitClients(t, s, 0)
	eventually(t, "the grace has run out", func() bool {
		s.clientsMu.Lock()
		defer s.clientsMu.Unlock()
		return s.leaveTimer == nil
	})
	// The timer has had ample time to try to queue the quit.
	time.Sleep(100 * time.Millisecond)
	if !quitWithin(a, 2*time.Second) {
		t.Fatal("the --window session's quit was lost to a full event queue")
	}
}
