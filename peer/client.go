package peer

import (
	"bufio"
	"crypto/hmac"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// readTimeout: three missed pongs mean the target is gone.
	readTimeout = 15 * time.Second
	dialTimeout = 10 * time.Second
	// maxDeviceMessage bounds the device clipboard text a target may send.
	maxDeviceMessage = 1 << 20
)

// pingInterval is kept regardless of input: the target only answers, so
// pings starved by continuous input would let readTimeout end a healthy
// session. A variable only so tests can shorten it.
var pingInterval = 5 * time.Second

// Client pairs with and connects to targets as one identity.
type Client struct {
	Identity *Identity
	Info     ClientInfo
}

// PairResult is what a successful pairing established.
type PairResult struct {
	Fingerprint Fingerprint
	Device      DeviceInfo
	Grants      []string
}

// dial opens mutual TLS, pinning the target when pin is set, and reports
// the identity the target presented.
func (c *Client) dial(host string, port int, pin *Fingerprint) (*tls.Conn, Fingerprint, error) {
	var seen Fingerprint
	cfg := &tls.Config{
		Certificates: []tls.Certificate{c.Identity.Certificate},
		// Trust is the pinned public key, never a CA chain or a host name.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the target presented no certificate")
			}
			seen = FingerprintOf(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
			if pin != nil && !seen.Equal(*pin) {
				return fmt.Errorf("the device at %s is %s, not the paired %s", host, seen.Short(), pin.Short())
			}
			return nil
		},
	}
	raw, err := (&net.Dialer{Timeout: dialTimeout}).Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, seen, err
	}
	conn := tls.Client(raw, cfg)
	conn.SetDeadline(time.Now().Add(dialTimeout))
	if err := conn.Handshake(); err != nil {
		raw.Close()
		return nil, seen, err
	}
	return conn, seen, nil
}

// Pair redeems an invitation. Both sides prove the secret bound to this TLS
// connection's two certificates; nothing is trusted before both proofs check.
func (c *Client) Pair(inv Invitation, servePort int) (PairResult, error) {
	conn, target, err := c.dial(inv.Host, inv.Port, inv.Fingerprint)
	if err != nil {
		return PairResult{}, err
	}
	defer conn.Close()
	proof := PairingProof(inv.Secret, RoleController, c.Identity.Fingerprint, target)
	err = WriteFrame(conn, &Message{Type: TypePair, V: ProtocolVersion, Proof: hex.EncodeToString(proof), Client: &c.Info, ServePort: servePort})
	if err != nil {
		return PairResult{}, err
	}
	reply, err := ReadFrame(conn)
	if err != nil {
		return PairResult{}, err
	}
	switch reply.Type {
	case TypePaired:
		got, err := hex.DecodeString(strings.ToLower(reply.Proof))
		if err != nil || !hmac.Equal(got, PairingProof(inv.Secret, RoleTarget, target, c.Identity.Fingerprint)) {
			return PairResult{}, &RejectError{Code: "bad_proof", Message: "the device could not prove it issued this code"}
		}
		r := PairResult{Fingerprint: target, Grants: reply.Grants}
		if reply.Device != nil {
			r.Device = *reply.Device
		}
		return r, nil
	case TypeReject:
		return PairResult{}, &RejectError{Code: reply.Code, Message: reply.Message}
	default:
		return PairResult{}, fmt.Errorf("unexpected pairing reply %q", reply.Type)
	}
}

// Connect opens a session to a paired target, which must present pin. Media
// channels are opened only when requested, granted and available.
func (c *Client) Connect(host string, port int, pin Fingerprint, req StreamRequest) (*Session, error) {
	control, _, err := c.dial(host, port, &pin)
	if err != nil {
		return nil, err
	}
	var video, audio net.Conn
	fail := func(err error) (*Session, error) {
		for _, conn := range []net.Conn{video, audio, control} {
			if conn != nil {
				conn.Close()
			}
		}
		return nil, err
	}
	err = WriteFrame(control, &Message{Type: TypeHello, V: ProtocolVersion, MinV: ProtocolVersion, Channel: ChannelControl, Request: &req, Client: &c.Info})
	if err != nil {
		return fail(err)
	}
	in := bufio.NewReaderSize(control, 16<<10)
	welcome, err := expectWelcome(in, ChannelControl)
	if err != nil {
		return fail(err)
	}
	if req.Video && hasGrant(welcome.Grants, GrantView) && (welcome.Streams == nil || welcome.Streams.Video) {
		if video, err = c.openMedia(host, port, pin, ChannelVideo, welcome); err != nil {
			return fail(err)
		}
	}
	if req.Audio && hasGrant(welcome.Grants, GrantAudio) && (welcome.Streams == nil || welcome.Streams.Audio) {
		if audio, err = c.openMedia(host, port, pin, ChannelAudio, welcome); err != nil {
			return fail(err)
		}
	}
	control.SetDeadline(time.Time{})
	return newSession(control, in, *welcome, video, audio), nil
}

