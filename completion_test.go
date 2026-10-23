package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// Shell completion: scterm completion bash|zsh|fish prints a script that
// completes the commands, the flags scterm -h lists and their fixed values,
// and the pairing argument of --peer, --peer= and scterm forget, which the
// script asks scterm __complete peers for. A pairing's name is the device's
// own text, so in each real shell, what completion inserts must run nothing,
// show no control character, and give scterm that pairing.
// ---------------------------------------------------------------------------

// The bundled server's audio codecs and sources (AudioCodec.java and
// AudioSource.java), sorted.
var (
	serverCodecs  = []string{"aac", "flac", "opus", "raw"}
	serverSources = []string{"mic", "mic-camcorder", "mic-unprocessed", "mic-voice-communication", "mic-voice-recognition",
		"output", "playback", "voice-call", "voice-call-downlink", "voice-call-uplink", "voice-performance"}
)

// completed is what scterm __complete peers prints, run with args as the
// scripts run it, one line to an element.
func completed(t *testing.T, args ...string) []string {
	t.Helper()
	return completedFor(t, "peers", args...)
}

// completedFor is completed for what (peers or forget).
func completedFor(t *testing.T, what string, args ...string) []string {
	t.Helper()
	handled := false
	out := captureStdout(t, func() { handled = runCompletionCommand(append([]string{"__complete", what}, args...)) })
	if !handled {
		t.Fatalf("__complete %s %q was not handled", what, args)
	}
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

// storedNames are the names of pasteNames that the store keeps otherwise. It
// writes JSON, which takes each byte that is not UTF-8 as U+FFFD
// (encoding/json), so the name read back is plain text, and completion,
// which reads the store, names the pairing by it.
var storedNames = map[string]string{"Pixel\x9b": "Pixel\uFFFD"}

// insertedArg is what completion inserts for p where bash and zsh insert it
// as written: the argument pasteNames gives, else its identity.
func (p pastePairing) insertedArg() string {
	switch name, ok := storedNames[p.rec.Name]; {
	case ok:
		return "'" + name + "'"
	case p.arg == "":
		return p.fp.Short()
	}
	return p.arg
}

// value is what scterm gets from what completion inserts for p: the name,
// or the identity.
func (p pastePairing) value() string {
	if name, ok := storedNames[p.rec.Name]; ok {
		return name
	}
	return p.want()
}

// TestCompletePeersGivesEachPairingAsPasteArgNamesIt: for a word typed so
// far that is empty or a single quote, scterm __complete peers gives every
// pairing as pasteArg names it, from the name in the store, in single quotes
// or by its identity, in the order of the pairings; without a word, the
// value itself, for a shell that quotes it. Nothing it prints has a control
// character, or any other character that does not print.
func TestCompletePeersGivesEachPairingAsPasteArgNamesIt(t *testing.T) {
	_, pairings := pastePairings(t)
	var args, values []string
	for _, p := range pairings {
		args, values = append(args, p.insertedArg()), append(values, p.value())
	}
	outputs := map[string][]string{"no word": completed(t), "an empty word": completed(t, ""), "a quote": completed(t, "'")}
	for name, want := range map[string][]string{"no word": values, "an empty word": args, "a quote": args} {
		if got := outputs[name]; !slices.Equal(got, want) {
			t.Errorf("%s: %q\nwant %q", name, got, want)
		}
	}
	for name, lines := range outputs {
		for _, l := range lines {
			if !utf8.ValidString(l) || strings.ContainsFunc(l, func(r rune) bool { return !unicode.IsPrint(r) }) {
				t.Errorf("%s: %q does not print as it is", name, l)
			}
		}
	}
}

// startingWith is what completion offers bash for a word that says prefix:
// each of pairings whose name, or identity when that names it, starts with
// prefix, in any case, as it is inserted.
func startingWith(pairings []pastePairing, prefix string) []string {
	var args []string
	for _, p := range pairings {
		if strings.HasPrefix(strings.ToLower(p.value()), strings.ToLower(prefix)) {
			args = append(args, p.insertedArg())
		}
	}
	return args
}

// TestCompletePeersMatchesWhatWasTyped: a word typed so far keeps the
// pairings whose name, or identity when that names them, starts with what
// the word says, in any case: unquoted, with a backslash taking the next
// character as it is, or after an opening single quote. A word with other
// quoting, or an expansion, gets nothing.
func TestCompletePeersMatchesWhatWasTyped(t *testing.T) {
	_, pairings := pastePairings(t)
	cases := map[string][]string{
		"pix":             startingWith(pairings, "pix"),
		`PIXEL\ 8`:        {"'Pixel 8'"},
		"'pixel ":         startingWith(pairings, "pixel "),
		`o\'b`:            {`'O'\''Brien'\''s Pixel'`},
		`\$\(t`:           startingWith(pairings, "$(t"),
		"'$(":             startingWith(pairings, "$("),
		"nothing like it": nil,
		"$HOME":           nil,
		"`x":              nil,
		"'a'b":            nil,
		"'O'":             nil,
		"a'b":             nil,
		`a"b`:             nil,
		`"a"`:             nil,
		`"a\`:             nil,
		`"$H`:             nil,
		"$'x":             nil,
	}
	// The identity of the first name given by its identity, typed in
	// lowercase, and its start after a quote.
	for _, p := range pairings {
		if id := p.fp.Short(); p.arg == "" {
			cases[strings.ToLower(id)] = []string{id}
			cases["'"+id[:4]] = startingWith(pairings, id[:4])
			break
		}
	}
	for word, want := range cases {
		if got := completed(t, word); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", word, got, want)
		}
	}
	// No candidate has a $ or a backquote (plainName refuses both), so a word
	// with one completes to nothing whether it is read or not: typedArg must
	// read them escaped or in single quotes, and refuse an expansion.
	for word, want := range map[string]string{`\$\(t`: "$(t", "'$(": "$(", "'`x": "`x"} {
		if prefix, _, ok := typedArg(word); !ok || prefix != want {
			t.Errorf("typedArg(%s): %q, %v; want %q", word, prefix, ok, want)
		}
	}
	for _, word := range []string{"$HOME", "`x", "x$", `a\b$c`, `"$H`, "\"`x"} {
		if prefix, _, ok := typedArg(word); ok {
			t.Errorf("typedArg(%s) reads %q", word, prefix)
		}
	}
}

// TestCompletePeersDoubleQuotesAfterADoubleQuote: after a double quote the
// user opened, each candidate is the name or identity in double quotes, as
// bash reads it: \, ", $ and the backquote after a backslash, and ! in
// single quotes between double-quoted parts, since an interactive bash
// expands history in double quotes.
func TestCompletePeersDoubleQuotesAfterADoubleQuote(t *testing.T) {
	_, pairings := pastePairings(t)
	got := completed(t, `"`)
	if len(got) != len(pairings) {
		t.Fatalf("%d candidates for %d pairings: %q", len(got), len(pairings), got)
	}
	// Each name here, when it names its pairing, in double quotes.
	quoted := map[string]string{
		"Pixel 8":                 `"Pixel 8"`,
		"$(touch MARKER-subst)":   `"\$(touch MARKER-subst)"`,
		"`touch MARKER-backtick`": "\"\\`touch MARKER-backtick\\`\"",
		"$HOME":                   `"\$HOME"`,
		"O'Brien's Pixel":         `"O'Brien's Pixel"`,
		`say "hi"`:                `"say \"hi\""`,
		"Hi!x":                    `"Hi"'!'"x"`,
	}
	var pix []string // the candidates for "pix, none of them with a character that needs a backslash
	for i, p := range pairings {
		arg := p.insertedArg()
		if strings.HasPrefix(strings.ToLower(p.value()), "pix") {
			pix = append(pix, `"`+p.value()+`"`)
		}
		switch w, ok := quoted[p.rec.Name]; {
		case !strings.HasPrefix(arg, "'") && got[i] != `"`+arg+`"`:
			t.Errorf("%q: %s, want its identity in double quotes", p.rec.Name, got[i])
		case strings.HasPrefix(arg, "'") && ok && got[i] != w:
			t.Errorf("%q: %s, want %s", p.rec.Name, got[i], w)
		}
	}
	if got := completed(t, `"pix`); !slices.Equal(got, pix) {
		t.Errorf(`"pix: %q, want %q`, got, pix)
	}
}

