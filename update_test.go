package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCheckoutOf(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module scterm\n\ngo 1.27\n"), 0o644)
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	os.Mkdir(filepath.Join(root, "bin"), 0o755)
	for _, exe := range []string{filepath.Join(root, "scterm"), filepath.Join(root, "bin", "scterm")} {
		if got := checkoutOf(exe); got != root {
			t.Errorf("checkoutOf(%s) = %q, want %q", exe, got, root)
		}
	}
	if got := checkoutOf(filepath.Join(t.TempDir(), "scterm")); got != "" {
		t.Errorf("found a checkout where there is none: %q", got)
	}
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/other\n"), 0o644)
	os.Mkdir(filepath.Join(other, ".git"), 0o755)
	if got := checkoutOf(filepath.Join(other, "scterm")); got != "" {
		t.Errorf("took another module's checkout: %q", got)
	}
}

func TestCheckRunsRefusesABinaryThatCannotStart(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	os.WriteFile(good, []byte("#!/bin/sh\necho 'scterm 2.1.0'\n"), 0o644)
	if err := checkRuns(good, "2.1.0"); err != nil {
		t.Errorf("a working binary was refused: %v", err)
	}
	if err := checkRuns(good, "2.2.0"); err == nil {
		t.Error("a binary reporting another version was accepted")
	}
	broken := filepath.Join(dir, "broken")
	os.WriteFile(broken, []byte("#!/bin/sh\necho 'error while loading shared libraries: libavcodec.so.61' >&2\nexit 127\n"), 0o644)
	if err := checkRuns(broken, "2.1.0"); err == nil || !strings.Contains(err.Error(), "libavcodec.so.61") {
		t.Errorf("a binary missing its libraries: %v", err)
	}
}

// fakeBinary is a stand-in scterm that answers `version` as release ver.
func fakeBinary(ver string) []byte { return []byte("#!/bin/sh\necho 'scterm " + ver + "'\n") }

// fakeRelease is the latest release as a test server publishes it.
type fakeRelease struct {
	version  string // the tag without its "v"
	notes    string
	binary   []byte // the scterm asset; nil for a release without one
	size     int64  // the size published for it, when not its own
	digest   string // the digest published for it, when not its own
	noSize   bool   // no size is published for it
	noDigest bool   // no digest is published for it
	missing  bool   // downloading it finds nothing
	apiDown  bool   // the release list cannot be had
}

// serveRelease publishes rel the way GitHub's API does, and counts the
// downloads of its binary.
func serveRelease(t *testing.T, rel fakeRelease) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	downloads := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			if rel.apiDown {
				http.Error(w, "try again later", http.StatusServiceUnavailable)
				return
			}
			type asset struct {
				Name   string `json:"name"`
				URL    string `json:"browser_download_url"`
				Size   int64  `json:"size"`
				Digest string `json:"digest"`
			}
			var assets []asset
			if rel.binary != nil {
				a := asset{Name: "scterm", URL: "http://" + r.Host + "/scterm", Size: int64(len(rel.binary)), Digest: "sha256:" + sha256Hex(rel.binary)}
				switch {
				case rel.noSize:
					a.Size = 0
				case rel.size != 0:
					a.Size = rel.size
				}
				switch {
				case rel.noDigest:
					a.Digest = ""
				case rel.digest != "":
					a.Digest = rel.digest
				}
				assets = append(assets, a)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v" + rel.version,
				"html_url": "https://example.invalid/releases/tag/v" + rel.version,
				"body":     rel.notes,
				"assets":   assets,
			})
		case "/scterm":
			downloads.Add(1)
			if rel.missing {
				http.NotFound(w, r)
				return
			}
			w.Write(rel.binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, downloads
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// updating points update at srv, as scterm current, with no terminal to ask
// on or to show progress on; tests turn on what they need.
func updating(t *testing.T, srv *httptest.Server, current string) {
	t.Helper()
	oldURL, oldVersion, oldProgress := latestRelease, version, stderrIsTerminal
	t.Cleanup(func() { latestRelease, version, stderrIsTerminal = oldURL, oldVersion, oldProgress })
	latestRelease = srv.URL + "/latest"
	version = current
	stderrIsTerminal = func() bool { return false }
	withInteractive(t, false)
}

// answering makes input what is typed at the terminal.
func answering(t *testing.T, input string) {
	t.Helper()
	old := terminalInput
	t.Cleanup(func() { terminalInput = old })
	terminalInput = bufioReader(input)
}

// installed puts a stand-in for scterm current in a directory of its own
// and returns its path.
func installed(t *testing.T, current string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "scterm")
	if err := os.WriteFile(exe, fakeBinary(current), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// runUpdate updates the release binary at exe and returns what it printed.
func runUpdate(t *testing.T, exe string, opts updateOptions) (stdout, stderr string, err error) {
	t.Helper()
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() { err = updateRelease(exe, opts) })
	})
	return stdout, stderr, err
}

