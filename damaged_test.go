package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// A pairing whose identity does not parse, in a peers.json damaged or edited
// by hand, is one that no name or identity selects: scterm forget NAME never
// touches it. scterm forget --damaged forgets such pairings, and only them,
// once it has shown them as data and asked; scterm peers lists them after the
// devices, with that command, and scterm doctor advises it.
// ---------------------------------------------------------------------------

// pairing is a record of a device named name, at host, whose identity is
// stored as fingerprint.
func pairing(fingerprint, name, host string) peer.Record {
	return peer.Record{Fingerprint: fingerprint, Name: name, Host: host, Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1}
}

// writePairings writes recs to peers.json as they are (Put would merge two
// stored with the same identity), under a fresh SCTERM_HOME, and returns the
// store.
func writePairings(t *testing.T, recs ...peer.Record) *peer.Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SCTERM_HOME", home)
	data, err := json.Marshal(recs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "peers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return peer.OpenStore(home)
}

// runForget runs scterm forget with args, answering what it asks with answer,
// and returns what it wrote to stdout and to stderr, and its error.
func runForget(t *testing.T, answer string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	old := terminalInput
	t.Cleanup(func() { terminalInput = old })
	terminalInput = bufioReader(answer)
	stdout = captureStdout(t, func() {
		stderr = captureStderr(t, func() { err = forgetCommand(args) })
	})
	return stdout, stderr, err
}

// wantLeft checks that store holds want, in that order, and nothing else.
func wantLeft(t *testing.T, store *peer.Store, what string, want ...peer.Record) {
	t.Helper()
	got, err := store.All()
	if err != nil || len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, want) {
		t.Errorf("%s left %+v, %v\nwant %+v", what, got, err, want)
	}
}

// editedPairings are pairings as a peers.json edited by hand can hold them:
// three whose identity parses though it is not stored as scterm writes it (in
// upper case, as hex pairs between colons, with spaces around), and one whose
// identity does not parse, written in upper case with spaces around.
func editedPairings(t *testing.T) (upper, colons, spaced, shouty peer.Record) {
	t.Helper()
	hex := func(seed string) string { return peer.FingerprintOf([]byte(seed)).Hex() }
	var pairs []string
	for h := hex("colons"); h != ""; h = h[2:] {
		pairs = append(pairs, h[:2])
	}
	upper = pairing(strings.ToUpper(hex("upper")), "Upper", "192.0.2.3")
	colons = pairing(strings.Join(pairs, ":"), "Colons", "192.0.2.4")
	spaced = pairing(" "+hex("spaced")+" ", "Spaced", "192.0.2.5")
	shouty = pairing(" NOT-A-FINGERPRINT ", "Shouty", "192.0.2.8")
	for _, r := range []peer.Record{upper, colons, spaced} {
		if fp, err := r.ID(); err != nil || fp.Hex() == r.Fingerprint {
			t.Fatalf("%q: %v; this test needs an identity that parses, stored otherwise than scterm writes it", r.Fingerprint, err)
		}
	}
	if _, err := shouty.ID(); err == nil {
		t.Fatalf("%q parses; this test needs one that does not", shouty.Fingerprint)
	}
	return upper, colons, spaced, shouty
}

