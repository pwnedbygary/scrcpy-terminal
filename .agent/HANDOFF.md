# Android peer app / Go client / parity development checkpoint

Read [the complete plan](../docs/history/ANDROID_CLIENT_PLAN.md) (section 2a lists the
design revisions adopted on 2026-09-24), [the peer protocol](../docs/PEER_PROTOCOL.md)
and applicable `AGENTS.md` instructions first (none exist in this repository or
its parents as of this snapshot). Reconcile this checkpoint against Git,
current source, live task ownership and test evidence before editing. Status
here is a snapshot, not proof that no other work has started.

## Update (2026-10-23: QoL overhaul, parts 1 to 64, condensed)

The QoL overhaul is PR #6, one commit per part on the `overhaul` branch
(branched from `62d6cfa`), each reviewed independently before it landed. Parts
1 to 50 are commits `c04fde6` to `bee1feb`. After the PR was marked ready, the
owner asked for a second batch: the PR's "Left for later" points became GitHub
issues #7 to #17, twelve more commits (`cb02c66` to `ee6cce3`) fixed most of
them, and their reviews filed #18 to #27. Parts 56 (an investigation, with no
commit) and 62 (this condensing) have no section. Each part's full entry (its
reviews, checks and evidence) is in this file's history
(`git show ee6cce3:.agent/HANDOFF.md`), and each commit message records its
reviews and checks. The sections below run newest first, in the order the
commits landed, and name the commit that added each entry. Each label starts a
bullet or sits inside one: "Keep:" marks a rule that a later change must not
break, "Decided:" a choice the part made, and "Owner's" one the owner made.
"Verified" lists the review rounds and the kinds of checks, which used fakes
(no device, adb, display or sound server) unless the line names something
real. "Open" is what was still open at `ee6cce3`, so an item a later part
fixed is left out. Items after "Also:" in an Open bullet are in no issue
unless they name one.

### Part 64: fish 4.2's looser matches (ee6cce3)

