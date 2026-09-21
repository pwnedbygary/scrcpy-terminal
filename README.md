# scterm — scrcpy in your terminal (or a window)

`scrcpy` inside a terminal, built from the ground up: our own client that speaks
the scrcpy wire protocol directly to the device. No `scrcpy` binary, no ffmpeg
CLI, no wrapper pipelines. One Go binary plus the (Apache-2.0) scrcpy server
that runs on the device.

Three display modes, one pipeline:

```sh
./scterm                  # terminal UI (half-blocks, hybrid redraw)
./scterm --web            # browser display on http://127.0.0.1:6969/
./scterm --web --web-port 9000
./scterm --window         # same, popped out into its own window
```

```
              ┌──────────────────────────────────────────────┐
              │             scterm (this repo)                │
              │  Go client: protocol demux, control, audio,  │
              │  TUI renderer (half-blocks, hybrid redraw)   │
              │  cgo: libavcodec/libswscale/decode+scale     │
              │       libpulse-simple (audio out)            │
              │  SIMD: AVX2 (x86_64) / NEON (aarch64)        │
              └──────────┬─────────────────────┬─────────────┘
                 adb reverse tcp (video/audio/control sockets)
                          │
              ┌───────────▼───────────────────▼──────────────┐
              │  scrcpy-server (vendored, Apache-2.0)        │
              │  MediaCodec capture → h264 + opus → sockets  │
              │  ControlChannel → injectInputEvent           │
              └──────────────────────────────────────────────┘
                         Android device
```

## Build

Requires: Go (with cgo), ffmpeg dev libraries, libpulse-simple.

```sh
go build -o scterm .
```

Then run with a device connected (or `-s <serial>`):

```sh
./scterm
```

## CI / releases

Every push to `main` and every pull request is built and tested on GitHub
Actions (`.github/workflows/build.yml`): `go vet`, `go test`, and a stripped
binary uploaded as an artifact. Pushing a `v*` tag also publishes a GitHub
release with the binary attached — the version is stamped into `main.version`
and shown in the status line.

## Control

Everything works when grabbed (mouse+keys to the device). F12 or Ctrl-G toggles
grab; in Zellij the session starts "ungrabbed" so the pane keeps its own mouse,
and you press F12/Ctrl-G to hand the mouse over.

### Alt+letter: the no-F-key controls

Phone keyboards (Unexpected Keyboard and friends) have Ctrl and Alt but no
F-key row, so **Alt+`<letter>` is the primary binding** for every device
action — in the terminal, a browser tab and `--window` alike. The letters are
mnemonics, and the same table drives all three front ends. The punctuation
chords (`Alt+/`, `Alt+-`, `Alt+=`) work there too: a phone reports them by
character rather than by key code, and the player normalises that before the
lookup.

| Chord | Action |
|-------|--------|
| Alt+H | Home |
| Alt+B (or Esc) | Back |
| Alt+T | Recents (tasks) |
| Alt+P | Power |
| Alt+U / Alt+D | Device volume up / down |
| Alt+C | Collapse panels |
| Alt+N | Notifications |
| Alt+E | Settings shade |
| Alt+R | Rotate device |
| Alt+G | Toggle grab |
| Alt+I | Show/hide the software keyboard (browser: raise the phone keyboard) |
| Alt+S | Screenshot (local: PPM in the TUI, PNG in the browser) |
| Alt+K | Request a keyframe |
| Alt+Q | Quit |
| Alt+/ | Action menu (TUI) / controls sheet (web) |
| Alt+M | Mute the LOCAL audio stream (never the device) |
| Alt+- / Alt+= | Local playback volume |
| ↑/↓/←/→ | D-pad |

The device's own Menu key and Mute have no chord: they live on F2/F7 and on the
TUI action menu (Alt+/) and the web action bar.

The classic keys still work, unchanged — nothing was taken away:

| Key | Action |
|-----|--------|
| Esc | Back |
| F1 | Home |
| F2 | Menu |
| F3 | Recents (app switch) |
| F4 | Power |
| F5 / F6 | Device volume down / up |
| F7 | Device mute |
| F8 | Rotate device |
| F9 / F10 | Notification / settings shade |
| F11 | Collapse panels |
| F12 / Ctrl-G | Toggle grab (Zellij mouse path) |
| Ctrl-K (or Ctrl-O) | Show/hide the on-screen keyboard |
| Mouse left | Tap (down+up) |
| Mouse left drag | Touch move (coalesced ~8ms) |
| Wheel | Scroll |
| Mouse right | Back |
| Mouse middle | Home |

