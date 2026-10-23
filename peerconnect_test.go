package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// A --peer connection that fails says what failed, where, and what to try; a
// device that moved is found again the way scterm peers --check finds it.
// The targets here are fakes on loopback, and the presence search is faked,
// so nothing goes out on the network.
// ---------------------------------------------------------------------------

// fakePeerTarget is a paired target that answers each hello.
type fakePeerTarget struct {
	id *peer.Identity
	ln net.Listener
}

// newFakePeerTarget answers every hello with reply.
func newFakePeerTarget(t *testing.T, reply peer.Message) *fakePeerTarget {
	t.Helper()
	return newAnsweringPeerTarget(t, func(*peer.Message) peer.Message { return reply })
}

// newAnsweringPeerTarget answers each hello with what answer makes of it.
func newAnsweringPeerTarget(t *testing.T, answer func(hello *peer.Message) peer.Message) *fakePeerTarget {
	t.Helper()
	id, err := peer.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{id.Certificate}, ClientAuth: tls.RequireAnyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				hello, err := peer.ReadFrame(conn)
				if err != nil {
					return
				}
				msg := answer(hello)
				if peer.WriteFrame(conn, &msg) == nil {
					io.Copy(io.Discard, conn)
				}
			}()
		}
	}()
	return &fakePeerTarget{id: id, ln: ln}
}

func (f *fakePeerTarget) fingerprint() string { return f.id.Fingerprint.Hex() }
func (f *fakePeerTarget) port() int           { return f.ln.Addr().(*net.TCPAddr).Port }

var controlWelcome = peer.Message{Type: peer.TypeWelcome, V: 1, Channel: peer.ChannelControl, Session: "s1",
	Token: strings.Repeat("0", 64), Grants: []string{peer.GrantControl}, Lease: peer.LeaseHeld, Device: &peer.DeviceInfo{Name: "Pixel 8"}}

// grantingOnly answers a control hello as the Android target does when its
// pairing with this computer grants grants (TargetServer.handleControl): it
// refuses as forbidden when nothing requested is granted (asking for control
// asks for the clipboard too); otherwise it welcomes, granting the overlap.
func grantingOnly(grants ...string) func(*peer.Message) peer.Message {
	return func(hello *peer.Message) peer.Message {
		req := peer.StreamRequest{Video: true, Audio: true, Control: true}
		if hello.Request != nil {
			req = *hello.Request
		}
		var effective []string
		for _, g := range []struct {
			asked bool
			grant string
		}{{req.Video, peer.GrantView}, {req.Audio, peer.GrantAudio}, {req.Control, peer.GrantControl}, {req.Control, peer.GrantClipboard}} {
			if g.asked && slices.Contains(grants, g.grant) {
				effective = append(effective, g.grant)
			}
		}
		if len(effective) == 0 {
			return peer.Message{Type: peer.TypeReject, Code: "forbidden", Message: "nothing requested is granted to this device"}
		}
		welcome := controlWelcome
		welcome.Grants = effective
		return welcome
	}
}

// pairedAt makes a fresh SCTERM_HOME whose one paired device, Pixel 8, has
// the identity fingerprint and was last at host:port.
func pairedAt(t *testing.T, fingerprint, host string, port int) *peer.Store {
	t.Helper()
	withInteractive(t, false)
	home := t.TempDir()
	t.Setenv("SCTERM_HOME", home)
	store := peer.OpenStore(home)
	if err := store.Put(peer.Record{Fingerprint: fingerprint, Name: "Pixel 8", Host: host, Port: port, Grants: []string{"control"}, PairedAtMs: 1}); err != nil {
		t.Fatal(err)
	}
	return store
}

// searchFinds makes the presence search find these devices; it returns how
// many searches there were.
func searchFinds(t *testing.T, found ...peer.NearbyDevice) *int {
	t.Helper()
	old := presenceSearch
	t.Cleanup(func() { presenceSearch = old })
	searches := new(int)
	presenceSearch = func(context.Context, time.Duration) ([]peer.NearbyDevice, error) {
		*searches++
		return found, nil
	}
	return searches
}

// closedPort is a loopback port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func controlOnly() config {
	cfg := defaultConfig()
	cfg.video, cfg.audio = false, false
	return cfg
}

