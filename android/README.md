# scterm for Android

One APK with both roles: **serve** this device's screen to paired devices, and
**view and control** paired devices. It speaks the
[scterm peer protocol](../docs/PEER_PROTOCOL.md): scrcpy v4.1 media and control
inside mutual TLS with pinned identities. scterm on Linux can view a device that
serves with this app (see [peer mode](../README.md#peer-mode)).

Status: early, but running on real devices (an Android 16 phone and an
Android 13 handheld): both serving modes, pairing, viewing, control and audio.

## Install

Download `scterm-android-<version>.apk` from the
[latest release](https://github.com/pwnedbygary/scrcpy-terminal/releases/latest)
and open it on the device (allow installs from your browser or file manager
when asked). Release APKs are signed by the certificate with SHA-256
`8b:dc:c9:7a:62:21:ad:9e:03:75:12:eb:79:e1:2b:5a:39:63:19:fb:39:5f:2b:23:a0:fd:dc:50:cc:3d:ca:76`
(`apksigner verify --print-certs` shows it).

A build from source is signed with your own debug key, and Android will not
replace an app signed with one key by the same app signed with another:
switching between the two means uninstalling first, which deletes the device's
identity and pairings (pair again afterwards).

Updates, from 2.1.0 on: *Check for updates* on the main screen (release builds
also look once a day and say when one is out). The app downloads the new APK,
checks it against the digest GitHub publishes and against its own signing
key, and hands it to Android's installer, which asks before replacing the app;
the first time, Android asks you to allow installs from scterm. Serving stops
during the update. A build from source that isn't signed with the release key
says so when a release is out, rather than offering one it can't install. Test
builds can point at a local server instead (`devicetest/update_server.py`,
`-Pscterm.updateUrl`).

## Using it

**Serve** (on the device to be controlled): choose a mode and tap *Start serving*.

- *Screen capture (standard install)*: approve the screen-capture prompt. For
  remote touch and keys, enable **scterm remote input** in Accessibility
  settings (the app offers a shortcut). Limits: no audio yet, one finger,
  keys only where Android offers an equivalent (home, back, recents, lock,
  volume, typing, D-pad on 13+).
- *Full control via ADB-activated helper (advanced)*: the app shows one command;
  run it from a computer with `adb` (USB or wireless debugging), or tap *Start
  with Shizuku* if [Shizuku](https://shizuku.rikka.app/) runs on the device. It
  starts the scrcpy server contained in this APK with shell identity: full
  input, multi-touch, audio, clipboard, rotation. Needed again after a reboot.

Then *Invite a device…* and choose what it may do (see, hear, control,
clipboard). The invitation is valid once, for 10 minutes, and while it is
open this device is listed as nearby on the network.

**Control** (on the other device): *Pair with a device…* and pick the device
under *Nearby*. Both screens show the same six-digit code; check it matches
and accept on both. Or enter the invitation's `address:port CODE` (or open a
shared `scterm://pair` link): this also works from another network, as long as
it can reach the device. Then *Connect*.

During a session, System Back is the remote Back, or hides the keyboard when
it is open. While connecting, and after a session has ended, Back leaves the
viewer. Leaving the screen ends the session. The bar has the device actions of
the catalog shared with the terminal and web clients (Back, Home, Recents, the
volume keys, Mute, Power, Menu, Rotate, Notifications, Settings shade,
Collapse and Refresh video), plus Keyboard and Screenshot; *Take control* and
*Disconnect* follow. In portrait the bar sits below the video; in landscape
the video fills the screen and the handle at the bottom brings the bar up in a
floating panel, which hides a few seconds after the last touch on it. The main
screen's *Viewer* settings keep the bar or the statistics up, and set the
delay.

On Android 17 and later, the app asks for the local network permission the
first time it needs it; without it, it cannot reach other devices. Nearby
pairing needs both devices on the same network. Forget a device or reduce what
it may do at any time; its live session ends immediately.

## Paired-device readiness

Each paired device has a colored badge and a text reason. Green means this
installation has authenticated the peer and can view its screen; control may
still belong to someone else. Yellow means checking/stale or a reachable
limitation (helper, permissions, older app). Red means the check failed; "Not
reachable" does not establish whether the device is powered off.

Tap a badge for address, last checked/contact, viewing/control/audio readiness,
and local serving/permission actions. Reverse readiness is checked on the other
device: an incoming session does not prove this device can view that peer.
Refresh status also offers local-network permission when needed.

Checks run every 15 seconds on the main screen, or every 60 seconds while
serving in the background, with two concurrent checks and a five-second budget
per peer. Results expire after two polling intervals. No checks run when the
main screen is closed and serving is stopped. The existing serving notification
summarizes readiness and names active incoming sessions, silently. Notifications
must be allowed in Android settings to see it. Saved direct addresses work
without discovery; presence-discovered replacements must pass pinned TLS.

Authenticated metadata updates immediately; routine successful-contact timestamps
are saved at most once per five minutes to reduce storage writes. Transient
connectivity is not restored as fresh after a process restart.

The disconnect screen keeps the paired device and viewer settings and offers
Retry after the previous session's sockets and media resources have closed.
Retry uses the latest verified address and never asks to take control from
another viewer. Old connection, decoder and session callbacks cannot affect the
new attempt. Live outgoing sessions show Connected or Watching and replace
extra probes for that direction. Incoming connections remain separate.

## Build

Requires JDK 17 and an Android SDK (AGP installs platform 37 and build-tools 36
on first build when licenses are accepted).

```sh
cd android
./gradlew :app:assembleDebug          # app/build/outputs/apk/debug/app-debug.apk
./gradlew :protocol:test :peer:test :app:testDebugUnitTest :app:lintDebug
```

Pinned toolchain: Gradle 9.7.1 (wrapper checksum pinned), AGP 9.4.1, Kotlin
2.4.20, compile/target SDK 37 (Android 17), min SDK 27 (Android 8.1).

Release builds and the signing key are described in
[docs/RELEASING.md](../docs/RELEASING.md).

## Modules

- `protocol`: pure Kotlin. scrcpy v4.1 wire codecs, the peer handshake and
  envelope schema, pairing, and the shared action catalog. Tested against
  `../protocol/fixtures`.
- `peer`: pure Kotlin. TLS identities, `TargetServer` (pairing, grants, input
  lease, stuck-input release, revocation), the keyframe-aware `MediaFanout`,
  and `ControllerClient`. Tested over loopback TLS.
- `scrcpy-server`: the vendored scrcpy v4.1 server
  (`../third_party/scrcpy-server-src`), compiled into the APK as the
  full-control helper. One local patch, marked `scterm patch`: playback audio
  capture also matches game and untagged players, not only media, so games
  stream their sound.
- `app`: the Android UI, the serving service and its backends, and the viewer
  (MediaCodec, AudioTrack, input).
- `peerctl`: a desktop tool built on `peer`. It pairs with real targets and
  runs sessions against them (saving the video or a frame, sending scripted
  input), or serves a test pattern as a target on loopback. Build it with
  `./gradlew :peerctl:installDist`; see
  [docs/DEVELOPMENT.md](../docs/DEVELOPMENT.md#peerctl).

## License

The app is released under the MIT License ([LICENSE](../LICENSE)). The scrcpy
server compiled into it keeps its own license, the Apache License 2.0
([third_party/LICENSE.scrcpy](../third_party/LICENSE.scrcpy)), and so do the
AndroidX and Kotlin libraries the app uses. The Shizuku API library, behind
*Start with Shizuku*, is under the MIT License
([third_party/LICENSE.shizuku](../third_party/LICENSE.shizuku)). The APK carries
the three texts, as `assets/LICENSE`, `assets/LICENSE.scrcpy` and
`assets/LICENSE.shizuku`.
