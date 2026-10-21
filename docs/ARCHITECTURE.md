# How scterm works

scterm is one Go program with a small C part (cgo) for the FFmpeg and PulseAudio libraries.
The browser player is embedded in the binary. This page explains the pieces; the
[README](../README.md) explains how to use them.

```text
device ──► demux ─┬─► H.264 ─► libavcodec ─► canvas ─┬─► terminal: half-block cells
                  │                                  └─► browsers: JPEG over WebSocket
                  └─► Opus, AAC, FLAC or raw ─► 48 kHz stereo PCM ─┬─► PulseAudio
                                                                   └─► browsers: PCM
keys, mouse, browser input ─► scrcpy control messages ─► device
```

The device side is the scrcpy server, started over adb, or the scterm Android app in peer mode.
Both send the same stream format, so everything after the connection is the same code.

## Starting a session over adb

1. scterm picks the device: `-s`, else `ANDROID_SERIAL`, else the only device that
   `adb devices` lists as ready. When several are ready, it asks for `-s`; when none is, it
   explains the state of the first device it found (`adb.go`).
2. It copies the server embedded in the binary to `/data/local/tmp/scrcpy-server.jar`.
3. It listens on 127.0.0.1, on the first free port from 27183 to 27282, and runs
   `adb reverse` so that the device can connect to it. When `adb reverse` fails, it uses
   `adb forward` and connects to the device instead (`link.go`).
4. It starts the server with `app_process`, passing the version it expects (4.1) and the
   options from the command line.
5. It accepts the video, audio and control connections, in that order, for the streams that are
   on. The first connection starts with the device name. scterm waits up to 15 seconds for all
   of the connections together, then up to 15 seconds more for the device name. When the server
   exits first, scterm shows the server's last output lines.
6. It removes the tunnel, and it stops the PulseAudio streams of any other scterm on this
   computer, so that only the new session plays sound.

## The scrcpy stream format

These notes describe scrcpy v4.1, which `protocol.go` and `stream.go` implement.

- Over adb, the connections come in the order video, audio, control, and any of them can be
  absent. The first one starts with a 64-byte device name. In peer mode the control connection
  comes first, and the handshake carries the name instead.
- Each media stream starts with a 4-byte codec id (0 means the stream is disabled, 1 that it
  failed). Then come packets, each after a 12-byte big-endian header: an 8-byte
  word and a 4-byte size. When the top bit of the word is set, the packet is a session packet,
  which carries the video size. Otherwise bit 62 marks a config packet, bit 61 a keyframe, and
  the rest is the timestamp in microseconds.
- The video stream starts with a session packet. scterm accepts only H.264.
- An H.264 config packet (SPS and PPS) is put in front of the next media packet.
- A touch event carries the current video frame size. The server ignores a touch event whose
  size does not match the video it sends.
- Control messages use the scrcpy v4.1 layouts. `protocol/fixtures/scrcpy_wire.json` holds
  hand-derived golden bytes for them, which the Go and Kotlin tests check.

## The terminal display

- The canvas is the size of the terminal: one pixel wide and two pixels tall per character
  cell, without the bottom line. The picture is scaled down to fit, keeps its shape, and is
  never scaled up beyond the video's own size (`stream.go`). libswscale does the scaling.
- Each cell is a `▀` character: the foreground colour is the top pixel and the background
  colour the bottom pixel. Each colour channel is cut to 3 bits, so that neighbouring cells are
  often equal and a row becomes a few long runs of one colour. The packing uses AVX2 on x86-64,
  NEON on aarch64 and plain C elsewhere (`native.c`).
- scterm writes only the rows that changed since the last frame. It rewrites the whole screen
  every 300 frames (`--repaint-interval`), after a resize and when an overlay changes. Each
  frame is wrapped in the terminal's synchronized-update mode (`tui.go`).
- Decoded frames reach the terminal through a mailbox with one slot. A newer frame replaces an
  older one that is not drawn yet, so a slow terminal drops frames and never slows the decoder.
- The action bar, the Controls menu and the on-screen keyboard are overlays. They repaint at
  once, without waiting for a video frame, because a still screen may send none.