// TestForgetDamagedAsksFirst: in a terminal, scterm forget --damaged shows the
// pairings whose identity does not parse, each as data (its name and saved
// address, then its identity as stored, each in quotes), and asks once, all
// on stderr, where a question goes. Yes forgets them all, two stored with the
// same identity included, and no other pairing, even one whose identity
// parses though a hand edit stored it in upper case, between colons or with
// spaces around; anything else keeps them, and says so.
func TestForgetDamagedAsksFirst(t *testing.T) {
	withInteractive(t, true)
	pixel := pairing(peer.FingerprintOf([]byte("Pixel 8")).Hex(), "Pixel 8", "192.0.2.1")
	tablet := pairing(peer.FingerprintOf([]byte("Tab S9")).Hex(), "Tab S9", "192.0.2.2")
	old := pairing("not-a-fingerprint", "Old phone", "192.0.2.9")
	blank := pairing("", "", "")
	spare := pairing("", "Spare", "spare.lan")
	upper, colons, spaced, shouty := editedPairings(t)
	const forgot = ". A device keeps this computer in its own list until it is removed there.\n"
	one := "1 pairing's identity does not parse:\n" +
		`  named "Old phone", at "192.0.2.9:27300"` + "\n" +
		`    identity "not-a-fingerprint"` + "\n" +
		"Forget the pairing? [y/N] "
	three := "3 pairings' identities do not parse:\n" +
		`  named "", at ":27300"` + "\n" +
		`    identity ""` + "\n" +
		`  named "Old phone", at "192.0.2.9:27300"` + "\n" +
		`    identity "not-a-fingerprint"` + "\n" +
		`  named "Spare", at "spare.lan:27300"` + "\n" +
		`    identity ""` + "\n" +
		"Forget the 3 pairings? [y/N] "
	edited := "2 pairings' identities do not parse:\n" +
		`  named "Old phone", at "192.0.2.9:27300"` + "\n" +
		`    identity "not-a-fingerprint"` + "\n" +
		`  named "Shouty", at "192.0.2.8:27300"` + "\n" +
		`    identity " NOT-A-FINGERPRINT "` + "\n" +
		"Forget the 2 pairings? [y/N] "
	for _, tc := range []struct {
		recs                []peer.Record
		answer, asked, said string
		left                []peer.Record
	}{
		{[]peer.Record{pixel, old}, "y\n", one, "Forgot the pairing" + forgot, []peer.Record{pixel}},
		{[]peer.Record{old, pixel}, "yes\n", one, "Forgot the pairing" + forgot, []peer.Record{pixel}},
		{[]peer.Record{pixel, old}, "n\n", one, "Kept the pairing.\n", []peer.Record{pixel, old}},
		{[]peer.Record{pixel, old}, "\n", one, "Kept the pairing.\n", []peer.Record{pixel, old}},
		{[]peer.Record{blank, pixel, old, tablet, spare}, "Y\n", three, "Forgot the 3 pairings" + forgot, []peer.Record{pixel, tablet}},
		{[]peer.Record{blank, pixel, old, tablet, spare}, "no\n", three, "Kept the 3 pairings.\n", []peer.Record{blank, pixel, old, tablet, spare}},
		{[]peer.Record{upper, old, colons, shouty, spaced}, "y\n", edited, "Forgot the 2 pairings" + forgot, []peer.Record{upper, colons, spaced}},
		{[]peer.Record{upper, old, colons, shouty, spaced}, "n\n", edited, "Kept the 2 pairings.\n", []peer.Record{upper, old, colons, shouty, spaced}},
	} {
		store := writePairings(t, tc.recs...)
		said, asked, err := runForget(t, tc.answer, "--damaged")
		if err != nil || said != tc.said || asked != tc.asked {
			t.Errorf("answering %q: %v, stdout %q, stderr %q\nwant stdout %q, stderr %q", tc.answer, err, said, asked, tc.said, tc.asked)
		}
		wantLeft(t, store, fmt.Sprintf("answering %q", tc.answer), tc.left...)
	}

	// No answer at all keeps them too.
	store := writePairings(t, pixel, old)
	if said, asked, err := runForget(t, "", "--damaged"); err == nil || said != "" || asked != one {
		t.Errorf("no answer: %v, stdout %q, stderr %q; want an error, nothing on stdout, and the question", err, said, asked)
	}
	wantLeft(t, store, "no answer", pixel, old)
}