### Action bar and action menu

The web page has a floating action bar: one button per action, arriving with
mouse activity and fading a couple of seconds after the last movement. The
button tooltips name the chords, so the buttons and the keyboard cannot
disagree.

The terminal gets the same thing drawn with the software keyboard's button
spans: a strip of two-row pills, centered in the pane just above the status
line, sized for pointing (inner padding and a gap between buttons). Any mouse
activity brings it up -- hover where the terminal forwards motion (mouse
tracking is set to SGR, with any-event motion enabled), otherwise a click or
wheel -- and it fades a couple of seconds after the last movement, except
while the pointer is resting on it: hiding a button from under the cursor
would turn the next click into a device tap. Buttons run through the same
dispatch as the chords and the menu, a click flashes the button green, and
when the strip is wider than the pane the `‹`/`›` edges scroll it. Clicks on
the bar never reach the device as taps, and the overlay repaints immediately
instead of waiting for the next video frame (a static device screen may send
none at all).

`Alt+/` opens the keyboard-driven action menu: every action with its chord,
letters run a row, `Esc` closes, and a mouse click on a row runs it too. The
web page gets the same list as a clickable controls sheet behind the `☰`
button.

### Pointer indication (web/`--window`)

The browser hides the native cursor over the video and paints its own: a ring
that follows a mouse or pen while hovering, fills when pressed and draws a
short trail while dragging. A finger has no hover to show, so touch gets a
ripple that exists only while the finger is down. This indication is local — it
shows where you are about to click; it does not move the device's own cursor.

In `--web`/`--window` the **same table applies to the same keys**: `F1`..`F11`
are home/menu/recents/power/vol-/vol+/mute/rotate/notif/settings/collapse and
`F12` (or `Alt+G`) toggles grab, exactly as in the terminal — the browser used
to send Android's own F1..F12 keycodes for those instead, so one key meant two
different things depending on which display you were looking at. `Alt+F1..F12`
remain as an alias for keyboards or window managers that eat a bare F-key.
`Esc` alone is back, and the `Alt+M` / `Alt+-` / `Alt+=` local playback controls
act on the browser's own output (the host sink is silent in these modes).

Grab is a real mode in the browser too: with it off, the accelerators that
belong to the browser itself (`Ctrl+W` close, `Ctrl+T`/`Ctrl+N` new tab/window,
`Ctrl+R` reload, `Ctrl+Q`) stay with the browser — `Ctrl+W` would otherwise end
a `--window` session. With grab on they go to the device, which is what F12
does for Zellij in the terminal. Bare `F5`/`F11` are volume-down/collapse, not
reload/fullscreen: the page consumes them.

Letters/digits type as Android key events (games work); uppercase adds shift
meta; Ctrl+A..Z send keycodes with the ctrl meta. Other printable characters
go through text injection. Unmapped Alt+letter chords are left to the front
end: the browser types the character, while the terminal has no use for it and
drops it (`Alt+Z`). The `Alt+F1..F12` aliases still exist for keyboards where a
bare F-key never arrives, but the mnemonics above are the intended path.

### On-screen keyboard

`Alt+I` (or `Ctrl-K`/`Ctrl-O`) opens a full software keyboard over the video
(bottom-anchored): QWERTY, digits, symbols, D-pad, Android nav (Home/Back/
Menu/Recents/Power/Vol±/Mute), F1–F12, and system actions (Rotate/Notif/Shade/
Collapse/Grab/Hide/Quit). Mouse-click a key to press it; arrows + Enter
navigate; sticky Shift/Ctrl/Alt behave like a real keyboard (one-shot after a
text key); Esc or F12 hides it. Pressed keys flash green. Clicks above the
keyboard pass through to the device as taps.

### Status bar

The bottom line shows device, resolution, grab state, volume, a live
frametime sparkline (`▁▂▃▄▅▆▇`) + average ms, and a one-line hint:
`Alt+/ actions · Alt+I keys · Alt+Q quit`. The full list is in the action menu
(and above) — the graph makes render spikes visible while you play.

## Flags

