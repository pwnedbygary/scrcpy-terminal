# Android peer app / Go client / parity development checkpoint

Read [the complete plan](../docs/history/ANDROID_CLIENT_PLAN.md) (section 2a lists the
design revisions adopted on 2026-09-24), [the peer protocol](../docs/PEER_PROTOCOL.md)
and applicable `AGENTS.md` instructions first (none exist in this repository or
its parents as of this snapshot). Reconcile this checkpoint against Git,
current source, live task ownership and test evidence before editing. Status
here is a snapshot, not proof that no other work has started.

## Update (2026-10-22: QoL overhaul, part 59: the libraries' licence texts)

- Issue #16. The binary links Go's runtime and standard library,
  `golang.org/x/net` and `golang.org/x/sys` (BSD 3-Clause: its text must
  come with binary copies); the APK includes the Shizuku API library (MIT:
  its notice must come with every copy). Neither carried them.
- `scterm licenses` prints a third text, `third_party/LICENSE.golang`
  (new): a lead naming what it covers, the modules one per line, then the
  BSD text. Go's own LICENSE (Arch keeps it in /usr/share/licenses/go;
  this computer's GOROOT has none), x/net v0.59.0's and x/sys v0.48.0's
  are byte-identical (sha256 911f8f57...). Not `LICENSE.go`: go would
  compile it.
- The APK carries `assets/LICENSE.shizuku`, a copy of
  `third_party/LICENSE.shizuku` (new), Shizuku-API's `LICENSE` byte for
  byte (no final newline). Releases attach both new files.
- Found: AndroidX, Kotlin, kotlinx, JSpecify, JetBrains annotations and
  ListenableFuture are Apache 2.0, already covered; no checked artifact
  has a NOTICE file.
- Left alone: AGP's default packaging drops a dependency's
  META-INF/NOTICE and top-level NOTICE files without a word (seen in
  AGP 9.3.2; 9.4.1, which the project pins, is not on this machine). A
  future dependency's NOTICE would have to be added as an asset by hand,
  and no test would notice it missing.
- Kept `/META-INF/{AL2.0,LGPL2.1}` excluded: in ~/.gradle only JNA's jars
  carry them (its dual licence), and the APK has no JNA.
- Docs: README's License section, android/README.md, RELEASING.md, and
  third_party/README.md's new Go and Shizuku sections.
- Tests: the command test (renamed ...PrintsTheTexts) checks the order
  and the Go file whole. New TestGoLicenseIsTheModulesOwn runs
  `go list -deps` (GOPROXY=off): the list must equal the linked modules,
  and the text must equal each module's copy (the module cache's, or
  vendor/'s in vendor mode) and GOROOT's; a copy missing here is skipped,
  saying why. Asset, release and README tests cover the new files.
- Verification (sandbox): go test ./... 515 passed, 3 skipped, 0 failed
  (base 514, 3, 0); the GOROOT subtest skips (Arch keeps that file in
  /usr/share/licenses/go) and passes against a GOROOT copy with it.
  Fail-before: 5 of 5 tests fail on base. 23 mutations, 23 caught.
  `-race` and `-count=20` pass. The binary prints 14220 bytes (was
  12580), at most 79 columns. aapt2 -A packs the 3 assets unchanged.
  Docs checker: 0 problems. gofmt, vet, release build clean.
- Not run by the builder: CI, Gradle and its APKs, the GOROOT check on a
  real GOROOT licence, NOTICE checks on the app's exact dependency
  versions (not on this machine).
- Intake: taken in on bee1feb, the branch tip: nine of the builder's 10
  files as built; README.md as built but for one line outside the License
  section the builder was given, and one line of flags.go, outside its
  files. The builder had left both lines true but incomplete: the help's
  Commands line and the README's table of commands now say the command
  prints the licenses of scterm and what it includes (the help's line
  fits on one line, as TestHelpIsGrouped requires). Reviewed on its own
  snapshot, /tmp/opencode/review-59.
- Review round 1 (/tmp/opencode/review-59): PASS, with one LOW and one
  NIT. LOW-1: in vendor mode go list gives a module no directory, and the
  module subtests skipped with "the module cache has no copy", though
  vendor/ held both LICENSE files. NIT-1: the entry's GOROOT wording, the
  AGP NOTICE risk and "10 files as built" (corrected above). Suggestions
  taken: pin Shizuku's text, docs that name no module, `licenses -h`.
- Round 2: a module with no directory from go list has its LICENSE read
  from vendor/<path>/LICENSE (moduleLicense, unit-tested in a temporary
  directory); with none there, the subtest skips and says both facts.
  TestAppCarriesTheLicenses also checks LICENSE.shizuku's SHA-256
  (64870037...), so an edit made to both copies alike fails; its helper
  has its own test. Both READMEs point at the list in LICENSE.golang
  instead of naming modules; third_party/README.md says to update the
  digest with the file. `licenses -h` reads as one list.
- Verification, round 2 (sandbox): go test -count=1 -v ./... 517 passed,
  3 skipped, 0 failed (round 1: 515, 3, 0). In a copy vendored offline,
  both module subtests read vendor/ and pass, an edited vendored LICENSE
  fails its subtest, and a removed one skips with the new reason; round
  1's test skips both, as the review found. 12 mutants, 12 caught; R9
  (both Shizuku copies edited alike) now fails. -count=20 and -race
  -count=5 pass. gofmt, vet and builds are clean; docs checker: 0
  problems.
- Intake, round 2: on cb02c66, the branch tip once part 57 had landed:
  the builder's round-2 files as built, with .github/workflows/build.yml
  and docs/RELEASING.md, which part 57 also changed, merged three ways
  (each merge adds exactly this part's lines). Reviewed on
  /tmp/opencode/review-59b.

## Update (2026-10-22: QoL overhaul, part 57: CI runs -race and the shell tests)

- CI's Go job installs zsh and fish and runs `go test -race ./...` (no plain
  run besides), so the paste tests run all 7 shell modes there; zsh's and
  fish's used to skip (issue #12). The Test step sets
  `SCTERM_TEST_REQUIRE_SHELLS=1`: with it set (any non-empty value, as
  `SCTERM_NEARBY_LIVE` is read), a shell that `pasteInto` does not find fails
  the test instead of skipping it, sh and bash included. Locally it skips.
- New `TestMissingShellSkipsUnlessRequired` runs `pasteInto` in a child
  (`SCTERM_TEST_PASTE_SHELL`): a missing shell skips without the variable and
  fails with it; /bin/sh passes either way. docs/DEVELOPMENT.md: CI runs
  `-race`; a paragraph on the shells and the variable; the child variable.
- Decisions: race only, as every test runs under it (no race build tags, no
  `testing.Short`); on 4 CPUs the race suite took 77.5 s, plain 34.7 s, a
  cold race build 14.3 s. The shells install in a step of their own
  (`--no-install-recommends`, then /usr/bin/zsh's and fish's versions).
- Node: the job installs none; GitHub's 22.04 and 24.04 images ship it (from
  memory); ubuntu-26.04's list was not checked (offline), and `go test`
  without -v does not show whether `TestWebClientSuites` skipped. Nothing
  about node was added to the YAML.
- Verification (dev-57, base bee1feb, go1.27.1, sandbox): gofmt, vet, build,
  `git diff --check` clean; `go test -count=1 ./...` 515 pass, 3 skip, 0
  fail (base 514/3/0); `SCTERM_TEST_REQUIRE_SHELLS=1 go test -race ./...`
  the same, no race. New test x20 20/20 (unset and set), -race x5 5/5.
- With zsh and fish overlaid to /nonexistent: variable set, their 8 subtests
  fail ("is not installed, and SCTERM_TEST_REQUIRE_SHELLS is set"); unset,
  they skip; on base, set, they skip. Base's check fails the new test;
  base's doc fails `TestDevelopmentNamesTheTestVariables`. Mutants: 10 of 12
  killed; `== "1"` survives by choice, Errorf (then Skipf: still FAIL) is
  equivalent.
- YAML: PyYAML parses it; actionlint 1.7.12 gives base's 4 findings
  (ubuntu-26.04 unknown x3, one SC2012 info), none new; only the build job
  changed.
- Left alone: the release job; node in CI; CI's test cache.
- Not run by the builder: CI; apt-get on ubuntu-26.04 and its zsh and fish
  (tests made on zsh 5.9.2, fish 4.9.3); a machine really missing a shell
  (overlays instead); node on the runner.
- Intake: taken in on bee1feb, the branch tip: the builder's 3 files as
  built, plus docs/RELEASING.md's account of the build job, outside the
  files it was given, which still said CI runs `go test ./...`: it now says
  the job installs zsh and fish for the paste tests and runs
  `go test -race ./...`. Reviewed on its own snapshot,
  /tmp/opencode/review-57.

## Update (2026-10-22: QoL overhaul, part 50: suggested commands are safe to paste)

- "Connect with: scterm --peer %q" (after `scterm pair`, typed or nearby)
  wrote the device's name as a Go string literal. Pasted, its double
  quotes let sh, bash and zsh run `$(...)` and backticks, fish run
  `$(...)`, `$VAR` expand, and an interactive bash or zsh expand `!`;
  `%q`'s escapes (`\x1b`, `\u202e`) reached the shell as backslashes.
  `scterm forget` refused part of a name with the stored name in that form.
- New `pasteArg` (peermode.go) gives the argument for both: the name, in
  single quotes with `'\''` for each `'`, when it is plain and finds this
  pairing alone; else the short identity, or the whole one when another
  pairing's name takes the short one (or two identities start alike),
  each only when it finds the pairing alone; and nothing when neither
  does, or when the pairings cannot be read, so that no command is
  printed. Round 1 quoted names through update.go's `shellQuote`, which
  leaves ASCII letters, digits and `./_-` bare, and gave the short
  identity unchecked when neither identity found the pairing alone; round
  2 changed both (below).
- Plain: not empty, UTF-8, every rune `unicode.IsPrint`, no backslash
  (fish reads one inside single quotes), no space at either end nor two
  together (so `cleanText` leaves it as it is), and not `help`, `-h`,
  `-help` or `--help` (`scterm forget help` prints the help). Finds alone:
  no other pairing has the name in any case (Find gives the first of two),
  and `Store.Find` gives this pairing for it (Find lowercases `İzmir` to
  `izmir`, which can be another pairing's name).
- `reportPaired` takes the store its two callers hold (round 2; round 1
  kept its signature and opened the store again through `peerHome`). The
  sentence before keeps the cleaned name; the line does not say why it
  shows an identity, which that sentence shows in parentheses. When
  pasteArg gives nothing, the line says "scterm cannot give a command
  that selects only this device: scterm peers lists the pairings." (round
  3; round 2's words, that no command could select the device, were not
  true: below).
- forget's refusal is one line, and no text of the device's comes before
  its command: `"pix" only partly matches a paired device: to forget it,
  run scterm forget 'Pixel 8'`. When pasteArg gives nothing, it ends ",
  and scterm cannot give a command that selects only that device: forget
  it from a terminal, where scterm asks first" (round 3's words). Round 1
  put the cleaned name before the command (round 2, below).
- `%` and `=` are quoted: fish 4.9.3 expanded `%self` to its PID and zsh
  5.9.2 `=ls` to `/usr/bin/ls`. `-x` is `--peer`'s value: Go's flag
  package gives `--peer` the next argument whatever it starts with.
- README: the pairing paragraph says how the command is written, when it
  gives the identity and when scterm prints none; the forget bullet says
  the refusal gives the command, or a note to use a terminal. flags.go's
  "quote a value with spaces, as --peer "Pixel 8"" hint is left alone: the
  user's own words, and a backslash would need a rule of its own for fish.
- Tests: pastecmd_test.go (new) has `TestConnectWithPastesAsPrinted` and
  `TestForgetRefusalPastesAsPrinted`. They paste the printed command into
  /usr/bin/sh, bash, zsh and fish (`-c`) and into interactive bash and zsh
  (lines on stdin), with a clean environment, scratch HOME and working
  directory, setsid, and scterm a function that prints its arguments. They
  check the arguments, that no MARKER file appears, that `parseArgs` and
  `pickPeer` (no question) find the pairing, and that the pasted forget
  command forgets it. pastearg_test.go (new) had
  `TestPasteArgNamesByPlainNamesOnly`, `TestPasteArgNeedsAName` and
  `TestPasteArgFallsBackToTheWholeIdentity`; rounds 2 and 3 add one test
  each (below). devicetext_test.go: the forget refusal and both
  Connect-with pins. Round 1 had 49 pairings (48 names and a no-name
  pairing) and 5 forget cases; round 2 adds to these (below).
- Verification (builder's tree, base `ed3d204`, iso.env, bwrap, go1.27.1):
  gofmt, vet, build and `git diff --check` clean. `go test -count=1 -v
  ./...`: 512 pass, 3 skip, 0 fail (base 507, 3, 0; +5 new); `-race`: the
  same, no race. 16 touched and related tests under `-race`: 16/16; the 5
  new x20: 100/100, under `-race` 100/100. On base's peermode.go the 2
  paste tests and the 3 pins fail (base's line ran `touch` from a name in
  all 6 shell modes; `"Hi!x"` failed in interactive bash and zsh); the 3
  helper tests do not build. Mutants: 19 of 19 killed.
- Left alone: `Store.Matches` lets a whole name win over an identity, so a
  device named after another's short identity takes `--peer IDENTITY`
  typed by hand or kept in shell history (peer/store.go); nearby pairing
  can store an empty name (peernearby.go; the line gives the identity);
  update.go's "To go back to it" quotes a path with `shellQuote`, which
  fish misreads only for a path with a backslash.
- Not run by the builder: a person pasting into a real terminal; dash and
  ksh (not installed); fish interactive; adb, a phone, a peer target or
  the Android app; the node suites (web/ untouched).
- Intake: the worktree was clean at `ed3d204`, the builder's base, so the
  five files were taken as built, byte for byte.
- Verification, snapshot round 1 (`/tmp/opencode/review-50`, on `ed3d204`;
  go1.27.1, iso.env, every Go command in the bwrap sandbox): gofmt, vet,
  build and `git diff --check` clean; `go test -count=1 -v ./...` 512 pass,
  3 skip, 0 fail; `-race -count=3` on 36 related tests (every test of
  devicetext_test.go, cli_test.go, peerwords_test.go and the two new files)
  passes; the 5 new tests and the 3 pins pass 20 times and 5 times under
  `-race`. With the base's peermode.go (pastearg_test.go stubbed) all 5
  fail: the Connect-with paste test in all 6 shell modes, the forget one on
  the refusal, which has no command, and the 3 pins (the forget refusal
  and both Connect-with lines in devicetext_test.go). Each of the builder's
  19 mutants fails at least one related test. Docs checker: README.md, 34
  links, no problem; the docs tests pass. The whole module, in a sandbox
  set up like a desktop session, connects to none of its decoy sockets; a
  positive control reaches two. The checks ran on a first build, kept as
  review-50.checked; the rebuild with this bullet changed only the
  handoff.
- Review round 1 (NEEDS CHANGES), one MEDIUM, three LOWs and two NITs.
  MEDIUM: forget's refusal put the cleaned name, bare, before the command,
  with the same words around it, so a device named `Pixel: to forget it,
  run scterm forget Pixel;touch MARKER #` planted a command that ran when
  pasted from the first "run": the review's sixth case of the forget test
  ran `touch` in all 6 shell modes. LOWs: an interactive zsh rewrites a
  bare name that names a global alias (oh-my-zsh's `...` for `../..`; with
  `alias -g L='| touch …'`, a device named `L` ran touch); the last
  fallback was not checked, so with other pairings named after both
  identities the short identity selected the impostor named after it; and
  four mutations survived the 36 related tests (forget's command from the
  cleaned name, a looser `shellQuote`, `unicode.IsGraphic` for `IsPrint`,
  the whole identity unchecked). NITs: the refusal for a name that cleans
  to nothing read "is only part of :", and wording: `shellQuote`'s bare
  set is ASCII only, the README's reasons for the identity left out the
  help words and a name Find reads as another pairing's, and the snapshot
  bullet did not name the three pins (corrected above).
- Round 2 changes peermode.go, peernearby.go (its call of `reportPaired`),
  README.md, the tests and this entry. forget's refusal has no text of the
  device's before its command, so it never shows a name that cleans to
  nothing, nor says "only part of NAME" for a query that began the
  identity; every name is in single quotes, which an interactive zsh never
  alias-expands; when neither identity selects the device alone, no
  command is printed and both lines say so (round 3 corrects what they
  say); `reportPaired` takes the store. Tests: pasteNames gains `L`,
  `...`, `#x`, `{a,b}`, `}`, `?`, `[a]`, `^x`, `end`, a no-break space and
  an ideographic space (60 pairings); a seventh shell mode, interactive
  zsh with `alias -g ...='../..' L='| touch MARKER-alias-L'`; the forget
  test gains the review's planted name and `Pixel 8\u202e`, which cleaned
  is `Pixel 8` and only partly matches it (7 cases), and checks that its
  refusal begins with the query and fixed words (round 3 checks the whole
  line); new `TestNoCommandWhenNothingSelectsThePairingAlone` (other
  pairings named after both identities: no argument, the two lines, the
  pairing kept); `pasteInto` sets `cmd.WaitDelay`; cli_test.go's
  `TestForgetNeedsTheWholeName` looks for the new words.
- Verification, round 2, in the worktree (iso.env, every Go command in the
  bwrap sandbox): gofmt and vet clean; the 37 related tests pass. Each
  mutant of round 2's peermode.go (`/tmp/opencode/dev-50b/mkmut.py`: the
  builder's M01 to M18 ported, M05 to pasteArg's own quoting; M19, which
  changed `shellQuote`'s bare set, has no port, as pasteArg no longer calls
  it; the review's R01, R03, R04, R16 and R22; and N1 to N5) fails a
  related test. N1, round 1's refusal, fails the forget test on its first
  words, `TestForgetCleansTheName` and `TestForgetNeedsTheWholeName`; N2,
  round 1's bare names, fails the plain-name test, and makes
  MARKER-alias-L and turns `...` into `../..` in the global-alias mode of
  the Connect-with paste test; N3, round 1's unchecked short identity, N4,
  the Connect-with line without an argument, and N5, forget's command with
  the short identity anyway, fail the new test; R01 fails the forget test
  on `Pixel 8\u202e`; R03 the plain-name test and the Connect-with paste
  test, on the two spaces; R04 the new test; R16 the plain-name test, and
  the Connect-with paste test in all 7 shell modes (`#x` loses its value,
  `}` stops zsh and fish parsing, `?` and `[a]` find no match); R22 the
  Connect-with paste test.
- Left alone in round 2: the "Paired with NAME (SHORT)" sentence before
  the Connect-with line still shows the cleaned name, as it did before
  this part, so a device can put text there that reads like a
  Connect-with command (the review's suggestion); quotes around it would
  not stop a reader copying from inside them, and showing the name only
  after the command would change part 47's output. CI installs neither zsh
  nor fish, so their subtests skip there; its /usr/bin/sh is dash, which
  runs the sh mode. fish's abbreviations, which only its interactive
  reader expands, are not tested.
- Verification, snapshot round 2 (`/tmp/opencode/review-50b`, on
  `ed3d204`; go1.27.1, iso.env, every Go command in the bwrap sandbox):
  gofmt, vet, build and `git diff --check` clean; `go test -count=1 -v
  ./...` 513 pass, 3 skip, 0 fail; `-race -count=3` on the 37 related tests
  passes; the 6 new tests and the 4 changed ones (the 3 pins and
  `TestForgetNeedsTheWholeName`) pass 20 times and 5 times under `-race`.
  Each of the 28 mutants fails at least one related test, the same ones as
  in the worktree; N2, run against the global-alias mode alone, fails it
  (MARKER-alias-L made, `...` read as `../..`). Docs checker: README.md,
  34 links, no problem; the docs tests pass. The whole module, in a
  sandbox set up like a desktop session, connects to none of its decoy
  sockets; a positive control reaches two. The checks ran before this
  bullet was written; packing again with it changed only the handoff.
- Review round 2 (NEEDS CHANGES), two LOWs and two NITs. LOW: the lines
  printed instead of a command said that no command could select the
  device, which was not true: in the new test's own setup `scterm --peer
  pix` selects it, and its whole name, typed with bash's `$'…'`, forgets
  it at once. A store that cannot be read gave that line too, though the
  cause was the read, and no test covered that branch: a pasteArg giving
  the short identity there passed every related test. LOW: device text
  can still read as scterm's own instruction. Inside the quoted name,
  copied from the device's own "scterm forget", `'Pixel: to forget it,
  run scterm forget Pixel;touch …'` runs `touch` in five of the seven
  shell modes (copied whole, the command is safe in all seven); and in the
  "Paired with" sentence (left alone in round 2, above). Both are older
  than this part, and the review accepts leaving them to a later part.
  NITs: the forget test checked only its line's first words, so a refusal
  ending in `# NAME` failed only in the interactive zsh modes and
  `TestForgetCleansTheName`; and wording: pastearg_test.go's tests, N1's
  and N2's failing tests, update.go's quoting, and a name that Find reads
  as another pairing's, which it selects instead, not too (corrected
  above). The review checked round 1's six findings fixed, and pasted 243
  hostile names into 16 shell modes in two locales: each reached scterm
  as the name or the identity, ran nothing and selected its pairing.
- Round 3 changes peermode.go's two lines without a command, README.md,
  the tests and this entry. After pairing: "This computer is SHORT. scterm
  cannot give a command that selects only this device: scterm peers lists
  the pairings."; forget ends ", and scterm cannot give a command that
  selects only that device: forget it from a terminal, where scterm asks
  first". The README says that scterm then prints no command, and says it
  cannot give one; and that a name which would select another paired
  device "too, or instead" is given by identity. Tests: new
  `TestNoCommandFromPairingsThatCannotBeRead` (a peers.json that does not
  read: no argument, and the line without a command; after a pairing and
  in forget the pairings were read just before, so this does not happen
  there); `TestNoCommandWhenNothingSelectsThePairingAlone` checks that
  `pix` still finds the pairing, and expects the new words; the forget
  test checks its whole line: the query, the fixed words and `scterm
  forget` with the name in single quotes, or the short identity, as
  written out by hand in the test; pastecmd_test.go's comment on `?` says
  it is a glob in sh, bash and zsh (fish 4.9.3 takes it as it is).
- Verification, round 3, in the worktree (iso.env, every Go command in the
  bwrap sandbox): gofmt and vet clean; the 38 related tests (round 2's 37
  and the new one) pass. Each of 38 mutants of round 3's peermode.go
  (`/tmp/opencode/dev-50c/mkmut.py`: round 2's 28, N5's text to replace
  in round 3's words; the review's X1 to X8; and N6 and N7, round 2's two
  lines put back) fails at least one related test. X7, a store that
  cannot be read giving the short identity, fails only the new test; the
  review's own X7, on a copy of round 2's snapshot, passes all 37 of
  round 2's related tests. X2, the cleaned name as a shell comment after
  forget's command, fails the forget test on its line, before any shell
  runs; round 2's forget test failed it only in the two interactive zsh
  modes, besides `TestForgetCleansTheName`
  (`/tmp/opencode/dev-50c/gaps.sh`).
- Left alone in round 3, for a later part: device text that reads like
  scterm's own instruction, inside the quoted name and in the "Paired
  with" sentence (the review's second LOW, above); the real command is
  safe, copied whole. The review's other remedy, a stricter "plain"
  without shell metacharacters (`;&|<>()$`, the backquote, `#*?[]{}~!`),
  so that no part of a quoted name could run anything, is not taken: such
  names would then be given by identity. forget's other lines that show
  the cleaned name ("Forget NAME? [y/N]", "Kept NAME.", "Forgot NAME.",
  the list of names that match) are as they were before this part.
- Verification, snapshot round 3 (`/tmp/opencode/review-50c`, on
  `ed3d204`; go1.27.1, iso.env, every Go command in the bwrap sandbox):
  gofmt, vet, build and `git diff --check` clean; `go test -count=1 -v
  ./...` 514 pass, 3 skip, 0 fail; `-race -count=3` on the 38 related
  tests passes; the 7 new tests and the 4 changed ones pass 20 times and 5
  times under `-race`. Each of the 38 mutants fails at least one related
  test, the same ones as in the worktree; N2, run against the global-alias
  mode alone, fails it. With round 2's files
  (`/tmp/opencode/dev-50c/r2files`), the review's X7 passes all 37 of
  round 2's related tests, and its X2 fails only `TestForgetCleansTheName`
  and the forget test's two interactive zsh modes; round 2's peermode.go
  under round 3's tests fails the two tests of the lines without a
  command. Docs checker: README.md, 34 links, no problem; the docs tests
  pass. The whole module, in a sandbox set up like a desktop session,
  connects to none of its decoy sockets; a positive control reaches two.
  The checks ran before this bullet was written; packing again with it
  changed only the handoff.

## Update (2026-10-21: QoL overhaul, part 49: adb mode runs the scrcpy server built from the vendored source)

- The Linux binary embedded the official scrcpy 4.1 server (733706 bytes,
  sha256 `deacb991…`), so adb mode lacked the vendored source's one local
  patch (`afcd41b`: playback capture also matches `USAGE_GAME` and
  `USAGE_UNKNOWN`), which the Android app's helper compiles in. The owner
  chose to rebuild the jar from the patched source.
- New `third_party/build-scrcpy-server.sh` builds the server offline from
  `third_party/scrcpy-server-src` (git, JDK 17 `javac` and `jar`, platform
  android-37.0, build-tools 36.0.0) and writes
  `third_party/scrcpy-server.jar.d/scrcpy-server.provenance` next to the
  jar: its sha256, the source's git tree hash, the version name, the
  toolchain and the command. `--check` builds again in a temporary
  directory and fails unless the result is the embedded jar, byte for byte.
- It follows the source's `build_without_gradle.sh` (aidl, javac, d8),
  changed thus. Platform android-37.0, as the helper; android-34 fails (4
  errors, first `VANILLA_ICE_CREAM`), android-36 is not installed. javac
  also compiles the aidl output that no source imports
  (`IOnPrimaryClipChangedListener`), as Gradle does. d8 `--release
  --min-api 21 --lib android.jar`, as the Gradle release build: with that
  script's `--classpath`, d8 gives the server's lambdas methods that
  forward to `java/util/Comparator$-CC` and `Predicate$-CC`, which no
  device has. The jar stores classes.dex with a fixed date and no
  manifest, so its bytes depend on the dex only (not the JDK's zlib), and
  zipalign puts it on 4 bytes, so ART can map it in place.
- The new jar: 215087 bytes, sha256 `aa2dc6a2…`; classes.dex 214956 bytes,
  DEX 035, 160 classes; source tree `fedb1c82…`; javac 17.0.20.1, d8
  8.10.9-dev. `AudioPlaybackCapture.java` gained a notice at its top that
  scterm changed it (2026-09-24, `afcd41b`, and what), built after.
  `VERSION_NAME` stays 4.1: `Options.parse` compares it with what
  `serverVersion` passes, and the patch keeps the protocol.
- Against the official jar: 1083 of its 1246 classes are the Kotlin stdlib
  and annotations (AGP 9.1.0's built-in Kotlin; the source has none). Of
  the other 163, the new jar lacks `R` (unused) and 17 that compilers made
  (12 lambda classes numbered otherwise, 3 `-IA`, an API outline, a
  try-with-resources helper) and has 15 of its own (12 lambda classes, 3
  `$1`). Of the 145 common classes, 132 declare the same members;
  `BuildConfig` lacks 3 unused AGP fields; in 11, members the compilers
  made differ (accessors named `access$NNN` against `-$$Nest$...`,
  synthetic package-private constructors taking `$1` in place of `-IA`,
  or both); and the name `AudioEncoder$$ExternalSyntheticLambda2` stands
  for another lambda. The server code's 580 framework and Java references
  are all among the official's 592 (the 12 others are in that helper). Left
  out: AndroidManifest.xml, resources.arsc, META-INF, kotlin_builtins.
- The patch is in the new jar only: its `createAudioRecord` has 2 `setUsage`
  call sites and 12 `Method.invoke` calls (the official 1 and 11), with an
  int array {14, 0} (`USAGE_GAME`, `USAGE_UNKNOWN`) looped over with
  `addMixRule` (dexdump; javap before d8 agrees).
- New tests (`server_jar_test.go`): `TestEmbeddedServerIsTheBuiltOne`
  (sha256 of the embedded bytes), `TestServerSourceIsTheBuiltOne` (the
  source's git tree hash, computed in Go), `TestServerVersionIsTheBuiltOne`
  (`serverVersion()` against the provenance) and `TestGitTreeHash` (the
  helper against `git write-tree`). CI's android job runs `--check` after
  installing build-tools 36.0.0 and android-37.0 with sdkmanager.
- Docs: README Credits say scterm builds the server from its source with
  one change; the `--audio-source` paragraph no longer says the bundled
  server leaves out game sound; `docs/DEVELOPMENT.md` gains "The scrcpy
  server" (rebuild and check); `docs/RELEASING.md` names the CI check;
  `adb.go`'s `serverVersion` comment no longer speaks of a system jar.
- Checks by the builder (on `afe9ebe`; go1.27.1, `iso.env`, bwrap): three
  clean builds (in the clone; from /tmp with `TZ=Asia/Tokyo`; in a copy at
  a path with a space), and `--check` on BASE plus the diff, give the same
  jar and provenance; d8 gives the same dex with 1 thread and in reverse
  input order; javac 17.0.20.1 twice gives the same 125 class files (javac
  8 and 27 give other dex). gofmt, vet and build clean; `go test ./...` 484
  passed, 3 skipped, 0 failed (the base 480, 3, 0: the 4 new tests); the
  new tests 80/80 under `-race -count=20`; the binary holds the new jar,
  not the old. On the base's jar, its Java file, or both without the
  provenance, the new tests fail; each of 8 Go mutants fails a test; 8
  recipe mutants each make `--check` fail, and without `--date` two builds
  differ.
- Left alone: `android/README.md` (it does not say adb mode differs); the
  vendored `build_without_gradle.sh` and its `--classpath` problem;
  `docs/ARCHITECTURE.md` (still right); upstream's
  `voiceCommunicationCaptureAllowed(true)`, called on the rule builder
  after `build()`; part 48's list of local modifications.
- Not run: the CI step (Temurin 17's javac may compile differently from
  17.0.20.1, failing the android job, which the release job needs;
  sdkmanager's path on ubuntu-26.04); the new jar on any device (round 3
  ran it on the test phone: the "On a device" bullet below); Android 5 to
  12; the node suites alone (`web/` untouched).
- Intake: `README.md`, `docs/RELEASING.md` and `build.yml` were merged three
  ways with part 48's changes, and `adb.go` with part 47's; each merge adds
  exactly this part's changed lines. The other files were taken as built,
  byte for byte. Intake also adds one sentence to the first section of
  part 48's `third_party/README.md` (its list of local modifications is
  unchanged): `build-scrcpy-server.sh` builds the embedded jar from that
  source, with the changes listed, and the provenance file records how.
- Review round 1 (PASS), with one LOW and one NIT. LOW: `--check` did not
  compare the provenance's version name with the script's `VERSION_NAME`,
  so a hand edit that set both `serverVersion` and the provenance to 4.2
  kept every guard green, while every adb session would fail the server's
  version check (`Options.parse`). NIT: the 145 common classes match in
  their declared members only (corrected above). The review rebuilt the
  jar byte for byte, also with JetBrains' JDK 17.0.14 (JDK 25 builds other
  bytes), and in the dex found only the patch changed: 677 strings the
  same, 835 of 842 methods with the same lines. It asked for a run on a
  device before landing: the version check, no class or method missing or
  failing to verify, video and control, Alt+Q, and on Android 13 or later
  playback capture with media and game sound (`dumpsys audio` listing the
  MEDIA, GAME and UNKNOWN usage rules); and on a device with Android 5 to
  11, if one is at hand, a start, video and control with no "Rejecting
  class" in its log, as the new jar has no API outline class, so those
  versions check such calls when they run.
- Round 2 changes the build script and `docs/DEVELOPMENT.md`; the jar, the
  provenance, the Go test and `adb.go` are round 1's. `--check` also fails
  unless the provenance's `version name` is the script's `VERSION_NAME`,
  which `server_jar_test.go` reads as the jar's; the build stops when d8
  writes anything but `classes.dex`; DEVELOPMENT.md says another JDK
  version builds a different jar.
- Verification, round 2 (`/tmp/opencode/dev-49b/verify2.sh`, offline in
  the sandbox): the jar, the provenance, `server_jar_test.go` and `adb.go`
  are round 1's; `--check` passes; the four guard tests pass. With the
  provenance and `serverVersion` both 4.2, round 2's `--check` fails and
  round 1's passes (the gap); a script that leaves a `classes2.dex` after
  d8 stops with "d8 wrote more than classes.dex".
- Intake, round 2: after round 1 the part was taken out of the worktree
  (it lands last, after a run on a device), and it is taken in again on
  top of parts 44 and 46, where round 1 sat on part 45. `README.md` was
  merged three ways with the changes of parts 48, 44 and 46,
  `docs/RELEASING.md` and `build.yml` with part 48's, and `adb.go` with
  part 47's; each merge adds exactly this part's changed lines, and the
  last three give round 1's files. The other files were taken as built,
  byte for byte; the sentence in `third_party/README.md` is round 1's.
- Verification, snapshot round 2 (`/tmp/opencode/review-49b`, on part 46's
  first round; go1.27.1, iso.env, every Go command in the bwrap sandbox):
  gofmt, vet and build clean; `go test ./...` 506 passed, 3 skipped, 0
  failed; the 14 related tests under `-race -count=3`; the 4 guard tests
  20 times and 5 times under `-race`. `--check` on the snapshot's tree
  finds the embedded jar to be what the source builds to, offline, and
  leaves the tree and its git objects as they were. On copies: the base's
  jar, its Java file, or both without the provenance, and each of the 8
  Go mutations, fail a guard test; each of the script's 8 mutations makes
  `--check` fail; two builds without `--date` differ; with the provenance
  and `serverVersion` both 4.2, `TestServerVersionIsTheBuiltOne` passes
  and `--check` fails (round 1's script passes it, the gap); a
  `classes2.dex` after d8 stops the build. Docs: 4 files, 50 relative
  links, no problem; the docs tests pass. In the decoy sandbox the tests
  made no connection, and the positive control two. `git diff --check`
  and the hashes agree. The checks ran on a first build, kept as
  review-49b.checked; the rebuild with this bullet changed only the
  handoff.
- Review round 2 (PASS), with one LOW and three NITs. LOW: the sentence
  round 2 corrected still miscounted: of the 145 common classes, 13
  differ, not 4 (corrected above). NITs: the d8 guard
  (`find . -type f ! -name classes.dex`) let a `classes.dex` in a
  subdirectory, or a symlink named `classes2.dex`, through (d8 writes
  neither); `--check`'s FAILED line said the jar differed when only the
  version name did; this entry's account of round 1's device request left
  out failing verification and the Android 5 to 11 run (corrected above).
  Of the provenance variants the review tried, none passes both the Go
  guards and round 2's `--check` while the jar and `serverVersion`
  disagree. Not taken: pinning Temurin's patch level in CI, as `--check`
  already fails if another JDK builds other bytes.
- Round 3 changes the build script, `server_jar_test.go`,
  `docs/DEVELOPMENT.md` and this entry; the jar, the provenance, `adb.go`
  and the Java file are round 1's. The build stops when d8 leaves
  anything but `classes.dex` in its output
  (`find . -mindepth 1 ! -path ./classes.dex`); `--check`'s FAILED line
  names what differs (the embedded jar, the recorded sha256, the recorded
  source tree, the recorded version name);
  `TestServerScriptVersionIsTheRecordedOne` fails unless exactly one of
  the script's lines starts with `VERSION_NAME=`, with the provenance's
  version name after it, and needs no Android SDK (it reads no other form:
  a second assignment written otherwise, indented or after `export`, goes
  unseen, though `--check` catches the jar it builds, and a comment or a
  space after the value fails the test);
  `docs/DEVELOPMENT.md` says the provenance records the version name, and
  what `--check` and `go test` compare.
- On a device (the owner's test phone, a nubia NX769J with Android 16,
  over wireless adb, through a guard that refuses any call not aimed at
  it; a binary built from the worktree with parts 46 and 49, sha256
  18a0a9dc..., whose code is what lands: later rounds changed only a test
  comment and handoff text): six sessions with this server, each past the
  version check and ending cleanly. Two runs of
  `--no-tui --audio-source=playback` each gave 576x1280 frames (their
  statistics lines read 13, 45, 19, 12 and 11 fps, then 12, 25, 12, 15
  and 13) and 1871 KB of PCM in 12 s, all digital silence (`peak=0` in
  every line), as nothing played on the phone; one scterm stream on the
  sound server while each ran and none after. Meanwhile `dumpsys audio`
  listed the server's capture policy, registered by the shell user, with
  three rules: `match usage` USAGE_UNKNOWN, USAGE_MEDIA and USAGE_GAME
  (the official source adds MEDIA alone). In the terminal display with
  the default source, Alt+N opened the notification shade and Alt+C
  closed it. With `--audio-source=playback`, Alt+N opened no shade, with
  the official server too (the same binary with the official jar), while
  `cmd statusbar expand-notifications` opened it at once (read live, not
  saved): older than this part, left alone. The phone's log stopped
  recording on 10-12, and scterm shows the server's own output only when
  the server stops before connecting, so classes missing or failing to
  verify could be looked for in neither; the code the patch changes ran,
  as its rules registered. No scrcpy file, process or reverse tunnel was
  left on the phone; a read-only probe afterwards saved that, the log's
  newest line (from 10-12) and the status bar's disable flags (all 0) in
  `/tmp/opencode/devtest/results6/probes-readonly.txt`. Not run: Android
  5 to 11 (no such device), and media or game sound playing during the
  capture, so the run shows the rules registered and a silent stream
  flowing, not media or game sound captured.
- Verification, snapshot round 3 (`/tmp/opencode/review-49c`, on part 46's
  second round; go1.27.1, iso.env, every Go command and build in the bwrap
  sandbox): gofmt, vet and build clean; `go test ./...` 507 passed, 3
  skipped, 0 failed; the 15 related tests under `-race -count=3`; the 5
  guard tests 20 times and 5 times under `-race`. `--check` on the
  snapshot's tree finds the embedded jar to be what the source builds to,
  offline, and leaves the tree and its git objects as they were. On
  copies: the base's jar, its Java file, or both without the provenance,
  each of the 8 Go mutations, and the script's version name changed to 4.2
  or set twice, fail a guard test (the last two only the new one); each of
  the script's 8 mutations makes `--check` fail; two builds without
  `--date` differ; with the provenance and `serverVersion` both 4.2,
  `TestServerVersionIsTheBuiltOne` passes and `--check` fails, naming
  only the recorded version name (round 1's script passes it); a
  `classes2.dex`, a `classes.dex` in a subdirectory or a `classes2.dex`
  symlink after d8 stops the build (round 2's script lets the last two
  through). Docs: 4 files, 50 relative links, no problem; the docs tests
  pass. In the decoy sandbox the tests made no connection, and the
  positive control two. `git diff --check` and the hashes agree. The
  checks ran before this bullet was written; packing again with it
  changed only the handoff.
- Review round 3 (NEEDS CHANGES), on this entry only. MEDIUM: the "On a
  device" bullet called the capture "1871 KB of sound", where every
  statistics line had `peak=0`, digital silence, and its "Not run" left
  out media: the run showed the rules registered and a silent stream, not
  the media and game sound captured that round 1 asked for. LOW: the
  round-3 snapshot bullet said the guard tests pass with the provenance
  and `serverVersion` both 4.2, but the check ran only
  `TestServerVersionIsTheBuiltOne`, and round 3's new test fails there (so
  `go test` now catches round 1's gap too); the same sentence in round 2's
  snapshot bullet is narrowed to that test as well. NITs: the new test's
  account (it reads only lines that start with `VERSION_NAME=`); "11 to 13
  fps", the first and last of each run's statistics lines; "private
  constructors", where those that differ are synthetic package-private
  ones (all corrected above). The review rebuilt the device binary byte for
  byte, ran the d8 guard against 21 output layouts (each extra entry stops
  the build; round 2's guard let 7 through) and 15 real `--check` runs
  (each names what differs), and recounted the classes with dexdump. Its
  suggestions, taken: say the server's own output was not visible either;
  save the one-off probes (the later read-only probe above); point round
  1's "Not run" bullet to the device bullet; word the test's account by
  what it reads. Not taken: a Go test reading the version name from the
  jar's dex, as `--check`, which CI's android job runs, already fails on a
  jar built with another one.
- Round 4 changes only this entry, and moves the part onto part 46's third
  round, which changes none of its files but the handoff; the part's
  other files are round 3's bytes.
- Verification, snapshot round 4 (`/tmp/opencode/review-49d`, on part 46's
  third round; go1.27.1, iso.env, every Go command and build in the bwrap
  sandbox): the same checks as round 3's, with the same results, and two
  more on copies. gofmt, vet and build clean; `go test ./...` 507 passed, 3
  skipped, 0 failed; the 15 related tests under `-race -count=3`; the 5
  guard tests 20 times and 5 times under `-race`; `--check` OK on the
  snapshot's tree, which it leaves as it was; the fail-before and mutation
  runs, the two undated builds and the d8 guard cases as in round 3. New,
  with all five guard tests: with the provenance and `serverVersion` both
  4.2, only `TestServerScriptVersionIsTheRecordedOne` fails, and `--check`
  names only the recorded version name; with the script set to 4.2 too
  and the jar not rebuilt, all five pass, and `--check` fails, naming the
  embedded jar and the recorded sha256 (the snapshot's
  `checks/extra-49-v.txt`, by `/tmp/opencode/check-49-v.sh`). Docs: 4
  files, 50 relative links, no problem; the docs tests pass. In the decoy
  sandbox the tests made no connection, and the positive control two.
  `git diff --check` and the hashes agree. The checks ran before this
  bullet was written; packing again with it changed only the handoff.

## Update (2026-10-21: QoL overhaul, part 46: the sound status tells the truth after audio errors)

- The session's sound has one account, `streamState.soundEnd`
  (`stream.go`), read through `soundStopped()`. `runAudio` sets it, with
  its error, however it ends other than at scterm's quit: codec id 0 as
  before, and now an audio config error (codec id 1), a codec scterm
  cannot decode, a codec id cut short, and the stream ending or failing
  to decode while the picture goes on. `audioOff` and `audioTurnedOff()`
  are gone. Before, only codec id 0 counted: after any other end, every
  display still said the sound was on.
- Readers: the browser's hello and status (`soundOn()`: `audio` off,
  `hostAudio` false), the terminal's status row (`audio off`, no
  `vol N%`) and `--no-tui`'s statistics line (`audio=off`, no pcm, peak
  or lag). The page's HUD already says `audio off` for any status but
  "on", so `web/player.js`'s code is unchanged (round 2 rewords a comment
  there). The terminal's notice when the stream ends is unchanged.
- Quit: `newApp` gives the stream the app's `quitting` flag, and once it
  is set the end is not recorded. The error cannot tell a quit:
  `isClosedConnErr` also matches EOF, and a peer session's teardown can
  end the read with EOF. main stops the session only after `run`
  returns, so no display reads the account then anyway.
- `--audio-dup`: `sendStatus` (`web.go`) and the `gain` op
  (`webinput.go`) read the sink through `failure()`, not `err`. A sink
  whose write fails mid-session now shows as one that did not open:
  `hostAudio` false (as before), `gain` 100, `muted` false, and the
  `gain` op leaves its volume alone, as the terminal's volume keys do.
  The two reads were no data race (since part 37, `err` is set before
  the sink is shared, and a failed write sets the atomic `writeFailed`),
  but they were blind to a failed write.
- Docs: README's "No sound" bullet (both bottom lines say `audio off`
  once the sound stops later); docs/ARCHITECTURE.md's sound bullets (how
  the sound stops and what each display then says; `hostAudio`, `gain`,
  `muted` and the `gain` op once this computer's stream has failed).
