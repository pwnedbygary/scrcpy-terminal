package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// Text from a device reaches the terminal cleaned on the command line too:
// its name, as it gave it when it paired or as anyone on the network can give
// it when answering the search for invitations, its refusals, and what adb
// relays from it. Names are still stored, and matched, as the device gave
// them.
// ---------------------------------------------------------------------------

// Names as a device could give them: an OSC title sequence, a bare BEL and a
// right-to-left override; a C1 CSI, a carriage return and a zero-width space.
// Cleaned, they read "Pixel ]0;owned 8" and "Pixel 2J 9".
const (
	dirtyPixel8 = "Pixel\x1b]0;owned\x07 8\u202e"
	dirtyPixel9 = "Pixel\u009b2J\r 9\u200b"
)

// unclean reports whether s holds what a terminal would act on or reorder a
// row by: a control character other than a line break, a format character,
// or a byte that is not UTF-8 (a lone 0x9b is a C1 CSI to some terminals).
func unclean(s string) bool {
	return !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool {
		return r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
	})
}

// TestSeveralMatchesListCleanNames: when a --peer query, or scterm forget's,
// means several paired devices, the error lists their names cleaned. A whole
// name as stored still picks its device.
func TestSeveralMatchesListCleanNames(t *testing.T) {
	withInteractive(t, false)
	store := pairedStore(t, dirtyPixel8, dirtyPixel9)
	for _, tc := range []struct{ query, want string }{
		{"", "2 devices are paired (Pixel ]0;owned 8, Pixel 2J 9); name one"},
		{"pixel", `"pixel" matches Pixel ]0;owned 8, Pixel 2J 9; give more of the name`},
	} {
		if _, err := pickPeer(store, tc.query); err == nil || err.Error() != tc.want {
			t.Errorf("--peer=%s: %q\nwant %q", tc.query, err, tc.want)
		}
	}
	want := `"pix" matches Pixel ]0;owned 8, Pixel 2J 9; give more of the name`
	if err := forgetCommand([]string{"pix"}); err == nil || err.Error() != want {
		t.Errorf("forget pix: %q\nwant %q", err, want)
	}
	for _, name := range []string{dirtyPixel8, dirtyPixel9} {
		if rec, err := pickPeer(store, name); err != nil || rec.Name != name {
			t.Errorf("--peer=%q: %q, %v; want the device of that name", name, rec.Name, err)
		}
	}
	if recs, err := store.All(); err != nil || len(recs) != 2 {
		t.Errorf("%d pairings left (%v), want both", len(recs), err)
	}
}

// TestPeerChooserCleansTheNames: the --peer chooser lists the devices a query
// means by their names, cleaned; the answer picks the device as stored.
func TestPeerChooserCleansTheNames(t *testing.T) {
	withInteractive(t, true)
	store := pairedStore(t, dirtyPixel8, dirtyPixel9)
	old := terminalInput
	t.Cleanup(func() { terminalInput = old })
	terminalInput = bufioReader("2\n")
	var rec peer.Record
	var err error
	stderr := captureStderr(t, func() { rec, err = pickPeer(store, "pixel") })
	want := "  1) Pixel ]0;owned 8 (192.0.2.1:27300)\n  2) Pixel 2J 9 (192.0.2.1:27300)\nConnect to which device? [1-2] "
	if err != nil || rec.Name != dirtyPixel9 || stderr != want {
		t.Errorf("picked %q, %v; stderr %q\nwant the second device and stderr %q", rec.Name, err, stderr, want)
	}
}

