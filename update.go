package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"scterm/internal/release"
)

// releaseBinary is the release asset holding the linux/amd64 build.
const releaseBinary = "scterm"

// runUpdateCommand runs `scterm update` and `scterm version`, and reports
// whether args named one of them.
func runUpdateCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "version":
		fmt.Println("scterm " + version)
	case "update":
		if err := updateCommand(); err != nil {
			fatal(err)
		}
	default:
		return false
	}
	return true
}

// updateCommand brings this scterm up to date: a release build replaces
// itself with the latest release; a source build pulls and rebuilds its
// checkout.
func updateCommand() error {
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
	return updateRelease(exe)
}

func updateRelease(exe string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("releases only include a linux/amd64 binary; on %s/%s, pull your source checkout and rebuild", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := &http.Client{}
	agent := "scterm/" + version
	rel, err := release.Latest(ctx, client, release.LatestURL, agent)
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
	fmt.Printf("Downloading scterm %s…\n", rel.Version)
	// Next to the binary, so the final rename cannot cross filesystems.
	next, err := release.Download(ctx, client, asset, filepath.Dir(exe), agent)
	if errors.Is(err, fs.ErrPermission) {
		next, err = release.Download(ctx, client, asset, os.TempDir(), agent)
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
	if err := os.Rename(next, exe); err != nil {
		os.Remove(next)
		return err
	}
	fmt.Printf("Updated %s from %s to %s.\n", exe, version, rel.Version)
	return nil
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
