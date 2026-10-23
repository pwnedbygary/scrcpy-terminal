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
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
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
       scterm forget --damaged

Forgets a paired device. NAME is its whole name, in any case, or its
identity; anything less is confirmed first, and refused when there is no
terminal to ask on.

With --damaged, scterm forgets the pairings whose identity does not parse
(scterm peers lists them after the others), which no NAME selects: it shows
them and asks first, and refuses when there is no terminal to ask on.
`,
}

// damagedArg is what scterm forget takes instead of a name to forget the
// pairings whose identity does not parse.
const damagedArg = "--damaged"

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

// reportPaired says what was paired. The command comes first, naming the
// device as pasteArg writes it (when pasteArg gives none, the line says that
// scterm cannot give one), so no text of the device's comes before it; then
// the device, as data, and what it allows.
func reportPaired(client *peer.Client, store *peer.Store, name string, fp peer.Fingerprint, grants []string) {
	if arg, ok := pasteArg(store, name, fp); ok {
		fmt.Printf("Paired. Connect with: scterm --peer %s\n", arg)
	} else {
		fmt.Println("Paired. scterm cannot give a command that selects only this device: scterm peers lists the pairings.")
	}
	fmt.Printf("The device is %s.\n", deviceData(fp, name))
	fmt.Printf("It allows this computer (%s) to %s.\n", client.Identity.Fingerprint.Short(), describeGrants(grants))
}

// nameCells is the most of a device's name that scterm shows, in cells: room
// for the names devices give (Galaxy S24 Ultra), and little enough that the
// lines after a pairing fit in 80 columns, so that no part of the name can
// start a row of its own there.
const nameCells = 32

// deviceData shows a paired device as data, never as words of scterm's: its
// identity, then its name in quotes, as quoteText shows it.
func deviceData(fp peer.Fingerprint, name string) string {
	if name == "" {
		return fp.Short() + ", with no name"
	}
	return fp.Short() + ", named " + quoteText(name, nameCells)
}

// identityCells is the most of an identity as stored that scterm shows, in
// cells: all 64 hex digits of a whole one. addressCells is the most of a saved
// address shown after a name of nameCells: what 80 columns leave on its line.
const (
	identityCells = 64
	addressCells  = 29
)

// damagedPairings are the pairings in recs whose identity does not parse.
// Matches leaves them out, so no name or identity selects them.
func damagedPairings(recs []peer.Record) []peer.Record {
	var damaged []peer.Record
	for _, r := range recs {
		if _, err := r.ID(); err != nil {
			damaged = append(damaged, r)
		}
	}
	return damaged
}

// damagedData shows pairings whose identity does not parse as data, two lines
// each, which fit in 80 columns: the name and the saved address, then the
// identity as stored, each in quotes, as quoteText shows it.
func damagedData(recs []peer.Record) string {
	var b strings.Builder
	for _, r := range recs {
		fmt.Fprintf(&b, "  named %s, at %s\n    identity %s\n", quoteText(r.Name, nameCells),
			quoteText(net.JoinHostPort(r.Host, strconv.Itoa(r.Port)), addressCells), quoteText(r.Fingerprint, identityCells))
	}
	return b.String()
}

// damagedWords gives the words for n pairings whose identity does not parse:
// what is wrong ("2 pairings' identities do not parse"), the pairings ("the 2
// pairings"), and "it" or "them".
func damagedWords(n int) (count, the, them string) {
	if n == 1 {
		return "1 pairing's identity does not parse", "the pairing", "it"
	}
	return fmt.Sprintf("%d pairings' identities do not parse", n), fmt.Sprintf("the %d pairings", n), "them"
}

// pasteArg is how a command printed for the user to paste (scterm --peer ARG,
// scterm forget ARG) names the pairing with identity fp: by its name, in
// single quotes, when that is plain and finds this pairing and no other; else
// by its short or its whole identity, whichever finds it alone, so that the
// command never carries text the device chose. It reports false when none
// does (two pairings have the identity, or the pairings cannot be read), and
// then no command should be printed.
// The quotes are there even when the name needs none: an interactive zsh
// expands a global alias wherever a bare word names one.
func pasteArg(store *peer.Store, name string, fp peer.Fingerprint) (string, bool) {
	recs, err := store.All()
	// finds reports whether query means this pairing: Find gives it. An
	// identity does whatever the pairings are named.
	finds := func(query string) bool {
		rec, findErr := store.Find(query)
		return err == nil && findErr == nil && rec.Fingerprint == fp.Hex()
	}
	// Find gives the first of the pairings that have the name in any case.
	namesake := slices.ContainsFunc(recs, func(r peer.Record) bool {
		return r.Fingerprint != fp.Hex() && strings.EqualFold(r.Name, name)
	})
	if plainName(name) && !namesake && finds(name) {
		return "'" + strings.ReplaceAll(name, "'", `'\''`) + "'", true
	}
	// Another pairing's short identity can be the same.
	for _, id := range []string{fp.Short(), fp.Hex()} {
		if finds(id) {
			return id, true
		}
	}
	return "", false
}

// shellChars are the characters a shell reads in a bare word: to end, chain or
// redirect a command, to substitute, expand or glob, to start a comment or a
// history expansion, or to escape (fish reads a backslash even inside single
// quotes, where sh, bash and zsh do not).
const shellChars = "\\`;&|<>()$*?[]{}~#!^%="

