# Native Android client and cross-mode parity development plan

Status: proposed implementation plan; no Android implementation is completed by
this document. Created 2026-09-22 from source baseline
`e48af13fd32f8e3fa37f2c2d6f72b0122406b545` on `codex/android-client`.

## 1. Start here: instructions for the next developer or LLM

Read this document, [the current checkpoint](../.agent/HANDOFF.md), and all
applicable `AGENTS.md` instructions before changing code. The user requested an
installable native Android client with the lowest practical latency and complete
functional parity, including closing gaps between existing modes.

The checkpoint is a navigation aid, not proof of repository state. Git, source,
live task ownership, and reproducible evidence override stale checkboxes. This
plan is not permission to discard unrelated changes, operate physical devices,
deploy, or publish a release without the authorization required by the active
session and repository instructions.

### Resume and reconciliation procedure

1. Inspect the actual checkout and all worktrees before editing:

   ```sh
   git status --short --branch
   git status --porcelain=v1 --untracked-files=all
   git branch --show-current
   git rev-parse HEAD
   git worktree list
   git log -15 --oneline --decorate
   git diff --stat
   git diff --cached --stat
   git diff -- README.md docs/ANDROID_CLIENT_PLAN.md .agent/HANDOFF.md
   ```

2. Read applicable parent and repository instructions, CI configuration,
   manifests, and relevant source/tests. Inspect the full scoped staged and
   unstaged diffs and relevant untracked files; `git diff` omits untracked files.
3. Read the checkpoint's task ledger, active claims, evidence, blockers, and next
   action. Check recent commits touching the proposed task's paths. Use
   `git show <commit> -- <paths>` and `git merge-base --is-ancestor <commit> HEAD`
   to distinguish implemented-on-this-branch from implemented elsewhere.
4. Inspect upstream state using `git branch -vv` and `git remote -v`. Remote
   tracking refs can be stale. Fetch when authorized/available before claiming a
   commit is published; otherwise record that publication is unverified. Do not
   merge or reset merely to make the checkpoint agree with a remote.
5. Check available task/session tooling for active work. Do not assume another
   developer stopped because a timestamp is old. If ownership cannot be checked,
   avoid overlapping edits and record that uncertainty. Do not automatically
   start agents; follow the current session's delegation rules.
6. Reconcile task status using evidence. A file existing is not proof of
   completion; a commit is not proof of tests, device behavior, or publication.
   Record partial implementations and missing validation instead of restarting
   them or marking them complete.
7. Choose the earliest unblocked dependency, claim a bounded path set, and state
   its invariant, verification, and stop conditions. Preserve unrelated work.
8. Implement one coherent change; verify; update the checkpoint before freezing
   its review snapshot; obtain independent review as required by repository
   instructions; commit only the reviewed allowlist; push according to policy.
9. At interruption or handoff, record exact changed paths, commands/results,
   remaining work, owner/session, branch/worktree, and next action. Never describe
   uncommitted or unreviewed work as committed or complete.

### State and evidence conventions

Use `NOT_STARTED`, `IN_PROGRESS`, `BLOCKED`, `IMPLEMENTED_UNVERIFIED`,
`VERIFIED`, or `DEFERRED` for engineering state. Track review, commit, and push
separately; they are not synonyms for verification. `VERIFIED` requires the
task-specific exit criteria and evidence links. A missing physical test remains
explicitly unverified even if host tests pass.

For every work item record: ID, dependencies, owner/session, branch/worktree,
base SHA, paths, engineering state, tests/evidence, review identity/verdict,
commit SHAs, publication state, blocker, and next action. Release criteria cannot
be satisfied by silently changing an item to `DEFERRED`.

Use four claim labels where needed: **observation** (source/output), **derived**
(calculation with inputs), **hypothesis** (needs measurement), and **unknown**.
Record the Android device models/API levels, source settings, browser/runtime
versions, network setup, and tested revision with performance/device evidence.

Avoid self-referential bookkeeping: a commit cannot contain its own final SHA.
Record the code commit in a subsequent checkpoint update, or resolve the
checkpoint's containing commit with `git log -1 --format='%H' -- <path>`.
Review the exact final checkpoint content too; do not modify it after PASS and
reuse that approval. Put the final review identity in the commit message when
necessary, and recover publication from Git instead of preclaiming success.