```
-s SERIAL        device serial (default: ANDROID_SERIAL or the only device)
-max-size N      max video width (default 1280)
-video-bit-rate  default 8000000
-audio-bit-rate  default 128000
-max-fps F       cap the device frame rate (0 = device default)
-audio=false     disable audio
-video=false     disable video
-control=false   disable control (mirror only)
-no-tui          headless stats mode (mutually exclusive with --web)
-dump-frames DIR write the first frames as PPM (verification)
-keys            print all supported Android keys
-web             serve the mirror to a browser (see Web mode above)
-window          --web plus a standalone window (see Window mode above)
-web-port N      port for --web/--window (default 6969, 0 = any)
-web-addr A      bind address (default 127.0.0.1)
-web-quality Q   mjpeg qscale for the web display (default 5)
-window-size WxH initial window size for --window
```

## Why our own client

The old project wrapped `scrcpy`/`ffmpeg` with subprocesses and file pipes. This
one:

- decodes h264 with libavcodec in-process (same library scrcpy uses) — the
  packet loop is ours;
- scales straight to the terminal canvas with libswscale (SIMD asm inside);
- packs 2-pixel half-block cells with hand-written AVX2/NEON SIMD and
  quantizes RGB to 3-bit channels so adjacent equal cells merge into runs
  (`▀▀▀▀` instead of per-cell SGR) — that's what keeps a terminal fast;
- renders with a hybrid redraw: changed rows only, full repaint every 24
  frames (recovers from any terminal/Zellij drifts);
- sends input over scrcpy's control socket (TCP_NODELAY), the same path scrcpy
  uses — one round trip, no `adb shell input` subprocess;
- decodes opus/aac/flac to 48kHz stereo PCM and plays through PulseAudio with
  a software gain (mute/volume without touching the device).

## Web mode (`--web`)

`--web` serves the same decoded canvas to a browser instead of to terminal
cells. Same adb tunnel, same server on the device, same decoder, same control
socket — only the last hop changes:

```
device → h264 --libavcodec--> BGR0 canvas --mjpeg--> WebSocket --> canvas
                                   │
                          (one mailbox per client, drop-old)
```

What that buys, and why it is built this way:

- **One decode, not two.** The host decodes h264 once; browsers receive JPEG
  stills (hardware-decoded, ~2 ms). No MSE, no fragmented-MP4 muxing, no
  keyframe hunting, no browser-side buffering.
- **One shared canvas.** The canvas is the video's own size, rendered once per
  frame and shared by every client; each browser scales it to its viewport with
  CSS. A client must never choose its own canvas: the frame buffer is allocated
  once per frame, so a client-sized canvas makes the encoder read past the end
  of that buffer (that was a real SIGSEGV — see the regression tests).
- **Drop-old, never backlog.** Each client owns a one-slot mailbox: a browser
  that stalls loses frames instead of pushing back on the decoder, so input
  latency stays flat no matter how slow the page is.
- **Zero-copy frame path.** The JPEG is encoded directly into the buffer that
  goes to the socket (header + payload in one write), and canvases come from a
  small pool. One pooled encoder per canvas geometry, serialised by a mutex,
  because an AVCodecContext is not thread-safe.
- **Audio is raw PCM** (s16le stereo 48 kHz) into an `AudioWorklet` ring buffer:
  ~20 ms of latency, no codec, and no autoplay gesture needed in `--window`.
  The browser asks for it in the `hello` message (`caps:"audio"`), sent when the
  socket opens — and again after every reconnect, because the server builds a
  fresh client (audio off) per connection.
- **Audio plays in the browser only.** The stream is the sole source of sound:
  the host sink stays silent in web/window mode so nothing is ever played twice.
  `--audio-dup` duplicates it onto the host device as well.
- **Input is ordered, not queued behind video.** Pointer/keyboard events are
  parsed in the socket reader and applied by one dispatcher goroutine; drag
  moves coalesce at 8 ms exactly like the TUI.

Controls mirror the TUI (the Alt+letter mnemonics and the F-keys are the same
tables — see Control): every action is on the floating action bar, and `Alt+/`
opens a clickable controls sheet so a phone can drive everything without
chords. Right click = back, wheel = scroll, middle click = home,
paste = text input. Everything else is a normal keyboard: letters/digits go as
key events, punctuation as injected text. The page also paints a local pointer
indication (hover ring, drag trail, touch ripples) so you can see where a click
will land even though the native cursor is hidden over the video.

```
-web                 serve the mirror over HTTP (canvas display)
-web-port N          TCP port (default 6969; 0 = pick a free port)
-web-addr A          interface to bind (default 127.0.0.1; 0.0.0.0 exposes it
                     to your network)
-web-quality Q       mjpeg qscale 2 (best, big) .. 31 (small), default 5
-audio-dup           duplicate the audio: keep it on the device while capturing,
                     and with --web/--window play it on the host as well as in
                     the browser (default: the stream is the only output)
```

