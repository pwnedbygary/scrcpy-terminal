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

// TestSeveralMatchesListNamesToTypeBack: when a --peer query, or scterm
// forget's, means several paired devices, the error lists them as a pasted
// command names them: a plain name in single quotes, else the identity. So
// each can be typed back as it is shown (cleaned, a name with two spaces
// together would read as another), and no other text of the devices' is
// shown. A whole name as stored still picks its device.
func TestSeveralMatchesListNamesToTypeBack(t *testing.T) {
	withInteractive(t, false)
	home := t.TempDir()
	t.Setenv("SCTERM_HOME", home)
	store := peer.OpenStore(home)
	names := []string{"Pixel 8", "Pixel  9", dirtyPixel8}
	for _, name := range names {
		rec := peer.Record{Fingerprint: peer.FingerprintOf([]byte(name)).Hex(), Name: name, Host: "192.0.2.1", Port: 27300, Grants: []string{"view"}, PairedAtMs: 1}
		if err := store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	list := "'Pixel 8', " + peer.FingerprintOf([]byte("Pixel  9")).Short() + ", " + peer.FingerprintOf([]byte(dirtyPixel8)).Short()
	for _, tc := range []struct{ query, want string }{
		{"", "3 devices are paired (" + list + "); name one"},
		{"pixel", `"pixel" matches ` + list + "; give more of the name"},
	} {
		if _, err := pickPeer(store, tc.query); err == nil || err.Error() != tc.want {
			t.Errorf("--peer=%s: %q\nwant %q", tc.query, err, tc.want)
		}
	}
	want := `"pix" matches ` + list + "; give more of the name"
	err := forgetCommand([]string{"pix"})
	if err == nil || err.Error() != want {
		t.Errorf("forget pix: %q\nwant %q", err, want)
	}
	shown, _ := strings.CutPrefix(fmt.Sprint(err), `"pix" matches `)
	shown, _ = strings.CutSuffix(shown, "; give more of the name")
	for i, arg := range strings.Split(shown, ", ") {
		typed := strings.TrimSuffix(strings.TrimPrefix(arg, "'"), "'")
		if rec, err := pickPeer(store, typed); i >= len(names) || err != nil || rec.Name != names[i] {
			t.Errorf("%s, typed back as --peer %q, picks %q, %v; want the device listed there", arg, typed, rec.Name, err)
		}
	}
	for _, name := range names {
		if rec, err := pickPeer(store, name); err != nil || rec.Name != name {
			t.Errorf("--peer=%q: %q, %v; want the device of that name", name, rec.Name, err)
		}
	}
	if recs, err := store.All(); err != nil || len(recs) != 3 {
		t.Errorf("%d pairings left (%v), want all 3", len(recs), err)
	}
}

// TestPeerChooserEscapesTheNames: the --peer chooser lists the devices a query
// means as data, each by its identity and its name in quotes, with escapes
// for what would not show as written; the answer picks the device as stored.
func TestPeerChooserEscapesTheNames(t *testing.T) {
	withInteractive(t, true)
	store := pairedStore(t, dirtyPixel8, dirtyPixel9)
	old := terminalInput
	t.Cleanup(func() { terminalInput = old })
	terminalInput = bufioReader("2\n")
	var rec peer.Record
	var err error
	stderr := captureStderr(t, func() { rec, err = pickPeer(store, "pixel") })
	want := `  1) 0000-0000-0000-0000, named "Pixel\x1b]0;owned\a 8\u202e" (192.0.2.1:27300)` + "\n" +
		`  2) 0000-0000-0000-0000, named "Pixel\u009b2J\r 9\u200b" (192.0.2.1:27300)` + "\n" +
		"Connect to which device? [1-2] "
	if err != nil || rec.Name != dirtyPixel9 || stderr != want {
		t.Errorf("picked %q, %v; stderr %q\nwant the second device and stderr %q", rec.Name, err, stderr, want)
	}
}

// TestForgetEscapesTheName: scterm forget shows the device on a line of its
// own, as data: its identity, and its name in quotes, with escapes for what
// would not show as written. Its question and what it says it did name the
// device by its identity. Without a terminal, part of the name is refused
// with a command that forgets the device, which names it by its identity, as
// the name is not plain text, and no other text of the device's; the whole
// name as stored, typed, forgets the device without a question.
func TestForgetEscapesTheName(t *testing.T) {
	withInteractive(t, true)
	pairedStore(t, dirtyPixel8)
	old := terminalInput
	t.Cleanup(func() { terminalInput = old })
	const device = `The device is 0000-0000-0000-0000, named "Pixel\x1b]0;owned\a 8\u202e".` + "\n"
	const question = device + "Forget 0000-0000-0000-0000? [y/N] "
	for _, tc := range []struct{ answer, said string }{
		{"n\n", "Kept 0000-0000-0000-0000.\n"},
		{"y\n", "Forgot 0000-0000-0000-0000. Its own list still names this computer until it is removed there.\n"},
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
	want := `"pix" only partly matches a paired device: to forget it, run scterm forget 0000-0000-0000-0000`
	if err := forgetCommand([]string{"pix"}); err == nil || err.Error() != want {
		t.Errorf("forget pix: %q\nwant %q", err, want)
	}
	var err error
	stdout := captureStdout(t, func() { err = forgetCommand([]string{dirtyPixel8}) })
	if want := device + "Forgot 0000-0000-0000-0000. Its own list still names this computer until it is removed there.\n"; err != nil || stdout != want {
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
// pairs with, in its chooser, its question, what it says it did and why it
// failed. Before the pairing's command, each name is shown in quotes, with
// escapes for what would not show as written, cut at 32 cells, and the line
// just before the command has none; why it failed is cleaned. The pairing is
// stored with the name as the device gave it, and the command to connect
// names the device by its identity, as the name is not plain text.
func TestPairNearbyCleansWhatTheDevicesSay(t *testing.T) {
	oldInput, oldSearch := terminalInput, invitationSearch
	t.Cleanup(func() { terminalInput, invitationSearch = oldInput, oldSearch })
	shownCode := regexp.MustCompile(`\n    [0-9]{3} [0-9]{3}\n`)
	// The second device's name and the one the target shows go on past 32
	// cells, to a fake command: each is cut at 32, after 14 and 5 z's.
	long := strings.Repeat("z", 70) + "Paired. Connect with: scterm --peer 'x'"
	const asked = `  1) "Tab\x1b[2J\u202eA9" (127.0.0.1:%d)` + "\n" + `  2) "Old\x9bphone\u200bzzzzzzzzzzzzzz"… (192.0.2.9:27300)` + "\nPair with which device? [1-2] " +
		"\n    CODE\n\n" + `Check that "Pixel\x1b]0;owned\a 8\u202ezzzzz"… shows the same code, then accept there too.` + "\nDoes it match? [y/N] "
	const waited = "Looking for devices showing an invitation on this network…\nWaiting for the device to accept…\n"

	// run picks the first of the two devices the search finds: a target that
	// shows dirtyPixel8, then long, beside the code and, once the code is
	// confirmed, ends with last. stderr comes back with the code as CODE.
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
			peer.WriteFrame(conn, &peer.Message{Type: peer.TypeCodeReveal, Nonce: hex.EncodeToString(nonce), Device: &peer.DeviceInfo{Name: dirtyPixel8 + long}})
			if m, err := peer.ReadFrame(conn); err == nil && m.Type == peer.TypeCodeConfirm {
				peer.WriteFrame(conn, &last)
			}
		})
		invitationSearch = func(context.Context, time.Duration) ([]peer.NearbyDevice, error) {
			return []peer.NearbyDevice{
				{Name: "Tab\x1b[2J\u202eA9", Host: "127.0.0.1", Port: port},
				{Name: "Old\x9bphone\u200b" + long, Host: "192.0.2.9", Port: 27300},
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
		wantOut := waited + "Paired. Connect with: scterm --peer " + target.Short() + "\n" +
			"The device is " + target.Short() + `, named "Pixel\u009b2J\r 9\u200b".` + "\n" +
			"It allows this computer (" + local.Fingerprint.Short() + ") to view, control.\n"
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
// names the device it paired with, after the command to connect, in quotes,
// with escapes for what would not show as written; or gives its refusal,
// cleaned. The pairing is stored with the name as the device gave it, and the
// command names the device by its identity, as the name is not plain text.
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
		want := "Paired. Connect with: scterm --peer " + target.Short() + "\n" +
			"The device is " + target.Short() + `, named "Pixel\x1b]0;owned\a 8\u202e".` + "\n" +
			"It allows this computer (" + local.Fingerprint.Short() + ") to view.\n"
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

// TestPickDeviceCleansTheSerials: a device chooses its own serial, which adb
// devices lists as it is. When scterm cannot pick a device, what it says of
// them reaches the terminal cleaned (here: an OSC title sequence, a BEL, a
// right-to-left override and a lone 0x9b byte; and the second word of a
// serial with a space in it, which the listing reads as the state).
func TestPickDeviceCleansTheSerials(t *testing.T) {
	const header = "List of devices attached\n"
	const serial = "R5\x1b]0;owned\x07CT\u202e\x9b"
	for _, tc := range []struct{ list, want string }{
		{header + serial + "\tdevice\nB\tdevice\n", "2 devices are connected (R5 ]0;owned CT \uFFFD, B): choose one with -s SERIAL"},
		{header + serial + "\tunauthorized\n", `R5 ]0;owned CT ` + "\uFFFD" + ` has not allowed USB debugging from this computer: unlock it, accept the "Allow USB debugging?" prompt, and run scterm again`},
		{header + serial + "\toffline\n", "R5 ]0;owned CT \uFFFD is offline: unplug and replug it, or restart adb with 'adb kill-server'"},
		{header + "R5CT \x1b[2J\u202e\tdevice\n", "R5CT is in [2J mode, not ready for scterm: start Android on it normally"},
	} {
		picked, err := pickDevice(tc.list)
		if picked != "" || err == nil || err.Error() != tc.want || unclean(err.Error()) {
			t.Errorf("%q: %q, %v\nwant %q", tc.list, picked, err, tc.want)
		}
	}
}