## 2. Scope, assumptions, and decisions

### Required scope

- Installable native Android receiving/control application.
- Host-connected operation equivalent to connecting a browser to `scterm`.
- Equivalent actions and device effects across TUI, web, window, and Android.
- Explicit diagnosis and closure of existing-client parity defects.
- Hardware video decoding, bounded latency, slow-client isolation, resilient
  input state, audio routing, reconnect, and measurable performance.
- Automated conformance tests, physical validation, reproducible APK packaging,
  and a durable development checkpoint.

### Proposed defaults, to validate during A00/A01

| Decision | Proposed default | Revisit when |
| --- | --- | --- |
| Topology | Existing Linux host owns ADB/source session; APK connects remotely | User needs hostless Android-to-Android operation |
| APK stack | Kotlin UI/session/input, MediaCodec + SurfaceView, Oboe JNI audio | Device measurements justify a narrower alternative |
| Minimum receiver | Android 8.1/API 27; prefer Android 11+ in low-latency validation | Actual receiver inventory requires another floor |
| Video | Forward original H.264; keep JPEG compatibility | Device codec/transport measurements justify another codec |
| Initial transport | Separate TLS WebSockets for control, video, audio | Packet-loss measurements justify datagram transport |
| Input compatibility | Existing gestures retained; explicit immediate-input policy | Shared UX decision supersedes the compatibility policy |
| Control ownership | One active controller, multiple viewers, explicit takeover | A tested multi-controller requirement is added |
| Background behavior | Suspend media/input and release presses; reconnect on return | Background playback is explicitly required |

These defaults are recommendations, not recorded user approval of every product
detail. Continue independent implementation using reversible defaults; clarify
only decisions that materially block work. Do not invent benchmark hardware,
signing keys, SDK versions, or results.

Hostless ADB is extension X01, not a hidden dependency of the first APK. HEVC/AV1,
gamepad axes, arbitrary remote app launching, recording, WAN relay infrastructure,
and background services are not part of the initial parity baseline. Existing
CLI capture controls remain host-owned; runtime remote reconfiguration is a
separate negotiated capability rather than assumed functionality.

## 3. Baseline source map and verified observations

Paths and symbols below refer to the baseline SHA. Re-read them after refactors.
Source observations do not establish physical-device behavior.

| Area | Source | Observation |
| --- | --- | --- |
| Bootstrap | `main.go`, `adb.go`, `link.go` | Embedded vendored server, external host ADB, reverse tunnel with forward fallback, video/audio/control sockets |
| Wire protocol | `protocol.go`; vendored `device/Streamer.java` | Codec IDs, 12-byte headers, session geometry, config/key flags, PTS; match this vendored revision |
| Video | `stream.go: runVideo`, `decode.go`, `native.c` | H.264 decoded on host and scaled into pooled BGR canvases |
| Web media | `web.go: sendFrame`, `pushPCM`, `sendLoop` | JPEG/raw video; PCM audio; per-client frame mailbox; shared connection for media/status |
| Window | `window.go: openWindow`, `findBrowser` | Browser app-mode launcher using the same web player; distinct lifecycle/environment behavior |
| Input | `appkeys.go`, `input.go`, `webinput.go`, `control.go` | Shared action dispatch plus frontend mappings; single app drag state; immediate key down/up helper |
| Browser | `web/player.js`, `web/pointer.js`, `web/pcm-worklet.js` | Keyboard/IME, three-finger keyboard gesture, local pointer feedback and audio |
| Frontend UI | `actionbar.go`, `menu.go`, `keyboard.go`, `webui.go` | TUI overlays/action tables and embedded browser assets |
| Audio | `stream.go: runAudio`, `audio.go`, `audiofollow.go`, `audio_select_test.go` | Host decode to PCM, PulseAudio sink, browser taps, routing and local gain |
| Verification | `web*_test.go`, `ctrl_test.go`, `wire_test.go`, `audio_routing_test.go`, `web/*_test.mjs` | Existing wire, mapping, geometry, audio, slow-client and browser tests |
| CI | `.github/workflows/build.yml`, `go.mod` | Go 1.27; vet/test/build; FFmpeg/PulseAudio dependencies; Linux binary artifacts/releases |

