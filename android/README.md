# scterm for Android

One APK with both roles: **serve** this device's screen to paired devices, and
**view and control** paired devices. It speaks the
[scterm peer protocol](../docs/PEER_PROTOCOL.md): scrcpy v4.1 media and control
inside mutual TLS with pinned identities.

Status: early, but running on real devices (an Android 16 phone and an
Android 13 handheld): both serving modes, pairing, viewing, control and audio.
See the [checkpoint](../.agent/HANDOFF.md) for what is verified and what is not.

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
during the update. Test builds can point at a local server instead
(`devicetest/update_server.py`, `-Pscterm.updateUrl`).

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

## Modules

| Module | What it is |
| --- | --- |
| `protocol` | Pure Kotlin: scrcpy v4.1 wire codecs, peer handshake/envelope schema, pairing, the shared action catalog. Tested against `../protocol/fixtures`. |
| `peer` | Pure Kotlin: TLS identities, `TargetServer` (pairing, grants, input lease, stuck-input release, revocation), keyframe-aware `MediaFanout`, `ControllerClient`. Tested over loopback TLS. |
| `scrcpy-server` | The vendored scrcpy v4.1 server (`../third_party/scrcpy-server-src`), compiled into the APK as the full-control helper. One local patch, marked `scterm patch`: playback audio capture also matches game and untagged players, not only media, so games stream their sound. |
| `app` | Android UI, serving service and backends, viewer (MediaCodec, AudioTrack, input). |

## Using it

**Serve** (on the device to be controlled): choose a mode and tap *Start serving*.

- *Screen capture (standard install)*: approve the screen-capture prompt. For
  remote touch and keys, enable **scterm remote input** in Accessibility
  settings (the app offers a shortcut). Limits: no audio yet, one finger,
  keys only where Android offers an equivalent (home, back, recents, lock,
  volume, typing, D-pad on 13+).
- *Full control via ADB helper (advanced)*: the app shows one command; run it
  from a computer with `adb` (USB or wireless debugging), or tap *Start with
  Shizuku* if [Shizuku](https://shizuku.rikka.app/) runs on the device. It
  starts the scrcpy server contained in this APK with shell identity: full
  input, multi-touch, audio, clipboard, rotation. Needed again after a reboot.

Then *Invite a device…* and choose what it may do (see, hear, control,
clipboard). The invitation is valid once, for 10 minutes, and while it is
open this device is listed as nearby on the network.

**Control** (on the other device): *Pair with a device…* and pick the device
under *Nearby*. Both screens show the same six-digit code; check it matches
and accept on both. Or enter the invitation's `address:port CODE` (or open a
shared `scterm://pair` link), for devices on another network. Then *Connect*.
System Back is the remote Back; the bar has every action the terminal and web
clients have; Disconnect leaves. Leaving the screen ends the session.

Both devices need Android 17's local network permission (asked on first use)
and the same network. Forget a device or reduce what it may do at any time;
its live session ends immediately.