// TestForgetDamagedNeedsATerminal: with no terminal to ask on, scterm forget
// --damaged shows the pairings whose identity does not parse, as data, and
// refuses, as it refuses part of a name: it forgets nothing.
func TestForgetDamagedNeedsATerminal(t *testing.T) {
	withInteractive(t, false)
	pixel := pairing(peer.FingerprintOf([]byte("Pixel 8")).Hex(), "Pixel 8", "192.0.2.1")
	old := pairing("not-a-fingerprint", "Old phone", "192.0.2.9")
	spare := pairing("0123", "Spare", "spare.lan")
	for _, tc := range []struct {
		recs           []peer.Record
		shown, wantErr string
	}{
		{[]peer.Record{pixel, old}, "1 pairing's identity does not parse:\n" +
			`  named "Old phone", at "192.0.2.9:27300"` + "\n" + `    identity "not-a-fingerprint"` + "\n",
			"1 pairing's identity does not parse: forget it from a terminal, where scterm asks first"},
		{[]peer.Record{old, pixel, spare}, "2 pairings' identities do not parse:\n" +
			`  named "Old phone", at "192.0.2.9:27300"` + "\n" + `    identity "not-a-fingerprint"` + "\n" +
			`  named "Spare", at "spare.lan:27300"` + "\n" + `    identity "0123"` + "\n",
			"2 pairings' identities do not parse: forget them from a terminal, where scterm asks first"},
	} {
		store := writePairings(t, tc.recs...)
		said, asked, err := runForget(t, "y\n", "--damaged")
		if errText(err) != tc.wantErr || said != tc.shown || asked != "" {
			t.Errorf("without a terminal: error %q, stdout %q, stderr %q\nwant error %q, stdout %q, nothing on stderr", errText(err), said, asked, tc.wantErr, tc.shown)
		}
		wantLeft(t, store, "a refusal", tc.recs...)
	}
}

// TestForgetDamagedWithNone: when every pairing's identity parses, or nothing
// is paired, scterm forget --damaged says there is nothing to forget, and
// succeeds, without asking anything or changing a file: it makes no identity.
func TestForgetDamagedWithNone(t *testing.T) {
	withInteractive(t, true)
	const none = "No pairing has an identity that does not parse: there is nothing to forget.\n"
	pixel := pairing(peer.FingerprintOf([]byte("Pixel 8")).Hex(), "Pixel 8", "192.0.2.1")
	tablet := pairing(peer.FingerprintOf([]byte("Tab S9")).Hex(), "Tab S9", "192.0.2.2")
	writePairings(t, pixel, tablet)
	for _, home := range []string{os.Getenv("SCTERM_HOME"), t.TempDir()} {
		t.Setenv("SCTERM_HOME", home)
		before := files(t, home)
		said, asked, err := runForget(t, "y\n", "--damaged")
		if err != nil || said != none || asked != "" {
			t.Errorf("%d files at home: %v, stdout %q, stderr %q; want stdout %q", len(before), err, said, asked, none)
		}
		if after := files(t, home); !reflect.DeepEqual(after, before) {
			t.Errorf("the files went from %q to %q", before, after)
		}
	}
}