// files maps each name in dir to what it holds.
func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		m[e.Name()] = string(b)
	}
	return m
}

// goBack runs the command an update printed for going back to the previous
// version, from an empty directory.
func goBack(t *testing.T, out string) {
	t.Helper()
	_, cmd, ok := strings.Cut(out, "To go back to it: ")
	if !ok {
		t.Fatalf("no way back in %q", out)
	}
	cmd, _, _ = strings.Cut(cmd, "\n")
	sh := exec.Command("sh", "-c", cmd)
	sh.Dir = t.TempDir()
	if b, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", cmd, err, b)
	}
}

func TestUpdateArgs(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    updateOptions
		help    bool
		refuses string // the word a usage error names
	}{
		{args: nil},
		{args: []string{"--check"}, want: updateOptions{check: true}},
		{args: []string{"-y"}, want: updateOptions{yes: true}},
		{args: []string{"--yes"}, want: updateOptions{yes: true}},
		{args: []string{"--check", "--yes"}, want: updateOptions{check: true, yes: true}},
		{args: []string{"-h"}, help: true},
		{args: []string{"now", "--help"}, help: true},
		{args: []string{"now"}, refuses: `"now"`},
		{args: []string{"-y", "--force"}, refuses: `"--force"`},
		{args: []string{"-check"}, refuses: `"-check"`},
	} {
		opts, help, err := parseUpdateArgs(tc.args)
		if help != tc.help {
			t.Errorf("%q: help = %v", tc.args, help)
			continue
		}
		if tc.refuses != "" {
			if err == nil || !strings.HasPrefix(err.Error(), "usage: scterm update [--check] [-y|--yes]") || !strings.Contains(err.Error(), tc.refuses) {
				t.Errorf("%q: %v, want a usage error naming %s", tc.args, err, tc.refuses)
			}
			continue
		}
		if !tc.help && (err != nil || opts != tc.want) {
			t.Errorf("%q: %+v, %v; want %+v", tc.args, opts, err, tc.want)
		}
	}
	out := captureStdout(t, func() { runUpdateCommand([]string{"update", "--help"}) })
	for _, want := range []string{"usage: scterm update [--check] [-y|--yes]\n", "\n  --check ", "\n  -y, --yes ", ".old"} {
		if !strings.Contains(out, want) {
			t.Errorf("update's help lacks %q:\n%s", want, out)
		}
	}
}

func TestUpdateCheck(t *testing.T) {
	srv, downloads := serveRelease(t, fakeRelease{version: "1.3.0", binary: fakeBinary("1.3.0"), notes: "Faster."})
	for _, tc := range []struct{ current, want string }{
		{"1.2.3", "scterm 1.3.0 is available (you have 1.2.3): run scterm update\n"},
		{"1.3.0", "scterm 1.3.0 is the latest release.\n"},
	} {
		updating(t, srv, tc.current)
		withInteractive(t, true)
		answering(t, "y\n")
		exe := installed(t, tc.current)
		before := files(t, filepath.Dir(exe))
		out, _, err := runUpdate(t, exe, updateOptions{check: true})
		if err != nil || out != tc.want {
			t.Errorf("--check as %s: %q, %v; want %q", tc.current, out, err, tc.want)
		}
		if after := files(t, filepath.Dir(exe)); !maps.Equal(after, before) {
			t.Errorf("--check as %s changed %v to %v", tc.current, before, after)
		}
	}
	if n := downloads.Load(); n != 0 {
		t.Errorf("--check downloaded the binary %d times", n)
	}
}