// TestForgetCleansTheName: scterm forget's question and what it says it did
// name the device cleaned. Without a terminal, part of the name is refused
// with the whole name as stored, quoted, which is what to type; typed, it
// forgets the device without a question.
func TestForgetCleansTheName(t *testing.T) {
	withInteractive(t, true)
	pairedStore(t, dirtyPixel8)
	old := terminalInput
	t.Cleanup(func() { terminalInput = old })
	const question = "Forget Pixel ]0;owned 8? [y/N] "
	for _, tc := range []struct{ answer, said string }{
		{"n\n", "Kept Pixel ]0;owned 8.\n"},
		{"y\n", "Forgot Pixel ]0;owned 8. Its own list still names this computer until it is removed there.\n"},
	} {
		terminalInput = bufioReader(tc.answer)
		var err error
		var stderr string
		stdout := captureStdout(t, func() {
			stderr = captureStderr(t, func() { err = forgetCommand([]string{"pix"}) })
		})
		if err != nil || stdout != tc.said || stderr != question {
			t.Errorf("answer %q: %v, stdout %q, stderr %q; want stdout %q and stderr %q", tc.answer, err, stdout, stderr, tc.said, question)
		}
	}

	withInteractive(t, false)
	store := pairedStore(t, dirtyPixel8)
	want := `"pix" is only part of "Pixel\x1b]0;owned\a 8\u202e": give the whole name to forget it`
	if err := forgetCommand([]string{"pix"}); err == nil || err.Error() != want {
		t.Errorf("forget pix: %q\nwant %q", err, want)
	}
	var err error
	stdout := captureStdout(t, func() { err = forgetCommand([]string{dirtyPixel8}) })
	if want := "Forgot Pixel ]0;owned 8. Its own list still names this computer until it is removed there.\n"; err != nil || stdout != want {
		t.Errorf("forget by the whole name: %v, stdout %q; want %q", err, stdout, want)
	}
	if recs, _ := store.All(); len(recs) != 0 {
		t.Errorf("forget by the whole name kept the pairing")
	}
}

// TestNearbyFailureCleansWhatTheDeviceSaid: a failed nearby pairing names the
// device and may give its refusal in its own words; both reach the terminal
// cleaned. A refusal whose words clean to nothing says the device declined.
func TestNearbyFailureCleansWhatTheDeviceSaid(t *testing.T) {
	const name = "Pixel ]0;owned 8"
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&peer.RejectError{Code: peer.RejectDeclined, Message: "\x1b[2JDeclined\u2066 on\r\nPixel"}, "not paired: [2JDeclined on Pixel"},
		{&peer.RejectError{Code: peer.RejectDeclined, Message: "\x1b\u200b\x07"}, "not paired: " + name + " declined"},
		{&peer.RejectError{Code: peer.RejectDeclined}, "not paired: " + name + " declined"},
		{&peer.RejectError{Code: "no_invitation"}, name + " is no longer inviting; create a new invitation on it"},
		{&peer.RejectError{Code: "unavailable"}, name + " is already pairing with another device; try again in a minute"},
		{&peer.RejectError{Code: "bad_proof"}, name + " did not follow the pairing protocol, so nothing was paired"},
		{&peer.RejectError{Code: "teapot\u009b31m", Message: "short\x1b]0;x\x07 and\u00ad stout"}, "pairing with " + name + ": rejected (teapot 31m): short ]0;x and stout"},
		{io.ErrUnexpectedEOF, "pairing with " + name + ": unexpected EOF"},
		{peer.ErrPairingCancelled, "pairing cancelled"},
	} {
		got := nearbyFailure(dirtyPixel8, tc.err).Error()
		if got != tc.want || unclean(got) {
			t.Errorf("%q: %q\nwant %q", tc.err, got, tc.want)
		}
	}
}

// servePeerOnce listens on loopback as an scterm target with an identity of
// its own, and hands the first connection, once TLS has shown this computer's
// identity, to serve. It returns the port and the target's identity.
func servePeerOnce(t *testing.T, serve func(conn net.Conn, self, controller peer.Fingerprint)) (int, peer.Fingerprint) {
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
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tc := conn.(*tls.Conn)
		if tc.Handshake() != nil {
			return
		}
		serve(conn, id.Fingerprint, peer.FingerprintOf(tc.ConnectionState().PeerCertificates[0].RawSubjectPublicKeyInfo))
	}()
	return ln.Addr().(*net.TCPAddr).Port, id.Fingerprint
}