func (c *Client) openMedia(host string, port int, pin Fingerprint, channel string, welcome *Message) (net.Conn, error) {
	conn, _, err := c.dial(host, port, &pin)
	if err != nil {
		return nil, err
	}
	err = WriteFrame(conn, &Message{Type: TypeHello, V: ProtocolVersion, MinV: ProtocolVersion, Channel: channel, Session: welcome.Session, Token: welcome.Token})
	if err == nil {
		// Read unbuffered: every byte after the welcome belongs to the media stream.
		_, err = expectWelcome(conn, channel)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

func expectWelcome(r io.Reader, channel string) (*Message, error) {
	m, err := ReadFrame(r)
	switch {
	case err != nil:
		return nil, err
	case m.Type == TypeWelcome && m.Channel == channel:
		return m, nil
	case m.Type == TypeReject:
		return nil, &RejectError{Code: m.Code, Message: m.Message}
	default:
		return nil, fmt.Errorf("expected a %s welcome, got %q", channel, m.Type)
	}
}

func hasGrant(grants []string, g string) bool {
	for _, x := range grants {
		if x == g {
			return true
		}
	}
	return false
}

// Events are optional callbacks, run on the session's reader goroutine.
type Events struct {
	Lease  func(state, holder string)
	Error  func(code, message, controlType string)
	Status func(m *Message)
	Closed func(reason string)
}

// Session is a live connection to a target. Video and Audio (nil unless
// granted and available) carry scrcpy v4.1 media streams from the codec
// header on; there is no device-name prefix (Welcome.Device replaces it).
type Session struct {
	Welcome Message
	Video   net.Conn
	Audio   net.Conn

	control *tls.Conn
	in      *bufio.Reader
	events  Events
	writeMu sync.Mutex
	device  chan []byte
	done    chan struct{}
	once    sync.Once
	closing atomic.Bool
	reason  atomic.Value
	rttMs   atomic.Int64
	lease   atomic.Value
}

func newSession(control *tls.Conn, in *bufio.Reader, welcome Message, video, audio net.Conn) *Session {
	s := &Session{Welcome: welcome, Video: video, Audio: audio, control: control, in: in, device: make(chan []byte, 64), done: make(chan struct{})}
	s.rttMs.Store(-1)
	s.lease.Store(welcome.Lease)
	return s
}

// Start begins reading the control channel and pinging. Call it once,
// before using Control, so no early lease or status event is missed.
func (s *Session) Start(ev Events) {
	s.events = ev
	go s.readLoop()
	go s.pingLoop()
}

// Control is the control channel as plain scrcpy: Write takes whole control
// messages (one per call, as scterm's controller writes them), Read yields
// device messages. Peer envelopes never appear on it.
func (s *Session) Control() net.Conn { return &controlConn{s: s} }

// Takeover asks for the input lease (needs the control grant).
func (s *Session) Takeover() error { return s.writeEnvelope(&Message{Type: TypeTakeover}) }

// Lease is LeaseHeld when this controller's input goes through.
func (s *Session) Lease() string { v, _ := s.lease.Load().(string); return v }

// RoundTrip is the last measured control round trip, or -1 before the first pong.
func (s *Session) RoundTrip() time.Duration {
	if ms := s.rttMs.Load(); ms >= 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return -1
}

// Done is closed when the session ends; Reason then says why.
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) Reason() string { v, _ := s.reason.Load().(string); return v }

// Close leaves after anything already written: bye, then close. endSession
// asks the target to disconnect every viewer (the legacy quit); it needs the lease.
func (s *Session) Close(endSession bool) {
	select {
	case <-s.done:
		return
	default:
	}
	s.closing.Store(true)
	if s.writeEnvelope(&Message{Type: TypeBye, EndSession: endSession}) == nil {
		s.control.CloseWrite()
		select {
		case <-s.done: // the target read the bye and closed
		case <-time.After(time.Second):
		}
	}
	s.shutdown("closed")
}

func (s *Session) readLoop() {
	for {
		s.control.SetReadDeadline(time.Now().Add(readTimeout))
		typ, err := s.in.ReadByte()
		if err != nil {
			s.shutdown(readFailure(err))
			return
		}
		if typ == envelopeType {
			if !s.handleEnvelope() {
				return
			}
			continue
		}
		msg, err := readDeviceMessage(s.in, typ)
		if err != nil {
			s.shutdown(readFailure(err))
			return
		}
		select {
		case s.device <- msg:
		default:
			// Nobody is reading device messages: keep the newest.
			select {
			case <-s.device:
			default:
			}
			select {
			case s.device <- msg:
			default:
			}
		}
	}
}

func (s *Session) handleEnvelope() bool {
	var hdr [4]byte
	if _, err := io.ReadFull(s.in, hdr[:]); err != nil {
		s.shutdown(readFailure(err))
		return false
	}
	m, err := readFrameBody(s.in, binary.BigEndian.Uint32(hdr[:]))
	if err != nil {
		s.shutdown("protocol error: " + err.Error())
		return false
	}
	switch m.Type {
	case TypePong:
		s.rttMs.Store(time.Now().UnixMilli() - m.T)
	case TypeLease:
		s.lease.Store(m.State)
		if s.events.Lease != nil {
			s.events.Lease(m.State, m.Holder)
		}
	case TypeError:
		if s.events.Error != nil {
			s.events.Error(m.Code, m.Message, m.ControlType)
		}
	case TypeStatus:
		if s.events.Status != nil {
			s.events.Status(m)
		}
	case TypeBye:
		reason := m.Reason
		if reason == "" {
			reason = "closed by the device"
		}
		s.shutdown(reason)
		return false
	}
	return true
}

func readFailure(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return "the device stopped responding"
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return "disconnected"
	default:
		return "connection lost: " + err.Error()
	}
}