func TestUpdateCheckOnASourceBuild(t *testing.T) {
	if exe, _ := os.Executable(); checkoutOf(exe) != "" {
		t.Skip("this test binary sits in a checkout, which a regression here would pull")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a source build's --check asked for %s", r.URL)
	}))
	t.Cleanup(srv.Close)
	updating(t, srv, "dev")
	var err error
	out := captureStdout(t, func() { err = updateCommand(updateOptions{check: true}) })
	if err != nil || out != "scterm dev is built from source, so there is no release to compare it with: scterm update pulls and rebuilds its checkout.\n" {
		t.Errorf("--check on a source build: %q, %v", out, err)
	}
}

func TestUpdateAsksOnATerminal(t *testing.T) {
	for _, tc := range []struct {
		answer  string
		updates bool
	}{
		{"\n", true}, // Enter means yes
		{"y\n", true},
		{"Yes\n", true},
		{"n\n", false},
		{"no\n", false},
		{"later\n", false},
		{"", false}, // the input ended without an answer
	} {
		srv, downloads := serveRelease(t, fakeRelease{version: "1.3.0", binary: fakeBinary("1.3.0")})
		updating(t, srv, "1.2.3")
		withInteractive(t, true)
		answering(t, tc.answer)
		exe := installed(t, "1.2.3")
		out, errOut, err := runUpdate(t, exe, updateOptions{})
		if err != nil {
			t.Errorf("answer %q: %v", tc.answer, err)
			continue
		}
		// The question goes to stderr, where it shows with stdout redirected
		// (scterm update > log); the input ending ends its line there too.
		question := "Update scterm 1.2.3 to 1.3.0? [Y/n] "
		if tc.answer == "" {
			question += "\n"
		}
		if errOut != question || strings.Contains(out, "[Y/n]") {
			t.Errorf("answer %q: stderr %q, stdout %q; want the question on stderr only", tc.answer, errOut, out)
		}
		got, _ := os.ReadFile(exe)
		if updated := string(got) == string(fakeBinary("1.3.0")); updated != tc.updates {
			t.Errorf("answer %q: updated = %v:\n%s", tc.answer, updated, out)
		}
		if tc.updates {
			continue
		}
		want := "Release page: https://example.invalid/releases/tag/v1.3.0\nKept scterm 1.2.3.\n"
		if !strings.HasSuffix(out, want) || downloads.Load() != 0 {
			t.Errorf("answer %q: %d downloads, printed %q", tc.answer, downloads.Load(), out)
		}
		if left := files(t, filepath.Dir(exe)); len(left) != 1 {
			t.Errorf("answer %q left %v", tc.answer, slices.Sorted(maps.Keys(left)))
		}
	}
}

func TestUpdateWithoutAQuestion(t *testing.T) {
	for _, tc := range []struct {
		name          string
		terminal, yes bool
	}{
		{"--yes on a terminal", true, true},
		{"no terminal", false, false},
	} {
		srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: fakeBinary("1.3.0"), notes: "Faster startup."})
		updating(t, srv, "1.2.3")
		withInteractive(t, tc.terminal)
		answering(t, "n\n") // the answer, were there a question
		exe := installed(t, "1.2.3")
		out, _, err := runUpdate(t, exe, updateOptions{yes: tc.yes})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		want := "scterm 1.3.0 is available (you have 1.2.3).\n" +
			"Release page: https://example.invalid/releases/tag/v1.3.0\n" +
			"\n  Faster startup.\n\n" +
			"Downloading scterm 1.3.0…\n" +
			"Updated " + exe + " from 1.2.3 to 1.3.0.\n"
		if !strings.HasPrefix(out, want) {
			t.Errorf("%s: printed\n%s\nwant it to start\n%s", tc.name, out, want)
		}
		if got, _ := os.ReadFile(exe); string(got) != string(fakeBinary("1.3.0")) {
			t.Errorf("%s: not updated", tc.name)
		}
	}
}

