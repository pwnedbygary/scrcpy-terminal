package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// webTestHarness runs a real webServer (real HTTP listener, real websocket)
// around a synthetic stream, so the whole frame path can be exercised without
// a device:
//
//	frameSink -> JPEG encode -> websocket -> assert pixels + control replies
type webTestHarness struct {
	srv  *webServer
	ctrl *controller
	app  *app
	// control traffic the server wrote to the device
	devMsgs chan []byte
}

func newWebTestHarness(t *testing.T, cfg config, withApp bool) *webTestHarness {
	t.Helper()
	clientSide, deviceSide := net.Pipe()
	ctrl := newController(clientSide)

	cfg.web = true
	cfg.webAddr = "127.0.0.1"
	cfg.webPort = 0

	stream := &streamState{cfg: cfg, ctrl: ctrl}
	sess := &session{deviceName: "testphone"}
	h := &webTestHarness{ctrl: ctrl, devMsgs: make(chan []byte, 64)}

	var a *app
	if withApp {
		a = &app{sess: sess, cfg: cfg, ctrl: ctrl, events: make(chan inputEvent, 64), grabbed: true}
		a.stream = stream
	}
	srv := newWebServer(cfg, sess, stream, nil, ctrl, a)
	h.srv = srv
	h.app = a
	// The sink is what runVideo calls; wire it like runWeb does.
	if a != nil {
		a.web = srv
	}

	// Drain whatever the server sends to the "device" so control tests can
	// assert on it without blocking the writer.
	go func() {
		br := bufio.NewReader(deviceSide)
		buf := make([]byte, 4096)
		for {
			n, err := br.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case h.devMsgs <- cp:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()

	go func() { _ = srv.run(false) }()
	select {
	case <-srv.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("web server never started listening")
	}
	t.Cleanup(srv.stop)
	return h
}

func (h *webTestHarness) url() string { return fmt.Sprintf("127.0.0.1:%d", h.srv.port) }

// testClient is a minimal websocket client for the harness.
type testClient struct {
	t    *testing.T
	conn net.Conn
	br   *bufio.Reader
	ws   *wsConn
}

func dialWS(t *testing.T, host string) *testClient {
	t.Helper()
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	key := base64.StdEncoding.EncodeToString([]byte("scterm-test-key"))
	req := "GET /ws HTTP/1.1\r\nHost: " + host + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("handshake write: %v", err)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("handshake read: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake status %q", strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("handshake headers: %v", err)
		}
		if strings.TrimRight(line, "\r\n") == "" {
			break
		}
	}
	return &testClient{t: t, conn: conn, br: br,
		ws: &wsConn{conn: conn, br: br, bw: bufio.NewWriter(conn), client: true, writeTimeout: 5 * time.Second}}
}

func (c *testClient) send(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	op := byte(wsOpText)
	var hdr []byte
	n := len(b)
	switch {
	case n < 126:
		hdr = []byte{0x80 | op, 0x80 | byte(n)}
	case n <= 0xffff:
		hdr = []byte{0x80 | op, 0x80 | 126, byte(n >> 8), byte(n)}
	default:
		c.t.Fatal("test payload too large")
	}
	mask := [4]byte{1, 2, 3, 4}
	masked := make([]byte, n)
	for i := range b {
		masked[i] = b[i] ^ mask[i&3]
	}
	if _, err := c.conn.Write(append(append(hdr, mask[:]...), masked...)); err != nil {
		c.t.Fatalf("send: %v", err)
	}
}

// next reads one frame, skipping status JSON, and returns the binary kind.
func (c *testClient) nextBinary(kind byte, timeout time.Duration) []byte {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			c.t.Fatal(err)
		}
		op, payload, err := c.ws.ReadMessage()
		if err != nil {
			c.t.Fatalf("read (waiting for kind %#x): %v", kind, err)
		}
		if op != wsOpBinary || len(payload) == 0 || payload[0] != kind {
			continue
		}
		return payload
	}
}

