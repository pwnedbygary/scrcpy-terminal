package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"scterm/peer"
)

// TestPasteArgNamesByPlainNamesOnly: a command printed for pasting names a
// pairing by its name, in single quotes, only when nothing copied from inside
// the quotes can run, the name reads as cleanText shows it, and it finds the
// pairing alone; otherwise by its short identity. Find gives the pairing for
// either.
func TestPasteArgNamesByPlainNamesOnly(t *testing.T) {
	store, pairings := pastePairings(t)
	for _, p := range pairings {
		want := p.arg
		if want == "" {
			want = p.fp.Short()
		}
		if got, ok := pasteArg(store, p.rec.Name, p.fp); !ok || got != want {
			t.Errorf("%q: %s, %v; want %s", p.rec.Name, got, ok, want)
		}
		if p.arg != "" && cleanText(p.rec.Name) != p.rec.Name {
			t.Errorf("%q would be named as it is, but is shown as %q", p.rec.Name, cleanText(p.rec.Name))
		}
		if rec, err := store.Find(p.want()); err != nil || rec.Fingerprint != p.rec.Fingerprint {
			t.Errorf("%q: Find(%q) gives %q, %v; want this pairing", p.rec.Name, p.want(), rec.Name, err)
		}
	}
}

// TestPasteArgNeedsAName: the only pairing, which has no name at all (as when
// neither a nearby device nor the search gave one), is named by its identity:
// an empty --peer value would mean whichever device is paired.
func TestPasteArgNeedsAName(t *testing.T) {
	store := pairedStore(t, "")
	fp, err := peer.ParseFingerprint(fmt.Sprintf("%064x", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := pasteArg(store, "", fp); !ok || got != "0000-0000-0000-0000" {
		t.Errorf("%s, %v; want the identity, 0000-0000-0000-0000", got, ok)
	}
}

// TestPasteArgFallsBackToTheWholeIdentity: when the short identity does not
// find the pairing alone, the command gives the whole identity, which does:
// two identities that start alike (pairedStore's differ only at the end).
func TestPasteArgFallsBackToTheWholeIdentity(t *testing.T) {
	store := pairedStore(t, dirtyPixel8, dirtyPixel9)
	for i, name := range []string{dirtyPixel8, dirtyPixel9} {
		fp, err := peer.ParseFingerprint(fmt.Sprintf("%064x", i+1))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := pasteArg(store, name, fp)
		if rec, err := store.Find(got); !ok || got != fp.Hex() || err != nil || rec.Name != name {
			t.Errorf("%q: %s, %v, which finds %q, %v; want %s", name, got, ok, rec.Name, err, fp.Hex())
		}
	}
}

// TestAnIdentityIsNotTakenByANamesake: devices named after another pairing's
// short and whole identities take neither. Typed by hand, as pasted or as a
// name, each identity selects its pairing, without a question, and forgets it
// at once; the commands scterm prints give the short identity.
func TestAnIdentityIsNotTakenByANamesake(t *testing.T) {
	victim := peer.FingerprintOf([]byte("victim"))
	compact := strings.ToLower(strings.ReplaceAll(victim.Short(), "-", ""))
	ids := []string{victim.Short(), victim.Hex(), compact}
	home := t.TempDir()
	t.Setenv("SCTERM_HOME", home)
	store := peer.OpenStore(home)
	for i, name := range append([]string{dirtyPixel8}, ids...) {
		fp := victim
		if i > 0 {
			fp = peer.FingerprintOf([]byte("impostor " + strconv.Itoa(i)))
		}
		if err := store.Put(peer.Record{Fingerprint: fp.Hex(), Name: name, Host: "192.0.2.1", Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1}); err != nil {
			t.Fatal(err)
		}
	}
	withInteractive(t, true)
	oldInput := terminalInput
	t.Cleanup(func() { terminalInput = oldInput })
	for _, id := range append(ids, strings.ToLower(victim.Short())) {
		if rec, err := store.Find(id); err != nil || rec.Fingerprint != victim.Hex() {
			t.Errorf("Find(%s) gives %q, %v; want the pairing of that identity", id, rec.Name, err)
		}
		terminalInput = bufioReader("") // a question fails
		var rec peer.Record
		var err error
		stderr := captureStderr(t, func() { rec, err = pickPeer(store, id) })
		if err != nil || rec.Fingerprint != victim.Hex() || stderr != "" {
			t.Errorf("--peer %s picks %q, %v, asking %q; want the pairing of that identity", id, rec.Name, err, stderr)
		}
	}
	if got, ok := pasteArg(store, dirtyPixel8, victim); !ok || got != victim.Short() {
		t.Errorf("pasteArg gives %s, %v; want %s", got, ok, victim.Short())
	}
	client := &peer.Client{Identity: &peer.Identity{Fingerprint: peer.FingerprintOf([]byte("this computer"))}}
	out := captureStdout(t, func() { reportPaired(client, store, dirtyPixel8, victim, []string{peer.GrantView}) })
	if want := "Paired. Connect with: scterm --peer " + victim.Short() + "\n"; !strings.HasPrefix(out, want) {
		t.Errorf("after pairing: %q\nwant it to start %q", out, want)
	}

	withInteractive(t, false)
	stdout := captureStdout(t, func() {
		if err := forgetCommand([]string{victim.Short()}); err != nil {
			t.Errorf("forget %s: %v", victim.Short(), err)
		}
	})
	recs, err := store.All()
	if err != nil || len(recs) != 3 || slices.ContainsFunc(recs, func(r peer.Record) bool { return r.Fingerprint == victim.Hex() }) {
		t.Errorf("forget %s printed %q and left %+v, %v; want the impostors only", victim.Short(), stdout, recs, err)
	}
}

// TestNoCommandWhenNothingSelectsThePairingAlone: when two pairings have one
// identity (in a list edited by hand) and the name is not plain text,
// pasteArg has nothing to give (`pix`, the start of the name, still selects
// it; pasteArg tries only the name and the identities). So no command is
// printed: after pairing, the line says scterm cannot give one, and forget's
// refusal says to forget it from a terminal, where scterm asks first. A list
// of the devices a query means shows it by its short identity.
func TestNoCommandWhenNothingSelectsThePairingAlone(t *testing.T) {
	victim := peer.FingerprintOf([]byte("victim"))
	home := t.TempDir()
	t.Setenv("SCTERM_HOME", home)
	twice, err := json.Marshal([]peer.Record{
		{Fingerprint: victim.Hex(), Name: dirtyPixel8, Host: "192.0.2.1", Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1},
		{Fingerprint: victim.Hex(), Name: "Tablet", Host: "192.0.2.2", Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "peers.json"), twice, 0o600); err != nil {
		t.Fatal(err)
	}
	store := peer.OpenStore(home)
	for _, id := range []string{victim.Short(), victim.Hex()} {
		var several *peer.AmbiguousError
		if _, err := store.Find(id); !errors.As(err, &several) {
			t.Fatalf("Find(%s): %v; this test needs both pairings", id, err)
		}
	}
	if rec, err := store.Find("pix"); err != nil || rec.Name != dirtyPixel8 {
		t.Fatalf("Find(pix) gives %q, %v; this test needs the victim", rec.Name, err)
	}
	if got, ok := pasteArg(store, dirtyPixel8, victim); ok {
		rec, err := store.Find(got)
		t.Errorf("pasteArg gives %s, which finds %q, %v; want nothing", got, rec.Name, err)
	}

	client := &peer.Client{Identity: &peer.Identity{Fingerprint: peer.FingerprintOf([]byte("this computer"))}}
	out := captureStdout(t, func() { reportPaired(client, store, dirtyPixel8, victim, []string{peer.GrantView}) })
	want := "Paired. scterm cannot give a command that selects only this device: scterm peers lists the pairings.\n" +
		`The device is ` + victim.Short() + `, named "Pixel\x1b]0;owned\a 8\u202e".` + "\n" +
		"It allows this computer (" + client.Identity.Fingerprint.Short() + ") to view.\n"
	if out != want {
		t.Errorf("after pairing: %q\nwant %q", out, want)
	}

	withInteractive(t, false)
	wantErr := `"pix" only partly matches a paired device, and scterm cannot give a command that selects only that device: forget it from a terminal, where scterm asks first`
	if err := forgetCommand([]string{"pix"}); err == nil || err.Error() != wantErr {
		t.Errorf("forget pix: %v\nwant %s", err, wantErr)
	}
	if recs, err := store.All(); err != nil || len(recs) != 2 {
		t.Errorf("forget pix left %d pairings, %v; want both", len(recs), err)
	}
	wantErr = "2 devices are paired (" + victim.Short() + ", 'Tablet'); name one"
	if _, err := pickPeer(store, ""); err == nil || err.Error() != wantErr {
		t.Errorf("--peer=: %v\nwant %s", err, wantErr)
	}
}

// TestNoCommandFromPairingsThatCannotBeRead: when the pairings cannot be read,
// nothing shows that a name or an identity finds this pairing alone, so
// pasteArg gives nothing and the line after pairing gives no command.
func TestNoCommandFromPairingsThatCannotBeRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCTERM_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "peers.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := peer.OpenStore(home)
	if _, err := store.All(); err == nil {
		t.Fatal("the pairings read; this test needs a file that does not")
	}
	fp := peer.FingerprintOf([]byte("Pixel 8"))
	if got, ok := pasteArg(store, "Pixel 8", fp); ok {
		t.Errorf("pasteArg gives %s; want nothing", got)
	}
	client := &peer.Client{Identity: &peer.Identity{Fingerprint: peer.FingerprintOf([]byte("this computer"))}}
	out := captureStdout(t, func() { reportPaired(client, store, "Pixel 8", fp, []string{peer.GrantView}) })
	want := "Paired. scterm cannot give a command that selects only this device: scterm peers lists the pairings.\n" +
		"The device is " + fp.Short() + ", named \"Pixel 8\".\n" +
		"It allows this computer (" + client.Identity.Fingerprint.Short() + ") to view.\n"
	if out != want {
		t.Errorf("after pairing: %q\nwant %q", out, want)
	}
}