At plan creation there is no Android receiver application/build. The vendored
scrcpy Android source is the source-device server, not that client.

Historical checks from the planning session (2026-09-22):

- `go test ./...`: initially blocked by the read-only default Go build cache.
- `GOCACHE=/tmp/scterm-plan-go-cache go test ./...`: compiled, but socket tests
  failed under restricted TCP/Unix socket permissions.
- The same command with permitted local socket access: `ok scterm 7.591s`.
- `node web/input_test.mjs`: 100 passed, 0 failed.
- `node web/pointer_test.mjs`: 17 passed, 0 failed.
- `node web/worklet_test.mjs`: 17 passed, 0 failed.
- Local versions subsequently inspected: Go `go1.27.1-X:nodwarf5 linux/amd64`,
  Node `v26.8.2`.
- No APK, emulator, source-device interaction, physical latency, or audio
  measurement was performed. No vet/build/race result is claimed here.

These results are a historical baseline, not evidence for future changed code.

## 4. Target architecture and invariants

```text
source Android / vendored scrcpy server
  -> existing ADB video/audio/control transport
  -> host packet reader and session manager
       -> compressed media subscribers -> native Android decode/output
       -> decoded consumers -> TUI
                            -> JPEG/raw compatibility -> web/window

all frontend input adapters -> shared action/input dispatcher -> source control
```

Refactor incrementally. Avoid importing the Linux/cgo application into Android
or rewriting unrelated terminal behavior.

### Host media subscriptions

- Split packet reading from decode/render consumption in `stream.go`.
- Immutable packet metadata: stream ID/generation, codec/config, sequence, PTS,
  keyframe flag, geometry/crop where relevant, payload ownership.
- One source session and packet reader per source stream. Multiple subscriptions
  do not each launch a server or read the same socket independently.
- Explicit pooled-buffer ownership/refcounts or equivalent lifetime mechanism;
  consumers cannot retain a reused reader buffer.
- Bounded subscriber queues; source readers never wait on a slow client.
- Start host video decode only when a decoded consumer needs it; native-only
  mode avoids BGR allocation/scaling/JPEG work. Reattach consumers safely.
- Preserve existing dump/debug behavior through explicit consumers.
- Separate capture/stream availability from host playback-sink availability.
  A failed PulseAudio sink must not silently disable remote audio.

### Compressed-video recovery

Do not copy JPEG's one-slot drop-old behavior onto dependent H.264 packets.
Maintain reference continuity while discarding obsolete decoded output, or
resynchronize the affected subscriber with configuration and a fresh keyframe.
Bound queued bytes and packet age. On discontinuity: invalidate old generation,
flush decoder, request a rate-limited/coalesced keyframe, send matching config and
geometry, resume only at a decodable point. New joins on static screens need the
same bootstrap. Verify whether resetvideo delivers the required recovery in this
vendored server before relying on it. Never replay an old keyframe with missing
dependent frames or mismatched configuration.

## 5. Versioned protocol and session design

Preserve legacy web protocol v1 during migration. Introduce a negotiated protocol
and pin an actual schema before implementing encoders/decoders. Do not infer
binary layouts from stale comments; use serializer/parser fixtures.

Required schema fields and behaviors:

- Handshake: supported versions, client identity, session/channel binding,
  authentication, capabilities, selected formats, limits and errors.
- Video: codec/profile/configuration, PTS units, sequence, keyframe/config flags,
  dimensions, stream generation, payload framing and maximum lengths.
- Audio: encoded format/config or PCM sample format/rate/channels, PTS, sample
  count, discontinuity, and sequence.
- Input: action IDs; key press/down/up/repeat/meta; pointer identity/action/tool/
  buttons/pressure; scroll axes; text/paste; coordinate space and geometry epoch.
- State: available streams/control, source volume, local-vs-remote state,
  ownership, acknowledgments/errors, heartbeat and telemetry.
- Lifecycle: disconnect versus end-source-session, reconnect negotiation,
  invalid credentials, incompatible version, and control-owner transfer.