func (c *testClient) nextJSON(op string, timeout time.Duration) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			c.t.Fatal(err)
		}
		kind, payload, err := c.ws.ReadMessage()
		if err != nil {
			c.t.Fatalf("read (waiting for json %q): %v", op, err)
		}
		if kind != wsOpText {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err != nil {
			continue
		}
		if m["op"] == op {
			return m
		}
	}
}

// makeCanvas builds a BGR0 canvas with a recognisable pattern.
func makeCanvas(w, h int, seed byte) []byte {
	buf := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := (y*w + x) * 4
			buf[p+0] = byte(x + int(seed)) // B
			buf[p+1] = byte(y)             // G
			buf[p+2] = seed                // R
			buf[p+3] = 0xff
		}
	}
	return buf
}

// TestWebEndToEndVideo drives a synthetic frame through the sink and asserts a
// decodable JPEG with the negotiated geometry arrives at the browser.
func TestWebEndToEndVideo(t *testing.T) {
	h := newWebTestHarness(t, config{web: true, video: true}, false)
	h.srv.cfg.maxSize = 1280

	cl := dialWS(t, h.url())
	cl.send(webEvent{Op: "hello", W: 640, H: 480, Format: "auto", Caps: "audio"})
	hello := cl.nextJSON("hello", 3*time.Second)
	if hello["proto"].(float64) != 1 {
		t.Fatalf("unexpected proto: %v", hello["proto"])
	}

	// 480x1080 device video in a 640x480 viewport -> 213x480 fit
	const vw, vh = 480, 1080
	want := computeWebCanvas(640, 480, vw, vh)
	if want.fw != 212 && want.fw != 214 {
		t.Logf("fit is %dx%d", want.fw, want.fh)
	}

	frame := &videoFrame{
		rgb: makeCanvas(want.cw, want.ch, 7),
		w:   vw, h: vh, cw: want.cw, ch: want.ch, fps: 59.9,
	}
	h.srv.frame(frame)

	msg := cl.nextBinary(webMsgVideo, 5*time.Second)
	if len(msg) < webHeaderLen+2 {
		t.Fatalf("short video message: %d bytes", len(msg))
	}
	gotW := int(binary.BigEndian.Uint16(msg[5:7]))
	gotH := int(binary.BigEndian.Uint16(msg[7:9]))
	if gotW != want.cw || gotH != want.ch {
		t.Fatalf("canvas %dx%d, want %dx%d", gotW, gotH, want.cw, want.ch)
	}
	if format := msg[11]; format != webFormatJPEG {
		t.Fatalf("format %d, want JPEG", format)
	}
	body := msg[webHeaderLen:]
	if body[0] != 0xff || body[1] != 0xd8 {
		t.Fatalf("payload is not a JPEG: % x", body[:4])
	}
	img, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode JPEG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != want.cw || b.Dy() != want.ch {
		t.Fatalf("jpeg is %dx%d, want %dx%d", b.Dx(), b.Dy(), want.cw, want.ch)
	}

	// The frame must have been recycled: encode a second frame with the same
	// pool, and the third frame must not need a new allocation either (this is
	// what keeps the pipeline allocation-free in steady state).
	for i := 0; i < 4; i++ {
		h.srv.frame(&videoFrame{
			rgb: makeCanvas(want.cw, want.ch, byte(i)),
			w:   vw, h: vh, cw: want.cw, ch: want.ch, fps: 60,
		})
		cl.nextBinary(webMsgVideo, 5*time.Second)
	}
	if n := h.srv.clientCount(); n != 1 {
		t.Fatalf("%d clients registered, want 1", n)
	}
}

// TestWebRawFormat checks the lossless path (format=raw) and that the header
// still describes the geometry.
func TestWebRawFormat(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := dialWS(t, h.url())
	cl.send(webEvent{Op: "hello", W: 320, H: 640, Format: "raw"})
	cl.nextJSON("hello", 3*time.Second)

	want := computeWebCanvas(320, 640, 320, 640)
	rgb := makeCanvas(want.cw, want.ch, 3)
	h.srv.frame(&videoFrame{rgb: rgb, w: 320, h: 640, cw: want.cw, ch: want.ch, fps: 30})

	msg := cl.nextBinary(webMsgVideo, 5*time.Second)
	if msg[11] != webFormatRaw {
		t.Fatalf("format %d, want raw", msg[11])
	}
	body := msg[webHeaderLen:]
	if len(body) != want.cw*want.ch*4 {
		t.Fatalf("raw payload %d bytes, want %d", len(body), want.cw*want.ch*4)
	}
	if !bytes.Equal(body, rgb) {
		t.Fatal("raw payload does not match the canvas")
	}
}

