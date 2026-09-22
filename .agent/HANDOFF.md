# Android peer app / Go client / parity development checkpoint

Read [the complete plan](../docs/ANDROID_CLIENT_PLAN.md) and applicable
`AGENTS.md` instructions first. Reconcile this checkpoint against Git, current
source, live task ownership and test evidence before editing. Status here is a
snapshot, not proof that no other work has started.

## Snapshot

- Date: 2026-09-22.
- Scope revision 2: same Android APK includes server and client roles, paired
  Android A/B can control each other directly, and maintained Go TUI/web/window
  authenticate to APK servers while retaining existing ADB mode. Plan-only work.
- Revision base: `be4594e60b7a16d906a50772565cd1cfd3c2c0de` (the original plan,
  independently reviewed, committed and pushed in the prior turn). Local HEAD
  and `origin/codex/android-client` matched that commit at revision start.
- Superseded assumption: Android-to-Android is no longer optional X01 or dependent
  on a Go host. X01 now points to required B00–B05. ADB is an activation/legacy
  mechanism; peer protocol and APK hosting are separate requirements.
- Branch at authoring: `codex/android-client` (actual branch; retain it rather
  than renaming to `android-client` or switching to main).
- Source baseline: `e48af13fd32f8e3fa37f2c2d6f72b0122406b545`.
- Worktree at authoring: `/home/garyb/LLM-Projects/scrcpy-terminal`.
- Remote at authoring: `origin`, `https://github.com/pwnedbygary/scrcpy-terminal.git`.
- Pre-existing unrelated change: `.gitignore` modified by the user; excluded
  from this documentation change. Do not revert, stage or absorb it.
- Documentation paths: `README.md`, `docs/ANDROID_CLIENT_PLAN.md`,
  `.agent/HANDOFF.md`.
- No Android client/server code, new remote protocol, parity fixes, helper
  activation, physical tests or benchmarks have been implemented in this change.

## Current task ledger

No implementation task is claimed by this checkpoint. Check live sessions and
worktrees before treating a task as available. All ownership/commit/evidence
fields for implementation tasks are currently unset.

| ID | State | Dependencies | Next action |
| --- | --- | --- | --- |
| A00 | NOT_STARTED | none | Reconcile checkout; inventory both-role hardware; baseline/measurement recipe |
| B00 | NOT_STARTED | A00 | Prove Android serving privilege/backend feasibility and helper IPC/activation |
| A01 | NOT_STARTED | A00, B00 | Frontend-by-backend capability matrix; reproduce G01–G18 |
| A02 | NOT_STARTED | A01 | Shared action contract; input integrity fixes; existing-mode conformance |
| A03 | NOT_STARTED | A00, A01 contracts | Packet subscriptions and compressed recovery |
| A04 | NOT_STARTED | A02/A03, B00 contracts | Interoperable authentication, directional grants and transport |
| A05 | NOT_STARTED | A03, A04 | Installable hardware-video client slice and Android scaffold |
| B01 | NOT_STARTED | A04, A05, B00 | Same-APK target service, peer endpoint and lifecycle |
| B02 | NOT_STARTED | B00, B01 | Activated scrcpy-derived backend; provenance and full-parity checks |
| B03 | NOT_STARTED | B00, B01 | Public-API backend; consent and explicit limited capabilities |
| B04 | NOT_STARTED | A02/A03/A04, B01; physical exit B02/B03 | Go peer source in all display modes, no ADB dependency; legacy preserved |
| A06 | NOT_STARTED | A04, A05 | Timestamped PCM/Oboe and Opus path |
| B05 | NOT_STARTED | B02/B03/B04, A05/A06, A02 | A/B reversal, duplex/resource and Go interoperability qualification |
| A07 | NOT_STARTED | A02/A04/A05/B05; A06 for audio | Native UI controls and full baseline parity |
| A08 | NOT_STARTED | A05–A07, B01–B05 | Direct/Go/legacy performance and browser compressed path |
| A09 | NOT_STARTED | A02–A08, B00–B05 | CI, dual-role packaging, real screenshots, current README and release qualification |
| X01 | SUPERSEDED | replaced by B00–B05 | Historical ID only; do not resume as optional ADB-only work |