func TestUpdateKeepsThePreviousBinary(t *testing.T) {
	srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: fakeBinary("1.3.0")})
	updating(t, srv, "1.2.3")
	dir := filepath.Join(t.TempDir(), "it's mine")
	exe := filepath.Join(dir, "scterm")
	os.Mkdir(dir, 0o755)
	os.WriteFile(exe+".old", fakeBinary("1.1.0"), 0o755) // from an earlier update
	os.WriteFile(exe, fakeBinary("1.2.3"), 0o700)
	os.Chmod(exe, 0o750) // whatever the umask
	out, _, err := runUpdate(t, exe, updateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"scterm": string(fakeBinary("1.3.0")), "scterm.old": string(fakeBinary("1.2.3"))}
	if got := files(t, dir); !maps.Equal(got, want) {
		t.Errorf("after the update: %v, want %v", got, want)
	}
	if info, err := os.Stat(exe + ".old"); err != nil || info.Mode().Perm() != 0o750 {
		t.Errorf("the kept binary: %v, want the mode it had (0750)", info)
	}
	if !strings.Contains(out, "Updated "+exe+" from 1.2.3 to 1.3.0.\nscterm 1.2.3 is kept as "+exe+".old. To go back to it: mv ") {
		t.Errorf("printed %q", out)
	}
	goBack(t, out)
	if got := files(t, dir); !maps.Equal(got, map[string]string{"scterm": string(fakeBinary("1.2.3"))}) {
		t.Errorf("going back left %v", got)
	}
}

func TestUpdateKeepsThePreviousBinaryWhenOldIsTaken(t *testing.T) {
	srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: fakeBinary("1.3.0")})
	updating(t, srv, "1.2.3")
	exe := installed(t, "1.2.3")
	os.Mkdir(exe+".old", 0o755)
	os.WriteFile(filepath.Join(exe+".old", "notes.txt"), []byte("mine"), 0o644)
	out, stderr, err := runUpdate(t, exe, updateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(fakeBinary("1.3.0")) {
		t.Error("not updated")
	}
	if !strings.Contains(stderr, "could not keep the previous version as "+exe+".old") {
		t.Errorf("stderr %q", stderr)
	}
	if !strings.Contains(out, "scterm 1.2.3 is kept as "+filepath.Join(filepath.Dir(exe), ".scterm-old-")) {
		t.Errorf("printed %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(exe+".old", "notes.txt")); string(got) != "mine" {
		t.Error("the directory in the way was touched")
	}
	goBack(t, out)
	if got, _ := os.ReadFile(exe); string(got) != string(fakeBinary("1.2.3")) {
		t.Error("going back did not bring back 1.2.3")
	}
}

func TestFailedUpdateKeepsTheOriginal(t *testing.T) {
	next := fakeBinary("1.3.0")
	for name, tc := range map[string]struct {
		rel    fakeRelease
		setup  func(exe string)
		errHas string
	}{
		"no release list":   {rel: fakeRelease{apiDown: true}, errHas: "checking for updates"},
		"no Linux binary":   {rel: fakeRelease{version: "1.3.0"}, errHas: "has no Linux binary"},
		"nothing to fetch":  {rel: fakeRelease{version: "1.3.0", binary: next, missing: true}, errHas: "404"},
		"another digest":    {rel: fakeRelease{version: "1.3.0", binary: next, digest: "sha256:" + sha256Hex([]byte("other"))}, errHas: "does not match the published"},
		"short download":    {rel: fakeRelease{version: "1.3.0", binary: next, size: int64(len(next)) + 5}, errHas: "expected"},
		"does not run":      {rel: fakeRelease{version: "1.3.0", binary: []byte("#!/bin/sh\necho 'error while loading shared libraries: libavcodec.so.61' >&2\nexit 127\n")}, errHas: "does not run on this system"},
		"another version":   {rel: fakeRelease{version: "1.3.0", binary: fakeBinary("1.2.9")}, errHas: "not scterm 1.3.0"},
		"no copy to keep":   {rel: fakeRelease{version: "1.3.0", binary: next}, setup: func(exe string) { os.Chmod(exe, 0) }, errHas: "could not keep a copy"},
		"last rename fails": {rel: fakeRelease{version: "1.3.0", binary: []byte("#!/bin/sh\nrm -f \"$0\"\necho 'scterm 1.3.0'\n")}, errHas: "no such file"},
	} {
		if tc.setup != nil && os.Geteuid() == 0 {
			t.Logf("%s: skipped, as root reads an unreadable file anyway", name)
			continue
		}
		srv, _ := serveRelease(t, tc.rel)
		updating(t, srv, "1.2.3")
		exe := installed(t, "1.2.3")
		os.WriteFile(exe+".old", fakeBinary("1.1.0"), 0o755)
		before := files(t, filepath.Dir(exe))
		if tc.setup != nil {
			tc.setup(exe)
		}
		_, _, err := runUpdate(t, exe, updateOptions{yes: true})
		os.Chmod(exe, 0o755)
		if err == nil || !strings.Contains(err.Error(), tc.errHas) {
			t.Errorf("%s: %v, want an error mentioning %q", name, err, tc.errHas)
		}
		if after := files(t, filepath.Dir(exe)); !maps.Equal(after, before) {
			t.Errorf("%s: the files went from %v to %v", name, before, after)
		}
	}
}

func TestUpdateWithoutPermissionLeavesItToSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may write anywhere")
	}
	srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: fakeBinary("1.3.0")})
	updating(t, srv, "1.2.3")
	tmp := t.TempDir()
	exe := installed(t, "1.2.3")
	dir := filepath.Dir(exe)
	before := files(t, dir)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	t.Setenv("TMPDIR", tmp)
	_, _, err := runUpdate(t, exe, updateOptions{yes: true})
	if err == nil || !strings.Contains(err.Error(), "no permission to replace "+exe) {
		t.Fatalf("%v, want no permission", err)
	}
	_, install, _ := strings.Cut(err.Error(), "sudo install -m 755 ")
	staged, _, _ := strings.Cut(install, " ")
	if got, _ := os.ReadFile(staged); filepath.Dir(staged) != tmp || string(got) != string(fakeBinary("1.3.0")) {
		t.Errorf("left for sudo: %s holding %q", staged, got)
	}
	if after := files(t, dir); !maps.Equal(after, before) {
		t.Errorf("the files went from %v to %v", before, after)
	}
}