// TestPairNearbyCleansWhatTheDevicesSay: scterm pair names the devices that
// answer its search, which anyone on the network can name, and the device it
// pairs with, in its chooser, its questions, what it says it did and why it
// failed; each name reaches the terminal cleaned, and the pairing is stored
// with the name as the device gave it.
func TestPairNearbyCleansWhatTheDevicesSay(t *testing.T) {
	oldInput, oldSearch := terminalInput, invitationSearch
	t.Cleanup(func() { terminalInput, invitationSearch = oldInput, oldSearch })
	shownCode := regexp.MustCompile(`\n    [0-9]{3} [0-9]{3}\n`)
	const asked = "  1) Tab [2J A9 (127.0.0.1:%d)\n  2) Old\uFFFDphone (192.0.2.9:27300)\nPair with which device? [1-2] " +
		"\n    CODE\n\nCheck that Pixel ]0;owned 8 shows the same code, then accept there too.\nDoes it match? [y/N] "
	const waited = "Looking for devices showing an invitation on this network…\nWaiting for Pixel ]0;owned 8 to accept…\n"

	// run picks the first of the two devices the search finds: a target that
	// shows dirtyPixel8 beside the code and, once the code is confirmed, ends
	// with last. stderr comes back with the code as CODE.
	run := func(t *testing.T, last peer.Message) (home string, port int, target peer.Fingerprint, stdout, stderr string, err error) {
		t.Helper()
		home = t.TempDir()
		t.Setenv("SCTERM_HOME", home)
		port, target = servePeerOnce(t, func(conn net.Conn, self, controller peer.Fingerprint) {
			if m, err := peer.ReadFrame(conn); err != nil || m.Type != peer.TypePairNearby {
				return
			}
			nonce := make([]byte, peer.NonceSize)
			rand.Read(nonce)
			peer.WriteFrame(conn, &peer.Message{Type: peer.TypeCodeCommit, Commit: hex.EncodeToString(peer.Commitment(nonce, self, controller))})
			if m, err := peer.ReadFrame(conn); err != nil || m.Type != peer.TypeCodeNonce {
				return
			}
			peer.WriteFrame(conn, &peer.Message{Type: peer.TypeCodeReveal, Nonce: hex.EncodeToString(nonce), Device: &peer.DeviceInfo{Name: dirtyPixel8}})
			if m, err := peer.ReadFrame(conn); err == nil && m.Type == peer.TypeCodeConfirm {
				peer.WriteFrame(conn, &last)
			}
		})
		invitationSearch = func(context.Context, time.Duration) ([]peer.NearbyDevice, error) {
			return []peer.NearbyDevice{
				{Name: "Tab\x1b[2J\u202eA9", Host: "127.0.0.1", Port: port},
				{Name: "Old\x9bphone\u200b", Host: "192.0.2.9", Port: 27300},
			}, nil
		}
		terminalInput = bufioReader("1\ny\n")
		stdout = captureStdout(t, func() {
			stderr = captureStderr(t, func() { err = pairNearbyCommand() })
		})
		return home, port, target, stdout, shownCode.ReplaceAllString(stderr, "\n    CODE\n"), err
	}

	t.Run("paired", func(t *testing.T) {
		home, port, target, stdout, stderr, err := run(t, peer.Message{Type: peer.TypePaired, Device: &peer.DeviceInfo{Name: dirtyPixel9}, Grants: []string{peer.GrantView, peer.GrantControl}})
		local, idErr := peer.LoadIdentity(home)
		if err != nil || idErr != nil {
			t.Fatalf("pair: %v; this computer's identity: %v", err, idErr)
		}
		wantOut := waited + "Paired with Pixel 2J 9 (" + target.Short() + "); it allows this computer to view, control.\n" +
			"This computer is " + local.Fingerprint.Short() + `. Connect with: scterm --peer "Pixel\u009b2J\r 9\u200b"` + "\n"
		if wantErr := fmt.Sprintf(asked, port); stdout != wantOut || stderr != wantErr || unclean(stdout+stderr) {
			t.Errorf("stdout %q\nwant   %q\nstderr %q\nwant   %q", stdout, wantOut, stderr, wantErr)
		}
		if recs, err := peer.OpenStore(home).All(); err != nil || len(recs) != 1 || recs[0].Name != dirtyPixel9 {
			t.Errorf("stored %+v, %v; want the name as the device gave it", recs, err)
		}
	})
	t.Run("declined", func(t *testing.T) {
		home, port, _, stdout, stderr, err := run(t, peer.Message{Type: peer.TypeReject, Code: peer.RejectDeclined, Message: "Declined\x1b[8m on\u2066 Pixel\r"})
		if want := "not paired: Declined [8m on Pixel"; err == nil || err.Error() != want {
			t.Errorf("pair: %q\nwant %q", err, want)
		}
		if wantErr := fmt.Sprintf(asked, port); stdout != waited || stderr != wantErr || unclean(stdout+stderr) {
			t.Errorf("stdout %q\nwant   %q\nstderr %q\nwant   %q", stdout, waited, stderr, wantErr)
		}
		if recs, _ := peer.OpenStore(home).All(); len(recs) != 0 {
			t.Errorf("a declined pairing was stored: %+v", recs)
		}
	})
}

