package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestLicensesCommandPrintsTheTexts: scterm licenses prints scterm's license,
// the scrcpy server's and that of the Go code in the binary, each whole and as
// the repository has it, in that order, and nothing after them. The first two
// each come after a line that says what it covers; the third says it in its
// own first lines.
func TestLicensesCommandPrintsTheTexts(t *testing.T) {
	mit, err := os.ReadFile("LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	apache, err := os.ReadFile("third_party/LICENSE.scrcpy")
	if err != nil {
		t.Fatal(err)
	}
	bsd, err := os.ReadFile("third_party/LICENSE.golang")
	if err != nil {
		t.Fatal(err)
	}
	var handled bool
	var stderr string
	out := captureStdout(t, func() {
		stderr = captureStderr(t, func() { handled = runLicensesCommand([]string{"licenses"}) })
	})
	if !handled || stderr != "" {
		t.Fatalf("handled=%v, stderr %q; want it handled, with nothing on stderr", handled, stderr)
	}
	before, rest, ok := strings.Cut(out, string(mit))
	if !ok {
		t.Fatalf("scterm licenses does not print LICENSE whole: %.300q", out)
	}
	between, after, ok := strings.Cut(rest, string(apache))
	if !ok {
		t.Fatalf("scterm licenses does not print third_party/LICENSE.scrcpy whole after LICENSE: %.300q", rest)
	}
	if !strings.Contains(before, "scterm") || !strings.Contains(before, "MIT License") {
		t.Errorf("what comes before LICENSE does not say it is scterm's, the MIT License: %q", before)
	}
	if !strings.Contains(between, "scrcpy server") || !strings.Contains(between, "Apache License 2.0") {
		t.Errorf("what comes before third_party/LICENSE.scrcpy does not say it is the scrcpy server's, the Apache License 2.0: %q", between)
	}
	if after != string(bsd) {
		t.Errorf("after third_party/LICENSE.scrcpy, scterm licenses prints %.300q; want third_party/LICENSE.golang whole, and nothing after it", after)
	}
	lead, _, _ := strings.Cut(string(bsd), "Copyright")
	if !strings.Contains(lead, "Go's runtime and standard library") || !strings.Contains(lead, "BSD 3-Clause License") {
		t.Errorf("third_party/LICENSE.golang does not start by saying that it covers Go's runtime and standard library, under the BSD 3-Clause License: %q", lead)
	}
}

// TestGoLicenseIsTheModulesOwn: third_party/LICENSE.golang lists the modules
// that the binary links, no more and no fewer, and ends with the license that
// each of them carries, as the module cache has it (vendor/, in vendor mode),
// and with Go's own, as GOROOT has it. It skips a check where go list cannot
// load the modules, where vendor/ lacks a module's LICENSE, and where GOROOT
// has none.
func TestGoLicenseIsTheModulesOwn(t *testing.T) {
	notice, err := os.ReadFile("third_party/LICENSE.golang")
	if err != nil {
		t.Fatal(err)
	}
	list := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}", ".")
	list.Env = append(os.Environ(), "GOPROXY=off")
	out, err := list.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			err = fmt.Errorf("%v: %s", err, exit.Stderr)
		}
		t.Skipf("go list cannot load the modules the binary links, which the module cache may lack: %v", err)
	}
	dirs := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if path, dir, ok := strings.Cut(line, " "); ok {
			dirs[path] = dir
		}
	}
	linked := slices.Sorted(maps.Keys(dirs))
	if len(linked) == 0 {
		t.Fatalf("go list finds no module that the binary links: this test reads the wrong output %q", out)
	}
	var listed []string
	for _, m := range regexp.MustCompile(`(?m)^- (\S+)$`).FindAllStringSubmatch(string(notice), -1) {
		listed = append(listed, m[1])
	}
	slices.Sort(listed)
	if !slices.Equal(listed, linked) {
		t.Errorf("third_party/LICENSE.golang lists the modules %q; the binary links %q", listed, linked)
	}
	endsWith := func(t *testing.T, file string) {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasSuffix(notice, text) {
			t.Errorf("third_party/LICENSE.golang does not end with %s", file)
		}
	}
	for _, path := range linked {
		t.Run(path, func(t *testing.T) {
			file, skip := moduleLicense(path, dirs[path])
			if skip != "" {
				t.Skip(skip)
			}
			endsWith(t, file)
		})
	}
	t.Run("GOROOT", func(t *testing.T) {
		goroot, err := exec.Command("go", "env", "GOROOT").Output()
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(strings.TrimSpace(string(goroot)), "LICENSE")
		if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
			// Some packages of Go, such as Arch Linux's, keep it elsewhere.
			t.Skipf("this Go has no %s", file)
		}
		endsWith(t, file)
	})
}

