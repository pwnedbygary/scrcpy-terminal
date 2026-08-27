// Package engine is a native scrcpy-4.1-protocol client: launches the
// server directly via app_process, drives the wire protocol over an adb
// forward tunnel, and exposes Video packets + Control (touch/key/scroll/
// clipboard/UHID gamepad). No scrcpy binary, no ffmpeg in this path.
package engine

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

const serverPayload = "/usr/share/scrcpy/scrcpy-server"
const DeviceServerPath = "/data/local/tmp/scrcpy-server"

// Session owns one live scrcpy server session.
type Session struct {
	Serial string
	SCID   string
	Port   int

	Video *VideoStream
	Ctrl  *Control
	Audio *AudioStream

	cmd *exec.Cmd
}

// Options mirror the subset of server options we need.
type Options struct {
	Audio        bool
	MaxSize      int    // 0 = native
	CodecOptions string // e.g. "i-frame-interval=2" (comma-separated)
}

// Open creates a session: push server, forward, launch, connect video +
// control (role-verified; full-session redial on misrouting).
func Open(serial string, opts Options) (*Session, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		s, err := openOnce(serial, opts)
		if err == nil {
			return s, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("open session: %w", lastErr)
}

func openOnce(serial string, opts Options) (*Session, error) {
	// hygiene: stale servers on the device
	runAdb(serial, "shell", "pkill -f genymobile.scrcpy.Server")
	time.Sleep(300 * time.Millisecond)

	scid := make([]byte, 4)
	rand.Read(scid)
	scid[0] &= 0x7f // server parses scid with Integer.parseInt(,16): must fit int32
	sid := fmt.Sprintf("%08x", scid)
	port := 37000 + int(time.Now().UnixNano()%5000)

	if out, err := exec.Command("adb", "-s", serial, "push", serverPayload,
		DeviceServerPath).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("push: %v %s", err, out)
	}
	runAdb(serial, "forward", "--remove", fmt.Sprintf("tcp:%d", port))
	runAdb(serial, "forward", "--no-rebind", fmt.Sprintf("tcp:%d", port),
		fmt.Sprintf("localabstract:scrcpy_%s", sid))

	args := strings.Join([]string{
		"CLASSPATH=" + DeviceServerPath,
		"app_process", "/", "com.genymobile.scrcpy.Server",
		"4.1", "scid=" + sid, "log_level=error", "tunnel_forward=true"},
		" ")
	if !opts.Audio {
		args += " audio=false"
	}
	if opts.CodecOptions != "" {
		args += " video_codec_options=" + opts.CodecOptions
	}
	if opts.MaxSize > 0 {
		args += fmt.Sprintf(" max_size=%d", opts.MaxSize)
	}
	cmd := exec.Command("adb", "-s", serial, "shell", args)
	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// video socket: dial + dummy byte (the real readiness signal)
	video, err := dialDummy(addr)
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("video connect: %w", err)
	}

	// audio socket (2nd accept) then control (3rd accept) when audio on;
	// with audio off: control is the 2nd accept
	var aud net.Conn
	if opts.Audio {
		aud, err = dialRaw(addr, 8*time.Second)
		if err != nil {
			video.Close()
			cmd.Process.Kill()
			return nil, fmt.Errorf("audio connect: %w", err)
		}
	}
	ctrl, err := dialRaw(addr, 8*time.Second)
	if err != nil {
		video.Close()
		if aud != nil {
			aud.Close()
		}
		cmd.Process.Kill()
		return nil, fmt.Errorf("control connect: %w", err)
	}

	vs := &VideoStream{conn: video}
	cs := &Control{conn: ctrl}
	var as *AudioStream
	if aud != nil {
		as = &AudioStream{conn: aud}
	}

	// handshake
	if err := vs.readHandshake(); err != nil {
		video.Close()
		ctrl.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if as != nil {
		if err := as.readHeader(); err != nil {
			video.Close()
			ctrl.Close()
			aud.Close()
			cmd.Process.Kill()
			return nil, fmt.Errorf("audio header: %w", err)
		}
	}

	s := &Session{Serial: serial, SCID: sid, Port: port,
		Video: vs, Ctrl: cs, Audio: as, cmd: cmd}

	// role-verify control: EXPAND must open the shade
	if !s.Ctrl.verifyControl(serial) {
		s.Close()
		return nil, fmt.Errorf("control socket role-verification failed (adbd misroute)")
	}
	return s, nil
}