// TestDoubleQuotedReadsBackAsItIs: text in double quotes as completion writes
// it reads back as it is, in bash running a script and in an interactive
// bash, whatever bash reads in double quotes: \, ", $, the backquote and !,
// even text that no name has (pasteArg names no pairing by a name with a
// backslash).
func TestDoubleQuotedReadsBackAsItIs(t *testing.T) {
	needShell(t, "/usr/bin/bash")
	texts := []string{`a\b`, `ends\`, `\"`, `say "hi"`, "$HOME", "$(touch MARKER-subst)", "`touch MARKER-backtick`",
		"Hi!x", "!!", "a!b!c", "!", "O'Brien", "'!'"}
	lines := make([]string, len(texts))
	for i, s := range texts {
		lines[i] = "scterm " + doubleQuoted(s)
	}
	for _, mode := range []string{"bash", "bash -i"} {
		got := pasteInto(t, pasteShellNamed(t, mode), lines)
		for i, s := range texts {
			if !slices.Equal(got[i], []string{s}) {
				t.Errorf("%s: %s gave scterm %q, want %q", mode, lines[i], got[i], s)
			}
		}
	}
}

// TestCompletePeersLeavesOutWhatNothingNames: a pairing that pasteArg cannot
// name is left out, whatever the word: here one whose name is not plain text,
// and whose identity another pairing has too (that one, Tablet, goes by its
// name). So is each pairing whose identity does not parse, which no name or
// identity selects, a plain name included; after scterm forget, the word that
// forgets those pairings, --damaged, comes instead, last, for the words it
// starts with. Completing leaves nothing in SCTERM_HOME but the pairings: not
// this computer's identity. Pairings that cannot be read give nothing, and
// none at all give nothing, without making anything in SCTERM_HOME.
func TestCompletePeersLeavesOutWhatNothingNames(t *testing.T) {
	victim := peer.FingerprintOf([]byte("victim"))
	writePairings(t, pairing(victim.Hex(), dirtyPixel8, "192.0.2.1"), pairing(victim.Hex(), "Tablet", "192.0.2.2"),
		pairing("not-a-fingerprint", "Galaxy", "192.0.2.3"), pairing(victim.Hex()[:63], "Old phone", "192.0.2.4"))
	home := os.Getenv("SCTERM_HOME")
	for _, tc := range []struct {
		what string
		args []string
		want []string
	}{
		{"peers", nil, []string{"Tablet"}},
		{"peers", []string{""}, []string{"'Tablet'"}},
		{"peers", []string{"'"}, []string{"'Tablet'"}},
		{"peers", []string{`"`}, []string{`"Tablet"`}},
		{"peers", []string{"-"}, nil},
		{"peers", []string{"gal"}, nil},
		{"peers", []string{"old"}, nil},
		{"forget", nil, []string{"Tablet", "--damaged"}},
		{"forget", []string{""}, []string{"'Tablet'", "--damaged"}},
		{"forget", []string{"'"}, []string{"'Tablet'", "--damaged"}},
		{"forget", []string{`"`}, []string{`"Tablet"`, `"--damaged"`}},
		{"forget", []string{"--d"}, []string{"--damaged"}},
		{"forget", []string{"--DAM"}, []string{"--damaged"}},
		{"forget", []string{"t"}, []string{"'Tablet'"}},
		{"forget", []string{"gal"}, nil},
		{"forget", []string{"--damaged-x"}, nil},
	} {
		if got := completedFor(t, tc.what, tc.args...); !slices.Equal(got, tc.want) {
			t.Errorf("%s %q: %q, want %q", tc.what, tc.args, got, tc.want)
		}
	}
	if left, _ := os.ReadDir(home); len(left) != 1 || left[0].Name() != "peers.json" {
		t.Errorf("SCTERM_HOME holds %v after completing; want peers.json alone", left)
	}

	if err := os.WriteFile(filepath.Join(home, "peers.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := append(completed(t, ""), completedFor(t, "forget", "")...); got != nil {
		t.Errorf("pairings that cannot be read: %q", got)
	}
	empty := t.TempDir()
	t.Setenv("SCTERM_HOME", empty)
	if got := append(append(completed(t), completed(t, "")...), completedFor(t, "forget", "")...); got != nil {
		t.Errorf("no pairings: %q", got)
	}
	if made, _ := os.ReadDir(empty); len(made) > 0 {
		t.Errorf("completion made %v in SCTERM_HOME", made)
	}
}

// TestCompletionGivesDamagedOnlyForTheCommand: scterm forget takes --damaged
// for the command, never for a name, so a device named --damaged, or with a
// name a paste can turn into it, completes to its identity, as pasteArg names
// it; and --damaged itself is offered after scterm forget alone, and only
// while a pairing's identity does not parse.
func TestCompletionGivesDamagedOnlyForTheCommand(t *testing.T) {
	named, selector := peer.FingerprintOf([]byte("--damaged")), peer.FingerprintOf([]byte("--damaged\ufe0f"))
	devices := []peer.Record{pairing(named.Hex(), "--damaged", "192.0.2.1"), pairing(selector.Hex(), "--damaged\ufe0f", "192.0.2.2")}
	ids := []string{named.Short(), selector.Short()}
	writePairings(t, devices...)
	for _, what := range []string{"peers", "forget"} {
		if got := completedFor(t, what, ""); !slices.Equal(got, ids) {
			t.Errorf("%s, no pairing damaged: %q, want %q", what, got, ids)
		}
		if got := completedFor(t, what, "--d"); got != nil {
			t.Errorf("%s --d, no pairing damaged: %q, want nothing", what, got)
		}
	}
	writePairings(t, append(devices, pairing("not-a-fingerprint", "Pixel", "192.0.2.3"))...)
	for _, tc := range []struct {
		what string
		args []string
		want []string
	}{
		{"peers", []string{""}, ids},
		{"peers", []string{"--d"}, nil},
		{"forget", []string{""}, append(slices.Clone(ids), "--damaged")},
		{"forget", nil, append(slices.Clone(ids), "--damaged")},
		{"forget", []string{"--d"}, []string{"--damaged"}},
	} {
		if got := completedFor(t, tc.what, tc.args...); !slices.Equal(got, tc.want) {
			t.Errorf("%s %q, a pairing damaged: %q, want %q", tc.what, tc.args, got, tc.want)
		}
	}
}

// TestCompletionReadsThePairingsOnce: scterm __complete reads the pairings
// once, however many there are, where calling pasteArg for each pairing would
// read them again for each name and identity it tries. Here peers.json is a
// FIFO, which gives its content to one read alone: a second read waits for a
// writer that never comes. Runs the helper in a child process.
func TestCompletionReadsThePairingsOnce(t *testing.T) {
	inCompletionChild()
	home := t.TempDir()
	path := filepath.Join(home, "peers.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	pixel, dirty := peer.FingerprintOf([]byte("pixel")), peer.FingerprintOf([]byte("dirty"))
	data, err := json.Marshal([]peer.Record{pairing(pixel.Hex(), "Pixel 8", "192.0.2.1"), pairing(dirty.Hex(), dirtyPixel8, "192.0.2.2"),
		pairing("not-a-fingerprint", "Old", "192.0.2.3")})
	if err != nil {
		t.Fatal(err)
	}
	wrote := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0) // waits for the reader
		if err == nil {
			_, err = f.Write(data)
			err = errors.Join(err, f.Close())
		}
		wrote <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCompletionReadsThePairingsOnce$", "--", "__complete", "forget", "")
	cmd.Env = []string{"SCTERM_TEST_COMPLETION=1", "SCTERM_HOME=" + home, "PATH=" + t.TempDir()}
	if dir, ok := os.LookupEnv("GOCOVERDIR"); ok {
		// Under go test -cover the child warns on stderr without it.
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}
	out, err := cmd.Output()
	if want := "'Pixel 8'\n" + dirty.Short() + "\n--damaged\n"; err != nil || string(out) != want {
		t.Errorf("scterm __complete forget '': %v, %q; want %q, read once (a second read waits until the child is killed)", err, out, want)
	}
	if r, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
		r.Close() // lets the writer go when nothing read
	}
	if err := <-wrote; err != nil {
		t.Errorf("writing the pairings: %v", err)
	}
}