Use separate TLS WebSocket connections for control/video/audio initially, bound
to one authenticated session. No unbounded pre-connect command queue; do not
replay stale presses or text after reconnect. Input serialization must preserve
ordering without being blocked by video encoding. Use complete writes, bounded
frame sizes and deadlines; cap per-client resource usage. Separate connections
do not eliminate shared-network congestion or TCP head-of-line blocking.

Add pairing, credential revocation, and certificate validation/pinning with an
explicit trust-establishment flow. Bind subsequent channels to the authenticated
session. Keep secrets out of URLs/logs/artifacts. Existing Origin checks remain
useful for browser requests but are not native-client authentication. Retain
localhost compatibility; secure remote exposure needs an explicit configuration
and migration story for old clients.

Distinguish viewer disconnect from host/session termination. Preserve legacy
`quit` behavior through compatibility mapping; make global termination explicit
and authorized in the new protocol. Test `--window` last-viewer exit separately
from a host intentionally serving multiple persistent viewers.

## 6. Native Android implementation

### Project and lifecycle

Create `android/` with a pinned Gradle wrapper, Android plugin/Kotlin versions,
compile/target/min SDKs, NDK and dependency versions selected against current
official requirements at implementation time. Use Kotlin for session/UI/input
and a small JNI module for Oboe. Start with ARM64 device and x86_64 emulator
builds; add other ABIs only when the device inventory requires them.

Use a session abstraction so host-connected transport and optional direct ADB can
share rendering/input/audio. Keep network and codec work off the UI thread. UI
state is observable without per-frame recomposition or bitmap conversion. Handle
foreground/background, process recreation, network changes, surface loss,
orientation, IME insets, display cutouts, and focus loss. Persist connection
preferences safely; never persist/replay active presses.

### Video

- MediaCodec H.264 output directly to SurfaceView; no normal-frame CPU readback.
- Inspect capabilities and configure low-latency mode only when supported.
  Enumerate fallback decoders and report the selected path in diagnostics.
- Pass the correct codec-specific data; validate Annex B/access-unit boundaries
  against actual server packets and codec requirements.
- Handle output format/crop changes, surface recreation and generation changes.
- Release stale decoded output without rendering; do not accumulate presentation
  deadlines far into the future. Measure vsync/output scheduling behavior.
- Negotiate unsupported profile/resolution explicitly; do not silently pretend
  software decode meets hardware performance targets.
- Capture screenshots on demand via PixelCopy or an equivalent surface capture;
  exclude controls if matching the browser screenshot contract. Save/share via
  Android storage APIs, handling unavailable/no-frame states.

### Audio

Implement timestamped host-decoded PCM first for source codec compatibility;
then add Opus forwarding/client decode and retain PCM fallback for unsupported
source formats. Determine MediaCodec versus a pinned libopus decoder from
measured latency, support, maintenance, and licensing; do not ship two paths
without evidence they are needed.

Use Oboe low-latency callbacks; request exclusive mode with shared fallback,
honor the actual output sample rate/burst size, and avoid allocation, locks,
network/file I/O, and logging on the audio callback. Begin with an experimental
10–20 ms adaptive application jitter target and tune from underruns. Account
separately for decoder, application, system, and Bluetooth buffering.

Preserve timestamps/sample counts through host PCM conversion. Maintain a
source-to-client clock mapping; correct drift with bounded resampling or a
documented discontinuity policy. Do not equate clocks on separate machines.
Define interactive latency versus A/V synchronization policy and expose measured
skew. Handle audio focus, interruptions, route/device changes, and stream rebuild.

Local gain/mute belongs to the receiving client. Source volume and source/host
audio duplication remain distinct actions/settings. Test no-audio, silence,
missing capture capability, host-sink failure, and duplicate-output cases.

### Input and UI

Provide every baseline action through touch-accessible controls, not only
shortcuts. Support physical keyboards, IME composition/commit/deletion without
duplicates, mouse/stylus, drag/scroll/cancel, screenshot and status. Route remote
Back separately from closing a local sheet/IME or leaving the client. Android
system-reserved keys require explicit UI alternatives, not promises to intercept
all Home/Power shortcuts.

