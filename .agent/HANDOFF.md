# Android peer app / Go client / parity development checkpoint

Read [the complete plan](../docs/ANDROID_CLIENT_PLAN.md) (section 2a lists the
design revisions adopted on 2026-09-24), [the peer protocol](../docs/PEER_PROTOCOL.md)
and applicable `AGENTS.md` instructions first (none exist in this repository or
its parents as of this snapshot). Reconcile this checkpoint against Git,
current source, live task ownership and test evidence before editing. Status
here is a snapshot, not proof that no other work has started.

## Update (2026-10-20: QoL overhaul, part 15: Android looks and accessibility)

- Cards are 16 dp rounded rectangles with a thin outline (same fill, padding
  and gaps). Buttons are flat: primary (accent fill, dark text) for Start or
  Stop serving and each device's Connect, secondary (outline, accent text)
  for the rest, drawn 40 dp tall inside a 48 dp touch target, with ripples
  and a dimmed disabled state. Visible buttons in a row sit 8 dp apart with
  no trailing margin, a lone button spans the row, and the tappable status
  line has a ripple. Page padding clears display cutouts and the keyboard as
  well as the system bars. The viewer's connection overlay uses the same
  buttons: Retry primary, Close (Cancel while connecting) secondary.
  Framework dialog buttons are unchanged.
- The viewer bar is built from the shared catalog (`viewer/ViewerBar.kt`):
  the catalog's short name is the text unless it is a clipped word (Rec,
  Rot, Notif, Keys, Shot), and its label is what TalkBack reads. The Menu key
  (after Power) and Refresh (one keyframe request, also in view-only
  sessions) are new; "Vol −"/"Vol +" became "Vol-"/"Vol+", "Rotate device"
  "Rotate", "Quick settings" "Shade". Bar buttons, the handle and the
  panel's collapse arrow are 48 dp tall targets (the landscape panel grows
  24 dp, and taps on the 64×24 dp of picture just above the handle's pill
  now open the panel instead of reaching the device, which in landscape is
  where a target's keyboard has its space bar). The blurred panel's tint
  went from 35% to 72%, so white labels over
  a white screen reach about 5:1 (they were about 2:1), and the handle is
  darker.
- TalkBack: card titles, the page title and the pair dialog's sections are
  headings; a device row's buttons read "Connect to <name>" and "Manage
  <name>"; the delay slider is named "Delay before the controls hide" with
  its value in seconds; the viewer's status line is a live region. Titles
  are still capitalised with `uppercase()`, which ignores the locale
  (`isAllCaps` would show "THİS DEVİCE" on Turkish devices), so TalkBack is
  given the capitalised text as before.