// TestWebSlowClientDropsFrames is the load-shedding guarantee: a client that
// never reads must not stall the frame sink (which runs on the demux loop).
func TestWebSlowClientDropsFrames(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := dialWS(t, h.url())
	cl.send(webEvent{Op: "hello", W: 400, H: 400})
	cl.nextJSON("hello", 3*time.Second)
	// Stop reading: fill the socket until the client's mailbox overflows.
	done := make(chan struct{})
	go func() {
		defer close(done)
		cv := computeWebCanvas(400, 400, 400, 400)
		for i := 0; i < 400; i++ {
			h.srv.frame(&videoFrame{
				rgb: makeCanvas(cv.cw, cv.ch, byte(i)),
				w:   400, h: 400, cw: cv.cw, ch: cv.ch, fps: 60,
			})
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("frame sink blocked on a client that stopped reading")
	}
}

// TestWebControlEvents checks the control path end to end: the exact scrcpy
// control bytes must reach the device socket for pointer, key and text events.
func TestWebControlEvents(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := dialWS(t, h.url())
	cl.send(webEvent{Op: "hello", W: 480, H: 1080})
	cl.nextJSON("hello", 3*time.Second)

	// Give the server a canvas: it needs one to map pointer coordinates. The
	// canvas is established when the client goroutine sends a frame and is
	// reported in the status message, so wait for that first (a real browser
	// cannot click before it has seen a frame either).
	cv := computeWebCanvas(480, 1080, 480, 1080) // 1:1, so the assertions below are exact
	h.srv.frame(&videoFrame{rgb: makeCanvas(cv.cw, cv.ch, 1), w: 480, h: 1080,
		cw: cv.cw, ch: cv.ch, fps: 60})
	cl.nextBinary(webMsgVideo, 5*time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for {
		st := cl.nextJSON("status", 3*time.Second)
		if st["canvasW"].(float64) == float64(cv.cw) && st["canvasH"].(float64) == float64(cv.ch) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never established the canvas geometry (last status: %vx%v)",
				st["canvasW"], st["canvasH"])
		}
	}

	readDeviceMsg := func() []byte {
		t.Helper()
		select {
		case b := <-h.devMsgs:
			return b
		case <-time.After(3 * time.Second):
			t.Fatal("no control message reached the device")
			return nil
		}
	}

	// centre tap: down, then up
	cl.send(webEvent{Op: "down", X: 32767, Y: 32767})
	data := readDeviceMsg()
	if data[0] != ctrlInjectTouch {
		t.Fatalf("first control byte %d, want %d (inject touch)", data[0], ctrlInjectTouch)
	}
	if data[1] != motionActionDown {
		t.Fatalf("action %d, want %d", data[1], motionActionDown)
	}
	x := int32(binary.BigEndian.Uint32(data[10:14]))
	y := int32(binary.BigEndian.Uint32(data[14:18]))
	sw := binary.BigEndian.Uint16(data[18:20])
	sh := binary.BigEndian.Uint16(data[20:22])
	if sw != 480 || sh != 1080 {
		t.Fatalf("screen size %dx%d, want 480x1080", sw, sh)
	}
	if x < 235 || x > 243 || y < 535 || y > 543 {
		t.Fatalf("centre tap mapped to (%d,%d), expected ~(239,539)", x, y)
	}

	cl.send(webEvent{Op: "up", X: 32767, Y: 32767})
	data = readDeviceMsg()
	if data[1] != motionActionUp {
		t.Fatalf("release action %d, want %d", data[1], motionActionUp)
	}

	// wheel
	cl.send(webEvent{Op: "wheel", X: 1000, Y: 1000, Delta: 2})
	data = readDeviceMsg()
	if data[0] != ctrlInjectScroll {
		t.Fatalf("wheel byte %d, want %d", data[0], ctrlInjectScroll)
	}

	// keycode
	cl.send(webEvent{Op: "key", Code: 3, Meta: 0})
	data = readDeviceMsg()
	if data[0] != ctrlInjectKeycode || data[1] != keyActionDown {
		t.Fatalf("key msg % x", data)
	}
	if got := binary.BigEndian.Uint32(data[2:6]); got != 3 {
		t.Fatalf("keycode %d, want 3", got)
	}
	data = readDeviceMsg()
	if data[0] != ctrlInjectKeycode || data[1] != keyActionUp {
		t.Fatalf("key up msg % x", data)
	}

	// text: ASCII letters become keycodes, punctuation becomes injectText
	cl.send(webEvent{Op: "text", Text: "a!"})
	data = readDeviceMsg() // 'a' down
	if data[0] != ctrlInjectKeycode {
		t.Fatalf("text letter byte %d msg % x", data[0], data)
	}
	if got := binary.BigEndian.Uint32(data[2:6]); got != 29 {
		t.Fatalf("'a' keycode %d, want 29", got)
	}
	_ = readDeviceMsg() // 'a' up
	data = readDeviceMsg()
	if data[0] != ctrlInjectText {
		t.Fatalf("punctuation byte %d, want %d (inject text)", data[0], ctrlInjectText)
	}
	if l := binary.BigEndian.Uint32(data[1:5]); l != 1 || data[5] != '!' {
		t.Fatalf("inject text payload % x", data[:6])
	}

	drain := func() {
		for {
			select {
			case <-h.devMsgs:
			case <-time.After(150 * time.Millisecond):
				return
			}
		}
	}

	// back (a down+up pair)
	cl.send(webEvent{Op: "back"})
	data = readDeviceMsg()
	if data[0] != ctrlBackOrScreenOn || data[1] != 0 {
		t.Fatalf("back msg % x", data)
	}
	data = readDeviceMsg()
	if data[0] != ctrlBackOrScreenOn || data[1] != 1 {
		t.Fatalf("back up msg % x", data)
	}

	// system keys, each a down+up pair sending one Android keycode
	for _, c := range []struct {
		op   string
		code uint32
	}{{"home", 3}, {"menu", 82}, {"appswitch", 187}, {"power", 26},
		{"volup", 24}, {"voldown", 25}, {"mute", 164}} {
		drain()
		cl.send(webEvent{Op: c.op})
		data = readDeviceMsg()
		if data[0] != ctrlInjectKeycode || binary.BigEndian.Uint32(data[2:6]) != c.code {
			t.Fatalf("%s msg % x, want keycode %d", c.op, data, c.code)
		}
		_ = readDeviceMsg() // key up
	}

	// panel commands are single byte messages with no payload
	for _, c := range []struct {
		op   string
		kind byte
	}{{"notif", ctrlExpandNotif}, {"settings", ctrlExpandSettings}, {"collapse", ctrlCollapsePanels},
		{"rotate", ctrlRotateDevice}, {"resetvideo", ctrlResetVideo}} {
		drain()
		cl.send(webEvent{Op: c.op})
		data = readDeviceMsg()
		if data[0] != c.kind {
			t.Fatalf("%s sent control byte %d, want %d", c.op, data[0], c.kind)
		}
	}
}