func TestUnreachablePeerSaysWhatToTry(t *testing.T) {
	target := newFakePeerTarget(t, controlWelcome)
	port := closedPort(t)
	pairedAt(t, target.fingerprint(), "127.0.0.1", port)
	searchFinds(t)
	_, err := newPeerSession("Pixel 8", controlOnly())
	for _, want := range []string{
		fmt.Sprintf("could not reach Pixel 8 at 127.0.0.1:%d (connection refused)", port),
		"check that it is serving and on this network, or run scterm peers --check",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("connect: %v\nwant it to say %q", err, want)
		}
	}
}

func TestPeerRefusalsSayWhyAndWhatToTry(t *testing.T) {
	for _, tc := range []struct{ code, message, want string }{
		{"not_paired", "this device is not paired with the target", "pairing no longer accepted; pair again with scterm pair"},
		{"busy", "too many sessions", "it already has the maximum number of viewers; close one of them and try again"},
		{"unavailable", "serving has not finished starting", "it is not ready (serving has not finished starting); check Pixel 8, then try again"},
		{"unavailable", "", "serving is not ready; check Pixel 8, then try again"},
		{"version", "peer protocol 1 required", "it runs an incompatible version of scterm; update scterm on Pixel 8 and on this computer"},
		{"bad_token", "unknown or expired session", "unknown or expired session; try again"},
		// A target's own words reach the terminal without control characters.
		{"teapot", "\x1b]0;owned\x07short and stout", "]0;ownedshort and stout; run scterm peers --check"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			target := newFakePeerTarget(t, peer.Message{Type: peer.TypeReject, Code: tc.code, Message: tc.message})
			pairedAt(t, target.fingerprint(), "127.0.0.1", target.port())
			searches := searchFinds(t)
			_, err := newPeerSession("Pixel 8", controlOnly())
			want := fmt.Sprintf("Pixel 8 at 127.0.0.1:%d refused the connection: %s", target.port(), tc.want)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("connect: %q\nwant it to say %q", err, want)
			}
			if err != nil && strings.ContainsFunc(err.Error(), unicode.IsControl) {
				t.Errorf("connect: %q holds control characters", err)
			}
			if *searches != 0 {
				t.Errorf("searched the network %d times for a device that answered", *searches)
			}
		})
	}
}

// TestForbiddenSaysWhatWasAsked: a device refuses as forbidden when it allows
// none of what was asked, whatever that was; the refusal names it, and says
// what to try that works whatever the device allows.
func TestForbiddenSaysWhatWasAsked(t *testing.T) {
	const orAskOther = ", or ask for what it does allow (scterm peers --check lists it)"
	for _, tc := range []struct {
		name                  string
		video, audio, control bool
		grants                []string
		want                  string // the refusal; "" when it connects
	}{
		{"control asked, view allowed", false, false, true, []string{peer.GrantView},
			"it does not allow this computer to control it; on Pixel 8, allow that" + orAskOther},
		{"audio asked, view and control allowed", false, true, false, []string{peer.GrantView, peer.GrantControl},
			"it does not allow this computer to hear it; on Pixel 8, allow that" + orAskOther},
		{"everything asked, nothing allowed", true, true, true, nil,
			"it does not allow this computer to view, hear or control it; on Pixel 8, allow that"},
		// The same target lets in a request it allows some of.
		{"control asked, view and control allowed", false, false, true, []string{peer.GrantView, peer.GrantControl}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := newAnsweringPeerTarget(t, grantingOnly(tc.grants...))
			pairedAt(t, target.fingerprint(), "127.0.0.1", target.port())
			searchFinds(t)
			cfg := defaultConfig()
			cfg.video, cfg.audio, cfg.control = tc.video, tc.audio, tc.control
			s, err := newPeerSession("Pixel 8", cfg)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("connect: %v", err)
				}
				s.stop()
				return
			}
			want := fmt.Sprintf("Pixel 8 at 127.0.0.1:%d refused the connection: %s", target.port(), tc.want)
			if err == nil || err.Error() != want {
				t.Errorf("connect: %v\nwant %q", err, want)
			}
		})
	}
}