// TestPairByInvitationCleansWhatTheDeviceSays: scterm pair with an invitation
// names the device it paired with, or gives its refusal; both reach the
// terminal cleaned, and the pairing is stored with the name as the device
// gave it.
func TestPairByInvitationCleansWhatTheDeviceSays(t *testing.T) {
	secret := []byte("ten bytes!")
	// invite serves one pairing by invitation, answering it with what answer
	// makes of the two identities.
	invite := func(t *testing.T, answer func(self, controller peer.Fingerprint) peer.Message) (home string, port int, target peer.Fingerprint) {
		t.Helper()
		home = t.TempDir()
		t.Setenv("SCTERM_HOME", home)
		port, target = servePeerOnce(t, func(conn net.Conn, self, controller peer.Fingerprint) {
			if m, err := peer.ReadFrame(conn); err != nil || m.Type != peer.TypePair {
				return
			}
			reply := answer(self, controller)
			if peer.WriteFrame(conn, &reply) == nil {
				io.Copy(io.Discard, conn)
			}
		})
		return home, port, target
	}

	t.Run("paired", func(t *testing.T) {
		home, port, target := invite(t, func(self, controller peer.Fingerprint) peer.Message {
			proof := peer.PairingProof(secret, peer.RoleTarget, self, controller)
			return peer.Message{Type: peer.TypePaired, Proof: hex.EncodeToString(proof), Device: &peer.DeviceInfo{Name: dirtyPixel8}, Grants: []string{peer.GrantView}}
		})
		inv := peer.Invitation{Host: "127.0.0.1", Port: port, Secret: secret}
		var err error
		stdout := captureStdout(t, func() { err = pairCommand([]string{inv.ManualText()}) })
		local, idErr := peer.LoadIdentity(home)
		if err != nil || idErr != nil {
			t.Fatalf("pair: %v; this computer's identity: %v", err, idErr)
		}
		want := "Paired with Pixel ]0;owned 8 (" + target.Short() + "); it allows this computer to view.\n" +
			"This computer is " + local.Fingerprint.Short() + `. Connect with: scterm --peer "Pixel\x1b]0;owned\a 8\u202e"` + "\n"
		if stdout != want || unclean(stdout) {
			t.Errorf("stdout %q\nwant   %q", stdout, want)
		}
		if recs, err := peer.OpenStore(home).All(); err != nil || len(recs) != 1 || recs[0].Name != dirtyPixel8 {
			t.Errorf("stored %+v, %v; want the name as the device gave it", recs, err)
		}
	})
	t.Run("refused", func(t *testing.T) {
		home, port, _ := invite(t, func(peer.Fingerprint, peer.Fingerprint) peer.Message {
			return peer.Message{Type: peer.TypeReject, Code: "busy\x1b[5m", Message: "too\u202e many\x07 viewers"}
		})
		inv := peer.Invitation{Host: "127.0.0.1", Port: port, Secret: secret}
		err := pairCommand([]string{inv.ManualText()})
		if want := fmt.Sprintf("pairing with 127.0.0.1:%d: rejected (busy [5m): too many viewers", port); err == nil || err.Error() != want {
			t.Errorf("pair: %q\nwant %q", err, want)
		}
		if recs, _ := peer.OpenStore(home).All(); len(recs) != 0 {
			t.Errorf("a refused pairing was stored: %+v", recs)
		}
	})
}

// TestADBFailureCleansWhatADBSaid: when adb fails, the error carries what it
// wrote to stderr, which can come from the device; it reaches the terminal on
// one line, cleaned (here: adb's daemon lines, an OSC title sequence, a C1
// CSI, a right-to-left override, a zero-width space and a lone 0x9b byte).
// What adb says without them reads as it did, without its line break.
func TestADBFailureCleansWhatADBSaid(t *testing.T) {
	for _, tc := range []struct{ said, want string }{
		{`* daemon not running; starting now at tcp:5037\n* daemon started successfully\n` +
			`adb: error: \033]0;owned\007failed to copy\r\n\302\2332J \342\200\256remote\342\200\213 \233denied\n`,
			"adb push a b: exit status 1: * daemon not running; starting now at tcp:5037 * daemon started successfully " +
				"adb: error: ]0;owned failed to copy 2J remote \uFFFDdenied"},
		{`error: closed\n`, "adb push a b: exit status 1: error: closed"},
	} {
		dir := t.TempDir()
		script := "#!/bin/sh\nprintf '" + tc.said + "' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(dir, "adb"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		if _, err := newADB("R5CT").run("push", "a", "b"); err == nil || err.Error() != tc.want {
			t.Errorf("adb wrote %s: %q\nwant %q", tc.said, err, tc.want)
		}
	}
}
