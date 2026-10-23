package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"unicode"

	"scterm/peer"
)

// TestPeerConnectFailureWords covers the failures a loopback target cannot
// produce quickly: a dial or handshake that times out, and anything else,
// which keeps its cause.
func TestPeerConnectFailureWords(t *testing.T) {
	timeout := &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
	for _, err := range []error{timeout, os.ErrDeadlineExceeded} {
		got := peerConnectFailure("Pixel 8", "192.0.2.7:27300", controlOnlyRequest, err).Error()
		if want := "could not reach Pixel 8 at 192.0.2.7:27300 (no answer): check that it is serving and on this network, or run scterm peers --check"; got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
	err := peerConnectFailure("Pixel 8", "192.0.2.7:27300", controlOnlyRequest, io.ErrUnexpectedEOF)
	if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.HasPrefix(err.Error(), "connecting to Pixel 8 at 192.0.2.7:27300: ") {
		t.Errorf("other failure: %v", err)
	}
}

var controlOnlyRequest = peer.StreamRequest{Control: true}

// TestPeerConnectFailureIsClean: whatever failed, the device's name and the
// address reach the terminal without control characters, including where a
// failed lookup repeats the address.
func TestPeerConnectFailureIsClean(t *testing.T) {
	const name, host = "Pixel 8\x1b]0;x\x07", "pixel\x1b[2J.lan"
	lookup := &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
	for _, err := range []error{
		&peer.RejectError{Code: "unavailable"},
		&peer.RejectError{Code: "forbidden", Message: "nothing requested is granted to this device"},
		os.ErrDeadlineExceeded,
		lookup,
		io.ErrUnexpectedEOF,
	} {
		got := peerConnectFailure(name, host+":27300", controlOnlyRequest, err).Error()
		if strings.ContainsFunc(got, unicode.IsControl) || !strings.Contains(got, "Pixel 8]0;x at pixel[2J.lan:27300") {
			t.Errorf("%q: %q", err, got)
		}
	}
}

// TestPeerTextLosesFormatCharacters: what scterm peers, a failed --peer and
// scterm doctor print of a device's text loses its format characters as it
// loses control characters (a right-to-left override, an isolate, a
// zero-width space, a soft hyphen, a byte order mark, which can reorder a
// row or hide part of it), and the spaces at either end.
func TestPeerTextLosesFormatCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"Pixel 8\u202e": "Pixel 8",
		" \u2066Pixel\u2069 8\u200b\u00ad\ufeff\x1b[2J ": "Pixel 8[2J",
		"\u202e \u200b": "",
	} {
		if got := cleanPeerText(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}

	const name = "Pixel\u202e 8\u2066"
	fp := peer.FingerprintOf([]byte(name))
	recs := []peer.Record{{Fingerprint: fp.Hex(), Name: name, Host: "192.0.2.1", Port: 27300, Grants: []string{peer.GrantView}}}
	client := &peer.Client{Identity: &peer.Identity{Fingerprint: peer.FingerprintOf([]byte("this computer"))}}
	var listed strings.Builder
	if err := printPeers(context.Background(), &listed, client, nil, recs, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	want := "This computer: " + client.Identity.Fingerprint.Short() + "\n" +
		"  Pixel 8                  " + fp.Short() + "  192.0.2.1:27300       may view\n"
	if listed.String() != want {
		t.Errorf("scterm peers: %q\nwant %q", listed.String(), want)
	}
	failed := peerConnectFailure(name, "pixel\u202e.lan:27300", controlOnlyRequest, os.ErrDeadlineExceeded).Error()
	if want := "could not reach Pixel 8 at pixel.lan:27300 (no answer): "; !strings.HasPrefix(failed, want) {
		t.Errorf("--peer: %q\nwant it to start %q", failed, want)
	}
}