// Close tears the session down (server exits when sockets close).
func (s *Session) Close() {
	if s.Video != nil {
		s.Video.conn.Close()
	}
	if s.Audio != nil {
		s.Audio.conn.Close()
	}
	if s.Ctrl != nil {
		s.Ctrl.conn.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	runAdb(s.Serial, "forward", "--remove", fmt.Sprintf("tcp:%d", s.Port))
}

// ---- wire helpers ----

func dialDummy(addr string) (net.Conn, error) {
	dummy := make([]byte, 1)
	for i := 0; i < 60; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		c.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
		n, _ := c.Read(dummy)
		if n == 1 {
			c.SetReadDeadline(time.Time{})
			return c, nil
		}
		c.Close()
		c.SetReadDeadline(time.Time{})
		time.Sleep(300 * time.Millisecond)
	}
	return nil, fmt.Errorf("dummy byte never arrived")
}

func dialRaw(addr string, deadline time.Duration) (net.Conn, error) {
	dl := time.Now().Add(deadline)
	for time.Now().Before(dl) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			return c, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("connect timeout")
}

func readFullN(c net.Conn, b []byte) error {
	got := 0
	for got < len(b) {
		c.SetReadDeadline(time.Now().Add(12 * time.Second))
		n, err := c.Read(b[got:])
		got += n
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
	}
	c.SetReadDeadline(time.Time{})
	return nil
}

func runAdb(serial string, args ...string) string {
	a := append([]string{"-s", serial}, args...)
	out, _ := exec.Command("adb", a...).CombinedOutput()
	return string(out)
}

// ---- video ----

// VideoStream reads the H264 packet stream.
type VideoStream struct {
	conn net.Conn
	// Media identity from the handshake
	Codec    string
	Device   string
	handshakeDone bool
	Width, Height int
}

func (v *VideoStream) readHandshake() error {
	// NOTE: the dummy byte was already consumed by dialDummy on connect;
	// reading another would eat the first byte of the device name.
	name := make([]byte, 64)
	readFullN(v.conn, name)
	codec := make([]byte, 4)
	readFullN(v.conn, codec)
	v.Device = strings.TrimRight(string(name), "\x00")
	v.Codec = fmt.Sprintf("%c%c%c%c", codec[0], codec[1], codec[2], codec[3])
	v.handshakeDone = true
	return v.readSession()
}

const (
	PacketFlagConfig   = uint64(1) << 62
	PacketFlagKeyFrame = uint64(1) << 61
)

// Packet is one video packet from the wire.
type Packet struct {
	PTS      uint64
	Config   bool
	KeyFrame bool
	Data     []byte
}

// Session header (first 12 bytes of the stream, when header[0]&0x80).
func (v *VideoStream) readSession() error {
	hdr := make([]byte, 12)
	if err := readFullN(v.conn, hdr); err != nil {
		return err
	}
	if hdr[0]&0x80 == 0 {
		return fmt.Errorf("first stream message is not a session header (0x%02x)", hdr[0])
	}
	v.Width = int(binary.BigEndian.Uint32(hdr[4:]))
	v.Height = int(binary.BigEndian.Uint32(hdr[8:]))
	return nil
}

// Next reads the next video packet (blocks).
func (v *VideoStream) Next() (*Packet, error) {
	hdr := make([]byte, 12)
	if err := readFullN(v.conn, hdr); err != nil {
		return nil, err
	}
	if hdr[0]&0x80 != 0 {
		// a (re)session message appeared mid-stream (client resize)
		v.Width = int(binary.BigEndian.Uint32(hdr[4:]))
		v.Height = int(binary.BigEndian.Uint32(hdr[8:]))
		if err := readFullN(v.conn, hdr); err != nil {
			return nil, err
		}
	}
	ptsFlags := binary.BigEndian.Uint64(hdr)
	ln := binary.BigEndian.Uint32(hdr[8:])
	if ln == 0 || ln > 64<<20 {
		return nil, fmt.Errorf("bad packet len %d", ln)
	}
	data := make([]byte, ln)
	if err := readFullN(v.conn, data); err != nil {
		return nil, err
	}
	return &Packet{
		PTS:      ptsFlags & (PacketFlagKeyFrame - 1),
		Config:   ptsFlags&PacketFlagConfig != 0,
		KeyFrame: ptsFlags&PacketFlagKeyFrame != 0,
		Data:     data,
	}, nil
}

// readHandshakeKey is set after readSession is called.
var _ = readFullN

// ---- control ----

type W struct{ b bytes.Buffer }

func (w *W) u8(v uint8)       { w.b.WriteByte(v) }
func (w *W) u16(v uint16)     { var b [2]byte; binary.BigEndian.PutUint16(b[:], v); w.b.Write(b[:]) }
func (w *W) u32(v uint32)     { var b [4]byte; binary.BigEndian.PutUint32(b[:], v); w.b.Write(b[:]) }
func (w *W) u64(v uint64)     { var b [8]byte; binary.BigEndian.PutUint64(b[:], v); w.b.Write(b[:]) }
func (w *W) str(s string)     { w.b.WriteString(s) }
func (w *W) raw(p []byte)     { w.b.Write(p) }