// moduleLicense finds the LICENSE of the module path, which go list found in
// dir. In vendor mode go list gives no directory, and the LICENSE is the copy
// that go mod vendor makes under vendor/; skip says why there is none to
// check.
func moduleLicense(path, dir string) (file, skip string) {
	if dir != "" {
		return filepath.Join(dir, "LICENSE"), ""
	}
	file = filepath.Join("vendor", filepath.FromSlash(path), "LICENSE")
	if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Sprintf("go list gives no directory for %s (vendor mode), and there is no %s", path, file)
	}
	return file, ""
}

// TestModuleLicenseInVendorMode: where go list gives a module no directory, as
// in vendor mode, its LICENSE is go mod vendor's copy under vendor/; with no
// copy there, the check is skipped, saying so.
func TestModuleLicenseInVendorMode(t *testing.T) {
	t.Chdir(t.TempDir())
	if file, skip := moduleLicense("example.com/m", "/mod/example.com/m@v1.0.0"); file != "/mod/example.com/m@v1.0.0/LICENSE" || skip != "" {
		t.Errorf("with a directory from go list: %q, skip %q; want the directory's LICENSE", file, skip)
	}
	const vendored = "vendor/example.com/m/LICENSE"
	file, skip := moduleLicense("example.com/m", "")
	if file != "" || !strings.Contains(skip, "go list") || !strings.Contains(skip, vendored) {
		t.Errorf("with no directory and no vendored copy: %q, skip %q; want a skip that names go list and %s", file, skip, vendored)
	}
	if err := os.MkdirAll(filepath.Dir(vendored), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vendored, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if file, skip := moduleLicense("example.com/m", ""); file != vendored || skip != "" {
		t.Errorf("with no directory and a vendored copy: %q, skip %q; want %s", file, skip, vendored)
	}
}

// TestAppCarriesTheLicenses: the APK carries scterm's license, the scrcpy
// server's and Shizuku's as assets, which must stay copies of the repository's
// files. Shizuku's must also stay Shizuku-API's LICENSE, which its SHA-256
// shows even when both copies are edited alike.
func TestAppCarriesTheLicenses(t *testing.T) {
	for asset, source := range map[string]string{
		"android/app/src/main/assets/LICENSE":         "LICENSE",
		"android/app/src/main/assets/LICENSE.scrcpy":  "third_party/LICENSE.scrcpy",
		"android/app/src/main/assets/LICENSE.shizuku": "third_party/LICENSE.shizuku",
	} {
		copied, err := os.ReadFile(asset)
		if err != nil {
			t.Errorf("the APK's copy of %s: %v", source, err)
			continue
		}
		want, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(copied, want) {
			t.Errorf("%s differs from %s: copy %s over it", asset, source, source)
		}
	}
	shizuku, err := os.ReadFile("third_party/LICENSE.shizuku")
	if err != nil {
		t.Fatal(err)
	}
	if problem := shizukuLicenseProblem(shizuku); problem != "" {
		t.Errorf("third_party/LICENSE.shizuku is no longer Shizuku-API's LICENSE: %s", problem)
	}
}

// shizukuLicenseSHA256 is the SHA-256 of the LICENSE of Shizuku-API
// (https://github.com/RikkaApps/Shizuku-API), which third_party/LICENSE.shizuku
// copies byte for byte.
const shizukuLicenseSHA256 = "64870037f294b6f2b75c8ebe77cc0702acb3b64f86b4b0e58bf8558525303686"

// shizukuLicenseProblem says how text differs from Shizuku-API's LICENSE, or
// returns "" when it is that text.
func shizukuLicenseProblem(text []byte) string {
	if sum := fmt.Sprintf("%x", sha256.Sum256(text)); sum != shizukuLicenseSHA256 {
		return fmt.Sprintf("its SHA-256 is %s, not %s", sum, shizukuLicenseSHA256)
	}
	return ""
}

// TestShizukuLicenseProblemSeesAnEdit: Shizuku's text with a final newline
// added, as an editor might save both copies, is not Shizuku-API's LICENSE.
func TestShizukuLicenseProblemSeesAnEdit(t *testing.T) {
	text, err := os.ReadFile("third_party/LICENSE.shizuku")
	if err != nil {
		t.Fatal(err)
	}
	if problem := shizukuLicenseProblem(append(text, '\n')); problem == "" {
		t.Error("shizukuLicenseProblem finds nothing wrong with Shizuku's text and a newline after it")
	}
}

// TestReleaseAttachesTheLicenses: the release job attaches the license texts
// of the binary and the APK next to them. The release action only warns about
// a file it cannot find, so each must be where the list says.
func TestReleaseAttachesTheLicenses(t *testing.T) {
	workflow, err := os.ReadFile(".github/workflows/build.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, step, _ := strings.Cut(string(workflow), "uses: softprops/action-gh-release@")
	_, list, ok := strings.Cut(step, "\n          files: |\n")
	if !ok {
		t.Fatal("build.yml's release step has no files list")
	}
	var files []string
	for _, line := range strings.Split(list, "\n") {
		f, ok := strings.CutPrefix(line, "            ")
		if !ok {
			break
		}
		files = append(files, f)
	}
	licenses := []string{"LICENSE", "third_party/LICENSE.scrcpy", "third_party/LICENSE.golang", "third_party/LICENSE.shizuku"}
	for _, f := range append([]string{"scterm", "scterm-android-*.apk"}, licenses...) {
		if !slices.Contains(files, f) {
			t.Errorf("the release does not attach %s: its files are %q", f, files)
		}
	}
	for _, f := range licenses {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("the release attaches %s: %v", f, err)
		}
	}
}

// TestLicensesAreDocumented: scterm licenses is in the README's table of
// commands, and the README's License section says where the license texts
// are: the command, the release's files and the APK's assets.
func TestLicensesAreDocumented(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, _ := strings.Cut(string(readme), "\n### Commands\n")
	table, _, _ = strings.Cut(table, "\n#")
	if !strings.Contains(table, "\n| `scterm licenses` |") {
		t.Error("README.md does not list scterm licenses under ### Commands")
	}
	_, section, ok := strings.Cut(string(readme), "\n## License\n")
	if !ok {
		t.Fatal("README.md has no ## License section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	for _, word := range []string{"`scterm licenses`", "`LICENSE`", "`LICENSE.scrcpy`", "`LICENSE.golang`", "`LICENSE.shizuku`",
		"`assets/LICENSE`", "`assets/LICENSE.scrcpy`", "`assets/LICENSE.shizuku`"} {
		if !strings.Contains(section, word) {
			t.Errorf("README.md's License section does not name %s", word)
		}
	}
}

// TestVendoredChangesAreListed: the local modifications that
// third_party/README.md lists are the files of the vendored scrcpy server
// marked "scterm patch", no more and no fewer.
func TestVendoredChangesAreListed(t *testing.T) {
	text, err := os.ReadFile("third_party/README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, mods, ok := strings.Cut(string(text), "\n## Local modifications\n")
	if !ok {
		t.Fatal("third_party/README.md has no ## Local modifications section")
	}
	mods, _, _ = strings.Cut(mods, "\n## ")
	var listed []string
	for _, m := range regexp.MustCompile("`(scrcpy-server-src/[^`]+)`").FindAllStringSubmatch(mods, -1) {
		listed = append(listed, m[1])
	}
	var marked []string
	err = filepath.WalkDir("third_party/scrcpy-server-src", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := os.ReadFile(path)
		if bytes.Contains(src, []byte("scterm patch")) {
			marked = append(marked, strings.TrimPrefix(path, "third_party/"))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) == 0 {
		t.Fatal(`no file of third_party/scrcpy-server-src is marked "scterm patch": this test reads the wrong files`)
	}
	slices.Sort(listed)
	if strings.Join(listed, "\n") != strings.Join(marked, "\n") {
		t.Errorf("third_party/README.md lists %q as changed; the files marked \"scterm patch\" are %q", listed, marked)
	}
}
