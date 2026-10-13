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
		fmt.Fprintln(out, `No paired devices. On the device choose "Invite a device…", then run: scterm pair "HOST:PORT CODE"`)
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

func cleanPeerText(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value))
}
