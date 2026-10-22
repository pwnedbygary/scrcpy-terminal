# Third-party code

## The scrcpy server

scterm bundles the server of [scrcpy](https://github.com/Genymobile/scrcpy) v4.1, by Genymobile
and its contributors, which is licensed under the Apache License 2.0:
[LICENSE.scrcpy](LICENSE.scrcpy).

- [scrcpy-server-src](scrcpy-server-src) is the server's source. The Android app compiles it into
  its full-control helper (the module [android/scrcpy-server](../android/scrcpy-server)).
- `scrcpy-server.jar.d/scrcpy-server` is the server that the Linux binary embeds and copies to
  the device in adb mode. [build-scrcpy-server.sh](build-scrcpy-server.sh) builds it from that
  source, with the changes below, and `scrcpy-server.jar.d/scrcpy-server.provenance` records
  how.

The APK carries a copy of `LICENSE.scrcpy` in `android/app/src/main/assets`: when the file
changes, copy it there too (`TestAppCarriesTheLicenses` checks that the two are the same).

## Local modifications

scterm's changes to the server's source, each marked with a `scterm patch` comment where it is
made:

- `scrcpy-server-src/src/main/java/com/genymobile/scrcpy/audio/AudioPlaybackCapture.java`,
  commit `afcd41b` (2026-09-24): scrcpy's playback capture (`--audio-source playback`) records
  media only (`USAGE_MEDIA`); scterm's also records game and untagged audio (`USAGE_GAME` and
  `USAGE_UNKNOWN`), so that games stream their sound.