Map points against the displayed content rectangle, crop and current geometry
generation; exclude letterboxes. Test rotation/IME resizing during a gesture.
Release all active input on cancellation, blur, disconnect, ownership loss, and
suspension. Track press state per controller. Legacy keyPress remains supported;
explicit down/up enables held keys where the frontend can report them.

Compatibility gesture policy preserves the existing three-finger keyboard
gesture. Immediate-input policy forwards the first touch immediately and uses an
explicit keyboard control instead. Offer the same policies in applicable web
and window clients. True multitouch is an explicit shared extension, not an
Android-only silent change: the present browser suppresses additional fingers.

## 7. Cross-mode parity ledger and gap closure

Create the executable action schema and conformance fixtures in A01/A02; this
table is the initial audit inventory, not a claim of complete current parity.

| Feature group | Required contract/scenarios |
| --- | --- |
| Navigation | Home, Back/wake, Menu, Recents; exact press sequences |
| System | Power, rotate, notifications/settings/collapse, reset video |
| Volume | Source volume ±/mute distinct from local gain/mute and duplication |
| Action access | Alt mnemonics/punctuation, F keys/aliases, toolbar/menu/sheet; equivalent accessible actions |
| Keyboard | ASCII, modifiers, D-pad, repeats, held keys where supported, reserved-key fallback |
| Text | IME commit/composition/delete, punctuation, Unicode, paste and device-to-client copy |
| Pointer | Tap/drag/up/cancel, right Back, middle Home, scroll, hover feedback |
| Gestures | Compatibility/immediate policy, three-finger toggle, optional true multitouch |
| Geometry | Aspect fit, resize, rotation, letterbox/crop, IME insets, stale epoch |
| Grab | What is routed locally/remotely; local state versus host status |
| Display | Live/static screen, screenshots, no-frame behavior, stream recovery |
| Audio | Local volume, mute restore, routes, disabled/failed capture, duplicate policy |
| Status | Source identity, geometry, stream availability, volume, client/performance stats |
| Lifecycle | Connect/reconnect, ownership, close/quit, focus/background, host/source loss |
| Flags | video/audio/control disabled; source codec/source selection and capture settings |

Parity means equivalent actions and device effects, not identical platform UI.
Record intentional exceptions: TUI screenshots may be PPM while graphical
clients use PNG; ordinary terminal protocols may lack key-up/multitouch; browser
and Android system shortcuts/clipboard APIs have platform restrictions. Provide
an equivalent action when possible. Do not label an unimplemented feature a
platform exception without evidence.

### Initial gap register

All rows start `NOT_STARTED`; source evidence needs reproduction. Priority P0
means input integrity/data safety, P1 means baseline functionality/performance,
P2 means shared capability or polish after baseline agreement.

| ID | Priority | Source observation / candidate gap | Required closure evidence |
| --- | --- | --- | --- |
| G01 | P1 | TUI middle-click sends Home; inspected browser pointer path ignores non-left down and lacks middle Home | Failing-before browser fixture, one Home per middle click, real web/window mouse check |
| G02 | P0 | `webinput.go: dispatch` drops incoming event when 512-entry inbox is full, including possible release | Saturation test; bounded move coalescing; protected transitions or deterministic cancel/reset; no stuck input |
| G03 | P0 | App-wide drag state; `webClient.close` does not explicitly release client-owned input | Per-controller state/ownership; disconnect/blur/takeover tests with no interference |
| G04 | P1 | Browser has 60 ms initial-touch grace; TUI sends mouse down directly | Shared selectable policies, gesture compatibility fixtures and measured immediate-input latency |
| G05 | P1 | 8 ms movement threshold is flushed by 16 ms app tick | Deadline instrumentation; independent input scheduling; no stale move after release |
| G06 | P1 | `controller.injectKey` sends immediate down/up | Backward-compatible keyPress plus down/up/repeat; held-key/modifier tests; documented terminal limits |
| G07 | P1 | Clipboard replies are logged; server text injection maps characters to key events | Explicit copy/paste/ack path, Unicode/IME tests, no sensitive clipboard logging; platform capability fallback |
| G08 | P1 | TUI/browser grab affects different routing and state | Defined common semantics with intentional exceptions; UI state and actual wire routing agree |
| G09 | P1 | Device mute maps to 91; source notes distinguish media mute 164 | Physical reproduction, intended media-mute decision, synchronized correction in all adapters |
| G10 | P1 | Audio and status depend on host sink paths; browser gain is local | Reproduce sink-failure and routing cases; capture availability independent of host playback; no unintended duplicate |
| G11 | P1 | Window last-viewer close stops session; web quit is host-wide | Explicit disconnect/end-session contract, legacy mapping, multi-viewer lifecycle tests |
| G12 | P2 | Screenshot formats and overlays differ; UI availability differs | Content/no-frame/save tests, accepted format exceptions and accessible action equivalents |
| G13 | P1 | Go/JS action maps duplicated; some dispatch paths duplicated | Generated maps from schema plus independent golden expectations; drift check in CI |
| G14 | P1 | Browser multi-finger suppression differs from potential native multitouch | Baseline gesture parity first; negotiate/test true multitouch across capable clients if enabled |