// TestForgetDamagedWithADamagedIdentity: scterm forget --damaged needs only the
// pairings. When identity.pem does not load either, as doctor can report
// before it advises scterm forget --damaged, the damaged pairings are listed
// and forgotten as usual, and identity.pem stays byte for byte as it was.
func TestForgetDamagedWithADamagedIdentity(t *testing.T) {
	withInteractive(t, true)
	pixel := pairing(peer.FingerprintOf([]byte("Pixel 8")).Hex(), "Pixel 8", "192.0.2.1")
	old := pairing("not-a-fingerprint", "Old phone", "192.0.2.9")
	store := writePairings(t, pixel, old)
	home := os.Getenv("SCTERM_HOME")
	pem := filepath.Join(home, "identity.pem")
	if err := os.WriteFile(pem, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.LoadIdentity(home); err == nil {
		t.Fatal("identity.pem loads; this test needs one that does not")
	}
	question := "1 pairing's identity does not parse:\n" +
		`  named "Old phone", at "192.0.2.9:27300"` + "\n" +
		`    identity "not-a-fingerprint"` + "\n" +
		"Forget the pairing? [y/N] "
	const forgot = "Forgot the pairing. A device keeps this computer in its own list until it is removed there.\n"
	said, asked, err := runForget(t, "y\n", "--damaged")
	if err != nil || said != forgot || asked != question {
		t.Errorf("identity.pem damaged: %v, stdout %q, stderr %q\nwant stdout %q, stderr %q", err, said, asked, forgot, question)
	}
	wantLeft(t, store, "forget --damaged with identity.pem damaged", pixel)
	if b, err := os.ReadFile(pem); err != nil || string(b) != "not a key" {
		t.Errorf("identity.pem now holds %q, %v; want it as it was, %q", b, err, "not a key")
	}
}

// TestDamagedPairingsShareNothingWithNamesakes: a pairing whose identity does
// not parse shares nothing with the devices, whatever either is named. Here
// one is named as a device is, a device is named as the other's identity is
// stored, and that identity is the start of a device's. scterm forget with a
// name or an identity finds, asks about and forgets those devices alone, and
// finds nothing by a damaged pairing's name; scterm forget --damaged forgets
// the damaged pairings alone.
func TestDamagedPairingsShareNothingWithNamesakes(t *testing.T) {
	victim, other := peer.FingerprintOf([]byte("victim")), peer.FingerprintOf([]byte("other"))
	pixel := pairing(victim.Hex(), "Pixel 8", "192.0.2.1")
	twin := pairing("not-a-fingerprint", "Pixel 8", "192.0.2.9")
	named := pairing(other.Hex(), "not-a-fingerprint", "192.0.2.2")
	start := pairing(victim.Hex()[:63], "Old phone", "192.0.2.8")
	all := []peer.Record{pixel, twin, named, start}
	question := func(fp peer.Fingerprint, name string) string {
		return "The device is " + fp.Short() + `, named "` + name + `".` + "\nForget " + fp.Short() + "? [y/N] "
	}
	damaged := "2 pairings' identities do not parse:\n" +
		`  named "Pixel 8", at "192.0.2.9:27300"` + "\n" +
		`    identity "not-a-fingerprint"` + "\n" +
		`  named "Old phone", at "192.0.2.8:27300"` + "\n" +
		`    identity "` + victim.Hex()[:63] + `"` + "\n" +
		"Forget the 2 pairings? [y/N] "
	for _, tc := range []struct {
		args           []string
		interactive    bool
		answer         string
		asked, wantErr string
		left           []peer.Record
	}{
		{[]string{"Pixel", "8"}, false, "", "", "", []peer.Record{twin, named, start}},
		{[]string{"pixel"}, true, "y\n", question(victim, "Pixel 8"), "", []peer.Record{twin, named, start}},
		{[]string{victim.Short()}, false, "", "", "", []peer.Record{twin, named, start}},
		{[]string{victim.Hex()[:63]}, true, "n\n", question(victim, "Pixel 8"), "", all},
		{[]string{"not-a-fingerprint"}, false, "", "", "", []peer.Record{pixel, twin, start}},
		{[]string{"not-a"}, true, "n\n", question(other, "not-a-fingerprint"), "", all},
		{[]string{"old"}, true, "y\n", "", `no paired device matches "old"`, all},
		{[]string{"Old", "phone"}, true, "y\n", "", `no paired device matches "Old phone"`, all},
		{[]string{"--damaged"}, true, "y\n", damaged, "", []peer.Record{pixel, named}},
	} {
		withInteractive(t, tc.interactive)
		store := writePairings(t, all...)
		_, asked, err := runForget(t, tc.answer, tc.args...)
		if asked != tc.asked || errText(err) != tc.wantErr {
			t.Errorf("forget %q: stderr %q, error %q\nwant stderr %q, error %q", tc.args, asked, errText(err), tc.asked, tc.wantErr)
		}
		wantLeft(t, store, fmt.Sprintf("forget %q", tc.args), tc.left...)
	}
}

// TestDamagedPairingsShowAsData: all of a pairing whose identity does not
// parse is text from outside. scterm forget --damaged, asking or refusing, and
// scterm peers show its name, its saved address and its identity in quotes,
// with escapes for control, format and blank-looking characters, each cut
// short, so that none of it can act on the terminal or pad a row, every line
// starts with scterm's own words, and each fits in 80 columns.
func TestDamagedPairingsShowAsData(t *testing.T) {
	fp := peer.FingerprintOf([]byte("Pixel 8"))
	pixel := pairing(fp.Hex(), "Pixel 8", "192.0.2.1")
	hostile := []peer.Record{
		pairing(strings.Repeat("\u2800", 70)+"Forgot everything", dirtyPixel8, "evil\x1b[2J\u200b.lan"),
		pairing("0123\u00854567\u3164", "Pixel"+strings.Repeat("\u3164", 40)+"Paired. Connect with", "pixel\u00a0lan"),
		pairing(strings.Repeat("0", 65), strings.Repeat("日本", 20), "fe80::1\u202e"),
		pairing("\x1b]0;owned\x07", "\u202e\u2066\u200b\u00ad\ufeff", "x\n1 more pairing's identity does not parse"),
	}
	const count = "4 pairings' identities do not parse"
	data := `  named "Pixel\x1b]0;owned\a 8\u202e", at "evil\x1b[2J\u200b.lan:27300"` + "\n" +
		`    identity "` + strings.Repeat(`\u2800`, 10) + `"…` + "\n" +
		`  named "Pixel\u3164\u3164\u3164\u3164"…, at "pixel\u00a0lan:27300"` + "\n" +
		`    identity "0123\u00854567\u3164"` + "\n" +
		`  named "` + strings.Repeat("日本", 8) + `"…, at "[fe80::1\u202e]:27300"` + "\n" +
		`    identity "` + strings.Repeat("0", 64) + `"…` + "\n" +
		`  named "\u202e\u2066\u200b\u00ad\ufeff", at "x\n1 more pairing's identity "…` + "\n" +
		`    identity "\x1b]0;owned\a"` + "\n"
	store := writePairings(t, append([]peer.Record{pixel}, hostile...)...)

	withInteractive(t, false)
	refused, asked, err := runForget(t, "", "--damaged")
	if want := count + ": forget them from a terminal, where scterm asks first"; refused != count+":\n"+data || asked != "" || errText(err) != want {
		t.Errorf("without a terminal: error %q, stdout %q, stderr %q\nwant error %q, stdout %q", errText(err), refused, asked, want, count+":\n"+data)
	}
	withInteractive(t, true)
	said, question, err := runForget(t, "n\n", "--damaged")
	if want := count + ":\n" + data + "Forget the 4 pairings? [y/N] "; err != nil || said != "Kept the 4 pairings.\n" || question != want {
		t.Errorf("in a terminal: %v, stdout %q, stderr %q\nwant stderr %q", err, said, question, want)
	}
	wantLeft(t, store, "forget --damaged, refused and declined", append([]peer.Record{pixel}, hostile...)...)

	recs, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	client := &peer.Client{Identity: &peer.Identity{Fingerprint: peer.FingerprintOf([]byte("this computer"))}}
	var listed strings.Builder
	if err := printPeers(context.Background(), &listed, client, store, recs, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	wantListed := "This computer: " + client.Identity.Fingerprint.Short() + "\n" +
		"  Pixel 8" + strings.Repeat(" ", 18) + fp.Short() + "  192.0.2.1:27300" + strings.Repeat(" ", 7) + "may view\n" +
		count + " (scterm forget --damaged forgets them):\n" + data
	if listed.String() != wantListed {
		t.Errorf("scterm peers: %q\nwant %q", listed.String(), wantListed)
	}

	words := []string{"This computer: ", "  Pixel 8 ", count, `  named "`, `    identity "`, "Forget the 4 pairings? [y/N] "}
	for what, text := range map[string]string{"refused": refused, "asked": question, "scterm peers": listed.String()} {
		if blankOrHidden(text) {
			t.Errorf("%s: %q holds what would act on the terminal or pad a row", what, text)
		}
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if cellWidth(line) > 80 {
				t.Errorf("%s: a line is %d cells: %q", what, cellWidth(line), line)
			}
			if !slices.ContainsFunc(words, func(w string) bool { return strings.HasPrefix(line, w) }) {
				t.Errorf("%s: a line does not start with scterm's words: %q", what, line)
			}
		}
	}
}

// TestPeersListDamagedPairings: scterm peers lists the devices, those whose
// identity a hand edit stored in upper case, between colons or with spaces
// around included, then a line that says how many pairings have an identity
// that does not parse and gives the command that forgets them, then each of
// those as data. --check checks the devices alone, and shows the damaged
// pairings as the plain listing does, with no word of their own.
func TestPeersListDamagedPairings(t *testing.T) {
	client, store, devices := checkFixture(t, 2)
	upper, colons, spaced, shouty := editedPairings(t)
	old, blank := pairing("not-a-fingerprint", "Old phone", "192.0.2.9"), pairing("", "", "")
	// device is a device's line: its name, short identity and address in the
	// listing's columns, then what it may do.
	device := func(r peer.Record) string {
		fp, err := r.ID()
		if err != nil {
			t.Fatal(err)
		}
		addr := r.Host + ":27300"
		return "  " + r.Name + strings.Repeat(" ", 25-len(r.Name)) + fp.Short() + "  " + addr + strings.Repeat(" ", 22-len(addr)) + "may view"
	}
	damaged := "3 pairings' identities do not parse (scterm forget --damaged forgets them):\n" +
		`  named "Old phone", at "192.0.2.9:27300"` + "\n" +
		`    identity "not-a-fingerprint"` + "\n" +
		`  named "Shouty", at "192.0.2.8:27300"` + "\n" +
		`    identity " NOT-A-FINGERPRINT "` + "\n" +
		`  named "", at ":27300"` + "\n" +
		`    identity ""` + "\n"
	self := "This computer: " + client.Identity.Fingerprint.Short() + "\n"
	discover := func(context.Context, time.Duration) ([]peer.NearbyDevice, error) { return nil, nil }
	var mu sync.Mutex
	var pins []string
	probe := func(_ context.Context, _ string, _ int, pin peer.Fingerprint) (*peer.Message, error) {
		mu.Lock()
		defer mu.Unlock()
		pins = append(pins, pin.Hex())
		yes := true
		return &peer.Message{Type: peer.TypeProbeResult, V: 1, Ready: &yes, Device: &peer.DeviceInfo{}, Caps: &peer.Capabilities{Video: true}, Grants: []string{peer.GrantView}}, nil
	}
	recs := []peer.Record{devices[0], old, upper, colons, shouty, devices[1], spaced, blank}
	var listed, checked string
	var ids []string
	for _, r := range []peer.Record{devices[0], upper, colons, devices[1], spaced} {
		listed += device(r) + "\n"
		checked += device(r) + " · Ready\n"
		fp, _ := r.ID()
		ids = append(ids, fp.Hex())
	}
	slices.Sort(ids)
	for _, tc := range []struct {
		check bool
		want  string
		pins  []string
	}{
		{false, self + listed + damaged, nil},
		{true, self + checked + damaged, ids},
	} {
		pins = nil
		var out strings.Builder
		if err := printPeers(context.Background(), &out, client, store, recs, tc.check, discover, probe); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.want {
			t.Errorf("check=%v: %q\nwant %q", tc.check, out.String(), tc.want)
		}
		slices.Sort(pins)
		if !slices.Equal(pins, tc.pins) {
			t.Errorf("check=%v probed %q; want the devices alone, %q", tc.check, pins, tc.pins)
		}
	}

	// Only a damaged pairing: no device is listed or probed, and it is shown,
	// checked or not.
	noProbe := func(context.Context, string, int, peer.Fingerprint) (*peer.Message, error) {
		t.Error("a damaged pairing was probed")
		return nil, errors.New("not probed")
	}
	want := self + "1 pairing's identity does not parse (scterm forget --damaged forgets it):\n" +
		`  named "Old phone", at "192.0.2.9:27300"` + "\n" + `    identity "not-a-fingerprint"` + "\n"
	for _, check := range []bool{false, true} {
		var out strings.Builder
		if err := printPeers(context.Background(), &out, client, store, []peer.Record{old}, check, discover, noProbe); err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Errorf("only a damaged pairing, check=%v: %q\nwant %q", check, out.String(), want)
		}
	}
}

