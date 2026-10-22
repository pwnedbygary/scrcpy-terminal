package main

// Peer mode feeds the same TUI/web/window pipeline from an scterm target (the
// Android app) over the authenticated peer protocol instead of adb. The target
// sends scrcpy's own media and control framing, so only the transport differs:
// session.video/audio/control carry exactly what the adb tunnel's sockets
// would, minus the 64-byte device name, which the handshake carries instead.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	if help, ok := peerCommandHelp[args[0]]; ok && len(args) > 1 && isHelpArg(args[1]) {
		fmt.Print(help)
		return true
	}
	var err error
	switch args[0] {
	case "pair":
		err = pairCommand(args[1:])
	case "peers":
		err = peersCommand(args[1:]...)
	case "forget":
		err = forgetCommand(args[1:])
	default:
		return false
	}
	var usage usageErr
	if errors.As(err, &usage) {
		usageError(string(usage))
	}
	if err != nil {
		fatal(err)
	}
	return true
}

var peerCommandHelp = map[string]string{
	"pair": `usage: scterm pair
       scterm pair "HOST:PORT CODE"

Without an invitation, scterm finds a device inviting on this network and
pairs once both screens show the same six-digit code and it is accepted on
both. With the invitation an scterm device shows, it pairs from anywhere the
device is reachable.
`,
	"peers": `usage: scterm peers [--check]

Lists the paired devices and what each allows this computer to do. --check
also connects to each one, authenticated, without starting a stream, and
says whether it is ready.
`,
	"forget": `usage: scterm forget NAME

Forgets a paired device. NAME is its whole name, in any case, or its
identity; anything less is confirmed first, and refused when there is no
terminal to ask on.
`,
}

// usageErr is a command run the wrong way; it is reported through usageError,
// as a bad flag is.
type usageErr string

func (e usageErr) Error() string { return string(e) }

func pairCommand(args []string) error {
	if len(args) == 0 {
		return pairNearbyCommand()
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
		return fmt.Errorf("pairing with %s: %s", inv.Address(), cleanText(err.Error())) // a refusal carries the device's words
	}
	name := res.Device.Name
	if name == "" {
		name = res.Fingerprint.Short()
	}
	rec := peer.Record{Fingerprint: res.Fingerprint.Hex(), Name: name, Host: inv.Host, Port: inv.Port, Grants: res.Grants, PairedAtMs: time.Now().UnixMilli()}
	if err := store.Put(rec); err != nil {
		return err
	}
	reportPaired(client, store, name, res.Fingerprint, res.Grants)
	return nil
}

// reportPaired says what was paired. The name is the device's own text: the
// sentence shows it cleaned, and the command names the device as pasteArg
// writes it; when pasteArg gives none, the line says that scterm cannot give
// one.
func reportPaired(client *peer.Client, store *peer.Store, name string, fp peer.Fingerprint, grants []string) {
	fmt.Printf("Paired with %s (%s); it allows this computer to %s.\n", cleanText(name), fp.Short(), describeGrants(grants))
	self := client.Identity.Fingerprint.Short()
	if arg, ok := pasteArg(store, name, fp); ok {
		fmt.Printf("This computer is %s. Connect with: scterm --peer %s\n", self, arg)
		return
	}
	fmt.Printf("This computer is %s. scterm cannot give a command that selects only this device: scterm peers lists the pairings.\n", self)
}

// pasteArg is how a command printed for the user to paste (scterm --peer ARG,
// scterm forget ARG) names the pairing with identity fp: by its name, in
// single quotes, when that is plain and finds this pairing and no other; else
// by its short or its whole identity, whichever finds it alone, so that the
// command never carries text the device chose. It reports false when none
// does (other pairings can be named after both identities, or the pairings
// cannot be read), and then no command should be printed.
// The quotes are there even when the name needs none: an interactive zsh
// expands a global alias wherever a bare word names one.
// A plain name is not empty, is UTF-8 and has only printable characters, none
// of them a backslash (fish reads one even inside single quotes, where sh,
// bash and zsh do not), no space at either end (a query loses them) and no
// two together, so cleanText leaves it as it is; and it is not a word that
// asks a command for its help.
func pasteArg(store *peer.Store, name string, fp peer.Fingerprint) (string, bool) {
	recs, err := store.All()
	// finds reports whether query means this pairing: Find gives it, and no
	// other pairing has that name in any case (Find gives the first of them).
	finds := func(query string) bool {
		for _, r := range recs {
			if r.Fingerprint != fp.Hex() && strings.EqualFold(r.Name, query) {
				return false
			}
		}
		rec, findErr := store.Find(query)
		return err == nil && findErr == nil && rec.Fingerprint == fp.Hex()
	}
	plain := name != "" && utf8.ValidString(name) && strings.TrimSpace(name) == name && !strings.Contains(name, "  ") &&
		!isHelpArg(name) && !strings.ContainsFunc(name, func(r rune) bool { return r == '\\' || !unicode.IsPrint(r) })
	if plain && finds(name) {
		return "'" + strings.ReplaceAll(name, "'", `'\''`) + "'", true
	}
	// Another pairing can be named after this one's short identity, or after
	// its whole one.
	for _, id := range []string{fp.Short(), fp.Hex()} {
		if finds(id) {
			return id, true
		}
	}
	return "", false
}