// TestCompletionNamesEachPairingAsPasteArgDoes: completion works out each
// pairing's argument from the pairings it read once (pasteArgFor, which
// searches them with peer.FindIn), and names every pairing exactly as
// pasteArg does from the store, in the order of the pairings. The stores:
// pastecmd_test.go's pairings, one pairing alone, two pairings with one
// identity, two whose short identities are the same, devices named after
// another's identity, damaged namesakes, identities stored otherwise than
// scterm writes them, and names that a terminal's copy can change, each of
// which goes by its name or its identity as everyCopyFinds says: a mark
// that makes Pixel another name, a keycap (two characters a terminal may
// leave out), the twin by case folding with a Hangul vowel, and a Hangul
// vowel inside a name, whose copy is another's name.
func TestCompletionNamesEachPairingAsPasteArgDoes(t *testing.T) {
	victim, other := peer.FingerprintOf([]byte("victim")), peer.FingerprintOf([]byte("other"))
	marked := peer.FingerprintOf([]byte("marked"))
	for _, tc := range []struct {
		name string
		open func() *peer.Store
		args map[string]string // what completion gives for these pairings, by name
	}{
		{"pastecmd_test.go's", func() *peer.Store { store, _ := pastePairings(t); return store }, nil},
		{"one pairing", func() *peer.Store { return writePairings(t, pairing(victim.Hex(), "Pixel 8", "192.0.2.1")) }, nil},
		{"one identity twice", func() *peer.Store {
			return writePairings(t, pairing(victim.Hex(), dirtyPixel8, "192.0.2.1"), pairing(victim.Hex(), "Tablet", "192.0.2.2"))
		}, nil},
		{"one short identity twice", func() *peer.Store { return pairedStore(t, dirtyPixel8, dirtyPixel9, "Pixel 8") }, nil},
		{"named after identities", func() *peer.Store {
			return writePairings(t, pairing(victim.Hex(), dirtyPixel8, "192.0.2.1"), pairing(other.Hex(), victim.Short(), "192.0.2.2"),
				pairing(peer.FingerprintOf([]byte("third")).Hex(), victim.Hex()[:32], "192.0.2.3"))
		}, nil},
		{"damaged namesakes", func() *peer.Store {
			return writePairings(t, pairing(victim.Hex(), "Pixel 8", "192.0.2.1"), pairing("not-a-fingerprint", "Pixel 8", "192.0.2.9"),
				pairing(other.Hex(), "not-a-fingerprint", "192.0.2.2"), pairing(victim.Hex()[:63], "Old phone", "192.0.2.8"))
		}, nil},
		{"edited by hand", func() *peer.Store {
			upper, colons, spaced, shouty := editedPairings(t)
			return writePairings(t, upper, colons, spaced, shouty, pairing(victim.Hex(), "upper", "192.0.2.1"))
		}, nil},
		{"a mark beside a name", func() *peer.Store {
			return writePairings(t, pairing(marked.Hex(), "Pix\ufe0fel", "192.0.2.1"), pairing(victim.Hex(), "Pixel", "192.0.2.2"))
		}, map[string]string{"Pix\ufe0fel": marked.Short(), "Pixel": "'Pixel'"}},
		{"a keycap", func() *peer.Store { return writePairings(t, pairing(marked.Hex(), "Room 1\ufe0f\u20e3", "192.0.2.1")) },
			map[string]string{"Room 1\ufe0f\u20e3": marked.Short()}},
		{"a twin by case folding", func() *peer.Store {
			return writePairings(t, pairing(marked.Hex(), "ſam's Pixel ❤\ufe0f\u1161", "192.0.2.1"),
				pairing(victim.Hex(), "Sam's Pixel ❤\ufe0f", "192.0.2.2"))
		}, map[string]string{"ſam's Pixel ❤\ufe0f\u1161": marked.Short(), "Sam's Pixel ❤\ufe0f": `'Sam'\''s Pixel ❤` + "\ufe0f'"}},
		{"a Hangul vowel inside", func() *peer.Store {
			return writePairings(t, pairing(marked.Hex(), "Pix\u1161el", "192.0.2.1"), pairing(victim.Hex(), "Pixel\u1161", "192.0.2.2"))
		}, map[string]string{"Pix\u1161el": marked.Short(), "Pixel\u1161": "'Pixel\u1161'"}},
	} {
		store := tc.open()
		recs, err := store.All()
		if err != nil {
			t.Fatal(err)
		}
		got := completed(t, "")
		var want []string
		for _, r := range recs {
			fp, err := r.ID()
			if err != nil {
				continue
			}
			arg, ok := pasteArg(store, r.Name, fp)
			if !ok {
				continue
			}
			if w, listed := tc.args[r.Name]; listed && (len(got) <= len(want) || got[len(want)] != w) {
				t.Errorf("%s: %+q: completion does not give %+q", tc.name, r.Name, w)
			}
			want = append(want, arg)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: completion gives %+q\nwant %+q, pasteArg's", tc.name, got, want)
		}
	}
}

// TestCompletionIsListedButNotItsHelper: scterm -h lists completion under
// Commands, naming the shells, and the README has it in its table of
// commands and says how to load each script. The scripts' helper, scterm
// __complete, is in neither, nor in docs/ or scterm completion -h.
func TestCompletionIsListedButNotItsHelper(t *testing.T) {
	_, help, entries := readHelp(t)
	listed := false
	for _, e := range entries {
		listed = listed || e.heading == "Commands" && strings.HasPrefix(strings.TrimSpace(e.lines[0]), "completion bash|zsh|fish ")
	}
	if !listed {
		t.Error("scterm -h does not list completion bash|zsh|fish under Commands")
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, _ := strings.Cut(string(readme), "\n### Commands\n")
	table, _, _ = strings.Cut(table, "\n#")
	if !strings.Contains(table, "\n| `scterm completion bash\\|zsh\\|fish` |") {
		t.Error("README.md does not list scterm completion under ### Commands")
	}
	_, section, _ := strings.Cut(string(readme), "\n### Shell completion\n")
	section, _, _ = strings.Cut(section, "\n#")
	for _, line := range []string{"source <(scterm completion bash)", "source <(scterm completion zsh)",
		"scterm completion fish > ~/.config/fish/completions/scterm.fish"} {
		if !strings.Contains(section, line) {
			t.Errorf("README.md's ### Shell completion does not give %q", line)
		}
	}
	texts := map[string]string{"scterm -h": help, "scterm completion -h": completionHelp, "README.md": string(readme)}
	docs, _ := filepath.Glob("docs/*.md")
	for _, name := range docs {
		text, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		texts[name] = string(text)
	}
	for name, text := range texts {
		if strings.Contains(text, "__complete") {
			t.Errorf("%s names the hidden helper, __complete", name)
		}
	}
}

// helpCommands gives the commands scterm -h lists, and its flags, as it
// writes them.
func helpCommands(t *testing.T) (commands, flags []string) {
	t.Helper()
	_, _, entries := readHelp(t)
	for _, e := range entries {
		first := strings.TrimSpace(e.lines[0])
		switch {
		case strings.HasPrefix(first, "-"):
			names, _ := flagNames(e)
			for _, n := range names {
				flags = append(flags, dashed(n))
			}
		case e.heading == "Commands":
			if word, _, _ := strings.Cut(first, " "); word != "COMMAND" {
				commands = append(commands, word)
			}
		}
	}
	return commands, flags
}

// commandArgs is what completes the first word after each command scterm -h
// lists: its options, the shells, or (as nil) the pairings for forget; a
// command with nothing to complete has an empty list.
var commandArgs = map[string][]string{
	"pair": {}, "peers": {"--check"}, "forget": nil, "doctor": {}, "update": {"--check", "-y", "--yes"},
	"version": {}, "licenses": {}, "completion": {"bash", "fish", "zsh"}, "help": {},
}

// needShell skips the test when the shell at path is not installed, or, with
// SCTERM_TEST_REQUIRE_SHELLS set to a value that is not empty (for CI), fails
// it.
func needShell(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		return
	}
	if os.Getenv("SCTERM_TEST_REQUIRE_SHELLS") != "" {
		t.Fatalf("%s is not installed, and SCTERM_TEST_REQUIRE_SHELLS is set", path)
	}
	t.Skipf("%s is not installed", path)
}