// TestDamagedIsNoName: scterm forget takes --damaged for the command, never for
// a name: a device named --damaged is named by its identity in the commands
// scterm prints, and so is one whose name, pasted, can come to --damaged as
// forget trims it: --damaged with a variation selector or a combining mark,
// which a terminal may leave out when it copies, at its end, inside it, or
// beside a space at either end. scterm forget --damaged leaves them all alone.
// The word is taken as written, so a device named --Damaged is named, and
// forgotten, by its name.
func TestDamagedIsNoName(t *testing.T) {
	withInteractive(t, false)
	for _, tc := range []struct{ name, query string }{ // query: a part of the name
		{"--damaged", "--dam"},
		{"--damaged\ufe0f", "--dam"},
		{"--damaged\u0301", "--dam"},
		{"-\u0301-damaged", "-\u0301-dam"},
		{"--damaged \ufe0f", "--dam"},
		{"\ufe0f --damaged", "--dam"},
	} {
		fp := peer.FingerprintOf([]byte(tc.name))
		dev := pairing(fp.Hex(), tc.name, "192.0.2.1")
		store := writePairings(t, dev)
		if got, ok := pasteArg(store, tc.name, fp); !ok || got != fp.Short() {
			t.Errorf("%+q: pasteArg gives %+q, %v; want %s", tc.name, got, ok, fp.Short())
		}
		said, _, err := runForget(t, "", "--damaged")
		if err != nil || said != "No pairing has an identity that does not parse: there is nothing to forget.\n" {
			t.Errorf("%+q: forget --damaged: %v, stdout %q; want nothing to forget", tc.name, err, said)
		}
		want := fmt.Sprintf("%q only partly matches a paired device: to forget it, run scterm forget %s", tc.query, fp.Short())
		if _, _, err := runForget(t, "", tc.query); errText(err) != want {
			t.Errorf("%+q: forget %+q: %q\nwant %q", tc.name, tc.query, errText(err), want)
		}
		wantLeft(t, store, fmt.Sprintf("%+q: forget --damaged and %+q", tc.name, tc.query), dev)
	}

	cased := peer.FingerprintOf([]byte("--Damaged"))
	store := writePairings(t, pairing(cased.Hex(), "--Damaged", "192.0.2.1"))
	if got, ok := pasteArg(store, "--Damaged", cased); !ok || got != "'--Damaged'" {
		t.Errorf("pasteArg gives %s, %v; want '--Damaged'", got, ok)
	}
	said, _, err := runForget(t, "", "--Damaged")
	if want := "The device is " + cased.Short() + `, named "--Damaged".` + "\nForgot " + cased.Short() + ". Its own list still names this computer until it is removed there.\n"; err != nil || said != want {
		t.Errorf("forget --Damaged: %v, stdout %q\nwant %q", err, said, want)
	}
	wantLeft(t, store, "forget --Damaged")
}

// TestForgetHelpGivesDamaged: scterm forget -h gives both ways to run it, and
// says what --damaged forgets.
func TestForgetHelpGivesDamaged(t *testing.T) {
	out := captureStdout(t, func() { runPeerCommand([]string{"forget", "-h"}) })
	if !strings.HasPrefix(out, "usage: scterm forget NAME\n       scterm forget --damaged\n") || !strings.Contains(out, "identity does not parse") {
		t.Errorf("scterm forget -h: %q", out)
	}
}