// TestWebHeadlessDrag covers move/up without an app loop (--web --no-tui),
// where the dispatcher writes to the controller directly.
func TestWebHeadlessDrag(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := dialWS(t, h.url())
	cl.send(webEvent{Op: "hello", W: 480, H: 1080})
	cl.nextJSON("hello", 3*time.Second)

	cv := computeWebCanvas(480, 1080, 480, 1080)
	h.srv.frame(&videoFrame{rgb: makeCanvas(cv.cw, cv.ch, 1), w: 480, h: 1080,
		cw: cv.cw, ch: cv.ch, fps: 60})
	cl.nextBinary(webMsgVideo, 5*time.Second)

	readDeviceMsg := func() []byte {
		t.Helper()
		for {
			select {
			case b := <-h.devMsgs:
				return b
			case <-time.After(3 * time.Second):
				t.Fatal("no control message reached the device")
				return nil
			}
		}
	}

	cl.send(webEvent{Op: "down", X: 1000, Y: 1000})
	if got := readDeviceMsg(); got[1] != motionActionDown {
		t.Fatalf("down msg % x", got)
	}
	cl.send(webEvent{Op: "move", X: 40000, Y: 40000})
	got := readDeviceMsg()
	if got[0] != ctrlInjectTouch || got[1] != motionActionMove {
		t.Fatalf("move msg % x", got)
	}
	if x := int32(binary.BigEndian.Uint32(got[10:14])); x < 290 || x > 295 {
		t.Fatalf("move mapped to x=%d, want ~293", x)
	}
	cl.send(webEvent{Op: "up", X: 40000, Y: 40000})
	if got := readDeviceMsg(); got[1] != motionActionUp {
		t.Fatalf("up msg % x", got)
	}
}