- From the part-11 review: "Listening on HOST:PORT" no longer formats the
  port with the locale's digits, and the notifications help line says which
  channel is blocked (serving: the Stop button can't appear; pairing:
  requests can't appear while the screen is closed; both: the old sentence).
  The two settings buttons part 11 added show a toast when the device has no
  such page instead of crashing. `onDestroy` also dismisses an open pair
  dialog; its dismiss listener stops the device search a second time, which
  `stop()` ignores.
- `app/build.gradle.kts` passes the catalog and `strings.xml` paths to the
  unit tests as system properties, declared as task inputs like
  `:protocol`'s. The new `ViewerBarTest` checks the bar's ids, texts and
  descriptions against both files, and that every catalog device action is on
  the bar exactly once.
- Verification (rerun after the review's fixes): aapt2 2.20 compiled and
  linked the resources against android-37 (min 27, target 37) without
  warnings. kotlinc 2.3.20 compiled `:protocol` and `:peer` with `-Werror`;
  kotlinc 2.4.10 compiled 25 of the 26 app sources without warnings
  (`ShizukuActivation.kt` needs Shizuku, which is not cached), with
  stand-ins for `BuildConfig`, `ShizukuActivation` and the scrcpy `Server`
  and cached androidx older than pinned (core 1.19.0, activity 1.9.1,
  annotation 1.10.0, coroutines 1.9.0). The app's unit tests ran 17/17 that
  way with the real kotlin-test-junit, and `:protocol`/`:peer` 94/94. The
  bar tests fail on a copy that encodes the old bar and on a catalog with
  changed labels. The build-script lines were checked with system Gradle
  9.8.0, using the `java` plugin in place of AGP. In review, the SDK's
  standalone lint 8.2.0 reported the same as on the base, and AGP 9.3.2
  (offline, on a minimal stand-in module with the same build-script lines)
  passed both properties to the unit-test process. Contrast figures are
  computed by alpha compositing. Not run: this project's Gradle build with
  its pinned AGP and lint (`abortOnError`), the debug or release build, or
  anything on a device (rendering, blur, insets, TalkBack).
- Left alone: the stats overlay over a white screen (4.35:1); a held bar
  button in the panel (3.18:1); four other settings `startActivity` calls
  (accessibility twice, Wi-Fi, installing unknown apps) are unguarded; the
  nearby-pairing progress and confirm dialogs aren't dismissed in
  `onDestroy`; inline text on lines not touched; for voice control, "Vol-"
  is read as "Volume down" and "Refresh" as "Keyframe";
  `ViewerActivity.perform()` runs only the Keyboard and Screenshot local
  actions, so any other local action put on the bar would do nothing; the
  unit tests get absolute paths, as `:protocol`'s do, so Gradle's cache keys
  depend on where the checkout is.

## Update (2026-10-20: QoL overhaul, part 12: browser sound start and the http fallback)

- Sound starts from any tap, click or key, not only a press on the picture,
  including a touch's end (pointerup or touchend), which is when phones count
  a tap as a gesture. The listeners sit on the window in the capture phase,
  leave while sound plays, and return if the browser suspends or interrupts
  it.
- One start at a time: overlapping starts each built an AudioContext and the
  first was never closed. A failed start closes its context, and only a later
  gesture tries again, never an audio packet. Audio that arrives while the
  context is not running is dropped rather than queued, so sound does not
  start about 240 ms late.
- Over plain http from another machine there is no AudioWorklet (it needs a
  secure context), so the page plays through a ScriptProcessorNode fed by the
  same ring code: `pcm-worklet.js` also loads as a page script, in 2048-frame
  blocks, with a little more delay. Before, the start threw and the page
  stayed silent while the HUD showed a volume.
- A sound chip at the top right shows only while there is something to do:
  "Tap for sound" (starts it), "Sound off · Alt+M" (a tap unmutes, since
  phones have no Alt), a note about the compatibility path (fades after 6 s; a
  tap explains it), or "No sound in this browser" (stays until tapped). It is
  a live region and does not fade with the HUD.
- The HUD's volume reads "device vol NN%", "device vol muted", "device vol —",
  "audio off" or "audio error"; "audio 47%" read like this browser's volume.
  The `gain` op's comment in `webinput.go` now says it sets the host's sound
  output (the PulseAudio sink), not the browser's volume.
- Verification: `go vet ./...` and `go test ./...` passed. Node suites: input
  110/0, worklet 25/0, pointer 17/0, conn 61/0, page 56/0, and the new audio
  132/0, which loads every script `index.html` lists into one scope against a
  stub Web Audio. The stub's plain-http page keeps the AudioWorkletNode
  constructor but gives the context no `audioWorklet`, as Chromium does, and
  a second case drops the constructor too, as Firefox does; choosing the path
  by the constructor alone fails 13 checks. Against the old files, audio fails
  73 checks and worklet 2. In the builder's tree, removing each of 12
  behaviours from `player.js` failed at least one check. In both reviews,
  headless Chrome 155, Firefox 157 and WebKitGTK 2.54.1 drove the page from a
  scratch server with the page's CSP and synthetic 48 kHz audio: on an
  insecure origin all three took the compatibility path and started on a
  tap's end or a key; on localhost all three used the worklet (WebKit in the
  second review only); WebKit reports a context made before any gesture as
  "interrupted", which the broadened resume starts; every engine ran at
  48000 Hz. Not run: a phone,
  Safari or iOS (`webkitAudioContext`), sound heard on real speakers, the
  compatibility path under a busy main thread.
- Known gaps, for a later web part: a start that keeps failing leaves "Tap for
  sound" up and builds and closes a context on every tap and key; the context's
  sample rate is not checked (a browser that ignores the 48 kHz request would
  play slow); until the session ends the chip, like the toolbar, cannot be
  reached with Tab, which goes to the device; "Sound off · Alt+M" stays over
  the picture's top-right corner while muted, so a tap there unmutes instead
  of reaching the device; on narrow screens a toast covers the chip; after a
  session has ended a key still starts the sound and the chip still offers
  it (on plain http with the 6 s compatibility note), with nothing to hear.
- Left alone: the `gain` op clamps -1 ("no change") to 0, which mutes, and
  `{op:"gain", mute:false}` without a `gain` mutes a sink that is not muted
  (the missing gain reads as 0); the ring counts underruns unevenly and never
  re-buffers after starving; the README and the `webui.go` header describe
  only the AudioWorklet path.

## Update (2026-10-20: QoL overhaul, part 11: Android readiness, notification, permission help)

- Readiness badges use short labels: "Helper not running", "Too many
  viewers", "Serving not ready" for any other not-ready answer, and "Peer
  refused the check" (the refusal used to be the label). The full sentence is
  the detail shown when the badge is tapped. A `Readiness.State` enum replaces
  matching on English labels. The not-ready sentences come from shared
  constants, so the probe text on the wire, which the Go client and older apps
  read, is byte-identical.
- The serving notification no longer changes text on every check: a re-check
  keeps each device's last result, and only a device never checked shows
  "Checking…". There is no summary line without paired devices, "Disconnect
  all" appears only while a device is connected, and the wording matches the
  main screen ("Listening on HOST:PORT", "NAME controlling" or "NAME
  viewing").
- Help lines with a button to the right settings page: notifications blocked
  while serving (Notification settings), local network access denied for good
  (App info), and on Android 13 and later a hint under "Enable remote input…"
  about Allow restricted settings (App info). They are re-checked on resume.
- From the part-5 review: only one pair dialog is open at a time (the previous
  dialog and its device search are dismissed first); a session that connects
  while the viewer is being destroyed is now closed, since `onConnected` is
  posted with `runOnUiThread` rather than to the handler `onDestroy` empties;
  and the allow-back grant is a pure function with a JVM unit test.
- Verification: no Gradle or lint here (offline, and the pinned plugin and
  libraries are not cached), so CI is the first real build. aapt2 (build-tools
  36.0.0) compiled and linked the resources and manifest against android-37.
  Kotlin 2.4.10 (the pin is 2.4.20) type-checked every app source against
  stubs and cached androidx; `Readiness.kt` and `PairingGrants.kt` compiled
  with `-Werror`, and their tests passed 11 of 11. Three new expectations fail
  with the old `Readiness.kt`. Not run: a device, the notification, the
  settings pages and the permission flows.

## Update (2026-10-20: QoL overhaul, part 10: grouped help, scrcpy flag names)

- `scterm -h`, `--help` and `scterm help` print the same help on stdout:
  flags grouped as Device, Video, Audio, Display, Peer and Debugging, with
  placeholders (`-s SERIAL`, `--max-size PIXELS`), each default once, and
  sections for commands, environment variables and examples. It used to go to
  stderr, sorted alphabetically, with some defaults twice and "PCI" for PCM.
- A flag mistake is one line ending "run scterm -h for the commands and
  flags" (exit 2) instead of the error and all 61 lines of usage.
- scrcpy's names work: `--serial`, `-n`/`--no-control`, `--no-audio`,
  `--no-video`, `-m` (`--max-size`) and `-b` (`--video-bit-rate`). Bit rates
  take K and M, as in `8M`, `1.5M` and `128K`. Plain values must be decimal
  digits: `-1`, `0x10`, `010` (octal 8) and `1_000` were accepted before, and
  values over 2147483647 are now refused because the server reads an int. `0`
  still means the server's default.
- `--web` or `--window` with `--no-tui` exits 2 like other usage errors (it
  exited 1). `--mirror-fps` is hidden from help; it still parses, and says it
  does nothing and will be removed.
- A leftover word that names a flag gets "did you mean --takeover?", also
  for `NAME=value`, ahead of the quoting hint; one-letter words are skipped.
  The cost: an unquoted name that contains a flag word, such as `--peer living
  room window`, gets that hint.
- adb's `detached` state says to run `adb -s SERIAL attach`, and
  `TestSubcommandHelp` sets `SCTERM_HOME`. Both are from the part-4 review.
- Verification: `gofmt`, `go vet ./...`, `go test ./...`, a `-race` run of the
  command-line tests, and a build all passed. Ten of the new or extended tests
  fail on the old code, run through a scratch shim. Real runs of `-h`,
  `--help`, `help`, `--bogus`, `-b 12x`, `--web --no-tui`, `--peer Pixel
  takeover` and `version` gave the expected output and exit codes; `version`
  prints the same bytes as before. Not run: a device, or peer mode.

## Update (2026-10-20: QoL overhaul, part 9: terminal action bar and two-press Quit)

- The click that wakes the hidden action bar never runs a button. Without
  hover reporting, a first click on the bottom rows used to press whatever
  was under it (POWER at 120 columns, Quit at 200); it now reaches the device
  as the tap it would have been without the bar.
- ‹ and › turn the bar by the buttons actually shown instead of 8, so every
  button can be reached at every width from 16 to 140 columns (at 80, Grab
  never showed). A left arrow counted twice also hid buttons at 16-17 columns.
- Turning mouse grab off hides the bar: with reporting off, no click could
  reach it and no motion could say the pointer had left, so it stayed up.
- Quit in the bar, the Alt+/ menu and the on-screen keyboard takes a second
  press within 2 s. The first press relabels it "Quit?", "Press again to
  quit" or "QUIT?" (in the same cells, so nothing moves), and any other press
  drops it. Alt+Q, and Ctrl-Q in the menu, still quit at once.
- Local volume is kept in whole percent: Alt+- steps read 90, 80, 70 (they
  read 89, 78), and Alt+M unmutes to the volume from before the mute instead
  of 100%. Test fixtures that build an audio sink by hand now set it to 100%
  like `newAudioSink`.
- A left press while a drag is still down (its release was lost, for example
  let go outside the window) lifts the device finger first, then counts as a
  new click; an on-screen keyboard click in that state used to become a device
  tap. The finger is lifted at the new press position, since the last one is
  not tracked.
- The on-screen keyboard and the menu ignore mouse buttons 8 to 11: button 8
  pressed keyboard keys and ran menu rows.
- Verification: `gofmt`, `go vet ./...` and `go test ./...` passed, and a
  `-race` run of the bar, menu, keyboard, quit and volume tests (60 passed).
  Nine of the ten new tests fail with the old production files;
  `TestQuitChordsQuitAtOnce` pins unchanged behaviour and passes with both.
  Not run: a real terminal or a device.
- Left alone: `mouseEvent` still masks the button byte with 0x7f, so buttons
  8 to 11 act as left, middle and right on the video and on the bar;
  unmuting through the browser's `gain` op still restores 100%; the README's
  Alt+M row does not say unmute restores the earlier volume.

## Update (2026-10-20: QoL overhaul, part 8: browser connection states)

- The page says what it is waiting for whenever it is not showing live video:
  a card over the dimmed last frame reads "Connecting…", "Waiting for video
  from <device>…", "Connection lost. Reconnecting…", or "Session ended" with
  the reason and a Reconnect button. Before, the state lived only in the HUD,
  which hides after 2.5 s, so a lost connection looked like a frozen picture
  and an ended session looked like an endless reconnect.
- Each viewer's socket now ends with `{"op":"bye","reason":...}` as the last
  message before the close frame (code still 1000), written under one lock
  hold so no frame or status can come between: "Quit from the browser." for
  the Quit button and Alt+Q, "The device stream ended." when the video stream
  ends in `--web` or `--window`, and "scterm stopped." for signals and server
  errors. When the last `--window` viewer leaves, nobody is left to read one.
  Additive: `web/check.mjs` ignores unknown ops.
- The page stops retrying on a bye. Reconnect starts again without a reload,
  and the card reads "Reconnecting…" at once, even while the ended socket is
  still closing, so a late frame on it cannot make the session look live. The
  button takes the focus when it appears. From the bye on there is no device
  to send keys to, so the page leaves every key to the browser: Enter or Space
  presses the focused button, Tab and Shift+Tab move between the page's
  buttons and back, F5 reloads as on any page, and a key typed between the bye
  and the close is not sent. Esc still closes the controls sheet.
- A later `stop()` waits, for up to 5 s, until the first has finished. The app
  loop calls it on its way out of the process, and returning at once let the
  process exit before a Quit from the browser had written the byes or closed
  the `--window` browser; adb mode's 200 ms pause in `sess.stop()` usually hid
  it, and `--peer` has no such pause. The byes go to all viewers at once, so
  one that stopped reading delays only its own.
- HUD: the device name stays (each status message replaced it with the
  resolution) and names the page title (`<device> · scterm`); the fps reading
  says "idle" after 2 s without a painted frame, also while disconnected;
  decode errors and lost frames turn the state red for 5 s instead of until a
  reload (lost frames used to turn it yellow, and only past 30); the
  resolution shows the video size before the first frame instead of 0×0.
- Known gap, not changed here: `writeJSON` marks a client closed after a
  failed write, so the later `close()` leaves its socket open and the client
  registered.
- Verification: `gofmt`, `go vet ./...`, `go test ./...` and `go test -race
  ./...` passed, and the bye tests 30 times under `-race`. Node suites: input
  110/0, pointer 17/0, worklet 17/0, conn 61/0, page 56/0. `web/page_test.mjs`
  runs the whole `player.js` against a stand-in DOM built from `index.html`;
  each of 22 one-line mutations of the page wiring fails it or `conn_test`.
  Against the old `web.go`, `webinput.go` and `app.go` the bye tests fail (no
  bye before the close). Against this part's first reviewed round, a second
  `stop()` returned at once, the page and card suites failed 7 and 5 checks,
  and a status landed between the bye and the close frame (that test races
  writers against the stop, so it catches the old code in most runs, not
  all). Against the second round's `player.js`, the page suite fails 4 checks
  (a key sent after the bye, Tab kept off the button, Enter on the picture,
  Esc from the button); dropping the ended check, moving it above the sheet's
  Esc, or keeping `ended` across Reconnect each fail it. Headless Chrome 155,
  driving the real page against a fake socket: Tab off Reconnect and
  Shift+Tab back, Enter presses it, keys reach the new socket, Esc closes the
  sheet from it. Not run: layout, fades, reduced motion, a screen reader, a
  device, `--window` or `--peer`; the stream end was simulated with a pipe.

## Update (2026-10-20: QoL overhaul, part 7: AVX2 flag on amd64 only)

- `-mavx2` was passed to the C compiler on every architecture, so an aarch64
  build failed at once (clang: unsupported option) although `native.c` has a
  NEON path. It is now an amd64-only cgo flag; amd64 builds are unchanged and
  still need a CPU with AVX2.
- Verification: `go list` shows `-mavx2` in the amd64 cgo flags and not in the
  arm64 ones. `native.c`, compiled for aarch64 with the Android NDK's clang and
  arm64 sysroot (FFmpeg headers from the host), fails with the old flags and
  builds with the new ones, and the object holds NEON code. A full linux/arm64
  build needs arm64 FFmpeg and PulseAudio libraries and was not run.
  `go vet ./...`, `go test ./...` and the amd64 build passed.

## Update (2026-10-20: QoL overhaul, part 6: terminal status row, colours, overlay padding)

- The status row is painted on its own as soon as it changes: grab, mute and
  volume changes no longer wait for a video frame (a static device screen may
  send none), and the fps reading, which changes twice a second, no longer
  forces a repaint of the whole screen. A shorter status erases what a longer
  one left behind.
- Each frame goes out as one synchronized update (DEC mode 2026, ignored by
  terminals without it).
- Cell colours use the full range: the three bits kept per channel are spread
  over 0..255 (white was 224, and every colour came out darker).
- Overlay rows are padded by display cells, not bytes: '·', '‹' and '›' left
  old cells showing at the end of the row.
- With no usable picture (before the first frame, with `--video=false`, or
  after a resize until the next frame) the overlay and the status still paint
  on a cleared screen, so the menu, the keyboard and the bar work; a stale
  snapshot is still never sheared into the new grid.
- Verification: `go vet ./...` and `go test ./...` passed; the six new tests in
  `tui_render_test.go` fail against the base `tui.go`. Not looked at in a real
  terminal.

## Update (2026-10-20: QoL overhaul, part 5: Android pairing, viewer Back, allow-back grant)

- `MainActivity` handles its own configuration changes, like the viewer, so
  rotating or unfolding no longer ends a pairing that is comparing codes;
  `alert()` refuses a dead window, the nearby flow no longer dismisses a dead
  dialog, and `onDestroy` stops the device search and its multicast lock.
- Viewer: Back leaves while connecting or after a session ended (it sent the
  remote Back to nothing); the overlay offers Cancel while connecting, Close
  after.
- "Also let it see, hear and control this device" grants view, audio and
  control, like the invite dialog's defaults; it used to share the clipboard.
- Verification: not compiled here (the offline Gradle cache lacks the pinned
  toolchain); CI on the draft pull request builds, tests and lints it. Not
  checked on a device.

## Update (2026-10-20: QoL overhaul, part 4: the command line fails fast)

- Startup no longer hangs (a bad `--audio-codec` used to hang silently): the
  server process exiting ends the wait at once and its last output is shown,
  error and exception lines rather than stack frames; a server that never
  connects is given up after 15 s, counted across all the sockets (the first
  accept had no limit). Once adb has exited, waiting for anything it left
  holding the output pipe stops after 1 s. `--audio-codec`/`--audio-source`
  are checked against the bundled server's lists, with `--peer` too, and all
  three streams off is refused before any device work (it panicked).
- adb: a missing adb, unauthorized, no permissions (with or without adb's
  udev hint), offline and transient or mode states are each named with the
  next step.
- Words left after the flags are refused (exit 2) instead of starting a
  mirror: a command given after flags, a typo of one (with a suggestion),
  `--audio false` (the hint says `--audio=false`; it used to mirror with
  audio on) and an unquoted `--peer Pixel 8` (the hint says to quote it).
  `scterm help` prints the help on stdout, every command answers `-h` or
  `--help` with its usage, and `update` and `version` take no other
  arguments (`update --help` used to update). Usage errors exit 2, `peers`
  with a bad argument included (it exited 1).
- `forget` needs a name (a bare `forget` removed the only pairing) and
  confirms a partial one, refusing when stdin is not a terminal.
  `interactive()` checks for a terminal (`/dev/null` used to pass), so
  `--peer=` with several pairings and no terminal asks for a name instead of
  prompting.
- Verification: unit tests, the command-line ones with the race detector,
  and the built binary run with no adb on PATH and with a fake adb: each case
  ends with its reason, at once except the 15 s no-connection timeout.
  Reverting the permissions, wait or stack-frame fix fails its new test.

## Update (2026-10-20: QoL overhaul, part 3: terminal keyboard, screenshots, crash restore)

- On-screen keyboard: K, L and M sent L, M and R (now checked against
  `keycodes_gen.go`); the cursor started on ROTATE, so Enter rotated the phone
  (it starts on ENT and is drawn); `act()` cleared the flash its callers set
  (it lasts `kbFlashDuration`, cleared on the tick); the system-key row is
  split so every row fits 80 columns; only a real left press acts (drag motion
  and wheel notches pressed keys), a click between keys stays on the keyboard,
  and a drag begun on the video ends on the device (it left a finger down).
- Alt+S and `--dump-frames` wrote the BGR0 canvas as RGB, swapping red and
  blue; pixels are now written R, G, B in one write.
- A panic on a terminal-UI goroutine restores the terminal (normal screen,
  mouse reporting off, cooked mode) before it carries on.
- Verification: the keycode, row-width and colour tests fail on the base
  code; a probe on the base showed a wheel notch pressing a key, a drag
  release leaving the touch down and the cursor on ROTATE.

## Update (2026-10-20: QoL overhaul, part 2: one press per key in the browser)

- Keys sent through `sendKey` (the plain keycode keys such as Backspace,
  Enter and the arrows, Ctrl+letter, and Ctrl or Alt with a keycode key)
  reached the device twice: a key op went out on keydown and again on keyup,
  and the server turns each key op into a full press (Backspace deleted two
  characters, Ctrl+A selected all and then typed "a"); held, those keys never
  repeated. Now one op per keydown, auto-repeats are forwarded as presses,
  and the key-up sends nothing, except for PrintScreen: Windows delivers only
  its key-up, so a key-up with no key-down before it is the press. That press
  goes without modifiers, as the key-up op always did. Bare, Shift and Alt
  F-keys, Alt chords, Esc and typed text already behaved and are unchanged.
- Bare modifier and lock keys are no longer sent. On Windows and Linux a lock
  key went out on both edges and cancelled itself. On macOS a Caps Lock tap
  is one event (key-down when it turns on, key-up when it turns off), so the
  device's caps lock followed the host's; it now stays as it is. What the
  device did with that state is untested; typed letters reach it as
  keycodes with their own Shift state (`webinput.go` `asciiKey`). A bare
  Meta tap (two Meta presses, and more around every Cmd chord) has no
  replacement.
- The `web/player.js` and `web/input_test.mjs` comments that the part 1
  entry lists as still open now name `actions.go`.
- Verification: `node web/input_test.mjs` 110 passed. Against the base
  `player.js` six new cases fail (one press for Backspace and for Ctrl+A,
  repeats, bare keys, PrintScreen down and up, Shift+PrintScreen down and
  up). The PrintScreen key-up-only case failed against the first version of
  this change, which dropped that press, and the modified key-up-only case
  fails against the second, which sent the modifiers. The Go web tests
  pass.

## Update (2026-10-20: QoL overhaul, part 1: shared action catalog)

- Branch `overhaul` from `62d6cfa`; one independently reviewed commit per
  change, pushed as each passes, with a draft pull request to `main`. Scope:
  quality of life and looks across the web player, terminal UI, command line,
  Android app and docs, from read-only surveys of each area at `62d6cfa`.
- Owner decisions: Quit from the action bar, menu or controls sheet needs a
  second press within about 2 s, Alt+Q still quits at once, Ctrl+C keeps going
  to the phone and the quit hint stays visible; Android polish uses framework
  widgets only (no new libraries); when the browser has no AudioWorklet (plain
  http from another machine) the player falls back to an older audio path and
  says so; no license chosen yet, so none is added.
- This commit finishes the unfinished catalog work from the main checkout's
  `feat/qol-features` working tree (applied byte-identical; that checkout is
  untouched): `protocol/fixtures/actions.json` v2 (kind, label, short, views)
  drives the Go chord, keycode, F-key, menu and bar tables (`actions.go`) and
  one dispatch path for the terminal, the browser dispatcher and the app-less
  web server the tests build (main refuses `--web --no-tui`). The webBar view
  records the web toolbar's order only: `web/index.html` still keeps its own
  list and labels. Finished: the keymap test no longer names the removed
  constants, `TestActionDispatchIsTheGoldenWire` pins every device action to
  Android's published keycodes and the hand-derived wire fixture, and the
  per-action app helpers left without callers are removed.
- Review round 1 (NEEDS CHANGES) resolved: the app-less dispatcher refreshes
  the device volume after a failed write again, as before; new dispatcher
  tests cover that refresh, the inject-key report and a missing controller
  (each fails when its behaviour is removed); a missing controller is refused
  before the debug log; the loader folds chord case, requires visible ASCII
  chords and lets only Back's down/up pair repeat a control name (the Android
  client sends each listed message); the comments in this commit's files that
  called the web toolbar a catalog view, placed the chord table in
  `appkeys.go` or named a `--web --no-tui` dispatch path are corrected
  (`menu.go`, `actions.go`, the catalog's `about`, `webinput.go`,
  `Actions.kt`); the test-only `headlessKey` and its test are replaced by a
  test that drives all three dispatchers and compares wire bytes.
- Still open from that review: comments in `input.go`, `web/player.js` and
  `web/input_test.mjs` still place the chord table in `appkeys.go` (the
  commits that change those files correct them), and `web.go` and
  `web_e2e_test.go` still describe a `--web --no-tui` mode that main refuses.
- Verification: `go vet ./...`, `go test ./...` (node suites ran, not
  skipped) and `go build -trimpath` passed; mutating the mute keycode to 91 in
  a scratch copy makes the golden test fail. Gradle not run (the offline cache
  lacks the pinned toolchain); the Kotlin catalog test parses the fixture
  generically and its v1 fields are unchanged, so CI covers it.

## Update (2026-10-13: paired-device readiness, part 3)

- Base `34cfc2b`: part 2 independently reviewed PASS, committed and pushed
  origin/main. Scope: CLI opt-in checks, viewer retry and owned media cleanup,
  active outgoing-session readiness, verified metadata/contact polish.
- `scterm peers --check`: separate presence hints, pinned five-second checks
  with two workers, deterministic output, verified metadata only, offline
  listing unchanged. No stream or takeover. Store updates do not recreate a
  forgotten record or rewrite unchanged metadata.
- Viewer Retry uses latest saved address and existing viewer settings, waits
  for old sockets/audio/decoder release, guards all old attempt callbacks and
  frames, and never requests takeover. Immediate-close TLS regression proves
  target session/input/lease cleanup before a new connection.
- Active outgoing viewers supply Connected/Watching status; incoming sessions
  remain separate. Notification taps scroll to paired devices. Last successful
  contact remains accurate in memory even after a failed check; persistence
  stays coalesced. Probes report live renames. Numeric endpoints bypass the
  bounded DNS worker pool.
- Verification: `go vet ./...`, `go test ./...`, `go test -race ./peer`,
  `go test -race . -run TestPeers`, and `go build -trimpath -o
  /tmp/scterm-paired-status .` passed. Regression fixed a caller-slice race
  exposed by CLI race tests, and authenticated rejection now wins over a
  failed spoofed discovery candidate.
- Pinned Java 17/Gradle 9.7.1 protocol/peer/app tests, lintDebug, assembleDebug,
  peerctl assemble and assembleRelease passed (25s, /tmp/scterm-final-gradle.log).
  Local release APK is unsigned because no signing key is configured. An
  initial duplicate isClosed property compile failure was corrected.
- Live CLI smoke: /tmp/scterm-paired-status paired with Kotlin peerctl target
  at loopback, plain peers listed offline, peers --check printed Ready and
  stored verified metadata. Earlier Go/Kotlin interop passed stream/input too.
- Physical checks not run: no phone/RP6 in adb devices. MediaCodec/AudioTrack,
  rendered UI, NSD/Wi-Fi transitions and notifications still need both-device
  testing. Pairings on user devices were not changed; no APK installed.
- A full rerun exposed an existing revocation-reason race: the connection
  handler closed raw TLS while administrative teardown was still sending Bye.
  Before correction, the unchanged revocation test failed 7/100 current runs
  and 1/100 baseline `34cfc2b` runs. Deferred socket ownership now preserves
  Bye/linger cleanup; an exception fallback still closes the socket. A gated
  production regression fails with baseline TargetServer classes and passes
  with final classes. After correction, revocation passed 100/100 iterations.
  Evidence: /tmp/scterm-current-revocation-repeat.log,
  /tmp/scterm-baseline-revocation-repeat.log, /tmp/scterm-fixed-repeat.log,
  /tmp/scterm-fixed-gate.log and /tmp/scterm-baseline-gate.log. These are JVM
  loopback results, not hardware evidence. Assertions remain unchanged.
- Independent final review /tmp/scterm-review-3 returned NEEDS CHANGES:
  Android retained a discovery identity mismatch over a later authenticated
  peer rejection. Production fallback policy now prefers authenticated
  revocation/forbidden/older-peer replies, with regression coverage. Added a
  production cleanup-barrier test with delayed audio/decoder and failed-audio
  paths, following the reviewer's nonblocking suggestion; delta
  review before commit. Final verdict recorded in commit body before push. No release tag or publication has been requested.

## Update (2026-10-13: paired-device readiness, part 2)

- Base `5f7c4f9` (part 1 independently reviewed PASS, committed and pushed
  to origin/main). Scope: Android monitor, directional readiness badges and
  details, resolved presence TXT hints, existing serving notification.
- Monitor runs on main-screen/serving lifecycle only, uses two workers,
  15/60-second cadence, closes in-flight sockets on cancellation, rejects late
  tickets, and checks pinned candidate/saved addresses within a five-second
  peer budget. Metadata writes are coalesced, and forgotten records never
  recreated. Presence hints are read after resolution, not from discovery.
- Peer rows update in place; diagnostics distinguish the two viewing directions.
  Notification updates only when summary text changes and never alerts again.
- Verification: pinned Gradle protocol/peer/app tests, lintDebug, assembleDebug
  passed (17s, /tmp/scterm-status-gradle.log). Readiness tests cover classification,
  controlled-clock freshness, and generation invalidation/forget/reordering.
  Fixed initial Android callback return-type compile error before rerun.
- Device checks not run: adb devices empty; host adb lacks mDNS command support.
  Real Android NSD/network lifecycle and rendered UI remain unverified on device.
- Independent review of /tmp/scterm-review-2 found periodic cancellation could
  starve later devices. Periodic polls now preserve pending tickets/queue;
  manual/network/lifecycle refresh still cancels. Added an eight-slow-peer,
  two-worker controlled scheduling regression. Delta review before commit.
- Next: CLI --check, viewer resource-safe one-tap retry, active-session status.

## Update (2026-10-13: paired-device readiness, part 1)

- Base: main `4669ee9`, fast-forwarded from `072f084`; worktree was clean.
- Added optional pinned mutual-TLS `probe` / `probe_result` v1 handshake,
  side-effect-free backend status, and separate serving-presence DNS-SD.
  Probes never call obtainBackend or create streaming sessions/leases.
- Shared fixtures and TLS tests cover readiness, empty grants, revoked/unknown
  peers, occupied control, mismatched identities, malformed/older replies and
  Go cancellation. peerctl exposes readiness for the interop test.
- Verification: `go vet ./...`, `go test ./...`, `go test -race ./peer` passed;
  `ANDROID_HOME=/opt/android-sdk android/gradlew --no-daemon -p android
  :protocol:test :peer:test :app:lintDebug :app:assembleDebug :peerctl:assemble`
  passed with Java 17 and pinned Gradle 9.7.1. An initial test compile warning
  (unnecessary non-null assertion under -Werror) was corrected before rerun.
- Physical checks: not run; `adb devices` lists none. Live Go/Kotlin interop
  subsequently passed before UI monitoring. No release or APK installation performed.
- Live `TestInteropWithKotlinTarget` passed against peerctl (paired/probed,
  h264 stream and injected touch); a host JVM target, not physical evidence.
- Independent review of /tmp/scterm-review-1 returned NEEDS CHANGES: synchronous
  DNS could escape Kotlin timeout. Added bounded DNS worker/caller waiting
  with blocked-lookup and cancellation regressions; delta review follows.
- Review snapshot: generated under /tmp/scterm-review-1; independent review
  required before commit/push. Review outcome recorded in commit body.
- Next: Android monitor/badges/notification, then CLI checks and viewer retry.

## Update (2026-09-30, session 7: landscape viewer)

- Viewer: in landscape the video fills the screen and `ControlsPanel` floats
  the button row in a dialog window (the video is a SurfaceView layer that no
  view can blur; Android 12+ blurs behind a window). The blur needs
  `windowIsTranslucent` and a radius set after the window is attached,
  cleared before dismissing. The panel is not focusable, so keys stay with the
  viewer, but that layers it over the keyboard: opening the keyboard hides it.
  It starts hidden (it covers where games put touch controls), comes up from
  the handle, and hides after the set seconds without touches on it. Main
  screen *Viewer* card: always show the controls, the delay, always show the
  statistics (`ScTermApp.viewer*`).
- A black picture with working audio and touch happened once on the phone and
  was not reproduced in portrait or landscape starts. Likely cause: a decoder
  left waiting for a keyframe after its one request fell inside the viewer's
  1 s throttle (scrcpy's natural keyframes are 10 s of frames apart). The
  viewer now re-asks every 2 s while `VideoDecoder.waitingForKeyFrame`, and
  the statistics say "Waiting for a keyframe".
- Updates: a build not signed with the release certificate
  (`BuildConfig.RELEASE_CERT_SHA256`; `-Pscterm.releaseCert=` empties it for
  debug-key update tests) explains instead of offering a release.
- Device notes: the Red Magic sets `log.tag=S` at boot, so apps log nothing;
  `adb shell setprop log.tag D` lasts until reboot. Rotate device cannot
  override an app that locks its orientation.

## Update (2026-09-29, session 6: nearby pairing for scterm, releases)

- Released v2.0.0 (signed APK + Linux binary); CI moved to Node 24 actions
  and ubuntu-26.04 (setup-gradle stays on v5: v6's caching is under Gradle's
  separate terms).
- scterm: `scterm pair` with no arguments finds inviting devices (DNS-SD
  `_scterm._tcp`, `peer/discover.go`: legacy-unicast query plus a shared
  multicast listener, via `golang.org/x/net` dnsmessage/ipv4, the module's
  first dependency) and runs nearby pairing (`peer/nearby.go`, same
  derivation as the app; `peer/nearby_test.go`: shared vectors, a scripted
  target for agree/decline/skipped-confirm/swapped-nonce/cancel, answer
  parsing). `--peer` takes the start of a name or of a word in it, and asks
  which device when several match (`peernearby.go`, `Store.Matches`).
- App: peer rows refresh what the other device allows on every connection;
  the empty Nearby list says what to do on the other device.
- Updates: the app checks GitHub's latest release (`update/AppUpdates.kt`;
  `BuildConfig.UPDATE_URL`, `-Pscterm.updateUrl` for tests), verifies the
  APK's digest, package, version and signing certificate, and installs it
  through `PackageInstaller` (`InstallResultReceiver` shows the confirmation
  and, after `MY_PACKAGE_REPLACED`, an "updated" notification). Verified on
  the Retroid with debug builds and `devicetest/update_server.py`: a real
  update (0.0.2), "latest version", and the refusal of a release-signed APK
  over a debug build. `scterm update` (`update.go`, `internal/release`):
  release binaries download, verify, test-run and replace themselves; a
  source build pulls and rebuilds its checkout. `scterm version`.
- Tooling: macOS's tmp cleanup removes files under /tmp and the sandbox
  cache after a few days. Go and Gradle now live in
  ~/Library/Caches/scterm-tools (fetched with curl: Java does not trust this
  network's TLS proxy, so the Gradle wrapper cannot download). A Gradle
  "Could not find ... GradleWorkerMain" means a purged worker jar: stop the
  daemon and delete `caches/<version>/workerMain/workerMain.lock`.

## Update (2026-09-27, session 5: nearby pairing, Shizuku start)

Session 4's work is committed on `codex/android-client` (6763091, afcd41b,
58765f0; draft PR #1, CI green). **This session's work is uncommitted:**

- Nearby pairing, so nothing is typed between devices (`PEER_PROTOCOL.md`,
  "Nearby pairing"). While an invitation is open the serving device
  advertises `_scterm._tcp` (`util/Nearby.kt`, tied to the invitation by
  `TargetServer.Events.onInvitationChanged`); "Pair with a device…" lists
  what it finds; both screens show a six-digit code (`ShortCode`:
  commit/reveal, numeric comparison) and both users confirm. New messages
  `pair_nearby`, `code_commit`, `code_nonce`, `code_reveal`, `code_confirm`;
  reject `declined`; vectors under `nearby` in `protocol/fixtures/pairing.json`
  (computed in Python). Target: one request at a time, only while inviting,
  60 s to answer, declines/hang-ups count as failed attempts. Requests show as
  a dialog, or a heads-up notification with Accept/Decline when the app is not
  on screen. The invitation dialog closes itself once the invitation is used,
  and has Stop inviting. `peerctl pair-nearby HOST[:PORT] [--confirm]`.
  Tests: `NearbyPairingTest` (10, incl. a fake target that skips confirmation
  or swaps its committed nonce) and two vector tests in `PairingTest`.
- "Start with Shizuku" for the helper (`ShizukuStarter`, `ShizukuActivation`)
  and a specific not-ready refusal ("X isn't ready: its full-control helper is
  not running…") instead of "Serving is not ready".
- Verified on devices (phone Android 16 as controller, Retroid Pocket 6
  Android 13 as target, same Wi-Fi): the Retroid's advertisement is visible
  (also from the Mac with `dns-sd -B`), the phone lists it, both screens
  showed the code, both confirmed, both stored each other with the invited
  grants (view, hear, control), both invitation dialogs closed, and the
  advertisement was withdrawn. Covers both NSD resolve paths (API 34+ and the
  legacy one).
- Start with Shizuku verified on the Retroid (Shizuku server running as
  shell): the helper connected with no computer involved.
- Peer rows now name who can do what ("You can see its screen…", "It can't
  connect to your device."): the old "Cannot connect to this device" (meaning
  the peer has no grants here) read as a failure to reach the peer.
- Also verified on the two devices: a decline on the Retroid reached the
  phone within 1.5 s without the phone confirming ("Retroid Pocket 6
  declined."), with the invitation left open; Cancel on the phone closed the
  Retroid's request dialog by itself (toast "Pairing failed: Red Magic 9s
  cancelled pairing"); each attempt showed a fresh code on both screens; Stop
  inviting withdrew the advertisement; the phone then connected to the
  Retroid (1920×1080, hardware low-latency decode) and disconnected cleanly.
- Not yet verified on devices: the time-out (JVM test only), the
  notification path (request while the app is not on screen), the new
  not-ready message on the phone. The Go client pairs with typed codes only.
- Devices left: the phone ↔ Retroid pairing, the Retroid serving with its
  helper (started through Shizuku), no invitation open, phone stay-awake off.
- Releases now attach a signed APK (`scterm-android-<version>.apk`) next to
  the Linux binary: `v*` tags (and manual runs, without releasing) build
  `:app:assembleRelease` with `-Pscterm.version` (2.0.0 → version code 20000)
  and the key from secrets `SCTERM_KEYSTORE_BASE64` / `SCTERM_KEYSTORE_PASSWORD`
  (PKCS12, alias `scterm`, RSA 4096, valid to 2054, certificate SHA-256
  `8bdcc97a…cc3dca76`). The keystore also lives outside the repo on the
  maintainer's machine, password in the macOS keychain. Without
  `SCTERM_KEYSTORE`, local release builds stay unsigned; dev builds are
  version `dev`, code 1. The R8 build was smoke-tested on the phone (debug key,
  data kept): launch, identity, peer list, nearby browsing, no crashes.

## Update (2026-09-24, session 4: first physical device runs)

Supersedes the "no device" statements below. Uncommitted at the time
(committed since; see session 5).

- Device: RedMagic NX769J, RedMagic OS 11.0.5, Android 16 /
  SDK 36, gesture navigation, 60/90/120 Hz. This ROM suppresses logcat
  (`log.tag=S`); crash evidence comes from `adb shell dumpsys dropbox --print data_app_crash`.
- Verified on it: install and launch; helper activation and streaming;
  projection capture with accessibility input; pairing (typed, pasted, and
  `scterm://` link via intent); grants, lease, takeover, live revocation,
  Disconnect everyone, Stop serving (sessions get their reason, app survives);
  serving-mode choice persists. Viewer role against the helper (self-loop) and
  against `peerctl serve` on the Mac through `adb reverse`: hardware
  low-latency decode (`c2.qti.avc.decoder.low_latency`), pixel-exact touch
  mapping in portrait and landscape, rotation and letterboxing, screenshots,
  keyboard, a 20 s continuous drag, 120 Hz while viewing.
- Found on the device and fixed:
  1. Crash: revocation/kick/stop wrote to sockets on the main thread
     (`NetworkOnMainThreadException`). `TargetServer` now does all of that on
     a `peer-admin` executor; `TargetService.stopServing` tears down off-thread.
  2. SELinux denies shell→app abstract-socket `connectto` on Android 16: the
     helper (`HelperMain`) relays scrcpy's sockets over loopback TCP with a
     256-bit token (`HelperHandshake`).
  3. Joiners after an encoder pause got the stale session/config
     (`BackendSink.videoPaused`, `MediaFanout.onPause`).
  4. The viewer's action bar covered the bottom of the video, and sat inside
     the gesture-navigation zone (horizontal swipes switched apps): bar now
     below the video, padded by the mandatory gesture inset.
  5. Keepalive starvation: pings were sent only after 5 s without input, so
     continuous input ended healthy sessions after ~15 s ("stopped
     responding"). Pings now keep their own schedule (Kotlin and Go);
     regression test `continuousInputDoesNotStarveTheKeepalive`.
  6. Smaller: fps readout averaged over 2 s; screenshots use `IS_PENDING` and
     delete failed writes.
- Latency pass (Moonlight-style; unit/lint clean; the helper was re-run on
  the device with the new options: High profile 864×1920, first frame 358 ms
  after connect, Opus audio, all frames decoded): low-latency encoder keys for the helper
  (`video_codec_options`) and projection encoder (no B-frames, Qualcomm
  low-latency mode); media sockets get small kernel send buffers (64 KiB
  video, 8 KiB audio) so backlog reaches the fanout's age limit, now 250 ms;
  decoder vendor low-latency keys and skip-ahead after 8 queued frames;
  unbuffered touch dispatch; fastest refresh rate while viewing. The Go
  decoder already used `AV_CODEC_FLAG_LOW_DELAY` and one thread.
- New dev tools: `peerctl serve` (a JCodec test-pattern target that draws and
  logs every touch; `TestPattern.kt`), `android/devicetest/ui.py`,
  `android/devicetest/activate_helper.py` (fills a fixed template with only a
  validated port/token/scid read from the screen).
- Go: new pure-Go package `peer/` (identity, invitations, frames and
  envelopes, pairing proofs, client session with scheduled pings, peer
  store). `go test -race ./peer` passes (shared fixtures, fake target), and
  `TestInteropWithKotlinTarget` passed against `peerctl serve`: pairing with
  both proofs, session, 576×1280 video packets, and a Go-sent tap logged by
  the Kotlin target at the exact centre (288,640).
- B04 written (not yet compiled with cgo or run end to end): `peermode.go`
  adds `scterm pair|peers|forget`, `--peer NAME` (`--peer=` for the only
  paired device) and `--takeover`; a peer-backed `session` feeds the unchanged
  TUI/web/window pipeline (adb-only features were already nil-guarded).
  Type-checked on macOS with stubs for the cgo app; `peermode_test.go` and
  the `peer` tests run in CI. The workflow gained `workflow_dispatch` (push
  and PR triggers cover only `main`) and builds `:peerctl`.
- Environment: in the agent sandbox `GRADLE_USER_HOME` is a
  `/var/folders/.../cursor-sandbox-cache/.../gradle` cache, and Gradle must
  run unsandboxed (its file-lock service opens a socket).
- Phone-only batch (later the same day), all on the same phone with
  `peerctl` controllers over `adb forward` and the Mac test pattern over
  `adb reverse`:
  1. The helper survives a physical USB unplug (same process; streams after replug).
  2. Rotating the phone gives a viewer new sessions per orientation, in
     helper and screen-capture modes.
  3. Swiping scterm out of Recents while serving (task removed, verified):
     process, foreground service, helper and the live session all survive.
  4. A viewer joining a static screen-capture screen gets one header and its
     first keyframe 280 ms after connecting, as a viewer (lease elsewhere).
  5. Rejoining after the encoder paused: exactly one fresh header (fix 3 holds).
  6. Not testable here: a system-initiated capture stop. This ROM shows no
     status-bar stop control, and `dumpsys media_projection` prints null even
     while capturing.
  7. Force-stopping the phone's viewer mid-drag: the target injected the
     release at the last position in the same millisecond as the disconnect.
  8. Release build (R8, not debuggable; signed with the local debug key for
     the test only, then replaced by the debug build): helper, video + Opus,
     viewer decode and exact input all work.
  9. Screen off and wake during a session: the session and helper persist
     (299 frames in 20 s); one RTT sample rose to 39 ms while the screen was off.
  Also seen: reinstalling the app ends the helper (its relay socket closes);
  the phone moved networks mid-session (hotspot to 192.168.1.x), and new
  invitations picked up the new address.
- Two-device session: the phone (Android 16) and a Retroid Pocket 6 (Android
  13, QCS8550) on the same Wi-Fi, both serving through the helper in turn.
  - Pairing works both ways. On the Retroid, uiautomator cannot read dialogs
    (Retroid's Game Assistant accessibility overlay); `ui.py` falls back to
    `--windows` for activities, and dialogs were driven from screenshots.
  - Phone viewing the Retroid: 1920×1080 at 59–60 fps with a game running,
    RTT 16–22 ms, hardware low-latency decode. Retroid viewing the phone:
    hardware low-latency decode on Android 13, rotation 864×1920 ↔ 1920×864,
    Home from the Retroid's bar reaches the phone.
  - Audio bug found and fixed: the helper used scrcpy's default `output`
    (REMOTE_SUBMIX) capture, which silenced the serving device for as long as
    the helper ran (the user had to reboot the Retroid), and the Retroid's
    captured stream was silent too (cause unknown; the log went to
    /dev/null). Now Android 13+ uses `audio_source=playback audio_dup=true`,
    the vendored scrcpy's playback capture also matches USAGE_GAME and
    USAGE_UNKNOWN (marked `scterm patch`), and the helper logs to
    `/data/local/tmp/scterm-helper.log`. Verified: the Retroid keeps its sound
    while serving, its game audio streams at 127 kbps, and both directions
    play on both devices, the viewer slightly behind.
  - The helper needs activating again after every reboot (expected).
  - Latency not measured yet: "show taps" dots are left out of screen
    recordings and captures on both devices. Next approach: a touch marker
    drawn by the viewer itself, timed against a visible reaction on the target.
  - Left in place for the user: the phone ↔ Retroid pairing. Removed: the Mac
    test controllers.
- Phone restored after testing: accessibility service off, auto-rotate on,
  stay-awake off, no adb forwards/reverses, no paired peers, not serving
  (helper process gone), test screenshot deleted, `log.tag.*` properties
  cleared again, Mac targets stopped. The
  phone clipboard still holds a used, expired invitation code.

## Snapshot (2026-09-24, session 3: implementation started)

- Branch `codex/android-client`, base `b30d96062f1c0f866e0fd745be35494a7df33276`
  (HEAD and `origin/codex/android-client` matched at session start; one worktree,
  on macOS; clean tree).
- User instruction: parse the docs and start implementing the native Android
  server/client app; push back on the plan where it can be better; fix bugs and
  performance problems found on the way.
- **All work below is uncommitted** at the time of writing; no review has been
  performed; nothing is pushed. Resolve commit/publication state with Git.
- No physical device, emulator or system image was available (`adb devices`
  empty), so **no device behavior is verified**. Every device exit criterion in
  the plan stays open.

### Environment inventory (A00)

| Item | Observation |
| --- | --- |
| Host | macOS (darwin 27.0.0), Apple Silicon arm64, 8 cores, 16 GB |
| JDK | Temurin 17.0.20.1 at `/Library/Java/JavaVirtualMachines/temurin-17.jdk` (not on PATH) |
| Android SDK | `~/Library/Android/sdk`: had platform 34, build-tools 34.0.0, NDK 26.1, cmake 3.22.1, cmdline-tools 12.0; AGP installed platform 37 (rev 2) and build-tools 36.0.0 |
| Gradle | not installed; 9.7.1 distribution downloaded to `/tmp/scterm-tools`, SHA-256 `acd53f1e…d20a` verified against services.gradle.org, used directly because the wrapper's own download failed Java TLS validation in this agent environment (a TLS-inspecting path whose CA Java does not trust; Google Maven and Maven Central worked). Your `./gradlew` should work normally. |
| Go | not installed; go1.27.1 darwin/arm64 downloaded to `/tmp/scterm-tools/go2`. The main package needs cgo (FFmpeg, PulseAudio) and `term_linux.go`, so Go was run only on isolated copies of pure files. |
| Node, Docker | Node absent; Docker CLI present, daemon not running. `web/*.mjs` suites not run (no JS changed). |

### What this session built (paths)

- `android/`: Gradle 9.7.1 wrapper (checksum pinned), AGP 9.4.1 (built-in
  Kotlin), Kotlin 2.4.20, compile/target SDK 37, min SDK 27, JDK 17. Modules:
  - `:protocol` (pure Kotlin): scrcpy v4.1 media/control/device codecs with
    strict bounds, peer handshake/envelope schema, pairing (Crockford base32,
    SPKI fingerprints, invitations, HMAC proofs), shared action catalog.
  - `:peer` (pure Kotlin): TLS identities and trust managers, `TargetServer`
    (pairing, handshake, grants, input lease, input release, clipboard
    routing, revocation), `MediaFanout` (keyframe-aware, slow-viewer
    isolation), `ControllerClient`/`ControllerSession`, `FilePeerStore`.
  - `:scrcpy-server`: the vendored server compiled unchanged (only
    `BuildConfig.VERSION_NAME = "4.1"` supplied), kept by R8.
  - `:app`: Keystore identity, `TargetService` (foreground service, local
    Stop/Disconnect-all, Wi-Fi lock), `ProjectionBackend` (MediaProjection →
    H.264), `RemoteInputService` (accessibility input), `HelperBackend`
    (ADB-activated vendored server), `ViewerActivity` (MediaCodec surface
    decode, AudioTrack audio, touch/mouse/keyboard/IME input, action bar,
    lease UI, screenshots), `MainActivity` (serve, invite, pair, manage peers).
- `protocol/fixtures/`: `scrcpy_wire.json` (hand-derived golden bytes for all
  23 control types, device messages, fixed point, media framing),
  `pairing.json` (vectors from an independent Python implementation),
  `actions.json` (one action catalog for Go/JS/Android).
- `docs/PEER_PROTOCOL.md` (new), plan section 2a (new), this file.
- Go: `protocol.go`, `control.go`, `appkeys.go`, `input.go`, `keyboard.go`,
  `webinput.go`; tests `protocol_fixtures_test.go`, `control_text_test.go`,
  `actions_catalog_test.go` (new) and `web_test.go`, `web_e2e_test.go`,
  `web_keymap_test.go`, `actionbar_test.go` (91 → 164).
- `.github/workflows/build.yml`: Android job added (unverified on GitHub).

## Current task ledger

States follow plan section 1. "Host-verified" means JVM/isolated tests only.

| ID | State | Evidence / what is missing | Next action |
| --- | --- | --- | --- |
| A00 | IMPLEMENTED_UNVERIFIED | Repo reconciled, tool inventory above; no device inventory possible; no measurement recipe yet | Nominate source/receiver devices; write the latency recipe |
| B00 | IN_PROGRESS | Source inspection done: vendored server launch identity, `InputManager` privilege, control-thread `AssertionError` on unexpected types (so peers are validated first). `TargetBackend`/capability/refusal contracts defined and implemented. Physical spikes not run | On a device: activation command, SELinux shell→app abstract socket, projection capture, accessibility streaming gestures, helper death/reboot |
| A01 | NOT_STARTED | G09/G13 decided early (below) | Matrix and reproductions |
| A02 | IN_PROGRESS | Shared action catalog fixture with Go and Kotlin tests; Go/JS generation not done; web input G02/G03 not fixed in Go | JS catalog test; Go per-controller input state |
| A03 | NOT_STARTED (Go) | Target-side equivalent exists in `android/peer` `MediaFanout` | Go packet subscriptions for the bridge role |
| A04 | IMPLEMENTED_UNVERIFIED | Peer protocol v1 implemented and JVM-tested over real TLS (pairing, MITM pin, wrong code lockout, single use, grants, lease, revocation, token binding, protocol errors); Go side absent | Go implementation (B04); device run |
| A05 | IMPLEMENTED_UNVERIFIED | Debug APK builds; decoder/viewer code compiled and linted only | Install on a physical receiver: static join, rotation, surface recreation, network loss |
| B01 | IMPLEMENTED_UNVERIFIED | Same APK serves and views; foreground service; local Stop/Disconnect-all; denial/revocation/binding tests pass on JVM | Device run; NSD discovery optional |
| B02 | IMPLEMENTED_UNVERIFIED | Helper built into APK (dexdump: `Server.main`, `VERSION_NAME "4.1"`, kept by R8); `HelperBackend` bridge written | Physical activation and full-parity checks |
| B03 | IMPLEMENTED_UNVERIFIED | Projection encoder + accessibility input written; capabilities and per-message refusals explicit; no audio capture yet | Device run; playback capture (API 29+) |
| B04 | NOT_STARTED | Now small: TLS dial + handshake feeding existing `stream.go`/`control.go` | Implement `AndroidPeerSource` in Go |
| A06 | IMPLEMENTED_UNVERIFIED | AudioTrack low-latency player (Opus/AAC via MediaCodec, raw PCM), 80 ms cap | Measure underruns/latency on hardware |
| B05, A07, A08, A09 | NOT_STARTED | A07 partly present (action bar, IME, mouse); A09 partly (CI job) | — |
| X01 | SUPERSEDED | Replaced by B00–B05 | — |

### Gap register changes

| ID | State | Change |
| --- | --- | --- |
| G07 | IMPLEMENTED_UNVERIFIED | Go no longer prints device clipboard text (length only, under `SCT_DEBUG_CONTROL`); Android never logs it |
| G09 | IMPLEMENTED_UNVERIFIED | Decided: mute = 164 in Go (F7, action table, on-screen keyboard), catalog and Android; physical confirmation pending |
| G13 | IN_PROGRESS | `actions.json` pins Go (`TestAppActionsMatchSharedCatalog`) and Kotlin; JS not yet pinned |
| G14 | IN_PROGRESS | Android controller sends real multi-touch (helper backend); browser unchanged |
| G15–G18 | IN_PROGRESS | Addressed in design and JVM tests (backends, grants, lease, input release, no input relay); physical evidence missing |
| G01–G06, G08, G10–G12 | NOT_STARTED | — |

New defects found and fixed this session (Go):

| ID | Defect | Evidence |
| --- | --- | --- |
| D1 | `scrollMsg` wrapped `int16(1.0*32768)` to -32768: scrolls of 16+ notches went the wrong way | Isolated run before fix: `scroll(16) = -32768, want 32767`; passes after |
| D2 | `serializeControlMsg` sliced a fixed 256-byte buffer: any injected text over 251 bytes panicked, and the web `text` op has no recover (a long CJK paste could crash scterm) | Found by `TestInjectTextSplitsAtUTF8Boundaries` (panic); fixed with exact sizing, plus 300-byte UTF-8-safe chunking |
| D3 | `parseDeviceMessage` did not know UHID output (type 2): consumed 1 byte, desynchronized | Isolated run before fix: `uhid_output: consumed 1/6`; passes after |
| D4 | Device clipboard printed verbatim to stderr (G07) | Output of the same run showed `scterm: clipboard: "hé"` |

## Evidence (commands and results)

Android (`cd android`, `JAVA_HOME` = Temurin 17, Gradle 9.7.1 as above):

- `:protocol:test`: 33 tests, 0 failures (golden wire, hostile input, pairing, peer JSON shape).
- `:peer:test`: 31 tests, 0 failures (7 fanout, 18 loopback-TLS sessions, 6 policy); 8 consecutive reruns stable after two test-timing fixes.
- `:app:testDebugUnitTest`: 4 tests, 0 failures.
- `:app:assembleDebug`: success (3.5 MB). `:app:assembleRelease`: success with R8 (393 KB unsigned).
- `:app:lintDebug` (checks `:protocol`/`:peer` at min SDK 27 too): 0 errors, 0 warnings after fixes.
- `aapt2 dump badging`: package `io.github.pwnedbygary.scterm`, target 37, permissions as declared.

Go (isolated copies of `protocol.go` + `control.go` + fixture tests; stubs for
`stderrWriter`/`isClosedConnErr`; and verbatim-extracted `appkeys.go` tables):
`go vet` clean; `go test -race` 5 tests pass; catalog test passes; `gofmt -l`
clean on every edited Go file. **Not run**: the full `go test ./...` (needs
Linux cgo), so the four updated web/actionbar tests (91 → 164) are unexecuted
here; CI on Linux will run them.

Physical checks performed: none.

## Next eligible actions

1. Review the uncommitted change set; commit in scoped pieces (Go fixes,
   fixtures, `android/` modules, Go `peer/`, docs, CI); run CI.
2. B04: run CI on this branch (`gh workflow run build.yml --ref codex/android-client`
   after pushing) to compile and test the cgo main package with peer mode;
   then pair scterm on Linux with the phone and run TUI, `--web` and
   `--window` over `--peer` (helper and screen-capture backends).
3. Latency measurement recipe (click-to-photon) on two physical devices;
   decide on datagram transport (plan A08) with that evidence.
4. A03 for the Go bridge role; JS catalog pin (G13); A01 matrix.

## History

- 2026-09-22: plan written (commit `be4594e`), scope revision 2 (dual-role APK,
  direct peering, Go as authenticated client) committed as `22fa13c`;
  `.gitignore` change `b30d960`. Baseline checks then: `go test ./...` passed
  with socket permissions on Linux (`ok scterm 7.591s`); Node suites input
  100, pointer 17, worklet 17 passed. No device work.

## Work record template for future updates

```text
Task/gap ID:
Engineering state:
Owner/session and last update:
Branch/worktree and base SHA:
Invariant / intended behavior:
Claimed paths / non-overlap boundaries:
Changed paths (including untracked):
Completed work and source evidence:
Commands, environment, exact results and artifact paths:
Physical checks performed / not performed:
Review snapshot, reviewer, verdict and unresolved findings:
Code commit SHAs (if already known):
Publication state and evidence (or unverified):
Blocker / assumptions / next eligible action:
```
