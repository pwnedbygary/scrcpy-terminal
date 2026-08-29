# sct — scrcpy in your terminal

`scrcpy` inside a terminal, built from the ground up: our own client that speaks
the scrcpy wire protocol directly to the device. No `scrcpy` binary, no ffmpeg
CLI, no wrapper pipelines. One Go binary plus the (Apache-2.0) scrcpy server
that runs on the device.

```
              ┌──────────────────────────────────────────────┐
              │               sct (this repo)                │
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
go build -o sct .
```

Then run with a device connected (or `-s <serial>`):

```sh
./sct
```

## Control

Everything works when grabbed (mouse+keys to the device). F12 or Ctrl-G toggles
grab; in Zellij the session starts "ungrabbed" so the pane keeps its own mouse,
and you press F12/Ctrl-G to hand the mouse over.

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
| Ctrl-K | Key palette: send *any* of the 284 Android keycodes (arrows navigate, Enter sends, `[a-z]` jumps) |
| Alt+M | Mute local audio stream |
| Alt+- / Alt+= | Local playback volume |
| Ctrl-Q | Quit |
| Mouse left | Tap (down+up) |
| Mouse left drag | Touch move |
| Wheel | Scroll |
| Mouse right | Back |
| Mouse middle | Home |

Letters/digits type as Android key events (games work); uppercase adds shift
meta. Other printable characters go through text injection.

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
-no-tui          headless stats mode
-dump-frames DIR write the first frames as PPM (verification)
-keys            print all supported Android keys
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

## Server

The server jar is vendored from [scrcpy](https://github.com/Genymobile/scrcpy)
(v4.1, Apache-2.0) — `third_party/scrcpy-server.jar.d/scrcpy-server`, pushed to
`/data/local/tmp/scrcpy-server.jar` on start. The Java server source is included
under `third_party/scrcpy-server-src/` and the license in
`third_party/LICENSE.scrcpy`. The client pins version `4.1`, which the server
enforces.

## Protocol notes (from scrcpy v4.1 source)

- Sockets, in order: video, audio, control (any may be absent).
- The first socket carries a 64-byte device name.
- Video/audio streams: 4-byte codec id, then 12-byte packet headers
  (session flag in MSB, config/keyframe flags in the PTS word), then payload.
- h264 config packets (SPS/PPS) must be prepended to the next media packet.
- Touch positions carry the *current video frame size* as screen_size; events
  with mismatched sizes are dropped by the server.
- Control message layouts mirror `sc_control_msg_serialize()` exactly.
