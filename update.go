package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	"unicode"

	"scterm/internal/release"
)

// releaseBinary is the release asset holding the linux/amd64 build.
const releaseBinary = "scterm"

// latestRelease is where update finds the newest release: release.LatestURL,
// or a test server speaking the same JSON.
var latestRelease = release.LatestURL

// maxNoteLines is how much of a release's notes update shows before it asks;
// the rest is on the release page.
const maxNoteLines = 15

// stderrIsTerminal reports whether stderr is a terminal, where a download's
// progress can be redrawn in place.
var stderrIsTerminal = func() bool { return isTerminal(syscall.Stderr) }

// updateOptions are the options `scterm update` was run with.
type updateOptions struct {
	check bool // only say whether a newer release exists
	yes   bool // replace this binary without asking
}

// runUpdateCommand runs `scterm update` and `scterm version`, and reports
// whether args named one of them.
func runUpdateCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "version":
		if len(args) > 1 {
			if isHelpArg(args[1]) {
				fmt.Print(versionHelp)
				return true
			}
			usageError("usage: scterm version (it takes no arguments)")
		}
		fmt.Println("scterm " + version)
	case "update":
		opts, help, err := parseUpdateArgs(args[1:])
		if help {
			fmt.Print(updateHelp)
			return true
		}
		if err != nil {
			usageError(err.Error())
		}
		if err := updateCommand(opts); err != nil {
			fatal(err)
		}
	default:
		return false
	}
	return true
}

// parseUpdateArgs reads update's options, and whether one of them asks for
// its help.
func parseUpdateArgs(args []string) (opts updateOptions, help bool, err error) {
	for _, arg := range args {
		switch {
		case isHelpArg(arg):
			return opts, true, nil
		case arg == "--check":
			opts.check = true
		case arg == "-y" || arg == "--yes":
			opts.yes = true
		default:
			if err == nil {
				err = fmt.Errorf("usage: scterm update [--check] [-y|--yes] (%q is not one of these)", arg)
			}
		}
	}
	return opts, false, err
}

const updateHelp = `usage: scterm update [--check] [-y|--yes]

A release binary looks for a newer GitHub release and shows its version,
release page and the start of its notes, then asks before going ahead when
there is a terminal to ask on. It downloads the release, checks it against
the SHA-256 digest GitHub publishes for it (when there is none, it says so
and checks only the size), and replaces itself once the new binary has
started on this system. The binary it replaces is kept beside it, with .old
added to its name. A build from a source checkout pulls and rebuilds it
instead.

  --check    say whether a newer release exists, without downloading it
  -y, --yes  update without asking
`

const versionHelp = `usage: scterm version

Prints this binary's version: scterm X.Y.Z for a release, scterm dev for a
build from source.
`

// isHelpArg reports whether arg asks a command for its help.
func isHelpArg(arg string) bool {
	switch arg {
	case "-h", "-help", "--help", "help":
		return true
	}
	return false
}