func peersCommand(args ...string) error {
	check := false
	if len(args) != 0 {
		if len(args) != 1 || args[0] != "--check" {
			return usageErr("usage: scterm peers [--check]")
		}
		check = true
	}
	client, store, err := openPeerClient()
	if err != nil {
		return err
	}
	recs, err := store.All()
	if err != nil {
		return err
	}
	return printPeers(context.Background(), os.Stdout, client, store, recs, check, peer.DiscoverPresence, client.Probe)
}

// forgetCommand removes a pairing. It needs a name: unlike --peer=, an empty
// query must not mean "the only paired device". A name that only matches
// part of one is confirmed first.
func forgetCommand(args []string) error {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return usageErr("usage: scterm forget NAME (scterm peers lists the names)")
	}
	_, store, err := openPeerClient()
	if err != nil {
		return err
	}
	rec, err := findPeer(store, query)
	if err != nil {
		return err
	}
	name := cleanText(rec.Name)
	if !namesExactly(rec, query) {
		if !interactive() {
			// No text of the device's comes before the command, where it
			// could pose as one; the command names the device as pasteArg
			// writes it, and ends the line.
			fp, err := rec.ID()
			if err != nil {
				return err
			}
			if arg, ok := pasteArg(store, rec.Name, fp); ok {
				return fmt.Errorf("%q only partly matches a paired device: to forget it, run scterm forget %s", query, arg)
			}
			return fmt.Errorf("%q only partly matches a paired device, and scterm cannot give a command that selects only that device: forget it from a terminal, where scterm asks first", query)
		}
		answer, err := ask(fmt.Sprintf("Forget %s? [y/N] ", name))
		if err != nil {
			return err
		}
		if a := strings.ToLower(answer); a != "y" && a != "yes" {
			fmt.Printf("Kept %s.\n", name)
			return nil
		}
	}
	if _, err := store.Remove(rec.Fingerprint); err != nil {
		return err
	}
	fmt.Printf("Forgot %s. Its own list still names this computer until it is removed there.\n", name)
	return nil
}

// namesExactly reports whether query is a paired device's whole name
// (ignoring case) or its whole identity, rather than the start of either.
func namesExactly(rec peer.Record, query string) bool {
	if strings.EqualFold(rec.Name, query) {
		return true
	}
	compact := strings.ToLower(strings.ReplaceAll(query, "-", ""))
	if strings.EqualFold(rec.Fingerprint, compact) {
		return true
	}
	fp, err := rec.ID()
	return err == nil && strings.EqualFold(strings.ReplaceAll(fp.Short(), "-", ""), compact)
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

// newPeerSession connects to a paired device, found by (the start of) its
// name or its identity; "" picks the only paired device, or asks which.
func newPeerSession(query string, cfg config) (*session, error) {
	client, store, err := openPeerClient()
	if err != nil {
		return nil, err
	}
	rec, err := pickPeer(store, query)
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
	ps, err := connectPeer(store, rec, req, func(host string, port int) (*peer.Session, error) {
		return client.Connect(host, port, pin, req)
	})
	if err != nil {
		return nil, err
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
	// The notices below carry the target's own text (its name, the holder of
	// the lease, a refusal), which stderr and the replay at exit print as it
	// is: their control characters are taken out.
	name := cleanText(s.deviceName)
	ps.Start(peer.Events{
		Lease: func(state, holder string) {
			switch {
			case state == peer.LeaseHeld:
				noticef("scterm: this computer has control of %s\n", name)
			case holder != "":
				noticef("scterm: %s took control of %s; this computer is viewing (--takeover takes it back)\n", cleanText(holder), name)
			}
		},
		Error: func(code, message, controlType string) {
			if code == "no_lease" {
				logOnce(fmt.Sprintf("scterm: input ignored: another device has control of %s (--takeover takes it)\n", name))
				return
			}
			noticef("scterm: %s refused %s: %s\n", name, cleanText(controlType), cleanText(message))
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
