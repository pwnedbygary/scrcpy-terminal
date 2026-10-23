package main

import (
	"slices"
	"strings"
	"testing"
	"unicode"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// A device's text never passes for scterm's own words: where scterm prints a
// command, none of it comes first, and where it shows a device, the name is
// data, after scterm's words, in quotes, with escapes for what would not show
// as written, and short enough that it cannot start a row of the terminal.
// ---------------------------------------------------------------------------

// blankOrHidden reports whether s holds a character that shows as a gap or as
// nothing, which could pad a row: a space other than the ASCII one, U+2800,
// a Hangul filler, or a control or format character other than a line break.
func blankOrHidden(s string) bool {
	return unclean(s) || strings.ContainsFunc(s, func(r rune) bool {
		return r == '\u2800' || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.IsSpace(r) && r != ' ' && r != '\n'
	})
}

// TestPairingPrintsTheCommandFirst: after a pairing, scterm prints the command
// to connect first, so no text of the device's comes before it, then the
// device as data: its identity, and its name in double quotes, with Go's
// escapes for what would not show as written and a backslash before ", \, $,
// the backquote and !, cut to 32 cells (a character that takes none counting
// as one) and then "…" after the quotes.
// Every line starts with scterm's own words, the first "Connect with" is
// scterm's, and the lines that show the name fit in 80 columns, so no part of
// a name can start a row.
func TestPairingPrintsTheCommandFirst(t *testing.T) {
	client := &peer.Client{Identity: &peer.Identity{Fingerprint: peer.FingerprintOf([]byte("this computer"))}}
	allowed := "It allows this computer (" + client.Identity.Fingerprint.Short() + ") to view.\n"
	for _, tc := range []struct {
		name, arg, shown string // arg "": the short identity; shown "": no name
	}{
		{"Pixel 8", "'Pixel 8'", `"Pixel 8"`},
		{"O'Brien's Pixel", `'O'\''Brien'\''s Pixel'`, `"O'Brien's Pixel"`},
		{`say "hi" \o/`, "", `"say \"hi\" \\o/"`},
		{"$HOME `id`", "", "\"\\$HOME \\`id\\`\""},
		{"Hi!", "", `"Hi\!"`},
		{"x;touch MARKER #", "", `"x;touch MARKER #"`},
		{"Pixel\nPaired. Connect with: scterm --peer evil", "", `"Pixel\nPaired. Connect with: sct"…`},
		{"Pixel" + strings.Repeat("\u2800", 100) + "Connect with: scterm --peer evil", "", `"Pixel\u2800\u2800\u2800\u2800"…`},
		{strings.Repeat("\u3164", 40) + "Connect with", "", `"\u3164\u3164\u3164\u3164\u3164"…`},
		{"Pixel" + strings.Repeat("\u3000", 40) + "x", "", `"Pixel\u3000\u3000\u3000\u3000"…`},
		{"Pixel" + strings.Repeat(" ", 40) + "x", "", `"Pixel` + strings.Repeat(" ", 27) + `"…`},
		{strings.Repeat("a", 300), "", `"` + strings.Repeat("a", 32) + `"…`},
		{strings.Repeat("'", 11), "", `"'''''''''''"`}, // 46 cells in the command, in quotes
		{"a" + strings.Repeat("\u0301", 40), "", `"a` + strings.Repeat("\u0301", 31) + `"…`},
		{strings.Repeat("日本", 20), "", `"` + strings.Repeat("日本", 8) + `"…`},
		{"Pixel 8\u202e\u2066\u200b\u00ad", "", `"Pixel 8\u202e\u2066\u200b\u00ad"`},
		{"Pixel\x9b8", "", `"Pixel\x9b8"`},
		{dirtyPixel8, "", `"Pixel\x1b]0;owned\a 8\u202e"`},
		{"", "", ""},
	} {
		store := pairedStore(t)
		fp := peer.FingerprintOf([]byte(tc.name))
		if err := store.Put(peer.Record{Fingerprint: fp.Hex(), Name: tc.name, Host: "192.0.2.1", Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1}); err != nil {
			t.Fatal(err)
		}
		arg, device := tc.arg, "The device is "+fp.Short()+", named "+tc.shown+".\n"
		if arg == "" {
			arg = fp.Short()
		}
		if tc.shown == "" {
			device = "The device is " + fp.Short() + ", with no name.\n"
		}
		out := captureStdout(t, func() { reportPaired(client, store, tc.name, fp, []string{peer.GrantView}) })
		if want := "Paired. Connect with: scterm --peer " + arg + "\n" + device + allowed; out != want {
			t.Errorf("%q: printed %q\nwant    %q", tc.name, out, want)
		}
		if i := strings.Index(out, "Connect with"); i != len("Paired. ") || blankOrHidden(out) {
			t.Errorf("%q: the first Connect with is at %d of %q", tc.name, i, out)
		}
		lines := strings.SplitAfter(out, "\n")
		for i, line := range lines[:len(lines)-1] {
			if !strings.HasPrefix(line, []string{"Paired. ", "The device is ", "It allows this computer ("}[min(i, 2)]) {
				t.Errorf("%q: line %d does not start with scterm's words: %q", tc.name, i+1, line)
			}
			if i < 2 && cellWidth(line) > 80 {
				t.Errorf("%q: line %d is %d cells: %q", tc.name, i+1, cellWidth(line), line)
			}
		}
	}
}

// TestDataLineNamePastesAsOneWord: the name on the line after the command,
// copied with its quotes into a command (scterm --peer "NAME"), is one word in
// every shell, and nothing in it is substituted or taken from the history: no
// MARKER file, no variable read. scterm gets the name, but for the backslashes
// a shell keeps: fish reads no backquote, so it keeps the one before a
// backquote, and only an interactive zsh takes out the one before a !.
func TestDataLineNamePastesAsOneWord(t *testing.T) {
	cases := []struct{ name, fish string }{ // fish: what fish gives scterm, when not the name
		{name: "$(touch MARKER-subst)"},
		{name: "`touch MARKER-backtick`", fish: "\\`touch MARKER-backtick\\`"},
		{name: "$HOME"},
		{name: "${PATH}"},
		{name: `x"$(touch MARKER-quote)"`},
		{name: `\$(touch MARKER-backslash)`},
		{name: "!#;touch MARKER-bang;#"}, // to an interactive bash, !# is the line so far, opening quote and all
		{name: "Hi!"},                    // an interactive zsh takes !" out, closing quote and all
	}
	client := &peer.Client{Identity: &peer.Identity{Fingerprint: peer.FingerprintOf([]byte("this computer"))}}
	commands := make([]string, len(cases))
	for i, c := range cases {
		store := pairedStore(t)
		fp := peer.FingerprintOf([]byte(c.name))
		if err := store.Put(peer.Record{Fingerprint: fp.Hex(), Name: c.name, Host: "192.0.2.1", Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1}); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() { reportPaired(client, store, c.name, fp, []string{peer.GrantView}) })
		_, quoted, _ := strings.Cut(out, "\nThe device is "+fp.Short()+", named ")
		quoted, _, ok := strings.Cut(quoted, ".\n")
		if !ok {
			t.Fatalf("%q: printed no line that names the device: %q", c.name, out)
		}
		commands[i] = "scterm --peer " + quoted
	}
	for _, sh := range pasteShells {
		t.Run(sh.name, func(t *testing.T) {
			got := pasteInto(t, sh, commands)
			for i, c := range cases {
				want := c.name
				if sh.name == "fish" && c.fish != "" {
					want = c.fish
				}
				if !strings.HasPrefix(sh.name, "zsh -i") {
					want = strings.ReplaceAll(want, "!", `\!`)
				}
				if !slices.Equal(got[i], []string{"--peer", want}) {
					t.Errorf("%q: pasted %s, scterm got %q; want --peer %q", c.name, commands[i], got[i], want)
				}
			}
		})
	}
}

