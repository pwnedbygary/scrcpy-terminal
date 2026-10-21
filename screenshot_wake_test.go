package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// A screenshot request wakes the video goroutine from a read that waits for
// the device (requestShot, shotReader). These tests pin what keeps such a wake
// from being lost: the request is registered before its deadline is set, the
// wake clears the deadline before it looks for requests, and data that comes
// back together with the deadline's timeout is kept.
// ---------------------------------------------------------------------------

// TestShotRequestsStaggeredWhileIdle: requests that come while the video
// goroutine answers an earlier one, with the device sending nothing, are all
// answered. The wake clears the read deadline before it looks for requests,
// so the deadline of a request made after the look stays set, and the next
// read answers it. Cleared after the look, that deadline could be cleared
// too, and the request would wait for a frame that does not come.
func TestShotRequestsStaggeredWhileIdle(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour)
	r := newShotRig(t, 80, 24, 1280, 2800, true)
	for pair := range 60 {
		first := r.a.stream.requestShot()
		// The second comes 0 to 1.5 ms after the first, about as long as an
		// answer takes. A sleep that short can overshoot by more than the
		// gap, so this spins.
		gap := time.Duration(pair*25) * time.Microsecond
		for start := time.Now(); time.Since(start) < gap; {
		}
		second := r.a.stream.requestShot()
		for i, ch := range []chan videoShot{first, second} {
			select {
			case shot, ok := <-ch:
				if !ok || shot.err != nil || shot.w != 1280 || shot.h != 2800 {
					t.Fatalf("pair %d, request %d: %dx%d, error %v, open %v; want the 1280x2800 frame",
						pair, i+1, shot.w, shot.h, shot.err, ok)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("pair %d: request %d (%v after the first) was not answered: its wake was lost", pair, i+1, gap)
			}
		}
	}
}

// heldConn is a video socket that holds a screenshot request once it has set
// its read deadline, until the video goroutine, woken by that deadline, has
// looked for requests and begun its next read: the request is as late as it
// can be.
type heldConn struct {
	net.Conn
	mu       sync.Mutex
	reading  bool          // a Read is under way
	nextRead chan struct{} // closed when the next Read begins
}

func newHeldConn(c net.Conn) *heldConn { return &heldConn{Conn: c, nextRead: make(chan struct{})} }

func (c *heldConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	close(c.nextRead)
	c.nextRead, c.reading = make(chan struct{}), true
	c.mu.Unlock()
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	c.reading = false
	c.mu.Unlock()
	return n, err
}

func (c *heldConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	next := c.nextRead
	c.mu.Unlock()
	err := c.Conn.SetReadDeadline(t)
	if !t.IsZero() { // a request's deadline, not the wake's clear
		select {
		case <-next:
		case <-time.After(5 * time.Second):
		}
	}
	return err
}

