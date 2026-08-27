# scterm — control an Android device, fast

Two front-ends share **one** high-performance capture pipeline:

```
device screen ─adb screenrecord→ H264 ─ffmpeg→ MJPEG frames
     ├── TUI   numpy-vectorised half-block renderer · incremental redraw
     └── WEB   single persistent MJPEG stream · zero re-encode · JSON API
```

That means the browser receives the **exact JPEG bytes ffmpeg produces** (no
second encode), and the terminal decodes with PIL's draft-scaling — the whole
frame pipeline costs ~3 ms at a 100×38 terminal.

## Quick start

```bash
# terminal UI (default when launched on a TTY)
python3 scterm.py

# headless web server — view + control from any browser (phone over
# Tailscale, desktop over LAN, or your future native app)
python3 scterm.py --web 8000

# both at the same time
python3 scterm.py --both 8000
# or: python3 scterm.py --tui --web 8000
```

Dependencies: **Python 3.10+**, **Pillow**, **adb** with an authorized
device. Optional but strongly recommended: **ffmpeg** (enables the 30–60 fps
H264 stream; without it a raw `screencap` fallback runs at ~1–3 fps) and
**numpy** (vectorises the renderer; pure-Python fallback included). **scrcpy**
is optional and only needed for the web audio toggle.

## Running modes — one, the other, or both

| Flag | What it runs |
|------|--------------|
| `--tui` | terminal UI only |
| `--web [PORT]` | web viewer only (headless, default port 8000) |
| `--both [PORT]` | terminal UI **and** web viewer |

With no flags: TUI when stdin is a TTY, otherwise a clear error. `--both`
with no TTY silently drops to web-only.

## Flags

```
-s, --serial SERIAL      device serial (default: first adb device)
    --pick               interactive device picker (USB + WiFi scan + IP)
    --fps N              TUI refresh cap (default 20; 60 ≈ screen feed rate)
    --web-fps N          web stream frame cap (default 30)
    --fit MODE           contain | cover | fill (default contain)
    --colors MODE        auto | truecolor | 256
    --stream / --no-stream    H264 stream (default if ffmpeg present) vs raw
    --max-size WxH       stream cap, aspect preserved (default 1280x1280)
    --bitrate N          screenrecord bit rate, bps (default 6 Mbps)
    --jpeg-quality Q     MJPEG quality 1–10 (default 4)
    --web-scale PCT      stream downscale % (default 100)
    --bind ADDR          web bind address (default 0.0.0.0)
    --zellij             Zellij/phone-friendly mode (no alt-screen, no mouse)
    --chrome-bars        TUI status bars on/off (default on in TTY)
    --no-wake            don't wake/unlock the device on launch
    --no-stay-awake      don't keep the device screen on while running
    --debug              diagnostics to /tmp/scterm_debug.log
```

## Terminal UI

```
┌─ Retroid Pocket 6  49016109  1080×1920  stream  33.8 fps  0.9 ms  fit contain  bat 80%
│  ▀▀▀▀▀▀▀▀ device screen renders here ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
│  ┌─ scterm 2.0.0 ───────────────────────────┐
│  │ ▶ Fit mode                        contain │   ← Ctrl-T menu
│  │    Capture                          stream│
│  │    TUI fps                              20│
│  │    Bitrate                          6 Mb/s│
│  │    ...                                   │
└─ Click=tap  Drag=touch  Wheel=scroll  Type=text  ?=help  ^T=menu  Esc×2=quit ▁▂▃▅▆▇█
```

| Input | Action |
|---|---|
| Click / drag / wheel | tap / live touch drag / scroll |
| Typing + Enter | text input (`input text`) |
| F1–F12, arrows | menu, home, back, recents, power, volume, dpad |
| `?` | help overlay (press again to close) |
| `Ctrl-T` | menu overlay (arrows + Enter, Esc/^T/Tab closes) |
| `Ctrl-F` | cycle fit mode |
| `Ctrl-S` / `F12` | toggle stream ↔ screencap |
| `Ctrl-Alt-G` / `F12` | grab mode — keys go straight to the device (also locks Zellij) |
| `Esc` ×2 | quit |
| click, hold, arrows | "pending tap": preview + nudge ±12 px, Enter confirms |

Dragging acts exactly like a finger: the device receives `DOWN` the instant
the button is pressed, live `MOVE` events as you drag (throttled to ~35 Hz),
and `UP` on release — apps react in real time, and long-press works by
holding the button.

Rendering is incremental: only changed rows are re-emitted, so slow links
and tmux/ssh sessions stay fluid. A live sparkline on the status bar shows
recent frame-render time.