// TestForgetShowsTheDeviceAsData: scterm forget shows the device it means on a
// line of its own, as data, as after a pairing; its question, and what it says
// it did, name the device by its identity, so that no name can pose as either.
func TestForgetShowsTheDeviceAsData(t *testing.T) {
	const name = "Pixel? [y/N] y\nForgot Victim. Its own list"
	fp := peer.FingerprintOf([]byte(name))
	device := "The device is " + fp.Short() + `, named "Pixel? [y/N] y\nForgot Victim. I"….` + "\n"
	forgot := "Forgot " + fp.Short() + ". Its own list still names this computer until it is removed there.\n"
	old := terminalInput
	t.Cleanup(func() { terminalInput = old })
	for _, tc := range []struct {
		interactive          bool
		query, answer        string
		asked, said, wantErr string
		left                 int
	}{
		{true, "pix", "n\n", device + "Forget " + fp.Short() + "? [y/N] ", "Kept " + fp.Short() + ".\n", "", 1},
		{true, "pix", "y\n", device + "Forget " + fp.Short() + "? [y/N] ", forgot, "", 0},
		{false, name, "", "", device + forgot, "", 0},
		{false, fp.Short(), "", "", device + forgot, "", 0},
		{false, "pix", "", "", "", `"pix" only partly matches a paired device: to forget it, run scterm forget ` + fp.Short(), 1},
	} {
		withInteractive(t, tc.interactive)
		store := pairedStore(t)
		if err := store.Put(peer.Record{Fingerprint: fp.Hex(), Name: name, Host: "192.0.2.1", Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1}); err != nil {
			t.Fatal(err)
		}
		terminalInput = bufioReader(tc.answer)
		var err error
		var asked string
		said := captureStdout(t, func() {
			asked = captureStderr(t, func() { err = forgetCommand([]string{tc.query}) })
		})
		if got := errText(err); said != tc.said || asked != tc.asked || got != tc.wantErr {
			t.Errorf("forget %q, answering %q: stdout %q, stderr %q, error %q\nwant stdout %q, stderr %q, error %q", tc.query, tc.answer, said, asked, got, tc.said, tc.asked, tc.wantErr)
		}
		if recs, err := store.All(); err != nil || len(recs) != tc.left {
			t.Errorf("forget %q, answering %q, left %d pairings, %v; want %d", tc.query, tc.answer, len(recs), err, tc.left)
		}
	}
}

// errText is err's message, or "" for none.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