// TestAMissingShellFailsWhenRequired: a test that needs a shell that is not
// installed skips, unless SCTERM_TEST_REQUIRE_SHELLS is set to a value that
// is not empty: then it fails. Runs needShell in a child process
// (SCTERM_TEST_MISSING_SHELL), for a shell in an empty directory.
func TestAMissingShellFailsWhenRequired(t *testing.T) {
	if _, ok := os.LookupEnv("SCTERM_TEST_MISSING_SHELL"); ok {
		needShell(t, filepath.Join(t.TempDir(), "zsh"))
		return
	}
	for _, tc := range []struct {
		env  []string
		says string
	}{
		{nil, "--- SKIP"},
		{[]string{"SCTERM_TEST_REQUIRE_SHELLS="}, "--- SKIP"},
		{[]string{"SCTERM_TEST_REQUIRE_SHELLS=1"}, "--- FAIL"},
		{[]string{"SCTERM_TEST_REQUIRE_SHELLS=yes"}, "--- FAIL"},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAMissingShellFailsWhenRequired$", "-test.v")
		cmd.Env = append([]string{"SCTERM_TEST_MISSING_SHELL=1"}, tc.env...)
		if dir, ok := os.LookupEnv("GOCOVERDIR"); ok {
			// Under go test -cover the child warns on stderr without it.
			cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
		}
		out, err := cmd.CombinedOutput()
		if status, want := childStatus(err), map[string]int{"--- SKIP": 0, "--- FAIL": 1}[tc.says]; status != want ||
			!strings.Contains(string(out), tc.says+": TestAMissingShellFailsWhenRequired") || !strings.Contains(string(out), "zsh is not installed") {
			t.Errorf("%q: status %d, output %q; want %d, %s and the shell named", tc.env, status, out, want, tc.says)
		}
	}
}

// inCompletionChild runs main with the arguments after -- and exits, when
// this process is the test binary standing in for scterm on the PATH of a
// shell under test (SCTERM_TEST_COMPLETION).
func inCompletionChild() {
	if _, ok := os.LookupEnv("SCTERM_TEST_COMPLETION"); !ok {
		return
	}
	os.Args = append([]string{"scterm"}, flag.Args()...)
	main()
	os.Exit(0)
}

// completionShell is a shell for the test this process runs: its PATH holds
// scterm, which is this test binary running main from the top of that test
// (inCompletionChild), and an adb that notes each run in adb.log. Its
// environment has nothing else of this computer's: a new HOME, SCTERM_HOME,
// a UTF-8 locale and an xterm.
type completionShell struct {
	dir    string // holds bin/, the scripts and adb.log
	env    []string
	script map[string]string // each shell's completion script, as a file
}

func newCompletionShell(t *testing.T, sctermHome string) *completionShell {
	t.Helper()
	test, _, _ := strings.Cut(t.Name(), "/")
	s := &completionShell{dir: t.TempDir(), script: map[string]string{}}
	bin := filepath.Join(s.dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"bin/scterm": "#!/bin/sh\nSCTERM_TEST_COMPLETION=1 exec '" + os.Args[0] + "' -test.run='^" + test + "$' -- \"$@\"\n",
		"bin/adb":    "#!/bin/sh\necho \"$*\" >> '" + filepath.Join(s.dir, "adb.log") + "'\nexit 1\n",
	}
	for _, shell := range completionShells {
		script, _ := completionScript(shell)
		s.script[shell] = filepath.Join(s.dir, "scterm."+shell)
		files["scterm."+shell] = script
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(s.dir, name), []byte(text), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	s.env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "SCTERM_HOME=" + sctermHome,
		"TERM=xterm", "LC_ALL=C.UTF-8"}
	if dir, ok := os.LookupEnv("GOCOVERDIR"); ok {
		// Under go test -cover the child warns on stderr without it.
		s.env = append(s.env, "GOCOVERDIR="+dir)
	}
	// Under go test -race, scterm, this test binary, would otherwise wait a
	// second as it exits (the race detector's atexit_sleep_ms), at each Tab.
	s.env = append(s.env, "GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	t.Cleanup(func() {
		if calls, err := os.ReadFile(filepath.Join(s.dir, "adb.log")); err == nil {
			t.Errorf("completion ran adb: %q", calls)
		}
	})
	return s
}

// shellQuoted is s in single quotes, for sh, bash and zsh, and for fish when
// no backslash in s comes before a backslash or a quote (fish reads \\ and \'
// in single quotes).
func shellQuoted(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// noMarkers fails the test when a shell that completed in dir ran one of the
// commands in pasteNames: dir holds a file whose name starts with MARKER.
func noMarkers(t *testing.T, dir string) {
	t.Helper()
	if made, _ := filepath.Glob(filepath.Join(dir, "MARKER*")); len(made) > 0 {
		t.Errorf("completing ran something: it made %q", made)
	}
}

// filesDir is a directory for completing --dump-frames DIR and --dump-audio
// FILE in: it holds the directory frames, and the files frames.raw and
// sound.raw.
func filesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "frames"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"frames.raw", "sound.raw"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestCompletionScriptsParse: each script is one its shell reads without a
// syntax error.
func TestCompletionScriptsParse(t *testing.T) {
	for shell, check := range map[string][]string{"bash": {"/usr/bin/bash", "-n"}, "zsh": {"/usr/bin/zsh", "-n"},
		"fish": {"/usr/bin/fish", "--no-config", "--no-execute"}} {
		t.Run(shell, func(t *testing.T) {
			needShell(t, check[0])
			script, ok := completionScript(shell)
			if !ok || !strings.HasPrefix(script, "# "+shell+" completion for scterm") {
				t.Fatalf("completionScript(%s): %v, %.60q", shell, ok, script)
			}
			cmd := exec.Command(check[0], check[1:]...)
			cmd.Stdin, cmd.Env = strings.NewReader(script), []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
			if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
				t.Errorf("%s %q: %v, %s", check[0], check[1:], err, out)
			}
		})
	}
}