- New tests: `TestSoundThatStopsIsOffEverywhere` (six ways the sound
  stops, each in the browser's hello and status, the status row and its
  replay, and the statistics line after `runHeadless` says why),
  `TestQuitIsNotTheSoundStopping` (`newApp`: an end at the quit leaves
  the sound on, one before it stops it) and
  `TestBrowserSeesTheSinkFailWhilePlaying` (`runWeb`, `--audio-dup`, a
  write that fails, then a gain op and a quit op over the websocket).
  `TestSoundThatStartedIsNotSaidToBeOff` pinned `vol 100%` after the
  stream ended: renamed `TestSoundThatStartedIsNotSaidToBeTurnedOff`, it
  wants `audio off`.
- Checks (builder's tree, base `19c898d`, go1.27.1, iso.env, bwrap):
  - gofmt, vet, build and `git diff --check` clean.
  - `go test ./...`: 502 passed, 3 skipped, 0 failed (base 499, 3, 0;
    +3 new, 1 renamed). `-race ./...`: 502, 3, 0, no race.
  - The 4 new or changed tests: `-race -count=20` 80/80, no race;
    `-count=20` 80/80.
  - On the base's `stream.go`, `app.go`, `web.go` and `webinput.go`,
    with a shim giving `soundStopped()` the base's meaning (codec id 0
    only), the main package has 468 passed, 1 skipped, 4 failed: the 4
    new or changed tests. There, the sink test under `-race` fails 3 of
    3 runs on the volume, with no race reported.
  - 9 of 9 mutants killed: the account for codec id 0 only; `soundOn`,
    the row and the statistics line each without it; no quit check;
    `newApp` without the flag; status and gain op back on `err`; one
    fixed reason for every end.
  - Docs tests 10 pass; mdcheck: 2 files, 36 relative links, 0 problems.
  - Node suites (`web/` untouched in the builder's tree): audio 183,
    conn 61, input 130, page 286, pointer 17, worklet 41 pass, 0 fail.
- Left alone: once the sound has stopped with the sink still open, the
  terminal's volume keys and the `gain` op still set its volume, which
  plays nothing, and the browser's status gives that volume (the
  terminal's row says `audio off`); the follower still follows the
  desktop's sink. `--web` prints no line when the sound stream simply
  ends (as before). `web/check.mjs` says "browser only (host sink stays
  silent)" whenever `hostAudio` is false. A mutant that swaps the two
  defers in `runAudio` was not run (a microsecond window, so no test
  catches it reliably); the comment states the order.
- Not run by the builder: a real device, sound server, browser or
  `--window` window; the scterm binary end to end; a decoder failure
  (libavcodec reports a bad Opus packet when it is sent, which
  `runAudio` skips); the desktop-like decoy-socket sandbox; CI; Android.
- Intake: the builder's base had part 44's first round; part 44's second
  round changed two of its tests. `audio_here_test.go` was merged three
  ways: the merge adds exactly this part's 137 changed lines and keeps
  part 44's second round's 31. The other eight files were taken as
  built, byte for byte; part 44's second round changes none of them.
- Verification, snapshot round 1 (`/tmp/opencode/review-46`, on part 44's
  second round; go1.27.1, iso.env, every Go command in the bwrap
  sandbox): gofmt, vet and build clean; `go test ./...` 502 passed, 3
  skipped, 0 failed; the 38 sound tests under `-race -count=3`; the 4 new
  and changed tests 20 times and 5 times under `-race`. On the base's
  four files with the builder's shim, all 4 fail, and the sink test fails
  3 of 3 runs under `-race`; each of the 9 mutants fails between 1 and 3
  of the related tests. Docs: 2 files, 36 relative links, no problem; the
  docs tests pass. In the decoy sandbox the tests made no connection, and
  the positive control two. `git diff --check` and the hashes agree. The
  checks ran on a first build, kept as review-46.checked; the rebuild
  with this bullet changed only the handoff.
- Review round 1 (PASS), with one LOW and two NITs. LOW, from reading the
  code: nothing orders the status the 500 ms ticker works out and the one
  the dispatcher sends after a gain op, so a tick's status worked out at
  100% before the op could be written after the op's 40%, and
  `TestBrowserSeesTheSinkFailWhilePlaying` then took it for the status
  after the sink failed, and failed on `hostAudio` true. Part 44's test
  uses the same helper, but never waits for a volume it had before, so
  an older status cannot match there. NITs: two comments outside the
  delta still said the device turned the sound off (`web/player.js`'s
  `audioLabel`, `TestBrowserIsToldTheSessionsSound`); this entry said
  "the status" gives a stopped sink's volume, where only the browser's
  does, and pointed at the builder's brief. The review re-ran the
  snapshot's checks, with the 4 new and changed tests 10 times and 4
  times under `-race` and 3 of the builder's 9 mutants (M5, M7, M8), and
  the whole module under `-race` too (502 passed, 3 skipped, 0 failed);
  of its own mutants, R7 (`muted` outside the `failure()` check: the
  same, as a muted sink writes nothing that could fail) and R1 (the
  defers swapped) survive.
- Round 2 changes `audio_write_test.go`, the two comments,
  `TestQuitIsNotTheSoundStopping` and this entry. After the failure, the
  sink test waits for a status at 100% with `hostAudio` false, and it
  logs the last status it read when none comes (on the old code: 40%,
  `hostAudio` false; the old code saw the failure in `hostAudio`, through
  `plays()`, but not in the volume). `TestQuitIsNotTheSoundStopping`
  calls `isolatePulse`, so a sink its sessions opened would reach no
  real server. Not taken: a line from `--web` when the sound stream ends
  with EOF (left alone above).
- Verification, round 2 (`/tmp/opencode/dev-46b`, in the worktree, every
  Go command in the bwrap sandbox): with `web.go` changed to force that
  order (`mkforced.py`: a status every 20 ms; the receive loop held for
  100 ms after the hello, so the gain op is read late; a status at 100%
  with the sink playing written 300 ms late), round 1's sink test failed
  10 of 10 runs, on `hostAudio` true, and round 2's passed 10 of 10.
- Verification, snapshot round 2 (`/tmp/opencode/review-46b`, on part 44's
  second round, which landed as 10bbfbf; every Go command in the bwrap
  sandbox with `iso.env`): gofmt, vet and build clean; 502 tests pass, 3
  skip, 0 fail; the 38 related tests pass three times under `-race`, and
  the 4 new and changed tests 20 times, and 5 times under `-race`. With the
  base's four files and the builder's shim, all 4 fail, and the sink test
  fails 3 of 3 under `-race`, on the volume, with no race reported; each of
  the builder's 9 mutants fails a test. With the order forced as above,
  round 2's sink test passes 20 of 20 runs and round 1's fails 20 of 20, on
  `hostAudio` true. Docs: 2 files, 36 links, no problem; the docs tests
  pass. In the decoy sandbox (decoy sound, display, D-Bus and adb sockets)
  no test connected; the positive control did, twice. The checks ran before
  this bullet was written; packing again with it changed only the handoff.
- Review round 2 (NEEDS CHANGES), on this entry and one comment. LOW:
  round 2's bullet said the old code's last status had `hostAudio` true;
  the builder's logs and the review's run of the old code show 40% with
  `hostAudio` false (corrected above, with the reason, as the review
  suggested). NITs: this entry called `web/player.js` unchanged after
  round 2 changed a comment there, gave the node suites' `web/` as
  untouched without saying it was the builder's tree, and said round 1's
  review re-ran every check; a comment in the sink test called a status
  at 40% the op's own, where a ticker status worked out after the op
  says 40% too (all corrected). The review forced three orders
  (round 1's; a status worked out across the failure; the op's own
  status written after the failure): round 2's sink test passed all
  three, round 1's failed only round 1's. It ran the node suites on
  round 2's `web/player.js` (0 failed; `go test` runs them too, through
  `TestWebClientSuites`), and saw `isolatePulse` keep a session that does
  open a sink off the decoy sound server (18 connections without it, 0
  with it). Its other suggestion, taken: the forced copy holds the
  receive loop, which handles the hello, so it is the gain op that is
  read late (the `mkforced.py` bullet above).
- Round 3 changes only this entry and that comment in
  `audio_write_test.go`; the part's other files are round 2's bytes.
- Verification, snapshot round 3 (`/tmp/opencode/review-46c`, on part 44's
  second round, which landed as 10bbfbf; every Go command in the bwrap
  sandbox with `iso.env`): the same checks as round 2's, with the same
  results. gofmt, vet and build clean; 502 tests pass, 3 skip, 0 fail; the
  38 related tests three times under `-race`; the 4 new and changed tests
  20 times, and 5 times under `-race`. The base's four files with the
  builder's shim fail all 4, and the sink test fails 3 of 3 under `-race`,
  waiting for the volume to read 100% again, with no race reported; each
  of the builder's 9 mutants fails a test. With the order forced, round
  3's sink test passes 20 of 20 runs and round 1's fails 20 of 20, each on
  `hostAudio` true. Docs: 2 files, 36 links, no problem; 13 docs tests
  pass. In the decoy sandbox no test connected; the positive control did,
  twice. The checks ran before this bullet was written; packing again
  with it changed only the handoff.

## Update (2026-10-21: QoL overhaul, part 44: a browser-only session leaves this computer's sound alone)

- Behaviour: a session whose only sound output is the browser (`--web`
  or `--window` without `--audio-dup`) does no sound work on this
  computer. `newApp` (`app.go`) opens a sink only when the session plays
  here (`playsHere`). A browser-only session gets `browserOnlySink`
  (`audio.go`), which is never opened: no `pactl` run to pick a sink, no
  libpulse connection (no stream in the desktop's mixer, no wait of up
  to about 0.45 s without a server), and no follower.
- `browserOnlySink` has no error, so `failure()` stays nil and a failed
  sink stays easy to tell apart (part 46). It is silent, so `plays()` is
  false. `audioFollower` (`audiofollow.go`) now asks `plays()` (opened,
  not failed, not silent) instead of `failure() == nil`.
- The browser still gets every packet: the PCM tap does not go through
  the sink. Hello and status keep their values (`audio` on, `hostAudio`
  false). The `gain` op is taken and reported (`gain`, `muted`), as with
  the silent sink before, and scales nothing the browser hears.
- Start-up line: `scterm: audio: browser only (pass --audio-dup to play
  it on this computer too)`. The failed-sink line (`browser only; no
  sound on this computer: …`) can no longer come without `--audio-dup`;
  it stays, unchanged, for `--audio-dup`, whose sink can still fail.
- Docs: README (the browser display's sound, the "No sound" bullet on
  other scterms' sound, `--audio-dup`) and docs/ARCHITECTURE.md's sound
  bullets. flags.go's `--audio-dup` help names no host sink: unchanged.
- New tests: `TestBrowserOnlySessionStaysOffTheSoundServer` (newApp and
  runWeb with a raw-PCM device, listeners as the sound server, a pactl
  that counts its runs, a 5 ms follower, a websocket viewer, the gain
  op), `TestBrowserOnlySinkStillDecodesForTheBrowser` (an Opus frame
  through libavcodec to the PCM taps) and `TestWebSaysWhereTheSoundPlays`
  (the three start-up lines).
- Tests changed where the old behaviour was the bug: the `web` case of
  `TestMainSilencesLeftoversOnlyWhenItPlaysHere` wants no connection;
  the follower cases of `TestWebRunStartsTheFollowerOnlyWhenItShould`
  and `TestWebRunStopsItsAudioFollower` run with `--audio-dup`, and two
  new browser-only cases want no follower; a browser-only case was
  added to `TestAudioFollowerNeedsASinkAndPactl`.
- A test narrowed, not for a bug:
  `TestWebWithoutPulseAudioSaysTheBrowserPlays` keeps only its
  `--audio-dup` case. Its other case, a browser-only session whose sink
  failed, would still pass, but `newApp` can no longer build it.
- Checks by the builder (base `afe9ebe`, go1.27.1, iso.env, bwrap):
  gofmt, vet, build and `git diff --check` clean; `go test ./...` 483
  pass, 3 skip, 0 fail (base 480, 3, 0). The base's tests on the new
  code: 477 pass, 3 skip, 3 fail:
  `TestMainSilencesLeftoversOnlyWhenItPlaysHere/web`,
  `TestWebRunStartsTheFollowerOnlyWhenItShould/a_sink_that_opened` and
  `TestWebRunStopsItsAudioFollower`. `-race -count=3` on 34 sound tests
  102/102, no race; the new and changed tests `-count=20` 160/160; the
  new ones `-race -count=5` 15/15.
- Fail-before and mutants: on the base's `app.go`, `audio.go` and
  `audiofollow.go` (with a shim for `browserOnlySink`), 5 of the 8 new
  or changed tests fail; the decode test and the two `--audio-dup` tests
  pass, as they should. 9 of 9 mutants are killed. Docs tests: 10 pass.
  mdcheck: 2 files, 35 links, 0 problems.
- Not run by the builder: a real sound server (PulseAudio or PipeWire),
  a real device, a browser, or a `--window` window (`--window` is
  checked through `newApp` only); `-race` on the whole module; the
  desktop-like decoy-socket sandbox (the snapshot checks below ran it).
  `web/check.mjs` still says "browser only (host sink stays silent)";
  web/ is outside this part.
- Intake: `README.md` was merged three ways with part 48's changes; the
  merge adds exactly this part's changed lines. The other seven files
  were taken as built, byte for byte; parts 48, 47 and 45, taken in
  first and landing first, change none of them.
- Verification, snapshot round 1 (`/tmp/opencode/review-44`, on part 45
  as it landed; go1.27.1, iso.env, every Go run in bwrap): gofmt, vet,
  build and `git diff --check` clean; `go test -v ./...` 499 pass, 3
  skip, 0 fail; `-race -count=3` on 34 sound tests; the 8 new and
  changed tests `-count=20` and `-race -count=5`; on the base's files
  with the builder's shim, 5 of the 8 fail and 3 pass, as above; each
  of the builder's 9 mutants fails 1 to 3 tests; docs checker 2 files,
  36 relative links, 0 problems; in the desktop-like decoy sandbox the
  tests make no connection, and the positive control (`pactl info` and
  a connect to adb's port) two.
- Review round 1 (PASS), with one LOW and three NITs. LOW: no test had
  a `--peer` session with sound in the browser display, so a `newApp`
  that opened a sink for one (the review's mutant R12) passed every
  test. NITs, on this entry: it listed the decoy sandbox as not run,
  though the intake's checks ran it; "the three named above" pointed
  at a bullet that names five tests; and it put the narrowed test
  among those changed for a bug. The narrowed case was also the only
  check of the order of `runWeb`'s failed-sink and browser-only cases:
  their swap (mutant R11) changes nothing for any sink `newApp` builds,
  and stays untested. Suggestions taken: counts of its own for each
  step of the browser-only test, a positive control there, and a check
  that the failed-sink line gives the open's error. Left for later:
  with `--audio-dup`, the sink scales the shared PCM buffer in place
  before the browser's tap, so a `gain` op changes what the browser
  hears as well (older than this part; no shipped client sends one);
  `web/check.mjs`'s wording, above.
- Round 2 changes two tests and this entry; the code is round 1's. In
  `TestBrowserOnlySessionStaysOffTheSoundServer`, a `--peer --web`
  session whose target sends sound must stay off the sound server too;
  each step counts only its own connections and pactl runs; and last,
  a `--web --audio-dup` session must connect and run pactl, so the
  listeners and the counting pactl would have seen the others. In
  `TestWebSaysWhereTheSoundPlays`, the failed-sink line's reason must
  be the open's error (`audio sink: …`), not an empty one.
- Verification, snapshot round 2 (`/tmp/opencode/review-44b`, on round
  1's base; go1.27.1, iso.env, every Go run in bwrap): gofmt, vet,
  build and `git diff --check` clean; `go test -v ./...` 499 pass, 3
  skip, 0 fail; `-race -count=3` on 34 sound tests; the 8 new and
  changed tests `-count=20` and `-race -count=5`; on the base's files
  with the builder's shim, 5 of the 8 fail and 3 pass; each of the
  builder's 9 mutants fails 1 to 3 tests. Of round 1's review's 13
  mutants, 12 fail 1 to 4 tests: R12 fails the browser-only test at
  `--peer --web` (6 connections, 3 pactl runs), and R1 now fails it at
  `--window` only. R11 passes, as above, and R12 with round 1's
  `audio_here_test.go` passes, the gap. Docs checker: 2 files, 36
  relative links, 0 problems. In the decoy sandbox the tests make no
  connection, and the positive control two. These checks ran before
  this bullet was written; the snapshot was then packed again with it,
  which changed only the handoff.

## Update (2026-10-21: QoL overhaul, part 45: pactl is read the same in every language)

- pactl translates what scterm reads (gettext domain "pulseaudio"): the
  "Sink Input #N" and "Sink: N" of its listing, and pactl info's field
  names. Under another language the follower found no scterm stream and
  never moved the sound. One builder, `cLocaleEnv` (`pulsecli.go`), now
  gives every pactl run `LC_ALL=C` without `LANGUAGE`, keeping the rest of
  the environment: `pactlTool.run` (the follower, `pulseCmd`,
  `pulseDefaultSink`) and doctor's `doctorOutput`, which kept `LANGUAGE`.
- The start-up cleanup runs no pactl (libpulse API): unchanged.
- Checked: the gettext 1.0 manual and man 3 gettext (no translation in C,
  `LANGUAGE` ignored there); glibc 2.44 does the same for C, POSIX and
  C.UTF-8; pactl prints names and properties through printf and
  pa_proplist, with no charset change (its one pa_locale_to_utf8 call is
  for -n). C changes no text scterm reads, so C.UTF-8 is not needed.
- Real probe, inside bwrap, on a private PipeWire 1.6.9, with the real
  pactl 17.0 and a stand-in ja catalog (the host has no pulseaudio.mo):
  - BASE in LANG=ja_JP.UTF-8 LANGUAGE=ja read "シンク入力 #45" and never
    moved the stream; the fix moved it to the default in 500 ms, then to a
    sink named scterm_テスト_ü in 250 ms.
  - Doctor and the cleanup worked on both.
  - Names, descriptions and properties were byte-identical in C, C.UTF-8,
    en_US.UTF-8 and ja_JP.UTF-8.
- New tests (`pulsecli_test.go`): `TestCLocaleEnvChangesOnlyTheLocale`,
  `TestEveryPactlRunIsInTheCLocale`, `TestAudioFollowerFollowsInAnyLanguage`.
  `doctor_test.go`: doctorHost sets LANGUAGE=fr; `workingPactl` answers in
  English only with LC_ALL=C and no LANGUAGE. On BASE: 6 tests fail (4
  doctor, 2 new); the unit test does not build. 10 of 10 mutants killed.
- Checks (afe9ebe, go1.27.1, iso.env, bwrap): gofmt, vet, build and
  `git diff --check` clean; `go test ./...` 483 passed, 3 skipped, 0
  failed (BASE 480, 3, 0); `-race ./...` 483/3/0, no race; `-race` on 31
  touched tests 31/0/0; the new tests `-race -count=20` 60/60.
- Not run: a real pulseaudio catalog (none on this host); pactl's source
  (its binary and the probes only); musl or other glibc versions; a real
  PulseAudio daemon; the scterm binary end to end; the node suites (`web/`
  untouched); Android.
- Intake: the worktree's copies of the four files were this part's base
  (`afe9ebe`), so they were taken as built, byte for byte. Parts 48 and
  47, taken in first and landing first, change none of them. The builder's
  optional sentence for `docs/ARCHITECTURE.md` is left out.

## Update (2026-10-21: QoL overhaul, part 47: device text cleaned in the last places)

- Device text reaches the terminal cleaned in the command line's last
  places, each through `cleanText`: the names in `Store.Find`'s error when
  a `--peer` or `scterm forget` query means several devices; the `--peer`
  chooser; `scterm pair`'s chooser (names from the search, which anyone on
  the network can answer), its two prompts, `nearbyFailure` (the name, a
  refusal's words, any other failure's text) and "Paired with NAME"; a
  refusal of `scterm pair "HOST:PORT CODE"`; `scterm forget`'s question,
  "Kept NAME." and "Forgot NAME."; and what adb writes to stderr, in
  `adbRunner.run`'s errors, now on one line (the "adb reverse unavailable
  (...)" line no longer breaks, and an error no longer ends in a line
  break). Text without control or format characters reads as before, but
  runs of spaces fold into one and spaces at either end go.
- Names are stored, and matched, as the device gave them: `--peer` and
  `scterm forget` take the whole stored name, and "Connect with: scterm
  --peer %q" and forget's "is only part of" refusal quote it as stored
  (`%q` escapes control and format characters).
- `cleanText` everywhere: it also takes out format characters (bidi
  overrides that reorder the row, zero-width characters) and keeps adb's
  lines apart; `cleanPeerText` deletes control characters and trims the
  ends (it does not clip), so a format character passes and adb's lines run
  together. Its sites (`scterm peers`, `--peer` failures, doctor) are as
  they were.
- `Store.Find` returns `*peer.AmbiguousError` (the query and the matches)
  when a query means several devices, worded as before; `findPeer`
  (peernearby.go) cleans each name in a copy. Cleaning the whole message
  left a space before a comma after a name ending in a control or format
  character, and folded spaces in the quoted query. The peer package does
  no cleaning.
- `nearbyFailure` says "NAME declined" for a refusal whose words clean to
  nothing; it and `pairCommand` no longer wrap the cause (`%w`), which no
  caller unwraps. `invitationSearch` (peernearby.go) holds
  `peer.DiscoverNearby`, so tests stay off the network.
- Tests: devicetext_test.go (new) has `TestSeveralMatchesListCleanNames`,
  `TestPeerChooserCleansTheNames`, `TestForgetCleansTheName`,
  `TestNearbyFailureCleansWhatTheDeviceSaid`,
  `TestPairNearbyCleansWhatTheDevicesSay` (a faked search, a loopback
  target speaking the nearby protocol),
  `TestPairByInvitationCleansWhatTheDeviceSays` and
  `TestADBFailureCleansWhatADBSaid` (a fake adb); peer_test.go has
  `TestStoreFindHandsBackSeveralMatches`. Inputs hold OSC and CSI
  sequences, a C1 CSI, BEL, CR, a right-to-left override, an isolate,
  zero-width and soft-hyphen characters, and a lone 0x9b byte.
- Verification (builder's tree, base `afe9ebe`, iso.env, bwrap, go1.27.1):
  gofmt, vet, build and `git diff --check` clean. `go test -count=1 -v
  ./...`: 488 pass, 3 skip, 0 fail (base 480, 3, 0; +8 new); `-race`: the
  same, no race. The 8 new and 19 related tests under `-race`: 27/27; the
  new tests x20: 160/160, under `-race` 160/160. On the base files
  (peernearby.go with only `invitationSearch` added) the 7 new main tests
  fail; the peer test does not build. Mutants: 30 of 30 killed.
- Left alone: `cleanPeerText` lets format characters through (`scterm
  peers`, `--peer` failures, doctor), and `peers --check` stores the name
  it probes that way; `adb devices` serials (a USB device's comes from the
  device) reach `pickDevice`'s errors raw, where a serial is also shown to
  be typed back; a name that cleans to nothing leaves "Waiting for  to
  accept…" and the like without a name; the `--peer %q` hint is in double
  quotes, where a shell expands `$`.
- Not run: a real terminal, adb, phone, peer target, Android app or
  multicast search; the node suites (web/ untouched).
- Intake: the worktree's copies of the six files were this part's base
  (`afe9ebe`), so they were taken as built, byte for byte. Part 48, taken
  in first and landing first, changes none of them.

## Update (2026-10-21: QoL overhaul, part 48: the scrcpy server's licence comes with every release)

- scterm is MIT and bundles the scrcpy server, under the Apache License
  2.0, in the Linux binary (the embedded jar) and in the APK (the helper
  built from `third_party/scrcpy-server-src`). Neither carried the Apache
  text, which its §4(a) asks recipients to get.
- `scterm update` fetches only the binary, so the binary now carries both
  texts. `scterm licenses` (`licenses.go`) prints `LICENSE` and
  `third_party/LICENSE.scrcpy` whole, as `go:embed` takes them from the
  repository, each after a line saying what it covers. It exits 0, `-h`
  prints its help, and an argument is a usage error with exit status 1. It
  is in scterm -h's Commands, the `commands` list (`scterm licence` gets
  "did you mean licenses?") and the README's table of commands.
- The release job attaches `LICENSE` and `third_party/LICENSE.scrcpy`
  (uploaded as `LICENSE.scrcpy`) next to the binary and the APK. Both
  updaters pick their asset by name, so neither sees the new ones.
- The APK carries both texts as `assets/LICENSE` and
  `assets/LICENSE.scrcpy`: copies under `android/app/src/main/assets`,
  with no Gradle or UI change. A Go test keeps them identical to their
  sources.
- `third_party/README.md` (new) lists scterm's changes to the vendored
  server: `AudioPlaybackCapture.java`, commit `afcd41b` (playback capture
  also records game and untagged audio). A test keeps the list equal to
  the files marked `scterm patch`. There is no NOTICE file to carry
  (§4(d)). The vendored source and the jar are unchanged (part 49).
- Docs: the README's License section says where the texts are and links
  the list. `docs/RELEASING.md` names the release's files.
  `android/README.md` has a License section.
- Tests: `TestSubcommandHelp`, `TestHelpIsGrouped`,
  `TestLeftoverWordsAreRefused` and `TestCommandLineExitStatus` cover the
  command. Five new tests in `licenses_test.go` cover the output (checked
  against the files), the APK copies, the release's `files` list (the
  action only warns about a missing file), the README and the list.
- Verification, every Go command in the bwrap sandbox with `iso.env`:
  - At BASE, the new tests do not build (`runLicensesCommand` undefined).
    With a stub that handles nothing, as BASE's main does, all 9 new or
    changed tests fail.
  - 16 mutations, 16 caught.
  - `go test ./...`: 485 passed, 3 skipped, 0 failed (BASE: 480, 3, 0).
  - `-race` on the 9 tests passes, and so does `-count=20` on the 5 new
    ones.
  - gofmt, vet, build and a release-flags build are clean. The binary
    prints 12580 bytes, at most 79 columns.
  - The docs checker finds 44 relative links and 0 problems.
  - aapt2 link with `-A` packs both assets unchanged.
  - `git diff --check` flags only the blank line that ends the
    byte-identical copy of `LICENSE.scrcpy`.
- Left alone: the APK's `/META-INF/{AL2.0,LGPL2.1}` exclusion (a Gradle
  change), and the BSD licences of Go's standard library, `golang.org/x/net`
  and `golang.org/x/sys` in the binary.
