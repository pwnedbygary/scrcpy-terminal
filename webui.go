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

import _ "embed"

//go:embed web/index.html
var webIndexHTML string

//go:embed web/player.js
var webPlayerJS string

//go:embed web/pointer.js
var webPointerJS string

//go:embed web/player.css
var webPlayerCSS string

//go:embed web/pcm-worklet.js
var webPCMWorkletJS string