- While scterm runs, the bottom line shows messages ("notices") instead of printing them,
  because printed lines would scroll the picture. scterm prints them again on exit
  (`notice.go`).

## Input

- Input goes over the control connection, with TCP_NODELAY. scterm writes scrcpy control
  messages itself; it never runs `adb shell input`.
- Drag moves are combined to at most one every 8 ms, so a flood of mouse events cannot queue up
  in front of the device (`appkeys.go`).
- Touch positions are mapped from the canvas (or the browser's picture) to the video frame.
- The action catalog, `protocol/fixtures/actions.json`, defines every action: its Alt chord,
  F-key, labels, what it sends, and the order of the terminal's action bar and Controls menu
  and of the browser's action bar. The Go tables (`actions.go`) and the browser page are built
  from it, and tests check the Go tables, the page and the Android viewer against it.

## Sound

- The device sends Opus (the default), AAC, FLAC or raw PCM. scterm decodes it with libavcodec
  and converts it with libswresample to 48 kHz stereo, 16-bit samples (`native.c`).
- The terminal display plays it through a PulseAudio stream named `scterm`, with a 20 ms
  buffer target and a software gain for Alt+M, Alt+- and Alt+= (`audio.go`).
- scterm plays on the sink set by `SCT_AUDIO_DEVICE`. Without it, scterm opens its stream on
  the desktop's default sink when that is a sound card, and otherwise on the sinks other
  programs play on, then the sound cards `pactl` reports running, then every sound card it
  lists, in that order; it asks `pactl` for these (`audio.go`). When none qualifies, and
  always without `pactl`, scterm names no sink, and the server plays the sound on its default
  sink (libpulse applies `PULSE_SINK` there). About a second after its stream appears, and
  again whenever the default changes, it moves the stream to the desktop's default sink,
  whatever kind of sink that is (`audiofollow.go`, which needs `pactl`): with a Bluetooth or
  virtual default, the order above holds only for that first second. PipeWire also moves a
  stream opened without a sink name to a new default by itself (seen with PipeWire 1.6 and
  WirePlumber 0.5).
- scterm tries libpulse's default server first (`PULSE_SERVER`, or client.conf's default
  server, or else the user's socket), then the socket in `XDG_RUNTIME_DIR` by name. It tries
  every server and sink once per round, in up to three rounds 150 and 300 ms apart, for a
  server that is still starting; a server or sink that libpulse refuses as invalid is not
  tried again, and with no server the open gives up after about 0.45 s. The leftover streams
  of another scterm are stopped on the first server, in the same order, that answers.
- In the browser display, the browser plays the sound, and the PulseAudio stream stays silent
  unless `--audio-dup` is given (`app.go`).

## The browser display

- The decoder is the same. The canvas is the video's own size, and every browser gets the same
  canvas and scales it to its window (`web.go`).
- Each browser has its own mailbox with one slot, and its own goroutine that encodes the newest
  frame as a JPEG (FFmpeg's mjpeg encoder, at the `--web-quality` level) and sends it. A slow
  browser loses frames instead of slowing the decoder or the other browsers.
- The JPEG is encoded straight into a pooled buffer, after the 16-byte frame header, so each
  frame is one write. There is one encoder for each canvas size, behind a mutex, because a
  libavcodec context is not thread-safe. Each browser costs one encode per frame.
- Sound goes to each browser that asked for it, as raw 16-bit stereo PCM at 48 kHz. A browser
  that falls behind loses its oldest sound first.
- The page plays the sound from a ring buffer (`web/pcm-worklet.js`) in an AudioWorklet. It
  starts playing once 96 ms is buffered, keeps at most 240 ms, and plays silence when the
  buffer runs dry. Browsers allow an AudioWorklet only on https or localhost. On a plain http
  page from another computer, a ScriptProcessorNode drains the same ring in blocks of 2048
  frames (about 43 ms), which adds a little delay. The page asks for a 48 kHz audio context;
  when the browser runs it at another rate anyway, the ring resamples, with linear
  interpolation, instead of playing the stream slow or fast until the ring overflows or runs
  dry.
- The page sends a `hello` message with its size and `caps:"audio"` each time it connects. The
  server makes a new client for each connection, so the hello must be sent again after every
  reconnect.
- Control events from the page are small JSON messages. The socket reader parses them and one
  dispatcher goroutine applies them in order, so input never waits for a frame to be encoded
  (`webinput.go`).
- Every request, for the page, its files or the WebSocket, must name the server in its Host
  header by an IP address, `localhost` or a name under it, the `--web-addr` name or a name in
  `SCTERM_WEB_HOSTS`, on any port (`webHostAllowed` in `web.go`). When the server listens on an
  address that is not a loopback address (`0.0.0.0` and `::` included), this computer's host
  name works too, with or without `.local`, and for a host name with dots its first label with
  `.local` (`webHostNames`). Any other name, and an empty or unreadable Host, gets a 403 and
  none of the page, which stops DNS rebinding. Before that, Go's HTTP server answers 400 to a
  request with two Host headers, or with one that has a byte no Host header may have, and to an
  HTTP/1.1 request with none; none of the page is served either way. At start, scterm reports
  each entry of `SCTERM_WEB_HOSTS` that is not a name, or a name and a port, and ignores it. The
  server also refuses WebSocket connections whose Origin header names a different host. It has
  no other access control: no password and no encryption.
- Messages from the server to the page: binary video frames (`0x01`, a 16-byte header, then a
  JPEG, or raw pixels for a client that asks for them), binary sound (`0x02`), the `hello`
  reply (`0x03`), JSON status messages, and a `bye` with the reason just before the server
  closes the connection.

## The window display

- `--window` runs the browser display and opens a browser on its address (`window.go`).
- A Chromium-family browser is started with `--app` (no tabs or address bar), `--window-size`,
  a private profile (`--user-data-dir=~/.cache/scterm/chromium`) and an autoplay policy that
  lets sound start without a tap. The private profile matters: with a shared profile, Chromium
  hands the address to the browser that is already running, which ignores the window size.
- Firefox gets a temporary profile with autoplay allowed. It opens a normal window, or a kiosk
  window with `SCTERM_FIREFOX_KIOSK=1`. Firefox cannot be given a window size.
- The browser runs in its own process group, with its output in a temporary log file. When it
  exits within 15 seconds without showing the picture, scterm prints the error lines from the
  log. When scterm ends, it stops the whole process group, so no browser is left holding the
  private profile.
- When the last browser of a `--window` session disconnects and none connects again within
  3 seconds, scterm ends. A reload connects again sooner, so it keeps the session.
- When `WAYLAND_DISPLAY` names no usable socket, scterm looks for a Wayland socket in
  `XDG_RUNTIME_DIR` (`/run/user/UID` when that is not set). When there is none, it uses
  `DISPLAY`, or an X socket that it finds in `/tmp/.X11-unix`. When it had to look, it says
  which one it uses.

## Peer mode

The Go client is in `peer/`: identities and pairing, nearby discovery over multicast DNS, the
readiness probe, and the list of paired devices. [PEER_PROTOCOL.md](PEER_PROTOCOL.md)
specifies the protocol.

## Exit status

A read from the device that fails after a stream has started means the device was lost
(`lostError` in `stream.go`). When that ends the stream the display shows (the video, or, in
the terminal display, the sound when there is no video), scterm exits with status 2. A failure
before the stream starts exits with 1, as in scrcpy (`main.go`).

## Encoding cost

`go test -run XXX -bench Encode -benchtime 200x` measures the JPEG encode for the browser
display. Its test image is noisy, which compresses much worse than a real screen, so the results
are an upper bound. Measured on an AMD Ryzen 7 7800X3D on 2026-10-21:

| Canvas | Quality | Encode | Frame | Frames per second, at most |
| --- | --- | --- | --- | --- |
| 1280x720 | 5 | 2.0 ms | 27 KB | about 490 |
| 1777x1000 | 5 | 3.7 ms | 67 KB | about 270 |
| 480x1080 | 5 | 1.0 ms | 14 KB | about 1000 |
| 1777x1000 | 2 (best) | 6.3 ms | 167 KB | about 160 |

On real content, the `jpegMs` field of the status messages, and the per-browser line that
`SCT_DEBUG_WEB=1` logs, show the actual cost.