// TestBashCompletesAsTheHelpSays: the bash script, sourced and called as
// bash calls it, with COMP_WORDS and COMP_CWORD set, completes the first
// word from the commands scterm -h lists, a word starting with a dash from
// its flags, and a flag's value from the server's codecs and sources, also
// after --flag= (which bash splits at the =, unless COMP_WORDBREAKS has no
// =), the paired devices for --peer and forget, and what each command takes.
// It completes no serial, no number, and no word after the flags. Each
// pairing it offers, inserted as written after scterm --peer, gives scterm
// that pairing and runs nothing, in bash running a script and in an
// interactive bash. In a terminal, it completes a directory for
// --dump-frames, a file for --dump-audio, and no file for -s, and with no =
// in COMP_WORDBREAKS, a whole --peer=VALUE word, but not after a quote in it.
func TestBashCompletesAsTheHelpSays(t *testing.T) {
	inCompletionChild()
	needShell(t, "/usr/bin/bash")
	_, pairings := pastePairings(t)
	sh := newCompletionShell(t, os.Getenv("SCTERM_HOME"))
	commands, flags := helpCommands(t)
	var all, peerPix []string // every pairing, as offered, and those for --peer=pix
	for _, p := range pairings {
		all = append(all, p.insertedArg())
	}
	pix := startingWith(pairings, "pix")
	for _, arg := range pix {
		peerPix = append(peerPix, "--peer="+arg)
	}
	type completion struct {
		words []string // COMP_WORDS, the last being the word completed
		want  []string // COMPREPLY, sorted
	}
	cases := []completion{
		{[]string{""}, commands},
		{[]string{"co"}, []string{"completion"}},
		{[]string{"-"}, flags},
		{[]string{"--audio-codec", ""}, serverCodecs},
		{[]string{"-audio-codec", "o"}, []string{"opus"}},
		{[]string{"--audio-codec", "="}, serverCodecs},
		{[]string{"--audio-codec", "=", "f"}, []string{"flac"}},
		{[]string{"--audio-codec=f"}, []string{"--audio-codec=flac"}},
		{[]string{"-n", "--audio-source", ""}, serverSources},
		{[]string{"--audio-source", "=", "mic-v"}, []string{"mic-voice-communication", "mic-voice-recognition"}},
		{[]string{"--peer", ""}, all},
		{[]string{"-peer", "="}, all},
		{[]string{"--peer", "=", "pix"}, pix},
		{[]string{"--peer=pix"}, peerPix},
		{[]string{`--peer=Pixel\ 8`}, []string{"--peer='Pixel 8'"}},
		{[]string{"--peer='pix"}, nil},
		{[]string{"--web", "--peer", "'O"}, []string{`'O'\''Brien'\''s Pixel'`}},
		{[]string{"forget", "pix"}, pix},
		{[]string{"forget", "pix", ""}, nil},
		{[]string{"update", "--c"}, []string{"--check"}},
		{[]string{"completion", "bash", ""}, nil},
		{[]string{"doctor", "-"}, nil},
		{[]string{"--web", ""}, nil},
		{[]string{"--web", "co"}, nil},
		{[]string{"-s", "-"}, nil},
		{[]string{"--dump-frames", ""}, nil},
		{[]string{"--dump-audio", ""}, nil},
	}
	for _, name := range []string{"-s", "--serial", "-m", "--max-size", "--max-fps", "-b", "--audio-bit-rate", "--web-port",
		"--web-addr", "--web-quality", "--window-size", "--repaint-interval"} {
		cases = append(cases, completion{[]string{name, ""}, nil})
	}
	for _, c := range commands {
		want, ok := commandArgs[c]
		if !ok {
			t.Errorf("scterm -h lists %s, for which this test has no completion", c)
		}
		if want == nil {
			want = all
		}
		cases = append(cases, completion{[]string{c, ""}, want})
	}
	script := "source " + shellQuoted(sh.script["bash"]) + "\n" +
		`try() { COMP_WORDS=(scterm "$@"); COMP_CWORD=$#; COMPREPLY=(); _scterm scterm "${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[COMP_CWORD-1]}"; ` +
		`printf '%s\n' "${COMPREPLY[@]}" '-- end --'; }` + "\n"
	for _, c := range cases {
		script += "try"
		for _, w := range c.words {
			script += " " + shellQuoted(w)
		}
		script += "\n"
	}
	cmd := exec.Command("/usr/bin/bash", "--norc", "--noprofile", "-c", script)
	cmd.Env, cmd.Dir = sh.env, t.TempDir()
	out, err := cmd.Output()
	noMarkers(t, cmd.Dir)
	blocks := strings.Split(strings.TrimSuffix(string(out), "-- end --\n"), "-- end --\n")
	if err != nil || len(blocks) != len(cases) {
		t.Fatalf("%d completions gave %d (%v): %q", len(cases), len(blocks), err, out)
	}
	for i, c := range cases {
		got := strings.Split(strings.TrimSuffix(blocks[i], "\n"), "\n")
		if blocks[i] == "" {
			got = nil
		}
		want := slices.Clone(c.want)
		if !slices.Equal(c.want, all) {
			slices.Sort(got)
			slices.Sort(want)
		}
		if !slices.Equal(got, want) {
			t.Errorf("scterm %q: %q\nwant %q", c.words, got, want)
		}
	}

	lines := make([]string, len(all))
	for i, arg := range all {
		lines[i] = "scterm --peer " + arg
	}
	for _, mode := range []string{"bash", "bash -i"} {
		got := pasteInto(t, pasteShellNamed(t, mode), lines)
		for i, p := range pairings {
			if want := []string{"--peer", p.value()}; !slices.Equal(got[i], want) {
				t.Errorf("%s: %s gave scterm %q, want %q", mode, lines[i], got[i], want)
			}
		}
	}

	// Directories and files come from bash itself (compopt), only while it
	// runs the completion: in a terminal. So does what readline does with a
	// whole --flag=VALUE word, which bash gives when COMP_WORDBREAKS has no
	// =: a candidate replaces the word, but none goes in after a quote.
	s := startPTYShell(t, sh, "bash", filesDir(t))
	n := 0 // the lines completed so far
	for _, c := range []struct{ keys, line string }{
		{"scterm --dump-frames fr", "scterm --dump-frames frames/"},
		{"scterm --dump-frames so", "scterm --dump-frames so"},
		{"scterm --dump-audio so", "scterm --dump-audio sound.raw "},
		{"scterm --dump-audio=so", "scterm --dump-audio=sound.raw "},
		{"scterm -s so", "scterm -s so"},
		{"COMP_WORDBREAKS=${COMP_WORDBREAKS//=}\r", ""},
		{`scterm --peer=Pixel\ 8`, "scterm --peer='Pixel 8' "},
		{"scterm --peer='Pixel 8", "scterm --peer='Pixel 8"},
		{"scterm --audio-codec=fl", "scterm --audio-codec=flac "},
	} {
		if c.line == "" {
			s.send(c.keys)
			continue
		}
		n++
		if line := s.complete(c.keys, n); line != c.line {
			t.Errorf("%q completes to %q, want %q", c.keys, line, c.line)
		}
	}
}

// pasteShellNamed is the shell of pasteShells with this name.
func pasteShellNamed(t *testing.T, name string) pasteShell {
	t.Helper()
	for _, sh := range pasteShells {
		if sh.name == name {
			return sh
		}
	}
	t.Fatalf("no paste shell named %q", name)
	return pasteShell{}
}

// TestFishCompletesAsTheHelpSays: fish, with the fish script sourced, gives
// for each command line what the bash script gives (complete -C, which
// lists the candidates as fish shows them, sorted), but the pairings, which
// are the values themselves: fish quotes each as it inserts it. fish offers a
// command's options, as it offers any, once the word starts with a dash.
func TestFishCompletesAsTheHelpSays(t *testing.T) {
	inCompletionChild()
	needShell(t, "/usr/bin/fish")
	_, pairings := pastePairings(t)
	sh := newCompletionShell(t, os.Getenv("SCTERM_HOME"))
	commands, flags := helpCommands(t)
	var all, pixel, pixelSpace, peerPixelSpace []string // every pairing; those whose value starts Pixel, and Pixel and a space
	for _, p := range pairings {
		v := p.value()
		all = append(all, v)
		if strings.HasPrefix(v, "Pixel") {
			pixel = append(pixel, v)
		}
		if strings.HasPrefix(v, "Pixel ") {
			pixelSpace, peerPixelSpace = append(pixelSpace, v), append(peerPixelSpace, "--peer="+v)
		}
	}
	cases := map[string][]string{
		"scterm ":                    commands,
		"scterm co":                  {"completion"},
		"scterm -":                   flags,
		"scterm -n":                  {"-n"},
		"scterm --audio-codec ":      serverCodecs,
		"scterm --audio-codec=f":     {"--audio-codec=flac"},
		"scterm -n --audio-source ":  serverSources,
		"scterm --peer ":             all,
		"scterm --web --peer Pixel":  pixel,
		"scterm --peer=Pixel\\ ":     peerPixelSpace,
		"scterm forget Pixel\\ ":     pixelSpace,
		"scterm forget Pixel\\ 8 ":   nil,
		"scterm update --c":          {"--check"},
		"scterm completion bash ":    nil,
		"scterm --web ":              nil,
		"scterm --web co":            nil,
		"scterm -s ":                 nil,
		"scterm -n -s ":              nil,
		"scterm -s so":               nil,
		"scterm --max-size ":         nil,
		"scterm --repaint-interval ": nil,
		"scterm --dump-frames fr":    {"frames/"},
		"scterm --dump-frames so":    nil,
		"scterm --dump-audio so":     {"sound.raw"},
	}
	for _, c := range commands {
		want, ok := commandArgs[c]
		if !ok {
			t.Errorf("scterm -h lists %s, for which this test has no completion", c)
		}
		typed := "scterm " + c + " "
		switch {
		case want == nil:
			want = all
		case len(want) > 0 && strings.HasPrefix(want[0], "-"):
			typed += "-"
		}
		cases[typed] = want
	}
	var lines []string
	script := "source " + shellQuoted(sh.script["fish"]) + "\n"
	for line := range cases {
		lines = append(lines, line)
		script += "complete -C " + shellQuoted(line) + "; printf '%s\\n' '-- end --'\n"
	}
	cmd := exec.Command("/usr/bin/fish", "--no-config", "-c", script)
	cmd.Env, cmd.Dir = sh.env, filesDir(t)
	out, err := cmd.Output()
	noMarkers(t, cmd.Dir)
	blocks := strings.Split(strings.TrimSuffix(string(out), "-- end --\n"), "-- end --\n")
	if err != nil || len(blocks) != len(lines) {
		t.Fatalf("%d completions gave %d (%v): %q", len(lines), len(blocks), err, out)
	}
	for i, line := range lines {
		var got []string
		if blocks[i] != "" {
			for _, c := range strings.Split(strings.TrimSuffix(blocks[i], "\n"), "\n") {
				c, _, _ = strings.Cut(c, "\t") // a description follows a tab
				got = append(got, c)
			}
		}
		want := slices.Clone(cases[line])
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%q: %q\nwant %q", line, got, want)
		}
	}
}

// ptyShell is an interactive shell in a pseudo-terminal, as a terminal
// emulator runs one: its line editor reads what the test types, completes on
// Tab, and on Ctrl-X Ctrl-P prints, in base64, the line it holds, and empties
// it. The zsh one also prints, before that, the words given to compadd since
// the last time.
type ptyShell struct {
	t        *testing.T
	pty      *os.File
	mu       sync.Mutex
	out      []byte
	answered int           // where in out the next terminal query is looked for
	wrote    chan struct{} // a signal that out has grown
}

// dumpLine is the key that makes a ptyShell print its line.
const dumpLine = "\x18\x10"

