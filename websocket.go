package main

// Minimal RFC 6455 server-side WebSocket, implemented here rather than pulled
// in as a dependency: scterm has no external modules and the display path only
// needs binary push + small JSON control frames.
//
// Deliberately supported subset:
//   - server->client: unmasked binary/text, fragmented sends, close
//   - client->server: masked binary/text/close/ping, continuation frames
//   - no extensions, no permessage-deflate (frames are already JPEG/PCM),
//     no UTF-8 validation beyond what the JSON decoder does itself
//
// Latency notes: every Write is a single SetWriteDeadline + bufio flush, and
// the client side never buffers more than one queued video frame.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xa

	// Frames are JPEG stills (tens of KB) and PCM bursts (a few KB); 8 MiB is
	// far above anything legitimate and bounds a hostile client's memory.
	wsMaxPayload = 8 << 20

	wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

// wsConn is a hijacked HTTP connection speaking WebSocket.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer

	// wmu serializes whole frames. Only the owning goroutine writes frames in
	// the normal case, but Close (which may come from a reader or the server)
	// must not interleave with a frame that is being written.
	wmu sync.Mutex

	// scratch header buffer, reused per write
	hdr [10]byte

	writeTimeout time.Duration

	// closeOnce makes Close idempotent and thread-safe: the reader and the
	// writer goroutines both call it, and the close frame must not race the
	// writer's frame buffer.
	closeOnce sync.Once

	// client marks a conn that dialled out (tests, future client modes): the
	// masking rules are asymmetric, so the reader needs to know which end it
	// is. Servers must send unmasked frames, clients must mask theirs.
	client bool
}

// wsIsUpgrade reports whether the request is a WebSocket handshake.
func wsIsUpgrade(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if !headerHasToken(r.Header, "Connection", "upgrade") {
		return false
	}
	return headerHasToken(r.Header, "Upgrade", "websocket")
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// wsUpgrade performs the server handshake and takes over the connection.
func wsUpgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "" && v != "13" {
		return nil, fmt.Errorf("unsupported websocket version %q", v)
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("response writer cannot hijack")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	// Video frames are pushed continuously; do not let a stalled client pin a
	// goroutine forever. 30s is only ever hit by a client that stopped reading
	// entirely (its frame mailbox would have been dropped long before).
	return &wsConn{
		conn:         conn,
		br:           rw.Reader,
		bw:           rw.Writer,
		writeTimeout: 30 * time.Second,
	}, nil
}

// Close sends a close frame (best effort) and closes the TCP connection.
// It is safe to call from any goroutine, repeatedly.
func (c *wsConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		// Take the write lock so the close frame cannot interleave with a
		// frame that is mid-write, then take the socket down.
		c.wmu.Lock()
		_ = c.writeFrame(wsOpClose, []byte{0x03, 0xe8}) // 1000 normal closure
		_ = c.conn.Close()
		c.wmu.Unlock()
	})
	return nil
}

// writeFrame writes one unfragmented, unmasked frame. Callers hold wmu.
func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	n := len(payload)
	c.hdr[0] = 0x80 | opcode // FIN
	switch {
	case n < 126:
		c.hdr[1] = byte(n)
		if err := c.writeAll(c.hdr[:2]); err != nil {
			return err
		}
	case n <= 0xffff:
		c.hdr[1] = 126
		binary.BigEndian.PutUint16(c.hdr[2:4], uint16(n))
		if err := c.writeAll(c.hdr[:4]); err != nil {
			return err
		}
	default:
		c.hdr[1] = 127
		binary.BigEndian.PutUint64(c.hdr[2:10], uint64(n))
		if err := c.writeAll(c.hdr[:10]); err != nil {
			return err
		}
	}
	if n > 0 {
		if err := c.writeAll(payload); err != nil {
			return err
		}
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	return nil
}

func (c *wsConn) writeAll(b []byte) error {
	if c.writeTimeout > 0 {
		_ = c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	_, err := c.bw.Write(b)
	return err
}

// WriteBinary sends a binary message.
func (c *wsConn) WriteBinary(p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.writeFrame(wsOpBinary, p)
}

// WriteText sends a text message.
func (c *wsConn) WriteText(s string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.writeFrame(wsOpText, []byte(s))
}

// ReadMessage returns the next complete data message (text or binary).
func (c *wsConn) ReadMessage() (opcode byte, payload []byte, err error) {
	var msgOp byte
	for {
		fin, op, data, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case wsOpContinuation:
			if msgOp == 0 {
				return 0, nil, errors.New("websocket: unexpected continuation")
			}
			payload = append(payload, data...)
			if len(payload) > wsMaxPayload {
				return 0, nil, errors.New("websocket: message too large")
			}
			if fin {
				return msgOp, payload, nil
			}
		case wsOpText, wsOpBinary:
			if fin {
				return op, data, nil
			}
			msgOp = op
			payload = append(payload[:0], data...)
		case wsOpClose:
			c.wmu.Lock()
			_ = c.writeFrame(wsOpClose, data)
			c.wmu.Unlock()
			return 0, nil, io.EOF
		case wsOpPing:
			c.wmu.Lock()
			err := c.writeFrame(wsOpPong, data)
			c.wmu.Unlock()
			if err != nil {
				return 0, nil, err
			}
		case wsOpPong:
			// ignore
		default:
			return 0, nil, fmt.Errorf("websocket: unsupported opcode %#x", op)
		}
	}
}

// readFrame reads one frame, unmasking a client frame's payload.
// It must not be called concurrently with itself; writes may proceed in
// parallel because the control-frame path (pong) is issued from this goroutine.
func (c *wsConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	if h[0]&0x70 != 0 {
		return false, 0, nil, errors.New("websocket: reserved bits set")
	}
	opcode = h[0] & 0x0f
	masked := h[1]&0x80 != 0
	length := uint64(h[1] & 0x7f)
	switch length {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > wsMaxPayload {
		return false, 0, nil, errors.New("websocket: frame too large")
	}
	if !masked {
		if !c.client {
			// Clients MUST mask (RFC 6455 §5.1).
			return false, 0, nil, errors.New("websocket: unmasked client frame")
		}
		payload = make([]byte, length)
		if length > 0 {
			if _, err = io.ReadFull(c.br, payload); err != nil {
				return
			}
		}
		return fin, opcode, payload, nil
	}
	var mask [4]byte
	if _, err = io.ReadFull(c.br, mask[:]); err != nil {
		return
	}
	payload = make([]byte, length)
	if length > 0 {
		if _, err = io.ReadFull(c.br, payload); err != nil {
			return
		}
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return fin, opcode, payload, nil
}
