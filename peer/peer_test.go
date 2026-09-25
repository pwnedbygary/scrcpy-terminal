package peer

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type pairingFixture struct {
	SecretHex                  string `json:"secretHex"`
	Code                       string `json:"code"`
	ControllerSpkiUtf8         string `json:"controllerSpkiUtf8"`
	TargetSpkiUtf8             string `json:"targetSpkiUtf8"`
	ControllerFingerprint      string `json:"controllerFingerprint"`
	TargetFingerprint          string `json:"targetFingerprint"`
	ControllerFingerprintShort string `json:"controllerFingerprintShort"`
	ControllerProof            string `json:"controllerProof"`
	TargetProof                string `json:"targetProof"`
	InvitationURI              string `json:"invitationUri"`
	InvitationManual           string `json:"invitationManual"`
	Base32                     []struct {
		Hex  string `json:"hex"`
		Text string `json:"text"`
	} `json:"base32"`
}

// The same vectors the Kotlin implementation is tested against.
func loadPairingFixture(t *testing.T) pairingFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "protocol", "fixtures", "pairing.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f pairingFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSharedPairingVectors(t *testing.T) {
	f := loadPairingFixture(t)
	for _, v := range f.Base32 {
		raw, _ := hex.DecodeString(v.Hex)
		if got := Base32Encode(raw); got != v.Text {
			t.Errorf("Base32Encode(%s) = %q, want %q", v.Hex, got, v.Text)
		}
		back, err := Base32Decode(v.Text)
		if err != nil || !bytes.Equal(back, raw) {
			t.Errorf("Base32Decode(%q) = %x, %v; want %s", v.Text, back, err, v.Hex)
		}
	}
	secret, _ := hex.DecodeString(f.SecretHex)
	controller := FingerprintOf([]byte(f.ControllerSpkiUtf8))
	target := FingerprintOf([]byte(f.TargetSpkiUtf8))
	if controller.Hex() != f.ControllerFingerprint || target.Hex() != f.TargetFingerprint {
		t.Fatalf("fingerprints %s / %s", controller.Hex(), target.Hex())
	}
	if controller.Short() != f.ControllerFingerprintShort {
		t.Errorf("short = %s, want %s", controller.Short(), f.ControllerFingerprintShort)
	}
	if got := hex.EncodeToString(PairingProof(secret, RoleController, controller, target)); got != f.ControllerProof {
		t.Errorf("controller proof = %s", got)
	}
	if got := hex.EncodeToString(PairingProof(secret, RoleTarget, target, controller)); got != f.TargetProof {
		t.Errorf("target proof = %s", got)
	}

	link, err := ParseInvitation(f.InvitationURI)
	if err != nil {
		t.Fatal(err)
	}
	if link.Host != "192.168.1.20" || link.Port != 27300 || !bytes.Equal(link.Secret, secret) || link.Fingerprint == nil || !link.Fingerprint.Equal(target) {
		t.Errorf("link parsed as %+v", link)
	}
	if link.URI() != f.InvitationURI || link.ManualText() != f.InvitationManual || link.Code() != f.Code {
		t.Errorf("link re-encoded as %q / %q", link.URI(), link.ManualText())
	}
	typed, err := ParseInvitation(f.InvitationManual)
	if err != nil || typed.Host != link.Host || typed.Port != link.Port || !bytes.Equal(typed.Secret, secret) || typed.Fingerprint != nil {
		t.Errorf("typed parsed as %+v, %v", typed, err)
	}
}