// ptySetup loads the completion script and binds dumpLine in each shell.
var ptySetup = map[string]string{
	"bash": `HISTFILE=; PS1='$ '; source SCRIPT; ` +
		`_dump() { printf '@@line=%s@@\n' "$(printf %s "$READLINE_LINE" | base64 -w0)"; READLINE_LINE=; READLINE_POINT=0; }; ` +
		`bind -x '"\C-x\C-p": _dump'`,
	"zsh": `PS1='$ '; autoload -Uz compinit && compinit -u -D; source SCRIPT; typeset -ga _offered; ` +
		`compadd() { _offered+=("${@[${@[(i)--]}+1,-1]}"); builtin compadd "$@"; }; ` +
		`_dump() { emulate -L zsh; print -r -- "@@offered=$(print -rn -- "${(F)_offered}" | base64 -w0)@@"; ` +
		`print -r -- "@@line=$(print -rn -- "$BUFFER" | base64 -w0)@@"; BUFFER=; _offered=(); }; ` +
		`zle -N _dump; bindkey '^X^P' _dump`,
	"fish": `function fish_prompt; echo -n '$ '; end; set -g fish_autosuggestion_enabled 0; source SCRIPT; ` +
		`function _dump; printf '@@line=%s@@\n' (printf %s (commandline) | base64 -w0); commandline ''; end; ` +
		`bind \cx\cp _dump`,
}

// ptyArgs start each shell interactive, without its startup files.
var ptyArgs = map[string][]string{"bash": {"--norc", "--noprofile", "-i"}, "zsh": {"-f", "-i"}, "fish": {"--no-config", "-i"}}

// startPTYShell starts shell in a pseudo-terminal, in dir, with the
// environment and script of sh, and waits until it is ready. The shell ends
// with the test, which then fails when the shell made a MARKER file in dir.
func startPTYShell(t *testing.T, sh *completionShell, shell, dir string) *ptyShell {
	t.Helper()
	ptmx, pts := openPTY(t)
	cmd := exec.Command("/usr/bin/"+shell, ptyArgs[shell]...)
	cmd.Env, cmd.Dir = sh.env, dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = pts, pts, pts
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pts.Close()
	s := &ptyShell{t: t, pty: ptmx, wrote: make(chan struct{}, 1)}
	read := make(chan struct{})
	go s.read(read)
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		ptmx.Close()
		<-read
		noMarkers(t, dir)
	})
	s.send(strings.ReplaceAll(ptySetup[shell], "SCRIPT", shellQuoted(sh.script[shell])) + "; printf '@@%s@@\\n' ready\r")
	s.waitFor(regexp.MustCompile("@@ready@@"), 1)
	return s
}

// read keeps what the shell writes, and answers fish's question for the
// terminal's attributes, which it waits for at every prompt.
func (s *ptyShell) read(done chan struct{}) {
	defer close(done)
	buf := make([]byte, 65536)
	for {
		n, err := s.pty.Read(buf)
		s.mu.Lock()
		s.out = append(s.out, buf[:n]...)
		answers, end := 0, s.answered
		for _, q := range [][]byte{[]byte("\x1b[c"), []byte("\x1b[0c")} {
			for i := s.answered; bytes.Contains(s.out[i:], q); answers++ {
				i += bytes.Index(s.out[i:], q) + len(q)
				end = max(end, i)
			}
		}
		// The next search starts after the last query, or 3 bytes from the
		// end, where a query cut in two may have started.
		s.answered = max(end, len(s.out)-3)
		s.mu.Unlock()
		for ; answers > 0; answers-- {
			s.pty.Write([]byte("\x1b[?62c"))
		}
		select {
		case s.wrote <- struct{}{}:
		default:
		}
		if err != nil {
			return
		}
	}
}

func (s *ptyShell) send(keys string) {
	s.t.Helper()
	if _, err := s.pty.Write([]byte(keys)); err != nil {
		s.t.Fatal(err)
	}
}

// waitFor waits until the shell has written n matches of re, and returns
// the submatches of each.
func (s *ptyShell) waitFor(re *regexp.Regexp, n int) [][]string {
	s.t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		s.mu.Lock()
		matches := re.FindAllStringSubmatch(string(s.out), -1)
		out := string(s.out)
		s.mu.Unlock()
		if len(matches) >= n {
			return matches
		}
		select {
		case <-s.wrote:
		case <-deadline:
			s.t.Fatalf("%d of %s after 30 seconds, want %d; the shell wrote %q", len(matches), re, n, out[max(0, len(out)-2000):])
		}
	}
}

var dumpedLine = regexp.MustCompile(`@@line=([A-Za-z0-9+/=]*)@@`)

// complete types keys, then Tab, then dumpLine, and returns the line as the
// shell then held it: the n-th it printed.
func (s *ptyShell) complete(keys string, n int) string {
	s.t.Helper()
	s.send(keys + "\t" + dumpLine)
	b64 := s.waitFor(dumpedLine, n)[n-1][1]
	line, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		s.t.Fatalf("line %d: %q: %v", n, b64, err)
	}
	return string(line)
}

// openPTY opens a pseudo-terminal 200 columns wide, and returns its
// controller side, which the test's end closes, and its terminal side.
func openPTY(t *testing.T) (ptmx, pts *os.File) {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ptmx.Close() })
	var unlock, n uint32
	size := struct{ rows, cols, x, y uint16 }{50, 200, 0, 0}
	for _, c := range []struct {
		req uintptr
		arg unsafe.Pointer
	}{{syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)}, {syscall.TIOCGPTN, unsafe.Pointer(&n)}, {syscall.TIOCSWINSZ, unsafe.Pointer(&size)}} {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), c.req, uintptr(c.arg)); errno != 0 {
			t.Fatal(errno)
		}
	}
	pts, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return ptmx, pts
}

// TestZshCompletesAsTheHelpSays: zsh's completion system, in a terminal,
// with the zsh script loaded, gives compadd for each command line what the
// bash script gives, and the paired devices, for --peer and forget: outside
// quotes, those that the word typed so far starts, as scterm writes them,
// each to replace the word, after --peer= too; inside quotes, the values for
// zsh to quote, which it matches with the word typed so far in any case, as
// scterm does. It completes a directory for --dump-frames and a file for
// --dump-audio.
func TestZshCompletesAsTheHelpSays(t *testing.T) {
	inCompletionChild()
	needShell(t, "/usr/bin/zsh")
	_, pairings := pastePairings(t)
	sh := newCompletionShell(t, os.Getenv("SCTERM_HOME"))
	commands, flags := helpCommands(t)
	var all, values []string
	for _, p := range pairings {
		all, values = append(all, p.insertedArg()), append(values, p.value())
	}
	s := startPTYShell(t, sh, "zsh", filesDir(t))
	type completion struct {
		keys, line string   // what is typed, and the line once it is completed
		offered    []string // the words given to compadd, sorted, unless nil
	}
	cases := []completion{
		{"scterm ", "scterm ", commands},
		{"scterm -", "scterm -", flags},
		{"scterm --audio-codec ", "scterm --audio-codec ", serverCodecs},
		{"scterm --audio-codec=", "scterm --audio-codec=", serverCodecs},
		{"scterm --audio-codec=fl", "scterm --audio-codec=flac ", serverCodecs},
		{"scterm -audio-codec fl", "scterm -audio-codec flac ", serverCodecs},
		{"scterm -n --audio-source ", "scterm -n --audio-source ", serverSources},
		{"scterm --peer ", "scterm --peer ", all},
		{"scterm --peer gal", "scterm --peer 'Galaxy_S23-FE.2' ", []string{"'Galaxy_S23-FE.2'"}},
		{"scterm --peer=gal", "scterm --peer='Galaxy_S23-FE.2' ", []string{"--peer='Galaxy_S23-FE.2'"}},
		{"scterm --peer '", "scterm --peer '", values},
		{"scterm --peer 'Gal", "scterm --peer 'Galaxy_S23-FE.2' ", values},
		{"scterm --peer 'gal", "scterm --peer 'Galaxy_S23-FE.2' ", values},
		{`scterm --peer "gal`, `scterm --peer "Galaxy_S23-FE.2" `, values},
		{"scterm forget ", "scterm forget ", all},
		{"scterm forget 'GAL", "scterm forget 'Galaxy_S23-FE.2' ", values},
		{`scterm forget o\'b`, `scterm forget 'O'\''Brien'\''s Pixel' `, []string{`'O'\''Brien'\''s Pixel'`}},
		{"scterm completion bash ", "scterm completion bash ", []string{}},
		{"scterm --web ", "scterm --web ", []string{}},
		{"scterm -s ", "scterm -s ", []string{}},
		{"scterm -s -", "scterm -s -", []string{}},
		{"scterm --max-size ", "scterm --max-size ", []string{}},
		{"scterm --dump-frames fr", "scterm --dump-frames frames/", nil},
		{"scterm --dump-audio so", "scterm --dump-audio sound.raw ", nil},
		{"scterm --dump-audio=so", "scterm --dump-audio=sound.raw ", nil},
	}
	for _, c := range commands {
		want, ok := commandArgs[c]
		if !ok {
			t.Errorf("scterm -h lists %s, for which this test has no completion", c)
		}
		if want != nil {
			cases = append(cases, completion{"scterm " + c + " ", "", want})
		}
	}
	offered := regexp.MustCompile(`@@offered=([A-Za-z0-9+/=]*)@@`)
	for i, c := range cases {
		line := s.complete(c.keys, i+1)
		if c.line != "" && line != c.line {
			t.Errorf("%q completes to %q, want %q", c.keys, line, c.line)
		}
		if c.offered == nil {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(s.waitFor(offered, i+1)[i][1])
		var got []string
		if len(b) > 0 {
			got = strings.Split(string(b), "\n")
		}
		want := slices.Clone(c.offered)
		if !slices.Equal(want, all) && !slices.Equal(want, values) {
			slices.Sort(got)
			slices.Sort(want)
		}
		if err != nil || !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%q offers %q (%v)\nwant %q", c.keys, got, err, want)
		}
	}
}

