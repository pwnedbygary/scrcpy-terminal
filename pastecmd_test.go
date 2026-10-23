package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// Commands scterm prints for the user to paste: "Connect with: scterm --peer
// ..." after a pairing, and the one scterm forget gives when it refuses part
// of a name. Each names a pairing, and the name is the device's own text, so
// pasted into sh, bash, zsh or fish, interactive or not, the command must run
// nothing else and give scterm exactly what finds that pairing.
// ---------------------------------------------------------------------------

// pasteNames are names a device can give, each with the argument a pasted
// command should name its pairing by: "" for its identity (the short form).
// A name is always in single quotes, even one that needs none, and only when
// whatever is copied from inside the quotes is words: a name with a character
// a shell reads is given by identity, as the comments say what it does bare.
var pasteNames = []struct{ name, arg string }{
	{"Pixel", "'Pixel'"},
	{"Galaxy_S23-FE.2", "'Galaxy_S23-FE.2'"},
	{"-x", "'-x'"}, // --peer takes it as its value
	{"Pixel 8", "'Pixel 8'"},
	{"a,b:c@d+e", "'a,b:c@d+e'"},
	{"$(touch MARKER-subst)", ""},
	{"`touch MARKER-backtick`", ""},
	{"$HOME", ""},
	{"O'Brien's Pixel", `'O'\''Brien'\''s Pixel'`}, // a quote alone runs nothing
	{"Gary's Pixel", `'Gary'\''s Pixel'`},
	{"x'; touch MARKER-quote; echo '", ""},
	{`say "hi"`, `'say "hi"'`},
	{"Hi!x", ""}, // history expansion, in an interactive bash or zsh
	{"a;touch MARKER-semicolon", ""},
	{"a&touch MARKER-amp", ""},
	{"a|touch MARKER-pipe", ""},
	{"(touch MARKER-paren)", ""}, // a subshell, at the start of a command
	{"a>MARKER-redirect", ""},
	{"a<b", ""},
	{"*", ""},
	{"~", ""},
	{"%self", ""},    // fish's process ID
	{"=ls", ""},      // zsh's path to ls
	{"L", "'L'"},     // bare, a global alias in the zsh mode below
	{"...", "'...'"}, // likewise, as oh-my-zsh defines it
	{"#x", ""},       // a comment
	{"{a,b}", ""},    // two words
	{"}", ""},        // a zsh parse error
	{"?", ""},        // a glob in sh, bash and zsh
	{"[a]", ""},      // likewise
	{"^x", ""},       // a glob, in a zsh with EXTENDED_GLOB
	{"end", "'end'"}, // a fish keyword
	{"Café", "'Café'"},
	{"日本", "'日本'"},
	{"Pixel 🚀", "'Pixel 🚀'"},
	{"izmir", "'izmir'"},
	// Planted: copied from inside the quotes, each would run.
	{"Pixel 8 x;touch MARKER-fragment #", ""},
	{"Pixel 8 x$(touch MARKER-fragment2)", ""},
	{"Pixel scterm forget Victim", ""}, // only words, which would forget Victim at once
	{"SCTERM", ""},
	{"myscterm", ""},
	// Copied, each is scterm: a shell takes quotes out of a word, and a
	// terminal may leave out a character that takes no cell when it copies.
	{`x sc""term forget Victim`, ""},
	{`sc"t"erm`, ""},
	{`"sc"term`, ""},
	{`s""cterm pair`, ""},
	{"sc''term", ""},
	{"sc\ufe0fterm forget Victim", ""}, // a variation selector
	{"sc\U00016fe4term", ""},
	// Blank-looking, so they can pad a row: a Braille cell without dots, and
	// the Hangul fillers.
	{"Pixel\u28008", ""},
	{"Pixel\u31648", ""},
	{"\u115fPixel", ""},
	{"Pixel\u1160", ""},
	{"Pixel\uffa08", ""},
	{"Pixel" + strings.Repeat("\u2800", 70) + "Paired. Connect with: scterm --peer 'x;touch MARKER-pad #'", ""},
	// At most 32 cells, as the command writes the name: an apostrophe takes
	// four there, and a character that takes no cell, as a combining mark
	// does, one.
	{"Cafe\u0301", "'Cafe\u0301'"},
	{"a" + strings.Repeat("\u0301", 40), ""},
	{strings.Repeat("a", 32), "'" + strings.Repeat("a", 32) + "'"},
	{strings.Repeat("b", 33), ""},
	{strings.Repeat("日", 16), "'" + strings.Repeat("日", 16) + "'"},
	{strings.Repeat("本", 17), ""},
	{"c'c'" + strings.Repeat("c", 22), `'c'\''c'\''` + strings.Repeat("c", 22) + "'"},
	{"d'd'" + strings.Repeat("d", 23), ""},
	{strings.Repeat("'", 11), ""},
	{"İzmir", ""}, // Find lowercases it to izmir, and gives that pairing
	{"Tab 7", ""}, // the same name in another case: neither says which
	{"TAB 7", ""},
	{`back\slash`, ""},
	{`a\\b`, ""},
	{`ends\`, ""},
	{"line\nbreak", ""},
	{"tab\there", ""},
	{"Pixel\x1b[31m 8", ""},
	{"Pixel\u009b2J", ""},
	{"Pixel\u202e 8", ""},
	{"Pixel\u200b8", ""},
	{"Pix\u00adel", ""},
	{"Pixel\x9b", ""},
	{"Pixel \ue000", ""}, // private use
	{"Pixel\u00a08", ""}, // a no-break space, which cleanText shows as a space
	{"Pixel\u30008", ""}, // an ideographic space, likewise
	{" Pixel", ""},
	{"Pixel ", ""},
	{"Moto ", ""}, // found only as the start of a name: Moto G would make it ambiguous
	{" Nokia", ""},
	{"Pixel  9", ""},
	{"help", ""}, // scterm forget help prints the help
	{"-h", ""},
	{"--help", ""},
	{"-help", ""},
	{"", ""},
}

// pastePairing is one of the tests' pairings, with the argument a pasted
// command should name it by ("" for its identity).
type pastePairing struct {
	rec peer.Record
	fp  peer.Fingerprint
	arg string
}

// want is what a pasted command should give scterm for p: its name, or its
// short identity.
func (p pastePairing) want() string {
	if p.arg == "" {
		return p.fp.Short()
	}
	return p.rec.Name
}

// pastePairings pairs each of pasteNames, then a device that gave no name,
// stored under its short identity (so named by that, in quotes), in a store
// under a fresh SCTERM_HOME.
func pastePairings(t *testing.T) (*peer.Store, []pastePairing) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SCTERM_HOME", home)
	store := peer.OpenStore(home)
	pairings := make([]pastePairing, len(pasteNames)+1)
	for i := range pairings {
		fp := peer.FingerprintOf([]byte("pairing " + strconv.Itoa(i)))
		p := pastePairing{fp: fp, arg: "'" + fp.Short() + "'", rec: peer.Record{Fingerprint: fp.Hex(), Name: fp.Short(), Host: "192.0.2.1", Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1}}
		if i < len(pasteNames) {
			p.rec.Name, p.arg = pasteNames[i].name, pasteNames[i].arg
		}
		if err := store.Put(p.rec); err != nil {
			t.Fatal(err)
		}
		pairings[i] = p
	}
	return store, pairings
}

// pasteShell is a shell a printed command is pasted into: how to start it
// without reading any startup file, and a definition of scterm as a function
// that prints how many arguments it got, then each on a line of its own.
type pasteShell struct {
	name, path string
	args       []string // the commands follow -c, or else are the shell's input
	function   string
}

const posixScterm = `scterm() { printf '%s\n' "$#" "$@"; }`

// pasteShells run the commands as a script, and bash and zsh also as an
// interactive shell reading typed lines, which is where they expand history;
// zsh once more with global aliases, which it expands in any bare word, as
// oh-my-zsh defines ... for ../.. (lib/directories.zsh).
var pasteShells = []pasteShell{
	{"sh", "/usr/bin/sh", []string{"-c"}, posixScterm},
	{"bash", "/usr/bin/bash", []string{"--norc", "--noprofile", "-c"}, posixScterm},
	{"zsh", "/usr/bin/zsh", []string{"-f", "-c"}, posixScterm},
	{"fish", "/usr/bin/fish", []string{"--no-config", "-c"}, `function scterm; count $argv; printf '%s\n' $argv; end`},
	{"bash -i", "/usr/bin/bash", []string{"--norc", "--noprofile", "-i"}, posixScterm},
	{"zsh -i", "/usr/bin/zsh", []string{"-f", "-i"}, posixScterm},
	{"zsh -i, global aliases", "/usr/bin/zsh", []string{"-f", "-i"}, posixScterm + "\nalias -g ...='../..' L='| touch MARKER-alias-L'"},
}

// pasteInto runs commands in sh, one after another once scterm is defined,
// with no terminal and only PATH and HOME set, HOME being a fresh directory
// that is also the working directory. It returns the arguments scterm got
// from each command, and fails the test when a command made a file there
// whose name starts with MARKER. When sh is not installed it skips the test,
// or fails it if SCTERM_TEST_REQUIRE_SHELLS is set, as CI sets it.
func pasteInto(t *testing.T, sh pasteShell, commands []string) [][]string {
	t.Helper()
	if _, err := os.Stat(sh.path); err != nil {
		if os.Getenv("SCTERM_TEST_REQUIRE_SHELLS") != "" {
			t.Fatalf("%s is not installed, and SCTERM_TEST_REQUIRE_SHELLS is set", sh.path)
		}
		t.Skipf("%s is not installed", sh.path)
	}
	const end = "-- end of command --"
	script := sh.function + "\n"
	for _, c := range commands {
		script += c + "\nprintf '%s\\n' '" + end + "'\n"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh.path, sh.args...)
	cmd.WaitDelay = time.Second // a child left holding stdout must not keep Run waiting
	if sh.args[len(sh.args)-1] == "-c" {
		cmd.Args = append(cmd.Args, script)
	} else {
		cmd.Stdin = strings.NewReader(script)
	}
	dir := t.TempDir()
	cmd.Dir, cmd.Env = dir, []string{"PATH=/usr/bin:/bin", "HOME=" + dir}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if made, _ := filepath.Glob(filepath.Join(dir, "MARKER*")); len(made) > 0 {
		t.Errorf("the commands ran something: they made %q (%v, stderr %q)", made, runErr, stderr.String())
	}
	blocks := strings.Split(strings.TrimSuffix(stdout.String(), end+"\n"), end+"\n")
	if len(blocks) != len(commands) {
		t.Fatalf("%d commands gave %d outputs (%v): stdout %q, stderr %q", len(commands), len(blocks), runErr, stdout.String(), stderr.String())
	}
	got := make([][]string, len(commands))
	for i, b := range blocks {
		lines := strings.Split(strings.TrimSuffix(b, "\n"), "\n")
		if n, err := strconv.Atoi(lines[0]); err == nil && n == len(lines)-1 {
			got[i] = lines[1:]
		} else {
			got[i] = append([]string{"(not a call of scterm)"}, lines...)
		}
	}
	return got
}

// TestConnectWithPastesAsPrinted: the command scterm prints first after a
// pairing, pasted into each shell, runs nothing else and gives scterm --peer
// and the pairing's name or, when the name is not plain text or does not find
// the pairing alone, its identity. The flag parser takes that as --peer's
// value, and it finds the pairing without a question.
func TestConnectWithPastesAsPrinted(t *testing.T) {
	store, pairings := pastePairings(t)
	client := &peer.Client{Identity: &peer.Identity{Fingerprint: peer.FingerprintOf([]byte("this computer"))}}
	commands := make([]string, len(pairings))
	for i, p := range pairings {
		out := captureStdout(t, func() { reportPaired(client, store, p.rec.Name, p.fp, p.rec.Grants) })
		line, _, ended := strings.Cut(out, "\n")
		command, ok := strings.CutPrefix(line, "Paired. Connect with: ")
		if !ok || !ended || unclean(command) || !strings.HasPrefix(command, "scterm --peer ") {
			t.Fatalf("%q: printed %q", p.rec.Name, out)
		}
		commands[i] = command
	}
	withInteractive(t, true)
	oldInput := terminalInput
	t.Cleanup(func() { terminalInput = oldInput })
	for _, sh := range pasteShells {
		t.Run(sh.name, func(t *testing.T) {
			got := pasteInto(t, sh, commands)
			for i, p := range pairings {
				want := p.want()
				if !slices.Equal(got[i], []string{"--peer", want}) {
					t.Errorf("%q: pasted %s, scterm got %q; want --peer %q", p.rec.Name, commands[i], got[i], want)
					continue
				}
				var cfg config
				if err := parseArgs(newFlagSet(&cfg, new(float64)), &cfg, got[i]); err != nil || !cfg.usePeer || cfg.peerQuery != want {
					t.Errorf("%q: %q parses as --peer %q, %v; want %q", p.rec.Name, got[i], cfg.peerQuery, err, want)
					continue
				}
				terminalInput = bufioReader("") // a question fails
				var rec peer.Record
				var err error
				captureStderr(t, func() { rec, err = pickPeer(store, cfg.peerQuery) })
				if err != nil || rec.Fingerprint != p.rec.Fingerprint {
					t.Errorf("%q: --peer %q finds %q, %v; want this pairing", p.rec.Name, cfg.peerQuery, rec.Name, err)
				}
			}
		})
	}
}

// TestQuotedNamesPasteBareAsWords: whatever is copied from inside the quotes
// of a printed command is only words. Each name pasteArg puts in quotes,
// pasted bare after scterm into each shell, runs nothing else, and scterm
// gets its words. A word can still name a command, or a global alias in zsh
// (which the mode with them would expand here: the quotes are for the whole
// command); a fragment that leaves a quote open, as part of Gary's would,
// runs nothing, the shell waiting for the rest.
func TestQuotedNamesPasteBareAsWords(t *testing.T) {
	store, pairings := pastePairings(t)
	closed := func(name string) bool { return strings.Count(name, "'")%2 == 0 && strings.Count(name, `"`)%2 == 0 }
	var names, commands, quoted []string
	for _, p := range pairings {
		name := p.rec.Name
		if strings.HasPrefix(p.arg, "'") && closed(name) {
			quoted = append(quoted, name)
		}
		if arg, ok := pasteArg(store, name, p.fp); ok && strings.HasPrefix(arg, "'") && closed(name) {
			names = append(names, name)
			commands = append(commands, "scterm "+name)
		}
	}
	if !slices.Equal(names, quoted) {
		t.Errorf("pasteArg puts %q in quotes\nwant %q", names, quoted)
	}
	for _, sh := range pasteShells {
		if strings.Contains(sh.function, "alias -g") {
			continue
		}
		t.Run(sh.name, func(t *testing.T) {
			got := pasteInto(t, sh, commands)
			for i, name := range names {
				// No name here has quotes around a space.
				if want := strings.Fields(strings.NewReplacer("'", "", `"`, "").Replace(name)); !slices.Equal(got[i], want) {
					t.Errorf("%q: pasted bare, scterm got %q; want %q", name, got[i], want)
				}
			}
		})
	}
}

// TestNamePartsRunNoScterm: a person can copy any part of what is inside the
// quotes of a printed command. Every part of each name pasteArg puts in
// quotes, from any of its characters to any later one (so that no quote
// further on can hide what comes before it), is pasted as a command line of
// its own into each shell, where a PATH that holds nothing but a recording
// scterm runs a part's first word if, as the shell reads it, it is scterm. No
// part runs it, while the lines scterm and sc""term do (a shell takes the
// quotes out of a word). Only the test's own names are pasted, so no part
// starts with a builtin that could do harm.
func TestNamePartsRunNoScterm(t *testing.T) {
	store, pairings := pastePairings(t)
	seen := map[string]bool{}
	var parts []string
	for _, p := range pairings {
		if arg, ok := pasteArg(store, p.rec.Name, p.fp); ok && strings.HasPrefix(arg, "'") {
			var cuts []int // where each character starts, then the end
			for i := range p.rec.Name {
				cuts = append(cuts, i)
			}
			cuts = append(cuts, len(p.rec.Name))
			for a, i := range cuts {
				for _, j := range cuts[a+1:] {
					if part := p.rec.Name[i:j]; !seen[part] {
						seen[part] = true
						parts = append(parts, part)
					}
				}
			}
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "scterm"), []byte("#!/bin/sh\n: >MARKER-scterm-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sh := range pasteShells {
		if strings.Contains(sh.function, "alias -g") {
			continue
		}
		t.Run(sh.name, func(t *testing.T) {
			pasteInto(t, sh, []string{"scterm"}) // as for the other paste tests when the shell is not installed
			controls := []string{"scterm", `sc""term`}
			for i, made := range runCommandLines(t, sh, bin, controls) {
				if len(made) == 0 {
					t.Fatalf("%q: the recording scterm did not run", controls[i])
				}
			}
			for i, made := range runCommandLines(t, sh, bin, parts) {
				if len(made) != 0 {
					t.Errorf("%q, pasted as a command line, made %q", parts[i], made)
				}
			}
		})
	}
}

// runCommandLines runs each line in a shell of its own, so that none can leave
// a quote open over the next: in a fresh directory, with bin the only
// directory on PATH. It returns the files whose names start with MARKER that
// each line made there.
func runCommandLines(t *testing.T, sh pasteShell, bin string, lines []string) [][]string {
	t.Helper()
	root := t.TempDir()
	made := make([][]string, len(lines))
	failed := make([]error, len(lines))
	work := make(chan int)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				made[i], failed[i] = runCommandLine(sh, bin, root, lines[i])
			}
		}()
	}
	for i := range lines {
		work <- i
	}
	close(work)
	wg.Wait()
	for i, err := range failed {
		if err != nil {
			t.Fatalf("%q: %v", lines[i], err)
		}
	}
	return made
}

