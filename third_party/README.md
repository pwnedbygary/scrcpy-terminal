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

## Go

The Linux binary includes Go's runtime and standard library, with the copies of `golang.org/x`
packages that the standard library carries, and the Go modules listed in
[LICENSE.golang](LICENSE.golang), all by the Go Authors and licensed under the BSD 3-Clause
License. That file, which `scterm licenses` prints, holds the list and the license.
`TestGoLicenseIsTheModulesOwn` checks the list against the modules that the binary links, and
the text against each module's copy in the module cache (under `vendor/`, in vendor mode) and
Go's in `GOROOT`.

## Shizuku

The APK includes the [Shizuku API](https://github.com/RikkaApps/Shizuku-API) library by RikkaW
(`dev.rikka.shizuku:api` and `:provider`, and the `:aidl` and `:shared` they depend on), which
is licensed under the MIT License: [LICENSE.shizuku](LICENSE.shizuku) is that repository's
`LICENSE`. The APK carries a copy of it in `android/app/src/main/assets`: when the file changes,
copy it there too and update its SHA-256 in `licenses_test.go` (`TestAppCarriesTheLicenses`
checks that the two are the same, and that the text has that SHA-256). The app's
other libraries, AndroidX, Kotlin's and the few that these use, are licensed under the Apache
License 2.0, whose text the APK carries as `assets/LICENSE.scrcpy`.