// fishNoSpace are the characters after which fish adds no space to a word it
// completes, so it leaves the word's quote open, for the user to close (seen
// with fish 4.9.3, for every character pastecmd_test.go's names end in).
const fishNoSpace = "/=@:.,-"

// TestCompletionInsertsPairingsSafely: in each shell, in a terminal, for
// each pairing alone in its store (pastePairings's names, hostile ones
// included), completing the argument of --peer from nothing, after a single
// quote, after a double quote, after --peer=, alone or with either quote,
// and after forget inserts what gives scterm that pairing, and runs nothing:
// each completed line, run as a script and in an interactive shell (zsh's
// also with global aliases), gives scterm the name, or the identity, of the
// pairing and makes no file. Outside quotes, bash and zsh insert what scterm
// prints, as written; fish quotes the value itself, as zsh does inside a
// quote that starts the word. fish ends each word with its closing quote and
// a space, but one that ends in a character of fishNoSpace, which the user
// ends.
func TestCompletionInsertsPairingsSafely(t *testing.T) {
	inCompletionChild()
	_, pairings := pastePairings(t)
	// Alone in its store, a pairing that pastePairings names by identity only
	// because another is named alike is named by its name.
	alike := []string{"İzmir", "Tab 7", "TAB 7"}
	homes := t.TempDir()
	for i, p := range pairings {
		if err := peer.OpenStore(filepath.Join(homes, fmt.Sprint(i))).Put(p.rec); err != nil {
			t.Fatal(err)
		}
	}
	for _, shell := range completionShells {
		for _, typed := range []string{"scterm --peer ", "scterm --peer '", `scterm --peer "`, "scterm --peer=", "scterm --peer='",
			`scterm --peer="`, "scterm forget "} {
			t.Run(shell+" "+typed, func(t *testing.T) {
				t.Parallel()
				needShell(t, "/usr/bin/"+shell)
				sh := newCompletionShell(t, t.TempDir())
				s := startPTYShell(t, sh, shell, t.TempDir())
				setHome := "export SCTERM_HOME="
				if shell == "fish" {
					setHome = "set -gx SCTERM_HOME "
				}
				quote := strings.TrimLeft(typed[len(typed)-1:], " =") // the quote typed, if any
				lines, values := make([]string, len(pairings)), make([]string, len(pairings))
				for i, p := range pairings {
					value, arg := p.value(), p.insertedArg()
					if slices.Contains(alike, p.rec.Name) {
						value, arg = p.rec.Name, "'"+p.rec.Name+"'"
					}
					s.send(setHome + shellQuoted(filepath.Join(homes, fmt.Sprint(i))) + "\r")
					line := s.complete(typed, i+1)
					switch end := quote + " "; {
					case shell == "fish" && strings.ContainsRune(fishNoSpace, rune(value[len(value)-1])) && !strings.HasSuffix(line, end):
						line += end
					case shell == "fish" && !strings.HasSuffix(line, end):
						t.Errorf("%q: completed to %q, which does not end in %q", p.rec.Name, line, end)
					case shell != "fish" && quote == "" && line != typed+arg+" ":
						t.Errorf("%q: completed to %q, want %q", p.rec.Name, line, typed+arg+" ")
					}
					lines[i], values[i] = line, value
				}
				modes := map[string][]string{"bash": {"bash", "bash -i"}, "zsh": {"zsh", "zsh -i", "zsh -i, global aliases"}, "fish": {"fish"}}
				for _, mode := range modes[shell] {
					got := pasteInto(t, pasteShellNamed(t, mode), lines)
					for i, p := range pairings {
						args := []string{"--peer", values[i]}
						switch {
						case strings.HasPrefix(typed, "scterm --peer="):
							args = []string{"--peer=" + values[i]}
						case strings.HasPrefix(typed, "scterm forget"):
							args = []string{"forget", values[i]}
						}
						if !slices.Equal(got[i], args) {
							t.Errorf("%s: %q: %s gave scterm %q, want %q", mode, p.rec.Name, lines[i], got[i], args)
						}
					}
				}
			})
		}
	}
}

// hostileLines are lines a stand-in scterm's helper prints, as the real one
// never does: each would run, expand, glob, redirect or end a quote if a
// shell read it as code, and each MARKER file it would make shows that it
// ran.
var hostileLines = []string{"'$(touch MARKER-1)'", "`touch MARKER-2`", "$(touch MARKER-3)", `"$(touch MARKER-4)"`,
	"(touch MARKER-5)", "a;touch MARKER-6", "${HOME+$(touch MARKER-7)}", "$((`touch MARKER-8`))", "a&touch MARKER-9",
	"a|touch MARKER-10", ">MARKER-11", "globbed*", "~/MARKER-13", "!!", `it's "quoted"`}

// TestScriptsInsertWhatTheHelperPrintsAsData: each script, with a stand-in
// scterm whose helper prints one of hostileLines, completes the pairing
// argument in each context it serves without running or expanding the
// line, nor a function named scterm, which the script must pass over for
// the program on PATH: no MARKER file appears, and the line gets the
// helper's line as text, or nothing. Where the script inserts what the
// helper prints (bash, and zsh outside quotes), the line holds it as
// written; where the shell quotes it (fish, zsh inside a quote), the line
// gives scterm the helper's line when it runs.
func TestScriptsInsertWhatTheHelperPrintsAsData(t *testing.T) {
	for _, shell := range completionShells {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			needShell(t, "/usr/bin/"+shell)
			sh := newCompletionShell(t, t.TempDir())
			printed := filepath.Join(sh.dir, "printed")
			stub := "#!/bin/sh\n[ \"$1\" = __complete ] && cat " + shellQuoted(printed) + "\n"
			if err := os.WriteFile(filepath.Join(sh.dir, "bin", "scterm"), []byte(stub), 0o700); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "globbed-file"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			s := startPTYShell(t, sh, shell, dir)
			decoy := "scterm() { touch MARKER-function; echo decoy; }"
			if shell == "fish" {
				decoy = "function scterm; touch MARKER-function; echo decoy; end"
			}
			s.send(decoy + "\r")
			type step struct{ typed, hostile, line string }
			var quoted []step // lines in which the shell quoted the helper's line
			n := 0
			for _, typed := range []string{"scterm --peer ", "scterm --peer x", "scterm --peer '", `scterm --peer "`, "scterm --peer=",
				"scterm --peer='", `scterm --peer="`, "scterm forget ", "scterm forget '", `scterm forget "`} {
				for _, hostile := range hostileLines {
					if err := os.WriteFile(printed, []byte(hostile+"\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					n++
					line := s.complete(typed, n)
					if made, _ := filepath.Glob(filepath.Join(dir, "MARKER*")); len(made) > 0 {
						t.Errorf("%q, the helper printing %q: completing made %q", typed, hostile, made)
						for _, m := range made {
							os.Remove(m)
						}
					}
					if line != typed && !strings.Contains(line, hostile) {
						quoted = append(quoted, step{typed, hostile, line})
					}
				}
			}
			if len(quoted) == 0 {
				return
			}
			lines := make([]string, len(quoted))
			for i, q := range quoted {
				lines[i] = q.line
			}
			// where the line runs: zsh reads back the \! it writes in double
			// quotes only when interactive
			mode := map[string]string{"bash": "bash -i", "zsh": "zsh -i", "fish": "fish"}[shell]
			for i, args := range pasteInto(t, pasteShellNamed(t, mode), lines) {
				q := quoted[i]
				want := []string{"--peer", q.hostile}
				switch {
				case strings.HasPrefix(q.typed, "scterm --peer="):
					want = []string{"--peer=" + q.hostile}
				case strings.HasPrefix(q.typed, "scterm forget"):
					want = []string{"forget", q.hostile}
				}
				if !slices.Equal(args, want) {
					t.Errorf("%q, the helper printing %q: completed to %q, which gives scterm %q; want %q", q.typed, q.hostile, q.line, args, want)
				}
			}
		})
	}
}