// readDeviceMessage returns one whole scrcpy v4.1 device message, type byte
// first, so it parses exactly as it would from the adb tunnel.
func readDeviceMessage(r *bufio.Reader, typ byte) ([]byte, error) {
	var body int
	switch typ {
	case 0: // clipboard: u32 length, UTF-8
		hdr, err := r.Peek(4)
		if err != nil {
			return nil, err
		}
		n := binary.BigEndian.Uint32(hdr)
		if n > maxDeviceMessage {
			return nil, fmt.Errorf("device clipboard of %d bytes", n)
		}
		body = 4 + int(n)
	case 1: // ack_clipboard: u64 sequence
		body = 8
	case 2: // uhid_output: u16 id, u16 size, data
		hdr, err := r.Peek(4)
		if err != nil {
			return nil, err
		}
		body = 4 + int(binary.BigEndian.Uint16(hdr[2:]))
	default:
		return nil, fmt.Errorf("unknown device message type %d", typ)
	}
	msg := make([]byte, 1+body)
	msg[0] = typ
	if _, err := io.ReadFull(r, msg[1:]); err != nil {
		return nil, err
	}
	return msg, nil
}

func (s *Session) pingLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			if err := s.writeEnvelope(&Message{Type: TypePing, T: time.Now().UnixMilli()}); err != nil {
				s.shutdown(readFailure(err))
				return
			}
		}
	}
}

func (s *Session) writeEnvelope(m *Message) error {
	b, err := encodeEnvelope(m)
	if err == nil {
		_, err = s.write(b)
	}
	return err
}

func (s *Session) write(b []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// A target that stopped reading must not block input forever.
	s.control.SetWriteDeadline(time.Now().Add(readTimeout))
	return s.control.Write(b)
}

func (s *Session) shutdown(reason string) {
	s.once.Do(func() {
		if s.closing.Load() {
			reason = "closed"
		}
		s.reason.Store(reason)
		close(s.done)
		for _, c := range []net.Conn{s.control, s.Video, s.Audio} {
			if c != nil {
				c.Close()
			}
		}
		if s.events.Closed != nil {
			s.events.Closed(reason)
		}
	})
}

type controlConn struct {
	s   *Session
	buf []byte
}

func (c *controlConn) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		select {
		case msg := <-c.s.device:
			c.buf = msg
		case <-c.s.done:
			return 0, io.EOF
		}
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *controlConn) Write(p []byte) (int, error) { return c.s.write(p) }
func (c *controlConn) Close() error                { c.s.Close(false); return nil }
func (c *controlConn) LocalAddr() net.Addr         { return c.s.control.LocalAddr() }
func (c *controlConn) RemoteAddr() net.Addr        { return c.s.control.RemoteAddr() }

// Deadlines belong to the session's own liveness checks.
func (c *controlConn) SetDeadline(time.Time) error      { return nil }
func (c *controlConn) SetReadDeadline(time.Time) error  { return nil }
func (c *controlConn) SetWriteDeadline(time.Time) error { return nil }
