package main

// The browser half of --web / --window, embedded in the binary so the binary
// stays self-contained (no assets to install, no version skew between the
// player and the protocol).
//
// Rendering path, chosen for latency rather than elegance:
//   WebSocket binary frame -> Blob -> createImageBitmap (JPEG decode off the
//   main thread) -> drawImage onto a canvas sized to the device pixels.
//
// At most one decode is in flight: a newer frame that arrives while the
// previous one is still decoding replaces it, so the picture is never a queue
// of stale frames behind the live one. Audio is raw PCM into an AudioWorklet
// ring buffer -- ~20 ms of latency, no codec, no jitter buffer, and no
// autoplay gesture needed in --window mode.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed web/index.html
var webIndexTemplate string

// webIndexHTML is the page as served: index.html with the action catalog
// (actions.go) in place of its placeholder. player.js builds the action bar,
// the controls sheet and its key tables from it.
var webIndexHTML = withActionCatalog(webIndexTemplate, actionsCatalogJSON)

// webActionsPlaceholder is the line of index.html that the catalog replaces.
// The node suites put the catalog fixture in its place the same way
// (web/page_fixture.mjs).
const webActionsPlaceholder = "<!--scterm:actions-->"

// withActionCatalog puts the catalog into the page as
// <script type="application/json" id="actions">, a data block the browser
// never runs, so the page's CSP needs nothing for it. json.HTMLEscape writes
// every '<', '>' and '&' as a \u escape, so no string in the catalog can end
// the element early. A page without exactly one placeholder is a build-time
// data error, like a malformed catalog.
func withActionCatalog(page string, catalog []byte) string {
	if n := strings.Count(page, webActionsPlaceholder); n != 1 {
		panic(fmt.Sprintf("web/index.html holds the action catalog placeholder %s %d times, want once", webActionsPlaceholder, n))
	}
	var block bytes.Buffer
	block.WriteString(`<script type="application/json" id="actions">`)
	json.HTMLEscape(&block, catalog)
	block.WriteString("</script>")
	return strings.Replace(page, webActionsPlaceholder, block.String(), 1)
}

//go:embed web/player.js
var webPlayerJS string

//go:embed web/pointer.js
var webPointerJS string

//go:embed web/player.css
var webPlayerCSS string

//go:embed web/pcm-worklet.js
var webPCMWorkletJS string