const (
	TypeInjectKeycode  = 0
	TypeInjectText     = 1
	TypeInjectTouch    = 2
	TypeInjectScroll   = 3
	TypeBackOrScreenOn = 4
	TypeExpandPanel    = 5
	TypeCollapsePanels = 7
	TypeSetClipboard   = 9
	TypeUhidCreate     = 12
	TypeUhidInput      = 13
	TypeUhidDestroy    = 14
)

type Control struct{ conn net.Conn }

func (c *Control) send(b []byte) {
	c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	c.conn.Write(b)
	c.conn.SetWriteDeadline(time.Time{})
}

func (c *Control) Key(action, keycode int) {
	var m W
	m.u8(TypeInjectKeycode)
	m.u8(uint8(action))
	m.u32(uint32(keycode))
	m.u32(0)
	m.u32(0)
	c.send(m.b.Bytes())
}

func (c *Control) Home() { c.Key(0, 3); time.Sleep(40 * time.Millisecond); c.Key(1, 3) }
func (c *Control) Power() {
	c.Key(0, 26)
	time.Sleep(50 * time.Millisecond)
	c.Key(1, 26)
}
func (c *Control) ExpandPanels()  { c.send([]byte{TypeExpandPanel}) }
func (c *Control) CollapsePanels(){ c.send([]byte{TypeCollapsePanels}) }

func (c *Control) Touch(action int, x, y, w, h int) {
	var m W
	m.u8(TypeInjectTouch)
	m.u8(uint8(action))
	m.u64(0) // pointerId
	m.u32(uint32(x))
	m.u32(uint32(y))
	m.u16(uint16(w))
	m.u16(uint16(h))
	m.u16(0xffff) // pressure 1.0
	m.u32(0)     // actionButton
	m.u32(1)     // buttons
	c.send(m.b.Bytes())
}

func (c *Control) Tap(x, y, w, h int) {
	c.Touch(0, x, y, w, h)
	time.Sleep(60 * time.Millisecond)
	c.Touch(1, x, y, w, h)
}

func (c *Control) SetClipboard(text string) {
	var m W
	m.u8(TypeSetClipboard)
	var lb []byte
	if len(text) < 0x100 {
		lb = []byte{byte(len(text))}
	} else {
		lb = []byte{byte(len(text) >> 8), byte(len(text))}
	}
	m.u8(uint8(len(lb)))
	m.raw(lb)
	m.str(text)
	m.u8(0)
	c.send(m.b.Bytes())
}

// GamepadDesc is scrcpy's stock 81-byte HID gamepad report descriptor.
var GamepadDesc = []byte{
0x05, 0x01, 0x09, 0x05, 0xa1, 0x01, 0xa1, 0x00, 0x05, 0x01, 0x09, 0x30,
0x09, 0x31, 0x09, 0x33, 0x09, 0x34, 0x15, 0x00, 0x27, 0xff, 0xff, 0x00,
0x00, 0x75, 0x10, 0x95, 0x04, 0x81, 0x02, 0x05, 0x01, 0x09, 0x32, 0x09,
0x35, 0x15, 0x00, 0x26, 0xff, 0x7f, 0x75, 0x10, 0x95, 0x02, 0x81, 0x02,
0x05, 0x09, 0x19, 0x01, 0x29, 0x10, 0x15, 0x00, 0x25, 0x01, 0x95, 0x10,
0x75, 0x01, 0x81, 0x02, 0x05, 0x01, 0x09, 0x39, 0x15, 0x01, 0x25, 0x08,
0x75, 0x04, 0x95, 0x01, 0x81, 0x42, 0xc0, 0xc0,
}

func (c *Control) UhidCreate(id uint16, vendor, product uint16, name string, desc []byte) {
	var m W
	m.u8(TypeUhidCreate)
	m.u16(id)
	m.u16(vendor)
	m.u16(product)
	m.u8(uint8(len(name)))
	m.str(name)
	m.u16(uint16(len(desc)))
	m.raw(desc)
	c.send(m.b.Bytes())
}

func (c *Control) UhidInput(id uint16, report []byte) {
	var m W
	m.u8(TypeUhidInput)
	m.u16(id)
	m.u16(uint16(len(report)))
	m.raw(report)
	c.send(m.b.Bytes())
}

func (c *Control) UhidDestroy(id uint16) {
	var m W
	m.u8(TypeUhidDestroy)
	m.u16(id)
	c.send(m.b.Bytes())
}

