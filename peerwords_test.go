package main

import (
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