// updateCommand brings this scterm up to date: a release build replaces
// itself with the latest release; a source build pulls and rebuilds its
// checkout.
func updateCommand(opts updateOptions) error {
	if version == "dev" && opts.check {
		fmt.Println("scterm dev is built from source, so there is no release to compare it with: scterm update pulls and rebuilds its checkout.")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if version == "dev" {
		return updateCheckout(exe)
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("releases only include a linux/amd64 binary; on %s/%s, pull your source checkout and rebuild", runtime.GOOS, runtime.GOARCH)
	}
	return updateRelease(exe, opts)
}

func updateRelease(exe string, opts updateOptions) error {
	client := &http.Client{}
	agent := "scterm/" + version
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	rel, err := release.Latest(ctx, client, latestRelease, agent)
	cancel()
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if !release.Newer(rel.Version, version) {
		fmt.Printf("scterm %s is the latest release.\n", version)
		return nil
	}
	asset := rel.Asset(releaseBinary)
	if asset == nil {
		return fmt.Errorf("release %s has no Linux binary", rel.Version)
	}
	if opts.check {
		fmt.Printf("scterm %s is available (you have %s): run scterm update\n", rel.Version, version)
		return nil
	}
	describeRelease(rel, asset)
	if !opts.yes && interactive() && !confirmed(fmt.Sprintf("Update scterm %s to %s? [Y/n] ", version, rel.Version)) {
		fmt.Printf("Kept scterm %s.\n", version)
		return nil
	}
	fmt.Printf("Downloading scterm %s…\n", rel.Version)
	// A limit of its own: the question above waits as long as it takes.
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Next to the binary, so the final rename cannot cross filesystems.
	next, err := fetch(ctx, client, asset, filepath.Dir(exe), agent)
	if errors.Is(err, fs.ErrPermission) {
		next, err = fetch(ctx, client, asset, os.TempDir(), agent)
		if err != nil {
			return err
		}
		if err := checkRuns(next, rel.Version); err != nil {
			os.Remove(next)
			return err
		}
		return fmt.Errorf("no permission to replace %s; install the new version with:\n  sudo install -m 755 %s %s", exe, next, exe)
	}
	if err != nil {
		return err
	}
	if err := checkRuns(next, rel.Version); err != nil {
		os.Remove(next)
		return err
	}
	return replaceBinary(next, exe, rel.Version)
}

// describeRelease says what updating to rel brings: its version, release
// page and the start of its notes, and, when GitHub published no digest for
// its binary, what is left to check it by.
func describeRelease(rel *release.Release, asset *release.Asset) {
	fmt.Printf("scterm %s is available (you have %s).\n", rel.Version, version)
	if rel.Page != "" {
		fmt.Printf("Release page: %s\n", rel.Page)
	}
	if lines, more := noteLines(rel.Notes); len(lines) > 0 {
		fmt.Println()
		for _, l := range lines {
			if l != "" {
				l = "  " + l
			}
			fmt.Println(l)
		}
		switch {
		case more && rel.Page != "":
			fmt.Println("  … (the rest is on the release page)")
		case more:
			fmt.Println("  …")
		}
		fmt.Println()
	}
	switch {
	case asset.SHA256 == "" && asset.Size > 0:
		fmt.Printf("GitHub published no SHA-256 digest for %s, so only its size is checked.\n", asset.Name)
	case asset.SHA256 == "":
		fmt.Printf("GitHub published no SHA-256 digest or size for %s, so neither is checked.\n", asset.Name)
	}
}

// noteLines is the start of a release's notes as update shows them: without
// control characters (GitHub often ends lines with \r\n) or the blank lines
// around them, and at most maxNoteLines lines. more reports that some were
// left out.
func noteLines(notes string) (lines []string, more bool) {
	notes = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, notes)
	for _, l := range strings.Split(notes, "\n") {
		lines = append(lines, strings.TrimRightFunc(l, unicode.IsSpace))
	}
	lines = trimBlank(lines)
	if len(lines) > maxNoteLines {
		lines, more = trimBlank(lines[:maxNoteLines]), true
	}
	return lines, more
}

