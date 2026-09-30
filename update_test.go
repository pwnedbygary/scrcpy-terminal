package main

import (
	"os"
	"path/filepath"
	"strings"
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