// plainName reports whether a command can name a device by its name, in single
// quotes: whatever part of the name is copied from inside the quotes, it is
// only words, and the name reads as cleanText shows it. A plain name is not
// empty, is UTF-8, takes at most nameCells cells as textCells counts them and
// the command writes it (an apostrophe takes four there), and every character
// of it shows as what it is and is none of shellChars; it has no space at
// either end (a query loses them) nor two together; no "scterm" in any case,
// not even in what a paste of it can come to (pastedAs), as words such as
// scterm forget Pixel copied from it would run as they read; and scterm forget
// takes it as a name: it is not a word that asks a command for its help, and
// no paste of it can come to --damaged.
func plainName(name string) bool {
	return name != "" && utf8.ValidString(name) && textCells(name)+3*strings.Count(name, "'") <= nameCells &&
		strings.TrimSpace(name) == name && !strings.Contains(name, "  ") &&
		!strings.Contains(strings.ToLower(pastedAs(name)), "scterm") && !isHelpArg(name) && pastedAs(name) != damagedArg &&
		!strings.ContainsFunc(name, func(r rune) bool { return !shownAsIs(r) || strings.ContainsRune(shellChars, r) })
}

// pastedAs is what a paste of text can come to: a terminal may leave out the
// characters that take no cell (variation selectors, combining marks) when it
// copies, and a shell takes the quotes ' and " out of a word, so sc""term runs
// scterm.
func pastedAs(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\'' || r == '"' || runeCells(r) == 0 {
			return -1
		}
		return r
	}, text)
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
// part of one is confirmed first. The device is shown as data, on a line of
// its own; the question and what was done name it by its identity.
func forgetCommand(args []string) error {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return usageErr("usage: scterm forget NAME (scterm peers lists the names)")
	}
	if query == damagedArg {
		return forgetDamaged()
	}
	_, store, err := openPeerClient()
	if err != nil {
		return err
	}
	rec, err := findPeer(store, query)
	if err != nil {
		return err
	}
	fp, err := rec.ID()
	if err != nil {
		return err
	}
	device := fmt.Sprintf("The device is %s.\n", deviceData(fp, rec.Name))
	exact := namesExactly(rec, query)
	if !exact {
		if !interactive() {
			// No text of the device's comes before the command, where it
			// could pose as one; the command names the device as pasteArg
			// writes it, and ends the line.
			if arg, ok := pasteArg(store, rec.Name, fp); ok {
				return fmt.Errorf("%q only partly matches a paired device: to forget it, run scterm forget %s", query, arg)
			}
			return fmt.Errorf("%q only partly matches a paired device, and scterm cannot give a command that selects only that device: forget it from a terminal, where scterm asks first", query)
		}
		answer, err := ask(device + fmt.Sprintf("Forget %s? [y/N] ", fp.Short()))
		if err != nil {
			return err
		}
		if a := strings.ToLower(answer); a != "y" && a != "yes" {
			fmt.Printf("Kept %s.\n", fp.Short())
			return nil
		}
	}
	if _, err := store.Remove(rec.Fingerprint); err != nil {
		return err
	}
	if exact {
		fmt.Print(device)
	}
	fmt.Printf("Forgot %s. Its own list still names this computer until it is removed there.\n", fp.Short())
	return nil
}

// forgetDamaged removes the pairings whose identity does not parse, once they
// are shown, as data, and the user agrees; with no terminal to ask on, it
// refuses. Each goes by its identity as stored, which Remove matches exactly,
// so no pairing whose identity parses can go with them.
func forgetDamaged() error {
	home, err := peerHome()
	if err != nil {
		return err
	}
	store := peer.OpenStore(home)
	recs, err := store.All()
	if err != nil {
		return err
	}
	damaged := damagedPairings(recs)
	if len(damaged) == 0 {
		fmt.Println("No pairing has an identity that does not parse: there is nothing to forget.")
		return nil
	}
	count, the, them := damagedWords(len(damaged))
	shown := count + ":\n" + damagedData(damaged)
	if !interactive() {
		fmt.Print(shown)
		return fmt.Errorf("%s: forget %s from a terminal, where scterm asks first", count, them)
	}
	answer, err := ask(shown + "Forget " + the + "? [y/N] ")
	if err != nil {
		return err
	}
	if a := strings.ToLower(answer); a != "y" && a != "yes" {
		fmt.Printf("Kept %s.\n", the)
		return nil
	}
	for _, rec := range damaged {
		if _, err := store.Remove(rec.Fingerprint); err != nil {
			return err
		}
	}
	fmt.Printf("Forgot %s. A device keeps this computer in its own list until it is removed there.\n", the)
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
