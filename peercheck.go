package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"scterm/peer"
)

type peerProbe func(context.Context, string, int, peer.Fingerprint) (*peer.Message, error)
type peerDiscovery func(context.Context, time.Duration) ([]peer.NearbyDevice, error)

// Plain listing is offline; opt-in checks use bounded pinned probes and stable output ordering.
func printPeers(ctx context.Context, out io.Writer, client *peer.Client, store *peer.Store, recs []peer.Record, check bool, discover peerDiscovery, probe peerProbe) error {
	recs = append([]peer.Record(nil), recs...)
	fmt.Fprintf(out, "This computer: %s\n", client.Identity.Fingerprint.Short())
	if len(recs) == 0 {
		fmt.Fprintln(out, `No paired devices. On the device choose "Invite a device…", then run scterm pair on the same network, or type its invitation: scterm pair "HOST:PORT CODE"`)
		return nil
	}
	labels := make([]string, len(recs))
	if check {
		candidates, _ := discover(ctx, time.Second) // saved addresses remain usable when multicast fails
		work := make(chan int)
		var wg sync.WaitGroup
		var storeMu sync.Mutex
		var storeErr error
		for worker := 0; worker < 2; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range work {
					rec := recs[i]
					fp, err := rec.ID()
					if err != nil {
						labels[i] = "Invalid paired identity"
						continue
					}
					deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
					addresses := make([]peer.NearbyDevice, 0, 3)
					for _, candidate := range candidates {
						if candidate.FingerprintHint == rec.Fingerprint {
							addresses = append(addresses, candidate)
						}
					}
					saved := peer.NearbyDevice{Host: rec.Host, Port: rec.Port}
					duplicate := false
					for _, a := range addresses {
						if a.Host == saved.Host && a.Port == saved.Port {
							duplicate = true
						}
					}
					if !duplicate && saved.Host != "" && saved.Port > 0 {
						addresses = append(addresses, saved)
					}
					var result *peer.Message
					var accepted peer.NearbyDevice
					var pinFailure error
					err = fmt.Errorf("no serving address")
					for index, address := range addresses {
						attempt := deadline
						endAttempt := func() {}
						if index < len(addresses)-1 {
							attempt, endAttempt = context.WithTimeout(deadline, 2*time.Second)
						}
						result, err = probe(attempt, address.Host, address.Port, fp)
						endAttempt()
						if err == nil {
							accepted = address
							break
						}
						if errors.Is(err, peer.ErrIdentityMismatch) {
							pinFailure = err
						}
						var rejected *peer.RejectError
						if errors.As(err, &rejected) {
							break
						}
						if deadline.Err() != nil {
							break
						}
					}
					cancel()
					var rejected *peer.RejectError
					if result == nil && pinFailure != nil && !errors.As(err, &rejected) {
						err = pinFailure
					}
					labels[i] = cleanPeerText(peer.ProbeLabel(result, err))
					if err == nil {
						now := time.Now().UnixMilli()
						rec.Name = cleanPeerText(result.Device.Name)
						if rec.Name == "" {
							rec.Name = recs[i].Name
						}
						rec.Host, rec.Port, rec.Grants = accepted.Host, accepted.Port, result.Grants
						if now-rec.LastSeenMs >= 300000 {
							rec.LastSeenMs = now
						}
						recs[i] = rec
						if err := store.UpdateExisting(rec.Fingerprint, func(current peer.Record) peer.Record {
							if current.Host != saved.Host || current.Port != saved.Port || current.LastSeenMs > now {
								return current
							}
							return rec
						}); err != nil {
							storeMu.Lock()
							storeErr = err
							storeMu.Unlock()
						}
					}
				}
			}()
		}
		for i := range recs {
			work <- i
		}
		close(work)
		wg.Wait()
		if storeErr != nil {
			return fmt.Errorf("recording peer status: %w", storeErr)
		}
	}
	for i, rec := range recs {
		fp, err := rec.ID()
		if err != nil {
			continue
		}
		fmt.Fprintf(out, "  %-24s %s  %-21s may %s", cleanPeerText(rec.Name), fp.Short(), net.JoinHostPort(rec.Host, strconv.Itoa(rec.Port)), describeGrants(rec.Grants))
		if check {
			fmt.Fprintf(out, " · %s", labels[i])
		}
		fmt.Fprintln(out)
	}
	return nil
}

// presenceSearch finds serving devices, as scterm peers --check does; a
// variable so tests stay off the network.
var presenceSearch peerDiscovery = peer.DiscoverPresence