// TestFailureCleansTheDeviceName: the name is the one the device gave when
// it paired; it reaches the terminal without control characters.
func TestFailureCleansTheDeviceName(t *testing.T) {
	refusing := newFakePeerTarget(t, peer.Message{Type: peer.TypeReject, Code: "unavailable"})
	closed := closedPort(t)
	for _, tc := range []struct {
		name string
		port int
		want string
	}{
		{"refused", refusing.port(), fmt.Sprintf("Pixel 8]0;x at 127.0.0.1:%d refused the connection: serving is not ready; check Pixel 8]0;x, then try again", refusing.port())},
		{"unreachable", closed, fmt.Sprintf("could not reach Pixel 8]0;x at 127.0.0.1:%d (connection refused): check that it is serving and on this network, or run scterm peers --check", closed)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := pairedAt(t, refusing.fingerprint(), "127.0.0.1", tc.port)
			if err := store.UpdateExisting(refusing.fingerprint(), func(r peer.Record) peer.Record {
				r.Name = "Pixel 8\x1b]0;x\x07"
				return r
			}); err != nil {
				t.Fatal(err)
			}
			searchFinds(t)
			_, err := newPeerSession("", controlOnly())
			if err == nil || err.Error() != tc.want {
				t.Errorf("connect: %q\nwant %q", err, tc.want)
			}
		})
	}
}

// TestMovedPeerIsFoundAgain: when the saved address no longer answers, the
// device is looked for, connected to where it proves who it is, and its new
// address saved. Another device serving on the network is left alone.
func TestMovedPeerIsFoundAgain(t *testing.T) {
	target := newFakePeerTarget(t, controlWelcome)
	other := newFakePeerTarget(t, controlWelcome)
	old := closedPort(t)
	store := pairedAt(t, target.fingerprint(), "127.0.0.1", old)
	searches := searchFinds(t,
		peer.NearbyDevice{Name: "Galaxy Tab", Host: "127.0.0.1", Port: other.port(), FingerprintHint: other.fingerprint()},
		peer.NearbyDevice{Name: "Pixel 8", Host: "127.0.0.1", Port: target.port(), FingerprintHint: target.fingerprint()})
	s, err := newPeerSession("Pixel 8", controlOnly())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.stop()
	if s.peer == nil || s.control == nil || s.deviceName != "Pixel 8" {
		t.Errorf("session %+v", s)
	}
	if *searches != 1 {
		t.Errorf("searched %d times, want once", *searches)
	}
	if recs, err := store.All(); err != nil || len(recs) != 1 || recs[0].Port != target.port() {
		t.Errorf("saved %+v, %v; want the new port %d", recs, err, target.port())
	}
}

// TestMovedPeerMustProveItself: a device found by the search that claims the
// identity but cannot prove it is not used, nor its address saved.
func TestMovedPeerMustProveItself(t *testing.T) {
	paired := newFakePeerTarget(t, controlWelcome)
	impostor := newFakePeerTarget(t, controlWelcome)
	old := closedPort(t)
	store := pairedAt(t, paired.fingerprint(), "127.0.0.1", old)
	searchFinds(t, peer.NearbyDevice{Name: "Pixel 8", Host: "127.0.0.1", Port: impostor.port(), FingerprintHint: paired.fingerprint()})
	_, err := newPeerSession("Pixel 8", controlOnly())
	if want := fmt.Sprintf("could not reach Pixel 8 at 127.0.0.1:%d (connection refused)", old); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("connect: %v\nwant it to say %q", err, want)
	}
	if recs, err := store.All(); err != nil || len(recs) != 1 || recs[0].Port != old {
		t.Errorf("saved %+v, %v; want the old port %d kept", recs, err, old)
	}
}

// TestMovedPeerThatRefusesSaysWhere: the device found elsewhere proves who it
// is and then refuses; that refusal, at its new address, is what is reported.
func TestMovedPeerThatRefusesSaysWhere(t *testing.T) {
	target := newFakePeerTarget(t, peer.Message{Type: peer.TypeReject, Code: "busy", Message: "too many sessions"})
	pairedAt(t, target.fingerprint(), "127.0.0.1", closedPort(t))
	searchFinds(t, peer.NearbyDevice{Name: "Pixel 8", Host: "127.0.0.1", Port: target.port(), FingerprintHint: target.fingerprint()})
	_, err := newPeerSession("Pixel 8", controlOnly())
	want := fmt.Sprintf("Pixel 8 at 127.0.0.1:%d refused the connection: it already has the maximum number of viewers", target.port())
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("connect: %v\nwant it to say %q", err, want)
	}
}
