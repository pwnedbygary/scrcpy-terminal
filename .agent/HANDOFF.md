# Android peer app / Go client / parity development checkpoint

Read [the complete plan](../docs/history/ANDROID_CLIENT_PLAN.md) (section 2a lists the
design revisions adopted on 2026-09-24), [the peer protocol](../docs/PEER_PROTOCOL.md)
and applicable `AGENTS.md` instructions first (none exist in this repository or
its parents as of this snapshot). Reconcile this checkpoint against Git,
current source, live task ownership and test evidence before editing. Status
here is a snapshot, not proof that no other work has started.

## Update (2026-10-21: QoL overhaul, part 26: loose ends)

- `--audio-dup` now does what scrcpy's does (v4.1, `cli.c`): without
  `--audio-source` it asks for playback capture, so the sound keeps playing
  on the device (Android 13 and later), and it is refused with another
  source ("--audio-dup needs --audio-source playback, not mic: only playback
  capture keeps the sound playing on the device") or with `--no-audio`,
  exit 1 before any device work. Before, with the default `output` source it
  did nothing on the device and said nothing. As in scrcpy, `--web
  --audio-dup --audio-source output` is now refused. In peer mode the rule
  does not apply: a peer device gets neither flag.
- On Android 11 and 12 the server turns playback capture off, so
  `--audio-dup` alone now gives no sound in the terminal display (it used
  to play on this computer) or in the browser display (it used to play in
  the browser and on this computer); the sound stays on the device. scterm
  says why: when the device turns its audio stream off (codec id 0), it
  prints `scterm: no sound from DEVICE`, then the server's last warning or
  error line about audio when there is one, and "(--audio-dup implies
  --audio-source playback, which needs Android 13 or later)" when
  `--audio-dup` chose the source. The terminal display shows the line on
  the status row and once in the exit replay, and its status row then says
  `audio off`; `--web`, `--window` and `--no-tui` print it on stderr. The
  session goes on without sound. scterm waits up to 1 s for the server's
  line, which comes over adb's shell stream, not the audio socket. A peer
  target also sends codec id 0 for a stream it turns off, so the same line
  appears there, without a reason. The README says so; the help names only
  the implied source and Android 13 or later.
- Without PulseAudio, `--web` and `--window` say "scterm: audio: browser
  only; no sound on this computer: REASON" and then only the address; they
  used to print "audio disabled", a line about `--audio-dup` that was not
  true, and "video only", though the browser still plays the sound. With
  `--no-audio` they print no line about where the sound plays (it suggested
  `--audio-dup`, which `--no-audio` now refuses outside peer mode).
- `--dump-audio` with `--audio-codec raw` writes the packets as sent, before
  the volume scaling; it wrote nothing. The encoded path writes the same
  bytes as before, through the same helper.
- `scterm peers` with no pairings also offers plain `scterm pair` on the
  same network.
- Questions go to stderr, with their numbered choices, the nearby pairing
  code and the newline that ends an unanswered question, so a redirected
  stdout no longer hides them (`scterm update`'s question, `forget`'s
  confirmation, the `--peer` chooser, nearby pairing). Answers are still
  read from the terminal; results and status lines stay on stdout.
- Stale comments and notes: `main.go`, `appkeys.go` (Ctrl+Q with the
  on-screen keyboard open goes to the device), `webui.go` (the sound buffer
  starts at about 96 ms), `exit_test.go`, `web/player.js` (`--window` ends
  3 s after the last page leaves) and `android/devicetest/ui.py`. The Alt+K
  notice without control now says "Refresh video needs control, and this
  session is view only" (it said "control disabled (no -control)"), and the
  `--window` note no longer says there is no DISPLAY when there is one.
- New pins for part 10's review gaps: the defaults `scterm -h` shows, the
  one-letter-word skip in the quoting hint, and K and M bit rates.
- This closes the part-21 entry's items on "video only", `--audio-dup`,
  `--dump-audio` with raw, `scterm peers` without pairings and the stale
  comments, the part-18 entry's `web/player.js` note, and part 24's question
  printed on a redirected stdout.
- Review round 1 found that on Android 11 and 12 `--audio-dup` now gave no
  sound and no message; that the snapshot's base did not isolate this part;
  test gaps (`--web` and `--window` without `--audio-dup`, the other
  sources); the README's peer-mode sentence against the refusal, and the
  routing line with `--no-audio`; and this entry's wording. Round 2 adds
  the "no sound" line, the peer-mode exception and the tests, drops the
  routing line with `--no-audio`, corrects the README and this entry, and
  is reviewed alone, on top of part 25.
