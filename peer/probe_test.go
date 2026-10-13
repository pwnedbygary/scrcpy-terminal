package peer

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

func TestProbeSharedFixturesAndLabels(t *testing.T) {
	data, err := os.ReadFile("../protocol/fixtures/probe.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]json.RawMessage
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for name, raw := range fixtures {
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if name == "ready" && ProbeLabel(&msg, nil) != "Ready" {
			t.Fatal(msg)
		}
		if name == "blocked" && ProbeLabel(&msg, nil) != "Start the helper on RP6" {
			t.Fatal(msg)
		}
	}
	if ProbeLabel(nil, &RejectError{Code: "bad_request"}) != "Status unavailable—update the peer" {
		t.Fatal("old peer")
	}
}

func TestProbeTLSValidationAndCancellation(t *testing.T) {
	for _, scenario := range []string{"ready", "malformed", "older", "cancel", "wrong_pin"} {
		t.Run(scenario, func(t *testing.T) {
			id, err := LoadOrCreateIdentity(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{id.Certificate}, ClientAuth: tls.RequireAnyClientCert})
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				request, err := ReadFrame(conn)
				if err != nil {
					return
				}
				if request.Type != TypeProbe || request.Channel != "" {
					t.Errorf("unexpected request: %+v", request)
					return
				}
				yes := true
				switch scenario {
				case "ready":
					WriteFrame(conn, &Message{Type: TypeProbeResult, V: 1, Ready: &yes, Device: &DeviceInfo{Name: "RP6"}, Grants: []string{"view"}, Caps: &Capabilities{Video: true}})
				case "older":
					WriteFrame(conn, &Message{Type: TypeReject, Code: "bad_request"})
				case "malformed":
					WriteFrame(conn, &Message{Type: TypeProbeResult, V: 1})
				case "cancel":
					ReadFrame(conn)
				}
			}()
			c := testClient(t)
			pin := id.Fingerprint
			if scenario == "wrong_pin" {
				pin = c.Identity.Fingerprint
			}
			ctx := context.Background()
			if scenario == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			result, err := c.Probe(ctx, "127.0.0.1", ln.Addr().(*net.TCPAddr).Port, pin)
			if scenario == "ready" {
				if err != nil || ProbeLabel(result, nil) != "Ready" {
					t.Fatalf("%+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("expected failure")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("probe leaked connection")
			}
		})
	}
}
