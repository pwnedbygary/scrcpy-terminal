# scterm — control an Android device, fast

**Go rewrite (v3)** — single static binary, **native scrcpy wire protocol**
engine (reverse-engineered scrcpy 4.1, no scrcpy binary needed):

```
device screen ─engine (hw H.264+Opus, scrcpy wire protocol)─→ fanout
     ├─ raw Annex-B H264 → ffmpeg decode → raw YCbCr → TUI half-block renderer
     ├─ per-frame fMP4 (Go muxer, real PTS) → web MSE <video>
     └─ Ogg Opus (Go muxer) → web <audio>
```

The TUI path has **no JPEG anywhere**: ffmpeg decodes and scales to the
terminal size, and the renderer draws raw YUV pixels directly. One ffmpeg
decode feeds the terminal; the web gets browser-native H.264 decode via
MSE. Fallbacks: scrcpy binary → mkv FIFO when the engine payload is
missing, adb screenrecord when neither exists. A screencap filler keeps
static screens fresh (damage-tracking ROMs).

## Quick start (Go)

```bash
go build -o scterm .          # needs Go 1.21+, ffmpeg, adb, scrcpy-server payload

./scterm --tui                # terminal UI (Zellij-auto-aware) — engine default
./scterm --web 8000           # headless web server (MSE H264 + audio)
./scterm --both 8000          # TUI + web on one pipeline
./scterm --window             # standalone scrcpy-style window (passthrough)
./scterm --no-engine          # force the scrcpy-binary path
```

The legacy Python single-file app remains as `scterm.py` (v2) for
reference; the Go version supersedes it.

## Dependencies

**Go 1.21+** · **ffmpeg** · **adb** · **scrcpy-server payload** at
`/usr/share/scrcpy/scrcpy-server` (ships with scrcpy; the `scrcpy`
binary itself is optional — only needed for `--window` and the
`--no-engine` fallback).

## Capture sources

| Source | How | When |
|---|---|---|
| `engine` | native Go scrcpy-4.1-protocol client (default) | server payload present |
| `scrcpy` | scrcpy binary → mkv FIFO → ffmpeg | `--no-engine`, or engine fails |
| `screenrecord` | adb screenrecord → h264 pipe | neither available |

Cycle with `Ctrl-S` or the menu (`Ctrl-T` → Source). The engine is a
reverse-engineering of the scrcpy 4.1 server protocol (see
`engine/PROTOCOL.md`): the same server payload, the same wire framing —
no scrcpy process, no mkv remux, no SDL.

## Flags

```
-s, --serial SERIAL      device serial (default: first adb device)
    --tui                terminal UI (default if stdin is a tty)
    --web[=PORT]         web viewer only (default port 8000)
    --both[=PORT]        TUI + web
    --window             standalone scrcpy-style window (passthrough)
    --engine             force the native engine (default when the
                         server payload is present)
    --no-engine          force the scrcpy-binary path (or screenrecord)
    --fps N              TUI refresh cap (default 30)
    --fit MODE           contain | cover | fill (default contain)
    --max-size WxH       stream cap, aspect preserved (default 1280)
    --bit-rate N         screenrecord bit rate, bps (default 8 Mbps)
    -q N                 web MJPEG quality 1–10 (default 4)
    --no-wake            don't wake/unlock the device on launch
    --no-stay-awake      don't keep the device screen on while running
    --version
Env: SCRCPY_ARGS — extra args for the scrcpy engine (--window).
     SCRCPY_SERVER — alternate path to the scrcpy-server payload.
```

## Terminal UI

```
┌─ Retroid Pocket 6  49016109  1920×1080  engine  33.8 fps  0.9 ms  fit contain  bat 80%
│  ▀▀▀▀▀▀▀▀ device screen renders here ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
│  ┌─ scterm 3.0.0 ───────────────────────────┐
│  │ ▶ Fit mode                        contain │   ← Ctrl-T menu
│  │    TUI fps                              30│
│  │    Source                      engine    │   ← cycle engine/scrcpy/screenrecord
│  │    ...                                   │
└─ Click=tap  Drag=touch  Wheel=scroll  Type=text  ?=help  ^T=menu  Esc×2=quit ▁▂▃▅▆▇█
```

