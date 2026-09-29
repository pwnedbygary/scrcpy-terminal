package peer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestNearbyCodesMatchTheSharedVectors(t *testing.T) {
	f := loadPairingFixture(t)
	if len(f.Nearby) == 0 {
		t.Fatal("pairing.json has no nearby vectors")
	}
	controller, err := ParseFingerprint(f.ControllerFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	target, err := ParseFingerprint(f.TargetFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range f.Nearby {
		cn, _ := hex.DecodeString(v.ControllerNonce)
		tn, _ := hex.DecodeString(v.TargetNonce)
		if got := hex.EncodeToString(Commitment(tn, target, controller)); got != v.Commitment {
			t.Errorf("Commitment = %s, want %s", got, v.Commitment)
		}
		if got := ShortCode(controller, target, cn, tn); got != v.Code {
			t.Errorf("ShortCode = %s, want %s", got, v.Code)
		}
	}
	if got := DisplayCode("097297"); got != "097 297" {
		t.Errorf("DisplayCode = %q", got)
	}
}

// nearbyScript is how a fake target behaves once past the common prefix.
type nearbyScript struct {
	rejectFirst string              // refuse the request with this code
	swapNonce   bool                // reveal a nonce other than the committed one
	after       func(conn net.Conn) // once both sides can show the code
}

// startNearbyTarget serves one nearby pairing and reports the code it computed.
func startNearbyTarget(t *testing.T, s nearbyScript) (int, Fingerprint, <-chan string) {
	t.Helper()
	id, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{id.Certificate}, ClientAuth: tls.RequireAnyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	codes := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tc := conn.(*tls.Conn)
		if tc.Handshake() != nil {
			return
		}
		controller := FingerprintOf(tc.ConnectionState().PeerCertificates[0].RawSubjectPublicKeyInfo)
		req, err := ReadFrame(conn)
		if err != nil || req.Type != TypePairNearby || req.Client == nil {
			return
		}
		if s.rejectFirst != "" {
			WriteFrame(conn, &Message{Type: TypeReject, Code: s.rejectFirst, Message: "not now"})
			return
		}
		nonce := make([]byte, NonceSize)
		rand.Read(nonce)
		WriteFrame(conn, &Message{Type: TypeCodeCommit, Commit: hex.EncodeToString(Commitment(nonce, id.Fingerprint, controller))})
		cn, err := ReadFrame(conn)
		if err != nil || cn.Type != TypeCodeNonce {
			return
		}
		controllerNonce, _ := hex.DecodeString(cn.Nonce)
		revealed := nonce
		if s.swapNonce {
			revealed = make([]byte, NonceSize)
			rand.Read(revealed)
		}
		WriteFrame(conn, &Message{Type: TypeCodeReveal, Nonce: hex.EncodeToString(revealed), Device: &DeviceInfo{Name: "Fake"}})
		codes <- ShortCode(controller, id.Fingerprint, controllerNonce, nonce)
		if s.after != nil {
			s.after(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, id.Fingerprint, codes
}

func result(t *testing.T, p *NearbyPairing) NearbyResult {
	t.Helper()
	select {
	case r := <-p.Result():
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no pairing result")
		return NearbyResult{}
	}
}

func TestNearbyPairingWhenBothAgree(t *testing.T) {
	port, targetID, codes := startNearbyTarget(t, nearbyScript{after: func(conn net.Conn) {
		if m, err := ReadFrame(conn); err == nil && m.Type == TypeCodeConfirm {
			WriteFrame(conn, &Message{Type: TypePaired, Device: &DeviceInfo{Name: "Fake"}, Grants: []string{GrantView, GrantControl}})
		}
	}})
	p, err := testClient(t).StartNearbyPairing("127.0.0.1", port, 0)
	if err != nil {
		t.Fatal(err)
	}
	if code := <-codes; p.Code != code || len(code) != 6 {
		t.Fatalf("codes differ: %q here, %q on the target", p.Code, code)
	}
	if p.Device.Name != "Fake" || !p.Target.Equal(targetID) {
		t.Fatalf("device %+v, target %s", p.Device, p.Target.Short())
	}
	if err := p.Confirm(); err != nil {
		t.Fatal(err)
	}
	r := result(t, p)
	if r.Err != nil || strings.Join(r.Pair.Grants, ",") != "view,control" || !r.Pair.Fingerprint.Equal(targetID) {
		t.Fatalf("result %+v", r)
	}
}

func TestNearbyDeclineArrivesWithoutConfirming(t *testing.T) {
	port, _, _ := startNearbyTarget(t, nearbyScript{after: func(conn net.Conn) {
		WriteFrame(conn, &Message{Type: TypeReject, Code: RejectDeclined, Message: "Fake declined"})
	}})
	p, err := testClient(t).StartNearbyPairing("127.0.0.1", port, 0)
	if err != nil {
		t.Fatal(err)
	}
	var rej *RejectError
	if r := result(t, p); !errors.As(r.Err, &rej) || rej.Code != RejectDeclined {
		t.Fatalf("result %+v", r)
	}
}

func TestNearbyTargetThatSkipsConfirmationIsRefused(t *testing.T) {
	port, _, _ := startNearbyTarget(t, nearbyScript{after: func(conn net.Conn) {
		WriteFrame(conn, &Message{Type: TypePaired, Device: &DeviceInfo{Name: "Fake"}, Grants: []string{GrantView}})
	}})
	p, err := testClient(t).StartNearbyPairing("127.0.0.1", port, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r := result(t, p); r.Err == nil || !strings.Contains(r.Err.Error(), "did not wait") {
		t.Fatalf("result %+v", r)
	}
}

func TestNearbySwappedNonceIsCaught(t *testing.T) {
	// A relay would need this to steer the code after seeing our nonce.
	port, _, _ := startNearbyTarget(t, nearbyScript{swapNonce: true})
	var rej *RejectError
	if _, err := testClient(t).StartNearbyPairing("127.0.0.1", port, 0); !errors.As(err, &rej) || rej.Code != "bad_proof" {
		t.Fatalf("swapped nonce: %v", err)
	}
}

func TestNearbyWithoutAnInvitation(t *testing.T) {
	port, _, _ := startNearbyTarget(t, nearbyScript{rejectFirst: "no_invitation"})
	var rej *RejectError
	if _, err := testClient(t).StartNearbyPairing("127.0.0.1", port, 0); !errors.As(err, &rej) || rej.Code != "no_invitation" {
		t.Fatalf("no invitation: %v", err)
	}
}

func TestNearbyCancel(t *testing.T) {
	gone := make(chan struct{})
	port, _, _ := startNearbyTarget(t, nearbyScript{after: func(conn net.Conn) {
		ReadFrame(conn) // returns when this side hangs up
		close(gone)
	}})
	p, err := testClient(t).StartNearbyPairing("127.0.0.1", port, 0)
	if err != nil {
		t.Fatal(err)
	}
	p.Cancel()
	if r := result(t, p); !errors.Is(r.Err, ErrPairingCancelled) {
		t.Fatalf("result %+v", r)
	}
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the target never saw the hang-up")
	}
}

// ------------------------------------------------------------- discovery

func mdnsAnswer(t *testing.T, add func(b *dnsmessage.Builder)) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	b.EnableCompression()
	add(&b)
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestNearbyCollectorAssemblesAnswers(t *testing.T) {
	svc := dnsmessage.MustNewName(nearbyService)
	inst := dnsmessage.MustNewName("Retroid Pocket 6." + nearbyService)
	host := dnsmessage.MustNewName("Android-2.local.")
	flush := dnsmessage.Class(0x8001) // mDNS cache-flush bit on IN
	c := newNearbyCollector()
	c.add(mdnsAnswer(t, func(b *dnsmessage.Builder) {
		b.StartAnswers()
		b.PTRResource(dnsmessage.ResourceHeader{Name: svc, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.PTRResource{PTR: inst})
		b.StartAdditionals()
		b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Class: flush, TTL: 120}, dnsmessage.SRVResource{Port: 27300, Target: host})
		b.TXTResource(dnsmessage.ResourceHeader{Name: inst, Class: flush, TTL: 120}, dnsmessage.TXTResource{TXT: []string{""}})
		b.AResource(dnsmessage.ResourceHeader{Name: host, Class: flush, TTL: 120}, dnsmessage.AResource{A: [4]byte{192, 168, 1, 241}})
	}), net.IPv4(192, 168, 1, 99))
	got := c.devices()
	if len(got) != 1 || got[0] != (NearbyDevice{Name: "Retroid Pocket 6", Host: "192.168.1.241", Port: 27300}) {
		t.Fatalf("devices %+v", got)
	}
}

func TestNearbyCollectorAsksAgainAndFallsBackToTheSender(t *testing.T) {
	svc := dnsmessage.MustNewName(nearbyService)
	inst := dnsmessage.MustNewName("Red Magic 9s." + nearbyService)
	c := newNearbyCollector()
	c.add(mdnsAnswer(t, func(b *dnsmessage.Builder) {
		b.StartAnswers()
		b.PTRResource(dnsmessage.ResourceHeader{Name: svc, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.PTRResource{PTR: inst})
	}), net.IPv4(192, 168, 1, 251))
	if u := c.unresolved(); len(u) != 1 || u[0] != "Red Magic 9s."+nearbyService {
		t.Fatalf("unresolved %v", u)
	}
	if len(c.devices()) != 0 {
		t.Fatal("a device without a port was listed")
	}
	query, err := nearbyQuery(c.unresolved())
	if err != nil {
		t.Fatal(err)
	}
	var p dnsmessage.Parser
	if _, err := p.Start(query); err != nil {
		t.Fatal(err)
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 2 || qs[0].Type != dnsmessage.TypePTR || qs[1].Type != dnsmessage.TypeSRV {
		t.Fatalf("query questions %v, %v", qs, err)
	}
	c.add(mdnsAnswer(t, func(b *dnsmessage.Builder) {
		b.StartAnswers()
		b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.SRVResource{Port: 27300, Target: dnsmessage.MustNewName("phone.local.")})
	}), net.IPv4(192, 168, 1, 251))
	if got := c.devices(); len(got) != 1 || got[0].Host != "192.168.1.251" || got[0].Name != "Red Magic 9s" {
		t.Fatalf("devices %+v", got)
	}
}

func TestNearbyCollectorIgnoresQueriesAndJunk(t *testing.T) {
	c := newNearbyCollector()
	query, _ := nearbyQuery(nil)
	c.add(query, net.IPv4(10, 0, 0, 1))
	junk := make([]byte, 512)
	for i := 0; i < 200; i++ {
		rand.Read(junk)
		c.add(junk[:i*2], net.IPv4(10, 0, 0, 2))
	}
	if len(c.devices()) != 0 || len(c.unresolved()) != 0 {
		t.Fatal("junk produced devices")
	}
}

// TestNearbyLive pairs with a real device that has an invitation open, e.g.
//
//	SCTERM_NEARBY_LIVE=1 go test ./peer -run NearbyLive -v
//
// and accept on the device once the codes are logged. SCTERM_NEARBY_LIVE=discover
// only lists what answers.
func TestNearbyLive(t *testing.T) {
	mode := os.Getenv("SCTERM_NEARBY_LIVE")
	if mode == "" {
		t.Skip("set SCTERM_NEARBY_LIVE with a device inviting on this network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	devices, err := DiscoverNearby(ctx, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("found %+v", devices)
	if len(devices) == 0 {
		t.Fatal("no device is inviting")
	}
	if mode == "discover" {
		return
	}
	d := devices[0]
	p, err := testClient(t).StartNearbyPairing(d.Host, d.Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CODE %s from %q; accept it on the device", DisplayCode(p.Code), p.Device.Name)
	if err := p.Confirm(); err != nil {
		t.Fatal(err)
	}
	r := <-p.Result()
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	t.Logf("paired with %q (%s), grants %v", r.Pair.Device.Name, r.Pair.Fingerprint.Short(), r.Pair.Grants)
}