// waitReading waits until runVideo waits in Read for the device.
func (c *heldConn) waitReading(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		reading := c.reading
		c.mu.Unlock()
		if reading {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("runVideo did not go back to reading")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestShotRegisteredBeforeItsDeadline: requestShot registers the request
// before it sets the read deadline, so the wake that the deadline causes finds
// it. Here the request is held once its deadline is set, until the video
// goroutine has woken, looked for requests and gone back to its read, and it
// is answered all the same. Were the deadline set first, that wake would find
// no request and clear the deadline, and the request would wait for the next
// frame.
func TestShotRegisteredBeforeItsDeadline(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour)
	var video *heldConn
	r := newShotRigOn(t, 80, 24, 1280, 2800, func(c net.Conn) net.Conn {
		video = newHeldConn(c)
		return video
	})
	video.waitReading(t) // and the device sends nothing more
	ch := r.a.stream.requestShot()
	select {
	case shot, ok := <-ch:
		if !ok || shot.err != nil || shot.w != 1280 || shot.h != 2800 {
			t.Fatalf("%dx%d, error %v, open %v; want the 1280x2800 frame", shot.w, shot.h, shot.err, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request was not answered: the wake looked for it before it was registered")
	}
}

// tlsRecorder is the device's end of a TLS connection. Once keep is set,
// after the handshake, what TLS writes is kept for the test to send (take).
type tlsRecorder struct {
	net.Conn
	keep bool
	kept []byte
}

func (c *tlsRecorder) Write(p []byte) (int, error) {
	if c.keep {
		c.kept = append(c.kept, p...)
		return len(p), nil
	}
	return c.Conn.Write(p)
}

// take returns the records kept since the last take.
func (c *tlsRecorder) take() []byte {
	b := c.kept
	c.kept = nil
	return b
}

// shotTestCert is a self-signed certificate for the device's end of a test's
// TLS connection.
func shotTestCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestShotWakeKeepsTheDataThatCameWithIt: in peer mode the video socket is a
// TLS connection. With TLS 1.2, a read that has a record's data, and the start
// of an alert record behind it, reads on for the rest of the alert (crypto/tls
// looks for a close_notify there), so the wake's timeout comes back together
// with the data. The data is kept: the frame that it ends is decoded.
func TestShotWakeKeepsTheDataThatCameWithIt(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour)
	withTermSize(t, 80, 24)
	config, frames := fixturePackets(t)
	client, device := net.Pipe()
	t.Cleanup(func() { client.Close(); device.Close() })
	rec := &tlsRecorder{Conn: device}
	dev := tls.Server(rec, &tls.Config{Certificates: []tls.Certificate{shotTestCert(t)}, MaxVersion: tls.VersionTLS12})
	video := tls.Client(client, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}) // the test's own device
	handshake := make(chan error, 1)
	go func() { handshake <- dev.Handshake() }()
	if err := video.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}
	if v := video.ConnectionState().Version; v != tls.VersionTLS12 {
		t.Fatalf("TLS version %#x, want 1.2", v)
	}
	go io.Copy(io.Discard, device) // the client's close_notify, when runVideo ends
	rec.keep = true
	seal := func(packets ...[]byte) []byte { // one record per packet
		t.Helper()
		for _, p := range packets {
			if _, err := dev.Write(p); err != nil {
				t.Fatal(err)
			}
		}
		return rec.take()
	}
	send := func(b []byte) { // returns once runVideo's TLS layer has read it all
		t.Helper()
		device.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := device.Write(b); err != nil {
			t.Fatal(err)
		}
	}

	a := streamApp(&session{deviceName: "Pixel 8", video: video})
	a.tui = &tui{cols: 80, rows: 23, running: true}
	a.frames = make(chan *videoFrame, 1)
	a.stream.setSink(a)
	ended := make(chan error, 1)
	go func() { ended <- a.stream.runVideo() }()
	session := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32([]byte{0x80, 0, 0, 0}, 1280), 2800)
	send(seal([]byte("h264"), session, config, frames[0]))
	deadline := time.Now().Add(5 * time.Second)
	for a.stream.frameCount.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("runVideo did not decode the first frame")
		}
		time.Sleep(time.Millisecond)
	}

	// The second frame, all but its last 40 bytes in one record and those in
	// another, and the close_notify alert behind them.
	f := frames[1]
	head, tail := seal(f[:len(f)-40]), seal(f[len(f)-40:])
	if err := dev.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	alert := rec.take()
	if len(alert) < 3 || alert[0] != 21 {
		t.Fatalf("close_notify record % x, want an alert record (21)", alert)
	}
	send(head)
	// The second record and the first two bytes of the alert, in one write:
	// the TLS layer reads them at once, takes the record's data and reads on
	// for the rest of the alert, which does not come.
	send(append(tail, alert[:2]...))
	if got := a.stream.frameCount.Load(); got != 1 {
		t.Fatalf("test setup: %d frames decoded before the wake, want 1", got)
	}
	ch := a.stream.requestShot()
	select {
	case shot, ok := <-ch:
		if !ok || shot.err != nil || shot.w != 1280 || shot.h != 2800 {
			t.Fatalf("%dx%d, error %v, open %v; want the 1280x2800 frame", shot.w, shot.h, shot.err, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request was not answered")
	}
	send(alert[2:]) // the device closes the stream
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("runVideo did not end at the close_notify")
	}
	if got := a.stream.frameCount.Load(); got != 2 {
		t.Errorf("decoded %d frames, want 2: the end of the second frame came back with the wake's timeout, and was lost", got)
	}
}