func TestUpdateSaysWhenThereIsNoDigest(t *testing.T) {
	const question = "Update scterm 1.2.3 to 1.3.0? [Y/n] "
	for name, tc := range map[string]struct {
		rel    fakeRelease
		notice string
	}{
		"digest":            {fakeRelease{}, ""},
		"no digest":         {fakeRelease{noDigest: true}, "GitHub published no SHA-256 digest for scterm, so only its size is checked.\n"},
		"no digest or size": {fakeRelease{noDigest: true, noSize: true}, "GitHub published no SHA-256 digest or size for scterm, so neither is checked.\n"},
	} {
		tc.rel.version, tc.rel.binary = "1.3.0", fakeBinary("1.3.0")
		srv, _ := serveRelease(t, tc.rel)
		updating(t, srv, "1.2.3")
		withInteractive(t, true)
		answering(t, "\n")
		exe := installed(t, "1.2.3")
		out, errOut, err := runUpdate(t, exe, updateOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// The notice is the last line on stdout before the question, which
		// goes to stderr, and the answer, which starts the download.
		if !strings.Contains(out, tc.notice+"Downloading scterm 1.3.0…\n") || errOut != question ||
			(tc.notice == "" && strings.Contains(out, "GitHub published no")) {
			t.Errorf("%s: printed %q, and %q on stderr; want %q just before the question", name, out, errOut, tc.notice)
		}
		if got, _ := os.ReadFile(exe); string(got) != string(fakeBinary("1.3.0")) {
			t.Errorf("%s: not updated", name)
		}
	}
}

func TestNoteLines(t *testing.T) {
	numbered := func(n int) []string {
		var lines []string
		for i := 1; i <= n; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i))
		}
		return lines
	}
	for _, tc := range []struct {
		name  string
		notes string
		want  []string
		more  bool
	}{
		{"none", "", nil, false},
		{"blank", "\r\n  \n\t\n", nil, false},
		{"CRLF and blank ends", "\r\n\r\nFaster.\r\n\r\nFixes:  \r\n- audio\r\n\r\n", []string{"Faster.", "", "Fixes:", "- audio"}, false},
		{"control characters", "a\x1b[2Jb\x07\tc", []string{"a[2Jb\tc"}, false},
		{"fifteen lines", strings.Join(numbered(15), "\n"), numbered(15), false},
		{"sixteen lines", strings.Join(numbered(16), "\n"), numbered(15), true},
		{"cut at a blank line", strings.Join(append(numbered(14), "", "after"), "\n"), numbered(14), true},
	} {
		if lines, more := noteLines(tc.notes); !slices.Equal(lines, tc.want) || more != tc.more {
			t.Errorf("%s: %q, more=%v; want %q, more=%v", tc.name, lines, more, tc.want, tc.more)
		}
	}
}