- Not run: CI (the release job's two new lines), Gradle and the APKs it
  builds (AGP's packing of the assets), the node suites (`web/` untouched).
- Intake: the worktree was this part's base (`afe9ebe`), so the 13 files
  were taken as built, byte for byte. Intake adds `.gitattributes`, which
  stops `git diff --check` flagging the blank line that ends
  `assets/LICENSE.scrcpy` (`whitespace=-blank-at-eof`, for that file only):
  the copy must stay identical to `third_party/LICENSE.scrcpy`, which ends
  the same way. For part 49, which rebuilds the jar:
  the README's line saying the bundled server records no game sound
  (README.md:588 here) becomes false, and `third_party/README.md` could
  name its build script and provenance file.

## Update (2026-10-21: QoL overhaul, part 43: two noise messages from the second phone run)

- Found on the second real-device run (sound and control): `scterm --no-tui`
  printed `scterm: audio: audio stream ended: read tcp ...: use of closed
  network connection` on a normal Ctrl+C (exit status 0), and the sound
  follower said `scterm: audio -> SINK` at start for the sink the stream
  already played on, which the terminal display's replay repeated at exit.
- `runHeadless` (`app.go`) now sets `quitting` when its loop ends, on a quit
  and on the end of the video stream, as the terminal display and `runWeb`
  do, and its audio goroutine takes `watchStreams`' rule: a closed-socket
  error once the run has ended is not printed; EOF from the device before
  then, and any other error, still is. `isClosedConnErr` is unchanged.
- The follower (`audiofollow.go`) moves the stream, and says so, only when
  it is not on the default sink yet, as pactl tells it: `sctSinkInputs`
  (`pulsecli.go`) also reads each stream's sink index from the `Sink:` line
  of the listing it already asks for, and the new `sinkIndex` reads the
  default's index from `pactl list sinks short`, asked only on a tick where
  the default differs from the one last seen (at start, after a switch,
  when the stream comes back), and again at each tick after a move that
  failed, until one works; other ticks make 2 pactl calls, as before. When
  pactl does not give both indices, the follower moves the stream, as
  before.
- Decided: each move is said (`noticef`, not `logOnce`). With `logOnce`, A
  to B, back to A and to B again left the second move to B unsaid, which a
  user switching outputs cannot tell from a follower that has stopped. When
  pactl gives both indices, a stream already on the default is not moved,
  so no line repeats without a switch; the replay at exit counts repeats
  ("(2 times)"). A browser-only `--web` or `--window` session follows too,
  moving a stream kept silent, so it now prints a line at each switch,
  where `logOnce` printed one per sink.
- New tests: `TestHeadlessTeardownSocketErrorsAreQuiet` (`exit_test.go`: a
  quit and the loss of the picture, each with the demuxer mid-stream on a
  TCP socket; EOF before a quit; an audio error after one), and
  `TestAudioFollowerMovesOnlyAStreamThatIsElsewhere` (4 cases) and
  `TestAudioFollowerSaysEachSwitch` (`audiofollow_test.go`, with a fake
  pactl whose `move-sink-input` moves the stream). On the base's `app.go`,
  `audiofollow.go` and `pulsecli.go` all 3 fail, the quit with the phone
  run's line; each of the builder's 12 mutants fails a test.
- Checks by the builder (on `b4c410d`, the branch with parts 37 to 42;
  go1.27.1, `iso.env`, bwrap): gofmt, vet, build and `git diff --check`
  clean; `go test ./...` 479 passed, 3 skipped, 0 failed (the base 476, 3,
  0: the 3 new tests); `-race` on 23 tests of the touched files and their
  neighbours 23/0/0, no race; the new tests `-count=20` 60/60, and 60/60
  under `-race`.
- Intake: the five files went into the worktree as built, as none had
  changed there since the builder's base, which held part 42's first round;
  part 42's second round changes none of them.
- Verification, snapshot round 1 (`/tmp/opencode/review-43`, on part 42's
  second round), every Go command inside the bwrap sandbox with `iso.env`:
  the base's copies of the five files are the builder's starting ones;
  gofmt, vet, the build and `git diff --check`; `go test ./...` (479
  top-level tests pass, 3 skip, 0 fail); `-race -count=3` on the builder's
  23 related tests; the 3 new tests 20 times and 5 times under `-race`.
  Under the new tests the base's `app.go`, `audiofollow.go` and
  `pulsecli.go` fail all 3, and each of the builder's 12 mutants fails at
  least one. The whole module, in the desktop-like sandbox, connects to
  none of its decoy sockets, while the positive control reaches two. These
  checks ran before this bullet was written; the snapshot was then built
  again with it, which changed only the handoff.
- Review round 1 (NEEDS CHANGES), MEDIUM: no test retried a failed move.
  The loop's `already = false` (`audiofollow.go`) is what keeps a stream
  whose move failed counted as elsewhere, so that the next tick tries
  again; with that line deleted the whole module still passed (479 pass, 3
  skip, 0 fail), and one failed move (a Bluetooth sink not ready yet, say)
  would leave the sound on the old output. The base had no such test
  either; its fake pactl did not answer `move-sink-input`. NIT: three
  phrases above said more than the code: the follower also moves the
  stream, and says so, when pactl gives only one of the two indices; the
  default's index is also asked at each tick after a failed move; and no
  line repeats without a switch only when pactl gives both. All three are
  corrected above. Of its suggestions, round 2 says what browser-only
  sessions now print (above); running pactl with `LC_ALL=C`, a test of the
  reset of a stream's sink at each block of the listing, and the signal
  goroutine each test run of `runHeadless` leaves are left alone (below).
- Round 2 changes only `audiofollow_test.go` and this entry. `sinksPactl`'s
  server gains sink-c (index 3, a Bluetooth sink in its listing), which
  takes no stream until a file `sink-c-ready` is in the test's directory.
  The new `TestAudioFollowerRetriesAFailedMove` makes sink-c the default
  with the stream on sink-a: the follower must try the move at every tick,
  asking pactl for the sinks each time and saying nothing, until the file
  appears; then the move works and is said once, and at the next 3 ticks
  or more the follower tries no move and asks for no sinks (round 3 checks
  that it asks pactl only for the default sink and scterm's streams). The
  other two follower tests use sink-a and sink-b only, so the third sink
  changes nothing for them.
- Verification, round 2, in the worktree (on part 42 as landed, `af78346`),
  every Go command inside the bwrap sandbox with `iso.env`: gofmt and vet
  clean; the new test passes 3 runs of 3; with the review's R12 (no
  `already = false`) it fails, as the follower never tries the move a
  second time, and R12 passes round 1's test file, the gap.
- Verification, snapshot round 2 (`/tmp/opencode/review-43b`, on part 42 as
  landed: its base's tree is `af78346`'s), every Go command inside the
  bwrap sandbox with `iso.env`: gofmt, vet, the build and `git diff
  --check`; `go test ./...` (480 top-level tests pass, 3 skip, 0 fail);
  `-race -count=3` on the 24 related tests; the 4 new tests 20 times and 5
  times under `-race`. Under the new tests the base's `app.go`,
  `audiofollow.go` and `pulsecli.go` fail all 4, the retry test only
  through its count of the sinks listings (the base tried the move at each
  of 6 ticks and said the one that worked once, but never asked for the
  sinks). Each of the builder's 12 mutants fails a test, as in round 1, and
  each of the review's 9 fails one too: R12 only the retry test, which also
  catches R2 and R10; under round 1's test file R12 passes, the gap. The
  whole module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two. These checks ran before
  this bullet was written; the snapshot was then packed again with it,
  which changed only the handoff.
- Review round 2 (NEEDS CHANGES), MEDIUM: the new test could fail on
  correct code. The fake writes down each call before it looks for
  `sink-c-ready`, so the move written down fifth when the test wrote the
  file could still work, while the test, which waited for 5 moves, wanted
  6 ticks up to the move that worked. The review saw 1 failure in 1000
  runs, 8 in 450 with three copies at once on one CPU, and 20 in 20 with a
  3 ms pause in the fake before it looks for the file. NIT: the test's
  comment and the Round 2 bullet said that after the move the follower
  asks pactl only for the default sink and scterm's streams, but the test
  checked only that it tries no move and asks for no sinks: a follower that
  also asked for the sink's volume at each tick passed the whole package.
  Of its suggestions, round 3 prints the calls in the failure messages; a
  test that the sinks are asked again when the stream comes back is left
  alone (below).
- Round 3 changes only `TestAudioFollowerRetriesAFailedMove` and this
  entry. The test waits for 6 tried moves before it writes the file: the
  follower waits for each pactl to end before it runs the next, so by then
  the first 5 have failed, and the 6th may find the file. It checks the
  calls in full: up to the move that worked, each tick asks for the default
  sink, scterm's streams and the sinks, then tries the move of stream 7 to
  sink-c, at 6 ticks or more; after it, pactl is asked only for the default
  sink and scterm's streams, at 3 ticks or more. Both failure messages
  print the calls they looked at.
- Verification, round 3, in the worktree (on `af78346`), every Go command
  inside the bwrap sandbox with `iso.env`: gofmt and vet clean; the retry
  test passes 1000 runs of 1000, and 450 of 450 as three copies at once on
  one CPU; with the review's 3 ms pause in the fake it passes 20 runs of
  20, where round 2's test fails 20 of 20 ("5 ticks, 5 moves tried").
  Against the follower tests, the round-2 review's X1, X2, X3, X5, X6, X7
  and X15 each fail the retry test only, and the round-1 review's R2, R4,
  R5, R6, R8, R10 and R12 each fail a test (R12 the retry test only); X15
  passes round 2's test file and R12 round 1's, the gaps; X9 (the last
  default kept when the stream disappears) and X16 (no reset of a stream's
  sink at a new block of the listing) pass, as said below. The follower
  tests pass 5 times under `-race`; `go test ./...` 480 top-level tests
  pass, 3 skip, 0 fail.
- Verification, snapshot round 3 (`/tmp/opencode/review-43c`, on part 42 as
  landed), every Go command inside the bwrap sandbox with `iso.env`: gofmt,
  vet, the build and `git diff --check`; `go test ./...` (480 top-level
  tests pass, 3 skip, 0 fail); `-race -count=3` on the 24 related tests;
  the 4 new tests 20 times and 5 times under `-race`. Under the new tests
  the base's `app.go`, `audiofollow.go` and `pulsecli.go` fail all 4, the
  retry test only at its calls before the move, which on the base never
  ask for the sinks. Each of the builder's 12 mutants fails a new test;
  each of the round-1 review's 9, and of the round-2 review's X1, X2, X3,
  X5, X6, X7 and X15, fails a test, while X9 and X16 pass; X15 passes
  round 2's test file and R12 round 1's, the gaps. With the review's 3 ms
  pause in the fake, round 2's retry test fails 20 runs of 20 and round
  3's passes 20 of 20. The whole module, in the desktop-like sandbox,
  connects to none of its decoy sockets, while the positive control
  reaches two. These checks ran before this bullet was written; the
  snapshot was then packed again with it, which changed only the handoff.
- Left alone: the browser path's own rule (it also drops EOF before a
  quit); `--no-tui` starts no follower; the follower still moves the stream
  to a Bluetooth or virtual default the start-up choice passed over, now
  only when the stream is elsewhere; a stream whose move keeps failing now
  costs a third pactl call on each retry tick; resetting the stream's sink
  at each new block of the listing has no test (it matters only for a block
  without a `Sink:` line, which pactl 17 never prints), nor has forgetting
  the last default when the stream disappears, so that the sinks are asked
  again when it comes back (the follower did so before this part too); each
  test run of `runHeadless` leaves its signal goroutine running, as the
  older tests' runs do.
- Not run: any real device, sound server or pactl (fakes only), so pactl in
  another language too (if it translates the labels the parser reads, the
  follower does nothing, as before; running pactl with `LC_ALL=C` would
  avoid that, a change of its own); the node suites (`web/` untouched);
  Android; `-race` on the whole module.

## Update (2026-10-21: QoL overhaul, part 42: the picture's scaler stays inside the picture)

- Found while making the pull request's screenshots: at 80x24 the terminal
  showed a stripe of stray colour in the black margin right of the picture.
  libswscale's vector code converts whole blocks of pixels. Scaled into the
  canvas, a picture of an even width that is not a multiple of 8 spilled 2
  to 6 pixels into the margin right of it on every row but the last two:
  columns 50 to 53 at 80x24 with a 576x1280 stream. (The display's widths
  are even, as `fitPicture` makes them; none of the 33 odd widths in the
  builder's probe spilled.) At the frame's own size (screenshots,
  `--dump-frames`, and every frame of the browser and window display, whose
  canvas is the video) the blocks are 16 pixels on the computer tested (a
  Ryzen 7 7800X3D, libswscale 10.1.102): a 1080- or 584-wide frame wrote 32
  bytes past the end of a buffer of its exact size, into Go memory, and a
  34- or 578-wide one left each row's last 2 pixels unwritten.
- `sct_vdec_scale` (`native.c`) now scales into a frame the decoder owns
  (`av_frame_get_buffer`, rows padded to 64 bytes, freed with the decoder,
  allocated again when the size asked for changes) and copies exactly
  `dst_w` pixels of each row into `dst`, whatever libswscale's block size.
  `native.h` says nothing outside the region is written; a `stream.go`
  comment no longer says swscale writes only the fit region.
- `scale_test.go` (new) scales into marked buffers: the fixture's first
  frame at every even width from 2 to 64, through resizes that keep first
  the width, then the height, and ending exactly where a buffer ends, with
  guard bytes after it; and two embedded frames, 1080x32 and 34x32 (183 and
  124 bytes, the ffmpeg command beside them), at their own size. Before
  this part all 4 tests fail (39 of 51 results); with it all 51 pass.
- The picture's bytes are as before in 173 of 174 probed geometries
  (SHA-256); the other is 578x1280 at its own size, where the old code left
  2560 pixels unwritten. The real binary against the fake 576x1280 device
  colours columns 30..49, 27..52 and 43..76 at 80x24, 80x30 and 120x40
  (before: 30..53, 27..58, 43..82); 100x56 and 80x28 are unchanged.
- All 8 mutants are caught: no copy; a copy 1 pixel too wide, 1 too narrow,
  1 row short, or at the wrong source stride; an unpadded scratch stride
  (20 runs of 20); reallocating on a height change only; on a width change
  only (glibc aborts; under `-asan`, a heap-buffer-overflow read).
- Cost, medians of 10, before and after: the terminal display at 80x24
  106.3 and 104.4 us, at 120x40 128.5 and 126.7 us (noise); a 576x1280
  screenshot 122.2 and 172.5 us; a 1080x2400 browser frame 426.4 and 616.2
  us: at 60 frames a second, about 2.6% and 3.7% of a core, 1.1% more.
- Checks by the builder (on `ebeb3f0`, the branch with parts 37 to 41 as
  they were then; go1.27.1, FFmpeg 9.0.2's libraries, `iso.env`, bwrap):
  gofmt, vet and build clean; `go test ./...` 476 passed, 3 skipped, 0
  failed; `-race` and `-asan` on 44 screenshot, dump, display and scale
  tests 44/0/0, no race or ASan report; the new tests `-race -count=20`
  80/80.
- Verification, snapshot round 1 (`/tmp/opencode/review-42`, on part 41's
  seventh round, the top of the stack), every Go command inside the bwrap
  sandbox with `iso.env`: the base's `decode.go`, `native.c`, `native.h`
  and `stream.go` are the builder's starting copies, the four files its
  final ones, and the code's delta its diff; gofmt, vet, the build and `git
  diff --check`; `go test ./...` (476 top-level tests pass, 3 skip, 0
  fail); the builder's 44 tests under `-race` (no race) and `-asan` (no
  report); the new tests 20 times and 5 times under `-race`. Under the new
  tests the base's `native.c`, `native.h` and `stream.go` fail all 4 (35 of
  47 subtests), and each of the builder's 8 mutants fails at least one (the
  width-only reallocation through glibc's abort). The whole module, in the
  desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two. The checks ran again on the snapshot with
  this bullet.
- Review round 1 (NEEDS CHANGES), on `/tmp/opencode/review-42`. MEDIUM: the
  tests checked the picture's colours only down the middle half of each
  quarter's rows, so a copy from the next row of the scratch frame (the
  review's mutant x3) passed every test: the picture moved up a row, and
  its last row showed leftover bytes (only `-asan` caught it, at 1080x32,
  as a read past the scratch frame). LOW: the comment of
  `TestScaleStopsAtTheEndOfTheBuffer` suggested it showed the old code
  writing past the end; there it failed only through the spill into the
  next row's bar (`TestScaleAtTheFramesOwnSize` shows the overrun past the
  end, and `TestScaleKeepsUpWithTheSize` also fails on the old code through
  the spill, as the old code had no frame of its own to reallocate). NITs:
  `sws_scale`'s result was ignored, though `native.h` promises -1 on error;
  two phrases of this entry (which widths spilled, and the browser frame's
  cost, given as the difference only); and two comments (`stream.go` said
  "directly", and `scale_test.go` gave 16 pixels at a time as a general
  fact). It found no defect in the code, and every figure above reproduced.
  Its suggestion, taken: a colour tolerance of 32 instead of 24, for other
  libswscale versions (CI builds with another FFmpeg).
- Round 2: `checkScaled` checks the colours from the picture's first row to
  its last, but for the rows within an eighth of its height of the middle,
  where the quarters meet (the review's line), with a tolerance of 32;
  `sct_vdec_scale` returns -1 when `sws_scale` fails; the two comments and
  the two phrases are corrected, and the comment of
  `TestScaleStopsAtTheEndOfTheBuffer` says how the old code failed it.
- Verification, round 2, before its snapshot (`/tmp/opencode/dev-42b`:
  `verify2.sh` and `remake3.sh`, the round-2 files laid by `-overlay` over
  round 1's snapshot tree, whose code base is the stack's top now; iso.env,
  bwrap): gofmt, vet and `gcc -Wall -Wextra` on `native.c` clean; the new
  tests pass, also 20 times and 5 times under `-race`; under them the
  base's files fail all 4 (35 subtests). The builder's 8 mutants and the
  review's x1, x2, x3, x4, x6 and x8, each made again on round 2's
  `native.c` (three by hand, as they touch the lines round 2 changed), each
  fail a new test; x3 fails all 4 (38 subtests), and passes round 1's
  tests, the review's gap. The review's x5 (no retry after a failed
  allocation) and x7 (rows aligned to 32 bytes, the same output here)
  pass, as does round 1's `native.c` (no guard: no input makes `sws_scale`
  fail). With the tolerance cut to 16 the tests pass, and at 15 they fail
  (a red of 239 at row 3): the colours are up to 16 levels off here.
- Verification, snapshot round 2 (`/tmp/opencode/review-42b`, on part 41's
  seventh round as rebuilt on part 37's eleventh), every Go command inside
  the bwrap sandbox with `iso.env`: the base's `decode.go`, `native.c`,
  `native.h` and `stream.go` are the builder's starting copies, and the four
  files the ones checked above; gofmt, vet, the build and `git diff
  --check`; `go test ./...` (476 top-level tests pass, 3 skip, 0 fail); the
  builder's 44 tests under `-race` (no race) and `-asan` (no report); the
  new tests 20 times and 5 times under `-race`. The base's files, the 17
  mutants, x3 under round 1's tests and the colour margin give the results
  above. The whole module, in the desktop-like sandbox, connects to none of
  its decoy sockets, while the positive control reaches two. These checks
  ran before this bullet was written; the snapshot was then re-packed with
  it, which changed only the handoff.
- Found, left alone: after a failed `sws_getContext`, a call at the old
  size passes a NULL context to `sws_scale` (out of memory only); the
  browser path's copy could go if its canvas carried a padded stride to the
  JPEG encoder; `native.h` still says RGBA for BGR0; the guard on
  `sws_scale`'s result and the retry after a failed allocation have no
  test, as no input makes either fail.
- Not run: the node suites (`web/` untouched), Android, any real device,
  other CPUs or FFmpeg builds, aarch64, and `-race` or `-asan` on the whole
  module.

## Update (2026-10-21: QoL overhaul, part 41: the MIT License)

- The owner chose the MIT License. `LICENSE` holds the standard MIT text,
  with "Copyright (c) 2026 Gary Bagley": the full name on most commits (the
  first 14 say "Gary", and the merges of pull requests #1 to #5 on GitHub,
  four merge commits and one squash, "pwnedbygary"), and the year of the
  first one (2026-08-29). The README ends with a License section, listed in
  its table of contents: scterm, the Android app included, is released
  under it; the scrcpy server, which scterm and the app both bundle, keeps
  the Apache License 2.0 that the Credits section names
  (`third_party/LICENSE.scrcpy`, unchanged), and other third-party files,
  such as the Gradle wrapper, keep theirs.
- Verification (snapshot `/tmp/opencode/review-41`, on `9598a59`, every Go
  command inside the bwrap sandbox with `iso.env`): `LICENSE`'s permission
  and warranty paragraphs have the same words as the MIT text this system
  ships for brotli (`/usr/share/licenses/brotli/LICENSE`), line breaks
  aside; the docs checker finds 32 relative links in the README and no
  problem, and no README line is over 100 columns; gofmt and vet are clean,
  the docs tests and `go test ./...` pass, and `git diff --check` is clean.
- Review round 1 (NEEDS CHANGES): LOW, the README's table of contents
  listed every section but the new one; NITs, the docs checker counts only
  relative links, and "the name on the commits" said too much. The review
  found `LICENSE` identical to gh's copy of GitHub's MIT template apart
  from the copyright line, and both README sentences accurate. Suggestion
  taken: "the scrcpy server that scterm and the app bundle" (the APK
  carries it too). Left for a later change: a test that the table of
  contents lists every section; the release assets (the binary and the
  APK) carry the scrcpy server without `LICENSE.scrcpy`, which Apache 2.0
  §4(a) asks for, the APK build excludes `/META-INF/{AL2.0,LGPL2.1}`
  (`android/app/build.gradle.kts:83`), and the server's one local patch is
  marked only by an inline comment (§4(b)).
- Round 2: `- [License](#license)` in the README's table of contents, the
  server sentence, and this entry's wording.
- Verification, round 2 (snapshot `/tmp/opencode/review-41b`, on
  `9598a59`, the same way): `LICENSE` is round 1's; the table of contents
  has an entry for each of the README's 13 `##` headings; the docs checker
  finds 33 relative links and no problem, and no README line is over 100
  columns; gofmt and vet are clean, the docs tests and `go test ./...`
  pass, and `git diff --check` is clean.
- Review round 2 (PASS), with no findings. Its suggestions, taken in round
  3: the "pwnedbygary" commits are the merges of 5 pull requests (four
  merge commits and a squash); name the files the APK build excludes; the
  README's server sentence; a word on other third-party files (the Gradle
  wrapper).
- Round 3: the README's License section and this entry, as above. The
  commit counts are gone from the first bullet, as each landing part adds
  to them. The part moves to the top of the stack, onto part 39.
- Verification, snapshot round 3 (`/tmp/opencode/review-41c`, on part 39's
  first snapshot), every Go command inside the bwrap sandbox with
  `iso.env`: `LICENSE` has the same words as the MIT text in
  `/usr/share/licenses/brotli/LICENSE` after the copyright line; the table
  of contents names all 13 `##` headings; the docs checker finds 33
  relative links in the README and no problem, and no README line is over
  100 columns; gofmt, vet, the 13 docs tests and `go test ./...` pass, and
  `git diff --check` is clean. The snapshot was built and checked once
  before this bullet, and again with it, with the same results.
- Review round 3 (PASS), with no findings. Its suggestion, taken: the
  path above in backticks.
- Round 4 changes only this entry, and rebuilds the part on the top of the
  stack as it then stood, part 39's second round
  (`/tmp/opencode/review-39b`, on part 37's fifth round): `LICENSE` as it
  is, and the table-of-contents line and the section added to the README
  there.
- Verification, snapshot round 4 (`/tmp/opencode/review-41d`, on part 39's
  second snapshot), the same checks as round 3's, with the same results:
  the same words as the MIT text, all 13 `##` headings in the table of
  contents, 33 relative links and no problem, no README line over 100
  columns; gofmt, vet, the 13 docs tests and `go test ./...` pass, and
  `git diff --check` is clean. The snapshot was built three times, each
  with these checks and the same results: on part 39's snapshot as first
  packed, again with this bullet, and last on part 39's snapshot re-packed
  with its own snapshot bullet.
- Review round 4 (BLOCKED on part 37): part 37's fifth round, under this
  snapshot, needed changes, so the tree could not land as reviewed, and
  the Round 4 bullet's claim that it is the branch tip once parts 37 to 40
  land was not true (corrected above). It found nothing to change in this
  part: `LICENSE` and the README's lines round 3's bytes, and every check
  reproduced. Its suggestion, taken: the snapshot bullet says how often the
  snapshot was built.
- Round 5 changes only this entry, and rebuilds the part on the top of the
  stack, part 39's third round, on part 37's sixth.
- Review round 5 (PASS), with no findings: `LICENSE` and the README's
  lines round 3's bytes, the base exactly part 39's third round, and every
  check reproduced. Part 37's sixth round then needed changes.
- Round 6 changes only this entry, and rebuilds the part on the top of the
  stack, part 39's fourth round, on part 37's seventh.
- Review round 6 (PASS), with no findings: `LICENSE` and the README byte
  for byte round 5's, the base exactly part 39's fourth round, and every
  check reproduced. Its suggestions, and the last move review's, concern
  the scripts that build the snapshots: `snap-41f.sh` now compares the
  whole README, and `snap-move.sh` now counts an entry's changed lines
  right. Part 37's seventh round then needed changes.
- Round 7 changes only this entry, and rebuilds the part on the top of the
  stack, part 39's sixth round, on part 37's eleventh.
- Not run: GitHub's license detection, which reads the file once it is on
  the branch.

## Update (2026-10-21: QoL overhaul, part 39: screenshot tests, held Alt+S)

- Held Alt+S: a terminal sends a held key as more presses, so holding
  Alt+S saved a file for each shot that ended while the key was down
  (about 20 a second on a simple screen). Now an Alt+S key press within
  200 ms of the Alt+S key press before it, taken or not, is the key's
  repeat and takes no screenshot (`screenshotKey`, `shotKeyRepeat` in
  input.go; `shotKeyAt` in app.go). At the usual repeat rates the first
  repeat comes 250 to 600 ms after the press and the rest 25 to 50 ms
  apart, so a hold saves one file or two: the press's, and the first
  repeat's unless the press's shot is still in flight then. With repeats
  200 ms or more apart, each one saves another, unless a shot is still in
  flight then. 200 ms is four times the longest usual repeat gap and shorter
  than the shortest usual first-repeat delay; two presses less than
  200 ms apart save one. The action bar's Shot and the menu's
  Screenshot row call `screenshot()` and are never repeats. The time is
  a parameter (`screenshotKey(now)`), as in `quitArmed` and
  `barHideCheck`. README.md says what a held Alt+S does.
- A session with no video socket says what the placeholder says: with
  `--video=false`, `scterm: screenshot: the video is off`; otherwise, as
  with a peer that grants no view, `scterm: screenshot: no video from
  DEVICE`, the name with control characters taken out (`deviceName`).
- README.md: a press while a screenshot is "still being taken or saved"
  takes no new one (it said "still being saved").
- Tests that catch their regressions at any CPU count. Before its two
  late presses, `TestShotPressesJoinTheShotInFlight` waits until the
  saving goroutine holds the picture and waits for the UI lock that the
  test holds (`waitSaverLocked`: a bounded poll of `runtime.Stack`, so no
  seam in production code). New `TestShotDeadlineClearedBeforeTheLook`:
  its socket (`clearHeldConn`) holds the wake's clear of the read
  deadline until a second request has set its deadline, so a wake that
  looks before it clears loses that request at any CPU count. `heldConn`
  and its test are unchanged.
- A slow PNG test fails alone: `waitShot`'s budget is `shotTestWait`
  (30 s, was 5 s); past it the test fails and waits as long again, so a
  late notice still lands in the test's own capture; the rig's cleanup
  waits (at most 30 s) for its last shot. A shot that outlives all three
  waits (90 s) can still reach the next test.
- web/input_test.mjs: the page's `performance.now` is the suite's own
  clock, moved by `elapse(ms)`, and the three-finger tests no longer
  sleep, so a loaded machine no longer makes the tap look longer than
  `SOFTKEY_TAP_MS` (700 ms). That cause was checked first: under 8 busy
  loops on its CPU, BASE's suite failed the three tap checks in 3 of 3
  runs, the tap timed at 2455 to 5995 ms. On this host ananicy-cpp runs
  every node process under `SCHED_IDLE` (its rule for `node`, type
  `BG_CPUIO`), so any load on the suite's CPU starves it. Timers stay
  real. player.js is unchanged.
- New tests: `TestScreenshotKeyRepeat` (the test's clock: the press, the
  first repeat 500 ms later, 30 repeats 33 ms apart, a press 199 ms after
  the last repeat and one 200 ms after that) and
  `TestScreenshotRepeatOnlyFromTheKey` (Alt+S through `handleInput`, the
  bar's Shot, the menu's row; rounds 2 to 4 add to it, below), and from
  round 4 `TestScreenshotKeyWithTheMenuOpen` (with the menu open, the first
  Alt+S takes a screenshot and, from round 5, leaves the menu open; the next
  Alt+S is its repeat). `TestShotWithTheVideoOff` is a table: video off, no
  video in the session, a name with control characters, and from round 2 a
  device with no name.
- Verification (builder's tree `b14cb05`: `9598a59` plus parts 33 to 36;
  go1.27.1, `iso.env`, bwrap with no network): gofmt, vet, build and
  `git diff --check` clean; the docs checker finds 33 links and no
  problem. `go test -count=1 ./...`: 447 pass, 3 skip, 0 fail (root
  419/1, internal/release 5, peer 23/2; BASE 444/3). The 32 touched
  tests: `-race -count=3` 96/96, `-count=20` 640/640. The 5 new and
  changed tests: `-count=20` 100/100, `-race -count=20` 100/100,
  `-cpu=1,2,4 -count=5` 75/75. Node suites, BASE and this part: input
  130, worklet 41, pointer 17, conn 61, page 285, audio 183, all pass;
  `TestWebClientSuites` passes.
- At each of `-cpu=1`, 2 and 4, 20 runs: MW2 (the flight ends when the
  saver receives the answer) fails the coalescing test 20 of 20 (BASE's
  test: 0, 19, 9); MA (the wake looks before it clears) fails the new
  wake test 20 of 20 (BASE's wake tests: 0, 20, 20). Unmutated, both
  pass 20 of 20 at each count.
- Starved `-race` runs of the screenshot tests (the binary and 4 or 8
  busy loops on one CPU): BASE fails the five PNG tests at 5 s, and with
  4 loops prints 17 race reports while later tests run; this part
  passes 21 of 21 at both loads with none; with only its budget cut to
  6 s, the slow tests (3 with 4 loops, 5 with 8) fail once each, in
  their own test, and no race report comes.
- BASE's input suite and this part's, started at once on one CPU: with
  8 busy loops there (one round), BASE's failed the three tap checks
  (2164 s) and this part's passed 130 of 130 (2313 s); with one loop,
  BASE's failed in 2 of 2 rounds (three other checks of the same test)
  and this part's passed both times. Alone with one loop, BASE's passed
  once: the failures depend on timing.
- Fail before: on BASE's input.go and app.go (and a shim), the two key
  tests and two of the three video-off subtests fail. Mutants, by
  overlay: 23 of 25 Go mutants fail a test (MK by hanging until the 2
  min timeout), among them seven of the repeat check and two of the
  notice. Not caught: MB as built here (the deadline set under `shotMu`,
  before the append) is equivalent, since the wake's look takes `shotMu`
  (the review's MB, set before the lock, is caught); MC (the flight ends
  after the write, before the notice) survives: the notice still came
  before the tests read it. Both JS mutants (a real clock again; the
  tap's length ignored) fail.
- Corrections to part 35's entry: round 2 did not make both docs say
  "being taken or saved": README.md said "still being saved", and only
  docs/ARCHITECTURE.md said "taken or saved"; README.md says it from
  part 39. A flight does not end only when "its file is written or it is
  dropped": it ends when the shot's goroutine does, which is also after
  an error notice (the shot's error, no frame at the end of the wait, a
  failed write), the channel closing after the notice. And the test's
  "while it waits to be saved" held only once the saver had run; from
  part 39 the test makes sure of it.
- Intake rehearsal: on a copy of the worktree as at 12:18 (`684f313`
  plus 53 uncommitted paths) the seven files merge three ways with no
  conflict (`shotKeyAt` sits below `kbAutoGrabbed`, clear of the
  worktree's new `streamFPS`); there gofmt and vet are clean,
  `go test ./...` gives 471 pass, 3 skip, 0 fail, `-race -count=3` on the
  touched tests and `TestStderrCapturesAreOrderedWithNotices` 99/99, and
  the node suites pass.
- Intake: merged three ways into the worktree over parts 34 to 38, 40
  and 41, on part 33 as committed: README.md, app.go, input.go and
  screenshot_test.go merged cleanly, each changed by exactly the
  builder's lines; the other three files are the builder's.
- Verification, snapshot round 1 (`/tmp/opencode/review-39`, stacked on
  part 40's second round), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (471 top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 33
  related tests; the 3 new and 2 changed tests 20 times and 5 times under
  `-race`. On the base's `input.go` and `app.go`, with the builder's shim,
  the two key tests and two of the three video-off subtests fail. At one
  CPU, 20 runs each, MW2 fails the coalescing test 20 of 20 and MA the new
  wake test 20 of 20. The docs checker finds 33 relative links in the
  README and docs/ARCHITECTURE.md and no problem; the whole module, in the
  desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two.
- Review round 1 (PASS), with one LOW and three NITs. LOW: no test showed
  that the action bar and the menu leave the key's window alone. With
  `screenshot()` also recording the time (the review's mutant RB), all 28
  screenshot and mnemonic tests passed: `TestScreenshotRepeatOnlyFromTheKey`
  pressed Alt+S before the bar and the menu, and its hour-long window made
  the last Alt+S a repeat either way. NITs: the README said a held Alt+S
  saves one screenshot or two, and this entry gave the repeat times as
  fact, though with repeats 200 ms or more apart each repeat after a saved
  shot takes another; with `knownDeviceName()` in place of `deviceName()`
  in the video-off notice (mutant RN), an unnamed device's notice would
  end at "from", and no test had such a device; and the New tests bullet
  said "presses 199 and 200 ms after the last", though the second comes
  200 ms after the first, 399 ms after the last repeat. The first bullet
  and the New tests bullet are corrected above.
- Round 2: `TestScreenshotRepeatOnlyFromTheKey` clicks the bar's Shot and
  chooses the menu's row first, and the Alt+S after them must take a
  screenshot; then a second Alt+S takes none, the bar and the menu take
  one each again, and the next Alt+S is still a repeat.
  `TestShotWithTheVideoOff` gains a device with no name ("no video from
  the device"). The README's held-Alt+S sentences say "with the usual
  repeat rates" and what a slower repeat does, and the rest of that
  paragraph is rewrapped; `shotKeyRepeat`'s comment says the same. The
  part moves onto part 40's fourth round, on part 37's fifth.
- Verification, round 2, in the worktree (parts 37 to 41, uncommitted),
  each Go command inside the bwrap sandbox with `iso.env`: gofmt and vet;
  the two changed tests and `TestScreenshotKeyRepeat` 20 times and 5 times
  under `-race`; the 28 screenshot and mnemonic tests pass; the whole
  module, 471 pass and 3 skip. The review's mutants, rebuilt from the
  worktree's input.go: RB fails `TestScreenshotRepeatOnlyFromTheKey`, RN
  the unnamed-device case of `TestShotWithTheVideoOff`, and RT (the repeat
  check on the real clock) `TestScreenshotKeyRepeat`; with round 1's test
  files, RB and RN pass, the gaps the review found.
- Verification, snapshot round 2 (`/tmp/opencode/review-39b`, on part 40's
  fourth round), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (471
  top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 33 related
  tests; the 3 new and 2 changed tests 20 times and 5 times under `-race`.
  On the base's `input.go` and `app.go`, with the builder's shim, the two
  key tests and three of the four video-off subtests fail. At one CPU, 20
  runs each, MW2 fails the coalescing test 20 of 20 and MA the new wake
  test 20 of 20. RB, RN and RT each fail their test; with round 1's test
  files, RB and RN pass. The docs checker finds 33 relative links in the
  README and docs/ARCHITECTURE.md and no problem; the whole module, in the
  desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two. These checks ran before this bullet was
  written; the snapshot was then rebuilt with it, the same files.
- Review round 2 (PASS), with one LOW and three NITs. LOW: the menu's
  Screenshot row was checked through its letter, never through a click
  (`mouseEvent`, `menuHitTest`, `runMenuRow`): with the click recording
  the key's time (V3) or going through `screenshotKey` (V4), every test
  passed. NITs: two variants no test caught (V5, a bar or menu press that
  moves a window still open, which the hour-long window cannot see; V14,
  Alt+S with the menu open run as the menu's row, as no test pressed Alt+S
  with the menu open); the README's "With a slower repeat" overstated,
  as a repeat under 0.2 s apart saves nothing more, and this entry's and
  the comment's "each one that comes after a saved shot" was looser than
  the code, since a repeat that comes while a shot is in flight joins it;
  and the New tests bullet still listed three video-off rows. The README,
  the first bullet and the New tests bullet are corrected above. It found
  round 1's findings resolved, the code correct, and every figure
  reproduced. Its suggestions are in
  /tmp/opencode/review-39b/round2-findings.md; taken: the click, Alt+S
  with the menu open, the README's words.
- Round 3: `TestScreenshotRepeatOnlyFromTheKey` also clicks the menu's
  Screenshot row in each pass over the bar and the menu, and ends with
  Alt+S pressed with the menu open, which must still be a repeat. The
  README says "With repeats 0.2 seconds or more apart, each repeat takes
  another, unless one is still being taken or saved"; `shotKeyRepeat`'s
  comment and the first bullet say the same. The part moves onto part
  40's fifth round, on part 38's seventh and part 37's sixth.
- Verification, round 3, in the worktree (parts 37 to 41, uncommitted),
  each Go command inside the bwrap sandbox with `iso.env`: gofmt and vet;
  the changed tests 20 times and 5 times under `-race`; the 54
  screenshot, mnemonic, menu and bar tests pass; the whole module, 471
  pass and 3 skip. Mutants laid on the worktree's files: RB, RN and RT
  fail their tests as before; V3, V4 and V14 fail
  `TestScreenshotRepeatOnlyFromTheKey`, and pass round 2's test file, the
  gap the review found; V5 passes, as no test here can see it.
- Review round 3 (PASS), with two NITs. The Alt+S with the menu open at the
  end of `TestScreenshotRepeatOnlyFromTheKey` wants no screenshot, as a
  repeat, which a menu that dropped the chord would give too: with the menu
  open, Alt+S dropped (X1), closing the menu and taking nothing (X2), or
  checked but not recorded (X3) passed every test, and X3 would let a held
  Alt+S save a file for each shot that ends while the key is held, with the
  menu open; the test did not check that the menu was open either. And the
  Review round 2 bullet said the hour-long window cannot see V14, which
  fails round 3's test with that window (corrected above). It found round
  2's LOW resolved, the README, the comment and this entry true to the code,
  and every figure reproduced. Its suggestions, taken: its probe, and part
  38's round in the Round 3 bullet's move.
- Round 4: new `TestScreenshotKeyWithTheMenuOpen`, the review's probe: on
  a fresh app with the menu open, the first Alt+S takes a screenshot and
  the next one is its repeat. `TestScreenshotRepeatOnlyFromTheKey` checks
  that the menu is open before its last Alt+S. The part moves onto part
  40's sixth round, on part 38's eighth and part 37's seventh.
- Verification, round 4, in the worktree (parts 37 to 41, uncommitted),
  each Go command inside the bwrap sandbox with `iso.env`: gofmt and vet;
  the changed tests 20 times and 5 times under `-race`; the 55
  screenshot, mnemonic, menu and bar tests pass; the whole module, 472
  pass and 3 skip. Mutants laid on the worktree's files: RB, RN, RT, V3
  and V4 fail their tests as before, and the review's X4 and X5 fail
  `TestScreenshotRepeatOnlyFromTheKey`; X1, X2 and X3 fail the new test,
  and pass round 3's test file, the gap the review found; V14 fails both
  tests; V5 passes, as no test here can see it.
- Review round 4 (PASS), with two NITs: the new test's comment said the
  menu neither drops the chord nor closes on it, but the test did not
  check that the menu stays open (Y1, a menu that closes on Alt+S and then
  takes the shot, passed every test); and the New tests bullet did not
  name the new test. It found round 3's NITs resolved and every figure
  reproduced. Its suggestions, taken: the check script's label (three key
  tests fail on the base files, not two), the Review round 3 bullet's
  words on X3, and no `delta-vs-old.txt` in the next snapshot, as the
  snapshot script wrote that file before laying on the round's test.
- Round 5 adds to `TestScreenshotKeyWithTheMenuOpen` the check that the
  menu is still open after the first Alt+S, and corrects the New tests and
  Review round 3 bullets above. The part moves onto part 40's seventh
  round, on part 38's ninth and part 37's ninth.
- Verification, round 5, in the worktree (parts 37 to 41, uncommitted),
  each Go command inside the bwrap sandbox with `iso.env`: gofmt and vet;
  the changed tests 20 times and 5 times under `-race`; the 55
  screenshot, mnemonic, menu and bar tests pass; the whole module, 472
  pass and 3 skip. Mutants laid on the worktree's files give round 4's
  results; the review's Y1 fails the new test and passes round 4's test
  file, the gap the review found, and its Y2 (Alt+S with the menu open
  recorded and shot without the repeat check) fails
  `TestScreenshotRepeatOnlyFromTheKey` and the new test.
- Review round 5 (PASS), with one NIT: the bullet above said Y2 fails both
  key tests, where this entry's two key tests are `TestScreenshotKeyRepeat`
  and `TestScreenshotRepeatOnlyFromTheKey`; Y2 passes the first (corrected
  above). It found round 4's NITs resolved (with Y1 the new check is the
  package's one failure, and under round 4's test file Y1 passes) and every
  figure it re-ran reproduced. Its suggestions, taken: "round 4's results"
  for the mutants, as V5 passes; the check script's labels for V14 and Y2,
  which said "both key tests" too, name the two tests.
- Round 6 corrects the Verification, round 5 bullet, and the part moves
  onto part 40's seventh round, on part 38's ninth and part 37's eleventh. The
  part's files are round 5's.
- Left alone: menu.go, stream.go, player.js, docs/ARCHITECTURE.md (held
  Alt+S is in the README only); what a screenshot saves, its name, one
  shot at a time and the wake. The repeat window uses the time the run
  loop handles a press, so a stall of the loop of more than 200 ms during
  a hold can take one shot more (the review suggests stamping input when
  it is read). A bar or menu press inside a window still open does not
  move it, and no test shows that (the round-2 review's V5): it needs a
  short real-clock window or a time given to `screenshot()`.
- Not run: a real terminal's key repeat (the times are typical defaults),
  the real phone and adb, a browser, and a peer that grants no view (the
  test's session has no video socket).

## Update (2026-10-21: QoL overhaul, part 40: fixes from the first real-device run)

- doctor runs commands only on the device scterm would use: the one
  `pickTarget` gives for `ANDROID_SERIAL`, or else the only ready one.
  Another ready device is listed from `adb devices -l` alone: `ok device
  SERIAL (MODEL) is ready; scterm would not use it, so doctor runs nothing
  on it`. With several ready and none chosen, or an `ANDROID_SERIAL` that
  names none or several, only `adb version` and `adb devices -l` run. On
  the first device run it ran `getprop` on two devices another program
  used (the guard refused both) and printed two warnings for them.
- `mirrorable`: a ready device doctor does not read may be one scterm can
  mirror. With two or more ready and none chosen, a device that is not
  ready beside them is a warning, and two ready devices too old for the
  server no longer fail (doctor cannot know without reading them); the
  "choose one" warning stays. The used device's rules are as before: not
  ready, too old, an unknown or ambiguous `ANDROID_SERIAL`, and the pairing
  cases of parts 30 and 37.
- doctor's advice after a failed read names the read that failed,
  `getprop ro.build.version.release` or `...sdk`; it always said `sdk`.
  `doctor -h` and the README's Troubleshooting say which device is read.
- `--no-tui`'s line (`app.statistics`) leaves out `pcm`, `peak` and `avlag`
  without sound playing: `audio=off` (part 36's rules) or no audio packet
  yet (`lastAudioPts` 0; the server stamps packets with the device's
  monotonic clock, except the first packet of Android's default Opus and
  FLAC encoders, which keeps the encoder's own stamp:
  `AudioEncoder.fixTimestamp`). `avlag` also needs a video packet. The
  run printed `avlag=214994453ms` with no sound, the video's own
  timestamp; now `[576x1280 fps=10 audio=off]`. The line is padded to the
  longest one printed, as it now shrinks when the sound stops (a failed
  sink write). `statisticsEvery` (2 s) is a seam for the test.
- The terminal's fps: no frame is lost at the rates the probe tried (the
  run loop's one-slot mailbox drops an older frame by design when the
  terminal falls behind, which the probe's pipe, read at once, cannot
  show). The row showed the mean of the run loop's last 32 frame times,
  which counts the pauses of a still screen (the server repeats a frame
  after 100 ms, `SurfaceEncoder.java` line 32; AOSP at most 10 times, per
  part 35); `stream.currentFPS` (which `--no-tui` prints and the browser
  display's status messages carry, though the page shows the rate it
  paints at) changes only as frames come, over windows of 0.5 s or more
  that end at a frame, so it dips after a pause too. The row now shows the
  stream's rate as each frame carries it (`videoFrame.fps`, kept in
  `app.streamFPS` by the run loop: race-free, no stream.go change), with
  1000/rate as the ms, and no rate in the stream's first 0.5 s. The
  sparkline is unchanged. README and docs/ARCHITECTURE.md say so.
- Evidence (fakes): a probe, not committed (real decoder, mailbox and run
  loop on a pipe, 120x40), on BASE: steady 10 fps, 40 sent, 40 drawn, both
  figures 10.0; a still screen (a frame and 10 repeats 100 ms apart every
  2 s, then 3 s of nothing), 33 sent, 33 drawn, row `156.2ms  6.4fps`,
  `currentFPS` 10.0 at every 2 s sample and 3 s after the last frame; pairs
  written at once every 200 ms, 40 of 40 drawn. On the device a
  frames-only test client got 23 frames in 6.0 s while the status said
  9.56, and the row's sparkline had no interval under 57 ms.
- Found, left alone: `takePooled` and `returnPooled` race on `canvasPool`
  (video goroutine against the run loop; 2 reports from the probe under
  `-race`, on BASE too; no frame lost): a mutex would fix it in stream.go.
  All three displays hold their last rate while no frame comes (the
  browser's HUD says idle after 2 s). web.go never sets the status's
  `dropped`. `currentFPS` and `audioBytes` are read off their goroutine.
  When the audio stream ends while the video goes on, nothing marks the
  sound as stopped (`playAudio` returns the error): the line keeps
  `audio=100%`, with `pcm` and `peak` that no longer move and an `avlag`
  that grows with the video (the review saw 120, 2120 and 4120 ms);
  recording the end would make it say `audio=off`.
- Tests: `doctor_calls_test.go` (a fake adb notes every call; exact call
  lists for 6 cases), `tui_fps_test.go`, `TestStatisticsLinePrinted`;
  doctor expectations updated, with a mirrorable case; the getprop failure
  test fails each read in turn; `statusApp` gives a stream rate;
  `TestStatisticsLineSaysOffWithoutSound` pins the whole line.
- Verification (builder's tree, base `62ba53b`, iso.env, bwrap, go1.27.1):
  gofmt, vet, build, `git diff --check` clean. `go test -count=1 -v ./...`:
  465 pass, 3 skip, 0 fail (base 462, 3, 0). `-race ./...`: 465, 3, 0, no
  race. 42 touched tests `-race -count=3`: 126 of 126. New tests x20: 60
  of 60; under `-race` x5: 15 of 15. Changed tests x20: 140 of 140. On
  BASE's files (a shim gives BASE's line under the new names) all 8 new
  or changed tests fail. Mutants: 14 of 14 caught.
- Not run: a device, adb, a real terminal, a browser, CI, and the node
  suites (web/ untouched). The AOSP limit of 10 repeats is part 35's
  reference, not re-read (no network). The canvas-pool race is not fixed.
- Intake: merged three ways into the worktree (the builder's base
  `62ba53b` was the worktree of that time). `README.md` merged cleanly
  with the worktree's later lines (part 41's table-of-contents entry and
  License section); the other eight files are the builder's.
- Verification, snapshot round 1 (`/tmp/opencode/review-40`, on part 38's
  third round), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (466
  top-level tests pass and 3 skip); `-race -count=3` on 46 related tests;
  the 8 new or changed tests (3 new, 5 changed), with `TestDoctorWithoutADB`
  and `TestDoctorOnAHealthyAndAnUnhealthyComputer`, unchanged, next to
  them, 20 times and 5 times under `-race`. On the base's `app.go`,
  `doctor.go` and `tui_status_test.go`, with the builder's shim, all 8 new
  or changed tests fail, and the two unchanged ones pass. The docs checker
  finds 33 relative links in the README and docs/ARCHITECTURE.md and no
  problem; the whole module, in the desktop-like sandbox, connects to none
  of its decoy sockets, while the positive control reaches two. These
  checks ran before part 38's snapshot gained its last handoff bullet; the
  snapshot was then rebuilt on the final one, with the same files.
- Review round 1 (NEEDS CHANGES, wording only): the snapshot bullet called
  10 tests new and changed, where 3 are new, 5 changed and 2 unchanged
  neighbours that the diff's hunks name; docs/ARCHITECTURE.md and the
  comments on `app.streamFPS` and `TestStatusShowsTheStreamsFrameRate` said
  the browser's status gives the same rate, but the page shows its own
  paint rate (`view.fps`) and uses neither the status's `fps` nor the
  frame header's; and the lost-audio case above was missing. Nits: "no
  frame is lost" needed its scope; the stream's windows last half a second
  or more and end at a frame; the 23 frames went to a frames-only test
  client; the first packet of Android's default Opus and FLAC encoders
  keeps the encoder's stamp. It found the code right: doctor runs nothing
  on a device scterm would not use, on any path; the padding neither leaves
  old text nor grows without end; the rate travels with the frame and only
  the run loop reads it. Left for later, from its suggestions: a call-list
  case for an `ANDROID_SERIAL` that names a device that is not ready;
  doctor's "choose one with -s SERIAL", though `scterm doctor` takes no
  `-s` (`ANDROID_SERIAL` would let doctor read the device); the README
  Troubleshooting paragraph's ragged lines; recording a lost audio stream
  in `app.go`.
- Round 2 corrects the sentences above, docs/ARCHITECTURE.md and the two
  comments, and moves the part onto part 38's fourth round.
- Verification, snapshot round 2 (`/tmp/opencode/review-40b`, on part 38's
  fourth round), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (468
  top-level tests pass and 3 skip); `-race -count=3` on 46 related tests;
  the 3 new and 5 changed tests, with the two unchanged neighbours, 20
  times and 5 times under `-race`. On the base's `app.go`, `doctor.go` and
  `tui_status_test.go`, with the builder's shim, the 8 new or changed tests
  fail and the two unchanged ones pass. The docs checker finds 33 relative
  links in the README and docs/ARCHITECTURE.md and no problem; the whole
  module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two. These checks, too, ran
  before part 38's snapshot gained its last handoff bullet; the snapshot
  was then rebuilt on the final one, with the same files.
- Review round 2 (PASS), with two nits, both corrected above: the round-2
  snapshot bullet left out that rebuild, and the Review round 1 bullet
  said the page reads neither rate, though it reads the frame header's
  (it uses neither). It found round 1's findings resolved, `app.go` and
  `tui_fps_test.go` changed only in comments (their tokens equal round
  1's), and every figure of the round-2 snapshot bullet reproduced, with
  the lost-audio figures. Left for later, from its suggestions: the
  comment on `statistics` in `app.go` still says the server stamps packets
  with the device's monotonic clock, without the exception above.
- Round 3 changes only this entry, and moves the part unchanged onto part
  38's fifth round (on part 37's fourth), which changes none of its files
  but the handoff.
- Verification, snapshot round 3 (`/tmp/opencode/review-40c`, on part 38's
  fifth round), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (468
  top-level tests pass and 3 skip); `-race -count=3` on 46 related tests;
  the 3 new and 5 changed tests, with the two unchanged neighbours, 20
  times and 5 times under `-race`; the docs checker finds 33 relative
  links in the README and docs/ARCHITECTURE.md and no problem; the whole
  module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two. The runs against older
  code were not repeated, as the part's files are round 2's bytes. Every
  file of the part but the handoff, `README.md` and `app.go` (which part
  39 also changes, and part 41 the README) equals the worktree's. These
  checks ran before this bullet was written and before part 38's snapshot
  gained its last handoff bullet; the snapshot was then rebuilt on the
  final one, with this bullet, the same files.
- Review round 3 (PASS), with two nits, both corrected above: the round-3
  snapshot bullet left out the rebuild of part 38's snapshot under it (the
  checks name its earlier base), and the Review round 2 bullet said every
  figure reproduced, where round 2 reproduced the round-2 snapshot
  bullet's figures and the lost-audio ones, not the device's or the
  probe's. It found round 2's nits resolved, only the handoff changed
  since round 2, and every figure of the round-3 snapshot bullet
  reproduced.
- Round 4 changes only this entry, and moves the part unchanged onto part
  38's sixth round, on part 37's fifth round, which changes none of its
  files but the handoff.
- Verification, snapshot round 4 (`/tmp/opencode/review-40d`, on part 38's
  sixth round): the same checks as round 3's, with the same results: 468
  top-level tests pass and 3 skip, the related, new and changed tests and
  the docs checks pass, and the whole module connects to none of its decoy
  sockets, while the positive control reaches two.
- Review round 4 (BLOCKED on part 37): part 37's fifth round, under this
  snapshot, needed changes, so the tree could not land as reviewed, and
  the Round 4 bullet's "on part 37 as committed" was not true (corrected
  above). It found nothing to change in this part: both of round 3's nits
  fixed as asked, the files and the delta right, and every check
  reproduced. Its suggestion, taken: a move names part 37's round until
  part 37 has landed.
- Round 5 changes only this entry, and moves the part unchanged onto part
  38's seventh round, on part 37's sixth, which changes none of its files
  but the handoff.
- Review round 5 (PASS), with no findings: the files, the delta and this
  entry right, the base exactly part 38's seventh round, and every check
  reproduced. Part 37's sixth round then needed changes.
- Round 6 changes only this entry, and moves the part unchanged onto part
  38's eighth round, on part 37's seventh, which changes none of its files
  but the handoff.
- Review round 6 (PASS), with no findings: the files, the delta and this
  entry right, the base exactly part 38's eighth round, and every check
  reproduced. Part 37's seventh round then needed changes.
- Round 7 changes only this entry, and moves the part unchanged onto part
  38's ninth round, on part 37's eleventh, which changes none of its files
  but the handoff.

## Update (2026-10-21: QoL overhaul, part 38: web loose ends and test isolation)

- `--window`: no browser opens once a stop has begun, and none outlives the
  stop. `openWindow` returns at once when a stop has begun, and starts the
  browser through `startWindow`, which holds `winMu` from its check of
  `closing` until the window is recorded. The stop sets `closing` first and
  looks for the window under `winMu` (`killWindow`), so it finds the browser
  and closes it, or the browser never starts. Before, a stop between `run()`
  publishing its server and the launch left the browser running after
  scterm, holding the private profile's lock. Refused, `openWindow` prints
  no window line and removes the browser's log (when the stop begins after
  its first check, the browser note is already out). `run()`'s comment says
  what holds: after a stop it serves nothing and prints no address, but it
  still warns of `SCTERM_WEB_HOSTS` entries it ignores (round 2 corrected
  the comment, which said it printed nothing). `windowStart` is the seam
  (`cmd.Start`); `setWindow` stays, for tests that register a stand-in
  window.
- A viewer refused with the stop's bye now counts as connected
  (`hadClient`): its page showed why the session ended, so the browser's
  log goes as chatter instead of staying to explain a failure. Two reads
  of `hadClient` remove the log: the stop's, just after it takes the
  window it closes (`killWindow`), and the window's watcher's, once the
  browser has exited (`watchWindow`, if scterm still runs). A refusal
  recorded before either read removes the log; one recorded after both
  changes nothing: the log goes or stays as before (the watcher removes it
  anyway, for example, for a window that lived 15 s, `windowGrace`, or a
  browser that handed the mirror over to a running one). Nothing orders
  the refusal and those reads: the refusal's critical section always
  follows the stop's, the stop never waits for the handler afterwards, and
  the watcher waits only for the browser. A window whose
  watcher cleared it before the stop looked, and reported it as a
  failure, keeps its log, which explains a real failure. A window the stop
  took first can lose a log its watcher has just reported, when a refusal
  comes between the report and the stop's read. This is reasoned from the
  code; the test forces the first order by holding `winMu`, and the
  reviews showed the watcher's read with a browser that ignores SIGTERM,
  the reported window with one that fails at once, and the last order
  with a probe that holds the stop between its two steps.
- Left as harmless: `quit()` waiting for room in a full event queue can end
  the session after a viewer came back. The queue fills only while a drag
  waits on a control socket the device stopped reading; the quit came after
  a whole grace with no viewer; `quit()` gives up when the server stops.
- Test isolation. `SCTERM_WEB_HOSTS` is emptied in the four host tests
  (`TestWebRefusesARebindingName`, `TestWebRefusalSuggestsTheAddressReached`,
  `TestWebReportsANameOnceInAnyForm` and `TestWebRefusesEveryMethod`), in
  `TestWebLoopbackRefusesThisComputersName` (this computer's name
  in the list broke it) and in the two stop-before-run tests (an ignored
  entry's notice fell into their capture). The run that
  `TestWebRunStartsTheFollowerOnlyWhenItShould` starts reads it, but the
  test's result does not depend on it. `isolatePulse` in
  `TestWebExitsTwoWhenTheDeviceIsLost` and `TestWebStreamEndSaysBye`;
  `webLinesUntil` isolates its run (`webLinesHere` does not, for the test
  that brings its own `pactl`). `setStderr` swaps `os.Stderr` under
  `notices.mu`, for all five capture helpers, so a leftover web viewer's
  notice no longer races a capture. The follower test has a positive
  control: with a working sink, `pactl` must run within its 20 ticks.
  `SCTERM_TEST_NO_PRESENCE` (in docs/DEVELOPMENT.md), set by `mainCommand`
  and `TestCommandLineExitStatus`, makes main in a child note its presence
  searches in a file instead of searching the network.
- docs/DEVELOPMENT.md says which tests catch a race only under `-race` and
  how to run them. CI is unchanged. Here: `go test ./...` 30.4 s, `go test
  -race ./...` 69.6 s, `go test -race -run TestWeb .` 21.0 s.
- Browser: `player.css`'s header says that in touch mode only ⋯ and the
  wheel bring the chrome up; `player.js` says "the bar's controls". The page
  suite fades the chrome before the Tab, so the Tab must bring it back.
- Tests, new: `window_stop_test.go` (3 tests, a stand-in browser),
  `TestWebStopBeforeRunFreesItsPort`, `TestWebRefusedViewerCountsAsAViewer`,
  `TestStderrCapturesAreOrderedWithNotices` (needs `-race`) and
  `TestChildMainSearchesNoNetwork`.
- Intake: merged three ways into the worktree over parts 33 to 37 (the
  builder's base was `40cb1a0`); `exit_test.go` and `tui_notice_test.go`,
  which part 37 also changes, merged cleanly, and the other 16 files are
  the builder's.
- Verification (builder's tree, base `40cb1a0`, iso.env, bwrap, go1.27.1,
  node v26.11.1): gofmt, vet, build, `git diff --check` clean. `go test
  -count=1 -v ./...`: 450 pass, 3 skip, 0 fail (base 443, 3, 0). Node: page
  286 checks (base 285), the other suites as before. New tests x20: 140 of
  140; under `-race` x5: 35 of 35; 31 touched tests `-race` x3: 93 of 93.
  `-race -shuffle` on the root package: base seeds 1, 2, 3 and 4 each fail
  with the capture race; this part's seeds 1 to 8 pass with no race. On the
  base code the new tests fail: window 3 of 3 (base plus the seam only),
  hadClient 1 of 1, capture 5 of 5 subtests (`-race`), presence 1 of 1; the
  port test passes there (it pins base's code) and fails without `run()`'s
  `ln.Close()`. Go mutants: 17 of 18 caught; MD2, which drops
  `webLinesUntil`'s `isolatePulse`, survives, because today's callers
  isolate themselves. The two page mutants are caught: MF1, and MF2, which
  is a guess at part 32's V2 (keys no longer bring the chrome back after
  the end).
  `SCTERM_WEB_HOSTS` set to `evil.example`, `http://evil.example` or
  `cachyos`: base fails 4, 2 and 1 tests, this part none. A runWeb that
  finds `pactl` itself and never stops its follower ran the session's
  `pactl` 52 times with the base tests, 0 with this part's.
- Review round 1 (NEEDS CHANGES, wording only): `run()`'s comment said it
  returned without printing after a stop, but `allowedNames` warns of
  ignored `SCTERM_WEB_HOSTS` entries before that check (the stop-before-run
  tests pass because they empty the variable); the mutant count left out
  MD2; the `hadClient` sentence needed the order it depends on; and two
  smaller wordings. It found the code sound: no browser can start once
  `killWindow` has looked, `setStderr` cannot deadlock, no non-test file
  reads an `SCTERM_TEST_` variable, and the isolation holds; every
  fail-before and mutant claim reproduced. Left for later, from its
  suggestions: a stop still reports a window it closed as a browser that
  failed (`watchWindow` could stay quiet once `closing` is set);
  `setWindow` is used only by tests; the presence swap lives in one child
  entry; the capture test counts a race once per process.
- Round 2 changes only `run()`'s comment in `web.go` and corrects the
  sentences above.
- Verification, snapshot round 2 (`/tmp/opencode/review-38c`, on round 1's
  base), every Go command inside the bwrap sandbox with `iso.env`: gofmt,
  vet, the build and `git diff --check`; `go test ./...` (462 top-level
  tests pass and 3 skip); `-race -count=3` on 112 related tests; the 7 new
  tests 20 times and 5 times under `-race`; the docs checker finds 10 links
  in docs/DEVELOPMENT.md and no problem, and the docs tests pass; the whole
  module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two.
- Review round 2 (NEEDS CHANGES, wording only): the Not run list named a
  desktop-like sandbox with decoy sockets, which the snapshot checks used
  (the item came from the builder's text, written before them); the
  `hadClient` sentence left out the window's watcher, which reads
  `hadClient` again once the browser has exited (`watchWindow`): with a
  browser that ignores SIGTERM, a refusal stored after `killWindow` looked
  let the watcher remove the log (the review's overlay test); `run()`'s
  comment had one line of 92 columns; the mutant count named only the Go
  mutants. It found round 1's findings resolved, the new comment exact,
  and every figure of round 2's bullets reproduced; `go test ./...` passed
  with `SCTERM_WEB_HOSTS=http://evil.example` too. Suggestions taken: the
  four host tests named; `git diff --check` and the hashes run again after
  the snapshot is re-packed.
- Round 3 changes this entry and the wrapping of `run()`'s comment, and
  moves the part onto part 37's second round, which also changes
  `exit_test.go`: that file is merged three ways.
- Verification, snapshot round 3 (`/tmp/opencode/review-38d`, on part 37's
  second round), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (463
  top-level tests pass and 3 skip); `-race -count=3` on 112 related tests;
  the 7 new tests 20 times and 5 times under `-race`; the docs checker
  finds 10 relative links in docs/DEVELOPMENT.md and no problem, and the
  docs tests pass; the whole module, in the desktop-like sandbox, connects
  to none of its decoy sockets, while the positive control reaches two.
  Every file of the part but the handoff equals the worktree's, and this
  entry is the worktree's.
- Review round 3 (PASS), with two nits, both corrected above: the
  `hadClient` sentence held only while the window was still open when the
  stop looked for it (a window that exited by itself and was reported keeps
  its log, and in the watcher's case the watcher removes the log, not the
  stop), and the snapshot bullet said every file of the part equals the
  worktree's, which the handoff does not. It found round 2's findings
  resolved, the merge of `exit_test.go` exact, and every figure reproduced.
  Left for later, from its suggestions: `snap-38d.sh` could check that
  `web.go` is the same in both bases, and name the handoff as the
  exception in its message.
- Round 4 changes only this entry, and moves the part unchanged onto part
  37's third round, which changes none of its files but the handoff.
- Verification, snapshot round 4 (`/tmp/opencode/review-38e`, on part 37's
  third round), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (465
  top-level tests pass and 3 skip, two more than round 3's, from parts 33
  and 37); `-race -count=3` on 112 related tests; the 7 new tests 20 times
  and 5 times under `-race`; the docs checker finds 10 relative links in
  docs/DEVELOPMENT.md and no problem, and the docs tests pass; the whole
  module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two. The runs against older
  code were not repeated, as the part's files are round 3's bytes. Every
  file of the part but the handoff and `screenshot_test.go` (which part 39
  also changes) equals the worktree's, and this entry is the worktree's.
- Review round 4 (PASS), with two nits, both corrected above: the
  `hadClient` sentence's last claims failed in one order (the stop takes
  the window, the browser fails by itself and its watcher reports it, a
  viewer is refused, and the stop then reads `hadClient` and removes the
  log just reported; the review's probe held the stop between taking the
  window and reading `hadClient`), and the round-4 bullet said part 37's
  third round changes none of this part's files, though it changes the
  handoff. It found round 3's nits resolved, every file but the handoff
  round 3's bytes, and every figure of the round-4 bullet reproduced.
  Suggestions taken: why nothing orders the refusal and the reads, and
  the other half of round 3's suggestion above.
- Round 5 changes only this entry, and moves the part unchanged onto part
  37's fourth round, which changes none of its files but the handoff.
- Verification, snapshot round 5 (`/tmp/opencode/review-38f`, on part 37's
  fourth round), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (465
  top-level tests pass and 3 skip); `-race -count=3` on 112 related tests;
  the 7 new tests 20 times and 5 times under `-race`; the docs checker
  finds 10 relative links in docs/DEVELOPMENT.md and no problem, and the
  docs tests pass; the whole module, in the desktop-like sandbox, connects
  to none of its decoy sockets, while the positive control reaches two.
  The runs against older code were not repeated, as the part's code and
  docs are round 3's bytes. Every file of the part but the handoff and
  `screenshot_test.go` (which part 39 also changes) equals the worktree's,
  and this entry is the worktree's. These checks ran before this bullet
  was written; the snapshot was then rebuilt with it, the same files.
- Review round 5 (PASS), with one nit, corrected above: the sentence on a
  refusal recorded after both reads left out a browser that hands the
  mirror over to a running one, whose log the watcher removes whatever
  `hadClient` says (the exit report's handover branch; the review's probe,
  with the stop's read taken while the browser still ran). It found round
  4's nits resolved, only the handoff changed since round 4, and every
  figure of the round-5 snapshot bullet reproduced. With the fix, round 6
  also says the late case generally, and that the watcher waits only for
  the browser.
- Round 6 changes only this entry, and moves the part unchanged onto part
  37's fifth round (`/tmp/opencode/review-37e`), which changes none of its
  files but the handoff.
- Verification, snapshot round 6 (`/tmp/opencode/review-38g`, on part 37's
  fifth round): the same checks as round 5's, with the same results: 465
  top-level tests pass and 3 skip, the related, new and docs checks pass,
  and the whole module connects to none of its decoy sockets, while the
  positive control reaches two.
- Review round 6 (BLOCKED on part 37): part 37's fifth round, this
  snapshot's base, needed changes, so the tree could not land as reviewed,
  and the Round 6 bullet's "part 37 as committed" was not true (corrected
  above). It found nothing to change in this part: the files, the delta
  and this entry's corrections right, and every check reproduced. It noted
  that round 5's findings file lists no suggestions, so the bullet that
  called the two wordings above its suggestions now says only that round 6
  made them. Its suggestion, taken: a move names part 37's round until
  part 37 has landed.
- Round 7 changes only this entry, and moves the part unchanged onto part
  37's sixth round, which changes none of its files but the handoff.
- Review round 7 (PASS), with no findings: the files, the delta and this
  entry right, the base exactly part 37's sixth round, and every check
  reproduced. Part 37's sixth round then needed changes.
- Round 8 changes only this entry, and moves the part unchanged onto part
  37's seventh round, which changes none of its files but the handoff.
- Review round 8 (PASS), with no findings: the files, the delta and this
  entry right, the base exactly part 37's seventh round, and every check
  reproduced. Its suggestions, and the last move review's, concern the
  scripts that build the snapshots, which now count an entry's changed
  lines right. Part 37's seventh round then needed changes.
- Round 9 changes only this entry, and moves the part unchanged onto part
  37's eleventh round, which changes none of its files but the handoff.
- Not run: a real browser, Firefox, `--window` on a desktop, a device, the
  terminal UI and CI. The real presence search ran only in the sandbox (no
  route).

## Update (2026-10-21: QoL overhaul, part 37: terminal and command-line loose ends)

- Device text no longer reaches the terminal raw in three more places, each
  through `cleanText`: the server's last lines in start errors
  (`serverOutputNote`; a line that cleans to nothing is left out), the
  reason and name in `endSession`'s peer line, and peer notices (the
  device's name, the lease holder, a refusal's type and words), which stderr
  and the exit replay printed as they were. Text without control or format
  characters reads as before (`cleanText` turns a format character, such as
  a zero-width space or joiner or a soft hyphen, into a space), but it folds
  a run of spaces into one and drops spaces at either end (see Left alone).
- The sound sink's error has one accessor, `failure()`. `err` is now only
  open's error or `errNoSound`, set before the sink is shared; a failed
  write sets `writeFailed` (atomic) on the playback goroutine. Every read in
  audio.go, app.go, input.go and audiofollow.go goes through `failure()`.
  Two reads of `err` stay, race-free but blind to a failed write:
  web.go:1568 (`sendStatus`, the volume the browser shows), off limits to
  this part, and webinput.go:114 (the volume its slider sets), kept with it
  so the two agree: with only webinput.go on `failure()`, a slider move
  after a failed write would be dropped while the page kept showing the old
  volume.
- scterm doctor counts only pairings peer mode can use: none without an
  identity it can load, and none whose fingerprint does not parse (these
  get a warning). With no adb and only pairings whose fingerprints do not
  parse, doctor could exit 0; now its adb line fails and it exits 1. With
  the identity missing or damaged it already exited 1 on its peers line;
  there only the adb line went from a warning to a failure. The `--peer`
  advice is typable: `--peer NAME`, or `--peer=` with one usable pairing.
  The shared hints changed, so scterm's own "adb not found" and "no device
  found" errors also say `--peer NAME`.
- Doctor: paired rows in `TestDoctorOnAPeerOnlyComputer` pin that an
  offline device and an ANDROID_SERIAL naming no device or two still fail,
  and `doctorDevices` states the rule. `doctorPeers` reads the identity once
  with the new `peer.LoadIdentity`, which never creates one (no stat before
  it, so an unsearchable home says "open ...", not "stat ..."). The
  `matchesTarget` comment says a USB device listed without a path answers
  as a network one (ABC:12 answers to ABC); a test pins it.
- Paste: README and input.go's comment say that after a Ctrl+C, input is
  dropped up to the paste's end or until the terminal sends nothing for a
  quarter of a second, so a rest that pauses that long is typed as keys.
  Reworded, not changed: no bounded wait makes the old sentence true, and a
  longer one drops the user's own keys after every Ctrl+C (part 27).
- README: `PULSE_SERVER` and `PULSE_SINK` rows. `readmeFlags` leaves out
  most of what GitHub shows as code or hides in an HTML comment: it skips
  fenced code, of backticks or tildes, which only a fence of the same
  character ends (at least as long, indented three columns at most, with
  only white space after it, where GitHub takes only spaces and tabs before
  the line ending); a line indented four columns or more is never a row
  (outside lists and block quotes, code or more of a paragraph); a line that
  starts with `<!--` hides itself and the lines after it through the one
  holding `-->` (to the end if none); a row is split into cells first, as
  GFM does, and in its first cell only a comment closed in the cell is taken
  out, while a `<!--` left open or in a code span is plain text (round 3);
  `<!-->` and `<!--->` are whole comments (round 4). Its doc comment lists
  ways it still differs from GitHub, which the reference does not use, and
  what a missed or an extra row does (rounds 4 to 7). The rules this part
  added have tests, every input checked in md4c and markdown-it-py (rounds 2
  to 8). Of the mutants of those rules tried in rounds 4 to 8, round 4's own
  and the ones the reviews of rounds 4 to 7 sent, each fails a README test
  but one, which cannot change what is read (a tab counted as four columns
  anywhere, the same at the threshold). Two rules kept from the old reader
  have tests too: a backtick fence with an info string such as `sh` opens
  code (round 6), and a comment block hides headings (round 6), the
  section's `### Flags` included (round 7). The old reader's other rules have
  none of their own: a name counted once per row, commas between names, a
  bare dash, a row needing two pipes, and the headings' forms.
- Tests: `TestForwardDialIsBounded` checks nothing is left running; the
  default wait for the server's reason is pinned at 0.9 to 2 s, and a
  reason half a second late is given; a restore behind a lock never let go
  takes 90 to 300 ms. New: `TestStartErrorsCleanWhatTheServerSaid`,
  `TestSessionEndCleansThePeersReason`, `TestPeerNoticesAreCleaned` (OSC,
  CSI, U+009B, bare Esc, clean), `TestSinkThatFailsWhilePlaying` (a zero C
  sink fails in the C write while other goroutines read),
  `TestLoadIdentityNeverCreatesOne`, `TestMatchesTargetCannotTell...`, three
  `TestReadmeFlagsLeaveOut...`, `TestNoSoundWaitsAboutASecondForTheReason`.
- Intake: merged three ways into the worktree over parts 33 to 36 (the
  builder's base was `40cb1a0`, with part 36's first round). `README.md`
  merged cleanly. `app.go` conflicted twice with part 36's second round,
  which moved the start-up line into `sayAudioDisabled`: that function is
  kept, and its check is now `a.audio.failure() != nil` (the same as
  reading `err` there, before any write). The other 20 files are the
  builder's. At intake the two new README rows, 260 columns wide where no
  other README line is over 100 (lines 540, 575 and 600 are exactly 100),
  were cut to a few words each, with the rest of their text in a paragraph
  under the table.
- Verification (builder's tree, base `40cb1a0`, iso.env, bwrap, go1.27.1):
  gofmt, vet, build and `git diff --check` clean. `go test -count=1 -v
  ./...`: 453 pass, 3 skip, 0 fail (base: 443, 3, 0; +10 new). `-race` on
  the module: 453, 3, 0, no race. `-race -count=3` on 156 tests of the
  touched and related files: 468/468. New tests x20: 200/200; under
  `-race` x5: 50/50. On the base code 14 changed or new tests fail, the
  sink test reports a race under `-race` in 10 of 10 runs (0 of 10 on this
  part), and the LoadIdentity test does not build. Mutants: 52 of 52
  killed; 4 equivalent (reads before playback starts) survive.
- Not run: a real terminal, adb, phone, peer target or sound server (the
  failed write is the C check of a zero sink, not pa_simple_write);
  GitHub's renderer for the README rules; the node suites (web/ untouched).
- Left alone: the two `err` reads above; a pairing that does not parse
  cannot be removed with `scterm forget` (`Store.Find` skips it); the
  250 ms pause after a cancel; `cleanText` folds runs of spaces in clean
  server lines into one, as the status row already did.
- Review round 1 (NEEDS CHANGES), resolved in round 2. MEDIUM: two
  `readmeFlags` rules contradicted rendering (md4c 0.6.0 `md2html
  --github`, markdown-it-py 4.2.0 CommonMark with tables): a row indented
  four spaces under a table counted where both show code, and an unclosed
  `<!--` in a row hid the rest of the file where both show the later
  `--peer NAME` row (and rows after `<!--` in a code span, or after a
  comment opened in one row and closed in the next); the doc comment
  called both CommonMark's. LOW: the backtick fence's info-string rule was
  untested (its condition replaced by `true` passed). LOW, for a later
  part: device text still reaches the terminal raw through `Store.Find`,
  the peer chooser, `scterm pair` and adb's stderr. Four wording nits,
  corrected above. The snapshot: 455 pass, 3 skip, 0 fail, also under
  `-race`. Suggestions: read the clock before `device.Write`; "the device"
  for a name that cleans to nothing; web.go and webinput.go on `failure()`
  once part 38 is in.
- Round 2: `readmeFlags` skips an indented line whatever is above it (the
  `text` state is gone); a line starting with `<!--`, three spaces in at
  most, hides lines through the one holding `-->`, that line's rest
  included (`<!-->` closes at once); `uncommented` takes out only comments
  closed on the line, steps over code spans and leaves an open `<!--` as
  text. The doc comment states each rule, citing GFM 0.29-gfm and
  CommonMark 0.31.2 from memory. `TestReadmeFlagsLeaveOutAnUnclosedComment`
  has six named cases; `TestReadmeFlagsLeaveOutIndentedCode` adds a tab and
  a paragraph's indented lines; the new
  `TestReadmeFlagsCountRowsAfterBackticksThatAreNoFence` pins the
  info-string rule, for tildes too. `endSession` says "the device" for a
  name that cleans to nothing, as `deviceName()` does (tested with "", Esc
  BEL, and a space, a zero-width space and a tab).
  `TestNoSoundWaitsAboutASecondForTheReason` reads the clock before
  `device.Write`.
- Verification, round 2 (clone of the worktree with part 38 in, base
  `462579e`, iso.env, bwrap, go1.27.1): gofmt, vet, build, `git diff
  --check` clean. `go test -count=1 -v ./...`: 463 pass, 3 skip, 0 fail
  (base 462, 3, 0; +1 new); `-race`: the same, no race. 17 touched tests
  `-race -count=3`: 51/51; the 5 changed tests x20: 100/100, `-race` x5:
  25/25. Each of the 9 `TestReadmeFlags...` inputs gives the test's rows in
  md4c and in markdown-it-py (round 1's 3 inputs: 4 of 6 renderings
  differed); README.md has 31 rows in both renderers and both rounds'
  readers. On round 1's code the comment test fails 4 of 6 cases, the
  indented test fails, the fence test passes (it pins round 1's rule) and
  the endSession test fails its 3 nameless names. Mutants: 20 of 20 killed;
  the clock read put back after the write survives (timing only): with the
  test's goroutine 150 ms late after `Write`, round 1's test failed 5 of 5
  (0.856 to 0.867 s), round 2's passed 5 of 5 (1.009 to 1.021 s).
- Verification, snapshot round 2 (`/tmp/opencode/review-37b`: round 1
  moved unchanged onto part 36's snapshot review-36e, with round 2's four
  files laid over it), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (456 top-level tests pass and 3 skip); `-race -count=3` on 90 related
  tests; the 11 new tests and 2 of the 10 changed ones 20 times and 5
  times under `-race`. Round 1's `readmeFlags` under round 2's tests fails
  the comment and indented-code tests (the two fence tests pass), and
  round 1's `main.go` fails `TestSessionEndCleansThePeersReason`. The docs
  checker finds 31 relative links and no problem; the whole module, in the
  desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two. Part 38, replayed onto this `exit_test.go`,
  gives the worktree's.
- Not run, round 2: GitHub's renderer (cmark-gfm) and the specs' text (no
  network); a comment holding `--`, where the spec versions disagree (a
  comment spanning a cell's pipe is round 3's); a real terminal, device,
  peer target or sound server; the node suites (web/ untouched).
- Left alone, round 2: a `|` line cut off from its table (after a blank
  line, indented code or a comment block) still counts, though both
  renderers show it as text; `readmeFlags` does not track a table's header
  and delimiter rows, and the README's 31 rows all sit in tables. A comment
  block right under a row: md4c keeps it in the table, markdown-it-py does
  not, so no test puts one there.
- Review round 2 (NEEDS CHANGES): LOW, comments were taken out of a whole
  row before its split into cells, which GFM does not do (it splits a row
  at its pipes before it reads the cells), so a flag after a `-->` in a
  later cell counted and a first-cell flag before such a comment was
  missed; the reader followed md4c, which reads a comment across the
  pipes, for comments, but markdown-it-py for code spans. LOW, the rule
  that a comment block runs through the line holding `-->` was tested only
  with a closing line that starts with `-->` (a mutant ending the block
  only there passed). Nits: the README bullet said `readmeFlags` counts
  only lines GitHub shows as rows, and the first bullet said text without
  control characters reads as before (format characters turn into a space
  too); both are corrected above. In its words, round 1's MEDIUM and its
  untested-rule LOW were found fixed (the cross-pipe case above was its own
  first LOW, and round 1's LOW for a later part stays open); it found the
  `endSession` and clock fixes right, and every figure reproduced.
- Round 3: `uncommented` runs on a row's first cell, after the split, and
  on a line that is no row, such as a heading; the doc comment says so and
  names two ways the reader still differs from GitHub (it splits at escaped
  pipes, and counts a row cut off from its table); rounds 4 and 5 list more.
  `TestReadmeFlagsEndACommentWithItsCell` pins the cell rule:
  markdown-it-py shows its rows, md4c does not, as its comment says. The
  comment block in the "a block" case closes on a line that starts with
  text (`soon -->`). A row holding `<!--` in a code span, a flag, and a
  bare `-->` in the same cell pins that the code span opens nothing; round
  2's row for that closed in a later cell, so with each cell read alone it
  no longer did (the mutant that stops skipping code spans passed).
- Verification, round 3 (the worktree, iso.env, bwrap, go1.27.1): gofmt
  and vet clean; the six README tests pass. Each of the 10
  `TestReadmeFlags...` inputs gives the test's rows in markdown-it-py
  4.2.0, and in md4c 0.6.0 all but the cell test's. Mutants (`go test
  -overlay`): the builder's 17 (four adapted to the cell rule), the
  review's two survivors and a variant, and a heading that keeps its
  comment each fail a README test, as do round 1's and round 2's readers
  under round 3's tests (round 2's fails only the cell test); the review's
  closing-line mutant passes round 2's tests.
- Verification, snapshot round 3 (`/tmp/opencode/review-37c`: round 2
  moved unchanged onto part 36's snapshot review-36f, on part 33 as
  committed, with round 3's `readme_test.go` laid over it), every Go
  command inside the bwrap sandbox with `iso.env`: gofmt, vet, the build
  and `git diff --check`; `go test ./...` (458 top-level tests pass, 3
  skip, 0 fail); `-race -count=3` on 91 related tests; the 12 new tests
  and 2 of the 10 changed ones 20 times and 5 times under `-race` (the
  review ran the other 8 too: 160 of 160, and 40 of 40 under `-race`).
  Round 1's `readmeFlags` under round 3's tests fails the comment, cell and
  indented-code tests; round 2's fails only the cell test. The docs checker
  finds 31 relative links and no problem; the whole module, in the
  desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two.
- Left alone, round 3: flag tables inside a block quote or a list item
  whose content starts four or more columns in, and code spans of two
  backticks, are missed (a flag listed only there fails the test as
  unlisted); "it is code, or more of a paragraph" holds only at the top
  level; `opensFence`'s own indentation check is now redundant.
- Review round 3 (NEEDS CHANGES): LOW, `uncommented` looked for `-->`
  only after the whole `<!--`, so an inline `<!-->` or `<!--->`, a whole
  comment in CommonMark 0.31.2 (and no comment in 0.29), hid the text up
  to a later `-->`: a first-cell flag after one was missed, and a heading
  holding one was read under a name GitHub does not show. Nits: "two ways"
  the reader differs from GitHub (the review found others: other HTML
  blocks, setext headings, rows without a leading pipe, a fence opened on
  a list item's line); the Review round 2 bullet's "apart from the first
  LOW"; the snapshot bullets' "new and changed" counts (12 tests are new
  and 10 changed); and "a list item", which holds only for a nested one.
  All four are corrected above, the first only in part until round 5. It
  found round 2's findings resolved, the cell rule agreeing with
  markdown-it-py on 32 probes but for escaped pipes and the LOW, and every
  figure reproduced.
- Round 4: `uncommented` looks for `-->` from two bytes into the `<!--`,
  so `<!-->` and `<!--->` are whole comments, as the block rule reads them;
  two rows pin that. The doc comment lists more ways the reader still
  differs from GitHub, and says what a miss does (round 5 adds to the list
  and corrects what a miss does). From the review's suggestions, three
  more rows pin rules no test held: a comment in the section's own heading
  (a missing TrimSpace), a comment opened in the first cell that closes in
  the second, and a flag inside a closed comment before an unclosed `<!--`
  (the first `<!--` must be read first).
- Verification, round 4 (the worktree, iso.env, bwrap, go1.27.1): gofmt
  and vet clean; the six README tests pass. Each of the 11
  `TestReadmeFlags...` inputs gives the test's rows in markdown-it-py, and
  in md4c all but the cell test's, as its comment says. Mutants: round 3's
  21, round 4's fix undone, and the review's three (a comment closing in
  the second cell, no TrimSpace, the last `<!--` first) each fail a README
  test, as do round 1's, round 2's and round 3's readers under round 4's
  tests; round 3's reader passes round 3's own tests (the review's gap).
- Verification, snapshot round 4 (`/tmp/opencode/review-37d`: round 2
  moved unchanged onto part 36's snapshot review-36f, on part 33 as
  committed, with round 4's `readme_test.go` laid over it), every Go
  command inside the bwrap sandbox with `iso.env`: gofmt, vet, the build
  and `git diff --check`; `go test ./...` (458 top-level tests pass, 3
  skip, 0 fail); `-race -count=3` on 93 related tests; the 12 new and 10
  changed tests 20 times and 5 times under `-race`. Round 1's
  `readmeFlags` under round 4's tests fails the comment, cell and
  indented-code tests; round 2's fails the comment and cell tests; round
  3's fails the comment test. The docs checker finds 31 relative links and
  no problem; the whole module, in the desktop-like sandbox, connects to
  none of its decoy sockets, while the positive control reaches two.
- Review round 4 (NEEDS CHANGES): LOW, seven rules of the reader had no
  test, though this entry said each had one: backtick fences open code,
  two fence characters do not, a closing fence with text after it or one
  indented four columns closes nothing, a blank line does not end fenced
  code, a second comment closed in a cell is taken out too, and a fence
  line inside a comment block opens nothing (a mutant of each passed every
  README test). Nits: what a missed row does held only for a flag's only
  row, and an extra row could fail the test too; the list of differences,
  called complete, left out a fence or
  `<!--` on a list item's line, single-pipe rows, tables in a top-level
  list item four columns in, and the headings' forms; round 3's mutants
  were 21, not 23. It found round 3's findings resolved (the search from
  two bytes in matches only a `-->` that overlaps the `<!--`; 800 fuzzed
  cells and headings never disagreed with both renderers) and every
  figure reproduced. Suggestions taken: round 2's words quoted; the old
  reader's rules named as untested, above.
- Round 5 changes only `readme_test.go`'s tests and comments, and this
  entry: the reader's code is round 4's.
  `TestReadmeFlagsLeaveOutTildeFences` becomes
  `TestReadmeFlagsLeaveOutFencedCode`, with backtick fences, a blank line
  inside one, a closing fence with text after it and one indented four
  columns, and `~~struck~~` text, which opens nothing; the comment test
  gains a cell with two closed comments, the second holding a flag, and a
  fence line inside a comment block. The doc comment adds the differences
  the review found, as some among others, and says what a missed or an
  extra row does (two of an extra row's six outcomes; rounds 6 and 7 name
  the others); the sentences above that said otherwise are corrected.
  Left as it was: a row with one pipe is still missed (the review's
  mutant reading it, as GitHub does, changes no test's outcome).
- Verification, round 5 (the worktree, iso.env, bwrap, go1.27.1): gofmt
  and vet clean; the six README tests pass. Each of the 11
  `TestReadmeFlags...` inputs gives the test's rows in markdown-it-py, and
  in md4c all but the cell test's, as its comment says. The reader's code,
  comment lines aside, is round 4's. Mutants (`go test -overlay`): round
  4's 25, and 19 of the round-4 review's 27, each fail a README test. The
  8 that pass: six change the old reader's rules (a "#####" line read as a
  "####" heading, any heading that starts with "### Flags", an untrimmed
  heading name, a name twice in a row, a comma between names, a bare
  dash), one reads a row with one pipe, and one counts a tab as four
  columns anywhere, which comes to the same at the threshold. The review's
  seven survivors pass under round 4's file and fail under round 5's;
  round 1's, round 2's and round 3's readers fail round 5's tests.
- Verification, snapshot round 5 (`/tmp/opencode/review-37e`: round 4's
  snapshot with round 5's `readme_test.go`, on the same base, whose tree
  is now the branch tip's), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (458 top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 93
  related tests; the 12 new and 10 changed tests 20 times and 5 times
  under `-race`. Under round 5's tests, round 1's reader fails the comment,
  cell and indented-code tests, round 2's the comment and cell tests, and
  round 3's the comment test; each of the round-4 review's seven survivors
  fails a README test, and the first of them passes round 4's file. The
  docs checker finds 31 relative links and no problem; the whole module,
  in the desktop-like sandbox, connects to none of its decoy sockets, while
  the positive control reaches two. These checks ran before this bullet
  was written; the snapshot was then re-packed with it, which changed only
  the handoff.
- Review round 5 (NEEDS CHANGES): LOW, "each rule this part added has a
  test" was still untrue: 11 of the review's mutants passed all six README
  tests. They changed rules this part added (a closing fence three columns
  in closes; an opening fence indented one to three columns opens; inside
  fenced code, a `<!--` line starts no comment and a `#` line is no heading
  and ends nothing; `~~~x` closes nothing; a comment block runs through an
  indented line that holds `-->`) and two kept from the old reader (a
  backtick fence with an info string opens code; a comment block hides
  headings). NIT: the doc comment named only two outcomes of an extra row,
  a hidden flag and a second row (the review counted four; there are six,
  round 7 below). It found round 4's findings resolved and every figure
  reproduced. Suggestions taken: "four or more columns in", and the Review
  round 4 bullet's summary of that review's first nit.
- Round 6 changes only `readme_test.go`'s tests and comments, and this
  entry. The review's fix, which it checked in both renderers, adds to the
  comment test's block case a comment holding `## Not the end` and
  `#### Hidden` that closes on a line indented four columns, and to the
  fenced-code test a `sh` fence holding a shell comment, a `<!--` line and
  a `####` line; a tilde fence that `~~~x` does not close, holding the
  same; and a fence opened and closed three columns in, before a last
  table. The tests' comments say so, the doc comment names four outcomes
  of an extra row (round 7 adds two), and the sentences above that said
  otherwise are corrected.
- Verification, round 6 (the worktree, iso.env, bwrap, go1.27.1): gofmt
  and vet clean; the six README tests pass. Each of the 11
  `TestReadmeFlags...` inputs gives the test's rows in markdown-it-py, and
  in md4c all but the cell test's, as its comment says. The reader's code,
  comment lines aside, is round 4's. Mutants (`go test -overlay`): round
  4's 25, 19 of the round-4 review's 27 (the same 8 pass as in round 5),
  and all 23 of the round-5 review's each fail a README test; under round
  5's file, 11 of those 23 pass, the review's gap. Round 1's, round 2's
  and round 3's readers fail round 6's tests as they failed round 5's.
- Verification, snapshot round 6 (`/tmp/opencode/review-37f`: round 5's
  snapshot with round 6's `readme_test.go`, on the same base, whose tree
  is the branch tip's), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (458 top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 93
  related tests; the 12 new and 10 changed tests 20 times and 5 times
  under `-race`. Under round 6's tests, round 1's reader fails the
  comment, cell and indented-code tests, round 2's the comment and cell
  tests, and round 3's the comment test; each of the round-5 review's 11
  survivors fails a README test, and the first of them passes round 5's
  file. The docs checker finds 31 relative links and no problem; the
  whole module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two. These checks ran
  before this bullet was written; the snapshot was then re-packed with it,
  which changed only the handoff.
- Review round 6 (NEEDS CHANGES): LOW, "each rule this part added has a
  test" was still untrue. Seven of the review's mutants, one of them three
  of the others at once, passed all six README tests, each on a probe both
  renderers show as the reader reads it: a `### Flags` in a comment block,
  in fenced code or on an indented line started the reference (no test had
  text before the section); a code span kept a `<!--` from opening a comment
  only when it was the first span in the cell; a closing fence's indentation
  counted bytes, so " \t~~~" closed; and a tilde fence's info string could
  not hold a tilde. Three weaker mutants passed too: a line holding "->"
  ended a comment block, an indented closing line ended one only if it
  started with "-->", and a heading's comments were taken out by a pattern
  that does not read "<!-->" as a whole comment. NIT: an extra row has more
  outcomes than the four the doc comment named, and than the Review round 5
  bullet counted: it also fails the test written with the wrong number of
  dashes, or for `--mirror-fps`. It found round 5's findings resolved and
  every figure reproduced. Suggestions taken: its fix, which it checked in
  both renderers, its new inputs described in the tests' comments, and pins
  for the three weaker mutants.
- Round 7 changes only `readme_test.go`'s tests and comments, and this
  entry. The review's fix adds a `### Flags` before the section in a
  comment block (the comment test), in a tilde and a backtick fence (the
  fenced-code test) and on an indented line (the indented-code test), each
  followed by a table whose row must not count; a row whose `<!--` sits in
  its second code span; a tilde fence that " \t~~~", indented four
  columns, does not close; and a tilde fence with "a~b" after it. Round 7
  adds a line holding "->" inside a comment block, a block that closes on
  a line indented four columns with text before the "-->", and the
  heading "#### Peer <!-->x -->", read as "Peer x -->". The tests'
  comments describe the new inputs, the doc comment also names the two
  outcomes the review found, and the sentences above that said otherwise
  are corrected.
- Verification, round 7 (the worktree, iso.env, bwrap, go1.27.1): gofmt
  and vet clean; the six README tests pass. Each of the 12
  `TestReadmeFlags...` inputs gives the test's rows in markdown-it-py, and
  in md4c all but the cell test's, as its comment says. The reader's code,
  comment lines aside, is round 4's. Mutants (`go test -overlay`): round
  4's 25, 19 of the round-4 review's 27 (the same 8 pass), all 23 of the
  round-5 review's and 10 of the round-6 review's 11 each fail a README
  test. The one that passes opens a comment at a line that starts with
  `<!-`; md4c disagrees with markdown-it-py on the review's probe for it,
  though the two agree on others (round 7's review, below). Under round
  6's file all 11 pass, the review's gap. Round 1's, round 2's and round
  3's readers fail round 7's tests as they failed round 6's.
- Verification, snapshot round 7 (`/tmp/opencode/review-37g`: round 6's
  snapshot with round 7's `readme_test.go`, on the same base, whose tree
  is the branch tip's), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (458 top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 93
  related tests; the 12 new and 10 changed tests 20 times and 5 times
  under `-race`. Under round 7's tests, round 1's reader fails the
  comment, cell and indented-code tests, round 2's the comment and cell
  tests, and round 3's the comment test; each of the round-6 review's
  mutants fails a README test but the one that opens a comment at a `<!-`
  line, which passes, as expected, and the first of them passes round 6's
  file. The docs checker finds 31 relative links and no problem; the
  whole module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two. These checks ran
  before this bullet was written; the snapshot was then re-packed with it,
  which changed only the handoff.
- Review round 7 (NEEDS CHANGES): LOW, the reason the Verification round 7
  bullet gave for the one mutant left, the one that opens a comment at a
  `<!-` line, was untrue: md4c and markdown-it-py disagree only on the
  round-6 review's probe for it, which has no ">" on the line. On `<!- note
  >`, `<!-x>`, or `<!- a` with the ">" on the next line, both show the table
  after it, as the reader reads it, while the mutant hides that table. The
  README bullet's second exception rested on the same reason. NIT: the
  Review round 6 bullet counted six of that review's mutants, where it had
  seven. Both are corrected above. It found round 6's findings resolved and
  every figure it re-ran reproduced, though it ran fewer repeats than this
  entry's (10 runs instead of 20, 3 under `-race` instead of 5, and the
  related tests once instead of 3 times). Suggestions taken: its fix,
  checked in both renderers, which pins that mutant and a block opened by
  "<!--" with no space after it; pins for its other mutants, each on a probe
  both renderers show as the reader reads it; the review rounds the README
  bullet's claim covers, named; and the mutants run on the snapshot's tree.
- Round 8 changes only `readme_test.go`'s tests and comments, and this
  entry. The review's fix adds to the comment test's "a block" case a
  block opened by "<!--old:" and a line "<!- no comment >", each followed
  by a table whose row must count. Round 8 adds to the same case a block
  that closes on a line indented four columns with text after the "-->",
  a line that starts with "<!-->" and holds a row, which must not count,
  and a block that closes on a line indented eight columns, each followed
  by a table whose row must count; to the fenced-code test, a tilde fence
  closed by "~~~" and a tab; and to the test of backticks that are no
  fence, the line "``` sh x`y", whose backtick is in the info string's
  second word. The tests' comments describe the new inputs, and
  `closesFence`'s comment says white space may follow a closing fence. The
  reader's code is round 4's, comment lines aside.
- Verification, round 8, on the snapshot's tree (`/tmp/opencode/review-37h`,
  as the review suggested; iso.env, bwrap, go1.27.1): gofmt and vet clean;
  the six README tests pass. Each of the 12 `TestReadmeFlags...` inputs
  gives the test's rows in markdown-it-py, and in md4c all but the cell
  test's, as its comment says. The reader's code, comment lines aside, is
  round 4's. Mutants (`go test -overlay`): round 4's 25, 19 of the round-4
  review's 27 (the same 8 pass), and all 23 of the round-5 review's, all
  11 of the round-6 review's and all 7 of the round-7 review's each fail a
  README test. Under their own rounds' files the round-6 review's 11 and
  the round-7 review's 7 all pass, the reviews' gaps. Round 1's, round 2's
  and round 3's readers fail round 8's tests as they failed round 7's.
- Verification, snapshot round 8 (`/tmp/opencode/review-37h`: round 7's
  snapshot with round 8's `readme_test.go`, on the same base, whose tree
  is the branch tip's), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (458 top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 93
  related tests; the 12 new and 10 changed tests 20 times and 5 times
  under `-race`. Under round 8's tests, round 1's reader fails the
  comment, cell and indented-code tests, round 2's the comment and cell
  tests, and round 3's the comment test; each of the round-7 review's
  seven mutants fails a README test, and the first of them passes round
  7's file. The docs checker finds 31 relative links and no problem; the
  whole module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two. These checks ran
  before this bullet was written; the snapshot was then re-packed with it,
  which changed only the handoff.
- Review round 8 (NEEDS CHANGES), on wording only. LOW: the README bullet
  above said a closing fence has nothing after it, and the fenced-code
  test's comment said spaces or tabs, while `closesFence` takes any white
  space (`strings.TrimSpace`): after "~~~" and a no-break space, a vertical
  tab, a form feed, NEL, an em space or an ideographic space, md4c and
  markdown-it-py keep the code open and the reader closes it (the README has
  none of these). NIT: the Review round 7 bullet said every figure
  reproduced, where that review ran fewer repeats. Both are corrected above,
  and `readmeFlags`' doc comment now lists the white-space case among its
  differences from GitHub. It found round 7's findings resolved, and every
  figure it re-ran reproduced, with fewer repeats than this entry's for the
  22 new and changed tests (3 times, and twice under `-race`) and the 93
  related tests (once under `-race`); it ran the README tests 50 times. Left
  for a later change, from its suggestions: three of its own mutants pass
  every test, outside the README bullet's claim, each on a probe both
  renderers show as the reader reads it (a `<!-->` line also hiding the line
  after it, the line after any comment block's last line hidden too, and a
  line indented eight columns ending a block only if it starts with `-->`);
  and "hides" is loose for text after a `<!-->`, or after a block's `-->` on
  its line, which both renderers show as plain text, though not as a row.
- Round 9 changes only comments in `readme_test.go` (the fenced-code test's,
  and a sentence in `readmeFlags`' doc comment) and this entry. The reader's
  code and the tests are round 8's.
- Verification, round 9, on the snapshot's tree (`/tmp/opencode/review-37i`;
  iso.env, bwrap, go1.27.1): gofmt and vet clean; the six README tests pass.
  Each of the 12 `TestReadmeFlags...` inputs gives the test's rows in
  markdown-it-py, and in md4c all but the cell test's, as its comment says.
  The reader's code, comment lines aside, is round 4's. Mutants (`go test
  -overlay`): round 4's 25, 19 of the round-4 review's 27 (the same 8 pass),
  and all 23 of the round-5 review's, all 11 of the round-6 review's and all
  7 of the round-7 review's each fail a README test; under their own rounds'
  files the round-5 review's 11, the round-6 review's 11 and the round-7
  review's 7 pass, the reviews' gaps. Round 1's, round 2's and round 3's
  readers fail round 9's tests as they failed round 8's.
- Verification, snapshot round 9 (`/tmp/opencode/review-37i`: round 8's
  snapshot with round 9's `readme_test.go`, on the same base, whose tree is
  the branch tip's), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (458 top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 93 related
  tests; the 12 new and 10 changed tests 20 times and 5 times under `-race`.
  Under round 9's tests, round 1's reader fails the comment, cell and
  indented-code tests, round 2's the comment and cell tests, and round 3's
  the comment test; each of the round-7 review's seven mutants fails a
  README test, and the first of them passes round 7's file. The docs checker
  finds 31 relative links and no problem; the whole module, in the
  desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two. These checks ran before this bullet was
  written; the snapshot was then re-packed with it, which changed only the
  handoff.
- Review round 9 (NEEDS CHANGES), two NITs. The Review round 8 bullet
  counted that review's 50 runs of the README tests among its fewer repeats,
  where this entry repeated them at most 20 times in one run
  (`TestReadmeListsEveryFlag`, not among the 22, at most 3 times, under
  `-race` with the related tests). And the doc comment's new
  sentence covered a carriage return too: the reader splits lines only at
  "\n", so a closing fence with a CRLF line ending has a "\r" after it,
  which `strings.TrimSpace` takes, as GitHub does, for which a carriage
  return ends the line. Both are corrected above. It found round 8's
  findings resolved and every figure it re-ran reproduced (with fewer
  repeats than this entry's: the 22 tests 5 times and twice under `-race`,
  the related tests once under `-race`). Its suggestions, taken: a CRLF
  closing fence in the fenced-code test (below); the round-8 review's X2
  (only spaces and tabs after a closing fence, its alternative fix) passed
  every test too, and is not taken, as it keeps the code open after a CRLF
  closing fence, where both renderers close it; and the stacked snapshots
  above this part are now cloned with `--no-hardlinks` (a later snapshot's
  `git clone` had refreshed the times of the object files in this one's
  tree, which share them).
- Round 10 changes `readme_test.go`'s fenced-code test and two of its
  comments, and this entry. The test adds "~~~" closing code with a CRLF
  line ending, followed by a table whose row (`--max-fps`) must count, which
  X2 hides. The test's comment names the carriage return among the white
  space a closing fence may have after it, and the doc comment's sentence
  excepts it. Fed the bytes as they are, md4c and markdown-it-py both close
  the code there and show the row. The reader's code is round 4's, comment
  lines aside.
- Verification, round 10, on the snapshot's tree (`/tmp/opencode/review-37j`;
  iso.env, bwrap, go1.27.1): gofmt and vet clean; the six README tests pass.
  Each of the 12 `TestReadmeFlags...` inputs gives the test's rows in
  markdown-it-py, and in md4c all but the cell test's, as its comment says.
  The reader's code, comment lines aside, is round 4's. Mutants (`go test
  -overlay`): round 4's 25, 19 of the round-4 review's 27 (the same 8 pass),
  and all 23 of the round-5 review's, all 11 of the round-6 review's and all
  7 of the round-7 review's each fail a README test, as do three of the
  round-8 review's six: X2, X6 and X11. Its other three (X1, X1b and X13)
  pass, as the Review round 8 bullet says. Under their own rounds' files the
  round-5 review's 11, the round-6 review's 11 and the round-7 review's 7
  pass, the reviews' gaps, and X2 passes round 9's. Round 1's, round 2's and
  round 3's readers fail round 10's tests as they failed round 9's.
- Verification, snapshot round 10 (`/tmp/opencode/review-37j`: round 9's
  snapshot with round 10's `readme_test.go`, on the same base, whose tree is
  the branch tip's), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (458 top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 93 related
  tests; the 12 new and 10 changed tests 20 times and 5 times under `-race`.
  Under round 10's tests, round 1's reader fails the comment, cell and
  indented-code tests, round 2's the comment and cell tests, and round 3's
  the comment test; each of the round-7 review's seven mutants fails a
  README test, and the first of them passes round 7's file; the round-8
  review's X2 fails the fenced-code test, and passes round 9's file. The
  docs checker finds 31 relative links and no problem; the whole module, in
  the desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two. These checks ran before this bullet was
  written; the snapshot was then re-packed with it, which changed only the
  handoff.
- Review round 10 (NEEDS CHANGES), one NIT: the Review round 9 bullet said
  this entry ran the README tests 20 times at most and listed
  `TestReadmeListsEveryFlag`'s runs, leaving out two (the whole-module run
  in the decoy sandbox and the round's own README run); across round 9's
  runs the five README tests among the 22 ran 32 times, and 20 was one
  run's repeat count (corrected above). It found round 9's findings
  resolved, the new comments true for every character `strings.TrimSpace`
  takes (in 57 probes a space, a tab or a CRLF's carriage return closes the
  code in the reader and both renderers, the other 21 only in the reader),
  and every figure it re-ran reproduced, with fewer repeats than this
  entry's. Its suggestions, taken: the README bullet says GitHub takes only
  spaces and tabs before the line ending; the renderer run reads its inputs
  as bytes, so the CRLF input reaches both renderers with its carriage
  return (round 10's run, through Python's newline translation, fed it as a
  plain line end). Left for a later change, from its probes: a closing
  fence followed by a lone carriage return and more text keeps the code
  open in the reader, where both renderers close it (the README has no
  carriage return).
- Round 11 changes only this entry: the Review round 9 bullet's count, the
  README bullet's words, and the bullets from here on. `readme_test.go` and
  every other file are round 10's.
- Verification, round 11, on the snapshot's tree (`/tmp/opencode/review-37k`;
  iso.env, bwrap, go1.27.1): `readme_test.go` is round 10's bytes, so round
  10's mutant runs stand; gofmt and vet clean; the six README tests pass.
  Read as bytes (`/tmp/opencode/dev-37k/render.py`), the 12
  `TestReadmeFlags...` inputs, the same bytes as round 10's, give the
  test's rows in markdown-it-py, and in md4c all but the cell test's, as
  its comment says; the fenced-code test's input, the one with a carriage
  return, reaches both renderers with it, and each makes 13 code blocks of
  it.
- Verification, snapshot round 11 (`/tmp/opencode/review-37k`: round 10's
  snapshot with round 11's entry, on the same base, whose tree is the
  branch tip's), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (458
  top-level tests pass, 3 skip, 0 fail); `-race -count=3` on 93 related
  tests; the 12 new and 10 changed tests 20 times and 5 times under `-race`.
  Under round 10's tests, round 1's reader fails the comment, cell and
  indented-code tests, round 2's the comment and cell tests, and round 3's
  the comment test; each of the round-7 review's seven mutants fails a
  README test, and the first of them passes round 7's file; the round-8
  review's X2 fails the fenced-code test, and passes round 9's file. The
  docs checker finds 31 relative links and no problem; the whole module, in
  the desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two. These checks ran before this bullet was
  written; the snapshot was then re-packed with it, which changed only the
  handoff.

## Update (2026-10-21: QoL overhaul, part 36: sound work only for sessions that play here, one account of the sound)

- Behaviour: main silences other scterms' leftover sound streams
  (`cleanupStaleStreams`) only when the session plays its sound on this
  computer (`playsHere`, `app.go`): not with `--no-audio`, not with
  `--web` or `--window` without `--audio-dup` (the browser is the only
  output), and not from a peer target that grants no sound. A session
  without a sound stream gets a sink that is never opened (`noAudioSink`,
  error `errNoSound`): nothing connects to the sound server, no stream
  shows in the mixer, the follower does not start, and no
  `audio disabled` or `browser only` line is printed (`soundless`).
- The browser's `hello.audio` and `status.audio` say whether the
  session's sound is on: on while it has a sound stream that the device
  has not turned off, whether or not this computer's sink opened.
  `"error"` is gone. `hostAudio` is true only when the sound is on and
  the sink opened and is not kept silent. The device-off flag moved from
  `app` to `streamState.audioOff` (set by `runAudio`), so a web server
  without an app reads it. The HUD's `audioLabel` says `audio off` for
  anything but `"on"`. The terminal status row keeps its meaning.
- `--no-tui`'s statistics line says `audio=off` with `--no-audio`, once
  the device has turned the sound off, and when the sink did not open.
- The server's reason in `scterm: no sound from …` goes through
  `cleanText` in `playAudio`: stderr and the replay at exit printed it
  raw (the status row already cleaned it).
- Decisions: in peer mode a session has sound only with an audio socket
  (`sess.audio`, opened when the target grants and offers sound), which
  is known when main decides; such a session's status row now says
  `audio off`, not `vol 100%`. `--web`/`--window` without `--audio-dup`
  still open the silent sink, as the brief defaulted
  (`TestWebRunStopsItsAudioFollower` runs the follower there); it costs
  up to ~0.45 s at start without a sound server, and a silent stream in
  the mixer. `status.audio` is per session, not per viewer's caps.
- Tests: `audio_here_test.go` (main in a child process with a fake adb
  device served through adb forward, cleanup calls counted through
  `killAppStreams`, a listener as the sound server; `newApp` without
  sound; the browser's hello and status, with and without an app; the
  statistics line; control characters in the reason) and a `playsHere`
  table in `audio_routing_test.go`. `web/audio_test.mjs` pins the label.
  `SCTERM_TEST_CLEANUP_LOG` is named in `docs/DEVELOPMENT.md`.
- Verification (builder's tree, base `844148c`, iso.env, bwrap, go1.27.1,
  node v26.11.1): gofmt, vet and build clean. `go test -count=1 -v
  ./...`: 433 pass, 3 skip, 0 fail (base: 430 tests listed; +6 new).
  `-race`: the new tests and those of the changed code 101/101, the whole
  tree 433/3/0, no race reported. New tests x20: 120/120; under `-race`
  x5: 30/30. On the base files all 6 new tests fail, and the label check
  fails on the base `player.js`. Mutants: 13 of 13 killed.
- Intake: taken into the worktree on top of parts 33 and 34's second
  rounds and part 35's first. README.md and web.go merged three ways
  (`git merge-file`), each adding exactly part 36's changed lines, and the
  worktree passed `go test ./...` (439 pass, 3 skip) with iso.env in
  bwrap. Part 35's second round, taken in next, also changed stream.go,
  README.md and docs/ARCHITECTURE.md (443 pass, 3 skip). So in the
  snapshot, on part 35's second round, four files hold part 36's lines
  merged with the parts under them (README.md, web.go, stream.go and
  docs/ARCHITECTURE.md), and the other nine are the builder's.
- Review round 1 (NEEDS CHANGES), three LOWs: the Intake bullet described
  the earlier snapshot (on part 35's first round: two merged files and
  439 tests, where this one has four and 443); the README said that a
  session that plays no sound here leaves the other scterms' sound alone,
  and that a device that does not let it hear keeps it off the sound
  server, which a device that connects its sound socket and then turns
  the sound off (Android 10 over adb) does not; and the terminal display's
  start-up line, reverted to the base's condition, passed every test (a
  peer session without sound would print `audio disabled` again). Its
  suggestions: pin the cleanup's order (moving it after the sink opens
  passed every test, and would silence the session's own stream), and
  test the `audio disabled` line itself (removing `--no-tui`'s passed).
- Round 2: `run` and `runHeadless` print the line through one function,
  `sayAudioDisabled`. `TestAudioDisabledIsSaidOnlyWithSound` checks it:
  the line comes with sound and a failed sink, and not when the sink
  opened, with `--no-audio`, or from a peer target that grants no sound;
  and it runs `runHeadless` with a device that sends no sound. The child
  process test also checks that a session that silences connects to the
  sound server only after the cleanup (`listenPulse` takes a callback,
  which reads the child's cleanup notes as each connection comes). The
  README and docs/ARCHITECTURE.md say that main decides at start, before
  the device says whether it sends sound. The Intake bullet is corrected
  above.
- Verification, round 2, in the worktree (parts 33 to 36, part 33 in its
  third round), in bwrap with iso.env: gofmt and vet; the seven tests of
  this part; the two changed ones 20 times and 5 times under `-race`;
  `go test -count=1 -v ./...`, 444 pass, 3 skip, 0 fail. Mutants: the
  base's condition in `sayAudioDisabled` fails the new test and
  `TestSessionWithoutSoundStaysOffTheSoundServer`; `runHeadless` without
  the call, and a `sayAudioDisabled` that prints nothing, fail the new
  test; the cleanup moved after `newApp` fails
  `TestMainSilencesLeftoversOnlyWhenItPlaysHere`; `run` without the call
  passes every test, as it needs a terminal (checked by reading).
- Verification, snapshot round 2 (`/tmp/opencode/review-36c`, on part
  35's second round), every Go command inside the bwrap sandbox with
  `iso.env`: gofmt, vet, the build and `git diff --check`; `go test ./...`
  (444 top-level tests pass and 3 skip); `-race -count=3` on 97 sound, web
  and headless tests; the 7 new tests, with `TestHostAudioSilent` and
  `TestDisplaySinkIsWiredForEveryMode` next to them, 20 times and 5 times
  under `-race`; with the base's `app.go`, `audio.go`, `main.go`,
  `stream.go` and `web.go` laid over (and a shim for what the new tests
  name), all 7 new tests fail; the mutants above fail the same tests, and
  `run` without the call passes; the docs checker finds 43 links in three
  files and no problem, and the docs tests pass; the whole module, in the
  desktop-like sandbox, connects to none of its decoy sockets, while the
  positive control reaches two.
- Left alone: the silent sink of browser-only sessions; no line when a
  peer target grants no sound; raw device text in start errors
  (`serverOutputNote`), in `endSession`'s peer line and in peer notices;
  `audioSink.err` read without its lock while `writePCM16` may set it
  (all before this part). A device that connects its sound socket and
  then turns the sound off (codec id 0: Android 10 over adb, or the
  playback source before Android 13) counts as playing here, since main
  decides at start: it stops the other scterms' sound and opens its sink.
  After an audio config error (codec id 1), or a sound stream that ends
  while the video goes on, `status.audio` stays `on` and the terminal row
  keeps `vol N%`. In `web/check.mjs`, "browser only (host sink stays
  silent)" also covers a failed host sink with `--audio-dup`.

## Update (2026-10-21: QoL overhaul, part 35: terminal screenshots at the stream's resolution)

- Behaviour: Alt+S, the Shot button and the menu row save the device's
  picture at the stream's resolution as a PNG,
  `scterm-YYYYMMDD-HHMMSS.png` in the current directory, as the browser
  does. A taken name gets `-2`, `-3` and so on (`O_EXCL`: a file is never
  replaced). The run loop asks the video goroutine, which owns the
  decoder (`requestShot`, `stream.go`). That goroutine scales the frame
  its decoder holds, at the frame's own size, into a fresh buffer
  (`answerShots`). The PNG is encoded at the default level and written on
  a goroutine of its own (`saveShot`, `writeShotPNG`).
- A still screen sends no frames: AOSP repeats the last frame at most 10
  times, 100 ms apart (`GraphicBufferSource`, `kRepeatLastFrameCount`),
  and the MediaFormat docs say "(once)". So the request also sets a past
  read deadline on the video socket. The read returns; the video
  goroutine clears the deadline, then answers from the decoded frame, and
  reads on (`shotReader`; crypto/tls keeps a timed-out read usable, which
  a scratch test checked). Cleared after the answer, the deadline of a
  request made meanwhile could be cleared too, and that request would
  wait for the next frame. Requests are also answered after each scaled
  frame. If no answer comes within `shotWait` (1 s), the canvas shown at
  the press is saved instead, cut to the picture (`fitPicture`, taken out
  of `updateGeometry`). A shot still waiting when the video stream ends,
  or answered after the terminal UI ended, is dropped silently, and no
  goroutine is left. The brief assumed that a still screen keeps sending
  frames; the builder found that it does not and added the wake, which
  was kept: without it, most shots of a still screen would wait 1 s and
  save the terminal-sized picture.
- Messages: `scterm: saved FILE (WxH)`. The fallback adds "at the
  terminal's size, because no frame came from the device in time".
  `scterm: screenshot: no frame yet` stays (no frame decoded, or no
  usable canvas at the end of the wait), as does
  `scterm: screenshot: ERROR`. A session with no video socket
  (`--video=false`, or a peer without video) says
  `scterm: screenshot: the video is off`.
- Tests: `screenshot_test.go` runs the real decoder on
  `testdata/shot-1280x2800.h264` (1167 bytes; the ffmpeg command is in
  the file and makes the same bytes with FFmpeg 9.0.2 and x264
  0.165.r3222). It covers a still screen, the next frame while the saver
  waits (the decoder reads on, no file yet), the canvas fallback pixel for
  pixel, video off, the stream ending with a shot waiting, a quit before
  the frame, two shots in one second, and the frame's own size against
  the session packet. Of `keybind_test.go`'s five screenshot tests, only
  `TestScreenshotWritesPNG` (was `TestScreenshotWritesPPM`) and
  `TestScreenshotKeepsColours` decode a PNG; `TestScreenshotHeadless`,
  `TestScreenshotNoFrameYet` and `TestScreenshotStaleCanvas` check that
  no file is written and what the notice says.
  `TestScreenshotNameTaken` and `TestDumpFrameKeepsColours` are new.
- Verification, in the builder's tree (`184e8e4`: `141cd3e` plus parts 29,
  30, 31 and 32 as they were then), with `iso.env`: `gofmt -l` clean, `go
  vet ./...`, `go build`. `go test -count=1 ./...`: 412 pass, 3 skip, 0
  fail across packages (root 385/1; the base had 402/3, root 375/1).
  `-race -count=3`: the screenshot, restore, mnemonic and fuzz tests 57/57,
  and `TestDisplaySink|TestAppFrameSink|TestDump|TestScreenshot` 30/30.
  The 15 screenshot tests: `-count=20` 300/300, `-race -count=5` 75/75.
  The docs checker reports 0 problems. On the base files (overlay plus a
  shim), 10 of the 15 fail and the 5 that pass are unchanged behaviour.
  Each of ten mutations fails a test: no full-size scale, PNG on the video
  goroutine, no suffix, no fallback, a request left blocking, no wake, the
  session size, no drop after a quit, a red/blue swap, an uncropped
  canvas. A 1280x2800 PNG takes 74 ms (screen-like) or 241 ms (noise) on
  a 7800X3D. A shot costs the video goroutine about 1 ms.
- Taken into the worktree: the code, test and fixture files as built;
  README.md and docs/ARCHITECTURE.md merged three ways onto parts 33 and
  34's lines (each merge added exactly this part's lines).
- Verification, snapshot (`/tmp/opencode/review-35`, on part 34's
  snapshot), with `iso.env`: gofmt, vet, the build and `git diff --check`;
  `go test ./...` (427 top-level tests pass and 3 skip); `-race -count=3`
  on 19 tests: TestScreenshot Headless, NoFrameYet, WritesPNG,
  KeepsColours, NameTaken and StaleCanvas; TestDumpFrameKeepsColours;
  round 1's eight TestShot tests; TestDumpAudioWritesThePacketsAsSent,
  TestInputFuzz, TestMnemonicLocalOps and
  TestRestoreWhileAScreenshotIsTaken (the pattern named TestDisplaySink
  and TestAppFrameSink exactly, which match no test; the reviewer ran
  TestDisplaySinkIsWiredForEveryMode and TestAppFrameSinkNeverBlocks with
  `-race -count=3`: 6 of 6 pass); the 15 screenshot and dump tests (11
  new, 4 changed) 20
  times and 5 times under `-race`; with the base's `stream.go`, `input.go`
  and `util.go` and the builder's shim laid over, 10 of the 15 fail and
  the same 5 as in the builder's run pass; the docs checker finds 33 links
  and no problem in README.md and docs/ARCHITECTURE.md, and the docs tests
  pass; and the whole module, run in a bwrap sandbox set up like a desktop
  session (decoy sockets for the sound server, Wayland, D-Bus and X, adb's
  port 5037, and the real `pactl` on PATH), connects to none of them.
- Left alone: `--dump-frames` (PPM), the browser's screenshot, and Alt+S
  in `--web`/`--window` (no TUI, so nothing happens). No change to
  `app.go`, `decode.go` or `native.c`. Moving only the PNG encoding onto
  the video goroutine, with the file written elsewhere, would pass every
  test, and a process that exits during the PNG write can leave a partial
  file.
- Not run: the real phone, adb and a real terminal. The still-screen
  premise comes from AOSP source, not from the phone. Over TLS the tree
  tests only TLS 1.2's data returned with the timeout; the reviewer's
  probes, not in the tree, checked wakes mid-record over TLS 1.2 and 1.3.
- Review round 1 (`ses_ea21e81fbffeCdOd3ITBVnH512`, snapshot
  `/tmp/opencode/review-35`): NEEDS CHANGES. The code held under every
  probe: TCP mid-packet, TLS 1.2 and 1.3 mid-record, TLS 1.2 data that
  comes with the timeout, about 9,000 staggered requests with none
  lost, no deadline left set, the drops at session end and after a
  quit, `O_EXCL` names and the fallback crop. LOW 1: three sentences of
  this entry were untrue (corrected above). LOW 2: the wake's two
  orderings and the TLS 1.2 data were not tested in the tree. The wake
  before the clear (MA) lost 1 to 7 of 1280 requests in 4 of 5 probe
  runs yet passed the suite, as did the deadline set before the request
  is registered (MB) and data dropped when it comes with the timeout
  (MD). LOW 3: Alt+S held down had no memory bound, one shot (30 MB
  allocated) per key repeat; 30 presses 33 ms apart peaked at about 74
  MB (flat picture), 492 MB (noise, 16 threads) and 847 MB (noise, 4
  threads) of extra heap.
- Round 2: one shot at a time. A press while a shot is in flight, from
  its press until its file is written or it is dropped, takes no shot
  of its own: `takeShot` returns that shot's channel, writes no file
  and posts no notice, so the shot's own "saved" line is the only one.
  The run loop keeps the last shot's channel in `streamState.shotDone`;
  the shot's goroutine closes it, and the next press then takes a new
  shot. The check comes before the UI lock, so a joined press costs
  nothing. A session with no video socket now says `scterm: screenshot:
  the video is off`. Comments: `requestShot` must register the request
  before it sets the deadline; `shotReader` takes every read-deadline
  expiry on the video socket for a wake, so a liveness deadline there
  would be silently defeated; why the clear comes before the wake. New
  tests: `TestShotPressesJoinTheShotInFlight` (presses while the shot
  waits for its frame and while it waits to be saved: one file, one
  notice; a press after it: a second file), and in
  `screenshot_wake_test.go` `TestShotRequestsStaggeredWhileIdle` (60
  pairs of requests 0 to 1.5 ms apart while the device is idle; MA),
  `TestShotRegisteredBeforeItsDeadline` (the request is held once its
  deadline is set, until the wake has looked and read again; MB,
  deterministic) and `TestShotWakeKeepsTheDataThatCameWithIt` (TLS 1.2
  on `net.Pipe`: the last record and two bytes of a close_notify in one
  write; MD, deterministic). `TestShotsInTheSameSecond` takes its
  second shot once the first is written (still `-2`), and
  `TestShotWithTheVideoOff` expects the new notice. README.md and
  docs/ARCHITECTURE.md say that a press while a shot is being taken or
  saved takes none.
- Verification, round 2 (builder's tree `de417ce`: `9598a59` plus parts
  33, 34 and 35 round 1; Go 1.27.1, `iso.env`, bwrap with no network):
  `gofmt -l` clean, `go vet ./...`, `go build`, `git diff --check`.
  `go test -count=1 -v ./...`: 437 pass, 3 skip, 0 fail (root 409/1,
  internal/release 5, peer 23/2; `de417ce` gave 433/3). `-race
  -count=3`: 111 of 111 on 37 tests, by name: TestScreenshot Headless,
  NoFrameYet, WritesPNG, KeepsColours, NameTaken and StaleCanvas;
  TestShot OfAStillScreen, WaitsForTheNextFrame, FallsBackToTheCanvas,
  WithTheVideoOff, DroppedWhenTheSessionEnds,
  DroppedAfterTheTerminalIsGivenBack, PressesJoinTheShotInFlight,
  IsTheFramesOwnSize, RequestsStaggeredWhileIdle,
  RegisteredBeforeItsDeadline and WakeKeepsTheDataThatCameWithIt;
  TestShotsInTheSameSecond; TestRestore EndsAnUnfinishedFrame,
  WaitsForTheFrameBeingWritten, TakesAFreeLockAtOnce, LetsGoOfALateLock,
  WhileThePainterRuns and WhileAScreenshotIsTaken; TestMnemonic KeyWire,
  UppercaseIsTheSameChord, ActionWire, LocalOps, MenuOpensNotMenuKey,
  OpsMatchTheBrowser and EntriesAreDispatchable; TestInputFuzz,
  TestInputFuzzSplitReads, TestDumpFrameKeepsColours,
  TestDumpAudioWritesThePacketsAsSent,
  TestDisplaySinkIsWiredForEveryMode and TestAppFrameSinkNeverBlocks.
  The 19 screenshot tests: `-count=20` 380 of 380, `-race -count=5` 95
  of 95. The six new and changed tests: `-count=20` 120 of 120, `-race
  -count=5` 30 of 30, `-cpu=1,2,4 -count=5` 90 of 90; under `-race` the
  three wake tests take at most 0.31 s, the coalescing test 3.4 s and
  `TestShotsInTheSameSecond` 4.2 s (a 1280x2800 PNG takes 1.5 to 2 s
  under `-race`). With round 1's `input.go`, `stream.go` and `util.go`
  laid over, 2 of the 19 fail (`TestShotPressesJoinTheShotInFlight`,
  `TestShotWithTheVideoOff`); round 1's tests on round 2's code fail 2
  of 15 (`TestShotsInTheSameSecond`, `TestShotWithTheVideoOff`).
  Mutants, by overlay: all 17 caught. Round 1's ten, with the same
  edits, each fail a test. MA fails the stress test in 20 of 20 runs
  and 10 of 10 under `-race`, by pair 4 of 60; MB fails the held-request
  test 20 of 20 and 10 of 10; MB with 1 ms between deadline and
  registration fails 5 tests; MD fails the TLS test 20 of 20 and 10 of
  10; no coalescing, and joining only while the answer is pending, fail
  `TestShotPressesJoinTheShotInFlight`; the old video-off notice fails
  `TestShotWithTheVideoOff`. Held Alt+S, the review's 30 presses 33 ms
  apart at 1280x2800 made through `takeShot` (a stand-in answers as the
  video goroutine does), peak extra heap over three runs: round 1 flat
  104 MB, noise 845 to 950 MB (16 threads) and 1098 to 1288 MB (4
  threads), 30 files each; round 2 flat 60 to 74 MB (18 to 22 files),
  noise 147 MB at either thread count (4 files). One noise shot alone
  peaks at 61 to 72 MB, and with `GOGC=25` the round 2 burst peaks at 76
  to 90 MB: what is above one shot is garbage not yet collected. The
  reviewer's probes pass on round 2 (640 staggered requests, none lost).
- Round 2 taken into the worktree, where part 36 had been taken in
  already: `input.go`, `screenshot_test.go` and the new
  `screenshot_wake_test.go` as built; `stream.go`, README.md and
  docs/ARCHITECTURE.md merged three ways (`git merge-file`), each adding
  exactly round 2's changed lines. The worktree then passed
  `go test ./...` (443 pass, 3 skip) with `iso.env` in bwrap, and no
  stray PNG was left in it.

## Update (2026-10-21: QoL overhaul, part 34: the web port is checked before the device)

- With `--web` or `--window`, also with `--peer`, main now listens on the
  browser display's address and port right after the flags are checked
  and the version line is printed, before any device or peer work: before
  `findDevice`, `newSession` and `start`, and before `newPeerSession`, its
  identity and its chooser. A port it cannot use ends the run there, with
  one line and exit status 1, where it used to end it only once the device
  had started. The display then serves that listener: main sets the web
  server's `ln` after `newApp`, and `run()` serves `s.ln` when it is set,
  and listens itself, through the `webListen` seam, only when it is not.
  `--web-port 0` still prints and opens the port the kernel picked, and
  `allowedNames` reads the served listener. Runs without `--web` or
  `--window` are unchanged. `listenWeb` (web.go) opens the listener and
  `webListenError` says why it cannot.
- Messages, after `scterm dev`: `scterm: web: port 6969 on 127.0.0.1 is
  in use: stop the program that uses it, or choose another port with
  --web-port PORT (0 picks a free one)` (EADDRINUSE, in scterm doctor's
  words, now shared through `webPortInUse` and `webPortAnother`);
  `scterm: web: port 80 on 127.0.0.1 needs privileges that scterm does not
  have: choose another port with --web-port PORT (0 picks a free one)`
  (EACCES); `scterm: web: 192.0.2.1 is not an address of this computer:
  --web-addr takes one of its addresses, 0.0.0.0 for all of them, or the
  default, 127.0.0.1` (EADDRNOTAVAIL); anything else keeps
  `web: listen on ADDR:PORT: ERR`. scterm doctor's output is unchanged.
- Tests: `TestWebPortIsCheckedBeforeTheDevice` (exit_test.go) runs main
  in a child process, through the `SCTERM_TEST_MAIN_ARGS` entry, with a
  fake adb that notes its calls and a scratch HOME, SCTERM_HOME and
  XDG_RUNTIME_DIR. With the port held, `--web`, `--window` and
  `--peer SOMENAME --web` exit 1 with the in-use line, run no adb and
  leave SCTERM_HOME empty. With it free, the run reaches `adb devices`,
  holds the port while the fake adb waits at a FIFO, and fails with
  today's "no device found" line. The child runs in its own process
  group, killed after 30 s. `TestWebServesTheListenerItIsGiven`
  (web_test.go): `run()` given a listener serves it, never calls
  `webListen`, prints its port and serves the host name its address
  allows. `TestWebPortErrorsSayWhatToDo` (new web_port_test.go): each
  message, from real errors (a held port, 192.0.2.1, port 1023 when not
  root and `ip_unprivileged_port_start` is above it, port 70000) and from
  built ones.
- Verification, in the builder's tree (`/tmp/opencode/dev-34`, base
  7e00821, under dev-29's `iso.env`): gofmt and `go vet ./...` clean,
  `go build` ok, `go test -count=1 ./...` 405 top-level tests pass and 3
  skip (378 and 1 in the root package; the base gives 402 and 3);
  `-race -count=5` on the new tests with `TestDoctorChecksTheWebPort` and
  `TestCommandLineExitStatus`, `-race -count=3` on
  `TestWeb|TestCommandLine|TestSession` and on `TestDoctor`, and the new
  tests `-count=20` (60 of 60) pass. The Markdown check of README.md and
  docs/ARCHITECTURE.md finds 33 links and no problem. Against the base's
  main.go, web.go, app.go and doctor.go (`go test -overlay`) the first two
  new tests fail as they should (the old late errors, adb run, an identity
  written, the port free during the device lookup, `run()` listening
  again), and web_port_test.go does not build (`undefined: listenWeb`).
  11 of 11 mutations are caught: the early check removed, moved after
  `findDevice`, skipped for `--window` or for `--peer`, a check that
  closes the port again, `run()` listening again or not reading the given
  port, the names ignoring its address, the in-use hint wrong, the shared
  words changed, and EACCES and EADDRNOTAVAIL swapped.
- Taken into the worktree: the files as built, with the README merged
  three ways onto part 33's lines (the merge changed exactly this part's
  lines). Part 30's third round was then merged into the worktree's
  `doctor.go`, adding exactly round 3's lines; this part's lines there are
  unchanged.
- Verification, snapshot (`/tmp/opencode/review-34`, on part 33's
  snapshot), with `iso.env`: gofmt, vet, the build and `git diff --check`;
  `go test ./...` (417 top-level tests pass and 3 skip); `-race -count=3`
  on the 84 `TestWeb*`, `TestCommandLine*`, `TestSession*` and
  `TestDoctor*` tests; the 3 new tests with `TestCommandLineExitStatus`,
  `TestWebAddrNameIsServed` and `TestDoctorChecksTheWebPort` 20 times and
  5 times under `-race`; with the base's `main.go`, `web.go` and
  `doctor.go` laid over and `web_port_test.go` left out (it does not build
  there), `TestWebPortIsCheckedBeforeTheDevice` and
  `TestWebServesTheListenerItIsGiven` fail; the docs checker finds 33
  links and no problem in README.md and docs/ARCHITECTURE.md, and the docs
  tests pass; and the whole module, in the desktop-like bwrap sandbox
  described in part 30's entry, connects to none of its decoy sockets.
- Left alone: app.go (no change needed); scterm doctor's wording for other
  errors (it checks only 127.0.0.1:6969); `validateConfig` still does not
  range-check `--web-port`, so `--web-port 70000` now fails early, with
  today's text. A browser that connects during device start-up, such as a
  page left open that reconnects, now waits in the accept queue and is
  served once the display starts, where it was refused and retried
  (`TestWebRequestDuringStartUpIsServed` shows it with a raw request;
  `player.js` has no connect timeout; not tried in a browser).
- Review round 1 (NEEDS CHANGES), resolved in round 2. MEDIUM: no test ran
  main's two new lines, so main handing its listener to the web server
  (`app.web.ln = webLn`) or passing `--web-addr` to `listenWeb` could each
  go with the whole package passing; and the reason this entry gave, the
  safety rules, did not hold, since the loopback fake peer target takes
  main past start-up without a device. NIT: the README promised what to do
  for any port scterm cannot listen on, though only the three named causes
  get advice. Suggestions: the queued-request and inheritance checks, a log
  line for the skipped 192.0.2.1 case, and the checks inside bwrap.
- Round 2, tests (web_port_test.go): `TestWebServesThePortMainHeld` runs
  main in a child process with `--peer "Pixel 8" --web --video=false
  --audio=false` against the loopback fake target, paired in a scratch
  SCTERM_HOME, with PULSE_SERVER naming no server and autospawn off, and
  fetches the page from the address main prints: on a fixed port, with
  `--web-port 0`, and with `--web-addr 127.0.0.2` while the same port is
  held on 127.0.0.1. `TestWebRequestDuringStartUpIsServed` connects and
  asks while the target holds back its welcome: no answer for 700 ms, then
  the page. `TestWebListenerIsNotInheritedByADB` reads the fake adb's open
  files while main holds the port: no socket (handed one on purpose, it
  lists one). `startMain` and the new `startMainLines` share `mainCommand`
  (exit_test.go), and `TestWebPortErrorsSayWhatToDo` logs when the
  192.0.2.1 case is built only. The README now says that the line says
  why, and what to do for the causes it lists.
- Round 2 also fixes a data race the new test found under `-race`, older
  than this part: `run()` set `s.http` on its goroutine while `stopWith`,
  on the app loop's, read it and shut it down, ordered by nothing when a
  signal ended the session (the in-process tests stop through a channel
  after `ready`, which orders the two). Under the race detector each such
  session exited with status 66. Before this part, `run()` set `s.ln` the
  same way. Now `run()` builds the server, publishes it and the listener
  under `srvMu`, and gives up, closing the listener and printing nothing,
  when a stop has begun; `stopWith` reads both under `srvMu`.
  `TestWebStopBeforeRunServesNothing` and `TestWebStopFromAnotherGoroutine`
  (web_test.go) pin it, the second with stops from at once to about 2 ms
  in, for the race detector.
- Verification, round 2, in the worktree, each Go command inside the bwrap
  sandbox with `iso.env`: gofmt and vet; the 5 new tests, also 20 times and
  5 times under `-race`, and `startMain`'s callers; `-race -count=3` on the
  browser-server tests; mutants, each failing a test: main not handing over
  its listener (the fixed-port and 127.0.0.2 cases and the request test),
  main ignoring `--web-addr` (the 127.0.0.2 case), the listener inherited
  by adb (close-on-exec cleared), `stopWith` without `srvMu` (under
  `-race`: the child-process test, and the in-process one in 5 of 5 runs),
  and a `run()` that serves after a stop; round 1's `web.go` fails the
  stop-before-run test, and under `-race` both stop tests and the
  child-process test (all three of its cases); with the base's `main.go`,
  `web.go` and `doctor.go` laid over, the request test (main never
  listened during start-up) and the stop-before-run test fail, and the
  other three pass. These runs, except the five in-process runs of
  `stopWith` without `srvMu`, came before the last edit of
  `TestWebStopFromAnotherGoroutine`, the one that spreads its stops over
  about 2 ms (before that edit, the test did not catch the missing lock);
  the snapshot checks below, and the review's mutant runs, used the final
  tests.
- Verification, snapshot round 2 (`/tmp/opencode/review-34b`, on part 33's
  second round), each Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (423
  top-level tests pass and 3 skip); `-race -count=3` on the 89 `TestWeb*`,
  `TestCommandLine*`, `TestSession*` and `TestDoctor*` tests; the 8 new
  tests, with `TestCommandLineExitStatus` and
  `TestSessionEndExitsWithItsStatus` next to them, 20 times and 5 times
  under `-race`; with the base's `main.go`, `web.go` and `doctor.go` laid
  over and `web_port_test.go` cut to the new tests, the early-check,
  request and stop-before-run tests fail and the other three pass; round
  1's `web.go` fails the stop-before-run test, and under `-race` that
  test, the in-process stop test and the child-process test; the docs
  checker finds 33 links and no problem, and the docs tests pass; the
  whole module, in the desktop-like sandbox, connects to none of its
  decoys, which the positive control reaches twice.
- Review round 2 (PASS), with two nits. First, this entry said round 1's
  `web.go` fails both stop tests without `-race`, where only the
  stop-before-run test does; round 3 corrects that above and adds the
  snapshot's checks. Second, no test pins `run()` closing its own listener
  when it gives up (only in-process tests listen themselves; left for
  later). Also left for later: a stop that lands between `run()`
  publishing its server and `openWindow` can still open a `--window`
  browser after `killWindow` ran (older than this part), so this part's
  comment "Once a stop has begun, run serves nothing" says more than the
  code does; the web tests' race checks run only by hand (a read or
  publish without `srvMu` passes every plain run); and this part's
  child-process peer tests, if one could not reach its fake target, would
  run a real one-second presence search on the LAN.
- Round 3 changes only this entry, and moves the part unchanged onto part
  33's third round.
- Verification, snapshot round 3 (`/tmp/opencode/review-34c`, on part 33's
  third round): the same checks as for round 2's snapshot gave the same
  results.
- Review round 3 (NEEDS CHANGES), on this entry only: the note that the
  round-2 runs came before the last test edit left out the five in-process
  runs of `stopWith` without `srvMu`, made after it; the presence-search
  item was listed as older than this part, though the child-process peer
  tests are this part's, as is the comment the window item quotes; and the
  round-2 snapshot bullet called the 8 new tests and their two neighbours
  10 new and changed tests. All three are corrected above.
- Round 4 changes only this entry, and moves the part unchanged onto part
  33's fourth round.
- Not run: scterm against a device, a real browser or a window (safety
  rules). Main runs past start-up only in peer mode, against the loopback
  fake target, so the adb path past the device lookup is checked by
  reading (the hand-off is one line after both paths). The node suites
  were not run: web/ is unchanged.

## Update (2026-10-21: QoL overhaul, part 33: scterm update follow-ups)

- `scterm update` keeps the previous binary as `<path>.old` before it prints
  "Updated …": with stdout closed (`scterm update -y | head -1`) the first
  line printed after the download ends the process by SIGPIPE, which used to
  leave the previous binary only as a hidden `.scterm-old-*` file (part 24's
  review, L1). The lines and their order are unchanged.
- `scterm update --check` on a newer release without a linux/amd64 binary
  says "scterm 1.3.0 has been released (you have 1.2.3), but it has no
  linux/amd64 binary to update to." and exits 0; it failed with "release
  1.3.0 has no Linux binary" (exit 1), as `scterm update` still does (L3).
- Release notes lose whole escape sequences, not just their control
  characters: "\x1b[31mred\x1b[0m" shows as "red", not "[31mred[0m" (N4).
  A CSI (ESC [ or U+009B) goes with the bytes 0x20-0x3F after it and a final
  byte 0x40-0x7E; a control string (ECMA-48: OSC, ESC ] or U+009D; DCS, ESC
  P or U+0090; SOS, ESC X or U+0098; PM, ESC ^ or U+009E; APC, ESC _ or
  U+009F) with its text up to ESC or ST (U+009C), and an OSC also up to BEL,
  as xterm takes it; any other ESC with the bytes 0x20-0x2F after it and a
  final byte 0x30-0x7E (ECMA-35: ESC ( B, ESC 7). A sequence ends at the
  first character that cannot be part of it, and a control string with no
  end at the end of its line, so an unfinished one hides nothing past its
  line. Tabs and newlines stay; CR and other control characters still go.
- SIGINT, SIGTERM and SIGHUP from the download on no longer leave files:
  the download, a copy of the old binary and a binary left in TMPDIR for
  sudo are removed, the binary and any `.old` stay as they were, and then
  the signal is raised again at its default, so the shell still sees 128
  plus its number. The swap is the point of no return: a signal after it
  waits until the old binary is `.old` (or named) and the lines are
  printed. Ctrl+C at the question still ends update at once. SIGHUP or
  SIGINT that the process started out ignoring (under nohup, or in a
  script's background job) stays ignored; SIGTERM does not, since Go's
  runtime takes it over at start even when it was ignored, so update catches
  it then too (before this part, an ignored SIGTERM still ended update, and
  left the download, as the review saw). The new binary's version check is
  killed when update is interrupted (`checkRuns` takes a context);
  `release.Download` already removed its file when its context ended, and a
  test now holds that.
- `update -h` and the README say the binary is checked against the digest
  and the size GitHub publishes (N1); they said the size was always
  checked. The README's Updating section also says that an update stops when
  no copy of the binary can be made, what an interruption does, and what
  `--check` says without a Linux binary. `scterm -h` lists `update [--check]
  [-y]` (N3).
- New tests run update in a child process (`SCTERM_TEST_UPDATE`, listed in
  `docs/DEVELOPMENT.md`) and send it real signals; they start the child
  with SIGINT and SIGHUP at their defaults, since `signal.Ignored` stops
  telling the truth once `Notify` has caught a signal. Part 24's review gaps
  are covered: a partial copy is removed (at a file size limit), the real
  stderr check (a file and a pipe are not a terminal, a pseudo-terminal is),
  and "…" alone when cut notes have no release page.
- Verification, in the builder's tree (`/tmp/opencode/dev-33`, base
  `b30c8ac`: `141cd3e` plus parts 29, 30, 31 and 32 as they were then):
  gofmt, `go vet ./...`, the build, `go test -count=1 ./...` (413 pass, 3
  skip, with the proposed docs and help applied in a copy; without them
  `TestDevelopmentNamesTheTestVariables` fails, as docs/DEVELOPMENT.md did
  not yet name `SCTERM_TEST_UPDATE`), `-race` on the update and release
  tests, the 13 new and changed tests 20 times and 5 times under `-race`.
  Against the old `update.go` with only its signature seams, 6 of them
  fail for their reasons; 7 pin older behaviour or part 24's gaps and pass
  there. 27 mutants, one change each: 25 caught; `signal.Reset` in `die`
  is redundant after `signal.Stop` (equivalent), and the removal of a
  staged binary on a late signal is reasoned, not tested. Not run: a real
  terminal's Ctrl+C (signals went to the child with kill), nohup itself, a
  full disk, GitHub, CI.
- Taken into the worktree: the code and test files as built; the
  builder's proposed texts for `flags.go`'s Commands line,
  `docs/DEVELOPMENT.md`'s list and the README's Updating section were
  applied by hand, after which all seven files equal the builder's
  proposed copies.
- Verification, snapshot (`/tmp/opencode/review-33`, on part 30's
  snapshot), with `iso.env`: gofmt, vet, the build and `git diff --check`;
  `go test ./...` (414 top-level tests pass and 3 skip); `-race -count=3`
  on 29 update, help and docs tests; 16 update and release tests (the 13
  new and changed ones and 3 unchanged ones next to them) 20 times and 5
  times under `-race`; with the builder's old `update.go` laid over, the
  same 6 tests fail as in its run (stdout closed, both interruptions, an
  interrupted swap, `--check` without a Linux binary, the notes' escape
  sequences), and the other 6 of the root package's 12 new and changed
  tests pass; the docs checker finds 41 links and no problem in README.md
  and docs/DEVELOPMENT.md, and the docs tests pass; and the whole module,
  in the desktop-like bwrap sandbox described in part 30's entry, connects
  to none of its decoy sockets.
- Review round 1 (NEEDS CHANGES), resolved in round 2. LOW: DCS, SOS, PM
  and APC kept their text, since only an OSC was taken as a string, so N4
  was met for OSC alone; the sentence on ignored signals was untrue for
  SIGTERM, here and in a comment; and no test held the swap as the point of
  no return (a check of the interruption after the swap passed the whole
  package and left L1's state). NITs: the fake server and the test both
  waited 30 s, so a download the interruption cannot end failed the test
  only because the test's timer won; and three ECMA-35 range ends were
  unpinned. Suggestions: an expectation for the cut notes that does not use
  `maxNoteLines`; the cosmetic cases below; the checks inside bwrap.
- Round 2: DCS, SOS, PM and APC end as an OSC does, in both forms, but not
  at BEL, which xterm takes as the end of an OSC only. `TestNoteLines`
  gained rows for the four in both forms, a sixel image, a BEL inside a DCS,
  an unfinished DCS and the ECMA-35 range ends. The new
  `TestSwapIsThePointOfNoReturn` gives `replaceBinary` a context meant to
  end just after the check before the swap (round 3 made it end, below),
  and wants the binary updated, the previous one kept as `.old` and both
  lines printed. The download test's
  server waits 10 minutes, so only the interruption can end the download
  within the test's 30 s. `TestUpdateCutsTheNotesWithoutAReleasePage`
  expects 15 lines by number. The comments on ignored signals in `update.go`
  and `update_process_test.go` name SIGHUP and SIGINT.
- Verification, round 2, in the worktree, each Go command inside the bwrap
  sandbox with `iso.env`: gofmt and vet; the five tests round 2 added or
  changed (one only in a comment) and
  `TestInterruptedSwapLeavesEverythingAsItWas` next to them, also 20 times
  and 5 times under `-race`; with round 1's `update.go` laid over, the five
  new control-string rows fail and the rest pass (the range ends were right,
  and the swap held); and eight mutants each fail a test: BEL ending every
  control string, ESC P or U+009F left out, each of the three ECMA-35 range
  ends, a check of the interruption after the swap through `Err` (in
  `TestSwapIsThePointOfNoReturn`), and a download the interruption cannot
  end (in the download test, after its 30 s wait).
- Review round 2 (NEEDS CHANGES), resolved in round 3. LOW: round 2's
  stand-in for an interruption (`flipCtx`) changed only what `Err` said;
  its `Done` was `context.Background()`'s, which never closes, where a real
  interruption closes `Done` as `Err` turns. So a check after the swap
  written with `Done` passed the whole module and left the previous binary
  under a hidden name, L1's state again. NIT: the round-2 bullet above said
  "the 6 changed tests". The review found round 1's other findings
  resolved: the control-string rule matched an independent model on
  2,000,000 random inputs and xterm's man page, the sentences on ignored
  signals matched Go's runtime, and with a download the interruption cannot
  end, the test now fails at its own 30 s wait. Its suggestions are taken:
  the fail-before figures below, one more case under Left alone, and "as
  xterm takes it" in `plainText`'s comment.
- Round 3: `flipCtx` cancelled a real context at the first call to `Err`,
  so from then on `Done` was closed and `Err` said so, together, and
  `TestSwapIsThePointOfNoReturn` also failed when `replaceBinary` never
  called `Err`. `update.go` changed only in that comment.
- Verification, round 3, in the worktree (parts 33 to 36, uncommitted),
  each Go command inside the bwrap sandbox with `iso.env`: gofmt and vet;
  the swap test 20 times and 5 times under `-race`; the whole module, 443
  pass and 3 skip. Mutants laid over `update.go`: a check after the swap
  through `Done` fails the swap test, and passes round 2's test, as the
  review found; the same check through `Err` fails it too; no check before
  the swap fails both swap tests. Against the `update.go` from before this
  part (part 24's, with only its signature seams), 7 of the 13 root-package
  tests this part adds or changes fail: the builder's 6, and now
  `TestSwapIsThePointOfNoReturn`, since the old code never checks for an
  interruption, so none comes (round 2's version of that test passed there;
  the builder's 13 had the release package's
  `TestDownloadCancelledLeavesNothing` in its place). Against round 1's
  `update.go`, only `TestNoteLines` fails.
- Verification, snapshot round 3 (`/tmp/opencode/review-33c`, on
  `9598a59`), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (415
  top-level tests pass and 3 skip); `-race -count=3` on 33 update, help and
  docs tests; the 17 update and release tests (14 new or changed, and 3
  unchanged ones next to them) 20 times and 5 times under `-race`; the
  fail-before runs and the three mutants as above, and the `Done` mutant
  passing round 2's test; the docs checker finds 41 links and no problem,
  and the docs tests pass; the whole module, in the desktop-like sandbox,
  connects to none of its decoy sockets, while the positive control
  reaches two.
- Review round 3 (NEEDS CHANGES): LOW: `flipCtx` ended at the first call
  to `Err`, not at the swap, so the test took it that `replaceBinary`
  checks once, through `Err`, just before the swap. A rewrite in two
  places, the check before the swap made through `Done` and an `Err` check
  added after the swap, passed the whole root package and left the
  previous binary under a hidden name. Correct rewrites, the check made
  through `Done` or an extra check before the copy, failed it with
  misleading messages. NITs: the round-3 bullet said the test failed when
  `replaceBinary` never checks before the swap (it failed when it never
  called `Err`), and the snapshot bullet counted 17 new and changed tests
  (14 are). Both are corrected above.
- Round 4: the review's stand-in, `swapCtx`, ends once the binary on disk
  is the new one, which only the swap makes it: a check before the swap
  finds it running, and a check after the swap made through its own `Err`
  or `Done` finds it ended (round 4's review found that a check through a
  channel, a derived context or an `AfterFunc` taken before the swap did
  not; round 5 replaces it, below). The test no longer asks that a check
  came: none after the swap is right, and none at all fails
  `TestInterruptedSwapLeavesEverythingAsItWas`. Only `update_test.go`
  changed.
- Verification, round 4, in the worktree (parts 33 to 36), each Go command
  inside the bwrap sandbox with `iso.env`: gofmt and vet; the two swap
  tests 20 times and 5 times under `-race`. Mutants laid over `update.go`
  with the new test: a check after the swap through `Done`, or through
  `Err`, fails the swap test; the check moved after the swap fails both
  swap tests; the check before the swap through `Done` with an `Err` check
  after the swap fails the swap test (round 3's test passed it, as the
  review found); no check at all fails only
  `TestInterruptedSwapLeavesEverythingAsItWas`; three correct rewrites
  pass both: the check before the swap through `Done`, or through
  `context.Cause`, and an extra `Err` check before the copy.
- Verification, snapshot round 4 (`/tmp/opencode/review-33d`, on
  `9598a59`), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `-race -count=3` on 33
  update, help and docs tests; the 17 update and release tests (14 new or
  changed, and 3 unchanged ones next to them) 20 times and 5 times under
  `-race`; against round 1's `update.go` only `TestNoteLines` fails, and
  against the one from before this part 6 of the 13 root-package tests
  fail, the builder's 6 (with no check at all the swap test now passes,
  and `TestInterruptedSwapLeavesEverythingAsItWas` catches it); the eight
  mutants as above, and the one with a check before the swap through
  `Done` and an `Err` check after it passing round 3's test; the docs
  checker finds 41 links and no problem, and the docs tests pass; the
  whole module, in the desktop-like sandbox, connects to none of its decoy
  sockets, while the positive control reaches two. The first
  `go test ./...` failed one test outside this part: three three-finger
  tap checks in `web/input_test.mjs` ("keyboard came up", "the field was
  focused", "second tap turns the keyboard off"), that suite taking about
  75 s while another review's sandbox kept the machine busy (part 34's
  third review saw three keyboard-tap checks there fail under load too).
  Run again once the load had dropped, `go test ./...` gave 415 top-level
  tests passing and 3 skipped, the browser suites taking about 1.3 s.
- Review round 4 (NEEDS CHANGES): LOW: `swapCtx` saw the swap only when
  `replaceBinary` later called its own `Err` or `Done`, so a check after
  the swap through a `Done` channel, a derived context or a
  `context.AfterFunc` taken before it passed both swap tests and the whole
  root package, and left the previous binary under a hidden name (two
  edits). LOW: before the swap, only an interruption that came before
  `replaceBinary` started was tested, so the one check moved to the start
  passed everything, and an interruption during the copy then replaced the
  binary and overwrote `exe.old`. It proposed a seam at the rename and a
  test with the binary as a FIFO.
- Round 5: `replaceBinary` renames through `rename`, a package variable
  holding `os.Rename`, and `TestSwapIsThePointOfNoReturn` ends a real
  context in the rename that puts the new binary in place, then pauses 20
  ms so that a function given to `context.AfterFunc` runs; a check before
  the swap finds it running and a check after finds it ended, however the
  check is made, as long as the swap goes through `rename` (round 6 made
  the test check that it does). New
  `TestInterruptedCopyLeavesEverythingAsItWas` makes the binary a FIFO, so
  the copy waits on the test, which interrupts before it lets the copy
  finish: the binary and `exe.old` stay as they were, and the download and
  the copy go. Its 10 s way out, for a copy that never opens the FIFO,
  worked only while the FIFO stayed at the binary's path (round 6 changed
  it). `swapCtx` is gone.
- Verification, round 5, in the worktree (parts 33 to 36), each Go command
  inside the bwrap sandbox with `iso.env`: gofmt and vet; the three tests
  that guard the swap 20 times and 5 times under `-race`, each at
  `-cpu=1,4`. Mutants laid over `update.go` with the new tests: every
  check after the swap (through `Err` or `Done`; through a `Done` channel
  or a derived context taken at the start, for that check alone or for
  both; through an `AfterFunc` registered at the start; and a check before
  the swap through `Done` with an `Err` check after it) fails the swap
  test, the `AfterFunc` one in 40 of 40 runs at `-cpu=1,4`; the check
  moved after the swap fails all three tests; no check, and renaming even
  when interrupted, fail the interrupted-swap and interrupted-copy tests;
  the one check moved to the start fails the interrupted-copy test; five
  correct rewrites pass all three: the check before the swap through
  `Done`, through `context.Cause`, or through a `Done` channel or a
  derived context taken at the start, and an extra `Err` check at the
  start. Round 4's tests pass the two new kinds of gap, as the review
  found. `go test ./...`: 445 top-level tests pass and 3 skip. With a test
  file that declares `rename` (which older code never calls), round 1's
  `update.go` fails only `TestNoteLines` of the 14 root-package tests this
  part adds or changes, and the one from before this part fails 7 of
  them: the builder's 6 and the interrupted-copy test.
- Verification, snapshot round 5 (`/tmp/opencode/review-33e`, on
  `9598a59`), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (416
  top-level tests pass and 3 skip); `-race -count=3` on 34 update, help
  and docs tests; the 18 update and release tests (15 new or changed, and
  3 unchanged ones next to them) 20 times and 5 times under `-race`; the
  fail-before runs and the seventeen mutants as above, and round 4's tests
  passing the review's two gaps; the docs checker finds 41 links and no
  problem, and the docs tests pass; the whole module, in the desktop-like
  sandbox, connects to none of its decoy sockets, while the positive
  control reaches two.
- Review round 5 (NEEDS CHANGES): LOW: the swap test never checked that
  its interruption came. A swap made with `os.Rename` rather than
  `rename`, with a check after it through `Err` or `Done`, passed all three
  tests that guard the swap and the whole root package, and would leave
  the previous binary under a hidden name. LOW: the interrupted-copy
  test's way out reopened the binary's path, not the FIFO, so a
  `replaceBinary` that renamed over the FIFO or moved it (copying the
  download instead of the binary, keeping the previous binary by a hard
  link, or renaming it aside) hung until go test's timeout. The review
  found round 4's findings resolved, the 20 ms pause needed (without it,
  two checks made on goroutines of their own were caught in none of 400
  runs; with 1 ms, in all 400), and the seam acceptable.
- Round 6 takes the review's tested fixes. The swap test fails unless the
  rename stand-in ended the context ("the new binary was not put in place
  through rename, so nothing interrupted it"). The interrupted-copy test
  opens the FIFO for writing without blocking, retrying while that fails
  with ENXIO (until the copy opens it for reading) for up to 10 s; it
  checks that what it opened is the FIFO, and that `exe.old` is still a
  plain file before reading it. Its comment now says it pins the copy: a
  previous binary kept by a hard link, also correct, cannot pass it. Only
  `update_test.go` changed.
- Verification, round 6, in a copy of the round-5 snapshot with the
  worktree's `update_test.go` (which differs from the review's tested fix
  file only in two comment lines), each Go command inside the bwrap sandbox
  with `iso.env`: gofmt and vet; the three tests that guard the swap 20
  times and 5 times under `-race`, each at `-cpu=1,4`. The seventeen
  mutants of round 5 end as before. Of the review's fifteen, the two swaps
  made with `os.Rename`, with a check after them through `Err` or `Done`,
  now fail the swap test (round 5's test passes both, as the review found);
  so does a correct swap made with `os.Rename` and no check after it, which
  round 6 means to refuse. Copying the download instead of the binary fails
  the interrupted-copy and swap tests in 1.8 s, and keeping the previous
  binary by a hard link or by renaming it aside fails the interrupted-copy
  test in 1.7 to 1.8 s, where those hung before; returning at the start
  fails all three in 12.1 s, through the 10 s way out; a copy that stops on
  the interruption passes all three, and the other wrong ones each fail a
  guard test. Of the 16 root-package update tests the fail-before runs
  pick, round 1's `update.go` now fails `TestNoteLines` and the swap test
  (that code has no `rename` to interrupt), and the one from before this
  part fails 8: round 5's 7 and the swap test. The whole module passes.
- Verification, snapshot round 6 (`/tmp/opencode/review-33f`, on
  `9598a59`), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (416
  top-level tests pass and 3 skip); `-race -count=3` on 34 update, help
  and docs tests; the 18 update and release tests 20 times and 5 times
  under `-race`; the fail-before runs as above; round 5's seventeen
  mutants and the review's fifteen, ending as in the round-6 bullet, and
  round 4's and round 5's tests passing the gaps their reviews found.
  Three of those overlay runs first failed to build, because something
  else trimmed the shared build cache in the middle of the run (Go then
  reported even `unicode` as missing from the standard library); run
  again, each ended as expected. The docs checker finds 41 links and no
  problem, and the docs tests pass; the whole module, in the desktop-like
  sandbox, connects to none of its decoy sockets, while the positive
  control reaches two.
- Review round 6 (NEEDS CHANGES): LOW: no test interrupted the swap with
  `.old` taken. One check after keeping the previous binary fails (`if
  keepErr != nil && intr.Err() != nil { return intr.Err() }`) passed the
  whole root package: it returns before printing anything, so the new
  binary is in place and the previous one only under a hidden name, with
  nothing said. NIT: the interrupted-copy test pins a copy that reads the
  binary to its end; one that copies only the size the file reports (0 for
  a FIFO) does not wait, and races the test (it failed 81 of 200 runs at
  `-cpu=1` and 1 of 200 at `-cpu=4`; round 5's version alike). The review
  found round 5's findings resolved and every claim of round 6's bullets
  true.
- Round 7: `TestSwapIsThePointOfNoReturnWhenOldIsTaken` interrupts the
  swap with `.old` a directory, and requires no error, the update and the
  hidden copy named on stdout, the line that says why it is not `.old` on
  stderr, and the previous binary in one hidden copy. The stand-in that
  interrupts at the swap is now `interruptAtSwap`, shared by both swap
  tests. The interrupted-copy test's comment says what it pins, and the
  comment on `rename` in `update.go` says the swap goes through it.
- Verification, round 7, in the worktree, each Go command inside the bwrap
  sandbox with `iso.env`: gofmt and vet; the four tests that guard the
  swap and the copy 20 times and 5 times under `-race`, each at
  `-cpu=1,4`. The review's mutant that returns when `.old` cannot be taken
  fails the new test (it printed nothing and returned "context canceled"),
  and passes round 6's tests; its two correct ones (the copy read whole
  with `io.ReadAll`, and `os.Rename` for the step that keeps `.old`) pass
  all four; leaving the copy behind on the error path fails both
  interruption tests. The root package passes.
- Verification, snapshot round 7 (`/tmp/opencode/review-33g`, on
  `9598a59`), every Go command inside the bwrap sandbox with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (417
  top-level tests pass and 3 skip); `-race -count=3` on 35 update, help
  and docs tests; the 19 update and release tests (16 new or changed, and
  3 unchanged ones next to them) 20 times and 5 times under `-race`. Of the
  17 root-package update tests the fail-before runs pick, round 1's
  `update.go` fails 3 (`TestNoteLines` and both swap tests: that code has
  no `rename` to interrupt), and the one from before this part fails 9
  (round 6's 8, and the new test). Every earlier mutant, with the new test
  among the four guard tests: each wrong one fails at least one of them,
  each correct one passes all four, and a correct swap made with
  `os.Rename` fails by design. Of the round-6 review's mutants, the one that
  returns when `.old` cannot be taken fails the new test, and passes round
  6's tests; the copy left behind fails both interruption tests; the two
  correct ones pass. The older tests pass the gaps their reviews found.
  The docs checker finds 41 links and no problem, and the docs tests pass;
  the whole module, in the desktop-like sandbox, connects to none of its
  decoy sockets, while the positive control reaches two.
- Left alone: a signal in the microseconds between the end of the catching
  and the sudo hint leaves the staged binary in TMPDIR without the hint; a
  second Ctrl+C during the cleanup is not special; a source build's update
  still catches nothing, so Ctrl+C there can leave `<path>.new`. CAN or SUB
  inside a control string does not end it, so the rest of its line stays
  hidden, and a C0 character inside a CSI ends it early, so "ESC [ 3 CR 1 m"
  leaves "1m", where a terminal acts on the C0 and goes on. A C1 control
  inside a control string does not end it either: "a ESC P b U+009B 31m c
  ESC \ d" gives "ad", where a terminal that follows DEC's parser ends the
  DCS at U+009B and shows the "c".

## Update (2026-10-21: QoL overhaul, part 30: scterm doctor)

- New command `scterm doctor` (`doctor.go`): one line for each check, `ok`,
  `warn` or `FAIL`, the check and what it found, and under it what to do
  when there is something to do. Each check's lines are printed as that
  check ends (the pairings, read first, print last). It exits 1 when a
  check fails and 0 with warnings only; `doctor -h` (or `-help`, `--help`,
  `help`) prints its usage, and any other argument is a usage error
  (exit 1). It prints no summary line and nothing on stderr.
- Checks: the build (on amd64, AVX2 from /proc/cpuinfo); adb (on PATH,
  `adb version`); each device `adb devices -l` lists, explained in
  scterm's own words, with each ready device's release and SDK read by
  getprop against what the README says (5.0 for the server, 11 for sound,
  13 for playback capture); sound for the terminal display (`pactl`,
  `pactl info`, `XDG_RUNTIME_DIR` when no server answered,
  `SCT_AUDIO_DEVICE` against `pactl list sinks short`); `--window`'s
  browser and display; the web port (listen and close on 127.0.0.1:6969)
  and the `SCTERM_WEB_HOSTS` entries; peer mode's identity and pairings,
  read-only (no identity is created) and without the network. Programs
  run in the C locale with a 10 s limit each, so a wedged adb or sound
  server cannot hang it; one that exits 0 but leaves its output open
  (`exec.ErrWaitDelay`) counts as a success.
- `ANDROID_SERIAL` is taken in every form adb takes for `-s`: the serial;
  a network device's host, with or without its port and a `tcp:` or
  `udp:` prefix; the USB path (`usb:1-1`); and `product:`, `model:` or
  `device:` with a name. These are ports of adb's
  `atransport::MatchesTarget` and libbase's `ParseNetAddress` (AOSP adb
  `transport.cpp` 1289-1323 at `1cf2f017`, libbase `parsenetaddress.cpp`
  27-79 at `6d19b5c6`; in `adb.go`, `matchesTarget`, `parseNetAddress`
  and `pickTarget`), applied to the `adb devices -l` listing doctor reads
  anyway, so there is no extra adb call. As in adb's
  `acquire_one_transport`, devices adb cannot open are passed over. The
  named device's line ends with `ANDROID_SERIAL names it` (or
  `ANDROID_SERIAL=VALUE names it`); a value that names no device (the
  advice lists the forms) or more than one (adb refuses then) fails.
- Severity: FAIL only for what stops a session from starting on this
  computer: no AVX2; adb missing or broken; the device scterm would use
  not ready, too old or absent; with no device chosen, no ready device
  that can be mirrored (then every device that cannot be used fails, a
  device that is not ready included); no device at all; a damaged
  identity or `peers.json`, or pairings whose identity is gone. Sound,
  window and web problems are warnings, since another display works
  without each. With a paired device, a missing or broken adb, a failing
  `adb devices -l` and "no device" are warnings too (their advice offers
  `--peer`). The display is ok when `--window` would find a Wayland or X
  socket itself, and warns only when it would find none.
- Messages: an adb error shows the first line after adb's `* ...`
  progress lines, so a server that does not start gives `adb: failed to
  check server version: cannot connect to daemon`, and the unauthorized
  error keeps its first line. Without `pactl`, doctor says scterm plays on
  the server's default output and that `pactl` makes the sound follow the
  output you pick, as the README says. When `pactl info` fails and
  `PULSE_SERVER` is set, doctor names it and tries the `XDG_RUNTIME_DIR`
  socket, as the stream does (`pulseSocketPath`). An identity or
  `peers.json` that cannot be read for its permissions gets chmod advice
  that keeps the pairings (a damaged file keeps the move-aside advice),
  and an identity that group or others can read warns.
- `SCTERM_WEB_HOSTS` is read through the browser display's own
  `webHostNames` (part 31): the names it adds, each once, in the order
  given, and a warning for each entry the display ignores, quoted with
  `%+q` and given the display's advice (`webHostsAdvice`), so doctor and
  the server agree on names with an empty label (`.example.lan`,
  `box..lan`) and on letters that only look like ASCII ones. This needs
  part 31, so this part lands after it.
- Shared code, with byte-identical messages (a probe on the old and new
  files in round 1; round 2 reads the serial and state as before):
  `adb.go` gained `listedDevices` (which also keeps the
  `-l` fields), `notReadyAdvice`, `noDeviceHint`, `chooseDeviceHint` and
  `severalDevices` out of `pickDevice`, and `adbMissingHint` out of
  `errADBNotFound`'s message; `window.go` gained `windowBrowser`, which
  `findBrowser` uses; `doctor.go` uses `pulseSocketPath` from `audio.go`.
- Help: `doctor` under Commands and in `commands`, saying it checks
  whether `--web` can listen on its default address, 127.0.0.1:6969, and
  the names in `SCTERM_WEB_HOSTS`; README: a Commands row, and a first
  Troubleshooting paragraph that names every check, the web ones too;
  `docs/DEVELOPMENT.md`: every `SCTERM_TEST_` variable, a list
  `TestDevelopmentNamesTheTestVariables` keeps complete.
- Tests: `doctor_test.go` (fake adb and pactl scripts with `PATH` set to
  their directory only, a fake cpuinfo, a scratch `SCTERM_HOME`, sockets
  and ports; `TestDoctorCommandLine` runs `scterm doctor` through
  `main()` in a child process with only `PATH`, `XDG_RUNTIME_DIR` and the
  test variable); `adb_target_test.go` (tables read from adb's C++);
  `doctor_adb_test.go`; `TestDoctorIsDocumented`; doctor cases in the
  help, leftover-word, subcommand-help and exit-status tests.
  `TestSubcommandHelp` gives `PATH`, `XDG_RUNTIME_DIR`, `HOME` and
  `SCTERM_HOME` their own temporary directories, so a broken argument
  check cannot run the developer's adb or pactl.
- Review round 1 (NEEDS CHANGES), resolved in round 2. M1: doctor wanted
  `ANDROID_SERIAL` to match a listed serial exactly, though adb also takes
  the other forms above, so it failed, and advised unsetting, a value adb
  accepts. L1: it showed adb's first stderr line, `* daemon not running;
  starting now at tcp:5037`, instead of the cause. L2: its missing-pactl
  advice implied the terminal display has no sound without `pactl`. L3:
  `TestSubcommandHelp` ran doctor in-process with the developer's `PATH`,
  so a broken argument check would run the real adb and pactl (a spy
  mutant did, and the real pactl reached a decoy sound server). L4: this
  entry claimed the builder ran with `iso.env` (corrected below). Nits:
  several ready devices all too old gave warnings only; a repeated
  `SCTERM_WEB_HOSTS` name was listed twice; the `XDG_RUNTIME_DIR` warning
  came even when `pactl` reached a server; a permission problem was
  advised to move the identity aside; the help and README said neither
  that the port checked is 127.0.0.1:6969 nor that `SCTERM_WEB_HOSTS` is
  checked. All are fixed, with the review's suggestions (the
  `PULSE_SERVER` try, `ErrWaitDelay`, lines as each check ends, the
  identity's mode, the full list of test variables); the display note
  doctor inherits from `browserEnv` (N6) is left alone.
- Correction (L4): round 1's builder did not use `iso.env`. Its logs
  record GOCACHE, GOTMPDIR and GOPROXY=off, with its own refusing fake adb
  and pactl first on PATH, and nothing about HOME, XDG_RUNTIME_DIR,
  PULSE_SERVER, DISPLAY or WAYLAND_DISPLAY. Its binary runs used `env -i`
  with only fake tools on PATH and no sandbox, so the unhealthy run's
  build, browser and display lines came from this host's /proc/cpuinfo,
  browser install and /run/user/1000 sockets, read only.
- Verification, round 1, in the builder's tree (`f35136c`: `f1c25f3` plus
  parts 25, 26, 27 and 28 as they were then): gofmt, vet, the build, `go
  test ./...` (360 top-level tests pass and 3 skip; 344 and 3 on the
  base), `-race`, the new tests 20 times and 5 times under `-race`,
  fail-before through the base binary and files, 47 of 47 mutations
  caught, and the built binary with only fake tools on PATH.
- Verification, round 2, in the builder's tree (`3e1f9a6`: `fd9e086` plus
  parts 27, 29 and 30 as they were then), with `iso.env`: gofmt, vet, the
  build and `git diff --check`; `go test ./...` (400 top-level tests pass
  and 3 skip; 394 and 3 on the base, the 6 new tests); `-race -count=3` on
  31 doctor, command-line, help and README tests; round 2's 23 new and
  changed tests 20 times and 5 times under `-race`; each new case fails on the
  base `adb.go` and `doctor.go` (through a shim over the base functions)
  or on the base docs; 69 of 69 mutations caught; the whole root package
  in a bwrap sandbox set up like a desktop session (decoys for the sound
  server, Wayland, D-Bus, X and adb's port 5037), with no connection,
  while a positive control logged 5; the built binary in bwrap on 17 fake
  setups, none of which changed HOME, SCTERM_HOME or XDG_RUNTIME_DIR.
- Taken into the worktree: the builder's files as built, and the README
  merged three ways (the merge changed exactly round 2's lines). Then
  `doctorWebHosts` was changed to read `SCTERM_WEB_HOSTS` through part
  31's functions, as above: `TestDoctorReadsWebHosts` gained empty-label
  and lookalike cases, and it and the whole-run test the display's
  wording; with the builder's `doctor.go`, both fail.
- Verification, snapshot (`/tmp/opencode/review-30b`, on `141cd3e` with
  parts 29, 32 and 31 as reviewed), with `iso.env`: gofmt, vet, the build,
  `git diff --check` and the hashes; `go test ./...` (402 top-level tests
  pass and 3 skip); `-race -count=3` on 31 doctor, command-line, help and
  README tests; round 2's 23 new and changed tests 20 times and 5 times under
  `-race`; with the builder's `doctor.go` through `-overlay`,
  `TestDoctorReadsWebHosts` and `TestDoctorOnAHealthyAndAnUnhealthyComputer`
  fail; the builder's mutations (all but one that targets the code the
  intake replaced) and five of the intake's change, 73 of 73 caught (one of
  the intake's ran again after a fix, since it did not compile); the docs
  checker (2 files, 41 links, no problems) and the docs tests; the whole
  root package in a bwrap sandbox with decoys for the sound server, D-Bus,
  Wayland and X, no connection.
- Review round 2 (PASS), notes taken up in round 3. LOW-1: no test
  reached "with a pairing, no device is only a warning" through
  `runDoctor`, only through `adbAndDevices`, the tests' copy of its adb
  wiring, so mutants that dropped the pairing there survived. Nits: no
  case had `ANDROID_SERIAL` name a ready and an offline device, which adb
  finds ambiguous, since it passes over only devices it may not open; no
  case had SDK 20, the newest SDK too old for the server; the identity's
  mode was pinned on its read bits only, so a warning on any group or
  other bit survived; and the `matchesTarget` comment said adb on Linux
  lists a USB path for every USB device, true of its libusb backend but
  not of its native one. Suggestions: a broken adb or a failing `adb
  devices -l` failed even with a pairing, while a missing adb only warned;
  and `doctorPeers` checks that `identity.pem` exists, then loads it with
  `peer.LoadOrCreateIdentity`, which would make one if the file went
  away in between. Its nits on this entry's wording (what the README
  says, round 2's test count against round 1, the help words `doctor`
  takes) are fixed in place.
- Round 3: with a paired device, an adb that does not run and a failing
  `adb devices -l` are warnings whose advice also offers `--peer`;
  without one they fail as before, with the same advice. A device that is
  not ready, too old, or not the one `ANDROID_SERIAL` names still fails
  with a pairing. `TestDoctorOnAPeerOnlyComputer` runs `runDoctor` without
  adb and with an adb that lists no device; `TestDoctorWhenADBFails` runs
  each case with and without a pairing; new cases cover a ready and an
  offline device of one model, SDK 20, and an identity with mode 0620,
  which gets no warning (0640 still does). The `matchesTarget` comment now
  says which backend lists a USB path, and that a USB device listed
  without one answers to its serial after `tcp:` or `udp:`, which adb
  refuses. Left alone: the gap in `doctorPeers` (`peer/` has no load-only
  reader).
- Verification, round 3, in the builder's tree (`4fdfdc4`: `141cd3e` plus
  parts 29, 30, 31 and 32 as they were then), with `iso.env`: gofmt, vet,
  the build and `git diff --check`; `go test ./...` (403 top-level tests
  pass and 3 skip, as on the base); `-race -count=3` on 31 doctor,
  command-line, help and README tests; the 6 changed tests 20 times and 5
  times under `-race`; with the base `doctor.go`, only the 4 paired cases
  of `TestDoctorWhenADBFails` fail; the review's R07, R21, R21b, R25 and
  R39 and round 2's adb rule survive round 2's tests and are caught now,
  22 of 22 mutations in all; the `adb.go` change is comment-only; and the
  built binary in bwrap on 6 fake setups exits 0 with a pairing and 1
  without one.
- Taken into the worktree: round 3's four files. The worktree's
  `doctor.go` also holds part 34's shared port wording, so round 3 was
  merged into it three ways, which added exactly round 3's lines; this
  part's snapshot takes the builder's `doctor.go`.
- Verification, snapshot (`/tmp/opencode/review-30c`, on `6b908b5`, the
  tip of `overhaul` with parts 29, 32 and 31 committed), with `iso.env`:
  gofmt, vet, the build and `git diff --check`; `go test ./...` (403
  top-level tests pass and 3 skip); `-race -count=3` on 31 doctor,
  adb-target, command-line, help and docs tests; 9 doctor and adb-target
  tests, among them the 6 round 3 changed, 20 times and 5 times under
  `-race`; with round 2's `doctor.go` laid over, `TestDoctorWhenADBFails`
  fails and the other 8 of the 9 pass; the docs checker finds 41 links and
  no problem in README.md and docs/DEVELOPMENT.md, and the docs tests
  pass; and the whole module, run in a bwrap sandbox set up like a desktop
  session (decoy sockets for the sound server, Wayland, D-Bus and X, adb's
  port 5037, and the real `pactl` on PATH), connects to none of them.
- Not run: a real adb, phone or sound server (adb's rules come from its
  source at the revisions above, not from running released
  platform-tools), a CPU without AVX2, the tests as root (the permission
  cases skip there), and a real `scterm --web` holding the port.
- Left alone: terminal capabilities (no reliable probe); doctor repeats
  `browserEnv`'s note, wrong when `DISPLAY` is set but its socket is
  missing (N6); `adb devices -l` may start the adb server, as scterm
  itself does; a pairing with an invalid fingerprint is counted, though
  `scterm peers` skips it; a device adb lists as `(no serial number)`
  cannot be named by its USB path or names (that parse is `pickDevice`'s);
  a `PULSE_SERVER` that lists several servers is named as it is, and a
  stale `default-server` in `client.conf` gets no fallback try; with a
  `PULSE_SERVER` that answers, doctor checks that server, as the stream
  does, while scterm's own `pactl` calls go to the `XDG_RUNTIME_DIR`
  socket (part 29's left-alone item).

## Update (2026-10-21: QoL overhaul, part 31: follow-ups to part 28's reviews)

- Framing: the page (`/`) is now sent with `X-Frame-Options: DENY`, and
  its Content-Security-Policy ends with `frame-ancestors 'none'`; its
  seven other directives are unchanged. A page opened as `127.0.0.1`
  passes the Host check, so until now any website could put the live page
  in a frame and trick the viewer into clicks and keys that reach the
  device. Only the page carries them: its files do nothing in a frame but
  show their text, the 403 and the other errors are one line of plain
  text, and the 403 goes only to names scterm does not serve, whose own
  page can read it anyway; the WebSocket's 101 is written by hand after
  the hijack. Nothing in scterm frames its page: `web/` has no frames, and
  `--window` opens Chromium with `--app=URL`, or Firefox with
  `--new-window URL` or `--kiosk URL`.
- `run()` takes the names from the new `allowedNames(ln)`: the listener's
  address, `--web-addr` (`s.addr`), and the host name from `osHostname`
  (`os.Hostname`). It listens through `webListen` (`net.Listen`). Tests
  replace both. Behaviour is unchanged. With an empty `--web-addr`
  (`--web-addr=`; the default is 127.0.0.1) the names no longer hold
  "127.0.0.1", which the check never looks up (it accepts any IP address
  first).
- `SCTERM_WEB_HOSTS`: each ignored entry is reported at start (a repeated
  entry once for each time it appears), as `scterm: web: ignored ENTRY in
  SCTERM_WEB_HOSTS: an entry must be a name, or a name and a port, such as
  mirror.lan or mirror.lan:8443`, with ENTRY quoted by `%+q` (a Kelvin
  sign shows as `\u212a`). When ENTRY has a character outside ASCII, the
  line adds `; write an international name in its xn-- form` (no
  conversion: `x/net/idna` needs `x/text`, which is not in `go.sum`). A
  name with an empty label (a leading dot, as in Django's `.example.lan`,
  two dots in a row, or two final dots) is now ignored too, with `no
  request can match a name that starts with a dot or has two dots in a
  row; list each name in full, such as mirror.example.lan`. A Host header
  with such a name, served until now, gets the 403. One final dot is
  still allowed. A path was already refused (`/` is not allowed in a
  name).
- Docs: the README's Browser warning says that the page asks not to be
  shown inside another page, and that a name in `SCTERM_WEB_HOSTS` or
  given to `--web-addr` is accepted with any address, loopback included,
  so the checks do not stop whoever can answer for it (for a `.local`
  name, anyone on the network); its safe way is now the default address,
  no names in `SCTERM_WEB_HOSTS`, and an SSH tunnel.
  `docs/ARCHITECTURE.md` gives the warnings' wording and a bullet for the
  frame headers. `flags.go` is unchanged.
- Tests: `TestWebPageRefusesFrames` (new `web_iframe_test.go`: the two
  headers, and the policy's directives parsed and compared);
  `TestWebServesTheNamesItStartsWith` (new: `run()` with `--web-addr
  Mirror.Scterm.test`, a fixed host name, a loopback listener that
  reports 192.0.2.10, so no DNS, no /etc/hosts and nothing off loopback,
  and `SCTERM_WEB_HOSTS` emptied, so a developer's own list cannot add
  names); `TestWebWarnsOfHostsEntriesItIgnores` (replaces
  `TestWebWarnsOfHostsEntriesThatAreNotNames`; whole lines compared, at
  start and after requests; among its entries `*..example.lan`, which
  would not be a name even without its extra dot, so it gets the
  not-a-name advice); `TestWebHostsEntriesMustBeNames` (extended).
- Verification. Builder, on `bb396d9` (`fd9e086` plus earlier rounds of
  parts 27, 29 and 30), with iso.env: gofmt, vet, the build, `go test
  ./...` (in the root package 366 top-level tests pass, against 364 on
  that base: two new, one renamed; 3 skip across the packages, on both),
  `-race -count=3` on the web tests (189 passes over 63 tests, no races),
  the changed tests 20 times and 5 times under `-race`, the docs tests
  and the docs checker. The new tests fail on the base files for their
  reasons; the `run()` test needs seams the base lacks, so instead N17
  and N18 were shown to pass the whole base package and each to fail the
  new test. 19 of 19 mutations are caught, part 28's survivor (`run()`
  passing no listener address) included. Headless Firefox 157 (no
  display, scratch profile, loopback): the base page loads in another
  site's iframe; this part's does not, with either header alone too,
  while the page opened directly loads. Not run: Chromium, a browser that
  knows only `X-Frame-Options`, real DNS rebinding or mDNS, a device.
- Review round 1 passed it, with one low finding and two nits, fixed in
  round 2: this entry said that with no `--web-addr` the names no longer
  hold "127.0.0.1", though the default `--web-addr` is 127.0.0.1; its test
  counts mixed the root package's count with the skips of every package,
  and counted the extended test as new; and the reviewer's mutant H12
  (`webHostsAdvice` choosing the empty-label advice by the entry's dots,
  without asking whether it is a name) survived the whole package, as no
  test entry that is not a name had a leading dot or two dots in a row.
  Round 2 adds `*..example.lan`, empties `SCTERM_WEB_HOSTS` in the
  `run()` test, says to list no names with the SSH tunnel, and moves the
  part onto part 29's fourth round and part 32's second, which changed
  none of its files.
- Verification, round 2's snapshot (review-31b: `141cd3e` plus parts 29
  and 32 as reviewed, with iso.env): gofmt, vet and the build are clean;
  `go test ./...` passes 353 top-level tests in the root package with 1
  skip (380 and 3 across the packages); the six node suites pass (input
  130, worklet 41, pointer 17, conn 61, page 285, audio 182); `-race
  -count=3` on the 61 web tests (`Web|^TestOriginOK$`) finds no races; the
  four changed tests pass 20 times, and 5 times under `-race`. Against the
  base's `web.go`, the three changed tests other than the `run()` one
  fail. N17, N18 and W1 (`allowedNames` given no listener address) each
  fail the `run()` test, and H12 fails
  `TestWebWarnsOfHostsEntriesItIgnores`. The docs checker finds no
  problems in 33 links, and the docs tests pass. The whole root package,
  run in a sandbox set up like a desktop session (a decoy sound server at
  `/run/user/1000/pulse/native`, `PULSE_SERVER` unset, a fake adb, a
  scratch HOME), made no connection to the decoy.
- Note for part 30 (`scterm doctor`, not yet committed, so not in this
  part's tree): its `doctorWebHosts` reads `SCTERM_WEB_HOSTS` with
  `webHostName` alone. With this part, it would say it adds
  `.example.lan` or `box..lan`, which the server now ignores, quote
  entries with `%q`, and keep its own hint. It should take the names and
  the ignored entries from `webHostNames` and print `webHostsAdvice`.
- Corrections to part 28's entry: it says the addresses `--window` opens
  are served, though its test covered only `127.0.0.1`, `localhost` and
  `::1` (a `--web-addr` name is covered from this part on); and it says
  that replaying gave the worktree's docs exactly, though a comparison of
  changed lines showed it.
- Left alone: the files and the error responses carry no frame headers
  (above), so a later HTML route would have to add them; `flags.go`'s
  help line, which says nothing of how an entry is written; the
  Host-header reader `webHostName`, so `a..b.localhost` and `.localhost`
  as a Host are still served by the `.localhost` rule, and
  `.foo.localhost` in `SCTERM_WEB_HOSTS` is told that no request can
  match it, though that rule serves such a Host anyway; the `xn--` hint
  comes for any character outside ASCII, an invisible one included (the
  `%+q` escape shows which); part 28's own left-alone items.

## Update (2026-10-21: QoL overhaul, part 32: follow-ups to part 19's reviews)

- Keyboard focus on a control of the chrome now holds the HUD and the bar
  up only once the session has ended, when the keys are the browser's.
  During a session Tab, Enter and Space go to the device, so the bar's
  Controls button, which gets the focus back when Esc closes a sheet it
  opened, no longer keeps the chrome up for the rest of the session: the
  chrome fades 2.5 s on, and the inert bar takes the focus with it. The
  sheet still gives the focus back to whatever opened it (part 14).
  Moving the focus to ☰ instead was not chosen: ☰ never fades, so its
  focus ring would stay over the picture, and the bar's Controls button
  would be the one opener the focus does not come back to.
- In touch mode a key no longer brings up the HUD and the bar, whether a
  soft keyboard's (key "Unidentified", keyCode 229, aimed at `#ime`) or a
  hardware keyboard's, so the next tap is not taken by a bar button. ⋯
  still shows the bar, and with a mouse keys show the chrome as before.
- `pcm-worklet.js` counts one underrun for each block the stream cannot
  fill, on the frame-for-frame and the resampling paths alike: a new
  `starve()` pads the rest of the block with silence and counts once. It
  counted one per block that started empty but one per missing frame
  inside a block, and with a ratio other than 1 one per frame for as
  long as the stream was stopped, because the resampler keeps its last
  frame for the next burst. The HUD's `u<N>` and the telemetry now mean
  the same at every rate, one per block: with the stream stopped,
  headless Chrome counts 375 a second at 48 kHz and 345 at 44.1 kHz
  (44,160 there before). A block is 128 frames in the worklet and 2,048
  in the plain-http fallback, so those two paths' counts still differ.
  The sound is unchanged: the old and new rings, fed the same random
  bursts, gave identical samples and ring state over 120,000 blocks.
- A sound chip whose words change and come back within 6 s (A, B, A) now
  shows for 6 s from their return: `showSound` clears its timer when the
  words change (`soundTimer`), where a guard on the words let the first
  A's timer through.
- Alt+M with the sound muted and its start failed unmutes, as the chip
  ("Sound off · Alt+M", which outranks "Sound failed") says; the next
  Alt+M retries. That was already so, and is now pinned.
- New checks pin what part 19's reviews found unpinned: a press on the
  picture after a bye leaves the focus on Reconnect; a mouse taking over
  drops ⋯'s hold; a finger on Close shows the bar again while ⋯ holds
  it; a coarse pointer at load starts with the bar down and inert;
  returning to the tab shows the chrome with a mouse and not in touch
  mode; the picture keeps clear of the side insets; the fade timer waits
  out an open sheet.
- Docs: `player.css`'s header says the chrome also shows at page load and
  on returning to the tab, and that on a touch screen taps and keys do not
  bring it up (⋯ does); the README says a swipe that starts between the
  bar's buttons passes through the bar, to the device where the picture
  is under it.
- Checks changed on purpose. Page: "2.5 s on, it still has the keyboard
  focus…" and "for as long as it has it" became "its keyboard focus does
  not hold the chrome during a session…", and the hold after a bye is
  checked instead; "a press on the picture is for the device, and takes
  the focus off the page's buttons" became "☰, never inert, keeps the
  focus through a fade; a press on the picture…", set up on ☰ instead of
  the bar's Controls button, whose focus now goes with the fading bar;
  the touch section's "a key still shows the chrome…" became "back in
  touch mode, a key does not bring it up…", its hover half moving to "a
  finger on the picture puts it away, though the finger's :hover is left
  on the bar". Worklet: running dry at 44.1 kHz must count exactly 7
  underruns (one per short block), not just some, and `play()` fills its
  output with NaN so that a frame left unwritten shows. Audio: `page()`
  takes `coarse`, a phone's pointer.
- Verification, in the builder's tree (`/tmp/opencode/dev-32`, base
  bb396d9, under dev-29's `iso.env`, which has no sound server): gofmt,
  `go vet ./...`, `go test -count=1 ./...` (391 top-level tests pass, 3
  skip), `-race` on part 19's web filter (74 tests), the Markdown check
  of README (31 links), and the node suites: input 130, worklet 41,
  pointer 17, conn 61, page 285 and audio 182 checks (130, 35, 17, 61,
  276 and 175 on the base). Against the base files the new checks fail
  as they should: worklet 4, page 2, audio 1; the new pins pass there.
  Mutations: this part's 15 of 15 are caught, part 19's 34 of 34, and
  the second reviewer's 23 of the 27 that still apply (R6, R14, R17 and
  R22 are not; R6 changes only the queued figure while starved, and R22
  cannot change behaviour, since `chrome()` returns before it would).
  Headless Chrome 155 on a served copy of the page with a fake socket:
  13 of 13 pass; the base page fails 6, all on what this part changes.
- Verification in the snapshot (`/tmp/opencode/review-32`, whose base is
  141cd3e with part 29's reviewed files, since part 29 lands first; under
  the same `iso.env`): gofmt and `go vet ./...` clean, `go build` ok,
  `go test -count=1 -v ./...` 378 top-level tests pass and 3 skip (part
  30's doctor tests are not in this base), the node suites give the
  counts above, and `-race` on part 19's web filter passes (74 tests).
  Against the base's `player.js`, `player.css` and `pcm-worklet.js` the
  new tests fail as above (worklet 4, page 2, audio 1; input, conn and
  pointer pass). The README's Markdown check finds 31 links and no
  problem. The whole root package, run in a sandbox set up like a
  desktop session for sound (a decoy socket where the session's sound
  server would listen, PULSE_SERVER unset, `iso.env`'s fake adb first on
  PATH and its scratch HOME), made no connection to the decoy.
- Review round 1 passed it, with five nits, fixed in round 2: this entry
  left out the changed ☰ check and the browser-less checks below; the
  sound chip's clause in `chromeHeld` could no longer decide anything (a
  bye hides the chip and moves the focus to Reconnect), so it is gone;
  `player.css`'s header said only ⋯ shows the chrome on a touch screen,
  though the wheel still does; the comment on `pictureDown` said the
  blur is skipped only while Reconnect has the focus, where it is skipped
  whenever the session has ended; and a check's name said Tab brings the
  chrome up, which the stand-in page cannot show (it was up already). The
  round also moved the part onto part 29's fourth round, which changed
  none of this part's files.
- Correction to part 19's entry: its checks did not run "under a scratch
  sound server"; dev-29's `iso.env` points PULSE_SERVER at a socket that
  nothing listens on, so they ran with no sound server at all. And not
  all eight web files are the builder's bytes: `player.css`'s header
  comment changed in review round 2.
- Not verified: real phones and their soft keyboards (CDP key events in
  Chrome's mobile emulation only); screen readers, for which the focus
  now leaves with a fading bar during a session; Firefox and Safari;
  real 44.1 kHz audio hardware. Returning to the tab, the fade timer
  waiting out a sheet, the side insets and Alt+M with the sound muted and
  failed were checked by the node suites only, not in a browser.
- Left alone: in touch mode the wheel still shows the chrome, while a key
  does not, after a bye too, so a hardware keyboard reaches the bar only
  through ⋯ there (not pinned); ☰ keeps a focus ring after Esc closes a
  sheet it opened, until a press on the picture; the ring still does not
  re-buffer after starving (part 12), and while resampling it reports one
  frame queued once the stream stops; R14 (the failed chip before any
  sound) and R17 (the faded bar's buttons, in CSS) stay unpinned.

## Update (2026-10-21: QoL overhaul, part 29: terminal sound without pactl, and on a running sound card)

- The terminal display's sound now opens without `pactl`. scterm gave
  libpulse an empty string, not NULL, for its default server and sink, and
  libpulse refuses "" as an invalid argument: without `pactl` and without
  `SCT_AUDIO_DEVICE`, every open failed after 1.8 s with "Invalid argument",
  and with a sink named, the first server always failed, which cost about
  0.9 s. Now "" reaches libpulse as NULL (`cstrOrNil`), so the stream opens
  on the server's default sink (which `PULSE_SINK` can set), and
  `PULSE_SERVER` and client.conf's default server are honoured. The C
  strings each try made are now freed (three leaked per try).
- `openFirst` tries each server (libpulse's default, then the
  `XDG_RUNTIME_DIR` socket by name, which rescues a stale `PULSE_SERVER`)
  with each sink, in up to three rounds 150 and 300 ms apart, for a server
  that is still starting. A pair libpulse refuses as an invalid argument or
  an invalid server is not tried again. A fallback opens at once (the old
  code waited 0.9 s on each failed pair), and with no server the open gives
  up after about 0.45 s instead of 1.8 s. The error names every server and
  sink that failed, "default" for libpulse's own: `audio sink: server
  "default" device "default": Connection refused; server
  "unix:/run/user/1000/pulse/native" device "default": Connection refused`.
  With one pair it reads as before.
- The leftover-stream cleanup at start runs on the first server, in the same
  order, that answers. It always used the `XDG_RUNTIME_DIR` socket, so a
  leftover stream on a `PULSE_SERVER` server kept playing.
- A sound card that `pactl` reports running is now preferred, as the code
  meant: `pactl list sinks short` separates its columns with tabs, and the
  sample spec column ("s16le 2ch 48000Hz") has spaces in it, so splitting
  at spaces read the state from the wrong column, and no sink ever counted
  as running (`splitFields`).
- The README (requirements, "No sound") and `docs/ARCHITECTURE.md` (sound)
  now say that only the server is needed: without `pactl`, scterm plays on
  the server's default output and follows nothing itself; PipeWire moves a
  stream opened without a sink name to a new default by itself.
- Tests: `audio_open_test.go` covers the open's order, rounds, pauses,
  refused pairs, error, server order and cleanup through two seams, the
  package variables `audioPause` and `killAppStreams`.
  `audio_libpulse_test.go` runs real libpulse under `isolatePulse` (its own
  `PULSE_SERVER` nobody listens on, an empty `XDG_RUNTIME_DIR`, autospawn
  off, a `PATH` without `pactl`). `TestSinkSelectionLogic` checked a local
  slice, not `audioDevices`; `TestAudioDevicesFollowTheirOrder` replaces
  it, with a fake `pactl` that prints the short listings in their real
  format. `TestDisplaySinkIsWiredForEveryMode` built three apps whose audio
  streams opened on a developer's live sound server and stayed open; it
  now runs under `isolatePulse`, without open's pauses.
- Owner's calls, as built: a server that refuses connections while it
  starts now gets about 0.45 s instead of up to 1.35 s (with socket
  activation, a starting PipeWire accepts the connection instead); like any
  libpulse program, scterm may now start PulseAudio itself where it is
  installed with autospawn on (the old code never reached the default
  server; not tested).
- Verification, by the builder, with every run cut off from the session's
  sound server (`iso.env`): a private PipeWire 1.6.9 with WirePlumber
  0.5.18 (its own configs and runtime directory, null sinks only, no
  hardware and no D-Bus; checked to hold no sound device or session
  handle). Without `pactl`, the open took 5–7 ms on the default sink (it
  failed after 1.8 s); with `SCT_AUDIO_DEVICE`, 5–6 ms (906–907 ms); with
  `pactl` and no sound card, 13–14 ms on the default (failed); with a
  stale `PULSE_SERVER`, 5–7 ms through the socket (failed); `PULSE_SINK`
  without `pactl` chose the sink; scterm's follower moved the stream as
  before; the cleanup silenced a leftover stream on a `PULSE_SERVER`
  server (it kept playing). With nothing listening: 0.45 s, "Connection
  refused" at both servers (1.8 s, "Invalid argument"). The two libpulse
  tests fail on the old `audio.go` on behaviour; the seam tests do not
  build there. Of the builder's 26 mutations, 24 are caught: one is
  equivalent (the cleanup's C side already maps "" to NULL), and one, a
  stream that opens and is not kept, needs a server, which the private
  server's run caught. At intake: on the old `splitFields`, only the
  running-card case of `TestAudioDevicesFollowTheirOrder` fails, and 9 of
  9 mutations of `audioDevices` and `splitFields` are caught. On
  `f1c25f3` with this part, all with `iso.env`: gofmt, vet, the build,
  `go test ./...` (315 top-level tests pass and 3 skip), `-race -count=3`
  on the audio tests (48 passes over 16 tests, no races), the new and
  changed tests 20 times each and 5 times under `-race`, and the docs
  checker on the README and `docs/ARCHITECTURE.md`.
  Not run: a real PulseAudio daemon (not installed here), client.conf's
  default server, the X11 property, TCP servers, autospawn, a desktop
  session, Bluetooth, real hardware, and the scterm binary end to end.
- Review round 1 found a cleanup test that set `XDG_RUNTIME_DIR` to
  `/run/user/1000`, where this host's live sound server listens, kept off it
  only by the `killAppStreams` seam: a regression that went around the seam
  would have killed a developer's `scterm` streams. It also found that
  `TestAudioSinkConnectsToTheDefaultServer` claimed more than it checked: its
  expected text was built from Go's own strings, so passing NULL for the
  socket server, or for a named sink, went unseen. Round 2: that cleanup test
  runs under `isolatePulse` and expects the socket in its directory; the
  tests that only compare server names use a runtime directory that cannot
  exist (`/nonexistent/scterm-test/xdg`); `isolatePulse` also gives libpulse
  its own `XDG_CONFIG_HOME` and no `PULSE_COOKIE`. The libpulse test now
  listens on the isolated socket (and hangs up), requires a connection there
  in each case, skips open's pauses, and says that a named sink reaching
  libpulse needs a real server, which only the builder's PipeWire probe
  checked. Two new tests pin that a missing sink ("No such entity") is tried
  in every round, and that another round follows when any pair may still
  work, even when the last pair tried was refused. The comments of
  `audioDevices` now give its real order.
- The audio follower (`followAudioSink`) ran until the process exited, so a
  test that ran `runWeb` with a working sink left one running. It asked
  `pactl` for the default sink once a second until the test binary exited,
  with the session's environment back once the test had ended; on a desktop
  with an scterm stream playing, it would also have moved that stream. Part
  26's tests left four this way: a run of the whole root package in a
  sandbox set up like a desktop session (bwrap, `/run/user` and `/run/dbus`
  hidden, a decoy socket at `/run/user/1000/pulse/native`, `PULSE_SERVER`
  unset) counted 80 connections, all `pactl get-default-sink` from the test
  binary. The follower now takes a stop channel and its interval: `runWeb`
  passes `a.web.done`, which every way out of it closes, and the terminal's
  `run` passes one it closes when it returns (checked by reading only, since
  `run` needs a terminal). The runs read `audioFollowEvery` before the
  follower's goroutine starts. `TestAudioFollowerStopsWithItsRun` and
  `TestWebRunStopsItsAudioFollower` pin the follower's stop and `runWeb`'s;
  the terminal's is checked by reading only.
- Verification of round 2, in the worktree with `iso.env` and in that
  sandbox: the changed and new tests pass, the new follower tests 5 times
  under `-race` (a first version read the interval inside the goroutine and
  raced with the test's cleanup). Round 1's tests with a cleanup that goes
  around the seam connected twice to the decoy; round 2's tests, none. In
  the sandbox these mutants are caught without a decoy connection: the
  cleanup going around the seam, NULL for every server, "No such entity"
  taken as final, and the last pair deciding whether another round follows.
  `runWeb` passing a nil stop and a follower that ignores its stop are
  caught too, but were run only with `iso.env`, which has no decoy (see
  review round 2 below). The whole root package made no connection to the
  decoy; that sandbox kept the session's `PATH` and `HOME`, with no network
  but loopback. On round 2's snapshot (the branch tip with part 19, and part
  27 as under review), with `iso.env` (no sound server reachable, a fake adb
  first on `PATH`): gofmt, vet, the build, `go test ./...` (375 top-level
  tests pass and 3 skip), `-race -count=3` on 25 audio tests, the new and
  changed tests 20 times and 5 times under `-race`, the six mutants above
  (each fails its test), and the docs checker; and the sandbox run of the
  whole root package, as above (no connection).
- Review round 2 found that the follower tests did not keep the regressions
  they catch away from the session: `t.Setenv` is undone when a test ends,
  so a follower that outlived its test (a `runWeb` that passes no stop, or a
  follower that ignores it) went on with the session's `PATH` and
  `XDG_RUNTIME_DIR` back. In the review's sandbox those two regressions
  failed their tests but still made 35 and 69 connections to the decoy in
  the audio tests, and 4,133 and 8,183 in the whole root package. It also
  found that the follower could ask once more after its stop: select picks
  at random between a closed stop and a tick that is due, as one always is
  after a slow `pactl`. Round 3: the follower looks `pactl` up on `PATH` and
  takes its environment once, when it starts (`findPactl` returns a
  `pactlTool`, the path and the environment), and reads neither again, so a
  follower that outlives its test runs only the `pactl` that test put on
  `PATH`, which goes with the test's directory. Without `pactl` the follower
  ends at once instead of looking for it every second (the README already
  says scterm follows nothing without it); part 26's tests give `runWeb` a
  `PATH` without `pactl`, so their followers end at once. After each tick
  the follower checks the stop again before it asks. `pulseCmd` and
  `pulseDefaultSink` still look `pactl` up at each call.
- New tests: `TestAudioFollowerStopsBeforeAnotherTick` (twelve rounds; each
  round's `pactl` waits until the test has closed the stop, and must have
  run exactly once), `TestAudioFollowerKeepsThePactlItFound` (after a second
  `isolatePulse` puts another `pactl` on `PATH`, the follower keeps asking
  the first and never the second), and
  `TestAudioFollowerWithoutPactlEndsAtOnce`. `TestWebRunStopsItsAudioFollower`
  now waits until the follower has moved the stream during the run (a fake
  `pactl` with another default sink and one scterm stream, and the "audio
  -> ..." line), so it also fails when `runWeb` starts no follower at all.
- Verification of round 3, on its snapshot (the branch tip with part 27 as
  under review), with `iso.env`: gofmt, vet, the build, `go test ./...` (378
  top-level tests pass and 3 skip), `-race -count=3` on 28 audio tests, and
  the new and changed tests 20 times and 5 times under `-race`. These
  mutants each fail their test: round 2's six, a follower that looks `pactl`
  up at each tick, one that goes on without `pactl`, and one without the
  second check of the stop (all 10 of 10 runs of its test fail). The whole
  root package in a bwrap sandbox set up like a desktop session for sound
  (`/run/user` and `/run/dbus` hidden, a decoy socket at
  `/run/user/1000/pulse/native`, `PULSE_SERVER` and `PULSE_RUNTIME_PATH`
  unset, no network but loopback), with `iso.env`'s fake adb first on `PATH`
  and its scratch `HOME`: no connection to the decoy unmutated, none with
  `runWeb` passing a nil stop, and none with a follower that ignores its
  stop; with the nil stop and a lookup at each tick together, 4,043, all
  `pactl get-default-sink`.
- Review round 3 found that the waiting `pactl` of
  `TestAudioFollowerStopsBeforeAnotherTick` had no way out but its release
  file. When a round failed, as it does for a follower that ignores its
  stop, the test's cleanups could remove that file before the script, and a
  script the follower started then waited forever: in 85 runs under that
  regression, two left one running after `go test` had exited. It also
  found that the tests kept one fault away from the session but not two: a
  follower that looked `pactl` up again when the one it had was gone, or
  that looked it up at its first tick instead of before it started, passed
  every test, and either one with a `runWeb` that passes no stop reached
  the decoy (4,257 and 80 connections; the first made 23,200 with a
  follower that ignores its stop). And `TestAudioFollowerKeepsThePactlItFound`
  said that it checked the environment, which it did not.
- Round 4: the runs look `pactl` up, and take the environment, before they
  start the follower (`audioFollower`, which also checks the sink and
  `SCT_AUDIO_DEVICE`), and hand both to it; without `pactl` they start no
  follower. A lookup can still be written inside the follower, though: one
  at each tick, or when the `pactl` it has is gone, fails
  `TestAudioFollowerKeepsThePactlItFound`, but one every 100 ticks, or only
  at the first tick, passes every test (review round 4). The waiting script
  also ends when the script itself is gone, or after 2,000 waits (about
  11 s).
  `TestAudioFollowerKeepsThePactlItFound` now records the `XDG_RUNTIME_DIR`
  each `pactl` ran with, then removes the first `pactl` and lets 50 ticks
  pass, after which the second must still not have run.
  `TestAudioFollowerNeedsASinkAndPactl` replaces
  `TestAudioFollowerWithoutPactlEndsAtOnce`: no sink, a sink that did not
  open, `SCT_AUDIO_DEVICE`, or no `pactl` start no follower, and a sink
  with `pactl` hands the follower that `pactl`.
- Verification of round 4, on its snapshot (`141cd3e`, the branch tip with
  part 27), with `iso.env`: gofmt, vet, the build, `go test ./...` (378
  top-level tests pass and 3 skip), `-race -count=3` on 28 audio tests, and
  the new and changed tests 20 times and 5 times under `-race`. Thirteen
  mutants each fail their test: round 3's nine (the one that went on
  without `pactl` is now the runs starting a follower without it; its first
  file did not build, and rebuilt, it fails), a follower that finds `pactl`
  again when it is gone (10 of 10 runs of its test fail), `pactl` reading
  the environment at each call, and the runs starting a follower for a sink
  that did not open, or with `SCT_AUDIO_DEVICE` set. The waiting script on
  its own waits while there is no release, ends at once when it is removed,
  and ends after 11.19 s with neither. Eighty runs of
  `TestAudioFollowerStopsBeforeAnotherTick` with a follower that ignores its
  stop, eight at a time in a pid namespace: all 80 fail, and a second later
  no `pactl` script was still running and no test directory was left (the
  review found one left in 160 such runs; see its bullet below). The
  whole root package in the decoy sandbox, as in round 3: no connection
  unmutated, with a nil stop, or with a follower that ignores its stop;
  4,052 with the nil stop and a lookup at each tick together. The docs
  checker, on docs this round did not change.
- Review round 4 found that no test checked that the runs ask
  `audioFollower`: a `runWeb` that looked `pactl` up itself passed every
  test, and with a nil stop, or a follower that ignores its stop, reached
  the decoy (47 connections each, from the two tests that run `runWeb`
  without `isolatePulse`). It found false round 4's claim that a lookup
  inside the follower could no longer be written without changing what it
  takes, and wrong the left-alone item that only two faults that each fail
  a test remained: a lookup every 100 ticks fails none, and with a nil stop
  it made 3,992 connections (both are corrected in place). It found that
  `TestAudioFollowerKeepsThePactlItFound` recorded `XDG_RUNTIME_DIR` but not
  `PULSE_SERVER`, the variable that points `pactl` at a server (a `pactl`
  that derived `PULSE_SERVER` again at each call passed). And under a
  follower that ignores its stop, one in 160 runs of
  `TestAudioFollowerStopsBeforeAnotherTick` left its test directory: a
  script the follower starts during the cleanup can write its log file
  again after the directory was emptied (nothing keeps running).
- Round 5 changes only `audiofollow_test.go` and this entry.
  `TestWebRunStartsTheFollowerOnlyWhenItShould` runs `runWeb` three times,
  with no sink, with a sink that did not open, and with `SCT_AUDIO_DEVICE`
  set, each with a counting `pactl` on `PATH` and the follower asking every
  5 ms; in the 20 ticks after the server listens, `pactl` must not run.
  `TestAudioFollowerKeepsThePactlItFound` also records `PULSE_SERVER`, which
  must be the socket under the runtime directory `pactl` was found with.
- Verification of round 5, on its snapshot (`141cd3e`), with `iso.env`:
  gofmt, vet, the build, `go test ./...` (379 top-level tests pass and 3
  skip), `-race -count=3` on 29 audio tests, and the new and changed tests
  20 times and 5 times under `-race`. Fifteen mutants each fail their test:
  round 4's thirteen, a `runWeb` that looks `pactl` up itself (10 of 10
  runs of the new test fail), and a `pactl` that derives `PULSE_SERVER`
  again at each call (10 of 10). As the review found, three pass: a
  `runWeb` that ignores `audioFollower`'s answer (harmless: the empty path
  it then hands over never runs), and lookups every 100 ticks and only at
  the first tick. Eighty runs of `TestAudioFollowerStopsBeforeAnotherTick`
  under a follower that ignores its stop: all 80 fail, and a second later
  no `pactl` script runs and no test directory is left. The whole root
  package in the decoy sandbox: no connection unmutated, with a nil stop,
  or with a follower that ignores its stop. With two faults, each run fails
  a test and still reaches the decoy, from followers that outlive their
  tests: 4,053 connections with a nil stop and a lookup at each tick, 47
  with a nil stop and a `runWeb` that looks `pactl` up itself, and 4,015
  with a nil stop and a lookup every 100 ticks.
- Resolved from part 21's left-alone list: the empty strings, the wasted
  first try, the `PULSE_SERVER` comment, and the wrong column. Not tested:
  whether a session without `XDG_RUNTIME_DIR` now gets sound (libpulse then
  looks for its socket itself).
- Left alone: `pactl` always talks to the `XDG_RUNTIME_DIR` socket
  (`pulseEnv`), while the stream tries libpulse's default first, so with a
  `PULSE_SERVER` naming another reachable server, the sinks `pactl` found
  may not exist there (the stream then falls back to the socket, as
  before); `audioDevices` never adds the server's default after named
  sinks, so on real PulseAudio a named sink that fails everywhere leaves no
  sound (PipeWire falls back itself); the cleanup pumps libpulse for 300 ms
  twice whenever a server answers, 606 ms at every start; the unused C
  function `sct_audio_open`; the success notice says `device "default"`
  when `PULSE_SINK` chose the sink; the README's environment table does not
  list `PULSE_SERVER` and `PULSE_SINK`, which now take effect; on PipeWire,
  `SCT_AUDIO_DEVICE` naming the sink that was the default at start does not
  keep the stream there when the default changes (the builder's probe P8b,
  before and after this part); without `pactl` no follower starts, and none
  starts when `pactl` is installed during the run; nothing pins that the
  terminal display's `run` starts the follower, or closes its stop (mutants
  that do neither pass; `run` needs a terminal); a lookup inside the
  follower that the tests do not see (one every 100 ticks) passes every
  test, and with a stop that never comes it reaches a desktop's sound server
  (4,015 connections in round 5's sandbox with a nil stop; review round 4
  counted 24,806 with a follower that ignores its stop, which fails a test);
  `TestWebExitsTwoWhenTheDeviceIsLost` and `TestWebStreamEndSaysBye` run
  `runWeb` without `isolatePulse`, so `audioFollower`'s check of the sink is
  what keeps the session's `pactl` from them; under a follower that ignores
  its stop, `TestAudioFollowerStopsBeforeAnotherTick` can leave its test
  directory (one in 160 runs in review round 4, none in 80 in round 5);
  `audioFollower` reads the sink's error without a lock while `writePCM16`
  can set it (`audio.go:464`, as before this part).

## Update (2026-10-21: QoL overhaul, part 27: a way out of a paste, and true paste notices)

- Notices are quoted here as the status row shows them, without the
  "scterm: " that stderr and the exit replay keep.
- A Ctrl+C byte anywhere between a paste's start and end markers cancels
  the paste: nothing of it is sent, the status row says "paste cancelled by
  Ctrl+C", and input is dropped up to the paste's end marker, or until no
  read has come for 250 ms (each dropped read starts the 250 ms again); then
  keys act again. This holds whether the paste is still coming in, already
  whole in one read, or being dropped as too long (the cancel notice then
  replaces the "paste not sent" one), so the result does not depend on where
  the terminal's reads cut it, and a genuine paste holding a Ctrl+C
  character is dropped whole. A paste unfinished for 2 s, counted from the
  read that brought its start marker, gets "Ctrl+C cancels the unfinished
  paste", again every 2 s while it stays unfinished. The input loop now
  waits until the decoder's next deadline (an Esc's 25 ms, that notice, or
  the end of the 250 ms pause), at most 500 ms, instead of 500 ms polls with
  one 25 ms window for a lone Esc. These deadlines count on the monotonic
  clock, from a start taken when the program starts, so setting the system
  time moves none of them. The debug log shows only "(N bytes of a paste)"
  for what a cancel drops.
- An Esc directly followed by a start marker is Back, then one paste. An
  Esc followed by what could still become a start marker waits the 25 ms a
  lone Esc gets for the rest, and when it does not come decodes as before
  (Alt+Esc, which does nothing, then the rest typed); so two Esc presses
  within 25 ms resolve 25 ms later than before, with the same events. ESC O
  followed by ESC is now ESC O alone (Alt+Shift+O, which does nothing) and
  the Esc begins what follows, everywhere, since an Esc cannot end an SS3
  sequence.
- With a paired device a paste is sent only while this computer holds the
  lease (`peer.Session.Lease()`, `peer/client.go:258`, is `held`).
  Otherwise nothing is sent, and every time the status row says "paste not
  sent: no control (--takeover takes it)"; before, the paste went out, the
  device refused it, and scterm said "pasted N characters". adb sessions
  have no lease and are unchanged.
- The typed fallback (a paired device without the clipboard grant,
  capabilities or `set_clipboard`) takes at most 4096 bytes of text as
  typed (after CR LF folding, with control characters and bytes that are not
  UTF-8 left out); a longer paste is refused whole: "paste not sent: N
  bytes, at most 4096 typed". 4096 is a judgement call (a long URL, a key or
  a page of text, and at most 14 inject-text messages); how fast a device
  types it was not measured. The clipboard path keeps its 262,130-byte
  limit.
- Paste notices (sent, not sent for any reason, cancelled, and the Ctrl+C
  hint) still show in the status row, or on stderr without the terminal UI,
  but are no longer kept for the exit replay. A send error ("scterm: paste:
  ...") still is, and so is "scterm: input ignored: ... (a paste)" when
  handling a paste panics, now without the paste's length.
- From part 23's review: tests for its gaps R2 (a paste whose text reaches
  the limit as its end marker is cut), R3 (a welcome with the clipboard
  grant and no capabilities) and R4 (an empty or all-control paste), each
  failing on part 23's code with that mutation; `TestInputFuzz` also feeds
  every entry through a fresh `inputDecoder`, in one read and a byte per
  read, and fails on a panic or an "input ignored" notice (a decoder panic
  planted as mutation M30 fails the new `TestInputFuzz` and passes the old
  one).
- From part 16's review: `repaint` lets go of the UI lock with `defer`, so a
  panic while it paints, which `handleInput`'s recover swallows, no longer
  leaves the lock held and the run loop hung.
- README: the terminal's paste paragraph says Ctrl+C cancels a paste, gives
  the 4,096-byte typed limit and the lease, says what an unfinished paste
  does, and that scterm says when one of these stops a paste.
- This closes the part-23 entry's "nothing inside it acts as a key" (Ctrl+C
  now does) and its "Left alone" items on an unfinished paste, an Esc or
  ESC O before a paste, the success notice after a refusal, the R2, R3, R4
  and fuzz gaps, and the untested input loop and Esc window, as well as
  part 23's suggestions on paste lengths in the exit replay and a cap for
  the typed fallback.
- Verification, in the builder's tree (`286d838` plus the then uncommitted
  parts 14, 20, 21, 22, 23, 25 and 26): gofmt, vet, the build, `go test
  ./...` (the root package takes 22.8 s, 19.4 s before), `-race` on the
  whole module and `-race -count=3` on the 83 input, paste, fuzz, notice and
  render tests, and the 19 new and changed tests 20 times plain and 5 times
  under `-race`. New tests: `TestCtrlCCancelsAPaste`, `TestEscBeforeAPaste`,
  `TestPasteNeedsTheLease` (real sessions through `peer.Client.Connect` to a
  fake TLS target answering as `docs/PEER_PROTOCOL.md` describes),
  `TestTypedPasteLimit`, `TestPasteNoticesAreNotReplayed`,
  `TestPasteAtTheLimitWithItsEndMarkerCut`,
  `TestPasteWithoutTheBackendsCapabilities`, `TestEmptyPasteSendsNothing`,
  `TestRepaintPanicLeavesTheLockFree`, `TestInputLoop` (the real input loop
  in a child process reading a pipe, with real pauses), and, on a fake
  clock, `TestCancelledPasteEndsWhenInputStops`, `TestUnfinishedPasteHint`,
  `TestCtrlCAlwaysGetsOut` (3000 random inputs), `TestEscWaitsForWhatFollows`
  and `TestWaitWithNothingDue`; `TestPasteWithoutTheDeviceClipboard` now
  builds its sessions with the lease held. The seven behaviour tests fail
  on the base files for their item's reason; the fake-clock tests use the
  new decoder API and cannot compile there, so mutations stand behind them:
  40 of 40 mutations of the new code are caught. In the worktree with the
  other uncommitted parts: gofmt, vet and `go test ./...` pass. Not
  verified: a real terminal (its paste markers, one that never sends the end
  marker, how the notices look), a device or a real peer, how long a helper
  backend takes to type 4096 bytes, and `TestInputLoop`'s timing margins
  under much heavier load than this host's.
- Review round 1 asked for docs fixes only: this entry named
  `peer.Client.Lease()` and said the old fuzz test missed a decoder panic
  (it was a planted mutation), and the README said scterm tells whenever a
  paste is not sent, though the menu, ungrabbed Zellij and control off stop
  one silently. Round 2 fixes those and takes four of the review's
  suggestions: the two refusals fit the 50 cells an 80-column status row has
  after its hints (48 cells at most; the device's name is left out, since a
  name longer than 7 cells would push out `--takeover`); the panic notice
  above lost the paste's length; the decoder's deadlines moved to the
  monotonic clock; and guard tests for the review's mutants V02, V03 and V04
  (`TestNoHintOncePasteIsOver`, and a Ctrl+C right after a paste's end
  marker, in the same read, is a key). Also new: `TestInputClockIsMonotonic`
  and `TestPasteRefusalsFitTheStatusRow`; `TestPasteNoticesAreNotReplayed`
  pins the panic notice in the replay. The 250 ms pause after a cancel
  stays: on the fake clock a longer pause only moves the threshold for a
  pasted Ctrl+C's late rest (with the 250 ms pause it is typed from 250 ms
  on, with a 1 s pause from 1 s on) and drops the user's own keys after
  every Ctrl+C for longer (at 1 s, ten keys typed 300 ms apart after a
  cancel are all dropped).
- Verification of round 2, in the builder's tree (`743400f`: `e0919d4` plus
  parts 26 round 2, 27, 28 round 1 and 29), with iso.env: gofmt, vet, the
  build, `go test ./...` (364 top-level tests pass and 3 skip; 361 and 3
  before), `-race` on the module and `-race -count=3` on 86 input, paste,
  notice and render tests, and the 9 new, changed or affected tests 20 times
  plain and 5 times under `-race`. The four tests of the panic notice and
  the refusals' width fail on round 1's files for their reasons; the two
  guards pass there, since that code was right. V02, V03 and V04 survive
  round 1's tests and fail the new ones; round 1's M03 (already caught
  there, by `TestInputFuzzSplitReads`) and the builder's 7 mutations of the
  round-2 code fail the new ones too. Taken into the worktree: `input.go`
  and the two test files as built (unchanged there since the builder's
  base), the README merged three ways with exactly the builder's lines;
  gofmt, vet, `go test ./...` and the README test pass there.
- Review round 2 asked for docs fixes only: the sentence above on round 1's
  M03 said round 1's tests missed it, and the figures below for the
  notices' widths and V14, and above for the late rest's threshold, were
  rounded or too broad. Round 3 corrects them and names two more users of
  the wall clock.
- Left alone: mouse motion reports keep the 250 ms pause from ending while
  the mouse moves; a new paste that starts within 250 ms of a cancel is
  dropped up to its end marker; a paste holding a Ctrl+C character whose
  rest arrives more than 250 ms after it (a stalled link) has that rest
  typed as keys: a CR in it presses Enter on the device, and a pasted `ESC
  q` quits scterm (seen on the fake clock), so the README's "nothing in it
  acts as a key except Ctrl+C" does not hold there; a complete paste more
  than 2 s in transit shows the Ctrl+C hint, though a Ctrl+C typed then
  arrives after the end marker and reaches the device; the Ctrl+C hint can
  stay up to 4 s after the paste ended; a start marker cut more than 25 ms
  after its Esc is still Back plus typed text; ESC O alone still waits for
  its third byte with no time limit; the too-long notice shows where the
  paste would have gone nowhere (menu open, or ungrabbed in Zellij), and on
  an 80-column pane it is cut after "...more than the 26213" (for a size of
  six digits; of seven, after "...more than the 2621"); the two refusals
  show whole down to 78 columns (the typed one, for a size of four digits,
  down to 76) and are cut on narrower panes; only the input decoder's
  deadlines are monotonic (the notices, the action bar, the keyboard flash,
  quit arming, the menu, the mouse-drag coalescer and the frame-rate counter
  still use the wall clock); the review's mutants V11 (a lease state of ""
  passing) and V14 (equivalent: the hint deadline is 0 whenever what follows
  a cancel is being dropped); the browser's paste is unchanged.

## Update (2026-10-21: QoL overhaul, part 19: the browser player on phones, its looks and its accessibility)

- On a touch screen, a finger on the picture no longer brings up the HUD and
  the action bar: a tap or a drag is for the device, and a bar that came up
  under it could take the next tap. A new ⋯ button beside ☰ (`#bar-btn`,
  `aria-controls="bar"`, `aria-expanded`) shows the bar until ⋯ is pressed
  again or the picture is touched; that touch still reaches the device. ⋯
  shows in touch mode: a coarse pointer at load, then whichever pointer
  pressed last (a mouse that moves leaves touch mode). With a mouse, keys or
  the wheel the chrome shows and fades as before (`CHROME_IDLE_MS`, 2.5 s),
  but not while the mouse rests on the bar or a control of the chrome has the
  keyboard focus. ☰ is always shown and never inert. The bar is inert while
  faded, but not while the controls sheet is open; it stays hidden behind the
  sheet however the sheet was opened, and shows again when the sheet closes
  if the chrome is still up.
- The bar is one row, centred while it fits, that scrolls sideways where it
  does not (a wheel over it scrolls it). Only its buttons take clicks, so the
  strip between and beside them passes clicks to the device. On a coarse
  pointer every button is at least 44 px square (`--tap`).
- The page lays itself out inside the safe area (`viewport-fit=cover`, and
  `env(safe-area-inset-*)` for the bar, the HUD, ☰ and ⋯, the sound chip, the
  toast, the status card, the sheet and the canvas), and the picture fits
  what the safe area leaves.
- The page can be zoomed again (`user-scalable=no` is gone). The picture
  keeps `touch-action: none`, so pinches, drags and the three-finger tap on
  it stay the page's, and a pinch outside it zooms the page. The canvas is
  sized from the layout viewport, because a zoomable phone page grows
  `innerWidth` and `innerHeight` to take in overflow; before, the canvas's
  markup size overflowed a 390×844 window.
- Full screen: a button at the end of the bar (after a separator) and one in
  the sheet's footer, before Close, both absent where the browser has no
  Fullscreen API (iPhone Safari). They call `requestFullscreen`, with webkit
  fallbacks, carry `aria-pressed` and the title "Full screen" or "Leave full
  screen (Esc)", follow `fullscreenchange` (so leaving by Esc shows), and a
  refused request toasts "full screen is not available here". F11 stays the
  device's Collapse panels.
- Looks: `player.css` is rewritten around `:root` custom properties
  (colours, radii, spacing, control size, type) with one shared button style,
  so hover and press show on every button. Also `color-scheme: dark`, a 2 px
  `:focus-visible` ring, one global `prefers-reduced-motion` rule, darker
  panels with lighter dim text (the HUD's help row measured 6.11:1 over a
  white picture in Chrome, 2.78:1 before), an inline `data:` SVG favicon (so
  `/favicon.ico` is no longer asked for), the dead rules `#menu-btn.idle` and
  `#screen.grabbed` removed, and denser sheet rows below 760 px of height, so
  the sheet fits down to 1280×600.
- The sound chip gaps the part-12 entry left are closed:
  - A start that fails no longer builds and closes a context on every tap and
    key: the gesture listeners stop, and the chip says "Sound failed ·
    Alt+M". A click on it, or Alt+M, tries again (Alt+M retries rather than
    muting sound that never played).
  - The context's real rate is read back, and `processorOptions.ratio`
    (48000 divided by that rate) makes the ring resample linearly, in the
    worklet and in the compatibility path. In Chrome, with a context forced
    to 44.1 kHz, the base page dropped 12,224 frames in 4 s and this one
    none.
  - The chip's keyboard path is its chord, named in its text and in
    `aria-keyshortcuts`, since Tab and Enter belong to the device during a
    session.
  - Any chip but "Tap for sound" shows for 6 s when its words change, and
    after that only along with the HUD and the bar. Hidden, it takes no taps,
    so "Sound off · Alt+M" no longer sits over the picture's corner unmuting
    taps meant for the device.
  - On windows 760 px wide or narrower, the toast sits below ☰, ⋯ and the
    chip.
  - After a bye the chip shows nothing, nothing is listened for, and the
    context is suspended; Reconnect resumes it inside its click. The canvas's
    own `pcm.resume()` still resumed it after a bye (found in the builder's
    real-browser run); `Pcm.resume()` now returns early once the session has
    ended.
- Leftovers from parts 8 and 14:
  - After a bye, Alt+/ (the sheet) and Alt+S (the last picture) still work,
    from chord and button alike (`ENDED_ACTIONS`). The other chords, Alt+M
    and the F-keys are left to the browser, so F5 reloads (now pinned by a
    test). Device buttons and sheet rows say "the session has ended" and do
    not flash, and Quit asks for no second press. A paste after a bye is the
    browser's, with no toast.
  - Keyboard focus on ☰ or the bar no longer drops to the page when the
    chrome fades, and a press on the picture takes the focus off the page's
    buttons (except after a bye).
  - A held toggle chord (Alt+G, F12, Alt+I, Alt+/, Alt+S, Alt+M) runs once
    per press; device actions still repeat like a held key.
  - The stale comments about an inert ☰ (`player.css`, `index.html`) are gone
    with the rules they described.
- The builder's judgement calls, kept:
  - ⋯ is a handle of its own; ☰ keeps opening the sheet, as part 14 named and
    wired it.
  - A paste while the connection is lost (not after a bye) says "not
    connected: nothing pasted" instead of claiming it pasted.
  - The 760 px breakpoints sit a little above 736 px, so 1280×720 fits.
  - A finger tap in the 4 px gap between two bar buttons lands on the nearer
    button, because Chrome snaps a tap within about 10 px onto a clickable
    element; the strip itself passes clicks through.
- Existing checks changed on purpose. Page: "the chrome fades… ☰ inert" is
  now "keyboard focus on the bar's Controls button holds the chrome; ☰ is
  never inert"; the separators and the sheet's Tab ring include Full screen;
  the Quit colour-order check refers to the shared button rules. Audio: "a
  tap tries again", "the gestures are still listened for" and "a key press
  does" pinned part 12's gap and are now their opposites. The per-element
  reduced-motion checks are now the global rule (conn accepts a `*` rule).
  The stand-in DOMs gained `contains`, `matches` (`:focus-visible` and
  `:hover`), `removeAttribute`, scroll sizes and a Fullscreen API.
- Docs: README (the phone paragraph with ⋯, the keys after a session ends,
  the bar not fading under the mouse, Full screen with F11 and Esc, pinch
  zoom, the "Sound failed" chip and the quiet chip) and ARCHITECTURE (the
  ring resamples).
- Incident: before the builder forced its headful Chrome runs onto a private
  Xvfb display, up to three of them opened a Chrome window on the owner's
  Wayland desktop (`WAYLAND_DISPLAY` was set), about 30 s each, between 02:13
  and 02:27; the last one toggled full screen there. They showed only a
  served copy of the page with a fake socket: no adb, device or scterm. The
  builder's `browser/cdp.mjs` now refuses a headful run without an Xvfb
  display, adds `--ozone-platform=x11` and drops `WAYLAND_DISPLAY`. Browser
  runs for this project must be headless, or headful only on a private Xvfb
  display with `WAYLAND_DISPLAY` unset.
- Verification, in the builder's tree (`/tmp/opencode/dev-19`): gofmt,
  `go vet ./...`, `go test -count=1 ./...`, `-race` on the 57 web tests, and
  the node suites: input 130, worklet 35, pointer 17, conn 61, page 276 and
  audio 175 checks, all passing (130, 25, 17, 61, 202 and 137 on the base).
  Against the base production files the new checks fail as they should:
  worklet 6, page 65, audio 29. 34 of 34 mutations are caught. Real
  browsers, on a served copy of the page with a fake socket, new page against
  base page: Chrome 155 phone emulation (390×844, touch) 26 passed and 0
  failed against 5 and 18; Chrome desktop on Xvfb (mouse) 39/0 against
  10/25; Chrome audio 12/0 against 3/9; an `--app` window with `window.go`'s
  flags, headless 7/0 against 1/6, and on Xvfb 4/0; Firefox 157 17/0
  against 4/12. Taken into the worktree, `player.js` conflicted only in the
  comment above the chrome code, which part 14's last review round had
  reworded and this part replaces; the worktree had changed none of the
  other files since the builder's base, so all eight files are the builder's
  bytes, and the six node suites pass there with the same counts. On the
  review snapshot, which sits on parts 26 and 28 as reviewed, under a scratch
  sound server: gofmt, `go vet ./...`, the build, `go test -count=1 ./...`
  (342 top-level tests pass and 3 skip), the node suites with the counts
  above, `-race` on the builder's web filter (73 tests on this base, no
  race), and the Markdown check of README and ARCHITECTURE (33 links).
- Not verified: real phones (Android Chrome, iOS Safari) and real notches,
  only Chrome's mobile emulation and its safe-area override; in Firefox, the
  focus ring and entering full screen (its window never had the focus);
  full screen under a real window manager; real 44.1 kHz audio hardware (a
  forced context in muted Chrome stood in); Safari's `webkitAudioContext`
  and webkit full-screen prefixes; screen readers announcing `aria-pressed`
  and `aria-keyshortcuts`; taps while the page is pinch-zoomed.
- Review round 1 (snapshot review-19, on parts 26 and 28 as then reviewed)
  passed. Round 2 rebuilds this part on the branch tip, where parts 26 and
  28 have landed, and takes the review's wording notes: the README says to
  swipe on the bar's buttons (a swipe that starts between them goes to the
  device), this entry says the bar shows again after the sheet only while
  the chrome is up, and `player.css`'s header no longer says the HUD appears
  only when the pointer moves. Left for a later change, from the review:
  keyboard focus left on the bar's Controls button (after Esc closes a sheet
  it opened) holds the chrome up for the rest of a session, though Tab,
  Enter and Space go to the device; in touch mode a key, a soft keyboard's
  too, still brings up the bar, and the next tap there runs a bar button (as
  before this part); with a ratio other than 1 the ring counts an underrun
  for every output frame it cannot fill, where the frame-for-frame path
  counts one per block, so the HUD's `u<N>` runs far higher on a 44.1 kHz
  context (part 12 left the uneven counting alone); a chip that changes A,
  B, A within 6 s can go quiet early; no check pins a press on the picture
  after a bye leaving Reconnect focused, a mouse taking over dropping the ⋯
  pin, closing the sheet by touch showing a pinned bar again, a coarse
  pointer at load starting with the bar down, returning to the tab in touch
  mode, `room()` keeping the side insets, Alt+M unmuting when the sound both
  failed and is muted, or the fade waiting out an open sheet (the review saw
  three of them work in Chrome); on a portrait phone the bars beside the
  picture are about 5 px wide, so there is little room to pinch the page.
  On round 2's snapshot (the branch tip with part 28 as rebuilt), under a
  scratch sound server: gofmt, `go vet ./...`, the build, `go test -count=1
  ./...` (342 top-level tests pass and 3 skip), the six node suites with the
  counts above, `-race` on the builder's web filter (73 tests, no race), and
  the Markdown check of README and ARCHITECTURE (33 links).
- Left alone: `web.go`, `webui.go`, `websocket.go` and the CSP; the canvas's
  direct `pcm.resume()` call (guarded inside `resume()` instead); browser
  touch adjustment; at 390×844 the sheet's 17 rows still scroll.

## Update (2026-10-21: QoL overhaul, part 28: the browser display refuses names it is not served by)

- DNS rebinding: a website that points its own name at this computer could
  load the page and drive the device through it, since the WebSocket
  origin check compares the Origin with the Host header, and both then name
  the website. Now `run()` wraps the whole mux in `hostCheck`, so every
  request (the page, its files, `/ws`, unknown paths and any route added
  later) must name the server in its Host header by an IP address (IPv6 in
  brackets), `localhost` or a name ending in `.localhost`, the `--web-addr`
  value when that is a name, or a name in the new `SCTERM_WEB_HOSTS`
  (comma-separated), on any port; a final dot is ignored. This computer's
  host name, with or without `.local`, in any case (and, for a host name
  with dots, its first label with `.local`), is allowed only when the
  server listens on an address that is not a loopback address (`0.0.0.0`
  and `::` count as not loopback; an address that cannot be read counts as
  loopback). At the default `127.0.0.1` those names get the 403 like any
  other: someone on the network who can answer for the name (mDNS has no
  authentication) could otherwise serve a page under it and then point the
  name at `127.0.0.1`. Any other name, and an empty or unreadable Host (a
  name may hold only ASCII letters, digits, `-`, `.` and `_`, so
  `a*b.localhost` is refused too), gets a 403 with one line of plain text
  and none of the page: "scterm does not serve the page by this name. Open
  it by address, as http://ADDR/, or start scterm with the name in
  SCTERM_WEB_HOSTS.", where ADDR is the address the request reached, so a
  viewer of `--web-addr 0.0.0.0` on the network is told an address that
  works. The Origin check is unchanged and still runs on `/ws`.
- At start, each `SCTERM_WEB_HOSTS` entry that is not a name, or a name and
  a port, is reported once on stderr and ignored: `scterm: web: ignored
  "ENTRY" in SCTERM_WEB_HOSTS: it is not a name, or a name and a port (such
  as mirror.lan or mirror.lan:8443)`.
- The terminal reports each refused name once, for the first 16 names per
  server, then one line for any more ("scterm: web: refused a request
  addressed to "NAME"; if you opened it, use http://ADDR/ or start scterm
  with the name in SCTERM_WEB_HOSTS"), quoted and cut at 253 bytes. Not
  through `logOnce`, whose map never shrinks: a page can make the browser
  try any number of names.
- Docs: the README's Browser warning (what the check stops, and what it
  does not: anyone who reaches the port by this computer's address, and,
  off loopback only, someone on the network who can answer for this
  computer's name), the README's environment table, the `scterm -h`
  Environment line (it ends "IP addresses and localhost always work"), and
  the access-control bullet in `docs/ARCHITECTURE.md` (net/http answers 400
  before the check to two Host headers, a malformed Host, or an HTTP/1.1
  request with none).
- Review round 1 found this computer's names allowed at the default
  loopback address (mDNS spoofing), the overflow test counting entries
  rather than occurrences, no `.local` lookalike among the refused names,
  this entry saying that `check.mjs` and `gesture.mjs` worked when they had
  been run only up to the WebSocket, and eleven surviving mutants. Round 2
  allows the names off loopback only, computed by a pure function
  (`webHostNames`) from the listener's real address, and adds the warning,
  the character rule, the first-label name and tests for all of it.
- Tests: `web_host_test.go` (the rebinding name refused on `/`, an asset
  and `/ws`; every allowed form served, with and without a port; lookalike
  and malformed names refused; this computer's names refused at loopback;
  one report per name in any form, exactly the first 16, and one overflow
  line counted by occurrences; a long name cut; every method refused; the
  suggested address following the address reached; the addresses
  `--window` opens served; the warning; the docs) and, in `web_test.go`,
  `TestWebAddrNameIsServed`, `TestWebServesThisComputersNamesOnlyOnTheNetwork`
  and `TestWebHostsEntriesMustBeNames`.
- Verification. Round 1's builder: gofmt, vet, the build, `go test ./...`
  (the six node suites inside), `-race` on the web tests and the new tests
  20 times, plain and under `-race`; the new tests failed on the base
  files, and 20 mutations were each caught; `check.mjs` and `gesture.mjs`
  were run against a test server with no device, only up to the WebSocket
  connection (the page, its files and the upgrade; no frames or sound).
  Round 2's builder, on `f1c25f3` with every part then uncommitted: gofmt,
  vet, the build, `go test ./...` (351 top-level tests pass and 3 skip,
  against 344 and 3 on its base: the 7 more are round 2's), `-race` on the
  65 web and host tests, and the new and changed tests 20 times, plain and
  under `-race`; the two new real-server tests fail on the base files and
  the two new unit tests on a shim of the base rule, each for its reason;
  30 of 31 mutations are caught, all eleven of the review's survivors and
  the names-at-loopback one included. The survivor, `run()` passing no
  address, behaves like loopback at `127.0.0.1` and needs a listener that
  is not loopback to tell apart. Round 2 was taken in with `web.go`,
  `web_test.go` and `web_host_test.go` as built and `README.md`,
  `docs/ARCHITECTURE.md` and `flags.go` merged three ways onto the
  worktree, cleanly. For review, this part was rebuilt alone on `e0919d4`
  plus part 26 (which lands first) by replaying both rounds onto those
  files; with the other uncommitted parts replayed on top, that gives the
  worktree's files exactly. On that snapshot, with iso.env: gofmt, vet, the
  build, `go test ./...` (342 top-level tests pass and 3 skip), `-race
  -count=3` on the web tests (207 passes over 69 tests, no races), the host
  tests 20 times and 5 times under `-race`, the docs tests and the docs
  checker. Not run: a real browser or `--window` launch, real DNS
  rebinding or mDNS spoofing (the tests send the headers such a page
  would), a device, and a listener on an address that is not loopback (so
  the names off loopback are shown only by the unit test).
- Left alone: `--window --web-addr 0.0.0.0` opens the placeholder address
  `http://<this-host>:PORT/` (older than this part); the names are read once
  at start, so a renamed host or a changed `SCTERM_WEB_HOSTS` needs a
  restart; behind a reverse proxy, the proxy must pass the browser's Host
  through (or the WebSocket's Origin check refuses the page), and the
  proxy's name must be in `SCTERM_WEB_HOSTS` (from reading the code; not
  run); earlier entries that say the check does not stop DNS rebinding
  describe the code before this part.

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
