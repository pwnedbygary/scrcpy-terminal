package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestLicensesCommandPrintsBothTexts: scterm licenses prints scterm's license
// and the scrcpy server's, each whole and as the repository has it, scterm's
// first, each after a line that says what it covers, and nothing after them.
func TestLicensesCommandPrintsBothTexts(t *testing.T) {
	mit, err := os.ReadFile("LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	apache, err := os.ReadFile("third_party/LICENSE.scrcpy")
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
	if after != "" {
		t.Errorf("scterm licenses prints more after the licenses: %q", after)
	}
}

// TestAppCarriesTheLicenses: the APK carries scterm's license and the scrcpy
// server's as assets, which must stay copies of the repository's files.
func TestAppCarriesTheLicenses(t *testing.T) {
	for asset, source := range map[string]string{
		"android/app/src/main/assets/LICENSE":        "LICENSE",
		"android/app/src/main/assets/LICENSE.scrcpy": "third_party/LICENSE.scrcpy",
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
}

// TestReleaseAttachesTheLicenses: the release job attaches scterm's license and
// the scrcpy server's next to the binary and the APK. The release action only
// warns about a file it cannot find, so each must be where the list says.
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
	for _, f := range []string{"scterm", "scterm-android-*.apk", "LICENSE", "third_party/LICENSE.scrcpy"} {
		if !slices.Contains(files, f) {
			t.Errorf("the release does not attach %s: its files are %q", f, files)
		}
	}
	for _, f := range []string{"LICENSE", "third_party/LICENSE.scrcpy"} {
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
	for _, word := range []string{"`scterm licenses`", "`LICENSE`", "`LICENSE.scrcpy`", "`assets/LICENSE`", "`assets/LICENSE.scrcpy`"} {
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
