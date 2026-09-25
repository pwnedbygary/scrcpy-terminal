package main

// Peer mode feeds the same TUI/web/window pipeline from an scterm target (the
// Android app) over the authenticated peer protocol instead of adb. The target
// sends scrcpy's own media and control framing, so only the transport differs:
// session.video/audio/control carry exactly what the adb tunnel's sockets
// would, minus the 64-byte device name, which the handshake carries instead.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"scterm/peer"
)

// peerHome holds this computer's identity and its paired devices.
func peerHome() (string, error) {
	if dir := os.Getenv("SCTERM_HOME"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "scterm"), nil
}

func openPeerClient() (*peer.Client, *peer.Store, error) {
	home, err := peerHome()
	if err != nil {
		return nil, nil, err
	}
	id, err := peer.LoadOrCreateIdentity(home)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: %w", err)
	}
	host, _ := os.Hostname()
	info := peer.ClientInfo{Name: strings.TrimSpace("scterm " + host), App: "scterm/" + version, Platform: runtime.GOOS}
	return &peer.Client{Identity: id, Info: info}, peer.OpenStore(home), nil
}

// runPeerCommand runs `scterm pair|peers|forget ...` and reports whether args
// named one of them.
func runPeerCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	var err error
	switch args[0] {
	case "pair":
		err = pairCommand(args[1:])
	case "peers":
		err = peersCommand()
	case "forget":
		err = forgetCommand(args[1:])
	default:
		return false
	}
	if err != nil {
		fatal(err)
	}
	return true
}

func pairCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`usage: scterm pair "HOST:PORT CODE" (from the device's invitation) or an scterm://pair link`)
	}
	inv, err := peer.ParseInvitation(strings.Join(args, " "))
	if err != nil {
		return err
	}
	client, store, err := openPeerClient()
	if err != nil {
		return err
	}
	res, err := client.Pair(inv, 0)
	if err != nil {
		return fmt.Errorf("pairing with %s: %w", inv.Address(), err)
	}
	name := res.Device.Name
	if name == "" {
		name = res.Fingerprint.Short()
	}
	rec := peer.Record{Fingerprint: res.Fingerprint.Hex(), Name: name, Host: inv.Host, Port: inv.Port, Grants: res.Grants, PairedAtMs: time.Now().UnixMilli()}
	if err := store.Put(rec); err != nil {
		return err
	}
	fmt.Printf("Paired with %s (%s); it allows this computer to %s.\n", name, res.Fingerprint.Short(), describeGrants(res.Grants))
	fmt.Printf("This computer is %s. Connect with: scterm --peer %q\n", client.Identity.Fingerprint.Short(), name)
	return nil
}

func peersCommand() error {
	client, store, err := openPeerClient()
	if err != nil {
		return err
	}
	recs, err := store.All()
	if err != nil {
		return err
	}
	fmt.Printf("This computer: %s\n", client.Identity.Fingerprint.Short())
	if len(recs) == 0 {
		fmt.Println(`No paired devices. On the device choose "Invite a device…", then run: scterm pair "HOST:PORT CODE"`)
		return nil
	}
	for _, r := range recs {
		fp, err := r.ID()
		if err != nil {
			continue
		}
		fmt.Printf("  %-24s %s  %-21s may %s\n", r.Name, fp.Short(), net.JoinHostPort(r.Host, strconv.Itoa(r.Port)), describeGrants(r.Grants))
	}
	return nil
}

func forgetCommand(args []string) error {
	_, store, err := openPeerClient()
	if err != nil {
		return err
	}
	rec, err := store.Find(strings.Join(args, " "))
	if err != nil {
		return err
	}
	if _, err := store.Remove(rec.Fingerprint); err != nil {
		return err
	}
	fmt.Printf("Forgot %s. Its own list still names this computer until it is removed there.\n", rec.Name)
	return nil
}

func describeGrants(grants []string) string {
	words := map[string]string{peer.GrantView: "view", peer.GrantAudio: "hear", peer.GrantControl: "control", peer.GrantClipboard: "share the clipboard"}
	var out []string
	for _, g := range grants {
		if w, ok := words[g]; ok {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return "do nothing"
	}
	return strings.Join(out, ", ")
}

// newPeerSession connects to a paired device, found by name or identity
// ("" picks the only paired device).
func newPeerSession(query string, cfg config) (*session, error) {
	client, store, err := openPeerClient()
	if err != nil {
		return nil, err
	}
	rec, err := store.Find(query)
	if err != nil {
		return nil, err
	}
	pin, err := rec.ID()
	if err != nil {
		return nil, err
	}
	req := peer.StreamRequest{
		Video:        cfg.video,
		Audio:        cfg.audio,
		Control:      cfg.control,
		MaxSize:      cfg.maxSize,
		MaxFPS:       cfg.maxFps,
		VideoBitRate: cfg.videoBitRate,
		VideoCodecs:  []string{"h264"},
		AudioCodecs:  audioCodecPreference(cfg.audioCodec),
	}
	ps, err := client.Connect(rec.Host, rec.Port, pin, req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s at %s: %w", rec.Name, net.JoinHostPort(rec.Host, strconv.Itoa(rec.Port)), err)
	}
	s := &session{peer: ps, video: ps.Video, audio: ps.Audio, deviceName: rec.Name}
	if d := ps.Welcome.Device; d != nil && d.Name != "" {
		s.deviceName = d.Name
	}
	canControl := false
	for _, g := range ps.Welcome.Grants {
		canControl = canControl || g == peer.GrantControl
	}
	if cfg.control && canControl {
		s.control = ps.Control()
	}
	name := s.deviceName
	ps.Start(peer.Events{
		Lease: func(state, holder string) {
			switch {
			case state == peer.LeaseHeld:
				fmt.Fprintf(stderrWriter(), "scterm: this computer has control of %s\n", name)
			case holder != "":
				fmt.Fprintf(stderrWriter(), "scterm: %s took control of %s; this computer is viewing (--takeover takes it back)\n", holder, name)
			}
		},
		Error: func(code, message, controlType string) {
			if code == "no_lease" {
				logOnce(fmt.Sprintf("scterm: input ignored: another device has control of %s (--takeover takes it)\n", name))
				return
			}
			fmt.Fprintf(stderrWriter(), "scterm: %s refused %s: %s\n", name, controlType, message)
		},
	})
	switch {
	case cfg.control && !canControl:
		logOnce(fmt.Sprintf("scterm: %s allows this computer to %s only\n", name, describeGrants(ps.Welcome.Grants)))
	case cfg.control && ps.Lease() != peer.LeaseHeld && cfg.takeover:
		ps.Takeover()
	case cfg.control && ps.Lease() != peer.LeaseHeld:
		logOnce(fmt.Sprintf("scterm: another device has control of %s; this computer is viewing (--takeover takes it)\n", name))
	}
	return s, nil
}

// audioCodecPreference asks for the codec chosen with --audio-codec first;
// the target picks what it can produce.
func audioCodecPreference(chosen string) []string {
	codecs := []string{"opus", "aac", "flac", "raw"}
	for i, c := range codecs {
		if c == chosen && i > 0 {
			return append([]string{c}, append(codecs[:i:i], codecs[i+1:]...)...)
		}
	}
	return codecs
}