// GamepadReport builds a 15-byte HID gamepad report (little-endian).
func GamepadReport(lx, ly, rx, ry, buttons uint16, hat uint8) []byte {
	r := make([]byte, 15)
	binary.LittleEndian.PutUint16(r[0:], lx)
	binary.LittleEndian.PutUint16(r[2:], ly)
	binary.LittleEndian.PutUint16(r[4:], rx)
	binary.LittleEndian.PutUint16(r[6:], ry)
	binary.LittleEndian.PutUint16(r[8:], 0)
	binary.LittleEndian.PutUint16(r[10:], 0)
	binary.LittleEndian.PutUint16(r[12:], buttons)
	r[14] = hat
	return r
}

// verifyControl proves this socket is the real control channel: EXPAND
// must steal focus to the notification shade.
func (c *Control) verifyControl(serial string) bool {
	c.CollapsePanels()
	time.Sleep(300 * time.Millisecond)
	c.ExpandPanels()
	time.Sleep(800 * time.Millisecond)
	out := runAdb(serial, "shell",
		"dumpsys window | grep mCurrentFocus | head -1")
	ok := strings.Contains(strings.ToLower(out), "notification")
	c.CollapsePanels()
	time.Sleep(400 * time.Millisecond)
	return ok
}

var _ = io.Copy

// ---- audio ----

// AudioStream reads the raw Opus packet stream (same framing as video,
// codec id first: 'opus' 0x6f707573).
type AudioStream struct {
	conn net.Conn
	Codec string
}

func (a *AudioStream) readHeader() error {
	codec := make([]byte, 4)
	if err := readFullN(a.conn, codec); err != nil {
		return err
	}
	a.Codec = fmt.Sprintf("%c%c%c%c", codec[0], codec[1], codec[2], codec[3])
	if a.Codec != "opus" {
		return fmt.Errorf("unexpected audio codec %q", a.Codec)
	}
	return nil
}

// Next reads the next audio packet (blocking).
func (a *AudioStream) Next() ([]byte, error) {
	hdr := make([]byte, 12)
	if err := readFullN(a.conn, hdr); err != nil {
		return nil, err
	}
	ln := binary.BigEndian.Uint32(hdr[8:])
	if ln == 0 || ln > 1<<20 {
		return nil, fmt.Errorf("bad audio len %d", ln)
	}
	data := make([]byte, ln)
	if err := readFullN(a.conn, data); err != nil {
		return nil, err
	}
	return data, nil
}

// Scroll injects a scroll wheel event (TYPE_INJECT_SCROLL_EVENT).
func (c *Control) Scroll(x, y, w, h int, hScroll, vScroll float64) {
	var m W
	m.u8(TypeInjectScroll)
	m.u32(uint32(x))
	m.u32(uint32(y))
	m.u16(uint16(w))
	m.u16(uint16(h))
	// fixed-point i16 *16: value range [-16,16] -> [-1,1]
	hs := clampF(hScroll, -1, 1) * 16
	vs := clampF(vScroll, -1, 1) * 16
	m.u16(uint16(int16(hs * 32768)))
	m.u16(uint16(int16(vs * 32768)))
	m.u32(0) // buttons
	c.send(m.b.Bytes())
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// GamepadState is a normalized gamepad snapshot (-1..1 axes, bool buttons).
type GamepadState struct {
	LX, LY, RX, RY float64
	L2, R2         float64
	A, B, X, Y     bool
	LB, RB         bool
	Back, Start    bool
	Guide          bool
	LStick, RStick bool
	Hat            int8 // 0 center, 1 up, 2 up-right, ..., 8 up-left
}

// Report converts a gamepad state to the 15-byte HID input report.
func (g *GamepadState) Report() []byte {
	rescale := func(v float64) uint16 {
		// -1 -> 0, +1 -> 65535 (mirrors scrcpy's AXIS_RESCALE)
		return uint16(clampF(v, -1, 1)*32767.5 + 32768)
	}
	var buttons uint16
	if g.A {
		buttons |= 0x0001
	}
	if g.B {
		buttons |= 0x0002
	}
	if g.X {
		buttons |= 0x0008
	}
	if g.Y {
		buttons |= 0x0010
	}
	if g.LB {
		buttons |= 0x0040
	}
	if g.RB {
		buttons |= 0x0080
	}
	if g.Back {
		buttons |= 0x0400
	}
	if g.Start {
		buttons |= 0x0800
	}
	if g.Guide {
		buttons |= 0x1000
	}
	if g.LStick {
		buttons |= 0x2000
	}
	if g.RStick {
		buttons |= 0x4000
	}
	return GamepadReport(rescale(g.LX), rescale(g.LY), rescale(g.RX), rescale(g.RY),
		buttons, uint8(g.Hat))
}
