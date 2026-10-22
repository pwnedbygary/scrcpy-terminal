package main

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// serverProvenance is where third_party/build-scrcpy-server.sh records how it
// built the embedded server.
const serverProvenance = "third_party/scrcpy-server.jar.d/scrcpy-server.provenance"

// TestEmbeddedServerIsTheBuiltOne: the scrcpy server the binary embeds is the
// jar third_party/build-scrcpy-server.sh built last, by the sha256 that its
// provenance records. Whether that jar is what the source builds to takes a
// rebuild: the script's --check, which CI runs.
func TestEmbeddedServerIsTheBuiltOne(t *testing.T) {
	p := readServerProvenance(t)
	sum := sha256.Sum256(serverJarData())
	if got := hex.EncodeToString(sum[:]); got != p["sha256"] {
		t.Errorf("the embedded server's sha256 is %s, but %s records %s: build the server with third_party/build-scrcpy-server.sh, which writes both", got, serverProvenance, p["sha256"])
	}
}

// TestServerSourceIsTheBuiltOne: third_party/scrcpy-server-src is the source
// the embedded server was built from, by the git tree hash that the
// provenance records. A change to the source needs a rebuild of the server.
func TestServerSourceIsTheBuiltOne(t *testing.T) {
	p := readServerProvenance(t)
	tree, err := gitTreeHash("third_party/scrcpy-server-src")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(tree); got != p["source tree"] {
		t.Errorf("third_party/scrcpy-server-src has the git tree hash %s, but the embedded server was built from %s: rebuild it with third_party/build-scrcpy-server.sh (files that git does not track there count too)", got, p["source tree"])
	}
}

// TestServerVersionIsTheBuiltOne: scterm passes the server the version name it
// was built with, since Options.parse() refuses to start with any other.
func TestServerVersionIsTheBuiltOne(t *testing.T) {
	p := readServerProvenance(t)
	if got := newADB("").serverVersion(); got != p["version name"] {
		t.Errorf("serverVersion() is %q, but the embedded server was built as %q and refuses any other", got, p["version name"])
	}
}

// TestServerScriptVersionIsTheRecordedOne: the provenance's version name is
// the one third_party/build-scrcpy-server.sh builds with, so that a hand edit
// of either fails here, without the Android SDK that the script's --check
// needs.
func TestServerScriptVersionIsTheRecordedOne(t *testing.T) {
	p := readServerProvenance(t)
	data, err := os.ReadFile("third_party/build-scrcpy-server.sh")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		if name, ok := strings.CutPrefix(line, "VERSION_NAME="); ok {
			names = append(names, strings.Trim(name, `"'`))
		}
	}
	if len(names) != 1 {
		t.Fatalf("third_party/build-scrcpy-server.sh sets VERSION_NAME on %d lines, not one", len(names))
	}
	if names[0] != p["version name"] {
		t.Errorf("third_party/build-scrcpy-server.sh builds the server as %q, but %s records %q: build it with the script, which writes both", names[0], serverProvenance, p["version name"])
	}
}

// TestGitTreeHash: gitTreeHash gives what git write-tree gave for these files:
// an executable one, a directory named as a file's prefix (git sorts "a.txt"
// before the directory "a"), and an empty directory, which git leaves out.
func TestGitTreeHash(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []struct {
		name, content string
		mode          os.FileMode
	}{
		{"a.txt", "hello\n", 0o644},
		{"a/b", "x\n", 0o644},
		{"run.sh", "#!/bin/sh\n", 0o755},
	} {
		path := filepath.Join(dir, f.name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.content), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, f.mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	tree, err := gitTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(tree), "126034c0097e6da9b537a3b7d156696432a020bd"; got != want {
		t.Errorf("gitTreeHash = %s, git write-tree gave %s", got, want)
	}
}

// readServerProvenance returns the "key: value" lines of the provenance,
// leaving out its comments.
func readServerProvenance(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(serverProvenance)
	if err != nil {
		t.Fatal(err)
	}
	p := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(line, ": "); ok && !strings.HasPrefix(line, "#") {
			p[key] = value
		}
	}
	for _, key := range []string{"sha256", "source tree", "version name"} {
		if p[key] == "" {
			t.Fatalf("%s has no %q line", serverProvenance, key)
		}
	}
	return p
}

// gitTreeHash returns the hash that git write-tree gives the files under dir
// after git add --all: each file by its content and by whether its owner may
// run it, each directory that holds files by its own tree, in git's order,
// where a directory's name sorts as if it ended in a slash.
func gitTreeHash(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type entry struct {
		key, mode, name string
		hash            []byte
	}
	var list []entry
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		switch {
		case info.IsDir():
			hash, err := gitTreeHash(path)
			if err != nil {
				return nil, err
			}
			if hash != nil {
				list = append(list, entry{e.Name() + "/", "40000", e.Name(), hash})
			}
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			mode := "100644"
			if info.Mode()&0o100 != 0 {
				mode = "100755"
			}
			list = append(list, entry{e.Name(), mode, e.Name(), gitObjectHash("blob", data)})
		default:
			return nil, fmt.Errorf("%s is neither a file nor a directory", path)
		}
	}
	if len(list) == 0 {
		return nil, nil
	}
	slices.SortFunc(list, func(a, b entry) int { return strings.Compare(a.key, b.key) })
	var body []byte
	for _, e := range list {
		body = fmt.Appendf(body, "%s %s\x00", e.mode, e.name)
		body = append(body, e.hash...)
	}
	return gitObjectHash("tree", body), nil
}

// gitObjectHash returns the hash that git gives an object of that kind and
// content.
func gitObjectHash(kind string, data []byte) []byte {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", kind, len(data))
	h.Write(data)
	return h.Sum(nil)
}
