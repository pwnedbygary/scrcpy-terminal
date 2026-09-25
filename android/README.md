# scterm for Android

One APK with both roles: **serve** this device's screen to paired devices, and
**view and control** paired devices. It speaks the
[scterm peer protocol](../docs/PEER_PROTOCOL.md): scrcpy v4.1 media and control
inside mutual TLS with pinned identities.

Status: early. Everything builds and the protocol and session layers pass
host tests, but no physical device has run this yet. See the
[checkpoint](../.agent/HANDOFF.md) for what is verified and what is not.

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
  from a computer with `adb` (USB or wireless debugging). It starts the scrcpy
  server contained in this APK with shell identity: full input, multi-touch,
  audio, clipboard, rotation. Needed again after a reboot.

Then *Invite a device…*, choose what it may do (see, hear, control,
clipboard) and read the invitation to the other device. It is valid once, for
10 minutes.

**Control** (on the other device): *Pair with a device…*, enter
`address:port CODE` (or open a shared `scterm://pair` link), then *Connect*.
System Back is the remote Back; the bar has every action the terminal and web
clients have; Disconnect leaves. Leaving the screen ends the session.

Both devices need Android 17's local network permission (asked on first use)
and the same network. Forget a device or reduce what it may do at any time;
its live session ends immediately.
