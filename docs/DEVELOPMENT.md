# Developing scterm

This page is for people who change scterm. To use it, read the [README](../README.md). The
Android app has its own [README](../android/README.md), which explains its build and tests.

## Build

The [requirements](../README.md#requirements) list what a build needs. Then:

```sh
go build -o scterm .
```

A build from source reports its version as `dev`. Release builds stamp the version into
`main.version`, as CI does:

```sh
go build -trimpath -ldflags "-s -w -X main.version=2.1.0" -o scterm .
```

### The scrcpy server

The binary embeds the scrcpy server that adb mode copies to the device,
`third_party/scrcpy-server.jar.d/scrcpy-server`. `third_party/build-scrcpy-server.sh` builds it
from `third_party/scrcpy-server-src`, the source that the Android app builds its helper from:
scrcpy 4.1 with one change of scterm's, in `AudioPlaybackCapture.java`. After a change to that
source, rebuild the server and commit the jar with `scrcpy-server.provenance`, which the script
writes next to it: the jar's SHA-256, the source's git tree hash, the version name and the
toolchain.

```sh
ANDROID_HOME=/path/to/sdk third_party/build-scrcpy-server.sh
```

The script needs git, the JDK 17 `javac` and `jar` (another JDK version builds a different jar),
and an Android SDK with platform android-37.0 and build-tools 36.0.0; it uses no network. The
build is reproducible: with `--check`, the script builds the server again and fails unless the
result is the embedded jar, byte for byte, and the provenance records its SHA-256, the source's
tree hash and the script's version name. CI's android job runs that check. `go test` checks that
the embedded jar and the source are the ones the provenance names, so it fails after a change to
the source until the server is rebuilt, and that the provenance's version name is the script's
and the one scterm passes the server.

## Checks

CI runs `go vet ./...` and `go test ./...` on every push to `main` and every pull request
(see [RELEASING.md](RELEASING.md)). Run them, and `gofmt`, before you send a change:

```sh
gofmt -l .
go vet ./...
go test ./...
```

Some tests catch a data race only under Go's race detector, which CI does not use. Among them
are the browser display's tests of a stop that comes while the server starts (the order under
`srvMu`), of the frame buffers that its viewers share, and of its list of viewers. When you
change the browser display, run its tests under the detector too, or the whole module with
`go test -race ./...`:

```sh
go test -race -run TestWeb .
```

`go test ./...` also runs the browser player's test suites when `node` is installed, and skips
them when it is not (`web_client_test.go`). The suites load the real `web/player.js` and
`web/pcm-worklet.js`, with the DOM and the AudioWorklet replaced by stubs, so they test the code
that ships. `web/page_fixture.mjs` builds the page exactly as the server serves it. Each suite
also runs on its own:

| Suite | What it tests |
| --- | --- |
| `node web/input_test.mjs` | Pointer and keyboard mapping, and that keys match the terminal's |
| `node web/worklet_test.mjs` | The sound ring buffer: buffering, burst sizes, statistics |
| `node web/pointer_test.mjs` | The visible pointer: hover ring, drag trail, touch ripples |
| `node web/conn_test.mjs` | The connection card, the reconnect rules, the status readouts |
| `node web/page_test.mjs` | The whole page: connection wiring, action bar, Controls sheet |
| `node web/audio_test.mjs` | Starting the sound, the compatibility path, the sound chip |

### Tests that need a device or a target

These tests in `./peer` are skipped unless you set their environment variable:

| Variable | Test |
| --- | --- |
| `SCTERM_PEER_INVITATION` | `TestInteropWithKotlinTarget`: pairs with a running target |
| `SCTERM_NEARBY_LIVE=1` | `TestNearbyLive`: pairs with a device inviting on this network |
| `SCTERM_NEARBY_LIVE=discover` | `TestNearbyLive`: only lists the devices that answer |

`SCTERM_PEER_INVITATION` holds the target's invitation. With `SCTERM_NEARBY_LIVE=1`, accept the
pairing on the device when the test logs the code.

For example, against `peerctl` (the desktop target in `android/peerctl`, see below):

```sh
peerctl --home /tmp/pattern serve --port 27411 --invite-host 127.0.0.1
SCTERM_PEER_INVITATION="127.0.0.1:27411 XXXX-XXXX-XXXX-XXXX" go test ./peer -run Interop -v
```