// TestCompletingMidWordGivesNoOtherPairing: Tab with the cursor inside the
// word, moved back into Pixel, never leaves a line that gives scterm another
// pairing than the one completed. With Pixel 8 beside My Pixel 8el, or beside
// Pixel 8el$x (a name pasteArg refuses), the one candidate for Pix is 'Pixel
// 8', and a shell that kept the el after the cursor would give scterm Pixel
// 8el, which selects the other. bash, which keeps it, offers no pairing
// there, and the line stays as typed; zsh and fish replace the whole word,
// and the line gives scterm Pixel 8.
func TestCompletingMidWordGivesNoOtherPairing(t *testing.T) {
	inCompletionChild()
	pixel := pairing(peer.FingerprintOf([]byte("pixel")).Hex(), "Pixel 8", "192.0.2.1")
	homes := t.TempDir()
	var stores []*peer.Store
	for i, name := range []string{"My Pixel 8el", "Pixel 8el$x"} {
		store := peer.OpenStore(filepath.Join(homes, fmt.Sprint(i)))
		for _, rec := range []peer.Record{pixel, pairing(peer.FingerprintOf([]byte(name)).Hex(), name, "192.0.2.2")} {
			if err := store.Put(rec); err != nil {
				t.Fatal(err)
			}
		}
		stores = append(stores, store)
	}
	back := map[string]string{"bash": "\x02", "zsh": "\x02", "fish": "\x1b[D"} // moves the cursor back one character
	for _, shell := range completionShells {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			needShell(t, "/usr/bin/"+shell)
			sh := newCompletionShell(t, t.TempDir())
			s := startPTYShell(t, sh, shell, t.TempDir())
			setHome := "export SCTERM_HOME="
			if shell == "fish" {
				setHome = "set -gx SCTERM_HOME "
			}
			type step struct {
				typed, line string
				store       *peer.Store
			}
			var steps []step
			for i, store := range stores {
				for _, typed := range []string{"scterm --peer Pixel", "scterm forget Pixel", "scterm --peer=Pixel", "scterm --peer 'Pixel"} {
					s.send(setHome + shellQuoted(filepath.Join(homes, fmt.Sprint(i))) + "\r")
					line := s.complete(typed+strings.Repeat(back[shell], 2), len(steps)+1)
					steps = append(steps, step{typed, line, store})
				}
			}
			if shell == "bash" {
				for _, st := range steps {
					if st.line != st.typed {
						t.Errorf("%q, cursor back two: Tab gave %q; want no pairing, the line as typed", st.typed, st.line)
					}
				}
				return
			}
			lines := make([]string, len(steps))
			for i, st := range steps {
				lines[i] = st.line
			}
			for i, args := range pasteInto(t, pasteShellNamed(t, shell), lines) {
				value := ""
				switch {
				case len(args) == 2 && (args[0] == "--peer" || args[0] == "forget"):
					value = args[1]
				case len(args) == 1 && strings.HasPrefix(args[0], "--peer="):
					value = strings.TrimPrefix(args[0], "--peer=")
				}
				if rec, err := steps[i].store.Find(value); value != "Pixel 8" || err != nil || rec.Name != "Pixel 8" {
					t.Errorf("%q, cursor back two: Tab gave %q, which gives scterm %q; want Pixel 8", steps[i].typed, lines[i], args)
				}
			}
		})
	}
}

// TestShellsCompleteDamagedAfterForget: with a pairing whose identity does not
// parse, bash, zsh and fish offer --damaged beside the devices after scterm
// forget, and complete scterm forget --d to scterm forget --damaged, but never
// offer it after --peer.
func TestShellsCompleteDamagedAfterForget(t *testing.T) {
	inCompletionChild()
	writePairings(t, pairing(peer.FingerprintOf([]byte("pixel")).Hex(), "Pixel 8", "192.0.2.1"), pairing("not-a-fingerprint", "Old", "192.0.2.2"))
	sh := newCompletionShell(t, os.Getenv("SCTERM_HOME"))
	t.Run("bash", func(t *testing.T) {
		needShell(t, "/usr/bin/bash")
		cases := []struct {
			words, want []string
		}{
			{[]string{"forget", ""}, []string{"'Pixel 8'", "--damaged"}},
			{[]string{"forget", "--d"}, []string{"--damaged"}},
			{[]string{"--peer", ""}, []string{"'Pixel 8'"}},
			{[]string{"--peer", "--d"}, nil},
		}
		script := "source " + shellQuoted(sh.script["bash"]) + "\n" +
			`try() { COMP_WORDS=(scterm "$@"); COMP_CWORD=$#; COMPREPLY=(); _scterm scterm "${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[COMP_CWORD-1]}"; ` +
			`printf '%s\n' "${COMPREPLY[@]}" '-- end --'; }` + "\n"
		for _, c := range cases {
			script += "try " + shellQuoted(c.words[0]) + " " + shellQuoted(c.words[1]) + "\n"
		}
		cmd := exec.Command("/usr/bin/bash", "--norc", "--noprofile", "-c", script)
		cmd.Env, cmd.Dir = sh.env, t.TempDir()
		out, err := cmd.Output()
		blocks := strings.Split(strings.TrimSuffix(string(out), "-- end --\n"), "-- end --\n")
		if err != nil || len(blocks) != len(cases) {
			t.Fatalf("%d completions gave %d (%v): %q", len(cases), len(blocks), err, out)
		}
		for i, c := range cases {
			var got []string
			if blocks[i] != "" {
				got = strings.Split(strings.TrimSuffix(blocks[i], "\n"), "\n")
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("scterm %q: %q, want %q", c.words, got, c.want)
			}
		}
	})
	t.Run("fish", func(t *testing.T) {
		needShell(t, "/usr/bin/fish")
		cases := map[string][]string{
			"scterm forget ":    {"--damaged", "Pixel 8"},
			"scterm forget --d": {"--damaged"},
			"scterm --peer ":    {"Pixel 8"},
		}
		var lines []string
		script := "source " + shellQuoted(sh.script["fish"]) + "\n"
		for line := range cases {
			lines = append(lines, line)
			script += "complete -C " + shellQuoted(line) + "; printf '%s\\n' '-- end --'\n"
		}
		script += "complete -C 'scterm --peer --d'\n"
		cmd := exec.Command("/usr/bin/fish", "--no-config", "-c", script)
		cmd.Env, cmd.Dir = sh.env, t.TempDir()
		out, err := cmd.Output()
		blocks := strings.Split(string(out), "-- end --\n")
		if err != nil || len(blocks) != len(lines)+1 {
			t.Fatalf("%d completions gave %d (%v): %q", len(lines)+1, len(blocks), err, out)
		}
		for i, line := range lines {
			var got []string
			if blocks[i] != "" {
				got = strings.Split(strings.TrimSuffix(blocks[i], "\n"), "\n")
			}
			slices.Sort(got)
			if !slices.Equal(got, cases[line]) {
				t.Errorf("%q: %q, want %q", line, got, cases[line])
			}
		}
		if last := blocks[len(lines)]; strings.Contains(last, "--damaged") {
			t.Errorf("scterm --peer --d offers --damaged: %q", last)
		}
	})
	t.Run("zsh", func(t *testing.T) {
		needShell(t, "/usr/bin/zsh")
		s := startPTYShell(t, sh, "zsh", t.TempDir())
		for i, c := range []struct{ keys, line string }{
			{"scterm forget --d", "scterm forget --damaged "},
			{"scterm --peer --d", "scterm --peer --d"},
		} {
			if line := s.complete(c.keys, i+1); line != c.line {
				t.Errorf("%q completes to %q, want %q", c.keys, line, c.line)
			}
		}
	})
}