// connectPeer connects to a paired device at its saved address. If that
// fails, short of the device refusing, it may have moved: a presence search
// can find it elsewhere, which is tried once, and saved only after pinned TLS
// has proved it is the paired device. connect must ask for req, which a
// refusal is worded from.
func connectPeer(store *peer.Store, rec peer.Record, req peer.StreamRequest, connect func(host string, port int) (*peer.Session, error)) (*peer.Session, error) {
	s, err := connect(rec.Host, rec.Port)
	if err == nil {
		return s, nil
	}
	saved := net.JoinHostPort(rec.Host, strconv.Itoa(rec.Port))
	var rejected *peer.RejectError
	if errors.As(err, &rejected) {
		return nil, peerConnectFailure(rec.Name, saved, req, err)
	}
	candidates, _ := presenceSearch(context.Background(), time.Second)
	for _, c := range candidates {
		if c.FingerprintHint != rec.Fingerprint || (c.Host == rec.Host && c.Port == rec.Port) {
			continue
		}
		moved, moveErr := connect(c.Host, c.Port)
		if errors.As(moveErr, &rejected) {
			return nil, peerConnectFailure(rec.Name, net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), req, moveErr)
		}
		if moveErr != nil {
			break
		}
		_ = store.UpdateExisting(rec.Fingerprint, func(current peer.Record) peer.Record {
			if current.Host == rec.Host && current.Port == rec.Port {
				current.Host, current.Port = c.Host, c.Port
			}
			return current
		})
		return moved, nil
	}
	return nil, peerConnectFailure(rec.Name, saved, req, err)
}

// peerConnectFailure words a failed --peer connection: what failed, where, and
// what to try. The name is the one the device gave when it paired; it and the
// address reach the terminal without control characters.
func peerConnectFailure(name, addr string, req peer.StreamRequest, err error) error {
	const tryReach = "check that it is serving and on this network, or run scterm peers --check"
	name, addr = cleanPeerText(name), cleanPeerText(addr)
	var rejected *peer.RejectError
	var timeout net.Error
	var dial *net.OpError
	switch {
	case errors.As(err, &rejected):
		reason, next := refusal(name, req, rejected)
		return fmt.Errorf("%s at %s refused the connection: %s; %s", name, addr, reason, next)
	case errors.As(err, &timeout) && timeout.Timeout():
		return fmt.Errorf("could not reach %s at %s (no answer): %s", name, addr, tryReach)
	case errors.As(err, &dial) && dial.Op == "dial":
		why := dial.Err.Error()
		var errno syscall.Errno
		if errors.As(dial.Err, &errno) {
			why = errno.Error()
		}
		return fmt.Errorf("could not reach %s at %s (%s): %s", name, addr, cleanPeerText(why), tryReach)
	default:
		return fmt.Errorf("connecting to %s at %s: %w", name, addr, err)
	}
}

// refusal says why a paired device refused to connect, and what to try,
// mostly in the words scterm peers --check and the Android app use for the
// same code. forbidden means the device allows none of req, so it names what
// was asked; bad_token, that the session the device had just opened ended or
// expired before the streams joined it.
func refusal(name string, req peer.StreamRequest, rejected *peer.RejectError) (reason, next string) {
	said := cleanPeerText(rejected.Message)
	next = "run scterm peers --check"
	switch rejected.Code {
	case "not_paired":
		return "pairing no longer accepted", "pair again with scterm pair"
	case "forbidden":
		next = "on " + name + ", allow that"
		if !req.Video || !req.Audio || !req.Control {
			next += ", or ask for what it does allow (scterm peers --check lists it)"
		}
		return "it does not allow this computer to " + asked(req) + " it", next
	case "busy":
		return "it already has the maximum number of viewers", "close one of them and try again"
	case "unavailable":
		if said == "" {
			return "serving is not ready", "check " + name + ", then try again"
		}
		return "it is not ready (" + said + ")", "check " + name + ", then try again"
	case "version", "bad_request", "unsupported":
		return "it runs an incompatible version of scterm", "update scterm on " + name + " and on this computer"
	case "bad_token":
		next = "try again"
	}
	if said == "" {
		said = cleanPeerText(rejected.Code)
	}
	return said, next
}

// asked says what req asks of a device in describeGrants' words, the last
// two joined by "or": "view, hear or control".
func asked(req peer.StreamRequest) string {
	var grants []string
	if req.Video {
		grants = append(grants, peer.GrantView)
	}
	if req.Audio {
		grants = append(grants, peer.GrantAudio)
	}
	if req.Control {
		grants = append(grants, peer.GrantControl)
	}
	words := describeGrants(grants)
	if i := strings.LastIndex(words, ", "); i >= 0 {
		words = words[:i] + " or " + words[i+len(", "):]
	}
	return words
}

func cleanPeerText(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value))
}