`SCTERM_TEST_MAIN_ARGS`, `SCTERM_TEST_NO_PRESENCE`, `SCTERM_TEST_CLEANUP_LOG`,
`SCTERM_TEST_LOST_DEVICE`, `SCTERM_TEST_SESSION_END`, `SCTERM_TEST_INPUT_LOOP`,
`SCTERM_TEST_DOCTOR` and `SCTERM_TEST_UPDATE` are set by tests for the child processes they
start. Do not set them yourself. With `SCTERM_TEST_NO_PRESENCE`, main in a child process that
cannot reach its paired device does not search the network for it (multicast DNS), and notes
each search in the file that the variable names.

## Debug logging

- `SCT_DEBUG_WEB=1` logs, once a second for each browser, what the page reports: frames painted,
  paint rate, decode and encode time, errors, lost frames and bit rate.
- `SCT_DEBUG_CONTROL=1` logs the raw input bytes from the terminal, each key, touch and scroll
  sent to the device, the on-screen keyboard's hit tests, and the size of each clipboard change
  that the device reports. It writes to stderr, over the terminal display.

## Tools

### Check a running browser display

`web/check.mjs` connects to a running `scterm --web` or `--window` as the page does, and checks
what comes back: decodable JPEG frames of the right size, sound, and status messages. It needs
no browser. It exits with 0 when everything is right and 1 otherwise.

```sh
node web/check.mjs 127.0.0.1:6969 5        # frames, JPEG, sound, geometry, for 5 seconds
node web/check.mjs 127.0.0.1:6969 5 raw    # also the raw pixels: length, black bars
```

The `raw` mode matters because a decodable JPEG proves nothing about its content: the host makes
a valid JPEG even from the wrong bytes. In `raw` mode the frame must be exactly `w*h*4` bytes,
and the pixels are checked.

### Drive a touch gesture

`web/gesture.mjs` sends a touch gesture through a running browser display, on the same path the
page uses.

```sh
node web/gesture.mjs 1478                     # lock-pattern dots 1 to 9, numbered like a keypad
node web/gesture.mjs --swipe 960,700,960,250  # a swipe, in device pixels
node web/gesture.mjs --host 100.64.0.1 1478   # a display bound to another address
node web/gesture.mjs --dry-run 1478           # print the path, send nothing
node web/gesture.mjs --back                   # send Back: dismisses the lock screen's pattern view
```

It reads the lock-pattern geometry and the display size from the device with `adb`, unless
`--grid` and `--device` give them, so it works after a rotation. `--host` is 127.0.0.1 unless
you give the address the display listens on. `node web/gesture.mjs --help` lists every option.

It exists because Android marks the lock screen's pattern view `FLAG_SECURE`: the picture is
black exactly while the pattern grid is shown (`screencap` fails the same way). The grid's
position can be read from the accessibility tree, so the gesture is computed instead.

The timing defaults (`--step 45 --delay 8`) were measured on a device: Android's gesture
recognition drops a slow, dense drag without any error. `--dwell` repeats the touch at each
dot between the first and the last, because Android combines motion events and keeps only the
last of a batch. It never dwells on the last dot: holding still before the lift takes away the
speed that a fling needs.

### peerctl

`android/peerctl` is a desktop controller and target for the peer protocol, built on the same
Kotlin code as the app. Build it in `android/` with `./gradlew :peerctl:installDist`; the
program is then `android/peerctl/build/install/peerctl/bin/peerctl`. Run it without arguments
to see its commands. `peerctl serve` is a target that streams a test pattern and listens on
loopback only.

### Encoding benchmark

```sh
go test -run XXX -bench Encode -benchtime 200x
```

[ARCHITECTURE.md](ARCHITECTURE.md#encoding-cost) explains the results.

## Where things are

- [ARCHITECTURE.md](ARCHITECTURE.md): how scterm works inside.
- [PEER_PROTOCOL.md](PEER_PROTOCOL.md): the peer protocol, and `protocol/fixtures/` for the
  golden test vectors that the Go and Kotlin code share.
- [RELEASING.md](RELEASING.md): CI, releases and the Android signing key.
- [history/ANDROID_CLIENT_PLAN.md](history/ANDROID_CLIENT_PLAN.md): the original plan for the
  Android app and peer mode, kept as a record.
- [`.agent/HANDOFF.md`](../.agent/HANDOFF.md): the development checkpoint, with what was
  verified and what is open. Check it against `git log` and the source before you act on it.