func TestCodesTolerateTypingMistakes(t *testing.T) {
	f := loadPairingFixture(t)
	secret, _ := hex.DecodeString(f.SecretHex)
	// Lower case, spaces instead of dashes, O for 0 and l/I for 1.
	for _, typed := range []string{"04hm-asw9-nf6y-y093", "O4HM ASW9 NF6Y Y093", "04HMASW9NF6YYO93"} {
		inv, err := ParseInvitation("host:1 " + typed)
		if err != nil || !bytes.Equal(inv.Secret, secret) {
			t.Errorf("%q: %x, %v", typed, inv.Secret, err)
		}
	}
	if b, _ := Base32Decode("1iIlL"); !bytes.Equal(b, mustBase32(t, "11111")) {
		t.Errorf("I/L not read as 1: %x", b)
	}
	for _, bad := range []string{"host:1 04HM-ASW9-NF6Y", "host:1 04HM-ASW9-NF6Y-Y09U", "host 04HM", "host:0 04HM-ASW9-NF6Y-Y093", "scterm://pair?p=1&c=04HMASW9NF6YY093"} {
		if _, err := ParseInvitation(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func mustBase32(t *testing.T, s string) []byte {
	b, err := Base32Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
	}{
		{"10.0.0.2", "10.0.0.2", DefaultPort},
		{"10.0.0.2:9", "10.0.0.2", 9},
		{"[fe80::1]", "fe80::1", DefaultPort},
		{"[fe80::1]:9", "fe80::1", 9},
		{"fe80::1", "fe80::1", DefaultPort},
	}
	for _, c := range cases {
		host, port, err := ParseAddress(c.in, DefaultPort)
		if err != nil || host != c.host || port != c.port {
			t.Errorf("%q = %q %d %v", c.in, host, port, err)
		}
	}
	inv := Invitation{Host: "fe80::1", Port: 9, Secret: make([]byte, secretBytes)}
	if inv.Address() != "[fe80::1]:9" || !strings.Contains(inv.URI(), "h=fe80%3A%3A1") {
		t.Errorf("IPv6 forms %q %q", inv.Address(), inv.URI())
	}
}

func TestFramesAreBounded(t *testing.T) {
	var buf bytes.Buffer
	sent := &Message{Type: TypeHello, V: 1, MinV: 1, Channel: ChannelControl, Request: &StreamRequest{Video: true, Control: true}}
	if err := WriteFrame(&buf, sent); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"type":"hello"`) || !strings.Contains(buf.String(), `"audio":false`) {
		t.Errorf("wire %q", buf.String()[4:])
	}
	got, err := ReadFrame(&buf)
	if err != nil || got.Type != TypeHello || got.Request == nil || !got.Request.Video {
		t.Fatalf("round trip %+v, %v", got, err)
	}
	frame := func(n uint32, body string) io.Reader {
		b := binary.BigEndian.AppendUint32(nil, n)
		return bytes.NewReader(append(b, body...))
	}
	for name, r := range map[string]io.Reader{
		"oversized":    frame(maxFrame+1, ""),
		"truncated":    frame(10, `{"type"`),
		"not json":     frame(3, "abc"),
		"missing type": frame(2, "{}"),
	} {
		if _, err := ReadFrame(r); err == nil {
			t.Errorf("%s frame accepted", name)
		}
	}
}

func TestIdentityPersists(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateIdentity(dir)
	if err != nil || !a.Fingerprint.Equal(b.Fingerprint) {
		t.Fatalf("reloaded identity differs: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "identity.pem"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("identity file mode %v, %v", info.Mode(), err)
	}
	leaf, _ := x509.ParseCertificate(a.Certificate.Certificate[0])
	if !FingerprintOf(leaf.RawSubjectPublicKeyInfo).Equal(a.Fingerprint) {
		t.Error("fingerprint is not SHA-256 of the SPKI")
	}
}

func TestStoreFind(t *testing.T) {
	s := OpenStore(t.TempDir())
	if _, err := s.Find(""); err == nil {
		t.Error("empty store found something")
	}
	a := Record{Fingerprint: strings.Repeat("ab", 32), Name: "Pixel", Host: "10.0.0.2", Port: DefaultPort}
	b := Record{Fingerprint: strings.Repeat("cd", 32), Name: "Tablet", Host: "10.0.0.3", Port: DefaultPort}
	for _, r := range []Record{a, b} {
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	fpA, _ := a.ID()
	for query, want := range map[string]string{"pixel": "Pixel", "abab": "Pixel", strings.ToLower(fpA.Short()[:9]): "Pixel", "cdcd": "Tablet"} {
		if r, err := s.Find(query); err != nil || r.Name != want {
			t.Errorf("Find(%q) = %q, %v", query, r.Name, err)
		}
	}
	for _, query := range []string{"", "ab", "zzzz"} {
		if _, err := s.Find(query); err == nil {
			t.Errorf("Find(%q) should fail with two paired devices", query)
		}
	}
	a.Name = "Pixel 9"
	s.Put(a)
	if recs, _ := s.All(); len(recs) != 2 || recs[0].Name != "Pixel 9" {
		t.Errorf("Put did not replace: %+v", recs)
	}
	if removed, _ := s.Remove(b.Fingerprint); !removed {
		t.Error("Remove failed")
	}
	if r, err := s.Find(""); err != nil || r.Name != "Pixel 9" {
		t.Errorf("single record not found by empty query: %v", err)
	}
}

// fakeTarget is a minimal target: enough protocol to exercise the client's
// TLS pinning, handshake and control-channel demultiplexing.
type fakeTarget struct {
	t        *testing.T
	identity *Identity
	ln       net.Listener
	control  chan net.Conn
	received chan *Message
}

func newFakeTarget(t *testing.T) *fakeTarget {
	id, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{id.Certificate}, ClientAuth: tls.RequireAnyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeTarget{t: t, identity: id, ln: ln, control: make(chan net.Conn, 1), received: make(chan *Message, 16)}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeTarget) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeTarget) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			hello, err := ReadFrame(conn)
			if err != nil {
				conn.Close()
				return
			}
			switch hello.Channel {
			case ChannelControl:
				WriteFrame(conn, &Message{Type: TypeWelcome, V: 1, Channel: ChannelControl, Session: "s1", Token: strings.Repeat("0", 64),
					Grants: []string{GrantView, GrantControl}, Lease: LeaseHeld, Device: &DeviceInfo{Name: "Fake"}})
				f.control <- conn
			case ChannelVideo:
				WriteFrame(conn, &Message{Type: TypeWelcome, V: 1, Channel: ChannelVideo, Session: hello.Session})
				conn.Write([]byte{'h', '2', '6', '4'})
			default:
				WriteFrame(conn, &Message{Type: TypeReject, Code: "forbidden"})
				conn.Close()
			}
		}()
	}
}

func testClient(t *testing.T) *Client {
	id, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Client{Identity: id, Info: ClientInfo{Name: "test", App: "scterm-test"}}
}

func TestPinRefusesAnotherIdentity(t *testing.T) {
	target := newFakeTarget(t)
	c := testClient(t)
	var wrong Fingerprint
	_, err := c.Connect("127.0.0.1", target.port(), wrong, StreamRequest{Control: true})
	if err == nil || !strings.Contains(err.Error(), "not the paired") {
		t.Fatalf("pinned connect to another identity: %v", err)
	}
}

func TestSessionDemultiplexesTheControlChannel(t *testing.T) {
	old := pingInterval
	pingInterval = 50 * time.Millisecond
	defer func() { pingInterval = old }()

	target := newFakeTarget(t)
	c := testClient(t)
	s, err := c.Connect("127.0.0.1", target.port(), target.identity.Fingerprint, StreamRequest{Video: true, Audio: true, Control: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Audio != nil {
		t.Error("audio opened without the audio grant")
	}
	codec := make([]byte, 4)
	if _, err := io.ReadFull(s.Video, codec); err != nil || string(codec) != "h264" {
		t.Fatalf("video stream starts %q, %v", codec, err)
	}
	leases := make(chan string, 4)
	closed := make(chan string, 1)
	s.Start(Events{Lease: func(state, holder string) { leases <- state + "/" + holder }, Closed: func(r string) { closed <- r }})
	conn := <-target.control
	in := bufio.NewReader(conn)

	// Target side: a lease envelope, then a device clipboard message, then answer pings.
	env, _ := encodeEnvelope(&Message{Type: TypeLease, State: LeaseViewer, Holder: "Other"})
	clip := append([]byte{0}, binary.BigEndian.AppendUint32(nil, 3)...)
	conn.Write(append(env, append(clip, "abc"...)...))

	got := make([]byte, 8)
	n, err := io.ReadFull(s.Control(), got)
	if err != nil || n != 8 || !bytes.Equal(got, append(clip, "abc"...)) {
		t.Fatalf("control read %x, %v: envelopes must not reach scrcpy parsing", got[:n], err)
	}
	if l := <-leases; l != "viewer/Other" || s.Lease() != LeaseViewer {
		t.Errorf("lease event %q, state %q", l, s.Lease())
	}

	// Controller side: a control message goes through untouched, pings keep coming.
	touch := []byte{2, 0, 1, 2, 3}
	s.Control().Write(touch)
	sawTouch, pings := false, 0
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for !sawTouch || pings < 2 {
		typ, err := in.ReadByte()
		if err != nil {
			t.Fatalf("target read: %v (touch %v, pings %d)", err, sawTouch, pings)
		}
		if typ != envelopeType {
			rest := make([]byte, len(touch)-1)
			io.ReadFull(in, rest)
			sawTouch = bytes.Equal(append([]byte{typ}, rest...), touch)
			continue
		}
		m, err := ReadFrame(in)
		if err != nil || m.Type != TypePing {
			t.Fatalf("envelope %+v, %v", m, err)
		}
		pings++
		pong, _ := encodeEnvelope(&Message{Type: TypePong, T: m.T})
		conn.Write(pong)
	}
	deadline := time.Now().Add(time.Second)
	for s.RoundTrip() < 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.RoundTrip() < 0 {
		t.Error("no round trip measured from pongs")
	}

	// Closing sends bye first.
	go s.Close(false)
	for {
		typ, err := in.ReadByte()
		if err != nil {
			t.Fatal("no bye before close")
		}
		if typ != envelopeType {
			t.Fatalf("unexpected control byte %d", typ)
		}
		if m, _ := ReadFrame(in); m != nil && m.Type == TypeBye {
			break
		}
	}
	conn.Close()
	if r := <-closed; r != "closed" {
		t.Errorf("closed reason %q", r)
	}
}

func TestByeFromTheTargetEndsTheSessionWithItsReason(t *testing.T) {
	target := newFakeTarget(t)
	s, err := testClient(t).Connect("127.0.0.1", target.port(), target.identity.Fingerprint, StreamRequest{Control: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Start(Events{})
	conn := <-target.control
	bye, _ := encodeEnvelope(&Message{Type: TypeBye, Reason: "access revoked"})
	conn.Write(bye)
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not end")
	}
	if s.Reason() != "access revoked" {
		t.Errorf("reason %q", s.Reason())
	}
	if _, err := s.Control().Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("control read after bye: %v", err)
	}
}

// TestInteropWithKotlinTarget runs against a real target, e.g.
//
//	peerctl --home /tmp/pattern serve --port 27411 --invite-host 127.0.0.1
//	SCTERM_PEER_INVITATION="127.0.0.1:27411 XXXX-XXXX-XXXX-XXXX" go test ./peer -run Interop -v
func TestInteropWithKotlinTarget(t *testing.T) {
	text := os.Getenv("SCTERM_PEER_INVITATION")
	if text == "" {
		t.Skip("set SCTERM_PEER_INVITATION to an invitation from a running target")
	}
	inv, err := ParseInvitation(text)
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t)
	paired, err := c.Pair(inv, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("paired with %q (%s), grants %v", paired.Device.Name, paired.Fingerprint.Short(), paired.Grants)
	s, err := c.Connect(inv.Host, inv.Port, paired.Fingerprint, StreamRequest{Video: true, Audio: true, Control: true, VideoCodecs: []string{"h264"}})
	if err != nil {
		t.Fatal(err)
	}
	refused := make(chan string, 4)
	s.Start(Events{Error: func(code, message, controlType string) { refused <- code + " " + controlType + ": " + message }})
	defer s.Close(false)
	t.Logf("welcome: backend %q, lease %q, grants %v", s.Welcome.Caps.Backend, s.Welcome.Lease, s.Welcome.Grants)
	if s.Video == nil {
		t.Fatal("no video channel")
	}
	s.Video.SetReadDeadline(time.Now().Add(10 * time.Second))
	header := make([]byte, 4)
	if _, err := io.ReadFull(s.Video, header); err != nil || string(header) != "h264" {
		t.Fatalf("codec header %q, %v", header, err)
	}
	// 12-byte headers: a session packet has the MSB set and carries the video
	// size; a media packet has PTS and flags, then its payload length.
	var width, height uint32
	sessions, frames := 0, 0
	for frames < 10 {
		var hdr [12]byte
		if _, err := io.ReadFull(s.Video, hdr[:]); err != nil {
			t.Fatal(err)
		}
		if hdr[0]&0x80 != 0 {
			sessions++
			width, height = binary.BigEndian.Uint32(hdr[4:]), binary.BigEndian.Uint32(hdr[8:])
			continue
		}
		body := make([]byte, binary.BigEndian.Uint32(hdr[8:]))
		if _, err := io.ReadFull(s.Video, body); err != nil {
			t.Fatal(err)
		}
		frames++
	}
	if sessions == 0 || width == 0 {
		t.Fatal("no session packet before media")
	}
	// A tap at the centre, laid out as scterm's controller writes inject_touch_event.
	for _, action := range []byte{0, 1} {
		msg := []byte{2, action}
		msg = binary.BigEndian.AppendUint64(msg, 0)
		msg = binary.BigEndian.AppendUint32(msg, width/2)
		msg = binary.BigEndian.AppendUint32(msg, height/2)
		msg = binary.BigEndian.AppendUint16(msg, uint16(width))
		msg = binary.BigEndian.AppendUint16(msg, uint16(height))
		msg = binary.BigEndian.AppendUint16(msg, map[byte]uint16{0: 0xffff, 1: 0}[action])
		msg = binary.BigEndian.AppendUint32(msg, 0)
		msg = binary.BigEndian.AppendUint32(msg, 0)
		if _, err := s.Control().Write(msg); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case r := <-refused:
		t.Fatalf("the target refused the tap: %s", r)
	case <-time.After(time.Second):
	}
	t.Logf("%dx%d, %d session packets, %d media packets; tapped %d,%d; round trip %v", width, height, sessions, frames, width/2, height/2, s.RoundTrip())
}