The terminal stays a normal terminal in web mode: no alternate screen, no raw
mode, no mouse grab — just log lines, so `scterm --web` can sit in a tmux pane
while you use the browser.

The browser client has its own suites, and `go test ./...` runs them (skipping
if `node` is absent), because both of the faults that made the page silent were
in it and invisible to any Go test:

```sh
node web/input_test.mjs     # pointer/keyboard mapping, terminal key parity
node web/worklet_test.mjs   # the PCM sink: buffering, burst sizes, stats
```

`input_test.mjs` lifts the real `Input` (and `Net`) out of `player.js` and drives
them, so it cannot drift from the shipped code; `worklet_test.mjs` runs the real
`pcm-worklet.js` with the AudioWorklet globals stubbed.

Verify a running instance from the shell (no browser needed):

```sh
node web/check.mjs 127.0.0.1:6969 5        # frames, JPEG, PCM, geometry
node web/check.mjs 127.0.0.1:6969 5 raw    # + BGR0 pixel content (length, bars)
SCT_DEBUG_WEB=1 ./scterm --web             # paint/decode/encode counters
```

`raw` mode is worth knowing about: decodable JPEGs prove nothing about content,
because a valid JPEG is produced even when the host encodes the wrong bytes. Raw
mode pins the payload to exactly `w*h*4` and inspects the pixels.

Drive a touch gesture without a browser:

```sh
node web/gesture.mjs 1478                     # lock-pattern dots 1..9 (keypad order)
node web/gesture.mjs --swipe 960,700,960,250  # explicit device-pixel swipe
node web/gesture.mjs --host 100.64.0.1 1478   # mirror bound to another interface
node web/gesture.mjs --dry-run 1478           # print the path, inject nothing
```

Pattern geometry and display size are read from the device with `adb` unless
`--grid`/`--device` are given, so it survives rotation. `--host` defaults to
`127.0.0.1`; pass the address the mirror is actually bound to. `--back` sends a
bare Back op, which is also how to dismiss the bouncer again.

This exists because Android marks the lock-screen bouncer `FLAG_SECURE`, so the
pattern grid is black in the browser exactly when it is on screen (`screencap`
fails the same way: exit 1, zero bytes). The grid is still readable from the
accessibility tree, so the gesture is computed numerically instead.

Pacing is not cosmetic. A dense slow drag is delivered and then discarded by
gesture recognition, with no error raised anywhere; measured on a Retroid
Pocket 6 (Android 13), raising the bouncer with an upward swipe succeeded 1/5
times at `--step 12 --delay 12`, 0/5 at `--step 12 --delay 40`, and 6/6 at
`--step 45 --delay 8`. The defaults are the measured-good values. `--dwell`
repeats the touch at each intermediate dot because `InputDispatcher` coalesces
motion events and keeps only the last of a batch, but it deliberately does *not*
dwell on the final waypoint: a stationary hold before the lift removes the
velocity a fling needs and turns a working swipe into a dead one.

## Window mode (`--window`)

`--window` is `--web` plus "put it in its own top-level window, like scrcpy
does". The trick is that no toolkit is involved: the display is already a local
HTTP+WebSocket endpoint, so the window is a browser in app mode pointed at it —
one process, hardware video scaling, low-latency WebAudio, and the whole video
pipeline still ours.

- Chromium family (`chromium`, `google-chrome`, `brave` and its channels such as
  `brave-origin-nightly`, `edge`, `vivaldi`, `ungoogled-chromium`, or
  `$SCTERM_CHROME`/`$CHROME_BIN`): launched with
  `--app=<url> --window-size=WxH` — a standalone window with no tabs, no URL
  bar, sized to the device's aspect ratio. Discovery also checks `/opt/brave.com/*`
  and similar install locations, because Brave's binary is often not on `PATH`
  under a name a naive probe would find.
- Firefox (no Chromium found): a throwaway profile
  (`--new-instance --profile …`) opens a normal window; `SCTERM_FIREFOX_KIOSK=1`
  makes it `--kiosk` (chromeless, full screen) instead. Install Chromium for
  the proper chromeless window.
- `-window-size WxH` overrides the initial size (default: the device's rotated
  display size, capped to fit a laptop screen).

Three things worth knowing about the window:

