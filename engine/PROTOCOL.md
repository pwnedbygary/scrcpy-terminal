# scrcpy 4.1 wire protocol — reverse-engineered spec

Source of truth: the installed server on the device
(`/usr/share/scrcpy/scrcpy-server`, 733706 bytes, a DEX payload) plus the
matching scrcpy v4.1 sources (verified: version string "4.1", socket names,
I/O byte orders all cross-checked).

## Session setup

The client (or our engine) does, per session:

1. `adb push <server payload> /data/local/tmp/scrcpy-server`
2. pick a random session id: `scid = %08x` (8 hex, e.g. `2dbc2801`)
3. `adb [-s SERIAL] forward --no-rebind tcp:PORT localabstract:scrcpy_<scid>`
   (USB uses a UsbFfs transport; the abstract name is exactly `scrcpy_<scid>`)
4. launch the server (returns immediately):

```
adb [-s SERIAL] shell CLASSPATH=/data/local/tmp/scrcpy-server \
    app_process / com.genymobile.scrcpy.Server \
    4.1 scid=<scid> log_level=error tunnel_forward=true [opts]
```

  Defaults when an option is omitted: video=true, audio=true, control=true,
  codecs h264/opus, source display. Omit-able options we pass:
  `video=false`, `audio=false`, `control=false`, `video_bit_rate=N`,
  `audio_bit_rate=N`, `max_size=N`, `max_fps=<int|"0">`, `tunnel_forward=true`.

5. Because `tunnel_forward=true`, the SERVER binds the abstract socket
   `scrcpy_<scid>` and **accepts up to three connections, in this order**:
   video, audio, control. Each host connection to `tcp:PORT` is routed by
   adb to one accept.

## Per-connection framing

### Video socket (first accept; also first to carry the handshake)

```
[1 byte ] 0x00            dummy byte (server writes it on the first accept)
[64 bytes] device name     UTF-8, null-padded (SC_DEVICE_NAME_FIELD_LENGTH)
[4 bytes] codec id         big-endian 4CC: 'h264'=0x68323634 'h265'=0x68323635
                           'av1'=0x00617631 'vp8' 'vp9' 'opus'=0x6f707573
                           'aac'=0x00616163 'flac' 'raw'=0x00726177
then a stream of packets:
[8 bytes] pts+flags        big-endian u64: bit62=CONFIG packet, bit61=KEY_FRAME,
                           bits0-60 = PTS (microseconds-ish)
[4 bytes] len              big-endian u32
[N bytes] packet data      CONFIG packet data = codec config (h264: SPS/PPS
                           annexb extradata); others = encoded frames
```

### Audio socket (second accept)

Same packet framing, no codec-id? (audio stream also starts with the codec
id 4CC per the demuxer; verify on the wire.)

### Control socket (third accept) — bidirectional

Client → server: `[1 byte] type` + payload. Server → client: device
messages (rotation, clipboard, power) on this socket.

Control message types (ControlMessage.java):

| type | name | payload |
|---|---|---|
| 0 | INJECT_KEYCODE | `[1]action [4]keycode [4]repeat [4]metaState` (KeyEvent DOWN=0 UP=1) |
| 1 | INJECT_TEXT | `[4]len [len]UTF-8 text` |
| 2 | INJECT_TOUCH_EVENT | `[1]action [8]pointerId [4]x [4]y [2]screenW [2]screenH [2]pressure(fixed u16/0xffff) [4]actionButton [4]buttons` (MotionEvent DOWN=0 UP=1 MOVE=2 CANCEL=3) |
| 3 | INJECT_SCROLL_EVENT | `[4]x [4]y [2]screenW [2]screenH [2]hScroll(i16 fixed*16) [2]vScroll(i16 fixed*16) [4]buttons` |
| 4 | BACK_OR_SCREEN_ON | — |
| 5 | EXPAND_NOTIFICATION_PANEL | — |
| 6 | EXPAND_SETTINGS_PANEL | — |
| 7 | COLLAPSE_PANELS | — |
| 8 | GET_CLIPBOARD | — |
| 9 | SET_CLIPBOARD | `[1]textLenSz [text] [1]paste`, paste=0 copy-only, 1 paste |
| 10 | SET_DISPLAY_POWER | `[1]mode` |
| 11 | ROTATE_DEVICE | — |
| 12–14 | UHID_CREATE/INPUT/DESTROY | HID reports |
| 15 | OPEN_HARD_KEYBOARD_SETTINGS | — |
| 16 | START_APP | — |
| 17 | RESET_VIDEO | — |
| 21 | RESIZE_DISPLAY | — |
| 22 | SCAN_FILE | — |

