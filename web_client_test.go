package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// The browser client is as much a part of this program as the Go code, and it
// has already shipped two faults that no Go test could see: a worklet that
// threw on every audio burst (so the page was silent while claiming to be
// connected) and a hello that was sent before the socket opened (so the server
// never turned audio on for it). web/*.mjs drives the real player.js and the
// real pcm-worklet.js with the DOM and AudioWorklet stubbed, so running them
// from `go test ./...` is what puts them in front of CI.
func TestWebClientSuites(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed: skipping the browser-client suites")
	}
	for _, suite := range []string{
		"web/input_test.mjs",   // pointer/keyboard mapping, terminal key parity
		"web/worklet_test.mjs", // the PCM sink itself: buffering, sizes, stats
		"web/pointer_test.mjs", // the visible pointer: ghost, drag, touch ripples
		"web/conn_test.mjs",    // the status card, the reconnect policy, the HUD readouts
		"web/page_test.mjs",    // the whole page: connection wiring, action bar, controls sheet
		"web/audio_test.mjs",   // starting the sound, the compatibility path, the sound chip
	} {
		suite := suite
		t.Run(filepath.Base(suite), func(t *testing.T) {
			cmd := exec.Command(node, suite)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", suite, err, out)
			}
			t.Logf("%s\n%s", suite, out)
		})
	}
}

// nodePageFixture evaluates a JavaScript expression in node and decodes its
// JSON value into v. The expression sees web/page_fixture.mjs as F, the action
// catalog this binary embeds as catalog (parsed) and catalogText, and
// readFileSync. Without node the test is skipped, as the suites are.
func nodePageFixture(t *testing.T, expr string, v any) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed: skipping a browser-client check")
	}
	script := `import * as F from "./web/page_fixture.mjs";
import { readFileSync } from "node:fs";
const catalogText = readFileSync(0, "utf8");
const catalog = JSON.parse(catalogText);
process.stdout.write(JSON.stringify(` + expr + `));`
	cmd := exec.Command(node, "--input-type=module", "-e", script)
	cmd.Stdin = bytes.NewReader(actionsCatalogJSON)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node, %s: %v\n%s", expr, err, stderr.String())
	}
	if err := json.Unmarshal(out, v); err != nil {
		t.Fatalf("node, %s: %v\n%s", expr, err, out)
	}
}

// TestNodeSuitesLoadTheServedPage: the page the node suites load is, byte for
// byte, the page the server sends, catalog included, so what they check is
// what a browser gets. The shipped catalog has nothing to escape, so a catalog
// holding markup checks that the two escape it the same way.
func TestNodeSuitesLoadTheServedPage(t *testing.T) {
	var page string
	nodePageFixture(t, "F.servedIndex()", &page)
	if page != webIndexHTML {
		t.Errorf("web/page_fixture.mjs servedIndex() differs from the page the server sends:\nnode:   %.300q\nserver: %.300q", page, webIndexHTML)
	}

	const tmpl = "<p><!--scterm:actions--></p>"
	hostile := "{\"label\": \"</script><!-- a & b \u2028 \u2029\"}"
	literal, err := json.Marshal(hostile)
	if err != nil {
		t.Fatal(err)
	}
	nodePageFixture(t, `F.withCatalog("`+tmpl+`", `+string(literal)+`)`, &page)
	if want := withActionCatalog(tmpl, []byte(hostile)); page != want {
		t.Errorf("a catalog holding markup:\nnode:   %q\nserver: %q", page, want)
	}
}