- The browser is launched with a **private profile** (`--user-data-dir`), so
  `--app` cannot be forwarded to your already-running browser. That forwarding
  is what used to break the window: Chromium's single-instance model handed the
  URL to your browser session, which ignored `--window-size` and opened the
  mirror at its own remembered geometry — a tall portrait window, say — with the
  picture stretched to fill it. The profile lives in
  `~/.cache/scterm/chromium` and is reused, because a brand-new profile has to
  build its caches before it can paint (about five seconds of black window on
  the first run, two and a half on the next).
- The display is **recovered from the compositor socket** when the environment
  does not describe one. A shell inside Zellij (or anything started from a tty
  session) can inherit no `DISPLAY`, no `WAYLAND_DISPLAY`, and a stale
  `XDG_SESSION_TYPE=tty`; Chromium then picks its X11 backend, finds no X server,
  prints `Missing X server or $DISPLAY`, and is gone in about a second — before a
  window can exist. Since that pane still belongs to your graphical session,
  `--window` finds the Wayland (or X) socket itself and says so:

      scterm: window: no DISPLAY/WAYLAND_DISPLAY in this session; using the Wayland compositor wayland-0

  A browser that still dies without showing anything is **reported, not
  swallowed**: its own output is captured and, if it exits within 15 s having
  never been connected to, the reason is printed along with the URL that is
  still serving. (Brave's launcher script masks the exit status with `|| true`,
  and its output used to go to `/dev/null` — which is how a segfaulting browser
  managed to look like "the window just does not open".)
- Audio starts on its own in `--window` (`--autoplay-policy=no-user-gesture-required`).
  In `--web` the first tap on the page is the gesture a browser may require
  before it will play.

The window *is* the session: closing it (its last client disconnecting) ends
`scterm` and tears the tunnel down, like closing scrcpy does. Quitting `scterm`
closes the window with it (the whole browser process group, launcher script
included), so no orphaned browser is left holding the private profile — a
leftover one would silently swallow the next `--window` launch.

## Server

The server jar is vendored from [scrcpy](https://github.com/Genymobile/scrcpy)
(v4.1, Apache-2.0) — `third_party/scrcpy-server.jar.d/scrcpy-server`, pushed to
`/data/local/tmp/scrcpy-server.jar` on start. The Java server source is included
under `third_party/scrcpy-server-src/` and the license in
`third_party/LICENSE.scrcpy`. The client pins version `4.1`, which the server
enforces.

## Performance notes

Measured on this project's reference host (Ryzen 7 7800X3D), noise-heavy
content, which compresses far worse than a real screen and so is a pessimistic
bound (`go test -run XXX -bench Encode -benchtime 200x`):

| Canvas | qscale | Encode | Frame | Ceiling |
|---|---|---|---|---|
| 1280x720 (web/window default) | 5 | 1.85 ms | 27 KB | ~540 fps |
| 1777x1000 (a large window) | 5 | 3.72 ms | 67 KB | ~270 fps |
| 480x1080 | 5 | 0.98 ms | 14 KB | ~1000 fps |
| 1777x1000 | 2 (best) | 5.78 ms | 167 KB | ~170 fps |

The browser side adds a measured ~2 ms of JPEG decode per frame. The whole
display path therefore costs a few milliseconds and a few MB/s at 60 fps, well
inside one core, and the device's own capture pipeline is the limiting factor in
practice: this phone's capture is damage-tracking, so a static screen produces
only ~10 fps no matter what the host can do. Live counters (`jpegMs` in the
status message, or the `SCT_DEBUG_WEB=1` per-client line) report the real
encode/decode cost on real content.

Clients share one canvas, so N clients cost N encodes of the same pixels
(serialised on one pooled encoder). With several windows open that is the next
thing to optimise: encode once per frame and share the payload.

At 480x1080 the mjpeg encode costs ~1 ms/frame single-threaded and lands around
14 KB/frame. `-max-size 0` streams the device's native resolution instead and
costs proportionally more; the default 1280 keeps the canvas at the video's own
size, which is what the display actually shows.

## Protocol notes (from scrcpy v4.1 source)

- Sockets, in order: video, audio, control (any may be absent).
- The first socket carries a 64-byte device name.
- Video/audio streams: 4-byte codec id, then 12-byte packet headers
  (session flag in MSB, config/keyframe flags in the PTS word), then payload.
- h264 config packets (SPS/PPS) must be prepended to the next media packet.
- Touch positions carry the *current video frame size* as screen_size; events
  with mismatched sizes are dropped by the server.
- Control message layouts mirror `sc_control_msg_serialize()` exactly.
