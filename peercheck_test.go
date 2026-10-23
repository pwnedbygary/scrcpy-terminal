package main

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scterm/peer"
)

func checkFixture(t *testing.T, count int) (*peer.Client, *peer.Store, []peer.Record) {
	t.Helper()
	id, err := peer.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := &peer.Client{Identity: id}
	store := peer.OpenStore(t.TempDir())
	recs := make([]peer.Record, count)
	for i := range recs {
		recs[i] = peer.Record{Fingerprint: fmt.Sprintf("%064x", i+1), Name: fmt.Sprintf("Peer %d", i), Host: "saved", Port: 27300, Grants: []string{"view"}, PairedAtMs: 1}
		if err := store.Put(recs[i]); err != nil {
			t.Fatal(err)
		}
	}
	return client, store, recs
}

func TestPeersListingStaysOffline(t *testing.T) {
	c, s, recs := checkFixture(t, 1)
	var out bytes.Buffer
	discover := func(context.Context, time.Duration) ([]peer.NearbyDevice, error) {
		t.Fatal("offline listing attempted discovery")
		return nil, nil
	}
	probe := func(context.Context, string, int, peer.Fingerprint) (*peer.Message, error) {
		t.Fatal("offline listing attempted probe")
		return nil, nil
	}
	if err := printPeers(context.Background(), &out, c, s, recs, false, discover, probe); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Ready") || !strings.Contains(out.String(), "Peer 0") {
		t.Fatal(out.String())
	}
}

// TestPeersWithoutPairingsSaysHowToPair: with nothing paired, scterm peers
// names both ways to pair: scterm pair on its own, which finds a device
// inviting on the same network, and scterm pair with a typed invitation.
func TestPeersWithoutPairingsSaysHowToPair(t *testing.T) {
	c, s, _ := checkFixture(t, 0)
	var out bytes.Buffer
	if err := printPeers(context.Background(), &out, c, s, nil, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	alone := false // scterm pair without an invitation after it
	for rest, found := text, true; found; {
		_, rest, found = strings.Cut(rest, "scterm pair")
		alone = alone || found && !strings.HasPrefix(rest, ` "`)
	}
	if !alone || !strings.Contains(text, "network") || !strings.Contains(text, `scterm pair "HOST:PORT CODE"`) {
		t.Errorf("no pairings: %q; want scterm pair on its own (same network) and with a typed invitation", text)
	}
}

func TestPeersCheckPinsCandidatesUsesTwoWorkersAndPersistsVerifiedMetadata(t *testing.T) {
	c, s, recs := checkFixture(t, 8)
	var active, max atomic.Int32
	discover := func(context.Context, time.Duration) ([]peer.NearbyDevice, error) {
		return []peer.NearbyDevice{{Host: "new", Port: 27301, FingerprintHint: recs[0].Fingerprint}}, nil
	}
	probe := func(ctx context.Context, host string, port int, pin peer.Fingerprint) (*peer.Message, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := max.Load(); current > old && !max.CompareAndSwap(old, current); old = max.Load() {
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
		if pin.Hex() == recs[0].Fingerprint && host != "new" {
			t.Error("candidate not preferred")
		}
		yes := true
		return &peer.Message{Type: peer.TypeProbeResult, V: 1, Ready: &yes, Device: &peer.DeviceInfo{Name: "Verified"}, Caps: &peer.Capabilities{Video: true}, Grants: []string{"view", "control"}}, nil
	}
	var out bytes.Buffer
	if err := printPeers(context.Background(), &out, c, s, recs, true, discover, probe); err != nil {
		t.Fatal(err)
	}
	if max.Load() != 2 {
		t.Fatalf("concurrent workers %d", max.Load())
	}
	if strings.Count(out.String(), "· Ready") != 8 {
		t.Fatal(out.String())
	}
	saved, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].Host != "new" || saved[0].Port != 27301 || saved[0].Name != "Verified" || saved[0].LastSeenMs == 0 {
		t.Fatal(saved[0])
	}
}

func TestFailedCandidateDoesNotReplaceSavedAddressAndAuthenticatedRejectionWins(t *testing.T) {
	c, s, recs := checkFixture(t, 1)
	before := append([]peer.Record(nil), recs...)
	discover := func(context.Context, time.Duration) ([]peer.NearbyDevice, error) {
		return []peer.NearbyDevice{{Host: "spoof", Port: 27301, FingerprintHint: recs[0].Fingerprint}}, nil
	}
	probe := func(_ context.Context, host string, _ int, _ peer.Fingerprint) (*peer.Message, error) {
		if host == "spoof" {
			return nil, peer.ErrIdentityMismatch
		}
		return nil, &peer.RejectError{Code: "not_paired"}
	}
	var out bytes.Buffer
	if err := printPeers(context.Background(), &out, c, s, recs, true, discover, probe); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.All()
	if !reflect.DeepEqual(saved, before) {
		t.Fatal("failed probe rewrote record")
	}
	if !strings.Contains(out.String(), "Pairing no longer accepted") {
		t.Fatal(out.String())
	}
}

func TestPeersCheckUsesSavedAddressWhenDiscoveryFails(t *testing.T) {
	c, s, recs := checkFixture(t, 1)
	discover := func(context.Context, time.Duration) ([]peer.NearbyDevice, error) {
		return nil, fmt.Errorf("multicast unavailable")
	}
	called := false
	probe := func(ctx context.Context, host string, port int, _ peer.Fingerprint) (*peer.Message, error) {
		called = true
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Error("unbounded check")
		}
		if host != "saved" || port != 27300 {
			t.Error("lost saved address")
		}
		return nil, &peer.RejectError{Code: "bad_request"}
	}
	var out bytes.Buffer
	if err := printPeers(context.Background(), &out, c, s, recs, true, discover, probe); err != nil {
		t.Fatal(err)
	}
	if !called || !strings.Contains(out.String(), "Status unavailable") {
		t.Fatal(out.String())
	}
}