### Process for each gap

1. Inspect current production code and reproduce on the relevant revision.
2. Classify defect, accepted platform difference, or shared missing capability.
3. Record expected behavior independently (user intent/protocol/platform source).
4. Add a failing-before test through the real frontend adapter/dispatcher.
5. Fix the common layer, then every affected frontend. Avoid unrelated cleanup.
6. Run scoped tests and broader affected suites; perform the physical check if
   the claim depends on hardware/browser behavior.
7. Record before/after evidence, reviewed commit and limitations. Mark closed only
   after the exit criteria; a host stub is not a real IME/audio/device result.

## 8. Milestones, dependencies, and acceptance criteria

The mutable task ledger lives in `.agent/HANDOFF.md`. All required A tasks below
are initially NOT_STARTED; optional X01 is DEFERRED. Each milestone may require
several small reviewed commits.

### A00 — Reconcile state and establish validation inventory

Dependencies: none. Inspect current work, instructions, CI, and baseline tests.
Record source/receiver devices, OS versions, network topology and available
hardware without operating devices absent authorization. Finalize topology/min
SDK defaults or record unanswered questions. Add a reproducible measurement
recipe. Exit: reconciled ledger, baseline checks and explicit unknowns; no
invented performance numbers.

### A01 — Specify parity and reproduce gaps

Depends on A00. Build feature-by-mode matrix and gap reproductions G01–G14.
Decide action/local ownership, input policies, disabled-capability semantics,
disconnect/quit and platform exceptions. Define test fixtures before fixing
behavior. Exit: each baseline action has a contract, status and verification
method; P0 cases have discriminating reproductions or precise remaining blockers.

### A02 — Shared action/input contract and existing-client repairs

Depends on A01. Add `protocol/` schema and independent fixtures; generate Go/JS
maps and later Kotlin maps. Extract only the shared dispatch/state needed, using
incremental packages/files rather than an unrelated rewrite. Close G02/G03
first, then functional gaps and shared latency policy. Exit: corrected baseline
TUI/web/window behavior, generation drift check, conformance tests, explicit
remaining physical checks. New capabilities must not break legacy clients.

### A03 — Packet subscriptions and compressed media recovery

Depends on A00 and the session/input contracts in A01; can proceed independently
of unrelated UI fixes when ownership/path boundaries are explicit. Extract media
packet reader/subscriptions, configuration lifetime, generation and recovery.
Keep decoded consumers working. Exit: native-only synthetic subscriber requires
no host video decode; mixed subscribers work; slow clients cannot block source
or other clients; bounds, ownership, late join and rotation tests pass.

### A04 — Authenticated versioned transport

Depends on A02/A03 contracts. Implement negotiated channels, bounded parsers,
pairing/trust/credentials, timeouts, ownership, reconnect and capability/error
reporting. Specify planned CLI/config options before adding them; none of the
proposed new options exists at baseline. Exit: old web clients retain supported
behavior; new test client handles media/control; malformed/unauthorized clients
are rejected; slow/disconnected channels clean up; remote setup is documented.

### A05 — Installable Android video vertical slice

Depends on A03/A04. Scaffold `android/`, connect/pair, hardware H.264 surface
decode, geometry, stats and reconnect. Exit: debug APK installs on a nominated
physical receiver and handles static join, rotation, surface recreation and
network loss; selected decoder is visible. Emulator-only success is insufficient.

