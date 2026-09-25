# scterm peer protocol, version 1

How an scterm controller (the Android app, or scterm's Go client) talks to an
scterm target (the Android app serving its screen). Reference implementations:
`android/protocol` (formats) and `android/peer` (TLS, sessions, policy) in
Kotlin, `peer/` in Go, with golden vectors in `protocol/fixtures/` that both
must match. Status: host-tested on the JVM and in Go (including Go against
the Kotlin target), and exercised on a physical Android 16 phone as target
and controller; not yet between two physical devices.

## Design in one paragraph

Media and input keep the **scrcpy v4.1 wire format** byte for byte (codec
header, 12-byte packet headers, session packets, control and device
messages). Version 1 wraps that in a small authenticated envelope: **mutual TLS
over TCP**, one connection per channel (control, video, audio) exactly like
scrcpy's three sockets, each opened with a JSON handshake that binds it to one
authenticated session. A Go client that already parses scrcpy streams therefore
only needs TLS, the handshake and the envelope frames; the existing demux,
decoders and control serializers stay as they are.

## Identity and trust

- Each installation has one long-term key pair and a self-signed X.509
  certificate (Android: EC P-256 in Android Keystore, non-exportable). Names and
  dates in the certificate mean nothing.
- **Identity = SHA-256 of the certificate's SubjectPublicKeyInfo (DER)**, stored
  and sent as 64 lowercase hex digits. People compare the *short form*: the
  first 10 bytes in Crockford base32, grouped `XXXX-XXXX-XXXX-XXXX`.
- Every connection is TLS 1.3 (TLS 1.2 where the platform lacks 1.3), with a
  client certificate always required. The controller pins the target's
  fingerprint; the target accepts any client certificate at the TLS layer and
  authorizes the fingerprint against its peer store right after the handshake
  (so refusals are explicit `reject` messages rather than TLS alerts). No
  hostname or CA validation happens anywhere.

## Pairing

A target that wants a new controller creates an **invitation**: its address,
an 80-bit one-time secret (valid 10 minutes, single use, invalidated after 5
wrong attempts) and, optionally, its fingerprint. Forms:

- typed: `192.168.1.20:27300 04HM-ASW9-NF6Y-Y093` (secret in Crockford base32;
  `-`, spaces, case, `O`/`0` and `I`/`L`/`1` confusions are tolerated)
- link: `scterm://pair?h=192.168.1.20&p=27300&c=04HMASW9NF6YY093&f=<hex fingerprint>`

The controller opens TLS (pinning `f` when present, otherwise accepting the
certificate provisionally) and exchanges one frame each way:

```text
controller -> {"type":"pair","v":1,"proof":"<hex>","client":{"name":"…","app":"…"},"servePort":27300}
target     -> {"type":"paired","proof":"<hex>","device":{"name":"…"},"grants":["view","audio","control"]}
           or {"type":"reject","code":"bad_proof"|"no_invitation","message":"…"}
```

```text
proof = HMAC-SHA256(secret, "scterm-pair-v1" 0x00 role 0x00 || selfFingerprint || peerFingerprint)
role  = "controller" | "target";  fingerprints are the 32 raw bytes seen on this TLS connection
```

The target verifies the controller's proof before answering; the controller
verifies the target's proof before storing anything. A relay terminating TLS
presents different certificates on each leg, so it cannot reuse a proof and
would need to brute-force 80 bits offline within the invitation's lifetime.
The invitation carries the **grants** the target chose when creating it;
`servePort` lets the target record where the controller serves, for the
reverse direction. Test vectors: `protocol/fixtures/pairing.json`.

## Directional grants

Grants are stored by the target, per peer, and enforced by the target:

| Grant | Allows |
| --- | --- |
| `view` | video channel; asking for a keyframe (`reset_video`) |
| `audio` | audio channel |
| `control` | input and navigation messages, while holding the input lease |
| `clipboard` | device clipboard updates; `get_clipboard`/`set_clipboard` (writes need the lease too) |

Pairing records identities in both directions but grants in one: B may
control A only if A granted B, independently of what B granted A. Changing or
removing a peer's grants ends its live sessions immediately.

## Channels and handshake

Every connection starts with one frame each way: `u32 big-endian length` +
UTF-8 JSON, at most 64 KiB, `type` as the discriminator, unknown fields
ignored. The control channel comes first:

```json
{"type":"hello","v":1,"minV":1,"channel":"control",
 "request":{"video":true,"audio":true,"control":true,"maxSize":1920,
            "videoCodecs":["h264"],"audioCodecs":["opus","aac","raw"]},
 "client":{"name":"Pixel 8","app":"scterm-android/0.1.0","platform":"android 37"}}
```

```json
{"type":"welcome","v":1,"channel":"control","session":"<26 chars>","token":"<64 hex>",
 "grants":["view","audio","control"],
 "caps":{"backend":"helper","video":true,"audio":true,"multitouch":true,"clipboard":true,
         "maxSessions":4,"control":["inject_keycode","inject_touch_event","…"],"notes":["…"]},
 "device":{"name":"Galaxy","model":"SM-S921B","manufacturer":"samsung","sdk":36},
 "streams":{"video":true,"audio":true,"control":true},
 "lease":"held"}
```

Then, within 15 seconds and from the **same client certificate**, one video
and/or one audio connection:

```json
{"type":"hello","v":1,"minV":1,"channel":"video","session":"<session>","token":"<token>"}
```

answered by `{"type":"welcome","v":1,"channel":"video","session":"…"}`. After
that frame each media connection carries exactly a scrcpy media stream: the
4-byte codec id (or `0` disabled / `1` failed), then session and packet
frames. There is no device-name field; `welcome.device` replaces it.

`reject.code` values: `version`, `not_paired`, `forbidden`, `busy`,
`bad_request`, `bad_token`, `bad_proof`, `no_invitation`, `unavailable`.
Versions: the target picks the highest version in `[minV, v]` it speaks (1).

## The control channel after the handshake

Controller to target: scrcpy v4.1 control messages. Target to controller:
scrcpy device messages. In both directions, **envelope frames** carry peer
events: type byte `0xFE` (never assigned by scrcpy), `u32` length, JSON.

| Envelope | Direction | Meaning |
| --- | --- | --- |
| `ping {t}` / `pong {t}` | C→T / T→C | Heartbeat every 5 s; the target drops a control channel silent for 20 s |
| `takeover` | C→T | Take the input lease (needs `control`) |
| `lease {state, holder}` | T→C | `held`, or `viewer` with the holder's name (none: the lease is free) |
| `error {code, message, controlType}` | T→C | A refused message: `forbidden`, `no_lease`, `unsupported`; at most one per type per second |
| `status {streams, caps, grants}` | T→C | Capabilities or stream availability changed |
| `bye {reason, endSession}` | both | Orderly close. From the lease holder, `endSession` disconnects every viewer (the legacy `quit`); serving itself stays on, since only the local user can stop it |

Unknown envelope types are skipped; malformed ones end the session.

## What the target enforces on every control message

Every message is fully parsed with bounds before anything else happens, then:

1. **Never forwarded**: `start_app`, `uhid_*`, `open_hard_keyboard_settings`,
   `camera_*`, `resize_display`, `scan_file` → `error forbidden`.
2. `reset_video` from a `view` grant becomes a keyframe request, rate limited
   to one per second across all viewers (the helper restarts capture; the
   screen-capture backend requests a sync frame).
3. Input needs the `control` grant, the **input lease**, and backend support,
   else `forbidden`, `no_lease` or `unsupported`. One controller holds the
   lease; others watch until they send `takeover`.
4. The target tracks what each session holds down. When a session ends or
   loses the lease, the matching releases (touch up, key up, back up) are
   injected before anyone else's input: a dropped connection cannot leave a
   finger or key stuck.
5. Clipboard sequence numbers are remapped per controller, so each device's
   `ack_clipboard` reaches the controller that asked. Device clipboard text
   goes only to sessions with the `clipboard` grant, and is never logged.
6. Text is at most 300 UTF-8 bytes per `inject_text` (the scrcpy client
   limit); longer input must be split on code-point boundaries.

## Media behavior

One capture/encoder serves every viewer; each packet is encoded to wire bytes
once and queued by reference. A viewer that falls more than 8 MiB or 250 ms
behind is cut back, re-sent the current session and codec config, and resumes
at the next keyframe (never a dependent frame after a gap). New viewers join
the same way. Audio viewers shed their oldest packets beyond 200 ms but keep
the codec config. Viewers never slow the source or each other.

Latency is preferred over completeness everywhere. Media sockets get small
kernel send buffers (64 KiB video, 8 KiB audio) so a slow link backs up into
the fanout, where the age limit applies, rather than into seconds of kernel
buffering; encoders run in low-latency mode without B-frames; controllers
decode in low-latency mode and skip ahead instead of queueing; pings keep
their own schedule so continuous input never starves the keepalive.

## Default port

TCP 27300, outside 27183–27282, which the Go client binds for its adb tunnels.