func TestUpdateShowsTheStartOfTheNotes(t *testing.T) {
	var notes, shown strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&notes, "Line %d of the notes.\r\n", i)
		if i <= maxNoteLines {
			fmt.Fprintf(&shown, "  Line %d of the notes.\n", i)
		}
	}
	srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: fakeBinary("1.3.0"), notes: notes.String()})
	updating(t, srv, "1.2.3")
	withInteractive(t, true)
	answering(t, "n\n")
	out, errOut, err := runUpdate(t, installed(t, "1.2.3"), updateOptions{})
	want := "Release page: https://example.invalid/releases/tag/v1.3.0\n\n" + shown.String() +
		"  … (the rest is on the release page)\n\nKept scterm 1.2.3.\n"
	if err != nil || !strings.Contains(out, want) || strings.Contains(out, "Line 16") || strings.Contains(out, "\r") {
		t.Errorf("printed %q, %v; want it to hold %q", out, err, want)
	}
	if errOut != "Update scterm 1.2.3 to 1.3.0? [Y/n] " {
		t.Errorf("asked %q on stderr", errOut)
	}
}

func TestProgressLine(t *testing.T) {
	var b strings.Builder
	p := &progressLine{w: &b, size: 2_000_000}
	p.clear()
	for _, done := range []int64{0, 500_000, 500_001, 2_000_000} {
		p.show(done)
	}
	p.clear()
	if want := "\r  0%  0.0 of 2.0 MB\r 25%  0.5 of 2.0 MB\r100%  2.0 of 2.0 MB\r\x1b[K"; b.String() != want {
		t.Errorf("drew %q, want %q", b.String(), want)
	}
	b.Reset()
	(&progressLine{w: &b}).show(1_500_000)
	if b.String() != "\r1.5 MB" {
		t.Errorf("without a size, drew %q", b.String())
	}
}

func TestUpdateShowsProgressOnlyOnATerminal(t *testing.T) {
	// 300 kB, so the download takes several reads; the script stops at exit.
	bin := append(fakeBinary("1.3.0"), "exit 0\n# "...)
	bin = append(bin, bytes.Repeat([]byte("x"), 300_000)...)
	for _, tc := range []struct {
		name      string
		terminal  bool
		published int64 // the size GitHub states; more than arrives fails the download
	}{
		{"terminal", true, 0},
		{"no terminal", false, 0},
		{"terminal, failed download", true, int64(len(bin)) + 1},
	} {
		srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: bin, size: tc.published})
		updating(t, srv, "1.2.3")
		stderrIsTerminal = func() bool { return tc.terminal }
		_, stderr, err := runUpdate(t, installed(t, "1.2.3"), updateOptions{yes: true})
		if (err != nil) != (tc.published != 0) {
			t.Errorf("%s: %v", tc.name, err)
		}
		switch {
		case !tc.terminal:
			if stderr != "" {
				t.Errorf("%s: stderr got %q", tc.name, stderr)
			}
		case !strings.HasPrefix(stderr, "\r") || !strings.HasSuffix(stderr, "\r\x1b[K") || strings.Contains(stderr, "\n"):
			t.Errorf("%s: stderr got %q, want one line redrawn and then cleared", tc.name, stderr)
		case tc.published == 0 && !strings.Contains(stderr, "\r100%  0.3 of 0.3 MB\r"):
			t.Errorf("%s: stderr got %q, want it to reach 100%%", tc.name, stderr)
		}
	}
}
