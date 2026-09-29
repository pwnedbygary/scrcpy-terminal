package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"scterm/peer"
)

// nearbySearch is how long `scterm pair` keeps looking for an inviting device.
const nearbySearch = 30 * time.Second

// pairNearbyCommand pairs with a device inviting on this network: both
// screens show a six-digit code, and both users confirm it.
func pairNearbyCommand() error {
	client, store, err := openPeerClient()
	if err != nil {
		return err
	}
	fmt.Println("Looking for devices showing an invitation on this network…")
	devices, err := findNearby()
	if err != nil {
		return err
	}
	d := devices[0]
	if len(devices) > 1 {
		labels := make([]string, len(devices))
		for i, n := range devices {
			labels[i] = fmt.Sprintf("%s (%s)", n.Name, net.JoinHostPort(n.Host, strconv.Itoa(n.Port)))
		}
		i, err := choose("Pair with which device?", labels)
		if err != nil {
			return err
		}
		d = devices[i]
	}
	p, err := client.StartNearbyPairing(d.Host, d.Port, 0)
	if err != nil {
		return nearbyFailure(d.Name, err)
	}
	name := p.Device.Name
	if name == "" {
		name = d.Name
	}
	fmt.Printf("\n    %s\n\nCheck that %s shows the same code, then accept there too.\n", peer.DisplayCode(p.Code), name)
	answer := make(chan string, 1)
	go func() {
		a, _ := ask("Does it match? [y/N] ")
		answer <- a
	}()
	select {
	case r := <-p.Result():
		// The device answered first: its user declined, or it went away.
		fmt.Println()
		return nearbyFailure(name, r.Err)
	case a := <-answer:
		if a = strings.ToLower(a); a != "y" && a != "yes" {
			p.Cancel()
			return errors.New("not paired: the code was not confirmed")
		}
	}
	p.Confirm() // if this fails, the result says why
	fmt.Printf("Waiting for %s to accept…\n", name)
	r := <-p.Result()
	if r.Err != nil {
		return nearbyFailure(name, r.Err)
	}
	if r.Pair.Device.Name != "" {
		name = r.Pair.Device.Name
	}
	rec := peer.Record{Fingerprint: r.Pair.Fingerprint.Hex(), Name: name, Host: d.Host, Port: d.Port, Grants: r.Pair.Grants, PairedAtMs: time.Now().UnixMilli()}
	if err := store.Put(rec); err != nil {
		return err
	}
	reportPaired(client, name, r.Pair.Fingerprint, r.Pair.Grants)
	return nil
}

// findNearby waits up to nearbySearch for at least one inviting device.
func findNearby() ([]peer.NearbyDevice, error) {
	deadline := time.Now().Add(nearbySearch)
	for hinted := false; ; {
		devices, err := peer.DiscoverNearby(context.Background(), 3*time.Second)
		if err != nil || len(devices) > 0 {
			return devices, err
		}
		if time.Now().After(deadline) {
			return nil, errors.New(`no device is inviting on this network. On the device, tap "Start serving", then "Invite a device…", and run scterm pair again; or type its invitation: scterm pair "HOST:PORT CODE"`)
		}
		if !hinted {
			fmt.Println(`None yet. On the device, tap "Start serving", then "Invite a device…". Still looking…`)
			hinted = true
		}
	}
}

// nearbyFailure words a failed nearby pairing for the person at this terminal.
func nearbyFailure(name string, err error) error {
	var rej *peer.RejectError
	switch {
	case errors.Is(err, peer.ErrPairingCancelled):
		return errors.New("pairing cancelled")
	case errors.As(err, &rej) && rej.Code == peer.RejectDeclined && rej.Message != "":
		return fmt.Errorf("not paired: %s", rej.Message)
	case errors.As(err, &rej) && rej.Code == peer.RejectDeclined:
		return fmt.Errorf("not paired: %s declined", name)
	case errors.As(err, &rej) && rej.Code == "no_invitation":
		return fmt.Errorf("%s is no longer inviting; create a new invitation on it", name)
	case errors.As(err, &rej) && rej.Code == "unavailable":
		return fmt.Errorf("%s is already pairing with another device; try again in a minute", name)
	case errors.As(err, &rej) && rej.Code == "bad_proof":
		return fmt.Errorf("%s did not follow the pairing protocol, so nothing was paired", name)
	default:
		return fmt.Errorf("pairing with %s: %w", name, err)
	}
}

// pickPeer finds the paired device a --peer query means, asking which one
// when several match and someone is at the terminal to answer.
func pickPeer(store *peer.Store, query string) (peer.Record, error) {
	hits, err := store.Matches(query)
	if err != nil {
		return peer.Record{}, err
	}
	if len(hits) > 1 && interactive() {
		labels := make([]string, len(hits))
		for i, r := range hits {
			labels[i] = fmt.Sprintf("%s (%s)", r.Name, net.JoinHostPort(r.Host, strconv.Itoa(r.Port)))
		}
		i, err := choose("Connect to which device?", labels)
		if err != nil {
			return peer.Record{}, err
		}
		return hits[i], nil
	}
	return store.Find(query) // the one match, or why there is none
}

var terminalInput = bufio.NewReader(os.Stdin)

// ask shows a prompt and reads one line of the answer.
func ask(prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := terminalInput.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// choose lists labelled options, numbered from 1, and returns the index picked.
func choose(question string, labels []string) (int, error) {
	for i, l := range labels {
		fmt.Printf("  %d) %s\n", i+1, l)
	}
	for {
		a, err := ask(fmt.Sprintf("%s [1-%d] ", question, len(labels)))
		if err != nil {
			return 0, err
		}
		if n, err := strconv.Atoi(a); err == nil && n >= 1 && n <= len(labels) {
			return n - 1, nil
		}
	}
}

// interactive reports whether stdin is a terminal someone can answer from.
func interactive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