### A06 — Native audio

Depends on A04/A05. Timestamped PCM, Oboe output, gain/mute, route/focus recovery,
then compressed Opus path with fallback. Exit: source codec variants supported
through negotiated direct/fallback paths; no accidental duplication; measured
underruns, latency and drift on physical hardware; unavailable audio does not
break video/control.

### A07 — Native controls and full baseline parity

Depends on A02/A04/A05; audio rows depend on A06. Implement toolbar, keyboard/IME,
mouse/touch, local screenshot, status, grab/ownership and lifecycle. Exit: all
baseline ledger rows pass or have evidence-backed accepted platform exceptions;
input integrity cases pass; shared enhancements are implemented in existing
capable clients too. Unavailable physical tests remain release blockers.

### A08 — Performance qualification and existing-browser fast path

Depends on A05–A07. Measure and optimize before/after; retain compatibility
policies. Add negotiated compressed browser decode where current browser APIs,
secure contexts and hardware support allow; keep JPEG fallback and test it.
Verify H.264 framing/config differences for the browser decoder. Consider
encoding a shared JPEG once per frame only after profiling establishes value.
Exit: published-in-repo benchmark recipe/results for defined hardware, no
functional regressions, mixed-client isolation and long-run limits met. Datagram
transport/alternate codecs require a separate evidence-backed decision.

### A09 — CI, packaging, and release qualification

Depends on A02–A08. Pin Node in CI, retain host tests, add Android unit/lint/build
and emulator smoke jobs. Store device qualification reports; signed release APK
and update install test; notices/license audit for embedded/native dependencies.
Do not commit signing keys or publish without required authorization. Exit:
installable reproducible artifact with checksum/build revision, compatibility
docs, rollback guidance, complete release gate and known-limitations list.

### X01 — Optional direct Android-to-Android provider

State: DEFERRED pending topology requirement. Reuse the same client media/input
interfaces, adding Android-side ADB authentication/pairing, vendored server
deployment/versioning, process/tunnel lifecycle and discovery. Validate wireless
debugging and USB-host permission/transport separately against official APIs and
real devices. No assumption of root or silent ADB authorization. Exit: hostless
session meets the same parity/performance criteria and cleans up sessions/keys;
record whether removal of the host actually improves end-to-end latency.

## 9. Test and performance strategy

### Automated checks

Existing canonical host checks (derive updates from CI/manifests):

```sh
go vet ./...
go test ./...
go build -trimpath -ldflags "-s -w" -o scterm .
node web/input_test.mjs
node web/pointer_test.mjs
node web/worklet_test.mjs
```

Use a writable `GOCACHE` when necessary and record that environment. Socket
tests need TCP/Unix socket permission. Never convert a permissions failure into
a product regression claim. Use race tests/fuzzing for changed concurrent/parser
paths where the actual build supports them; record exact invocation/results.
Android commands must be derived from the pinned wrapper/modules once created,
not presented as already runnable today.

Add tests for wire bounds/truncation/version skew; independent golden control
bytes; packet buffer lifetime; geometry epoch; key/pointer cancellation; mailbox
overflow; held modifiers; ownership conflicts; join/config/keyframe recovery;
no-frame screen; audio-disabled/capture-error/host-sink-error combinations;
reconnect with no stale input; source restart; surface/route changes; all
video/audio/control flag combinations. Ensure single-thread and concurrent
dispatch paths share semantics, including internal headless test paths without
assuming a currently unsupported CLI combination is public functionality.

### Physical and real-browser matrix

Validate nominated source and receiver Android versions/vendors; lower-end and
high-refresh receivers; Chromium and Firefox web/window where supported; desktop
and Android browser; physical keyboard/mouse and common IMEs. Cover good LAN,
congested Wi-Fi, added delay/loss, disconnect/rejoin, USB source transport, and
multiple viewers. Record actual coverage; do not claim every Android device.

Test Unicode/emoji/CJK, IME deletion/composition, source media mute, screenshots,
OS-reserved keys, protected content behavior, output audio routes and focus,
orientation while pressed, sleep/wake, sustained heat and power. Protected
content/capture restrictions require honest capability/error behavior, not a
promise to bypass platform security.