## Web viewer

One `<img src="/stream.mjpg">` — a single multipart stream, no polling, no
per-frame HTTP overhead. Features:

- tap / drag-to-swipe with a cursor dot, pinch-safe pointer handling
- pointer-lock keyboard capture (`Ctrl-Alt-G`) with Android F-key mapping
- on-screen keyboard + text bar on touch devices
- device picker (USB list, WiFi/Tailscale scan, manual IP) with **live
  switching** without restarting the server
- sliders that actually work: FPS pacing, screenrecord bitrate, JPEG
  quality, stream scale — each restarts the pipeline with the new params
- rotate device, wake, stay-awake, audio (via scrcpy, if installed)
- HUD chips: live/fps/resolution/battery · glass UI, dark theme

## JSON API (the future native-app surface)

The API is deliberately REST-shaped and dependency-free, so a Rust/Go
daemon or an Android client can reuse the exact same endpoints:

| Endpoint | Purpose |
|---|---|
| `GET /api/status` | serial, model, size, battery, wake, source, bitrate/q/scale, devices |
| `GET /api/devices` | USB devices from `adb devices` |
| `POST /api/scan` | scan WiFi/Tailscale for adb (port 5555) |
| `POST /api/connect` | `{"serial": "…"}` — hot-switch device |
| `POST /input/tap` | `{"x": 0.5, "y": 0.5}` — **normalised** 0–1 coords |
| `POST /input/swipe` | `{"x1","y1","x2","y2"}` normalised |
| `POST /input/key` | `{"code": 82}` or `?code=82` |
| `POST /input/text` | `{"text": "hello world"}` (auto-chunked) |
| `POST /input/audio` | `{"enabled": true}` (requires scrcpy; live **Ogg Opus** at `/audio.ogg`) |
| `GET /audio.ogg` | live Ogg Opus stream from the device (browser-plays via 🔊) |
| `POST /settings/bitrate` | `{"bitrate": 8000000}` |
| `POST /settings/fps` | `{"fps": 30}` |
| `POST /settings/quality` | `{"q": 4}` — MJPEG quality 1–10 |
| `POST /settings/scale` | `{"scale": 75}` — stream downscale % |
| `POST /settings/rotate` | `{"deg": 90}` |
| `POST /settings/wake` | wake + dismiss keyguard |
| `GET /stream.mjpg` | multipart MJPEG live stream |

## Performance notes

- **Input**: one persistent `adb shell` pipe replaces per-event `adb`
  process spawns — event latency drops from ~80 ms to ~2 ms.
- **Capture**: `screenrecord` → `ffmpeg -flags low_delay -probesize 32768
  -strict unofficial -c:v mjpeg -f image2pipe`. The `-fflags nobuffer
  -probesize 32` combo that appears in most scripts **starves the h264
  demuxer on live pipes** — 32 KB probe + low_delay streams at full rate
  with the first frame in ~1 s.
- **Raw fallback** (`--no-stream`, no ffmpeg): raw RGBA `screencap` (16-byte
  header-aware) skips device-side PNG encoding — ~2× the old `-p` path's
  speed; the web downscales to ≤1280 before encoding JPEG.
- **Stale pipelines**: the app `pkill`s orphaned on-device `screenrecord`s
  before starting, and a watchdog respawns a stalled stream or falls back
  to raw capture, so the two front-ends never silently freeze.
- The TUI decodes JPEG with `Image.draft()` — decompress only at the
  resolution the terminal needs.

## Scenarios

- **USB**: `scterm.py` — instant, zero-config.
- **WiFi adb**: `adb tcpip 5555`, `adb connect IP`, then `--serial IP:5555`.
- **Tailscale / anywhere**: run `scterm.py --web 8000` on the host, open
  `http://<tailscale-ip>:8000` from any device. The TUI keeps running
  locally with `--both` if you want both.
- **Zellij**: `scterm.py --zellij` — stays on the normal screen (detach/
  reattach keeps content), `Ctrl-Alt-G` locks Zellij and passes keys
  through to the Android device.

## Roadmap (porting)

The pipeline is deliberately small and side-effect-light: `AdbInput`
(persistent shell), `CaptureManager` + `FfmpegSource` (screenrecord →
ffmpeg → MJPEG frames), `TUI` (render/draw), `WebApp` (HTTP). All params
flow through one `params` dict under one lock — a Rust/Go port can take the
ffmpeg command line and the JSON API as the specification, and the web page
is already a self-contained client the Android app can wrap in a WebView or
replace with the API calls directly.