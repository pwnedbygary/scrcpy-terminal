package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// wsTestPair starts a minimal websocket server (no net/http: the test must
// observe exactly the bytes on the wire) and returns a paired client/server
// wsConn.
func wsTestPair(t *testing.T) (*wsConn, *wsConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	type result struct {
		c   *wsConn
		err error
	}
	serverCh := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverCh <- result{err: err}
			return
		}
		br := bufio.NewReader(conn)
		fake := &bytesResponseWriter{conn: conn, br: br, bw: bufio.NewWriter(conn)}
		req, err := http.ReadRequest(br)
		if err != nil {
			serverCh <- result{err: err}
			return
		}
		if !wsIsUpgrade(req) {
			serverCh <- result{err: errNotUpgrade}
			return
		}
		c, err := wsUpgrade(fake, req)
		serverCh <- result{c: c, err: err}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	req := "GET /ws HTTP/1.1\r\nHost: " + ln.Addr().String() + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("status line %q, want 101", strings.TrimSpace(status))
	}
	var accept string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "Sec-WebSocket-Accept") {
			accept = strings.TrimSpace(v)
		}
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	if want := base64.StdEncoding.EncodeToString(sum[:]); accept != want {
		t.Fatalf("accept %q, want %q", accept, want)
	}
	_ = conn.SetReadDeadline(time.Time{})

	var server *wsConn
	select {
	case res := <-serverCh:
		if res.err != nil {
			t.Fatalf("server upgrade: %v", res.err)
		}
		server = res.c
	case <-time.After(5 * time.Second):
		t.Fatal("server side never upgraded")
	}
	client := &wsConn{conn: conn, br: br, bw: bufio.NewWriter(conn), client: true,
		writeTimeout: 3 * time.Second}
	return client, server
}

var errNotUpgrade = errors.New("request was not a websocket upgrade")

// bytesResponseWriter is the minimum http.ResponseWriter wsUpgrade needs; the
// test drives the handshake bytes directly so nothing is buffered away.
type bytesResponseWriter struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
}

func (w *bytesResponseWriter) Header() http.Header         { return http.Header{} }
func (w *bytesResponseWriter) WriteHeader(int)             {}
func (w *bytesResponseWriter) Write(b []byte) (int, error) { return w.bw.Write(b) }
func (w *bytesResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(w.br, w.bw), nil
}

// writeMasked writes a client->server frame (client frames must be masked).
func (c *wsConn) writeMasked(t *testing.T, opcode byte, payload []byte) {
	t.Helper()
	var hdr []byte
	n := len(payload)
	switch {
	case n < 126:
		hdr = []byte{0x80 | opcode, 0x80 | byte(n)}
	case n <= 0xffff:
		hdr = []byte{0x80 | opcode, 0x80 | 126, 0, 0}
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = []byte{0x80 | opcode, 0x80 | 127, 0, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i&3]
	}
	if _, err := c.conn.Write(append(append(hdr, mask[:]...), masked...)); err != nil {
		t.Fatalf("write masked frame: %v", err)
	}
}

// readServerFrame reads one unmasked server frame.
func readServerFrame(t *testing.T, c *wsConn) (byte, []byte) {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	fin, op, payload, err := c.readFrame()
	if err != nil {
		t.Fatalf("read server frame: %v", err)
	}
	if !fin {
		t.Fatal("server frame was not FIN")
	}
	return op, payload
}

func TestWSUpgradeAndEcho(t *testing.T) {
	client, server := wsTestPair(t)
	defer server.Close()

	// server -> client, all three payload-length encodings
	for _, n := range []int{5, 200, 70000} {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte(i)
		}
		if err := server.WriteBinary(payload); err != nil {
			t.Fatalf("server write %d bytes: %v", n, err)
		}
		op, got := readServerFrame(t, client)
		if op != wsOpBinary || len(got) != n {
			t.Fatalf("got opcode %#x len %d, want binary len %d", op, len(got), n)
		}
		for i := range got {
			if got[i] != payload[i] {
				t.Fatalf("payload mismatch at %d (n=%d)", i, n)
			}
		}
	}

	if err := server.WriteText(`{"op":"status"}`); err != nil {
		t.Fatalf("text write: %v", err)
	}
	op, got := readServerFrame(t, client)
	if op != wsOpText || string(got) != `{"op":"status"}` {
		t.Fatalf("text frame = (%#x, %q)", op, got)
	}
}

func TestWSClientToServer(t *testing.T) {
	client, server := wsTestPair(t)
	defer server.Close()
	server.writeTimeout = 3 * time.Second

	client.writeMasked(t, wsOpText, []byte(`{"op":"ping"}`))
	op, payload, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != wsOpText || string(payload) != `{"op":"ping"}` {
		t.Fatalf("got (%#x, %q)", op, payload)
	}

	// A ping is answered from inside the read loop (the pong is written before
	// ReadMessage returns), so the client must drain it afterwards.
	client.writeMasked(t, wsOpPing, []byte("hi"))
	done := make(chan struct{})
	go func() {
		// ReadMessage consumes the ping and returns the next data message; it
		// blocks until one arrives, which is where the deadline matters.
		_, _, _ = server.ReadMessage()
		close(done)
	}()
	op, payload = readServerFrame(t, client)
	if op != wsOpPong || string(payload) != "hi" {
		t.Fatalf("pong = (%#x, %q)", op, payload)
	}
	client.writeMasked(t, wsOpText, []byte("next"))
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("server never returned from ReadMessage after the ping")
	}

	// close from the client ends the loop with EOF
	client.writeMasked(t, wsOpClose, []byte{0x03, 0xe8})
	if _, _, err := server.ReadMessage(); err != io.EOF {
		t.Fatalf("after close: err = %v, want EOF", err)
	}
}

func TestWSRejectsUnmaskedClientFrame(t *testing.T) {
	client, server := wsTestPair(t)
	defer server.Close()

	// hand-roll an unmasked frame, which RFC 6455 forbids from a client
	frame := append([]byte{0x81, 0x02}, 'h', 'i')
	if _, err := client.conn.Write(frame); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.ReadMessage(); err == nil {
		t.Fatal("expected the unmasked frame to be rejected")
	}
}

func TestWSHeaderToken(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "keep-alive, Upgrade")
	h.Add("Upgrade", "websocket")
	if !headerHasToken(h, "Connection", "upgrade") {
		t.Error("Connection token not matched")
	}
	if !headerHasToken(h, "Upgrade", "websocket") {
		t.Error("Upgrade token not matched")
	}
	if headerHasToken(h, "Connection", "close") {
		t.Error("false positive on Connection")
	}
	r := &http.Request{Method: "GET", Header: h}
	if !wsIsUpgrade(r) {
		t.Error("wsIsUpgrade said no")
	}
	r.Method = "POST"
	if wsIsUpgrade(r) {
		t.Error("wsIsUpgrade accepted POST")
	}
}