| Input | Action |
|---|---|
| Click / drag / wheel | tap / live touch drag / scroll |
| Typing + Enter | text input (`INJECT_TEXT` in engine mode — real IME text) |
| F1–F12, arrows | menu, home, back, recents, power, volume, dpad |
| `?` | help overlay (press again to close) |
| `Ctrl-T` | menu overlay (arrows + Enter, Esc/^T/Tab closes) |
| `Ctrl-F` | cycle fit mode |
| `Ctrl-S` | cycle capture source |
| `Ctrl-Alt-G` / `F12` | grab mode — keys go straight to the device (also locks Zellij) |
| `Esc` ×2 | quit |

Rendering is raw-YCbCr: ffmpeg decodes the H.264 stream, scales to the
terminal size, and the renderer draws half-blocks directly (~1ms/frame).
Incremental redraw (only changed rows) keeps slow links fluid.

## Web viewer

MSE H.264 by default (engine path): the browser decodes the device's
hardware H.264 natively from per-frame fragmented MP4 with real PTS —
sub-frame glass-to-glass, no server-side re-encode. MJPEG fallback when
no avc1 codec is reported. Features:

- tap / drag-to-swipe with a cursor dot, pinch-safe pointer handling
- pointer-lock keyboard capture (`Ctrl-Alt-G`) with Android F-key mapping
- on-screen keyboard + text bar on touch devices
- HUD chips: live/fps/resolution/battery · glass UI, dark theme
- audio toggle (Ogg Opus, live)

## JSON API (the future native-app surface)

| Endpoint | Purpose |
|---|---|
| `GET /api/status` | serial, model, size, battery, source, codec, fps |
| `POST /input/tap` | `{"x": 0.5, "y": 0.5}` — **normalised** 0–1 coords |
| `POST /input/swipe` | `{"x1","y1","x2","y2"}` normalised |
| `POST /input/touch` | `{"act":"down|move|up","x","y"}` — live drag |
| `POST /input/key` | `{"code": 82}` or `?code=82` |
| `POST /input/text` | `{"text": "hello world"}` (INJECT_TEXT in engine mode) |
| `POST /input/scroll` | `{"x","y","dx","dy"}` |
| `POST /input/gamepad` | viewer gamepad state → UHID gamepad on device |
| `POST /input/audio` | `{"enabled": true}` |
| `GET /stream.fmp4` | live fragmented MP4 (MSE) |
| `GET /stream.mjpg` | multipart MJPEG fallback |
| `GET /audio.ogg` | live Ogg Opus stream |
| `POST /settings/fps` · `/quality` · `/scale` · `/rotate` · `/wake` | live settings |

## Performance notes

- **Engine**: one `adb forward` tunnel carries video + audio + control
  sockets; input is a single persistent control socket (TCP_NODELAY) —
  no per-event adb process spawns.
- **Startup drain**: the role-verify animation backlog is discarded
  (framing-safe, full packets) and the SPS/PPS config packet replayed, so
  viewers start at current content instead of ~2s of stale frames.
- **TUI**: ffmpeg `-f h264 -use_wallclock_as_timestamps 1` (arrival-time
  PTS survive post-stall bursts — the fake-PTS demux dropped them) →
  raw yuv420p → direct renderer. No JPEG encode/decode in the path.
- **Fanout**: config + keyframes are never dropped; only non-key frames
  may drop when the terminal pipe is slow (recover on the next keyframe).
- **Static screens**: this ROM's encoder is damage-tracking — a static
  screen stalls ANY stream (scrcpy --window freezes too). A screencap
  filler refreshes the viewers at ~5fps.
- **Fallbacks**: engine → scrcpy binary → screenrecord, automatic; the
  watchdog respawns only when a process actually dies.

## Scenarios

- **Zellij**: `scterm --tui` — F12/Ctrl-G locks the pane and passes keys
  through; detach/reattach keeps content.
- **Termux / CLI**: the engine needs no scrcpy binary and no windowing —
  just adb + the server payload.
- **USB**: `scterm.py` — instant, zero-config.
- **Tailscale / anywhere**: `scterm --web 8000` on the host, open
  `http://<tailscale-ip>:8000` from any device.