func runCommandLine(sh pasteShell, bin, root, line string) ([]string, error) {
	dir, err := os.MkdirTemp(root, "")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh.path, sh.args...)
	cmd.WaitDelay = time.Second
	if sh.args[len(sh.args)-1] == "-c" {
		cmd.Args = append(cmd.Args, line)
	} else {
		cmd.Stdin = strings.NewReader(line + "\n")
	}
	cmd.Dir, cmd.Env = dir, []string{"PATH=" + bin, "HOME=" + dir}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Run() // most lines name no command, or leave a quote open
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s did not finish: %v", sh.name, ctx.Err())
	}
	return filepath.Glob(filepath.Join(dir, "MARKER*"))
}

// TestForgetRefusalPastesAsPrinted: without a terminal, scterm forget refuses
// part of a name with a command that forgets the device, and no text of the
// device's comes before it on the line, where a name could pose as the
// command: the line is the query, fixed words and the command, nothing else.
// Pasted into each shell, the command runs nothing else and gives
// scterm forget and the name or, when the name is not plain text, the
// identity; run, it forgets the pairing. A device named help is given by its
// identity: scterm forget help prints the help.
func TestForgetRefusalPastesAsPrinted(t *testing.T) {
	withInteractive(t, false)
	cases := []struct {
		name, query string
		printed     string // the name as the command writes it; "": the short identity
		home        string
		fp          peer.Fingerprint
	}{
		{name: "Pixel 8", query: "pix", printed: `'Pixel 8'`},
		{name: "O'Brien's Pixel", query: "o'b", printed: `'O'\''Brien'\''s Pixel'`},
		{name: "$(touch MARKER-forget)", query: "$(t"},
		{name: "Pixel: to forget it, run scterm forget Pixel;touch MARKER-spoof #", query: "pix"},
		{name: "Pixel 8 x;touch MARKER-fragment #", query: "pix"},
		{name: "Pixel scterm forget Victim", query: "pix"},
		{name: `x sc""term forget Victim`, query: "x"},
		{name: "Pixel" + strings.Repeat("\u2800", 70) + "run scterm forget 'x;touch MARKER-pad #'", query: "pix"},
		{name: "help", query: "hel"},
		{name: dirtyPixel8, query: "pix"},
		{name: "Pixel 8\u202e", query: "pix"}, // cleaned, it is Pixel 8, which only partly matches it
	}
	commands := make([]string, len(cases))
	for i := range cases {
		c := &cases[i]
		c.home, c.fp = t.TempDir(), peer.FingerprintOf([]byte(c.name))
		t.Setenv("SCTERM_HOME", c.home)
		rec := peer.Record{Fingerprint: c.fp.Hex(), Name: c.name, Host: "192.0.2.1", Port: 27300, Grants: []string{peer.GrantView}, PairedAtMs: 1}
		if err := peer.OpenStore(c.home).Put(rec); err != nil {
			t.Fatal(err)
		}
		arg := c.printed
		if arg == "" {
			arg = c.fp.Short()
		}
		err := forgetCommand([]string{c.query})
		lead := fmt.Sprintf("%q only partly matches a paired device: to forget it, run ", c.query)
		command, ok := strings.CutPrefix(fmt.Sprint(err), lead)
		if err == nil || !ok || command != "scterm forget "+arg {
			t.Fatalf("forget %s, with %q paired: %v; want %s", c.query, c.name, err, lead+"scterm forget "+arg)
		}
		commands[i] = command
	}
	var pasted [][]string // what scterm got from each command in the first shell
	for _, sh := range pasteShells {
		t.Run(sh.name, func(t *testing.T) {
			got := pasteInto(t, sh, commands)
			for i, c := range cases {
				want := c.fp.Short()
				if c.printed != "" {
					want = c.name
				}
				if !slices.Equal(got[i], []string{"forget", want}) {
					t.Errorf("%q: pasted %s, scterm got %q; want forget %q", c.name, commands[i], got[i], want)
				}
			}
			if pasted == nil {
				pasted = got
			}
		})
	}
	for i, c := range cases {
		if pasted == nil {
			break
		}
		args := pasted[i]
		// runPeerCommand exits on an error, so only what names this pairing
		// whole is run.
		if len(args) != 2 || args[0] != "forget" || args[1] != c.name && args[1] != c.fp.Short() && args[1] != c.fp.Hex() {
			continue
		}
		t.Setenv("SCTERM_HOME", c.home)
		store := peer.OpenStore(c.home)
		if rec, err := store.Find(args[1]); err != nil || rec.Fingerprint != c.fp.Hex() {
			t.Errorf("%q: Find(%q) gives %q, %v; want this pairing", c.name, args[1], rec.Name, err)
			continue
		}
		var handled bool
		stdout := captureStdout(t, func() { handled = runPeerCommand(args) })
		said := "The device is " + c.fp.Short() + ", named "
		forgot := "\nForgot " + c.fp.Short() + ". "
		if recs, err := store.All(); !handled || err != nil || len(recs) != 0 || !strings.HasPrefix(stdout, said) || !strings.Contains(stdout, forgot) {
			t.Errorf("%q: scterm %q printed %q and left %d pairings, %v; want the pairing forgotten", c.name, args, stdout, len(recs), err)
		}
	}
}