- `TestFishCompletesAsTheHelpSays` failed CI's first run on part 60 (879202f):
  for `scterm -n`, the fish CI installs (Ubuntu's 4.2.1) offered `-n` and
  seven long flags holding an n, where the test wanted `-n` alone. Part 64
  fixes that test (CI passed on ee6cce3); CI's shell install step, its test
  step's comment and `docs/RELEASING.md` now name the completion tests too.
- Fish's matching is the cause, not the scripts, which are unchanged. Where no
  candidate starts with the word, fish 4.2.1 and 4.9.3 offer looser matches:
  those holding the word, or else its letters in order (4.2.1 gives both for
  an option). Fish 4.2.1 also offers looser long options beside those that
  start with an option's name; fish 4.7.0 lists a fix that fits.
- The test still compares what fish offers for a line whole, except where the
  word is an option's name longer than a dash and some candidate starts with
  it, ignoring case: those candidates must equal the expectation, which is
  unchanged, and each other must be offered for the line with the word cut to
  a dash, itself a case. A case line with a quote or a stray backslash, or
  needing a dash line that is not a case, fails at once. Keep: a line where no
  candidate starts with the word is compared whole, as at 879202f.
- Verified: 2 review rounds; the tests under fish 4.2.1 and 4.9.3; mutants.
- Open (#26): a fish script offering the pairings beside `--dump-frames`'s
  directories fails no test; no case pins that the exception needs a candidate
  starting with the word (`scterm -w` would); the test's comment says fish's
  looser matches hold the word's letters in order, but save for an option
  under 4.2.1, fish offers those holding the word whole when any does.

### Part 60: shell completion (879202f)

- `scterm completion bash|zsh|fish` prints a script; a missing, extra or
  unknown shell is a usage error, status 1. It completes commands, flags
  (scrcpy's aliases too), command options, codec, source and dump-path values,
  and pairings after `--peer`, `--peer=` and `forget`, which the hidden
  `scterm __complete peers|forget [WORD]` lists; no serials: it runs no adb.
- No pairing whose identity does not parse is offered, and `--damaged` is last
  after `forget` while one is damaged; the pairings file is read once, the
  identity file never. Keep: completion names a pairing as `pasteArg` does,
  through `pasteArgFor`, which calls part 63's `everyCopyFinds`, and searches
  with `peer.FindIn` (so `MatchesIn`), which `Store.Find` and `Store.Matches`
  call, so a change to how a pairing is named or found reaches both.
- bash inserts as written and offers no pairing when the cursor is inside a
  word (`_scterm_peers`), as the rest of the word could name another pairing;
  flags and values still complete. zsh inserts as written outside quotes and
  lets zsh quote inside one, matching either case; fish escapes. Keep: each
  script runs the helper with `command` and expands none of its lines.
- Verified: 2 review rounds; real bash, zsh and fish in ptys; mutants.
- Open (#26): nothing pins two of `Find`'s rules or bash completing before a
  space; the hostile-line test runs bash only with `=` in `COMP_WORDBREAKS`; the
  bash tests read `/etc/inputrc`; a Tab parses every identity per pairing (0.6 s
  at 1,000); zsh puts the space inside a closed quote; a quoting quirk in each
  shell; the README omits the pairings not offered, and the `--damaged` offer.
  Also: `TestShotDroppedWhenTheSessionEnds` can fail on correct code (#27).

### Part 63: names a terminal's copying could change (85901e2)

- A printed command (after a pairing, in forget's refusal) names the device
  only if a paste does the same whatever the terminal drops, as it copies, of
  what has no width: marks, and the Hangul vowels and final consonants
  (U+1160-U+11FF, U+D7B0-U+D7FF; width 0 in glibc and GLib). Part of #19.
- The name holds at most one such character; as written and without it, it
  selects this pairing alone; without them and its quotes (`pastedAs`), it is
  not empty, has no space at either end or two together, holds no scterm, and
  is not a help word or `--damaged`. Otherwise the command gives the identity.
  Before, `Pix`, U+FE0F, `el` beside `Pixel` was printed by name, and its
  command, copied without the mark, forgot `Pixel` at once.
- doctor cleans the serials of several ready devices, as scterm's error does.
- Decided: selection keeps the name's quotes, as a shell does inside the
  command's (`Gary's Pixel` stays named); a name whose copy selects nothing,
  or with two such characters (a keycap), goes by identity.
- Verified: 3 review rounds; 7 shell modes; name sweeps; mutants.
- Open: the rest of #19 (in part 51's Open); `Find` matches a whole name by
  `EqualFold`, its start by `ToLower`, which differ on some letters, a gap
  closed only for printed commands and, since part 60, completion; doctor's
  test of its several-devices line runs only while `pickDevice`'s error says
  "devices are connected", so rewording that error turns the test off; whether
  a name with two such characters could keep its name, if all four copies were
  checked, is for the owner to decide; part 52's `--damaged` check refuses a
  little more than needed; no test loops over all 231 Hangul letters.

### Part 54: status kbps, rawBytes, dropped (b7252bf)

- The browser display's half-second status fills in three fields that were
  always 0, each for its viewer: `dropped`, the frames it missed since it
  connected (replaced in its mailbox, or not sendable: too short for its
  canvas, no encoder, a failed encode); `rawBytes`, the bytes of raw frames
  sent to it since then, 16-byte headers included; `kbps`, what scterm wrote
  to its socket between two half-second statuses, in kilobits (1000 bits) per
  second, WebSocket headers included, the statuses not. Fixes #7.
- `wsConn.wrote` (`websocket.go`) counts each frame written whole; `sendLoop`
  turns it into a rate at each tick (`kbpsOver`); a status that answers a ping
  repeats it. The counters are atomics, with no lock. `web/check.mjs` prints
  the server's last status.
- Decided: `rawBytes` runs since the viewer connected, like `sent` and
  `dropped`; the window is the status interval; sound bursts dropped for a
  slow viewer are not frames, so not in `dropped`; the HUD keeps the page's
  own figures.
- Verified: 3 review rounds; web tests under `-race`, a load test; mutants.
- Open (#25): the ping test's first wait can take a status that rates only the
  hello reply, so it could fail on correct code (seen only with a 600 ms stall
  added); no test holds that text messages count toward `kbps`. Also: bytes
  the reader writes while a tick's own status is being written count in
  neither window; the window test cannot tell a fresh rate from a repeat of
  the stored one; and no test holds that the rate is over the time measured,
  not the nominal 500 ms.

### Part 55: dropping a cancelled paste's rest (814555c)

- After Ctrl+C cancels a paste, input is dropped up to the paste's end marker,
  however late it comes; before, it stopped after 250 ms without input, and on
  a slow link the rest was typed as keys (an Esc q in it quit). Fixes #10.
- Where the end marker never comes, Ctrl+C pressed again ends the dropping: a
  read of only 1 to 4 Ctrl+C, as key presses come. It and what was left of the
  paste are dropped, and the status row says "keys no longer dropped".
- 2 s after the cancel, while dropping, the status row says "Ctrl+C stops it:
  keys dropped until the paste ends", again every 2 s; the end marker then
  brings "paste ended: keys no longer dropped". Keep: `hintAt` stays set while
  dropping, so bytes held for the end marker never time out as an Esc; the
  notice leads with Ctrl+C, so narrow rows keep it (a test pins 40 columns).
- Decided: Ctrl+C again, not a longer quiet window, which would still type the
  rest on a slower link and drop the user's keys after every Ctrl+C when the
  end marker never comes; a lost end marker takes Ctrl+C twice (README).
- Verified: 4 review rounds; the real input loop on a pipe; fuzzing; mutants.
- Open (#20): a lost end marker drops the next paste whole without a word, and
  if the notice showed, its end marker even says "paste ended"; a Ctrl+C
  pressed just after the end marker, as the notice advises when the rest is
  late, reaches the device; a pasted run of 1 to 4 Ctrl+C arriving in a read
  of its own ends the dropping, and the rest is typed; Ctrl+C pressed during a
  slow paste ends it only where the terminal or multiplexer slips keys into a
  paste. Also: `captureStderr` and `captureStdout` read their pipe only after
  `fn` returns, so a test writing more than a pipe holds hangs (#21).

### Part 52: forgetting a damaged pairing (531fd36)

- `scterm forget --damaged` forgets pairings whose identity in `peers.json`
  does not parse, which no name or identity selects (before, only a hand edit
  removed them). It lists each as quoted data and asks once, [y/N], on stderr;
  without a terminal it lists them and refuses (exit 1). Fixes #9.
- `scterm peers` lists them after the devices, with the command that forgets
  them, and `--check` never probes them; doctor advises `forget --damaged`,
  then pairing again; `forget -h` and the README name the word.
- Keep: each goes by `Store.Remove` of its stored text, which only an
  unparseable record can have, so none whose identity parses is selected, even
  if the file changes before the answer (the store has no lock across
  processes, as before); it makes no `identity.pem`.
- Decided: the word is the whole query, exactly `--damaged`; a device named
  so, or with a name a paste can turn into it, is given by identity in printed
  commands (before, `forget --damaged` forgot one at once).
- Verified: 2 review rounds; the binary in a pty from script(1); mutants.
- Open: doctor's detail lines show damaged pairings' names cleaned, not
  quoted; `peers -h` and the README's peers bullet and Commands table don't
  mention them; `--peer` with only damaged pairings says none matches;
  `--check` and `--peer` look for a moved device by its identity as stored, so
  one stored in another form by hand is not found at a new address; with
  devices named `--damaged` and `--Damaged`, `forget --Damaged` forgets the
  first case-insensitive match without asking; `forget ' --damaged '` runs it
  too (the query is trimmed first).

### Part 51: device names shown as data (85c5b15)

- After `scterm pair` the command comes first ("Paired. Connect with: scterm
  --peer 'Pixel 8'"), then the device as data; before, "Paired with NAME" came
  first, where a name could pose as a command. Fixes #8.
- The data name is quoted, escaping control, format and blank-looking
  characters, `$`, the backquote and `!`, and cut at 32 cells. A printed
  command gives the name only if no part copied from inside its quotes can run
  (no shell character, every character shown as itself, at most 32 cells, no
  "scterm" even without quotes and marks of no width), else the identity.
- forget's question, Kept and Forgot use the short identity; a list of matches
  names each device as the command would; the `--peer` chooser shows
  `ID, named "NAME" (ADDR)`; an exact identity beats a pairing named after it.
  scterm cleans adb serials, doctor a not-ready device's serial and state, and
  `cleanPeerText` drops format characters too.
- Decided: names holding "scterm" go by identity; the cap counts what the
  command writes, so the lines after pairing fit 80 columns; scterm quotes
  with its own quoter, as `%q` keeps U+2800 and the Hangul fillers.
- Verified: 2 review rounds; quoted names pasted in 6 shell modes; mutants.
- Open (#19): a plain name can hold a lone word that names a program
  (`reboot`), which runs if that word alone is copied from inside the quotes;
  forget's refusal wraps for a long name; `scterm peers`, doctor, the
  connection and nearby failures and the declined-pairing message show the
  cleaned name uncapped, so blank-looking characters can push text onto a row
  of its own; `pasteArg` reads `peers.json` up to 5 times a pairing.

### Part 53: no silent stream left behind (fc7d19e)

- Once the session's sound has stopped while the picture goes on (any end but
  scterm's quit, codec id 0 included), `playAudio` closes this computer's
  sink, also one whose write failed: no silent `scterm` stream stays in the
  desktop's mixer, and the follower ends (a tick that begins after the close
  asks pactl nothing). Before, both lasted until scterm quit. Fixes #17.
- Terminal: Alt+M, Alt+- and Alt+= then leave the volume alone, and the first
  press says "the sound is off, so its volume stays as it is". Browser: the
  `gain` op changes nothing, and the status says `audio` off, `hostAudio`
  false, `gain` 100 and `muted` false.
- `--web` and `--window`: every end of the sound stream before the quit is one
  line, `scterm: audio: REASON`, cleaned, EOF and a reset connection included;
  when the device turns the sound off, the line stays
  `scterm: no sound from DEVICE`.
- Keep: `close` and `writePCM16` share `a.mu`, and the close comes after the
  writer has returned; `plays()` honours `closed`, `failure()` does not.
  Decided: a sink whose write failed keeps its follower until the sound stops.
- Verified: 2 review rounds; the close in each display under `-race`; mutants.
- Open: the displays keep different rules at scterm's own quit: the terminal
  and `--no-tui` still report a peer that closes mid-packet after the quit,
  where `--web` says nothing then. The `--audio-dup` scaling (#18) and
  `web/check.mjs`'s wording are in part 44's Open.

### Part 61: debug app next to the release (8b5fa95)

- Debug builds of the Android app are a separate app, "scterm debug"
  (`io.github.pwnedbygary.scterm.debug`), which installs next to the release
  with its own identity and pairings instead of replacing it:
  `applicationIdSuffix = ".debug"` on the debug build type only.
- A debug resource overlay names the app and its accessibility service; with a
  release out, a debug build says the release installs next to it (it advised
  uninstalling, which loses pairings); `devicetest/activate_helper.py` starts
  the helper from the APK of whichever of the two apps shows the command.
- Keep: only the application ID differs (the namespace, class names, binder
  descriptor, intent actions and protocol names stay);
  `${applicationId}.shizuku`, Shizuku's ComponentName, the accessibility check
  and the helper's CLASSPATH follow the build.
- Decided: no version-name suffix (the update-done notice compares the release
  tag with `VERSION_NAME`, and peers see it); both apps open `scterm://pair`
  links, and Android asks which.
- Verified: 2 review rounds; kotlinc with stand-ins; aapt2; Gradle left to CI.
- Open: no check that runs pins the release application ID, as AGP 9 runs
  `PackageNameTest` for debug only (#22); both apps take port 27300, the
  phone's name and one helper log file, and the texts on the accessibility
  service and Shizuku, and `android/README.md`, don't say which app's (#24);
  the README's paragraph on updates treats a debug build as a release with
  another key; a debug build made with `-Pscterm.releaseCert=` and the GitHub
  URL calls the release "not scterm".

### Part 58: a lock for the canvas pool (20dad0f)

- The terminal display's two-canvas pool (`takePooled`, `returnPooled`,
  `resetPool` in `stream.go`) was used from two goroutines without a lock: the
  video goroutine takes canvases and gives back those the mailbox evicts,
  while the run loop (terminal UI or `--no-tui`) gives back each one it took.
  A mutex, `poolMu`, now guards it; nothing changes on screen. Fixes #11.
- Decided: a mutex, not a channel: the tests read the `[2][]byte` slots, and a
  zero `streamState{}` needs no set-up. A miss allocates after the unlock, as
  `web.go`'s `poolGet` does.
- `pool_shared_test.go` drives the real sink and eviction against the real run
  loop (terminal UI and `--no-tui`), writing every canvas as the scaler does;
  under `-race` it fails on the old `stream.go`. A second test counts no
  allocation in steady state.
- Verified: 2 review rounds; `-race` at 1 to 16 CPUs, and shuffled; mutants.
- Open (#23): `resetPool` empties only the pool, so a canvas in flight at a
  resize comes back with the old picture, and after the terminal grows two
  small canvases can fill both slots, so every frame allocates until the next
  resize; the browser display gives no canvas back, so `runVideo` makes one
  per frame there; the test sees a shared canvas only through `draw`'s copy
  into `lastRGB`, as `packCells` reads it in C; `--no-tui`'s give-back could
  move ahead of the `--dump-frames` write unnoticed;
  `TestDroppedFrameBufferIsRecycled` compares only lengths. Also: `mapCell`,
  `frames()` and `currentFPS` read the video goroutine's state without a lock;
  no test from outside can catch `resetPool` unlocking between its two clears.

### Part 59: the libraries' licence texts (184d40e)

- The binary links Go's runtime and standard library, `golang.org/x/net` and
  `golang.org/x/sys` (BSD 3-Clause), and the APK the Shizuku API library
  (MIT); neither carried the licence text that must come with it. Fixes #16.
- `scterm licenses` prints a third text, `third_party/LICENSE.golang`: a lead
  naming the modules the binary links, then the BSD text, which Go's copy and
  each module's are byte for byte (not `LICENSE.go`: go would compile it).
  `third_party/LICENSE.shizuku`, Shizuku-API's `LICENSE` byte for byte, is in
  the APK as `assets/LICENSE.shizuku`; releases attach both `third_party/`
  files.
- AndroidX, Kotlin, kotlinx, JSpecify, JetBrains annotations and
  ListenableFuture are Apache 2.0, already covered; jcodec is only in
  `peerctl`, which no release ships; no checked artifact has a NOTICE file.
  Decided: `/META-INF/{AL2.0,LGPL2.1}` stay excluded: in the Gradle cache only
  JNA's jars carry them, and the APK has no JNA.
- Keep: tests hold `LICENSE.golang`'s module list to `go list`'s, its text to
  each module's and GOROOT's copy, and Shizuku's text to its SHA-256.
- Verified: 2 review rounds; tests, also in vendor mode; mutants.
- Open: AGP's default packaging silently drops a dependency's NOTICE files
  (AGP 9.3.2; the pinned 9.4.1 not tried), so a future dependency's NOTICE
  must be added by hand, unnoticed by any test; the Go licence check skips a
  module whose licence it cannot find, and an emptied vendored `LICENSE`
  passes (#22); no test pins the `licenses -h` prose; the new asset's packing
  in the APK was left to CI, and the GOROOT subtest was only emulated.

### Part 57: CI runs -race and the shell tests (cb02c66)

- CI's build job installs zsh and fish, in a step of its own that prints their
  versions, and runs `go test -race ./...` in place of the plain run, with
  `SCTERM_TEST_REQUIRE_SHELLS=1`. Before, the race detector never ran on CI,
  and the paste tests skipped zsh and fish there without a word; now all 7
  shell modes run. Fixes #12. The android and release jobs are unchanged.
- With the variable set, a shell that `pasteInto` does not find fails the test
  instead of skipping it, sh and bash included; locally a missing shell still
  skips. `TestMissingShellSkipsUnlessRequired` runs `pasteInto` in a child
  (`SCTERM_TEST_PASTE_SHELL`), with and without the variable.
- Keep: any non-empty value turns the variable on, as with the repository's
  other on/off variables; part 60's tests read it the same way.
- Decided: race only, as every test runs under it (no race build tags, no
  `testing.Short`).
- `docs/DEVELOPMENT.md` says CI runs `-race`, and names the shells and both
  variables; `docs/RELEASING.md` says the build job installs zsh and fish and
  runs `-race`.
- Verified: 1 review round; CI's command, all 14 shell subtests run; mutants.
- Open: the job installs no node, so an image without it would turn four tests
  (`TestWebClientSuites` and three that read the served page through node)
  into skips that a log without `-v` does not show, and no test guards the
  workflow's Test step (#22); the install step relies on the build-deps step's
  `apt-get update`.

### Part 50: suggested commands are safe to paste (bee1feb)

- "Connect with: scterm --peer %q" (after `scterm pair`) wrote the name as a
  Go string literal: pasted, its double quotes let a shell run `$(...)` or
  backticks and expand `$VAR` or `!`. `pasteArg` (`peermode.go`) now gives the
  argument for that line and for `scterm forget`'s refusal (since part 60, its
  rule, in `pasteArgFor`, also gives completion's): the name in single quotes
  (`'\''` for each `'`) when it is plain and selects this pairing alone; else
  the short identity, or the whole one, when that does; else no command, and
  the line says so.
- Plain: printable UTF-8, no backslash (fish reads one inside single quotes),
  no space at either end or two together, not a help word (parts 51, 52 and 63
  add rules). Keep: every name is single-quoted, even `L` or `...`, which an
  interactive zsh would expand as global aliases.
- forget's refusal puts no device text before its command, where a name
  could plant a fake one.
- Verified: 3 review rounds; pasted into 7 shell modes, nothing ran; mutants.
- Open: a lone word that names a program, copied from inside the quotes, still
  runs (part 51's Open, #19); with the user's own text, `update.go`'s "mv"
  line to go back quotes paths with `shellQuote`, which fish misreads for a
  path holding a backslash, and `flags.go`'s hint for an unquoted `--peer`
  value quotes it with `%q`.

### Part 49: adb mode runs the server built from the vendored source (ed3d204)

- The binary embedded the official scrcpy 4.1 server jar, without the vendored
  source's one patch (`afcd41b`: playback capture also takes `USAGE_GAME` and
  `USAGE_UNKNOWN`), which the Android helper has. Owner's call: rebuild the
  jar from the patched source.
- `third_party/build-scrcpy-server.sh` builds it offline (JDK 17,
  android-37.0, build-tools 36.0.0) and records how in
  `third_party/scrcpy-server.jar.d/scrcpy-server.provenance`. `--check`
  builds again and fails, naming what differs, unless it gets the
  embedded jar byte for byte; CI's android job runs it.
- Keep: d8 runs with `--release --min-api 21 --lib`, not the upstream script's
  `--classpath` (with it, the server's lambdas call `$-CC` classes no device
  has); the jar has a fixed date and no manifest; another JDK builds other
  bytes.
- Keep: `VERSION_NAME` stays 4.1, as `Options.parse` compares it with what
  `serverVersion` passes. Go tests pin the jar's sha256, the source tree
  hash and both version names to the provenance.
- Verified: 4 review rounds; rebuilds; mutants; six sessions on a phone.
- Open: game sound capture and Android 5 to 11 are untested; CI's JDK may
  build other bytes; with the script, the provenance and `serverVersion` all
  set to another version but the jar not rebuilt, only `--check` fails (#15).
  With `--audio-source=playback`, Alt+N opened no notification shade, with the
  official server too (#13). The script's header does not name the Apache 2.0
  licence of the upstream script it adapts.

### Part 46: the sound status tells the truth after audio errors (34f3250)

- The session's sound has one account, `streamState.soundEnd`, read
  through `soundStopped()`. `runAudio` records any end but scterm's quit:
  the device turning the sound off (codec id 0), an audio config error
  (codec id 1), a codec scterm cannot decode, a codec id cut short, and
  the stream ending or failing while the picture goes on. Before, only
  codec id 0 counted, and after any other end every display said "on".
- The browser's hello and status (`soundOn()`), the terminal's status row
  (`audio off`) and `--no-tui`'s line (`audio=off`) all read it.
- Keep: the stream gets the app's `quitting` flag and records no end once
  it is set; the error alone cannot tell a quit, as `isClosedConnErr`
  also matches EOF.
- `sendStatus` and the `gain` op read the sink through `failure()`, so a
  sink whose write fails mid-session shows as one that never opened.
- Verified: 3 review rounds; six ways the sound stops, each display; mutants.
- Open: nothing.

### Part 44: a browser-only session leaves this computer's sound alone (10bbfbf)

- A session whose only sound output is the browser (`--web` or
  `--window` without `--audio-dup`) does no sound work here: `newApp`
  opens a sink only when the session plays here (`playsHere`), and gives
  a browser-only one `browserOnlySink`, never opened: no pactl, no
  libpulse connection, no stream in the mixer, no follower.
- Keep: `browserOnlySink` has no error, so `failure()` stays nil and a
  failed sink stays distinct (part 46 relies on it); it is silent, so
  `plays()` is false, and `audioFollower` asks `plays()`.
- The browser still gets every packet (its PCM tap does not go through
  the sink); the `gain` op is taken and reported, and scales nothing.
- Start-up line: "scterm: audio: browser only (pass --audio-dup to play
  it on this computer too)"; the failed-sink line is for `--audio-dup`.
- Verified: 2 review rounds; fake sound servers and pactl; mutants.
- Open: with `--audio-dup` the sink scales the shared PCM buffer in place
  before the browser's tap, so a `gain` op also changes what the browser hears
  (no shipped page sends one; #18); `web/check.mjs` still says "host sink
  stays silent" whenever `hostAudio` is false.

### Part 45: pactl is read the same in every language (59d89d7)

- pactl translates what scterm reads from it ("Sink Input #N", "Sink: N",
  `pactl info`'s field names), so under another language the follower
  found no scterm stream and never moved the sound. `cLocaleEnv`
  (`pulsecli.go`) gives every pactl run `LC_ALL=C` without `LANGUAGE`,
  keeping the rest of the environment: `pactlTool.run` (the follower,
  `pulseCmd`, `pulseDefaultSink`) and doctor's `doctorOutput`.
- Why C is enough: gettext translates nothing in C and ignores
  `LANGUAGE` there, and names, descriptions and properties came out
  byte-identical in C, C.UTF-8, en_US.UTF-8 and ja_JP.UTF-8.
- The start-up cleanup uses the libpulse API, not pactl: unchanged.
- Verified: 1 review round; mutants; real pactl, stand-in Japanese catalog.
- Open: nothing.

### Part 47: device text cleaned in the last places (de030e8)

- Device text reaches the terminal through `cleanText` in the command line's
  last places: `Store.Find`'s error for several matches, the `--peer` chooser,
  `scterm pair`'s chooser (names from a network search anyone can answer), its
  prompts, failures and "Paired with NAME", a refused
  `scterm pair "HOST:PORT CODE"`, `scterm forget`'s question and results, and
  adb's stderr in `adbRunner.run`'s errors (since part 51, some of these show
  the name as quoted data or the short identity instead).
- `cleanText` turns each run of control, format (bidi, zero-width) and space
  characters into one space and trims the ends; `cleanPeerText`
  (`scterm peers`, `--peer` failures, doctor) only deletes control characters
  (since part 51, format ones too) and trims.
- Keep: names are stored and matched as the device gave them; only what
  is shown is cleaned, by the caller (the peer package cleans nothing;
  `Store.Find` returns `*peer.AmbiguousError`).
- Verified: 1 review round; hostile inputs, fake target and adb; mutants.
- Open: nothing.

### Part 48: the scrcpy server's licence comes with every release (252fde7)

- scterm (MIT) bundles the scrcpy server (Apache 2.0) in the Linux binary
  and in the APK, and neither carried the Apache text that its §4(a)
  asks for. `scterm licenses` (`licenses.go`) prints `LICENSE` and
  `third_party/LICENSE.scrcpy` whole (embedded with `go:embed`), since
  `scterm update` fetches only the binary. It is in `scterm -h`, the
  `commands` list and the README.
- The release job attaches `LICENSE` and `LICENSE.scrcpy` beside the
  binary and the APK; both updaters pick their asset by name.
- The APK carries both as `assets/LICENSE` and `assets/LICENSE.scrcpy`
  (copies under `android/app/src/main/assets`), and a Go test keeps them
  identical to their sources. Keep: the copy's final blank line, which
  `.gitattributes` stops `git diff --check` from flagging.
- Since part 59 there are two more texts: `LICENSE.golang`, which
  `scterm licenses` also prints, and `LICENSE.shizuku`, which the APK carries;
  the release job attaches both.
- `third_party/README.md` lists scterm's changes to the vendored server;
  a test keeps the list equal to the files marked `scterm patch`.
- Verified: 1 review round; tests, mutants, aapt2; no CI run or APK build.
- Open: nothing.

### Part 43: two noise messages from the second phone run (afe9ebe)

- Seen on the second real-device run: `scterm --no-tui` printed "audio
  stream ended: ... use of closed network connection" on a normal Ctrl+C,
  and the sound follower said "audio -> SINK" at start for the sink the
  stream already played on.
- `runHeadless` now sets `quitting` when its loop ends, and its audio
  goroutine takes `watchStreams`' rule: a closed-socket error after the
  run has ended is not printed; EOF before then, and other errors, are.
- The follower moves the stream, and says so, only when pactl shows it
  elsewhere than the default sink: `sctSinkInputs` reads each stream's
  `Sink:` index, and `sinkIndex` reads the default's, asked only when the
  default changed or after a failed move. Without both indices it moves
  as before. A failed move is tried again at the next tick.
- Decided: each move is said (`noticef`, not `logOnce`), so switching A,
  B, A, B says every switch; the exit replay counts repeats.
- Verified: 3 review rounds; a fake pactl; mutants; on a phone, both gone.
- Open: the follower still moves the stream to a Bluetooth or virtual
  default that the start-up choice passed over (now only when the stream
  is elsewhere); a move that keeps failing costs a third pactl call per
  tick; `--no-tui` starts no follower.

### Part 42: the picture's scaler stays inside the picture (af78346)

- libswscale's vector code converts whole blocks of pixels. Scaled into
  the terminal's canvas, an even width that is not a multiple of 8
  spilled 2 to 6 pixels into the margin (a stray stripe at 80x24); at the
  frame's own size (screenshots, `--dump-frames`, every browser frame) a
  1080-wide frame wrote 32 bytes past a buffer of its exact size, into Go
  memory, and a 34- or 578-wide one left pixels unwritten.
- `sct_vdec_scale` (`native.c`) now scales into a frame the decoder owns
  (rows padded to 64 bytes, made again when the size changes) and copies
  exactly `dst_w` pixels of each row, so nothing outside the region is
  written whatever the block size; it returns -1 when `sws_scale` fails.
- `scale_test.go` scales into marked buffers with guard bytes: every even
  width from 2 to 64, resizes, a picture ending at the buffer's end, and
  two embedded frames at their own size. Colours may be up to 32 levels
  off, to allow for other libswscale versions.
- Cost: about 1% more of a core for a 1080x2400 browser frame at 60 fps.
- Verified: 2 review rounds; `-race`, `-asan`, mutants; one CPU and FFmpeg.
- Open: after a failed `sws_getContext`, a call at the old size passes a
  NULL context (out of memory only); `native.h` still says RGBA for the
  BGR0 output; the result guard and the allocation retry have no test.

### Part 41: the MIT License (ea39b83)

- Owner's call: the MIT License. `LICENSE` holds the standard text with
  "Copyright (c) 2026 Gary Bagley": the full name on most commits, and the
  year of the first.
- The README ends with a License section (in its table of contents):
  scterm, the Android app included, is MIT; the scrcpy server that both
  bundle keeps the Apache License 2.0 (`third_party/LICENSE.scrcpy`), and
  other third-party files, such as the Gradle wrapper, keep theirs.
- Verified: 7 review rounds, most of them moves; GitHub's MIT template.
- Open: no test checks that the README's table of contents lists every
  section.

### Part 39: screenshot tests, held Alt+S (188441e)

- An Alt+S press within 200 ms of the Alt+S press before it, taken or
  not, is the key's repeat and takes no screenshot (`screenshotKey`,
  `shotKeyRepeat`). 200 ms sits above the usual repeat gaps (25 to 50 ms)
  and below the usual first-repeat delays (250 to 600 ms), typical
  defaults not measured on a real terminal: at those rates a held Alt+S
  saves one screenshot or two, and with repeats 200 ms or more apart,
  one per repeat unless a shot is still in flight. The bar's Shot and
  the menu's row are never repeats; the time is a parameter, as in
  `quitArmed`.
- A session with no video socket says what the placeholder says: "the
  video is off", or "no video from DEVICE" (the name cleaned).
- Tests catch their regressions at any CPU count, and a slow PNG test
  fails alone (`shotTestWait`, 30 s).
- `web/input_test.mjs` runs on its own clock (`elapse(ms)`): on this host
  ananicy-cpp runs every node process under `SCHED_IDLE`, so any load on
  its CPU made the three-finger tap look too long.
- Verified: 6 review rounds; mutants; part 43's phone run: held Alt+S, two PNGs.
- Open: the repeat window uses the time the run loop handles a press, so
  a loop stall over 200 ms during a hold can take one shot more; no test
  shows a bar or menu press inside an open window leaving it unmoved.

### Part 40: fixes from the first real-device run (c89fd16)

- doctor runs commands only on the device scterm would use (the one
  `ANDROID_SERIAL` names, or else the only ready one) and lists other
  ready devices from `adb devices -l` alone: on the run it ran `getprop`
  on two devices another program was using. With several ready and none
  chosen it reads none, and warns instead of failing.
- `--no-tui`'s statistics line leaves out `pcm`, `peak` and `avlag`
  without sound (the run printed `avlag=214994453ms`); `avlag` also
  needs a video packet. The line is padded to the longest one printed.
- The terminal's fps shows the stream's rate as each frame carries it
  (`videoFrame.fps`, kept in `app.streamFPS` by the run loop), not the
  mean of the run loop's last 32 frame times, which counted a still
  screen's pauses.
- Verified: 7 review rounds, most moves; mutants; part 43's phone run, all 3.
- Open: `avlag` does not match the delay heard (#14), and the comment on
  `statistics` omits that the first packet of Android's default Opus and FLAC
  encoders keeps the encoder's stamp (`AudioEncoder.fixTimestamp`). Also:
  every display holds its last rate while no frame comes; `statistics` reads
  `currentFPS` and `audioBytes` off their goroutine; doctor's advice says
  "choose one with -s SERIAL", which doctor does not take.

### Part 38: web loose ends and test isolation (31f5214)

- `--window`: no browser opens once a stop has begun, and none outlives
  the stop. `openWindow` returns at once when a stop has begun. Keep:
  `startWindow` checks `closing` again under `winMu` and holds it until
  the window is recorded, and the stop sets `closing` first and looks
  for the window under `winMu` (`killWindow`), so either the stop finds
  the browser or the browser never starts.
- A viewer refused with the stop's bye counts as connected (`hadClient`):
  a refusal recorded before the stop or the window's watcher reads it
  removes the browser's log as chatter, and one recorded after both
  changes nothing. Nothing orders them, so the log of a failure the
  watcher has just reported can still be removed.
- Test isolation: host tests empty `SCTERM_WEB_HOSTS`;
  `TestWebExitsTwoWhenTheDeviceIsLost`, `TestWebStreamEndSaysBye` and
  `webLinesUntil` use `isolatePulse`; `setStderr` swaps `os.Stderr`
  under `notices.mu` (a leftover viewer's notice raced the captures);
  `SCTERM_TEST_NO_PRESENCE` makes a child `main` note its presence
  searches in a file instead of searching the network.
- `docs/DEVELOPMENT.md` names some tests that catch a race only under `-race`.
- Verified: 9 review rounds, most of them moves; `-race -shuffle`; mutants.
- Open: a stop reports a window it closed within 15 s (`windowGrace`),
  before any viewer came, as a browser that failed (`watchWindow` could
  stay quiet once `closing` is set); `setWindow` is used only by tests;
  nothing makes a new `runWeb` test isolate itself.

### Part 37: terminal and command-line loose ends (864ce6b)

- Device text no longer reaches the terminal raw in three more places,
  each through `cleanText`: the server's last lines in start errors
  (`serverOutputNote`), the reason and name in `endSession`'s peer line
  (a name that cleans to nothing reads "the device"), and peer notices.
- The sound sink's error has one accessor, `failure()`: `err` holds only
  open's error or `errNoSound`, set before the sink is shared, and a
  failed write sets the atomic `writeFailed`.
- doctor counts only pairings peer mode can use (an identity it can load,
  fingerprints that parse), through the new `peer.LoadIdentity`, which
  never creates one. Its advice says `--peer NAME`, or `--peer=` with one
  usable pairing, and scterm's "adb not found" and "no device found"
  hints say `--peer NAME`: each can be typed as shown.
- README: `PULSE_SERVER` and `PULSE_SINK` rows; what Ctrl+C in a paste
  drops. `readmeFlags`, the README flag test's reader, skips fenced code,
  indented code and HTML comments, and splits a row into cells first, as
  GitHub does; its doc comment lists where it still differs.
- Verified: 11 review rounds, nearly all on the README reader; mutants.
- Open: the reader misses, among others, flag tables in nested lists or
  block quotes, and rows with one pipe.

### Part 36: sound work only for sessions that play here (c11cf92)

- main silences other scterms' leftover sound streams
  (`cleanupStaleStreams`) only when the session plays its sound on this
  computer (`playsHere`): not with `--no-audio`, not browser-only (no
  `--audio-dup`), and not from a peer target that grants no sound. A
  session with no sound stream gets `noAudioSink` (`errNoSound`), never
  opened: nothing reaches the sound server, no follower, no start-up line.
- Keep: the cleanup runs before `newApp` opens the session's own sink
  (`main.go`), or the session would silence its own stream; tests pin it.
- In peer mode a session has sound only with an audio socket; its status
  row then says `audio off`, not `vol 100%`.
- The browser's `hello.audio` and `status.audio` say whether the
  session's sound is on (`"error"` is gone), and `hostAudio` whether this
  computer plays it. Decided: per session, not per viewer's `caps`. The
  HUD says `audio off` for anything but "on". The reason in `scterm: no
  sound from ...` is cleaned.
- Verified: 2 review rounds; `main` run with a fake sound server; mutants.
- Open: main decides at start, before scterm reads the device's codec
  id, so a device that connects its sound socket and then turns the
  sound off (Android 10 over adb) counts as playing here; no line says
  at session start that a paired device grants no sound.

### Part 35: terminal screenshots at the stream's resolution (08bdda6)

- Alt+S, the Shot button and the menu row save the device's picture at
  the stream's resolution as `scterm-YYYYMMDD-HHMMSS.png` in the current
  directory, as the browser does; a taken name gets `-2`, `-3` and so on
  (`O_EXCL`: never replaced). `--dump-frames` still writes PPM.
- The run loop asks the video goroutine, which owns the decoder
  (`requestShot`, `answerShots`), to scale its frame at its own size.
  Keep: the PNG is encoded and written on a goroutine of its own, so
  neither the decoder nor the run loop waits on it (no test pins where
  the encoding runs).
- A still screen sends no frames (AOSP repeats the last one at most 10
  times, 100 ms apart), so a request also sets a past read deadline on
  the video socket to wake the read (`shotReader`). Keep: register the
  request before setting the deadline, and clear the deadline before
  answering; any deadline expiry there counts as a wake, so a liveness
  deadline on that socket would be defeated.
- With no answer within `shotWait` (1 s) the canvas shown at the press is
  saved, cut to the picture, and the message says so.
- One shot at a time: a press while one is in flight takes none (a held
  Alt+S used hundreds of MB).
- Verified: 2 review rounds; the real decoder; wake-order tests; mutants.
- Open: a process that exits during the PNG write can leave a partial
  file.

### Part 34: the web port is checked before the device (b7e5b94)

- With `--web` or `--window`, also with `--peer`, main listens on the
  browser display's address and port right after the flags and the
  version line, before any device or peer work, and a port it cannot use
  ends the run there (one line, exit 1). The display then serves that
  listener (`s.ln`); `--web-port 0` still picks a free port.
- The line says what to do for a port in use (words shared with doctor),
  one that needs privileges, and an address that is not this computer's;
  other errors keep `web: listen on ADDR:PORT: ERR`.
- Fixed an older data race: `run()` set `s.http` while `stopWith` read it
  on a signal. `s.ln` and `s.http` are now published and read under
  `srvMu`, and `run()` gives up when a stop has begun.
- A browser that connects during device start-up now waits in the accept
  queue and is served once the display starts (it was refused).
- Verified: 4 review rounds; `main` with a fake adb and a fake peer; mutants.
- Open: `validateConfig` does not range-check `--web-port`; doctor checks
  only 127.0.0.1:6969.

### Part 33: scterm update follow-ups (684f313)

- `scterm update` keeps the previous binary as `<path>.old` before it
  prints "Updated ...", so `scterm update -y | head -1` (SIGPIPE) no
  longer leaves it under a hidden name (unless `.old` is taken: then the
  hidden copy stays, and the output names it).
- `update --check` on a release without a linux/amd64 binary says so and
  exits 0.
- Release notes lose whole escape sequences (CSI, the OSC, DCS, SOS, PM
  and APC strings, other ESC sequences), not only their control
  characters; an unfinished one hides nothing past its line.
- SIGINT, SIGTERM and SIGHUP from the download on remove the download,
  the copy and a binary staged for sudo, leave the binary and any `.old`
  as they were, and are raised again. Signals ignored at start stay
  ignored, except SIGTERM, which Go's runtime takes over.
- Keep: the swap is the point of no return; a signal after it waits
  until the old binary is `.old` (or named) and the lines are printed.
  The swap goes through `rename`, a package variable, so tests can
  interrupt there.
- Verified: 7 review rounds; real signals to a child process; mutants.
- Open: a source build's update catches no signal (Ctrl+C can leave
  `<path>.new`); in release notes a C0 character inside a CSI ends it
  early, and CAN, SUB or a C1 control inside a string does not end it.

### Part 30: scterm doctor (9598a59)

- New `scterm doctor` (`doctor.go`): an `ok`, `warn` or `FAIL` line per
  check with what to do, printed as each check ends; exit 1 if a check
  fails. It checks the build (AVX2), adb, each listed device (release
  and SDK against the README's limits; since part 40, only the device
  scterm would use is read), the terminal display's sound, `--window`'s
  browser and display, the web port and `SCTERM_WEB_HOSTS`, and peer
  mode's identity and pairings (read only). Each program runs in the C
  locale with a 10 s limit.
- `ANDROID_SERIAL` is matched in every form adb takes for `-s`, by ports
  of adb's own matching (`matchesTarget`, `parseNetAddress`,
  `pickTarget`, from AOSP adb and libbase at pinned revisions).
- Decided: FAIL only for what stops a session from starting on this
  computer; sound, window and web problems warn. With a paired device, a
  missing or broken adb and "no device" only warn, and offer `--peer`.
- Keep: doctor shares its messages and helpers with scterm (`adb.go`'s
  hints, `windowBrowser`, `pulseSocketPath`, the display's
  `webHostNames`), so the two agree.
- Verified: 3 review rounds; fake adb and pactl; decoy sockets; mutants.
- Open: terminal capabilities are not checked; doctor repeats
  `browserEnv`'s display note, which is wrong when `DISPLAY` is set but
  its socket is missing; `adb devices -l` may start the adb server.

### Part 31: follow-ups to part 28's reviews (6b908b5)

- The page (`/`) is sent with `X-Frame-Options: DENY` and a CSP ending in
  `frame-ancestors 'none'`: a page opened as `127.0.0.1` passes the Host
  check, so any website could frame the live page and trick the viewer
  into clicks and keys that reach the device. Only the page carries them.
- `run()` takes its names from `allowedNames(ln)` (the listener's
  address, `--web-addr`, `os.Hostname` through `osHostname`) and listens
  through `webListen`; tests replace both.
- `SCTERM_WEB_HOSTS`: each ignored entry is reported at start, quoted
  with `%+q`; a non-ASCII entry is told to use its `xn--` form (no
  conversion: `x/net/idna` needs `x/text`, which is not in `go.sum`). A
  name with an empty label (`.example.lan`, `box..lan`) is ignored, and
  a Host with one gets the 403.
- README: names in `SCTERM_WEB_HOSTS` or given to `--web-addr` are
  accepted with any address; the safe way is the default address, no
  names, and an SSH tunnel.
- Verified: 2 review rounds; mutants; headless Firefox refuses the frame.
- Open: the page's files and error responses carry no frame headers, so
  a later HTML route must add them; the Host reader still serves
  `a..b.localhost` and `.localhost` through the `.localhost` rule; the
  `xn--` hint comes for any non-ASCII character, an invisible one too.

### Part 32: follow-ups to part 19's reviews (f9fc5b4)

- Keyboard focus on a control of the bar holds the HUD and the bar up only
  after the session has ended, when keys are the browser's. During a session
  the chrome fades 2.5 s on and the inert bar takes the focus with it (not
  tried with a screen reader). Decided: the focus does not move to ☰ instead:
  ☰ never fades, so its focus ring would stay over the picture.
- In touch mode a key, a soft keyboard's or a hardware one's, no longer
  brings up the HUD and the bar; ⋯ still shows the bar.
- `pcm-worklet.js` counts one underrun per block it cannot fill
  (`starve()`), on its frame-for-frame and resampling paths alike; the
  sound is unchanged. Blocks are 128 frames in the worklet and 2048 in
  the plain-http fallback, so those two counts still differ.
- A sound chip whose words change and come back within 6 s shows for 6 s
  from their return. New checks pin most of what part 19's reviews left
  unpinned.
- Verified: 2 review rounds; node suites, mutants; headless Chrome.
- Open: in touch mode the wheel still shows the chrome while a key does
  not, so a hardware keyboard reaches the bar only through ⋯; ☰ keeps a
  focus ring after Esc closes a sheet it opened; the ring never
  re-buffers after starving, and while resampling it reports one frame
  queued once the stream stops.

### Part 29: terminal sound without pactl, and on a running sound card (7b9106c)

- The terminal display's sound opens without `pactl`: libpulse refuses
  "" for the default server and sink, so "" now reaches it as NULL
  (`cstrOrNil`); the stream opens on the server's default sink, and
  `PULSE_SERVER`, `PULSE_SINK` and client.conf are honoured.
- `openFirst` tries each server (libpulse's default, then the
  `XDG_RUNTIME_DIR` socket) with each sink, in up to three rounds, and
  gives up after about 0.45 s without a server (1.8 s before). A running
  sound card is now preferred, as the code meant (`splitFields`: `pactl
  list sinks short` is tab-separated, and its sample spec has spaces).
- Keep: the runs find `pactl` and its environment before they start the
  follower (`audioFollower`) and give it a stop channel; tests stay off
  the developer's sound server through `isolatePulse`.
- Owner's calls: a server that refuses connections while it starts gets
  about 0.45 s, not up to 1.35 s; scterm may now autospawn PulseAudio
  where that is on (not tested).
- Verified: 5 review rounds; a private PipeWire; a decoy socket; mutants.
- Open: `pactl` always asks the `XDG_RUNTIME_DIR` socket while the stream tries
  libpulse's default first; after named sinks the server's default is never
  tried (on PulseAudio a named sink that fails everywhere leaves no sound); the
  success notice says `device "default"` when `PULSE_SINK` chose the sink; on
  PipeWire, `SCT_AUDIO_DEVICE` naming the default sink at start does not keep
  the stream there when the default changes; when a server answers, the
  leftover-stream cleanup takes about 0.6 s per session that plays here.

### Part 27: a way out of a paste, and true paste notices (141cd3e)

- A Ctrl+C byte anywhere inside a paste cancels it ("paste cancelled by
  Ctrl+C"): nothing is sent, and input is dropped up to the end marker or
  until no read comes for 250 ms (since part 55, up to the end marker only, or
  a second Ctrl+C). A paste unfinished for 2 s shows "Ctrl+C cancels the
  unfinished paste". The decoder's deadlines use the monotonic clock.
- An Esc right before a start marker is Back, then one paste; ESC O
  followed by ESC is ESC O alone (an Esc cannot end an SS3 sequence).
- A paste to a paired device goes only while this computer holds the
  lease (`peer.Session.Lease()`), else "paste not sent: no control
  (--takeover takes it)"; adb sessions have no lease.
- The typed fallback takes at most 4096 bytes and refuses a longer paste
  whole (the clipboard path: 262,130). Paste notices are not replayed at
  exit. `repaint` frees the UI lock with `defer`: a panic left it held.
- Decided: a 250 ms pause, as a longer one drops the user's own keys after
  every Ctrl+C (part 55 removed the pause); 4096 is a judgement (a long URL, a
  key, a page).
- Verified: 3 review rounds; the real input loop; a fake clock; mutants.
- Open: a start marker that comes more than 25 ms after its Esc is Back, then
  the rest of the marker and the paste are typed as keys; after a cancelled
  paste whose end marker is lost, the next paste is dropped whole (part 55's
  Open, #20); a complete paste more than 2 s in transit shows the hint, and a
  Ctrl+C typed then reaches the device; ESC O alone waits for its third byte
  with no limit; the browser's paste is unchanged.

### Part 19: the browser player on phones, its looks and accessibility (fd9e086)

- On a touch screen a finger on the picture no longer brings up the HUD
  and the bar (a bar under it took the next tap); a new ⋯ button shows
  the bar until it is pressed again or the picture is touched. Touch mode
  is a coarse pointer at load, then whichever pointer pressed last. With
  a mouse the chrome fades after 2.5 s, not while the mouse rests on the
  bar; ☰ always shows and is never inert.
- The bar is one row that scrolls sideways; only its buttons take clicks.
  The page keeps to the safe area, can be pinch-zoomed again (the picture
  keeps `touch-action: none`), and has Full screen buttons where the
  browser has the API (F11 stays the device's). `player.css` is rebuilt
  on custom properties, with 44 px touch targets and more contrast.
- Sound chip: a failed start says "Sound failed · Alt+M" and stops
  listening (a click or Alt+M tries again); the context's real rate is
  read back and the ring resamples (`processorOptions.ratio`).
- After a bye, Alt+/ and Alt+S still work (`ENDED_ACTIONS`) and other
  keys are the browser's. A held toggle chord runs once per press.
- Keep: browser runs for this project are headless, or headful only on a
  private Xvfb display with `WAYLAND_DISPLAY` unset (an earlier run opened
  Chrome windows on the owner's desktop).
- Verified: 2 review rounds; node suites; Chrome and Firefox; no phone.
- Open: on a portrait phone the bars beside the picture are about 5 px
  wide, so there is little room to pinch the page.

### Part 28: the browser display refuses names it is not served by (ba38ad3)

- DNS rebinding let a website that points its own name at this computer
  load the page and drive the device, as the WebSocket check only
  compared Origin with Host. Now `hostCheck` wraps the whole mux: every
  request must name the server in its Host by an IP address, `localhost`
  or a `.localhost` name, the `--web-addr` name, or a name in the new
  `SCTERM_WEB_HOSTS` (comma-separated), on any port. Others get a 403
  with one line: open it by address, or add the name to the variable.
- Keep: this computer's own host name (with or without `.local`) is
  allowed only when the server listens off loopback; at the default
  127.0.0.1, anyone on the network who can answer for that name (mDNS has
  no authentication) could rebind it.
- Each refused name is reported once, for the first 16 names, then one
  line; not through `logOnce`, whose map never shrinks.
- Verified: 3 review rounds; rebinding headers in tests; mutants.
- Open: `--window --web-addr 0.0.0.0` opens the placeholder address
  `http://<this-host>:PORT/`; the 403's suggested address can be unusable
  from the browser (a zoned IPv6 address, an SSH tunnel); the names are
  read once at start; behind a reverse proxy, the proxy must pass Host
  through and be named in `SCTERM_WEB_HOSTS` (from reading the code).

### Part 26: loose ends (a64b14a)

- `--audio-dup` does what scrcpy's does: without `--audio-source` it asks
  for playback capture (the sound keeps playing on the device, Android 13
  and later); with another source, or `--no-audio`, it is refused before
  any device work. Peer mode is exempt (a peer gets neither flag).
- When the device turns its audio stream off (codec id 0), scterm prints
  `scterm: no sound from DEVICE`, the server's last audio warning (it
  waits up to 1 s for it), and, when `--audio-dup` chose the source, that
  playback capture needs Android 13 or later; the session goes on.
- `--dump-audio` with `--audio-codec raw` writes the packets as sent (it
  wrote nothing). `scterm peers` with no pairings also offers `scterm
  pair`.
- Questions (update's, forget's, the `--peer` chooser, the nearby pairing
  code) go to stderr, so a redirected stdout cannot hide them.
- Verified: 3 review rounds; `-race`; mutants; no device or peer target.
- Open: the X fallback note is wrong when `DISPLAY` is set but its socket
  is missing (it checks `/tmp/.X11-unix`); `scterm pair` keeps its status
  lines on stdout; `closeAudioDump` is never called and dump write errors
  are ignored; the browser's sound chip has no state for a device that
  sends no sound; an audio-only terminal session goes on, silent, after
  the device turns its sound off (as in scrcpy); the Go client ignores a
  peer welcome's `audioError`; `android/devicetest/ui.py` defaults ADB to
  the macOS SDK path.

### Part 25: browser viewers share frames safely (e0919d4)

- With two or more viewers, a frame buffer could go back to the pool more
  than once and be handed out twice, so a page could get a damaged
  picture. Each frame now counts its holders (`videoFrame.refs`):
  `frame()` holds it while handing it out, each viewer's mailbox takes
  its own hold (`offer()`), and `releaseFrame()` returns the buffer with
  the last hold. A viewer lets go once its sender has encoded or copied
  the frame (before the socket write) or refused it, when a newer frame
  replaces it, or when the viewer closes. `frame()` still allocates
  nothing and never waits.
- `nClients` is stored inside the locked section that changes the list.
- A viewer whose upgrade ends after `stopWith` took the list is closed at
  once with the stop's bye (`stopBye`).
- `quit()`, which the `--window` leave timer calls, waits for room in the
  app's event queue, or for the server to stop, instead of dropping it.
- Verified: 4 review rounds; lock-holding and load tests; `-race`; mutants.
- Open: `sendStatus` reads the frame rate and video size, which the
  demux goroutine writes, without synchronisation; every JPEG viewer
  encodes the same frame again; `stopWith` does not wait for a refused
  late viewer's bye.

### Part 21: the docs (f1c25f3)

- `README.md` is rebuilt for a first-time user: what scterm is,
  requirements, install, quick start, the displays, controls (one table
  from the shared catalog), peer mode, updating, troubleshooting, a
  reference, how it works, development and credits. It no longer links
  this file.
- New `docs/ARCHITECTURE.md`, `docs/DEVELOPMENT.md` (which links this
  file) and `docs/RELEASING.md`; the plan moved to
  `docs/history/ANDROID_CLIENT_PLAN.md`, marked historical.
- Claims were checked against the code: AVX2 for every amd64 build,
  Linux only, the LAN warning (no password or encryption), the Ctrl keys
  in each front end, screenshots, `update`, `forget`.
- `readme_test.go` (`TestReadmeListsEveryFlag`): the README's flag tables
  name every flag `newFlagSet` defines, once, under the heading
  `scterm -h` puts it under; `--mirror-fps` stays out.
- Verified: 5 review rounds; a Markdown link checker; README mutations.
- Open: the README says `--peer` has not been run against a phone, which
  is still true; the browser's pointer effects (hover ring, drag trail,
  touch ripple) are not described; `android/README.md`'s readiness
  section reads like implementation notes; the browser cancels Ctrl+V's
  keydown, trusting that the paste event follows (untested).

### Part 20: Android follow-ups (33d89a8)

- Contrast, by sRGB compositing over a white picture: the viewer's
  statistics text is white (5.74:1); a held bar button keeps its fill and
  lights a 1 dp `#80FFFFFF` outline, since in the blurred panel a pressed
  fill of about 20% white or more falls under 4.5:1.
- The other settings launches go through `openSettings()`, which toasts
  where no screen answers; the nearby-pairing dialogs are dismissed in
  `onDestroy`; the viewer overlay's Retry and Close lose their capitals.
- Voice control: a bar button's name is its label when that contains the
  shown text, else "shown text, label" ("Vol-, Volume down"). Keep: only
  Keyboard and Screenshot are local actions on the bar, the two that
  `ViewerActivity.perform()` runs; a test pins it.
- The restricted-settings hint says to tap the greyed switch once first:
  App info offers the menu item only after that.
- Both busy rejects use `Readiness.TOO_MANY_SESSIONS_REASON`, so the wire
  text is unchanged. Comments say the helper's server is upstream plus
  one "scterm patch" (`AudioPlaybackCapture.java`).
- Verified: 2 review rounds; aapt2, kotlinc and tests on stand-ins; lint.
- Open: inline text not moved to resources, some with "peer"; the busy
  check inside the lock (`TargetServer.kt`) has no test.

### Part 14: browser toolbar and controls sheet from the catalog (ee2a77a)

- `webui.go` puts the shared catalog into the served `index.html` as a
  JSON data block (escaped with `json.HTMLEscape`; the CSP is unchanged)
  and panics at start unless the placeholder line is there exactly once.
  `player.js` reads it first and derives the Alt+key, F-key and Alt+F-key
  tables, the bar (`views.webBar`) and the controls sheet from it.
- Bar buttons show the catalog's short names and are named by that
  visible text, so speech control can press "Vol+"; tooltips give the
  label and chords, and `aria-keyshortcuts` the chords. The sheet lists
  the catalog's actions with their chords and runs one when clicked.
- The catalog's `mute` reads "Mute device", and `resetvideo` "Refresh
  video" with the short name "Redraw"; the Android bar's strings match.
- The sheet is a modal dialog: focus moves in, and goes back to its
  opener only while still inside; Tab stays in; Esc closes it. After Esc,
  or Enter or Space on a page button, that key's repeats are dropped.
- Quit in the bar and the sheet takes a second press within 2 s; Alt+Q
  quits at once. Middle click is Home; the context menu is Back for a
  mouse only (a touch long-press sent Back).
- Verified: 5 review rounds; node suites; headless Chrome; Firefox on Xvfb.
- Open: bar buttons give their chords twice (tooltip and
  `aria-keyshortcuts`). Chrome keeps Ctrl+W/T/N in a normal tab, so those
  reach the device only in `--window`.

### Part 22: adb and peer connection errors (dadf0b5)

- The `adb forward` fallback, used when `adb reverse` fails, can now
  connect: `start` passes `tunnel_forward=true` (the server connected out
  instead of listening); sockets land in their slots by index; one
  deadline covers them all, and the wait stops if the server exits.
- A missing adb is reported first, even with `-s` or `ANDROID_SERIAL`.
- The server output tail keeps whole lines and, apart, the last 8 error,
  warning and exception lines; a message shows 6, plain ones dropped
  first, so an error outlasts a long run of plain lines.
- `--peer` failures name the device and address, both cleaned (part 47 says
  how), and say what to try; a refusal says why (`forbidden`: what was asked
  and is not allowed). Other phrases are copied from `ProbeLabel` and the
  Android viewer into `peercheck.go`; `peers --check` is unchanged.
- When the saved address fails (not an authenticated refusal), scterm runs the
  `peers --check` search once and saves a new address only after the pinned
  TLS connection there works (a failing `--peer` takes about 1 s more).
- Verified: 3 review rounds; the real `start()` against a fake adb; mutants.
- Open: `ProbeLabel` calls a refusal it has no label for (such as `busy`) "Not
  reachable", and its "Viewing permission missing" for `forbidden` is dead: no
  probe receives one; in forward mode a server that dies just after the first
  socket's dummy byte is noticed only at the next dial or the device-name read
  (the protocol allows no better); past 6 notable lines the `[server] ERROR:`
  line can be left out; the search tries one candidate and does not say the
  device moved; the fallback "connecting to" error keeps its cause raw.

### Part 23: typing beyond ASCII, and pasting, in the terminal (8641ad7)

- Characters beyond ASCII typed in the terminal (é, €, CJK, emoji) reach
  the device as injected text, also when split across reads; ASCII keeps
  the keycode path; bytes that are not UTF-8 are ignored.
- Bracketed paste is on while the UI owns the screen (`\x1b[?2004h`, off
  again on exit and on the panic path). A paste is one event, and nothing
  in it acts as a key once its start marker is recognised (part 27 lets
  Ctrl+C cancel it; its Open says when the marker is missed). It goes as
  one set-clipboard message with paste on, as scrcpy's Ctrl+V does, so it
  replaces the device's clipboard; a peer without the clipboard grant or
  `set_clipboard` gets inject-text messages of at most 300 bytes.
- Line breaks become LF; other C0 controls and DEL are removed (tabs and
  C1 stay). A paste over 262,130 bytes (scrcpy's limit) is dropped with a
  notice of its size, never cut. Notices and logs never show pasted
  text.
- With control off, typing and pasting do nothing and say nothing.
- Verified: 3 review rounds; fuzzing; `-race`; the real input loop.
- Open: the browser still pastes key by key through the `text` op; the
  scrcpy server types injected text through its key map, so typed €,
  CJK and emoji do not appear in adb sessions (a paste carries them); a
  big set-clipboard is written from the run loop and can briefly hold up
  drawing over a slow peer link.

### Part 18: the browser viewer's server side (286d838)

- Reloading the `--window` page no longer ends the session: when the last
  viewer leaves, the session ends `webLeaveGrace` (3 s) later unless one
  connects. Closing the window ends scterm about 3 s after its socket
  drops (`window.go` only waits for the browser). A browser that never
  connects never ends the session.
- A failed text write closes the client fully (`writeJSON` calls
  `close()`), so the page reconnects instead of freezing.
- The client list is copy-on-write: `frame()` and `pushPCM()` walk a
  snapshot without the lock, and a stored list is never written again.
  Keep: copy on add as well as on remove, so the rule does not rest on
  slice capacity.
- A close sets a 2 s write deadline (`wsCloseTimeout`) before it takes
  the write lock, so a viewer that stopped reading no longer holds up the
  stop (it then gets no bye).
- The `gain` op (no shipped page sends it): an absent or negative `gain`
  keeps the level, and `mute:false` restores the level from before the
  mute. `webEvent.Gain` is `*int`.
- Verified: 3 review rounds; churn tests under `-race`; mutants.
- Open: the leave timer's guards against a simultaneous connect or stop
  are reasoned, not tested; a stop can outlast the 5 s a second stop
  waits (about 5.5 s at worst), so the process can exit with the window
  open; an unmute through the `gain` op can race Alt+M.

### Part 24: scterm update (a72934f)

- `scterm update` first shows the current and new versions, the release
  page and the first 15 lines of the notes; on a terminal it then asks
  "Update scterm A to B? [Y/n]" (anything but Enter, y or yes keeps it,
  with exit 0). `-y`/`--yes` skips the question; without a terminal it
  updates after the summary.
- `scterm update --check` says whether a newer release exists, downloads
  nothing and exits 0; on a source build it says `dev` cannot be
  compared.
- The download shows a progress line while stderr is a terminal.
- The previous binary is kept as `<path>.old`, and the output says how to
  go back (`mv ...`, quoted for a POSIX shell). A failure before the swap
  leaves the binary and any older `.old` untouched.
- A release with no published SHA-256 digest is still installed, with
  only its size checked, and update says so before the question.
- Keep: what `version` prints, since `checkRuns` reads it.
- Decided: the release check and the download each get their own
  5-minute limit, since the question between them can wait.
- Verified: 1 review round; local release servers; a pseudo-terminal.
- Open: the copy needs about 13 MB of disk while the update runs; the
  sudo hint is unquoted and keeps no `.old`; a source build is replaced
  without a question or `.old`.

### Part 16: terminal polish (c29c7d0)

- Autowrap is off while the UI owns the screen (`\x1b[?7l`, back on at
  exit and on the panic path), so writing the last column cannot scroll
  the picture. Each frame ends with the colours reset inside its
  synchronized update, and the restore ends an update a crash left open.
- An open on-screen keyboard is laid out again after a resize. While
  Quit is armed, the status row says "press Quit again to quit".
- CR LF in one read is one Enter (on CR LF terminals, one Enter on a
  focused QUIT both armed and confirmed it).
- Keep: the restore takes the UI lock, waiting at most 100 ms
  (`lockWithin`: a `Lock` on a helper goroutine, since `TryLock` polling
  loses to a busy painter), so it never interleaves with a frame; after
  100 ms (a stuck holder) it goes ahead without the lock.
- The Zellij re-grab timer only posts an event (it drew off the run
  loop); under Zellij the row says typing goes to Zellij, F12 gives control.
- After the restore, closed-socket errors from the teardown are not
  printed: `terminalRestorer` sets `quitting`, and only then does the
  watcher drop them (`isClosedConnErr` matches EOF too).
- Verified: 2 review rounds; `-race` and repeated timing tests; mutants.
- Open: `setMouse` and `toggleGrab` write outside the UI lock, so a crash
  restore racing a re-grab could turn mouse reporting back on; a socket
  closed mid-packet ends with "unexpected EOF", which `isClosedConnErr`
  does not match, so it could print after a quit; ESC CR LF still sends
  Ctrl+J.

### Part 17: scrcpy's exit codes (0ce433a)

- Exit status follows scrcpy: 0 for a normal end (Quit, closing the
  `--window` window, a signal during a session, help, version, commands
  that worked); 1 when scterm could not start, a command or its usage
  was wrong, or the session failed otherwise; 2 when the device is lost
  during the session. Usage errors were 2 (parts 4 and 10). `scterm -h`
  has an "Exit status" section.
- Keep: "lost" means a read failed inside the video or audio packet loop
  after the stream started (`lostError` in `stream.go`; `exitStatus` maps
  it to 2). As in scrcpy v4.1, a failure before the stream starts exits
  1: the server sends the codec id before it sets up the encoder, so an
  early failure usually means the server failed. Unlike scrcpy, a
  disconnect in the middle of a packet also exits 2.
- The terminal exits 2 when the stream it shows (the video, or the audio
  without video) is lost, and `--web`, `--window` and `--no-tui` when the
  video is lost (`--no-tui` returns the error to `main`, which prints it
  once after shutdown); so does a peer that ends the session mid-stream.
- Verified: 2 review rounds; child-process `main()`; mutants; `env -i` runs.
- Open: `--web` prints a video error twice when it is not a closed
  connection; the terminal says "lost the video stream" for a decline
  too (exit 1); control-only sessions and `--web` without video never
  notice a lost device, and `--no-tui` with audio only prints the error
  and runs on; a signal while waiting for the server kills scterm, where
  scrcpy exits 0.

### Part 13: terminal status line, notices, device loss (6d0ab7d)

- The status line starts with the way out, "Alt+Q quit · Alt+/ controls",
  then, as room allows, the input state when the mouse is not grabbed,
  "[KB]", the volume, the device name, the frame size and rate, and the
  sparkline. It is cut and erased by display cells, so a wide name cannot
  wrap it.
- Runtime messages no longer scramble the picture: while the UI runs they
  show in the status row for 4 s; once the terminal is restored, also
  after a panic, they print on stderr once each, in first-seen order with
  repeats counted (the 50 newest). `SCT_DEBUG_*` traces still go to
  stderr.
- Losing the device ends the session with a reason, as in "scterm: lost
  the video stream from Pixel 8: stream ended: EOF" (it exited 0 in
  silence; part 17 made it exit 2).
- Before the first frame the picture area says "Waiting for video from
  <device>…", "Video is off (--video=false)" or "No video from <device>".
- Control characters are taken out of notices, and of the device name
  where the UI shows it.
- Verified: 3 review rounds; `-race`, repeated runs, reverts on a copy.
- Open: ambiguous-width characters count as one cell, and so does a
  narrow emoji with variation selector 16, which many terminals draw as
  two; "No video from <device>" does not say why.

### Part 15: Android looks and accessibility (54de2c4)

- Cards are 16 dp rounded rectangles with a thin outline. Buttons are
  flat: primary (accent fill) for Start or Stop serving and Connect,
  secondary (outline) for the rest, drawn 40 dp tall in a 48 dp target.
  Page padding clears cutouts and the keyboard. Framework widgets only.
- The viewer bar is built from the shared catalog (`ViewerBar.kt`): each
  button shows the short name, and TalkBack reads the label (since part 20,
  "shown text, label" when the label lacks the shown text). Keep: where the
  short name is clipped (Rec, Rot, Notif, Keys, Shot), the button shows the
  one-word label. New: the Menu key, and Redraw ("Refresh video", one keyframe
  request). Targets are 48 dp; the blurred panel's tint went from 35% to 72%.
- TalkBack: headings, "Connect to <name>", a named delay slider, a live
  status line. Keep: titles stay capitalised with `uppercase()`, not
  `isAllCaps`, which would show "THİS DEVİCE" on Turkish devices.
- From part 11's review: the port is no longer written in the locale's
  digits; the notification help names the blocked channel; the settings
  buttons toast instead of crashing.
- `ViewerBarTest` checks the bar against the catalog and `strings.xml`,
  whose paths `app/build.gradle.kts` passes as task inputs.
- Verified: 2 review rounds; aapt2, kotlinc, unit tests; no Gradle build.
- Open: the unit tests get absolute paths, so Gradle's cache keys depend
  on where the checkout is.

### Part 12: browser sound start and the http fallback (fa210a5)

- Sound starts from any tap, click or key, including a touch's end, which
  is when phones count a tap as a gesture; the listeners sit on the
  window and come back if the browser suspends the sound.
- One start at a time (overlapping starts leaked contexts). Audio that
  arrives while the context is not running is dropped, not queued, so the
  sound does not start about 240 ms late.
- Over plain http from another machine there is no AudioWorklet (it needs
  a secure context), so the page plays through a ScriptProcessorNode fed
  by the same ring code (`pcm-worklet.js` also loads as a page script).
  Keep: choose the path by the context's `audioWorklet`, not by the
  constructor, which Chromium keeps on plain http.
- A sound chip at the top right shows only when there is something to do
  ("Tap for sound", "Sound off · Alt+M", the compatibility note; part 19
  reworked it). The HUD reads "device vol NN%", where "audio 47%" read
  like this browser's volume. The `gain` op sets this computer's sink
  (since part 44 only with `--audio-dup`; browser-only sessions have
  none).
- Verified: 4 review rounds; node suites; headless Chrome, Firefox, WebKit.
- Open: the ring never re-buffers after it starves.

### Part 9: terminal action bar and two-press Quit (fb6e42d)

- The click that wakes the hidden action bar never runs a button (without
  hover reporting it pressed whatever was under it); it reaches the
  device as a tap.
- ‹ and › turn the bar by the buttons shown, so every button can be
  reached at every width; turning mouse grab off hides the bar.
- Quit in the bar, the Alt+/ menu and the on-screen keyboard takes a
  second press within 2 s (it reads "Quit?", "Press again to quit" or
  "QUIT?", in the same cells); any other press drops it. Alt+Q, and
  Ctrl+Q in the menu, quit at once.
- Local volume moves in whole percent, and Alt+M unmutes to the volume
  from before the mute.
- A left press while a drag is still down (its release lost) lifts the
  device finger first, then counts as a new click.
- The keyboard and the menu ignore mouse buttons 8 to 11.
- Verified: 1 review round; tests, some under `-race`; no real terminal.
- Open: `mouseEvent` masks the button byte with 0x7f, so buttons 8 to 10
  act as left, middle and right on the picture and the bar; the README
  does not say Alt+M restores the earlier volume.

### Part 8: browser connection states (1bf2a8d)

- Whenever the page shows no live video, a card over the dimmed last
  frame says why: "Connecting…", "Waiting for video from <device>…",
  "Connection lost. Reconnecting…", or "Session ended" with the reason
  and a Reconnect button. Before, a lost connection looked like a frozen
  picture.
- Each viewer's socket ends with `{"op":"bye","reason":...}` as the last
  message before the close frame, written under one lock hold: Quit from
  the browser, the device stream ending, or scterm stopping.
  `web/check.mjs` ignores unknown ops. The page stops retrying on a bye.
- After a bye the keys are the browser's (Enter presses the focused
  button, Tab moves, F5 reloads); part 19 keeps Alt+/ and Alt+S.
- Keep: a second `stop()` waits up to 5 s for the first; returning at
  once let the process exit before the byes went out or the `--window`
  browser closed.
- The HUD keeps the device name, which also titles the page; fps says
  "idle" after 2 s without a painted frame; errors turn it red for 5 s.
- Verified: 3 review rounds; node suites on a stand-in DOM; headless Chrome.
- Open: nothing.

### Part 11: Android readiness, notification, permission help (96a2594)

- Readiness badges use short labels ("Helper not running", "Too many
  viewers", "Serving not ready", "Peer refused the check"); a tap shows
  the full sentence. A `Readiness.State` enum replaces matching on
  English labels. Keep: the not-ready sentences come from shared
  constants, so the probe text on the wire, which the Go client and
  older apps read, stays byte-identical.
- The serving notification changes only when a result does: a re-check
  keeps each device's last result, "Checking…" shows only for a device
  never checked, and "Disconnect all" only while a device is connected.
- Help lines with a button to the right settings page: notifications
  blocked, local network access denied for good, and on Android 13 and
  later the "Allow restricted settings" hint; re-checked on resume.
- From part 5's review: one pair dialog at a time; a session that
  connects while the viewer is being destroyed is closed; the allow-back
  grant is a pure function with a JVM test.
- Verified: 1 review round; aapt2, kotlinc on stubs; no Gradle or device.
- Open: nothing.

### Part 10: grouped help, scrcpy flag names (3e47fab)

- `scterm -h`, `--help` and `scterm help` print the same help on stdout:
  flags grouped (Device, Video, Audio, Display, Peer, Debugging) with
  placeholders and each default once, then commands, environment
  variables and examples.
- A flag mistake is one line ending "run scterm -h for the commands and
  flags", not the error and the whole usage.
- scrcpy's names work: `--serial`, `-n`/`--no-control`, `--no-audio`,
  `--no-video`, `-m` and `-b`. Bit rates take K and M (`8M`, `1.5M`).
  Plain values must be decimal digits, and values over 2147483647 are
  refused, as the server reads an int; `0` means the server's default.
- `--mirror-fps` is hidden from help and says it does nothing.
- A leftover word that names a flag gets "did you mean --takeover?"; the
  cost is that an unquoted name holding a flag word gets it too.
- Usage errors exited 2 here; part 17 made them 1.
- Verified: 1 review round; tests, `-race`; the binary run for each case.
- Open: nothing.

### Part 7: AVX2 flag on amd64 only (b3b1fbb)

- `-mavx2` went to the C compiler on every architecture, so an aarch64
  build failed at once, though `native.c` has a NEON path. It is now an
  amd64-only cgo flag; amd64 builds are unchanged and still need a CPU
  with AVX2.
- Verified: 1 review round; `native.c` built for aarch64 with the NDK.
- Open: a full linux/arm64 build (it needs arm64 FFmpeg and PulseAudio)
  has never run, so the NEON path is compiled but untested.

### Part 6: terminal status row, colours, overlay padding (301b1cd)

- The status row is painted on its own as soon as it changes: grab, mute
  and volume no longer wait for a video frame (a still screen may send
  none), and the fps reading no longer forces a full repaint.
- Each frame goes out as one synchronized update (DEC mode 2026, ignored
  where unsupported).
- Cell colours use the full range: the three bits kept per channel are
  spread over 0..255 (white was 224).
- Overlay rows are padded by characters (runes), not bytes, so '·', '‹'
  and '›' no longer leave old cells showing.
- With no usable picture (before the first frame, `--video=false`, after
  a resize) the overlay and the status still paint on a cleared screen;
  a stale snapshot is never sheared into the new grid.
- Verified: 1 review round; six new render tests; no real terminal.
- Open: nothing.

### Part 5: Android pairing, viewer Back, allow-back grant (260d0fb)

- `MainActivity` handles its own configuration changes, so rotating, or
  unfolding without a density change, no longer ends a pairing that is
  comparing codes; `alert()` refuses a dead window, and `onDestroy`
  stops the device search and its multicast lock.
- Viewer: Back leaves while connecting or after a session has ended (it
  sent the remote Back to nothing); the overlay offers Cancel while
  connecting, Close after.
- "Also let it see, hear and control this device" grants view, audio and
  control, like the invite dialog's defaults; it used to share the
  clipboard.
- Verified: 1 review round; built only by CI (offline Gradle); no device.
- Open: a density change, as on some foldables, still recreates the
  screen, which ends a pairing that is comparing codes.

### Part 4: the command line fails fast (95b3e43)

- Start-up no longer hangs: the server exiting ends the wait at once and
  its last error lines are shown (not stack frames); a server that never
  connects is given up after 15 s for all its sockets together; once adb
  has exited, waiting for what holds its output stops after 1 s.
- `--audio-codec` and `--audio-source` are checked against the bundled
  server's lists, also with `--peer`; all three streams off is refused
  before any device work (it panicked).
- adb's states (missing, unauthorized, no permissions, offline,
  transient) each come with the next step.
- Words left after the flags are refused instead of starting a mirror,
  with a hint: a mistyped command, `--audio false` (`--audio=false`), an
  unquoted `--peer Pixel 8` (quote it). Every command answers `-h`;
  `update --help` no longer updates. Usage errors exited 2 (part 17: 1).
- `forget` needs a name (a bare one removed the only pairing) and asks
  before removing a partial match, refusing without a terminal;
  `interactive()` checks for a terminal (`/dev/null` used to pass).
- Verified: 2 review rounds; `-race`; the binary with no adb or a fake one.
- Open: nothing.

### Part 3: terminal keyboard, screenshots, crash restore (e6d120b)

- On-screen keyboard: K, L and M sent L, M and R (now checked against
  `keycodes_gen.go`); the cursor starts on ENT (it started on ROTATE, so
  Enter rotated the phone); rows fit 80 columns; only a real left press
  acts (wheel notches and drags pressed keys), and a drag begun on the
  picture ends on the device (it left a finger down).
- Alt+S and `--dump-frames` wrote the BGR0 canvas as RGB, swapping red
  and blue; now they write R, G, B. (Since part 35, Alt+S saves the
  stream's frame as a PNG, with the canvas only as its fallback.)
- A panic on a terminal-UI goroutine restores the terminal (normal
  screen, mouse reporting off, cooked mode) before it carries on.
- Verified: 1 review round; the new tests fail on the old code.
- Open: nothing.

### Part 2: one press per key in the browser (08cf157)

- Keys sent through `sendKey` (Backspace, Enter, the arrows, Ctrl+letter,
  Ctrl or Alt with a keycode key) reached the device twice: an op went
  out on keydown and again on keyup, and the server makes each op a full
  press (Backspace deleted two characters). Now one op per keydown,
  auto-repeats are sent as presses, and the keyup sends nothing.
- Keep: PrintScreen is the exception, as Windows delivers only its keyup:
  a keyup with no keydown before it is the press, sent without modifiers.
- Bare modifier and lock keys are no longer sent (a lock key cancelled
  itself, or on macOS followed the host's caps lock). A bare Meta tap
  sends nothing. Typed letters carry their own Shift state (`asciiKey`).
- Verified: 3 review rounds; node cases; the device's caps lock untried.
- Open: nothing.

### Part 1: shared action catalog (c04fde6)

- The shared catalog `protocol/fixtures/actions.json` (v2: kind, label,
  short, views) drives the terminal's chord, keycode, F-key, menu and bar
  tables (`actions.go`) and one dispatch path for the terminal, the
  browser and the app-less web server the tests build. It finishes the
  catalog work from the main checkout's `feat/qol-features` tree.
- `TestActionDispatchIsTheGoldenWire` pins every device action to
  Android's published keycodes and a wire fixture derived by hand. The
  loader folds chord case, takes only visible ASCII chords, and lets only
  Back's down/up pair repeat a control name.
- Owner's decisions for the whole overhaul: Quit from a bar, menu or
  sheet takes a second press within about 2 s, Alt+Q quits at once,
  Ctrl+C goes to the phone, and the quit hint stays visible; the Android
  polish uses framework widgets only (no new libraries); a browser
  without AudioWorklet falls back to an older audio path and says so.
- Verified: 3 review rounds; vet, tests, a mutant; Gradle left to CI.
- Open: comments in `web.go` and `web_e2e_test.go` still describe a
  `--web --no-tui` mode, which main refuses.

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
