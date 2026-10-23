# Releasing scterm

CI builds and tests every change, and publishes a release for each version tag. The workflow is
[`.github/workflows/build.yml`](../.github/workflows/build.yml).

## What CI does

The workflow runs on every push to `main`, on every pull request to `main`, on every `v*` tag,
and when you start it by hand. It has three jobs:

- **build**: installs `pkg-config`, the FFmpeg and PulseAudio development packages, and zsh and
  fish for the paste tests; runs `go vet ./...` and `go test -race ./...`, and builds a stripped
  binary, uploaded as the artifact `scterm-linux-x86_64`.
- **android**: runs the Android unit tests and lint, builds the debug APK and `peerctl`, and
  uploads the debug APK as `scterm-android-debug`. It rebuilds the scrcpy server that the binary
  embeds, and fails unless the result is the embedded jar (see
  [DEVELOPMENT.md](DEVELOPMENT.md#the-scrcpy-server)). For a tag or a manual run, it also builds
  the signed release APK and uploads it as `scterm-android-release`.
- **release**, for a tag or a manual run: builds the release binary with the version stamped
  into it and fetches the signed APK. For a tag only, it publishes a GitHub release named
  `scterm vX.Y.Z`, with the tagged commit's message as its notes. It attaches `scterm`,
  `scterm-android-X.Y.Z.apk`, and the license texts `LICENSE` (scterm's) and `LICENSE.scrcpy`
  (the scrcpy server's).

## Make a release

1. Check that CI passes on the commit you want to release.
2. Tag it and push the tag:

   ```sh
   git tag v2.1.0
   git push origin v2.1.0
   ```

The version must be `MAJOR.MINOR.PATCH`, each part from 0 to 99. The Go binary gets it in
`main.version` without the `v`, so `scterm version` prints `scterm 2.1.0`. The APK gets it as
its version name, and as its version code `MAJOR*10000 + MINOR*100 + PATCH` (`v2.0.0` gives
20000): Android accepts an update only when the version code grows.

Once the release is published, `scterm update` and the app's *Check for updates* find it. Both
check each download against the SHA-256 digest that GitHub publishes for it, when GitHub
publishes one.

## Rehearse a release

```sh
gh workflow run build.yml --ref BRANCH
```

A manual run does everything a tag does, including the signed APK, but publishes nothing. The
binary's version is then `dev`, and the APK is `scterm-android-dev.apk`.

## The Android signing key

The release APK is signed with a key kept in two repository secrets:

| Secret | What it holds |
| --- | --- |
| `SCTERM_KEYSTORE_BASE64` | The PKCS12 keystore, base64-encoded |
| `SCTERM_KEYSTORE_PASSWORD` | The keystore's password, which is also the key's password |

The key alias is `scterm`. CI writes the keystore to a temporary file, points
`SCTERM_KEYSTORE` at it, builds the release APK, deletes the file, and prints the certificate's
SHA-256 with `apksigner`. Without `SCTERM_KEYSTORE`, a release build is left unsigned.

Android lets an app be replaced only by an APK signed with the same key. Keep a backup of the
keystore and its password: without them, moving to a new key means removing the app from every
device, which deletes its identity and its pairings. The release certificate's SHA-256 is also
in `android/app/build.gradle.kts` (`releaseCert`), so that a build signed with another key says
so instead of offering a release it cannot install.

To build a signed release APK on your own computer:

```sh
cd android
export SCTERM_KEYSTORE=/path/to/scterm-release.p12
read -rs SCTERM_KEYSTORE_PASSWORD && export SCTERM_KEYSTORE_PASSWORD
./gradlew --no-configuration-cache :app:assembleRelease -Pscterm.version=2.1.0
```

`SCTERM_KEY_ALIAS` sets another key alias.