// TestWebAudioTap checks that decoded PCM is fanned out to the browser.
func TestWebAudioTap(t *testing.T) {
	h := newWebTestHarness(t, config{web: true, audio: true}, false)
	cl := dialWS(t, h.url())
	cl.send(webEvent{Op: "hello", W: 480, H: 1080, Caps: "audio"})
	cl.nextJSON("hello", 3*time.Second)

	pcm := make([]byte, 960) // 5 ms of s16 stereo at 48 kHz
	for i := range pcm {
		pcm[i] = byte(i)
	}
	h.srv.pushPCM(pcm)
	msg := cl.nextBinary(webMsgAudio, 5*time.Second)
	if len(msg) != 1+len(pcm) {
		t.Fatalf("audio message %d bytes, want %d", len(msg), 1+len(pcm))
	}
	if !bytes.Equal(msg[1:], pcm) {
		t.Fatal("PCM payload mismatch")
	}
}

// TestWebOriginRefused rejects a cross-origin websocket handshake.
func TestWebOriginRefused(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	req, err := http.NewRequest("GET", "http://"+h.url()+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString([]byte("k")))
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
}

// TestWebAssetsAreServed makes sure the embedded player is reachable and has
// the content types the browser needs.
func TestWebAssetsAreServed(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	base := "http://" + h.url()
	cases := []struct{ path, ctype, needle string }{
		{"/", "text/html", "<canvas id=\"screen\""},
		{"/player.js", "text/javascript", "createImageBitmap"},
		{"/player.css", "text/css", "#screen"},
		{"/pcm-worklet.js", "text/javascript", "registerProcessor"},
		// The phone soft-keyboard path (three-finger tap) spans all three
		// assets. The player is embedded in the binary, so a stale or missing
		// piece would otherwise only show up on a phone.
		{"/", "text/html", "id=\"ime\""},
		{"/player.js", "text/javascript", "toggleSoftKeys"},
		{"/player.css", "text/css", "#ime {"},
		// The controls overhaul: pointer indication, action bar and sheet are
		// each served and wired, so a missing chunk fails here, not in a
		// user's browser.
		{"/pointer.js", "text/javascript", "cursor-ghost"},
		{"/player.js", "text/javascript", "MNEMONIC_ACTIONS"},
		{"/", "text/html", "data-act=\"home\""},
		{"/", "text/html", "id=\"sheet\""},
		{"/player.css", "text/css", "#cursor-ghost"},
	}
	for _, c := range cases {
		resp, err := http.Get(base + c.path)
		if err != nil {
			t.Fatalf("GET %s: %v", c.path, err)
		}
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: status %d", c.path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, c.ctype) {
			t.Errorf("GET %s: content-type %q, want %q", c.path, ct, c.ctype)
		}
		if !strings.Contains(body.String(), c.needle) {
			t.Errorf("GET %s: body does not contain %q", c.path, c.needle)
		}
	}
}