All multi-byte fields are big-endian. Successfully speaking this protocol
eliminates the scrcpy binary, the mkv FIFO and the ffmpeg remux from the
hot path.
### UHID (virtual HID — gamepads, keyboards, mice)

UHID creates a virtual HID device on the Android device (via /dev/uhid).
This is how scrcpy exposes a host gamepad; it works on any Android ≥ 9
(unrooted).

| type | name | payload |
|---|---|---|
| 12 | UHID_CREATE | `[2B]id [2B]vendorId [2B]productId [1B]nameLen [name UTF-8] [2B]reportDescLen [report descriptor]` |
| 13 | UHID_INPUT | `[2B]id [2B]len [report data]` (id ∈ 0x2000.. — SC_HID_ID_GAMEPAD_FIRST+) |
| 14 | UHID_DESTROY | `[2B]id` |

### Standard gamepad report (scrcpy's, 81-byte descriptor below)

UHID_INPUT data = 15 bytes, little-endian:

```
[0:2]  axis_left_x   (u16, -32768..32767 → 0..65535)
[2:4]  axis_left_y
[4:6]  axis_right_x
[6:8]  axis_right_y
[8:10] axis_left_trigger
[10:12]axis_right_trigger
[12:14]buttons  16-bit LE: bit0=A(south) 1=B(east) 3=X(west) 4=Y(north)
                  6=LB 7=RB 10=back 11=start 12=guide 13=Lstick 14=Rstick
[14]   hat (dpad): 0 center, 1 up, 2 up-right, 3 right, 4 down-right,
                   5 down, 6 down-left, 7 left, 8 up-left
```

Gamepad report descriptor (81 bytes):

```
0x05,0x01,0x09,0x05,0xa1,0x01,0xa1,0x00,0x05,0x01,0x09,0x30,0x09,0x31,
0x09,0x33,0x09,0x34,0x15,0x00,0x27,0xff,0xff,0x00,0x00,0x75,0x10,
0x95,0x04,0x81,0x02,0x05,0x01,0x09,0x32,0x09,0x35,0x15,0x00,0x26,0xff,
0x7f,0x75,0x10,0x95,0x02,0x81,0x02,0x05,0x09,0x19,0x01,0x29,0x10,0x15,
0x00,0x25,0x01,0x95,0x10,0x75,0x01,0x81,0x02,0x05,0x01,0x09,0x39,0x15,
0x01,0x25,0x08,0x75,0x04,0x95,0x01,0x81,0x42,0xc0,0xc0
```

### Session header (video stream, 4.x)

After codec-id, the stream starts with a **session message** (12 bytes)
when `header[0] & 0x80`:

```
[4B] flags      (bit7 of byte0 = SESSION; low bit(s) = client-resized)
[4B] width      (big-endian)
[4B] height
```

Re-session messages can appear mid-stream (client resize). Verified
against the v4.1 demuxer (`sc_demuxer_is_session`: `header[0] & 0x80`).

### UHID / gamepads on this device

UHID needs `/dev/uhid` (open as uid 2000 shell; group uhid is granted on
this ROM, so open is possible) — registration on-device still unconfirmed
in the engine probe (format matches the client serializer byte-for-byte;
next step: capture the server-side UHID error during create). Fallback
that needs no UHID: map gamepad dpad/buttons to INJECT_KEYCODE
(KEYCODE_DPAD_* / KEYCODE_BUTTON_*) and sticks to INJECT_TOUCH, all over
the (verified) control channel — root-free.