G01–G18 are all NOT_STARTED. G01–G14 retain the original source/parity findings;
G15–G18 track privilege feasibility, directional grants, Go source abstraction,
and duplex lifecycle/feedback respectively. Source findings are not physical
reproductions. No implementation owner/session is currently claimed here.

## Evidence and unknowns

Historical source baseline checks during the planning session:

- Default `go test ./...` blocked by build-cache permissions.
- `GOCACHE=/tmp/scterm-plan-go-cache go test ./...` blocked by sandbox local
  TCP/Unix socket restrictions; rerun with socket permission passed:
  `ok scterm 7.591s`.
- Browser Node suites passed: input 100, pointer 17, worklet 17; 0 failures each.
- Local versions inspected: Go `go1.27.1-X:nodwarf5 linux/amd64`, Node `v26.8.2`.
- No device/emulator/APK/latency/thermal/audio-route validation; no vet, build,
  or race-test result claimed from the planning session.

The plan's source map records observations. Revision 2 additionally inspected
`adb.go: startServer`, vendored `Server.java` and `wrappers/InputManager.java`:
current scrcpy runs through ADB shell/app_process and invokes privileged input
APIs. Bundling it in a regular APK does not confer those privileges.

Official Android MediaProjection, Accessibility, playback-capture, foreground
service and ADB references were checked during revision 2; links live in the
plan. They establish permission/API constraints, not feasibility on actual user
hardware. No new application test execution is claimed for this docs-only update.

Direct dual-role peering and maintained Go interoperability are user requirements,
not unanswered topology questions. API 27 install floor, exact serving support,
activation method (including whether same-device wireless activation works),
SDK pins, signing, audio budgets and schema details need B00 evidence. The
public backend is explicitly limited; full scrcpy parity remains a distinct
activated-backend gate. Do not advertise unrestricted unattended control on all
stock Android devices. Performance benefits and duplex capacity remain hypotheses.

## Review, commit and publication reconciliation

This checkpoint is frozen before independent review of the documentation change.
Its exact review verdict/hashes belong in the resulting commit message; do not
infer PASS from this paragraph. Find the documentation commit and review record:

```sh
git log -1 --format=fuller -- docs/ANDROID_CLIENT_PLAN.md
git log -1 --format='%H' -- .agent/HANDOFF.md
git status --short --branch
git branch -vv
```

Original documentation commit `be4594e60b7a16d906a50772565cd1cfd3c2c0de` was
reviewed and pushed. This scope revision is uncommitted at checkpoint authoring;
resolve its eventual containing commit and publication state with Git. Do not
mistake the original PASS for approval of this changed snapshot. Do not rewrite reviewed content
just to insert its own commit SHA. Independent review approval covers only the
exact reviewed documentation snapshot, not implementation or device performance.

## Next eligible action

After reconciliation, start A00, then B00, then A01. Preserve the user's
`.gitignore` edit. The next technical gate is proving how a target serves with
ordinary app permissions versus an explicitly activated helper, including
capability limits and local IPC isolation. A05 can use the existing Go host for
an early integration slice, but it does not satisfy direct Android peering.
Do not build a WebView wrapper, assume a bundled server gains shell privilege,
or apply JPEG drop-old logic to H.264 packets. No baseline tests close the new
G15–G18 requirements.

User-required completion work: A09 must capture real implemented-feature
screenshots, add them to README with build/device provenance, update README to
current setup/architecture/commands, and replace/remove outdated information once
the feature is complete. No screenshots were taken for this planning-only change;
do not fabricate them or remove still-correct legacy documentation prematurely.

## Work record template for future updates

Update this checkpoint before each review and at interruptions. Retain unresolved
findings; supersede stale claims explicitly instead of deleting their history.

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