- Verification. Round 1, in its builder's tree (HEAD then, with every part
  in the worktree): gofmt, `go vet ./...`, the build, `go test ./...` (the
  six node suites included), `-race` on the whole run and 5 times on the 30
  touched or related tests; 12 behaviour tests failed on the base files
  (through an overlay) and the three pins passed there, as pins should; 26
  mutations were each caught. Round 2, in its builder's tree (`8641ad7`
  with every part then in the worktree, round 1 included): the same checks
  (`-race -count=3` on 30 tests: 90 of 90). New tests:
  `TestNoSoundFromTheDeviceIsSaid`, `TestSoundThatStartedIsNotSaidToBeOff`,
  `TestNoSoundFromTheDeviceSaysWhy`, `TestNoSoundWaitsForTheServersReason`
  and `TestNoSoundFromTheDeviceIsPrinted` (new `audiooff_test.go`), and
  `TestWebWithoutSoundSaysNothingOfWhereItPlays`;
  `TestAudioDupNeedsThePlaybackSource` gains `--web`, `--window`, the ten
  other sources and the peer cases. On round 1's code they fail as
  expected, and the pins of unchanged behaviour pass. The builder's 20
  mutations are caught, and so are the review's B, C, E and G, and two
  forms of its A (A as written no longer applies); D (raw packets trimmed
  to whole 4-byte frames, close to a no-op for the test's packets) and F
  (the pairing code back on stdout, which no test reaches) survive. Binary
  runs up to argument handling (fake adb, scratch HOME): exit statuses and
  output as before, except the three `--peer` cases; `-h` and `version`
  byte-identical. Round 2 was merged three ways onto the worktree (one
  README conflict, where part 29 changed the line after round 2's, resolved
  by keeping both). For review, this part was rebuilt alone on `e0919d4`
  (with part 25) by replaying both rounds onto that commit's files (one
  README conflict there too, resolved by keeping round 2's lines and that
  commit's wording of the next bullet); with the other uncommitted parts
  of the time replayed on top, that gave the worktree's
  files exactly (for the README, a line count showed the difference was
  exactly parts 27, 28 and 29's lines). On that snapshot, with iso.env:
  gofmt, vet, the build,
  `go test ./...` (327 top-level tests pass and 3 skip; the six node suites
  ran), `-race -count=3` on the touched tests (141 passes over 47 tests, no
  races), `-race` on the whole module, round 2's tests 20 times each, and
  the docs checker on the README. Not run: a device (the implied playback
  source on Android 13, and on 11 and 12), a peer target, a desktop without
  PulseAudio, a browser or a window, a real terminal with stdout
  redirected, the pairing-code lines (no test reaches them), or a raw dump
  played back. The 1 s wait for the server's line is a judgement, not a
  measurement.
- Left alone: the X fallback note is wrong when DISPLAY is set but its
  socket is missing, and that check hard-codes `/tmp/.X11-unix`; `scterm
  pair` keeps its status lines on stdout; `closeAudioDump` is never called
  and dump write errors are ignored; `android/devicetest/ui.py` defaults
  ADB to the macOS SDK path; the browser page's sound chip has no state for
  a device that sends no sound (the line goes to stderr); the peer target's
  welcome carries its audio error (`audioError`), which the Go client does
  not read, and a peer target that grants no audio says nothing while the
  status row shows a volume (older than this part); an audio-only terminal
  session goes on after the device turns its sound off; the reason is read
  with `lastLines`, which can return a line still being written (not
  tested; judged unlikely); no test has a raw packet whose size is not a
  multiple of 4 (mutant D).

## Update (2026-10-21: QoL overhaul, part 25: browser viewers share frames safely)

- With two or more browser viewers, every viewer that took a video frame put
  its buffer back in the pool when it was done with it, so one buffer could
  go back more than once. It could then be handed out twice, with two
  senders writing into it at once, or while a JPEG viewer was still encoding
  from it, and a page could get a damaged picture (the part-18 entry's
  `-race` probe found the races in `sendFrame`; this part's load test also
  saw payloads that were not the frame's). Now each frame counts its holders
  (`videoFrame.refs`, the only change in `stream.go`): `frame()` holds it
  while it hands it out, each viewer's mailbox takes its own hold in
  `offer()`, and `releaseFrame()` puts the buffer back only when the last
  hold goes. A viewer lets go once its sender has encoded or copied the
  frame (before the socket write, as before, so a slow socket keeps no frame
  buffer out of the pool) or refused it (too short for its canvas, or with
  no encoder to be had for it), when a newer frame replaces it in the
  mailbox, or when the viewer closes with it still waiting. `frame()` still
  allocates nothing and never waits.
- The viewer count (`nClients`) is stored inside the locked section that
  changes the list, in `handleWS` and `closeWith`, so two viewers coming or
  going together can no longer leave it stale.
- A viewer whose WebSocket upgrade finishes after `stopWith` has taken its
  list used to be added to the list anyway, so it got no bye and its socket
  stayed open until the process exited. `stopWith` now records its bye
  (`stopBye`, under `clientsMu`, in the same locked section that copies the
  list), and `handleWS` closes such a viewer at once with that bye and then
  the close frame, so its page shows "Session ended" with the reason. A
  viewer that finishes its upgrade before the list is taken, while the stop
  is still in `http.Shutdown`, is on the list and gets the bye as before.
- `quit()`, which the `--window` leave timer calls, now waits for room in
  the app's event queue, or for the server to stop, instead of dropping the
  quit when the queue is full; a dropped quit left the session running with
  no window.
- This closes the shared-buffer, stale-count, late-viewer and dropped-quit
  items the part-18 entry left alone.
- Verification, in the builder's tree (part 17, and part 18 as first
  reviewed; part 18's code has not changed since): gofmt, `go vet ./...`,
  the build, `go test -count=1 ./...`, and `-race -count=3` on the web, bye,
  second-stop, WebSocket, window-kill and encoder tests (47 tests, 141
  passes, no races); the seven new tests 20 times each, plain and under
  `-race` (140 of 140 each). New tests: `web_share_test.go`
  (`TestWebSharedFrameGoesBackOnce`, with a frame sent, a frame too short
  for its canvas and, from round 2, a frame no encoder can be opened for;
  `TestWebHeldFrameStaysOutOfThePool`, `TestWebFrameAllocatesNothing`,
  `TestWebSharedFramesUnderLoad`, and, from round 2,
  `TestWebRawSenderCopiesBeforeLettingGo`) and, in
  `web_session_test.go`, `TestWebViewerCountFollowsTheList`,
  `TestWebLateViewerGetsTheBye` and `TestWebWindowEndsWithAFullEventQueue`.
  Seven fail on the base files on behaviour; `TestWebFrameAllocatesNothing`
  passes there too, as a guard (the base allocated nothing either). The
  deterministic tests hold a real lock at a chosen point, with no test
  hooks: the encoder's lock or a viewer's `cfgMu` stops a sender inside or
  before its work on the frame, its socket's `wmu` stops
  it after it is done with the frame, and a viewer's `frameMu` stops
  `frame()` before it reaches that viewer; the late-viewer test holds a
  viewer's `wmu` to hold the stop, and runs `handleWS` through a fake
  hijacker. The load test (three JPEG viewers and one raw viewer at 480×1080,
  200 frames) compares every payload byte for byte with the raw pixels or
  one reference JPEG, which holds because the JPEG encoder gave the same
  bytes for the same pixels in 50 encodes (a probe), so it also catches
  damage inside the C encoder that `-race` cannot see; on the base files it
  failed in 10 of 10 processes with `-race` and 10 of 10 without, and the
  count test in 20 of 20. Of the builder's 13 mutations, 12 are caught: the
  no-encoder path's release, which round 1 of this entry called untestable,
  fails the round-2 case (a 70000×2 frame is wider than libavcodec's JPEG
  encoder takes, so no encoder opens; its buffer went into 3 pool slots),
  and only recording the bye in its own locked section just after the list
  copy survives (that the bye is published with the list is reasoned, not
  tested). The review's mutation that lets the raw sender go before it
  copies the frame fails `TestWebRawSenderCopiesBeforeLettingGo` under
  `-race` in 8 of 8 processes (a race report, and the sent frame showing
  the test's write) and passes without `-race` in 8 of 8, since the copy is
  done before the test writes; the code as it is passes that test under
  `-race` in 8 of 8 with no race report. Round 2, on `8641ad7` with this
  part: gofmt, vet, the build, `go test ./...` (the root package: 258
  top-level tests pass, 1 skip), `-race -count=3` on the same selection
  (144 passes over 48 tests, no races), and the eight new tests 20 times
  each, plain and under `-race`. Round 3, on `33d89a8` with this part:
  gofmt, vet, the build, `go test ./...` (311 top-level tests pass and 3
  skip; in the root package 284 pass and one skips), `-race -count=3` on
  the same selection (144 passes over 48 tests, no races), and the eight
  new tests 20 times each, plain and under `-race`. Against HEAD's `web.go`
  and `stream.go` seven of them fail and `TestWebFrameAllocatesNothing`
  passes; the no-encoder mutation fails only the round-2 case; the review's
  raw-sender mutation fails in 8 of 8 processes under `-race`, each with a
  race report, and the code as it is fails in none. Round 4, on `f1c25f3`
  (part 21's docs commit, which changes none of this part's files) with
  this part: gofmt, vet, the build, `go test ./...` (312 top-level tests
  pass and 3 skip; the one more is part 21's README test; in the root
  package 285 pass and one skips) and `-race -count=3` on the same
  selection (144 passes over 48 tests, no races).
  Not run: a real browser or device, a real `--window` session, and
  the late viewer against the real `http.Server.Shutdown` (the test
  completes the upgrade through a fake hijacker; its timing against
  `Shutdown` is reasoned). Frame rate, latency and CPU were not measured.
- Review round 1 found this entry calling the no-encoder path untestable,
  the locks the tests hold described wrongly, a test comment's premise
  wrong for a `bytes.Clone`d buffer (its capacity is rounded up, so a raw
  output could fit in it), and nothing catching a raw sender that lets go
  before its copy. Round 2 adds the case and the test above, builds that
  buffer at its exact size, and corrects this entry; the production code is
  as first reviewed.
- Review round 2 passed the code and the tests. It found the comment above
  `TestWebSharedFrameGoesBackOnce` calling all its viewers raw, though the
  round-2 case's are JPEG viewers, and this entry giving the root package's
  test count as that of `go test ./...`. Round 3 corrects both, takes its
  comment suggestions (the raw-sender test relies on `-race`; the file's
  header names the frame lock; the 65500-pixel limit is libavcodec's), and
  is rebuilt on `33d89a8`, as the round-2 snapshot's handoff predates
  part 22's entry.
- Review round 3 passed this part on `33d89a8`. Part 21's docs commit then
  landed with its entry where this one goes, so round 4 rebuilds this file
  on `f1c25f3`, with this entry above part 21's.
- Left alone, for later changes: `sendStatus` reads stream fields (the frame
  rate and the video size) that the demux goroutine writes without
  synchronisation, a race the web tests never reach because they run no
  stream; every JPEG viewer still encodes the same frame again (encoding
  once per frame would save CPU); `stopWith` does not wait for a refused
  late viewer's bye, so if the process exits at once that page may see the
  socket drop instead; a refused late viewer does not count as a viewer
  that connected (`hadClient`), so a `--window` whose first connection
  lands during a stop may be reported as having shown nothing; once the
  leave grace has run out, `quit()` waits for room in the event queue for
  as long as the app's loop is stuck (the queue fills after about 4 s), and
  a viewer that comes back meanwhile does not take the quit back, which a
  check of the list while it waits would; its `s.done` way out has no test.

## Update (2026-10-21: QoL overhaul, part 21: the docs)

- `README.md` is reorganised for a first-time user: what scterm is,
  Requirements, Install, Quick start, Displays (terminal, browser with a LAN
  warning, window), Controls (one table built from the shared catalog, then
  mouse and touch, typing, the on-screen keyboard, screenshots, sound on this
  computer), Peer mode, Updating, Troubleshooting, Reference (flags grouped
  as `scterm -h` groups them, commands, environment variables, exit status,
  files), How it works (a diagram with both the adb and the peer paths),
  Development and Credits. It no longer links this file.
- New docs: `docs/ARCHITECTURE.md` (the design and scrcpy protocol notes,
  with the encoding benchmark measured again on this host),
  `docs/DEVELOPMENT.md` (building, the checks, the six node suites, the
  test-only variables, `check.mjs`, `gesture.mjs`, `peerctl`; it links this
  file) and `docs/RELEASING.md` (CI, tags and versions, the signing secrets,
  backing up the keystore, rehearsing a release). The plan moved to
  `docs/history/ANDROID_CLIENT_PLAN.md` with a banner saying it is
  historical and its links fixed; this file's link follows it.
  `android/README.md` puts Build and Modules last and lists `peerctl`;
  `docs/PEER_PROTOCOL.md` records the two-device session and lists the
  `declined` reject code.
- Claims corrected against the code: the requirements (adb on PATH, Go
  1.27, a C compiler and pkg-config, Linux only, AVX2 on amd64 also when
  built from source, NEON compiled but untested on aarch64, no 32-bit
  builds, releases for linux/amd64 only, the Android versions sound and
  playback capture need); what `--audio-dup` does (nothing on the device
  with the default `output` source, while playback capture without it
  silences the captured apps); the LAN warning (no password and no
  encryption; the only check compares a WebSocket's Origin with the address
  the page was opened on, which a site that uses DNS rebinding passes, even
  at the default address, unless the browser blocks it); the Ctrl keys in
  each front end; `--peer --window` opening at a default size; the full
  repaint every 300 frames, not 24; screenshots (a PPM at terminal
  resolution in the working directory, a PNG download in the browser); the
  terminal menu having no Mute; the two-press Quit, and Ctrl+Q quitting at
  once in the terminal's Controls menu; the version going to stderr before
  the terminal UI starts; what `update` checks; `forget`; the Android
  local-network permission and helper label.
- `readme_test.go` (`TestReadmeListsEveryFlag`): the README's flag tables
  name every flag `newFlagSet` defines, each in one row, under the heading
  `scterm -h` puts it under (`--video`, `--audio` and `--control` under the
  heading of their `--no-` forms), with one dash for a one-letter flag and
  two for the others, and no other flag (`--mirror-fps` stays out). Rows
  inside HTML comments do not count, since they do not render; the Default
  column is not checked. Ten mutations of the README are each caught: a row
  removed, a fake flag, a wrong heading, `--mirror-fps` listed,
  `--control=false` dropped, the heading renamed, a row of its own for
  `--video=false` under Audio, `--web` listed again under Peer, a flag's
  only row inside a multi-line HTML comment, and `--s` written for `-s`
  (the first round's version of the test missed the last four). The old
  README fails it.
- The orchestrator brought the README up to the parts that land before it:
  the Zellij text and the Quit prompt on the bottom line (part 16), the exit
  status wording (part 17), closing the `--window` window ending scterm about
  3 s later with a reload keeping the session (part 18), the peer connection
  messages and the search for a moved device (part 22), pasting in the
  terminal and what the server can type (part 23), and `update`'s question,
  `--check`, `-y`, progress and `.old` (part 24).
- Verification: the builder's markdown checker (`/tmp/opencode/dev-21/
  mdcheck.py`, self-tested) finds no broken relative link or anchor (47), no
  code fence without a language and no line over 100 columns in the six
  docs, and the moved plan's links resolve (the same after round 2); gofmt,
  `go vet ./...` and `go test ./...` pass with the parts named above in the
  same tree, the six node suites running. The flag test's mutations ran in
  a scratch clone of 286d838 with this README
  (`/tmp/opencode/dev-21b/mutate-readme.sh`). Not run: a real terminal,
  browser or device.
- Review round 1 found two README claims that contradicted the code: that
  only release binaries need AVX2 (every amd64 build uses it, so a build
  from source stops the same way), and that the WebSocket check refuses
  pages of other websites (a site that uses DNS rebinding passes it). It
  also found `docs/ARCHITECTURE.md` waiting 15 s for each connection (one
  deadline covers them all, then 15 s more for the device name), a stale
  comment placed in the wrong file below, and the flag test claiming more
  than it checked. Round 2 fixes these, makes the flag test stricter, and
  takes the review's suggestions on the exit replay (the last 50 different
  messages), Ctrl+Q in the terminal's Controls menu, `pactl`,
  `XDG_RUNTIME_DIR`, the 3 s `--window` grace and the exit status under
  `--web`. Review round 2 found two sentences round 1 should have caught:
  `docs/ARCHITECTURE.md` said scterm moves its sound to the default sink
  only when that changes (the follower also moves it about a second after
  the stream appears), and `docs/DEVELOPMENT.md` left
  `SCTERM_TEST_SESSION_END` out of the test-only variables. Round 3
  corrects both and takes three README notes: Go reports a missing AVX2 as
  `SIGILL: illegal instruction` with exit status 2, `pactl` also chooses the
  output scterm starts on, and scterm itself finds PulseAudio through
  `XDG_RUNTIME_DIR` too. Review round 3 found round 3's sentence on what
  happens without `pactl` wrong: scterm gives libpulse an empty string, not
  NULL, for the default server and sink, libpulse rejects it, and so without
  `pactl` (unless `SCT_AUDIO_DEVICE` names a sink), and whenever no sink
  qualifies, the terminal display has no sound. Round 4 says so in the
  README and in `docs/ARCHITECTURE.md`. Review round 4 found an older clause
  of that `docs/ARCHITECTURE.md` sentence wrong too: scterm never prefers a
  running sound card (see "Left alone"); round 5 drops the clause.
- Decisions on the builder's questions: the README keeps "`--peer` has not
  been run against a phone yet", since no later record says otherwise;
  `docs/DEVELOPMENT.md` may link this file; whether `--audio-dup` should
  imply the playback source, and rebuilding the bundled server jar, are left
  for later changes (below).
- Left alone, for later changes: without PulseAudio `--web` says "video
  only", and the browser's status "audio error", though the browser still
  gets sound; the bundled server jar predates
  the game-audio patch, so playback capture over adb does not record game
  sound (rebuilding it needs the Android build); `--audio-dup` with the
  default source has no device effect and gives no warning (upstream scrcpy
  is recalled to imply the playback source; unverified); `--dump-audio`
  writes nothing with `--audio-codec raw`; `scterm peers` with no pairings
  suggests only typed pairing; stale comments (`main.go` says the version
  goes to the status line, `appkeys.go` (`keyboardInput`) leaves Ctrl+Q to
  "the global handler" to quit, though outside the menu Ctrl+Q goes to the
  device, `webui.go` says "~20 ms latency", `input.go` spells `-control`,
  and `window.go` prints a misleading note when DISPLAY is valid);
  `android/devicetest/ui.py` names a doc that does not exist; the browser
  cancels Ctrl+V's keydown, which may stop the paste event (untested); the
  historical plan keeps its 63 lines over 100 columns; the WebSocket check
  does not stop DNS rebinding (to be fixed in a change of its own); the
  browser pointer's hover ring, drag trail and touch ripple are not
  described; the readiness section of `android/README.md` still says
  "peer" and reads like implementation notes; `readme_test.go` still reads
  table rows inside an unclosed HTML comment, a `~~~` fence or indented
  code; the sound follower
  (`audiofollow.go`) starts with no sink recorded, so about a second after
  the stream appears it moves it to the desktop's default sink even when
  that is a Bluetooth or virtual sink the start-up choice passed over;
  scterm gives libpulse empty strings rather than NULL for the default
  server and sink (`audio.go`), which libpulse 17 rejects, so without
  `pactl` (unless `SCT_AUDIO_DEVICE` names a sink), or when no sink
  qualifies, the terminal display has no sound, and the first try, at the
  default server, always fails, which by the code costs about 0.9 s per
  candidate sink before the `XDG_RUNTIME_DIR` socket opens the stream; the
  stream therefore never uses `PULSE_SERVER`, though a comment in
  `audio.go` says it does, and without `XDG_RUNTIME_DIR` there is no sound
  even with `SCT_AUDIO_DEVICE`; and `audioDevices` reads a sink's state
  from the wrong column of `pactl list sinks short` (the sample spec before
  it contains spaces), so a running sound card is never preferred over a
  suspended one (a code change of its own for all of these).

## Update (2026-10-21: QoL overhaul, part 20: Android follow-ups)

- Contrast, computed by sRGB alpha compositing over a white picture: the
  viewer's statistics text is white (`#FFFFFFFF`, as the handle on the same
  background), 4.35:1 before, 5.74:1 now. A held bar button keeps the resting
  fill and lights a 1 dp `#80FFFFFF` outline instead of a lighter fill: a
  pressed fill of about 20% white or more falls under 4.5:1, and anything
  fainter hardly differs from the resting fill (about 1.1:1). Its label
  goes from 3.18:1 to 5.07:1 in the blurred panel, from 4.14:1 to 7.52:1 in
  the unblurred one, and from 5.77:1 to 11.98:1 on the opaque portrait bar;
  the outline stands out from the panel at 3.22:1 blurred and 4.32:1
  unblurred.
- The other settings launches (accessibility twice, Wi-Fi, installing
  unknown apps) go through `openSettings()`, which shows a toast where no
  screen answers. The nearby-pairing progress and confirm dialogs are
  dismissed in `onDestroy`.
- `android:textAllCaps="false"` is on `Widget.ScTerm.Button`, so the viewer
  overlay's Retry and Close no longer show in capitals; `MainActivity`'s own
  `isAllCaps = false` went, as the style's value wins over the text
  appearance. The bar buttons keep theirs (they use the framework's style).
- For voice control a bar button's name is its label when the label contains
  the shown text (ignoring case), otherwise "shown text, label": "Vol-,
  Volume down", "Vol+, Volume up" and "Redraw, Refresh video"; "Shade" stays
  "Settings shade". A test pins the bar's local actions to the two the viewer
  performs (Keyboard and Screenshot).
- From part 11's review: the restricted-settings line reads "If the switch is
  greyed out, tap it once, then open App info, tap the three-dot menu and
  Allow restricted settings, and turn the switch on." App info offers that
  menu item only after the switch has been tried: on Android 13 once the
  restricted-settings app-op is ignored, on Android 15 and later once the
  "Restricted setting" dialog has been shown (framework sources, read in
  review). The main screen's session lines use
  whole format strings ("%1$s (%2$s) controlling" and "… viewing") instead of
  joining the name and address in code; `PairingGrantsTest` asserts exactly
  view, audio and control; both busy rejects take their message from
  `Readiness.TOO_MANY_SESSIONS_REASON` (the wire text is unchanged);
  `ProbeTest` covers a target at `maxSessions` (not ready, "too many
  sessions", while full; a second session refused as busy; ready again
  after); a manual refresh with paired devices shows "Checking paired
  devices…".
- The subtitle says "paired devices", and "Copied" shows only below Android
  13, which confirms copies itself. The helper's build comment says the
  server is upstream plus the one change marked "scterm patch"
  (`AudioPlaybackCapture.java`, commit afcd41b) and names `HelperMain`, not a
  `HelperActivation` that never existed; `app/lint.xml`,
  `settings.gradle.kts` and `HelperMain`'s comment called the server
  unchanged too and are corrected (`HelperMain` now says its socket code is
  unchanged).
- Correction to the part-15 entry: its "Verification (rerun after the
  review's fixes)" also lists the `:protocol`/`:peer` tests, the bar-test
  mutations and the Gradle 9.8.0 check, which ran in its first round on
  inputs the fixes did not change.
- Verification, on HEAD plus part 14's catalog and its three renamed labels:
  aapt2 compiles and links the resources; kotlinc compiles `:protocol` and
  `:peer` with `-Werror` and 25 of the 26 app sources without warnings
  (`ShizukuActivation.kt` needs the uncached Shizuku; stand-ins as in part
  15); `:protocol`/`:peer` 95/95 (94 before, plus the new probe
  case); the app's unit tests 19/19 (17/17 before). The SDK's standalone lint
  reports the same as on the base: one `MissingClass` error for the uncached
  Shizuku and three warnings. The new bar-name test does not compile against
  the old bar. Mutations, each caught: names that are the label only, a
  case-sensitive match, Grab added to the bar, an extra grant (the old
  `PairingGrantsTest` missed it), a probe that ignores capacity, and a busy
  reject with another message; the new probe case also passes on the old
  `TargetServer.kt`, so reusing the constant kept the bytes. The contrast,
  settings, dialog, capitals, text and comment changes are Activity or
  resource code with no Android runtime here: compiled, checked in the linked
  resources, and read. Not run: this project's Gradle build with its pinned
  toolchain and AGP lint, a device (the outline, blur, capitals on API 27 to
  37, toasts, dialogs during destroy, the settings pages), TalkBack or Voice
  Access.
- Left alone: inline text not moved to resources; the debug build's package
  name; Gradle test-input paths; the device status dialog's inline
  "controlling" and "viewing"; "peer" in "peer may…" and "Status
  unavailable—update the peer"; `docs/ANDROID_CLIENT_PLAN.md:184` still says
  "without edits"; the busy check inside the lock (`TargetServer.kt` line
  574) needs a race to reach and has no test.
- Review round 1 found that the restricted-settings line sent the user
  straight to App info, where the menu item is missing until the switch has
  been tried, that the pressed-fill limit is about 20% white, not 18%, that
  `settings.gradle.kts` and `HelperMain` still called the server unchanged,
  and that the source count was overstated; round 2 fixes those as described
  above, and reruns aapt2, kotlinc, the app tests, the `:protocol`/`:peer`
  tests and the standalone lint with the same results.

## Update (2026-10-20: QoL overhaul, part 14: browser toolbar and controls sheet from the catalog)

- The browser's toolbar and controls sheet are built from the shared action
  catalog. `webui.go` puts the catalog into the served `index.html` as a
  JSON data block (escaped with `json.HTMLEscape`; the CSP is unchanged) and
  panics at startup unless the placeholder line is there exactly once.
  `player.js` reads it before anything else and derives the Alt+key, F-key
  and Alt+F-key tables from it, the bar (`views.webBar`, gaps before Vol+,
  Rot and Power) and the sheet. Opened as a file, the page warns on the
  console and has no catalog actions.
- Bar texts are the catalog's short names (Rec, Vol+/Vol-, Rot, Notif,
  Shade, Keys, Shot, Redraw), and each button is named by that visible text;
  its tooltip is the label plus its chords, e.g. "Home (Alt+H, F1)" or "Menu
  key (F2)" (the Menu tooltip said "no chord"), and `aria-keyshortcuts`
  carries the chords ("Alt+U F6"). The sheet lists all 17 actions but
  Controls with the same label and chords as `<kbd>` chips, each row a
  button named by what it shows (no tooltip), and runs one when clicked,
  then static rows for the mouse, the three-finger tap, Esc, this browser's
  mute and volume, and Alt+F1…Alt+F12. ☰ is named "Controls", with the
  tooltip "Controls (Alt+/)"; it and the bar's Controls button both have
  `aria-haspopup="dialog"`, `aria-controls="sheet"` and an `aria-expanded`
  that follows the sheet. The toast is a polite status region, emptied
  200 ms after it hides.
- The catalog labels `mute` "Mute device" and `resetvideo` "Refresh video"
  with the short name "Redraw" (they were "Mute", and "Keyframe" with
  "Refresh", which read like reloading the page). The terminal's menu and
  bar follow from the catalog, and the Android viewer bar's strings
  (`action_mute_label`, `action_resetvideo_label`, `action_resetvideo_short`)
  are changed to match, which part 15's bar test checks. The catalog's
  `about` text no longer says `index.html` repeats the bar by hand.
- The sheet is a dialog (`role=dialog`, `aria-modal`, labelled by its
  title): focus moves into it on open; on close it goes back to what opened
  it only while focus is still in the sheet (or nowhere), so after a session
  ends it stays on Reconnect. While it is open the idle fade does not run,
  so nothing turns inert and focus can always go back to ☰ or the bar's
  Controls button. The HUD and ☰ stay visible, dimmed, behind it. Opening
  the sheet hides the bar until the next key press, pointer move or press,
  or wheel: Alt+/ is itself a key press, so the bar stays showing, but after
  a click, a tap, or Enter or Space on ☰ or the bar's Controls button (the
  keyboard's way in once a session has ended) it stays hidden. Tab and
  Shift+Tab stay
  inside, Esc closes it, and a row closes the sheet before running (the
  first Quit press arms in place). After Esc closes the sheet, or Enter or
  Space presses a button on the page, the rest of that key's auto-repeats
  are dropped until it is released, so a held key no longer runs a row
  again or reaches the device; keys that go to the device still repeat.
  Each row's chord chips are hidden from assistive technology, so a row is
  named by its label and its chords come from `aria-keyshortcuts`. The card
  is `min(560px, 92vw)` wide: two columns in a desktop window or a phone
  turned sideways, one on a phone held upright.
- Quit in the bar and the sheet needs a second press within 2 s: the button
  reads "Quit?" at the same width, its tooltip "Quit? Press again to end the
  session", with a toast. Alt+Q still quits at once. Any other action, any
  other key but a bare modifier (Tab and Shift+Tab in the sheet included), a
  press on the picture, a wheel step that reaches the device, text from the
  phone keyboard, a paste, closing the sheet, or 2 s drop the arm, as any
  other press does in the terminal (once a session has ended, keys are left
  to the browser and only Tab in the sheet, or Esc by closing the sheet,
  drops it; there is nothing left to quit). Esc, the right click and the Alt aliases
  go through the same action path.
- Middle click is Home (documented, never wired); the context menu is Back
  for a mouse only, so a touch long-press no longer sends Back. The grab
  toasts say what the keys do: "grab on: Ctrl+W/T/N/R go to the device,
  Ctrl+Q ends the session", "grab off: Ctrl+W/T/N/R/Q stay with the
  browser".
- Tests: the regex pins on `index.html` and `player.js` are replaced by
  checks on the served page (`TestServedPageCarriesTheActionCatalog`,
  `TestActionCatalogCannotBreakOutOfThePage`, `TestNodeSuitesLoadTheServedPage`,
  `TestPlayerNamesOnlyRealOps`); the key-table parity tests run the page's
  own table code in node on the catalog the binary embeds, mapping key codes
  to characters through a table from the UI Events spec;
  `TestToolbarButtonsAreRealActions` is gone with the hand-written buttons.
  `web/page_fixture.mjs` serves the page to the node suites as the server
  does.
- Verification: built on part 8's second round and part 12's first, then
  merged onto the committed part 8 and part 12's reviewed second round: one
  conflict in `Input.key`, resolved by keeping the sheet's dialog keys and
  dropping the old Reconnect-button return for part 8's ended-session check,
  and one in a test header. On the merge: `gofmt`, `go vet ./...`, `go test
  ./...`, the web tests under `-race`; node input 130/0, worklet 25/0,
  pointer 17/0, conn 61/0, page 142/0, audio 137/0. In the builder's tree,
  six Go tests and the input suite (11 checks) fail on the old code, the
  page and audio suites cannot load the old page, the parity tests pass on
  the old code but fail on it with a changed catalog, and each of 40
  mutations is caught. Chrome keeps Ctrl+W/T/N in a normal tab, so those
  reach the device only in `--window`.
- Review round 1 found that the bar's aria-labels replaced the visible text
  as the spoken name (so speech control could not press "Vol+", "Vol-",
  "Keys" or "Redraw" by what they show), that closing the sheet after a
  session ended moved focus off
  Reconnect, that the sheet resolved to one 310 px column, and that the bar
  grouping, the toolbar's own keys and Tab on keydown only were not pinned.
  The second round fixes those as described above and takes the review's
  suggestions (`aria-controls`, `aria-expanded`, `aria-keyshortcuts`, held
  keys, the toast's status role, any other press dropping an armed Quit).
  Its checks: node input 130/0, worklet 25/0, pointer 17/0, conn 61/0, page
  169/0 (27 of them fail on the first round's files; the grouping, Enter or
  Space reaching the device when focus is not on a sheet button, and Tab on
  keydown only passed there too, and are held by mutations), audio 137/0;
  `gofmt`, `go vet ./...`, `go test ./...` and the web, catalog and keymap
  tests under `-race`; 25 mutations, each caught. Expected names, tooltips
  and shortcuts come from a table written by hand from `actions.json`. In
  headless Chrome 155 and Firefox 157 on the served page: every bar button
  is named by its text in Chrome's accessibility tree, ☰ is "Controls"
  (expanded false, then true); after a session ends, Esc leaves focus on
  Reconnect (both browsers) and Enter reconnects (Chrome); a held Enter on
  the Quit row now only arms it (it sent quit and seven Enter keys); the
  sheet shows two columns at 1280×800 and 1920×1080 and one at 390×844.
  Headless Firefox over WebDriver BiDi never presses a focused button with
  Enter or Space, so the held-key and reconnect results are Chrome's. Not
  run: a screen reader, a phone, Safari, what middle click does natively,
  Gradle for the Android strings (review round 2 saw Chrome 155 and Firefox
  157 give a mouse's context menu the pointer type "mouse").
- Review round 2 found that Esc or Alt+/ after the chrome had faded (2.5 s
  idle) sent focus to the page body, because the faded ☰ and bar are inert;
  that Tab and Shift+Tab in the sheet kept an armed Quit; that a held key's
  repeats reached the device after Esc or Close closed the sheet, and queued
  Enters after Reconnect; a
  stale comment in `index.html`; and some unpinned branches. Round 3 fixes
  those as described above and takes its suggestions (repeats dropped until
  release, a wheel dropping the arm only when it reaches the device, the
  rows' chords left to `aria-keyshortcuts`, the bar Controls button's dialog
  attributes, the toast emptied). Its checks: node input 130/0, worklet
  25/0, pointer 17/0, conn 61/0, page 202/0 (19 of them fail on the second
  round's files; the checks for focus on the body then Esc, a lone key-up,
  Ctrl+C and Ctrl+A, and a repeated Tab after a session ended pass there too
  and are held by mutations), audio 137/0; `gofmt`, `go vet ./...`, `go test
  ./...` and the web tests under `-race`; 35 mutations, 34 caught (the miss
  is the branch for no focus at all). In headless Chrome 155 and headful
  Firefox 157 (real keys and auto-repeat under Xvfb), 21 and 8 checks that
  failed on the second round pass: Esc after 3 s idle focuses the opener;
  Tab drops an armed Quit; a held Enter on Close, a held Esc, and a held
  Enter on Reconnect after a session ended send nothing more; the toast is
  emptied about 2.2 s after it shows; Chrome's accessibility tree names a row
  "Back" (it was "Back Alt+B") and gives the bar's Controls button a dialog
  popup whose expanded state follows the sheet.
- Review round 3 (Chrome 155 headless, 40 checks, and Firefox 157 headful,
  18, all passing) found two sentences of this entry wrong, on the bar while
  the sheet is open (a click that opens the sheet leaves the bar hidden) and
  on which button has `aria-haspopup` (both do), and the comment above
  `chrome()` stale (☰ never fades, it only turns inert); round 4 corrected
  the entry and that comment and changed no code. Review round 4 found the
  bar's state still described wrongly after a session has ended (Enter or
  Space on ☰ opens the sheet from a click, so the bar stays hidden) and the
  new comment calling the HUD inert; round 5 takes the review's wording for
  both.
- Correction to the part-15 entry: with these labels its viewer bar shows
  "Redraw" for that button, read as "Refresh video", not "Refresh" read as
  "Keyframe".
- Left alone: the README still says Ctrl+Q goes to the device with grab on
  and that the terminal menu has Mute (the docs change rewrites both); the
  sheet still scrolls in windows shorter than about 736 px; focus on ☰ or a
  bar button drops to the page body when the chrome fades 2.5 s later (older
  than this change); Alt+/, Alt+G, Alt+I, Alt+M and F12 toggle again on every
  auto-repeat (from reading the code); bar buttons give their chords twice,
  in the tooltip and in `aria-keyshortcuts`; a screen reader that ignores
  `aria-keyshortcuts` no longer speaks a row's chords; the bar stays hidden
  behind the sheet after a click, a tap, or Enter or Space on ☰ or the
  bar's Controls button, but shows after Alt+/ (the two could match);
  `#menu-btn.idle` in the CSS is never used, and comments in `player.css`
  and `index.html` still say ☰ fades with the chrome.

## Update (2026-10-21: QoL overhaul, part 22: adb and peer connection errors)

- The adb forward fallback can now connect (shown against a fake adb
  modelled on the server's source). scterm falls back to `adb forward` when
  `adb reverse` fails, but never passed `tunnel_forward=true`, so the
  vendored server connected out instead of listening and the fallback could
  never connect; `start` now passes it. In forward mode the sockets land in
  their slots by index, as in reverse mode; they took the first empty slot,
  so with video or audio off the next socket landed in the wrong one.
- The forward wait is bounded: one deadline for all the sockets, where the
  dummy-byte read had none and each dial had its own budget (about 10 to 60
  s); the device-name read that follows keeps its own 15 s, as in reverse
  mode. It retries while adb accepts and hangs up before the server listens,
  stops as soon as the server exits, also in the middle of a dial or of the
  wait for the first socket's dummy byte (that connection is closed), and on
  a timeout says what reverse mode says, with the server's last lines.
- A missing adb is reported first, also with `-s` or `ANDROID_SERIAL`, as
  "scterm: adb not found: install Android platform-tools (often packaged as
  android-tools or adb), or use --peer to reach the scterm Android app
  without it"; `adbRunner.run` gives the same hint. With a named device it
  used to fail later on a raw exec error.
- The server output tail keeps whole lines and, separately, the last 8
  error, warning and exception lines, so an error survives a long trace. It
  keeps the written order, never drops the newest line, gives up plain lines
  first, and no longer starts with a cut-off fragment. An error is now kept
  even without an overflow, ahead of plain lines that came after it.
- `--peer` failures name the device and address and say what to try, as in
  "could not reach Pixel 8 at ADDR (connection refused): check that it is
  serving and on this network, or run scterm peers --check", and a refusal
  says why and what to do ("Pixel 8 at ADDR refused the connection: pairing
  no longer accepted; pair again with scterm pair"). The target's own text,
  the device name, the address and the dial error are stripped of control
  characters. A `forbidden` refusal says what was asked and is not allowed,
  in the words `scterm peers` uses ("it does not allow this computer to
  control it; on Pixel 8, allow that, or ask for what it does allow (scterm
  peers --check lists it)", the second choice only when the request left a
  stream out), and `bad_token`, a session that ended or a token that expired
  before the video or audio connection came, says "try again". The other
  phrases reuse `ProbeLabel`'s and the Android viewer's wording, copied into
  `peercheck.go` so `peers --check` output is unchanged. When the saved
  address fails (other than an authenticated refusal), scterm runs the
  `peers --check` presence search once and saves a new address only after
  the pinned TLS connection there succeeds.
- Verification: gofmt, `go vet ./...`, the build, `go test -count=1 ./...`
  (the node suites included), `-race` on the 28 new and touched tests and on
  the whole package, the new tests 20 times. New tests (`adb_test.go`,
  `link_test.go`, `peerconnect_test.go`, `peerwords_test.go`):
  `TestSocketsLandInTheirSlots` (7 stream combinations in both modes; 4
  forward ones fail on the old code), `TestForwardModeAsksTheServerToListen`
  (the real `start()` against a fake adb whose server listens only when
  asked to; the old code fails with "dummy byte: EOF"), four forward-wait
  tests, `TestMissingADBIsReportedFirst`,
  `TestADBRunCarriesTheMissingADBHint`, the guard
  `TestNamedDeviceSkipsTheLookup` (passes on the old code by design), three
  tail tests, and the peer tests (unreachable, the refusals, a moved device
  found again, one that must prove itself, one that refuses at the new
  address, the wording). Every other new test fails on the old code, and 27
  mutations are each caught. Round 2 adds `TestForbiddenSaysWhatWasAsked`
  (a fake target that applies the device's rule to the request it gets),
  `TestForwardDummyByteWaitEndsWhenTheServerStops`, the guard
  `TestForwardSocketsOutliveTheWait` (a successful connect leaves nothing
  running that could close a socket later), `TestFailureCleansTheDeviceName`
  and `TestPeerConnectFailureIsClean`, and drops the `forbidden` row from
  `TestPeerRefusalsSayWhyAndWhatToTry`. The 23 tests in the four test files
  pass 20 times under `-race`; the other new ones fail on the round-1 files
  (the peer ones through a shim that only adds the request parameter),
  while the guard `TestForwardSocketsOutliveTheWait` passes there by design
  (round 1 had no watcher), and 12 more mutations are each caught.
  `TestForwardDummyByteWaitIsBounded` also checks that nothing of connect
  is left running after a timeout (round 3): stopping the watcher only on
  success passes the round-2 tests and fails this one. Tests that touch adb put an empty
  directory or a fake adb first on `PATH`. `peermode.go` was merged three
  ways onto part 17's change, cleanly, with the same added and removed
  lines. Not run: a real device, adb or scrcpy server (the forward behaviour
  is modelled from the server's source and comments), a real multicast
  search, a real 10 s dial timeout (a synthetic one covers the wording), the
  Kotlin target.
- Review round 1 found `forbidden` worded as a missing view permission
  whatever was asked (the device sends it when none of what was asked is
  allowed), the forward wait ignoring the server's exit while blocked on the
  dummy byte, two overstated sentences in this entry, and the device name
  unfiltered in these messages; round 2 fixes all four and takes its
  `bad_token` suggestion. Review round 2 found that this entry claimed the
  guard fails on the round-1 files; round 3 corrects that and adds the
  timeout path's leak check.
- Left alone: `peers --check` calls a refusal it has no label for (such as
  `busy`) "Not reachable"; an identity mismatch at the saved address now
  starts the search too, and keeps its old message when nothing better turns
  up; in forward mode only the first socket gets the dummy byte, so a server
  that dies right after it is noticed at the next socket's dial or, at the
  latest, at the device-name read (the protocol
  allows no better); the rediscovery tries one candidate and does not say
  the device moved (`scterm peers` shows the new address); the device name
  in other peer notices ("this computer has control of …") is still not
  cleaned; the last "connecting to …" error keeps its cause unfiltered so
  that `errors.Is` works (an identity mismatch can repeat the host, in
  practice an IP address); `ProbeLabel` still says "Viewing permission
  missing" for `forbidden`, which the probe never receives; aborting a dial
  in flight is not tested on its own; when a trace has more than 6 notable
  lines, the `[server] ERROR:` line can be the one left out; a server line
  over 4 KB keeps only its first 4 KB; a failing `--peer` takes about 1 s
  longer because of the search.

## Update (2026-10-21: QoL overhaul, part 23: typing beyond ASCII, and pasting, in the terminal)

- Characters beyond ASCII typed in the terminal UI (é, ñ, €, CJK, emoji)
  reach the device as injected text; they were dropped. A character split
  across two reads waits for its other bytes, and bytes that are not UTF-8
  are still ignored. ASCII keeps the keycode path. Alt plus such a character
  is one chord that does nothing, like other unmapped Alt chords (it used to
  be read as an Alt chord on its first byte, `ESC 0xC3` for é, then one
  stray byte). With the menu open, such a character
  closes the menu and is not typed, as other keys without a row do; with the
  on-screen keyboard open it is typed.
- Bracketed paste is on while the terminal UI owns the screen (`shellInit`
  writes `\x1b[?2004h`; `shellClose`, also on the panic path, writes
  `\x1b[?2004l`). A paste is one event however many reads it spans, and
  nothing inside it acts as a key (it used to be typed key by key, so a CR
  pressed Enter and a pasted Esc or Ctrl key acted), apart from the cases
  under "Left alone" where the start marker is not seen: then the rest of
  the marker (`[200~`) and the paste are typed key by key. It goes to the
  device as
  one set-clipboard message with paste on, as scrcpy's Ctrl+V does, so it
  replaces the device's clipboard; a peer that lacks the clipboard grant or
  does not list `set_clipboard` gets inject-text messages of at most 300
  bytes instead. Line breaks become LF, and the other C0 control characters
  and DEL are removed (tabs stay; C1 characters, U+0080 to U+009F, are
  kept). A notice says "pasted N characters" once it is sent, also
  when the device then refuses it (see "Left alone"); a paste over
  262130 bytes (scrcpy's limit, which the Android peer also enforces) is
  dropped as it arrives, with a notice giving its size, never truncated.
  Notices, the debug log and the panic notice show sizes only, never pasted
  text. With the menu open a paste closes it and is not sent.
- Newlines in a paste: on the set-clipboard path they arrive as line breaks
  inside the text and no Enter is pressed (on Android 6 and older the
  clipboard is set but nothing is pasted, since the server presses the paste
  key only on API 24 and later); on the inject-text fallback the scrcpy
  server presses Enter for each one, while a projection-backend peer inserts
  them as text.
- With control off (`--control=false`, a view-only peer), typed text and
  pastes do nothing and say nothing.
- Verification: gofmt, `go vet ./...`, the build, `go test -count=1 ./...`,
  and the 26 tests in the five test files below under `-race` (no race) and
  20 times each. New tests (in `input_test.go`, `paste_test.go`,
  `control_text_test.go`, `fuzz_input_test.go` and `crash_test.go`): typing
  (`TestTypedNonASCIIGoesAsText`, `TestTypedNonASCIISplitAcrossReads`,
  `TestTypedMixedASCIIAndNonASCII`, `TestEscAndAltWithNonASCII`,
  `TestNonASCIIInTheOverlays`, `TestTextLeavesTheActionBarAlone`,
  `TestInputFuzzSplitReads`), pasting (`TestBracketedPasteWhileTheUIOwnsTheScreen`,
  `TestPasteIsOnePaste`, `TestPasteSplitAcrossReads`,
  `TestPasteLongerThanOneMessage`, `TestDroppingAPasteNeverLeavesALoneEsc`,
  `TestPasteWithoutTheDeviceClipboard`, `TestPasteIsNeverKeys`,
  `TestPasteInTheOverlaysAndUngrabbed`, `TestPastedTextIsNeverPrinted`,
  `TestSetClipboardWire` against the shared wire fixture,
  `TestSetClipboardLimit`) and guards that pass on the old code by design
  (`TestInvalidUTF8StaysIgnored`, `TestPasteWithoutControl`,
  `TestTextWithoutAControlSocket`, the extended `TestInputFuzz`). The other
  18 fail on the old code, and 26 mutations are each caught. Built on part
  16's first round; `tui.go` was merged three ways onto its second round,
  cleanly, with the same added and removed lines. Not run: a real terminal
  (its paste markers and line breaks are taken from documented behaviour), a
  device, adb or a peer; the input loop's own reads and its 25 ms Esc window
  have no test.
- Review round 1 (read-only) also ran the real input loop in a child process
  reading a pipe, and the vendored server's message parser compiled with
  `javac` and a stub `Size`; both behaved as this entry describes. It found
  this entry's wording wrong or incomplete on an unfinished paste, C1
  characters, an Esc just before a paste, the notice for a refused paste and
  how Alt+é used to be read; round 2 corrects the entry and the `pasteText`
  comment, which now says C1 characters are kept (the code is otherwise as
  reviewed). Review round 2 found that comment also saying tabs are
  removed; round 3 corrects it and takes the review's wording notes (the
  rest of the start marker is typed too, ESC O has no time limit, mouse
  reports join an unfinished paste).
- Left alone, for later changes: the browser still pastes key by key
  through the `text` op, so the two front ends paste differently; the scrcpy
  server types injected text only through its key map, so typed €, CJK and
  emoji do not appear in adb sessions (accented letters do, and pasting
  carries them all); a paste start marker cut by more than 25 ms on a slow
  link is read as Esc (Back), and the rest of the marker (`[200~`) and the
  paste are typed, as for any escape sequence; an Esc in the same read as a
  paste's start marker, or less than 25 ms before it, is read together with
  the marker's ESC as Alt+Esc, so the rest of the marker and the whole
  paste are typed key by key (a pasted ESC q would then quit scterm and a CR
  would press Enter), and an ESC O with nothing after it before a paste
  does the same however long before (ESC O waits for its third byte with no
  time limit); a terminal that sends a start marker
  and never an end marker holds all further input, and the keyboard cannot
  get out of it: Ctrl-C, Alt+Q and Esc do nothing (raw mode reads Ctrl-C as
  a byte, and every byte until an end marker belongs to the paste), so only
  closing the terminal, a signal from outside or the session ending ends
  scterm, and if an end marker comes later, for example with the next paste,
  everything typed in between, mouse reports included (without their Esc),
  goes to the device as one paste, or is dropped with the size notice once
  it passes the paste limit (bash's
  readline and vim also hold later keys, but Ctrl-C gets out of both; zsh
  ends such a paste without its marker); the notice "pasted N characters"
  also appears when a peer refuses the paste because this computer does not
  hold control (the lease), and that refusal notice shows only once per run,
  so after the first refusal the success notice is the only feedback; a
  large set-clipboard is written from the run loop and can briefly hold up
  drawing over a slow peer link; `TestNilConnGuard` does not list
  `setClipboard`; test gaps the review found: mutations that survive
  (dropping once the text alone reaches the limit, no `Caps` nil check,
  sending an empty paste), and the paste markers in `TestInputFuzz` go
  straight to `decodeInput`, so they never reach the paste decoder; the
  README says nothing about pasting in the terminal.

## Update (2026-10-21: QoL overhaul, part 18: the browser viewer's server side)

- Reloading the `--window` page no longer ends the session. When the last
  viewer leaves, the session ends `webLeaveGrace` (3 s) later unless a viewer
  connects first, and a reloaded page connects well within that, as soon as
  its script loads (a page that stays loaded retries a dropped socket after
  250 ms). Closing the window still ends scterm, now about 3 s after its
  socket drops: `window.go` only waits for the browser and never stops the
  server, so the end still comes through the socket. A stop or a returning
  viewer cancels the pending timer. When it fires, it checks under the lock
  that it is still the current timer and that no viewer is connected, then
  checks that no stop has begun and quits after releasing the lock; a connect
  or a stop landing between those checks and the quit does not prevent it,
  which is the same as arriving just after the grace. A browser that never
  connects still never ends the session, as before.
- A failed text write closes the client fully (`writeJSON` calls `close()`):
  it leaves the list, the viewer count drops and its socket closes, so its
  page shows "Connection lost. Reconnecting…" and connects again; before, a
  status write that timed out after 30 s could leave the page on a frozen
  picture with its socket still open, because the client was only marked
  closed, stayed listed, and `close()` returned early (the gap the part-8
  entry recorded). In `--window` mode, if it was the last viewer, the leave
  grace then starts, so the session ends only if the page does not
  reconnect. A failed binary write still leaves it to the next status
  write, at most 500 ms later.
- The client list is copy-on-write: `frame()` and `pushPCM()` walk a snapshot
  without the lock, and a list once stored is never written again; `frame()`
  allocates nothing new. Viewers coming and going while frames and sound fan
  out used to race (`closeWith` and `handleWS` against `frame`/`pushPCM`).
- A viewer that stopped reading no longer holds up shutdown. A close sets a
  2 s write deadline (`wsCloseTimeout`) before it takes the write lock, which
  also cuts short a frame being written to that viewer, and a write that
  starts as the close begins keeps the close's deadline. The bye is still the
  last message before the close frame. A stalled viewer gets neither, and a
  live viewer that needs more than 2 s to finish its current frame gets a cut
  frame and no bye, so its page shows "Reconnecting" rather than "Session
  ended". With a stalled viewer the stop took 2.05 s; before, the first stop
  waited out the 30 s write timeout and a second stop gave up after 5 s with
  the window still open.
- The `gain` op (no shipped page sends it): an absent or negative `gain`
  keeps the level, then `mute:true` mutes and remembers the level and
  `mute:false` restores it when muted. Before, an absent `gain` meant 0 and
  -1 was clamped to 0, so both muted, even beside `mute:false`, and
  `mute:false` restored 100%. `webEvent.Gain` is now `*int`, like `Mute`.
  This closes the `gain` problems the part-9 and part-12 entries recorded.
- Verification: gofmt, `go vet ./...`, the build, `go test -count=1 ./...`,
  and `-race -count=3` on the web, bye, second-stop, WebSocket, window-kill
  and encoder tests (40 tests, 120 passes, the node client suites included;
  the builder's base had 39, before part 17 added
  `TestWebExitsTwoWhenTheDeviceIsLost`).
  New tests: `web_session_test.go` (`TestWebWindowSurvivesAReload`,
  `TestWebWindowReloadCancelsTheGrace`, `TestWebWindowEndsAfterItsLastViewer`,
  `TestWebStopCancelsTheWindowGrace`, `TestWebFailedWriteClosesTheClient`,
  `TestWebClientListChurn`, `TestWebStopWithAStalledViewerClosesTheWindow`,
  `TestWSCloseBoundsARacingWrite`) and `webinput_gain_test.go`
  (`TestWebGainOp`, 11 cases from wire JSON). Each of the nine fails on the
  base files: six on behaviour (the churn test under `-race`, in 10 of 10
  processes; the gain test in 8 of its 11 cases), three on the new state
  behind a three-field compile shim. Mutations, each caught: quitting at
  once, no cancel on connect or on stop, `writeJSON` only marking the client
  closed, removing or adding and removing in place, no close deadline,
  bounding only the final frames, no re-check in `writeAll`, and the old gain
  handling. Adding in place (copying only on remove) survives: an in-place
  add writes only past the current length, and a copy-on-write remove either
  allocates a new array or, when it drops the viewer at the end of the list,
  leaves capacity equal to length, so no older, longer list shares the
  slots an add writes. The add-side copy stays so the rule does not depend
  on that. The tests simulate a reload and a window close by a test client
  dropping and reopening its socket. Not run: a real browser or device, a
  measured page reload, or `window.go` with a real browser exiting (read
  only).
- Review round 1 found the surviving mutation described backwards with a
  false reason, the timer's and the failed write's consequences overstated,
  the 5.5 s worst case below missing, and the user-visible change for a
  failed status write unstated; round 2 found the test count stale and the
  failed write's last-viewer condition dropped. Rounds 2 and 3 correct this
  entry only (the code is as reviewed).
- Left alone, for later changes: every viewer that takes a shared frame
  returns its buffer to the pool, so with two or more JPEG viewers one buffer
  can reach two senders at once (a throwaway `-race` probe with 3 viewers at
  480×1080 reported races in `sendFrame` on the base files and after);
  `nClients` is stored after the lock is released, so two viewers coming or
  going together can leave it stale; a viewer whose upgrade finishes while
  `stopWith` runs can miss the stop; `quit()` drops the event when the app's
  queue is full (read only); the `a == nil` check in the `gain` case cannot be
  true; the timer's guards against a connect or a stop at the same moment are
  reasoned, not tested deterministically (removing either guard passes the
  window tests); a stop can still outlast the 5 s a second stop waits, in
  the worst case 2 s for an in-flight page request or an idle connection
  that sent none (a browser's preconnect), 2 s to close a stalled viewer and
  1.5 s for a browser that ignores SIGTERM, so the process can
  exit with the window open; an unmute through the `gain` op can race Alt+M
  on the app loop and mute instead; `README.md` still says closing the
  window ends scterm, without the grace or the reload, and a note in
  `web/player.js` still says `--window` ends when the last client
  disconnects.

## Update (2026-10-21: QoL overhaul, part 24: scterm update)

- `scterm update` shows what it is about to do first: the current and new
  versions, the release page, and the release notes cut to 15 lines ("… (the
  rest is on the release page)"). On a terminal it then asks "Update scterm
  1.2.3 to 1.3.0? [Y/n]": Enter, y or yes updates, anything else or the end
  of input keeps it ("Kept scterm 1.2.3.", exit 0). `-y`/`--yes` skips the
  question; without a terminal it updates as before, after the same summary.
  It used to replace the binary without showing the notes or asking.
- `scterm update --check` says whether a newer release exists ("scterm 1.3.0
  is available (you have 1.2.3): run scterm update", or the usual
  latest-release line), exits 0 and downloads nothing. On a source build it
  explains that `dev` cannot be compared with a release.
- The download shows one progress line on stderr while stderr is a terminal,
  redrawn in place and cleared at the end (`release.Download` gained a
  progress callback).
- The previous binary is kept as `<path>.old`: copied under a temporary name
  before the swap and renamed after it, and the output says how to go back
  ("scterm 1.2.3 is kept as <path>.old. To go back to it: mv …", quoted for a
  POSIX shell when needed). A failure before the swap leaves the binary and
  any older `.old` untouched; with something in the way of `.old`, the
  update stands, a warning says where the copy is, and the go-back command
  names it.
- A release without a published SHA-256 digest is still installed, as the
  help says, but it now says so before the question: "GitHub published no
  SHA-256 digest for scterm, so only its size is checked."
- Unchanged: what `version` prints (`checkRuns` relies on it), the
  source-build path and the latest-release message. An unknown argument now
  says which word is wrong ("usage: scterm update [--check] [-y|--yes]
  ("--bogus" is not one of these)"), and part 17's exit-status test expects
  that line.
- Verification: gofmt, `go vet ./...`, the build, `go test -count=1 ./...`,
  `-race` on the update and release tests, the new tests 20 times. New
  tests: `TestUpdateWithoutAQuestion`, `TestNoteLines`,
  `TestUpdateShowsTheStartOfTheNotes`, `TestUpdateAsksOnATerminal`,
  `TestUpdateArgs`, `TestUpdateCheck`, `TestUpdateCheckOnASourceBuild`,
  `TestDownloadReportsProgress`, `TestProgressLine`,
  `TestUpdateShowsProgressOnlyOnATerminal`,
  `TestUpdateKeepsThePreviousBinary`,
  `TestUpdateKeepsThePreviousBinaryWhenOldIsTaken`,
  `TestFailedUpdateKeepsTheOriginal` (9 cases),
  `TestUpdateWithoutPermissionLeavesItToSudo` and
  `TestUpdateSaysWhenThereIsNoDigest`. They do not compile against the old
  files; against the old behaviour with only test seams added, every new
  behavioural test fails, while the sudo test and 8 of the 9 failure cases
  guard older behaviour and pass there. 21 single-line mutants, each caught.
  Release-style builds run against local test servers (with a dead proxy for
  anything else) made only local requests, downloaded only after a yes, and
  `mv` took them back to 1.2.3; an unreadable binary stopped the update with
  nothing replaced. The terminal cases ran in a pseudo-terminal from
  `script`. Not run: the real GitHub API or a published release, CI.
- Left alone: the question comes before the download, so the release check
  and the download each get their own 5-minute limit (10 minutes at worst);
  progress follows stderr and the question stdin; the copy costs about 13 MB
  of extra disk while the update runs; the question goes to stdout through
  the shared `ask()`, as `pair`, `forget` and the peer chooser do, so with
  stdout redirected it is hidden and update seems to hang (`-y` avoids it);
  the sudo hint is unquoted and keeps no `.old`; a source build is still
  replaced without a question or a `.old`; Ctrl+C mid-update can leave a
  `.scterm-download-*` or `.scterm-old-*` file; the README is out of date.

## Update (2026-10-21: QoL overhaul, part 16: terminal polish)

- Autowrap is off while the terminal UI owns the screen: `shellInit` writes
  `\x1b[?7l` and `shellClose` writes `\x1b[?7h`, also on the panic path
  through `terminalRestorer`, so a write that reaches the last column can no
  longer wrap and scroll the picture. `appendStatus` still skips `\x1b[K`
  after a full row, since the cursor sits on the last cell; its comment no
  longer mentions a pending wrap.
- Each frame ends with the colours reset (`\x1b[0m`) before the end of the
  synchronized update, so text that reaches the terminal between frames no
  longer takes the video's colours, and `shellClose` first ends a
  synchronized update that a crash may have left open (`\x1b[?2026l`). Both
  were suggestions from part 6's review.
- An open on-screen keyboard is laid out again after a resize, also before
  the first frame; it kept its old layout (part 6's review, and part 13's
  left-alone list). Its rows follow the terminal's height, not its width.
  With a frame drawn, the repaint is stale and clears the picture until the
  next frame, as it already does for the menu and the bar.
- Tests now pin what part 6's review found unpinned: the status row is
  painted again after `resize` and `shellInit` clear it, a forced full
  repaint paints the status row, and the no-canvas overlay test draws a
  striped canvas, so its `▀` check can fail.
- While Quit is armed from the bar, the menu or the keyboard, the status row
  says "press Quit again to quit" first after the hints, also before a
  notice (a notice shows for 4 s, an arm lasts 2 s), because the menu's
  "Press again to quit" row is cut off at 18 rows or fewer (part 9's
  review). On a narrow pane it is cut with "…" like the other parts: 50
  columns show "press Quit again to…".
- A lost mouse release lifts the finger where the device last saw it (the
  press, or the last move actually sent), not at the new click (part 9's
  review). A move still waiting to be sent does not count.
- CR followed by LF in the same read is one Enter. On terminals that send
  CR LF, one Enter on a focused QUIT used to arm and confirm it, and the
  device got Enter and then Ctrl+J (part 9's review). CR and LF that arrive
  in separate reads are still two keys.
- The terminal restore no longer interleaves with a frame being drawn
  (part 3's review): `shellClose` clears `running` and writes the restore
  while holding the UI lock, waiting at most 100 ms for it (`lockWithin`),
  and `screenshot` reads `running` under the same lock. The wait is a `Lock`
  on a helper goroutine, not `TryLock` polling: `TryLock` never queues, so it
  wins only in the brief gap between two of a busy painter's draws, while a
  queued `Lock` is handed the mutex once the painter has kept it waiting for
  1 ms (with polling, under `-race`, a frame was written after the restore
  in 21 of 30 runs). A lock that comes after the 100 ms is let go again at
  once. The bound is for a holder that is not writing, such as a panic while
  it held the lock: after 100 ms the restore goes ahead without the lock, a
  data race only in that stuck case. A painter blocked in a write still
  holds the restore up, since the restore's own writes queue behind it on
  stdout; either way the restore comes after the frame.
- The Zellij re-grab timer only posts an event; the run loop does the
  re-grab, and does nothing once the UI has stopped. It used to draw off the
  run loop (part 13's left-alone list).
- Once the terminal is restored (a quit, a lost picture, a crash restore),
  closed-socket errors from tearing down the streams are no longer printed,
  such as "scterm: audio: audio codec id: read tcp …: use of closed network
  connection" (part 13's review). `terminalRestorer` now sets
  `quitting`, which the terminal UI never set, and the stream watcher drops
  `isClosedConnErr` errors only once it is set: that check also matches
  `io.EOF`, and an audio stream that ends under a running picture must still
  get part 13's notice.
- Under Zellij the status row says "typing goes to Zellij · F12 gives
  control"; part 13's "keys go to Zellij" was wrong for F-keys, Alt chords
  and Esc, which still reach the device. At 80 columns the longer text drops
  the volume part.
- A device whose name is unknown is left out of the status row instead of
  showing as "the device"; the placeholder and the lost-device message still
  say "the device".
- Verification: in the builder's tree, on part 13's round-2 files: gofmt,
  `go vet ./...`, `go build ./...`, `go test ./...`, `-race` on all 84 tests
  in the bar, menu, keyboard, status, notice, render and new input test
  files, and 11 timing-sensitive tests 20 times, plain and under `-race`
  (220 of 220 each). Against the old code (through `go test -overlay`), 13
  of the 19 new or changed tests fail; 3 pin existing behaviour (mutations
  that pass the old suite fail them), 2 fail only under `-race` (races of
  `shellClose` with `draw` and with `screenshot`), and 1 guards the new lock
  helper. 29 mutations, each caught. Merged onto part 13's round 3 and part
  17: `app.go` and `tui_notice_test.go` merged cleanly with the same added
  and removed lines, and the other files are as built.
- Review round 1 found the reason given for `TryLock` polling failing wrong
  here, the 100 ms bound overstated here and in the comments on
  `shellCloseWait` and the restore test, and two promises no test held: the
  prompt before a notice, and a late lock being let go. Round 2 corrects
  the text, adds `TestStatusPromptComesBeforeANotice` and
  `TestRestoreLetsGoOfALateLock` (each fails on the mutation the review
  used, a notice replacing the prompt and a buffered handoff channel, which
  the rest of the suite misses), lists every event kind on
  `inputEvent.kind`, and holds the frame for 15 ms instead of 50 in
  `TestRestoreWaitsForTheFrameBeingWritten`, against the 100 ms bound.
  Round 2's checks, on the part-17 commit: gofmt, `go vet ./...`, the build,
  `go test ./...`, `-race` on the terminal UI and exit tests, the new and
  touched restore and status tests 20 times under `-race`, and the two lock
  tests 50 times. Not run: a real terminal (autowrap, synchronized output, a
  clipped wide character), Zellij, a device or a peer session.
- Left alone: `setMouse` and `toggleGrab` write to stdout outside the UI lock
  (older), so a crash restore on another goroutine racing a re-grab that is
  already past its check could still turn mouse reporting back on
  (untested); peer sessions' teardown errors were not exercised, and a
  target that closes a media socket in the middle of a packet ends it with
  "unexpected EOF", which `isClosedConnErr` does not match, so that line
  could still print after a quit (untested); ESC CR LF (Alt+Enter on a CR LF
  terminal) still sends Ctrl+J. From part 13's last review,
  test-only and older: under `-race` with `-shuffle`, a test that swaps
  `os.Stderr` (`captureStderr`) can race a web client goroutine left over
  from an earlier test that logs through `logOnce`.

## Update (2026-10-21: QoL overhaul, part 17: scrcpy's exit codes)

- Exit status follows scrcpy: 0 for a normal end (Quit, closing the
  `--window` window, a signal during a session, `-h`, `help`, `version` and
  commands that worked), 1 when scterm could not start, a command failed or
  the session failed for another reason (a decoder error, the web server
  failing), 2 when the device is lost during the session. Usage errors (flag
  mistakes, unknown words, `validateConfig` refusals such as all streams off
  or `--no-tui --web`, `version x`, `update --bogus`, `peers --bogus`, a bare
  `forget`) now exit 1, not the 2 that parts 4 and 10 gave them; their
  messages are unchanged. The help has an "Exit status" section between
  Environment and Examples, which also says a crash exits 2.
- "Lost" means a read from the device failed inside the video or audio
  packet loop after the stream started: `stream.go` wraps those six reads in
  `lostError` (messages unchanged), and `exitStatus` maps it to 2 and any
  other error to 1. As in scrcpy v4.1 (`demuxer.c`, `scrcpy.c`), a failure
  before the stream starts (declined, an unknown codec, an unreadable codec
  id or session packet, a decoder that won't open) or a decoder error
  mid-stream exits 1: the bundled server sends the codec id before it sets up
  the encoder and closes the socket if that fails, so an early read failure
  usually means the server failed, not the device. Unlike scrcpy, a
  disconnect in the middle of a packet also exits 2.
- Per display: the terminal UI exits 2 when the stream it shows (video, or
  audio without video) is lost (it was 1); `--web` and `--window` when video
  is lost after it started (1 before); `--no-tui` likewise (0 before: it now
  returns the error to `main`, which prints the same `scterm: video: …` line
  once, after shutdown, about 200 ms later, as the other displays already
  did; found in review: after a signal quit it no longer prints the
  teardown's closed-connection video line, and that line can no longer
  interleave with a late stats line). A peer target ending the session while
  the stream runs exits 2 (its reason line still prints first), before the
  stream started 1. `main`'s ending moved into `endSession`: the peer reason
  line, then for an error the session's stop before `exitWith` prints the
  line and exits, while a quit returns as before; `fatal` still exits 1.
  Panics stay Go's 2, and a signal before the session starts still kills
  scterm (130, 143 or 129 in a shell), as before.
- Verification: gofmt, `go vet ./...`, `go test -count=1 ./...` (on the
  part-13 commit, 187 top-level tests in `scterm` pass and one skips; that
  commit alone has 182 and the same skip), `-race -count=3` on the seven exit
  and help tests, a `go test -cover` run of the three child-process tests,
  and the production build, also with CI's flags. New `exit_test.go`:
  `TestOnlyALostDeviceExitsTwo` (14 cases through the terminal UI's stream
  watcher: 7 lost give 2, covering each of the six wrapped reads and a socket
  closed at this end; 7 pre-stream failures give 1),
  `TestWebExitsTwoWhenTheDeviceIsLost`,
  `TestHeadlessExitsTwoWhenTheDeviceIsLost` (which also checks that
  `runHeadless` prints nothing itself), `TestSessionEndExitsWithItsStatus`
  (`endSession` in a child process: a lost device exits 2 and any other
  error 1, each after the session's sockets are closed; a quit returns) and
  `TestCommandLineExitStatus` (the real `main()` in a child process with no
  inherited environment but `GOCOVERDIR`, an empty PATH or a fake adb;
  expected stderr taken from the base binary). The renamed
  `TestLostDeviceIsPrintedAndExitsTwo` drives the real watcher into
  `exitWith` in a child process and checks stderr exactly. On the base,
  `TestCommandLineExitStatus` fails on exactly the nine usage cases and
  `TestHelpIsGrouped` on the missing section; with a shim reproducing the
  base `main`'s ending, the four exit-2 tests fail on their lost cases (and
  `--no-tui` on its declined case, which exited 0). Mutations, each caught:
  nine in the first round (lost exits 1, usage exits 2, each kind of wrap
  removed, a decline or a codec-id failure marked lost, `runHeadless`
  returning nil) and six in the second (the raw-audio header read and the
  decoded-audio packet body read unwrapped, `endSession` calling `fatal`,
  skipping the stop or exiting on a quit, and the help's old line for 1).
  The real binary under `env -i` with an empty PATH (first round): the nine
  usage cases went from 2 to 1, every other case kept its status, and stderr
  is byte-identical; stdout differs only by the help section. Built on part
  13's round-2 files and merged onto round 3 (only `tui_notice_test.go`,
  cleanly); with parts 14 and 16 in the same tree, vet and `go test ./...`
  pass. Not run: a device, an unplug, a peer revoking access, the terminal
  UI's full `run()` in a terminal, or `main()` past the session start; the
  peer reason line in `endSession` is checked by reading only.
- Review round 1 asked for this entry's test counts (taken from a tree
  without part 13's last test) and its list of out-of-date entries to be
  corrected, and noted that reverting `main`'s final call to `fatal` passed
  the whole suite, that `TestCommandLineExitStatus` failed all 14 cases under
  `go test -cover` (the child lost `GOCOVERDIR`; the first round's test file
  still prints 14 coverage warnings there), that the two reads above had no
  case, and that the help's 1 left out sessions that fail for other reasons.
  All are fixed as described above.
- Left alone: `--web` prints a video error twice when it is not a closed
  connection (older); the terminal UI's "lost the video stream from …"
  wording also shows for a decline, which now exits 1; an audio stream
  ending under a picture is only a notice, and control-only sessions or
  `--web` without a video socket never notice a lost device; scrcpy exits 0
  on Ctrl+C while waiting for the server, where a signal still kills scterm;
  the part-4, part-10 and part-13 entries describe the exit codes of their
  time (part 13's says a lost device and a peer ending the session exit 1,
  and that `--no-tui` exits 0 on a lost device).

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

## Update (2026-10-20: QoL overhaul, part 13: terminal status line, notices, device loss)

- The status line starts with the way out, "Alt+Q quit · Alt+/ controls",
  then " | " and, as room allows: the input state when the mouse is not
  grabbed ("mouse free (F12)", or under Zellij "keys go to Zellij · F12 gives
  control"), "[KB]", "vol N%" ("audio off" when the sink failed, nothing with
  `--audio=false`), the device name, the frame size and rate, and the
  sparkline. The first part that does not fit ends the row; the state, the
  name and a notice are cut with "…" instead when 6 cells remain. At 80
  columns the hints used to be cut to "| Al". A steady 60 fps now draws "▁"
  in every cell (it drew 20 blanks), and the row is cut and erased by display
  cells, so a name in double-width (CJK) characters cannot wrap it; an emoji
  that is narrow by default followed by variation selector 16 (❤️) counts as
  one cell, and many terminals draw it as two. "UNGRAB" and "Alt+I keys" are
  gone, and the menu title reads "scterm · Controls".
- Runtime messages no longer scramble the picture. While the terminal UI
  runs, a message shows in the status row after the hints for 4 s (the newest
  wins): screenshot results with the saved path, audio sink switches, peer
  control changes, input, inject and control-receive failures, and anything
  through `logOnce`. After the terminal is restored, also after a panic, they
  print on stderr once each in first-seen order, with repeats counted,
  keeping the 50 newest and a first line counting any left out. Before and
  after the UI, and with `--no-tui` or `--web`, they print at once as before.
  `SCT_DEBUG_*` traces still go to stderr.
- Losing the device ends with a reason and exit 1, as in "scterm: lost the
  video stream from Pixel 8: stream ended: EOF" (the audio stream when there
  is no video); it used to exit 0 with nothing shown. `main` stops the
  session before exiting, as the deferred stop did. A device declining video,
  and a peer ending the session (after its reason line), now also exit 1. A
  quit (Alt+Q, two-press Quit, signals) still exits 0.
- Before the first frame the picture area says "Waiting for video from
  <device>…", "Video is off (--video=false)", or "No video from <device>"
  when the session has no video (a peer pairing without view, or a peer
  device that offers none), centred again after a resize. Control
  characters are taken out of notices in the status row and of the device
  name in the status row, the placeholder and the lost-device message, so an
  escape sequence there no longer reaches the terminal; the replayed lines
  and other stderr messages keep their text as posted, a device name
  included.
- Resize needed no change: the scaler rebuilds for any new size and the
  server repeats the last frame after 100 ms. A test pins that the next frame
  comes at the new size.
- Corrections to part 9's entry: its `-race` run passed 61 tests, not 60;
  and mouse buttons 8 to 10 act as left, middle and right on the video and
  the bar, while button 11 does nothing.
- Verification: the first round, in the builder's tree: `gofmt`, `go vet
  ./...`, `go test ./...` (197 top-level tests pass, 3 skip), a `-race` set
  of 78 tests, and the notice, loss and placeholder tests 20 times under
  `-race`. The final code on the committed parts 8 to 11: `gofmt`, `go vet
  ./...`, `go test ./...` (208 top-level tests pass, 3 skip), the notice,
  status, placeholder, bar, menu, keyboard and quit tests under `-race`, and
  the replay, notice and placeholder tests 20 times under `-race`. Five of
  the six new status tests fail on the old code. The notice tests cannot
  compile against it, so five behaviours were reverted on a copy instead,
  and each revert was caught. Review found three that no test held, now
  each failing its test when broken: the replay's order (stdout and stderr
  read through one pipe), the replay's stream (stderr, not stdout), and the
  cleaning of notices. The no-video placeholder case fails on the first
  round's code. `scterm version` and the no-adb error are byte-identical.
  Not run: a real terminal (layout, colours, wide characters), Zellij, or a
  real USB or peer loss.
- Left alone: the README still shows the old status text and "Action menu";
  the Zellij keyboard nudge timer paints off the run loop (a data race); an
  open keyboard overlay is not refreshed on resize; ambiguous-width
  characters count as one cell; control-only sessions do not notice a lost
  device, and `--no-tui` notices it (`scterm: video: …`) but still exits 0,
  or with audio only prints `scterm: audio: …` and keeps running; "No video
  from <device>" does not say why. Seen in review, older and from reading
  the code: in adb forward mode (when `adb reverse` fails) each socket takes
  the first empty slot, so with `--video=false` the audio socket would land
  in the video slot.

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