// trimBlank drops the empty lines at either end of lines.
func trimBlank(lines []string) []string {
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// confirmed asks question and reports whether the answer is yes: Enter, y
// or yes. No answer at all, when the input ends, is a no.
func confirmed(question string) bool {
	answer, err := ask(question)
	if err != nil {
		fmt.Println() // end the prompt's line, which no answer did
		return false
	}
	switch strings.ToLower(answer) {
	case "", "y", "yes":
		return true
	}
	return false
}

// fetch downloads asset into dir, showing its progress on stderr when that
// is a terminal.
func fetch(ctx context.Context, client *http.Client, asset *release.Asset, dir, agent string) (string, error) {
	if !stderrIsTerminal() {
		return release.Download(ctx, client, asset, dir, agent, nil)
	}
	line := &progressLine{w: os.Stderr, size: asset.Size}
	defer line.clear()
	return release.Download(ctx, client, asset, dir, agent, line.show)
}

// progressLine shows how much of a download has arrived, redrawn in place
// on one line of w.
type progressLine struct {
	w     io.Writer
	size  int64 // 0 when unknown
	shown string
}

func (p *progressLine) show(done int64) {
	text := fmt.Sprintf("%.1f MB", float64(done)/1e6)
	if p.size > 0 {
		text = fmt.Sprintf("%3d%%  %.1f of %.1f MB", min(done*100/p.size, 100), float64(done)/1e6, float64(p.size)/1e6)
	}
	if text != p.shown {
		fmt.Fprint(p.w, "\r"+text)
		p.shown = text
	}
}

// clear erases the line, if anything was drawn on it.
func (p *progressLine) clear() {
	if p.shown != "" {
		fmt.Fprint(p.w, "\r\x1b[K")
	}
}

// replaceBinary puts the new binary at next in exe's place and keeps the
// one it replaces as exe.old, instead of any older one. Until the new binary
// is in place, a failure leaves exe and exe.old as they were.
func replaceBinary(next, exe, newVersion string) error {
	saved, err := copyBeside(exe)
	if err != nil {
		os.Remove(next)
		return fmt.Errorf("could not keep a copy of %s, so it was not replaced: %w", exe, err)
	}
	if err := os.Rename(next, exe); err != nil {
		os.Remove(next)
		os.Remove(saved)
		return err
	}
	fmt.Printf("Updated %s from %s to %s.\n", exe, version, newVersion)
	old := exe + ".old"
	if err := os.Rename(saved, old); err != nil {
		fmt.Fprintf(os.Stderr, "scterm: could not keep the previous version as %s: %v\n", old, err)
		old = saved
	}
	fmt.Printf("scterm %s is kept as %s. To go back to it: mv %s %s\n", version, old, shellQuote(old), shellQuote(exe))
	return nil
}

// copyBeside copies the file at path to a new file in the same directory,
// with the same permissions, and returns the copy's path.
func copyBeside(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return "", err
	}
	dst, err := os.CreateTemp(filepath.Dir(path), ".scterm-old-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(dst, src)
	if err == nil {
		err = dst.Chmod(info.Mode().Perm())
	}
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
}

// shellQuote quotes path for a POSIX shell when it holds anything but
// letters, digits and . / _ -.
func shellQuote(path string) string {
	if strings.Trim(path, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789./_-") == "" {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// updateCheckout updates a source build in place: pull its git checkout,
// rebuild, and swap the new binary in.
func updateCheckout(exe string) error {
	dir := checkoutOf(exe)
	if dir == "" {
		return errors.New("this scterm was built from source outside its checkout; in the checkout, run: git pull && go build -o scterm .")
	}
	fmt.Printf("Updating the checkout in %s…\n", dir)
	if err := runIn(dir, "git", "pull", "--ff-only"); err != nil {
		return err
	}
	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("git rev-parse: %w", err)
	}
	rev := strings.TrimSpace(string(head))
	if built, modified := buildRevision(); built == rev && !modified {
		fmt.Printf("scterm is up to date (%s).\n", rev[:min(12, len(rev))])
		return nil
	}
	fmt.Println("Building…")
	next := exe + ".new"
	if err := runIn(dir, "go", "build", "-o", next, "."); err != nil {
		os.Remove(next)
		return err
	}
	if err := checkRuns(next, version); err != nil {
		os.Remove(next)
		return err
	}
	if err := os.Rename(next, exe); err != nil {
		os.Remove(next)
		return err
	}
	fmt.Printf("Updated %s to %s.\n", exe, rev[:min(12, len(rev))])
	return nil
}

// checkoutOf finds the scterm source checkout holding exe: its directory
// or one of the two above it.
func checkoutOf(exe string) string {
	dir := filepath.Dir(exe)
	for i := 0; i < 3; i++ {
		mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if _, gitErr := os.Stat(filepath.Join(dir, ".git")); err == nil && gitErr == nil && bytes.HasPrefix(mod, []byte("module scterm\n")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// buildRevision is the commit this binary was built from, as the Go
// toolchain stamps it in a git checkout.
func buildRevision() (revision string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return revision, modified
}

// checkRuns makes sure a new binary starts on this system, and is the
// version expected, before it replaces the running one: a release binary
// needs the same shared libraries (ffmpeg, PulseAudio) it was built against.
func checkRuns(path, want string) error {
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	out, err := exec.Command(path, "version").CombinedOutput()
	got := strings.TrimSpace(string(out))
	if err != nil {
		first, _, _ := strings.Cut(got, "\n")
		return fmt.Errorf("the new scterm does not run on this system (%s); build it from source instead", first)
	}
	if got != "scterm "+want {
		return fmt.Errorf("the new binary reports %q, not scterm %s", got, want)
	}
	return nil
}

func runIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