### Measurement definitions and provisional budgets

Use high-speed camera input-to-visible-response measurements with the same
workload/settings; internal timestamps cannot prove end-to-end physical latency.
Instrument source/host/client stages, clock-offset uncertainty, queue age, decode
and presentation, underruns, A/V skew, CPU/RSS/bandwidth, battery and thermal state.
Report sample count, warmup, percentiles and outliers; compare a browser on the
same receiver as the native app. Report 60/90/120 Hz separately.

| Metric | Provisional target, not a measured claim |
| --- | --- |
| Host compressed forwarding | p95 < 2 ms under declared workload, excluding source/network delay |
| Receiver video processing | p95 within 1–2 display intervals on nominated hardware |
| Healthy-LAN input-to-visible response | target p50 < 50 ms, p95 < 80 ms; qualify exact source/receiver/settings |
| Slow viewer | bounded memory/queue age, no sustained latency increase in healthy viewers |
| Stability | 30–60 minute active sessions without growing backlog, stuck input or resource leaks |
| Audio | choose underrun/skew/latency budgets from measured device baseline in A00/A06; never infer end-to-end delay from buffer size |

The targets may be limited by source capture, receiver display, or network.
Record bottlenecks and proposed changes before adjusting budgets; do not lower
them silently to declare success. Reliable recovery, audio quality and parity
are release gates alongside speed.

## 10. Planned code organization and change boundaries

Names below are proposed, not existing packages or mandatory mass migrations:

| Path | Responsibility |
| --- | --- |
| `protocol/` | Versioned schema, action catalog, independent golden fixtures, generation tooling |
| `internal/session/` | Session state, controller ownership, stream capability negotiation |
| `internal/media/` | Packet metadata, subscriptions, bounds, generation/config recovery |
| `internal/remote/` | Authenticated channels and native/browser protocol adapters |
| `android/` | Native receiving application and pinned Android build |
| `docs/validation/` | Reproducible test/benchmark recipes and non-sensitive results |
| `.agent/HANDOFF.md` | Current task state, claims, evidence and next action |

Keep `stream.go`, `web.go`, `webinput.go`, `appkeys.go`, `control.go`, `app.go`,
`input.go`, `flags.go`, `web/*`, CI and nearby tests in scoped incremental
changes. Do not modify vendored server source/binary until the build/update
workflow and need are established; any server change must include reproducible
source-to-embedded-artifact provenance and protocol compatibility checks.

## 11. Completion and release gate

- All required A tasks satisfy their exit criteria; G rows are resolved with
  evidence or explicitly accepted platform exceptions, not hidden deferrals.
- APK installs and upgrades on the declared physical support matrix.
- Existing TUI/web/window modes retain agreed behavior; shared fixes pass their
  conformance scenarios and disabled-capability cases.
- Latency/thermal/soak results are reproducible, with measured limitations.
- Pairing/revocation, ownership and disconnect cleanup pass; no credentials,
  clipboard contents or device-private data leak into diagnostic artifacts.
- Old/new protocol compatibility and JPEG fallback are tested.
- Host and Android CI checks pass for the release revision; device results name
  that revision or explain any subsequent nonfunctional delta.
- License notices, signing provenance, artifact checksum and installation,
  configuration, troubleshooting and rollback documentation are available.
- Independent review, scoped commits and publication status are recorded.

## 12. Reference sources

Consult current official docs when selecting SDKs, capabilities, and dependencies.
These references informed the plan on 2026-09-22; they are not pinned dependency
versions or evidence that a specific device supports a feature.

- [MediaCodec](https://developer.android.com/reference/android/media/MediaCodec)
- [MediaFormat low-latency settings](https://developer.android.com/reference/android/media/MediaFormat#KEY_LOW_LATENCY)
- [Android low-latency audio / Oboe](https://developer.android.com/games/sdk/oboe/low-latency-audio)
- [PixelCopy](https://developer.android.com/reference/android/view/PixelCopy)
- Source protocol authority: this repository's `protocol.go`, `control.go`, and
  `third_party/scrcpy-server-src/src/main/java/com/genymobile/scrcpy/` at the
  tested revision, together with independently derived wire fixtures.