// TestMissingShellSkipsUnlessRequired: a shell that is not installed skips the
// test that pastes into it, unless SCTERM_TEST_REQUIRE_SHELLS is set, as CI
// sets it: then it fails the test, where a skip would go unnoticed. A shell
// that is installed runs, with the variable set or not. Runs pasteInto in a
// child process, on the shell that SCTERM_TEST_PASTE_SHELL names.
func TestMissingShellSkipsUnlessRequired(t *testing.T) {
	if path := os.Getenv("SCTERM_TEST_PASTE_SHELL"); path != "" {
		got := pasteInto(t, pasteShell{"sh", path, []string{"-c"}, posixScterm}, []string{"scterm x"})
		if !slices.Equal(got[0], []string{"x"}) {
			t.Fatalf("pasted into %s, scterm got %q; want x", path, got[0])
		}
		return
	}
	missing := filepath.Join(t.TempDir(), "sh")
	for _, tc := range []struct {
		shell, require string // require is SCTERM_TEST_REQUIRE_SHELLS
		status         int
		want           []string // in the child's output
	}{
		{missing, "", 0, []string{"--- SKIP: TestMissingShellSkipsUnlessRequired", missing + " is not installed\n"}},
		{missing, "1", 1, []string{"--- FAIL: TestMissingShellSkipsUnlessRequired", missing + " is not installed, and SCTERM_TEST_REQUIRE_SHELLS is set\n"}},
		{"/bin/sh", "", 0, []string{"--- PASS: TestMissingShellSkipsUnlessRequired"}},
		{"/bin/sh", "1", 0, []string{"--- PASS: TestMissingShellSkipsUnlessRequired"}},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMissingShellSkipsUnlessRequired$", "-test.v")
		cmd.Env = append(os.Environ(), "SCTERM_TEST_PASTE_SHELL="+tc.shell, "SCTERM_TEST_REQUIRE_SHELLS="+tc.require)
		out, err := cmd.CombinedOutput()
		if status := cmd.ProcessState.ExitCode(); status != tc.status {
			t.Errorf("SCTERM_TEST_REQUIRE_SHELLS=%q, pasting into %s: exit status %d (%v), want %d; output %q", tc.require, tc.shell, status, err, tc.status, out)
		}
		for _, w := range tc.want {
			if !strings.Contains(string(out), w) {
				t.Errorf("SCTERM_TEST_REQUIRE_SHELLS=%q, pasting into %s: output %q, want %q in it", tc.require, tc.shell, out, w)
			}
		}
	}
}
