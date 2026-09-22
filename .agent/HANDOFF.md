# Android client / parity development checkpoint

Read [the complete plan](../docs/ANDROID_CLIENT_PLAN.md) and applicable
`AGENTS.md` instructions first. Reconcile this checkpoint against Git, current
source, live task ownership and test evidence before editing. Status here is a
snapshot, not proof that no other work has started.

## Snapshot

- Date: 2026-09-22.
- Scope: documentation of native Android client architecture, existing-mode
  parity repairs, implementation sequence, verification and resume procedure.
- Branch at authoring: `codex/android-client`.
- Source baseline: `e48af13fd32f8e3fa37f2c2d6f72b0122406b545`.
- Worktree at authoring: `/home/garyb/LLM-Projects/scrcpy-terminal`.
- Remote at authoring: `origin`, `https://github.com/pwnedbygary/scrcpy-terminal.git`.
- Pre-existing unrelated change: `.gitignore` modified by the user; excluded
  from this documentation change. Do not revert, stage or absorb it.
- Documentation paths: `README.md`, `docs/ANDROID_CLIENT_PLAN.md`,
  `.agent/HANDOFF.md`.
- No Android client code, new remote protocol, parity fixes, or benchmarks have
  been implemented in this planning change.

## Current task ledger

No implementation task is claimed by this checkpoint. Check live sessions and
worktrees before treating a task as available. All ownership/commit/evidence
fields for implementation tasks are currently unset.

| ID | State | Dependencies | Next action |
| --- | --- | --- | --- |
| A00 | NOT_STARTED | none | Reconcile checkout; inventory validation hardware; record baseline and measurement recipe |
| A01 | NOT_STARTED | A00 | Create feature-by-mode matrix and reproduce G01–G14 |
| A02 | NOT_STARTED | A01 | Shared action contract; input integrity fixes first; existing-mode conformance |
| A03 | NOT_STARTED | A00, A01 contracts | Packet subscriptions and compressed recovery |
| A04 | NOT_STARTED | A02/A03 contracts | Versioned authenticated transport |
| A05 | NOT_STARTED | A03, A04 | Installable Android hardware-video slice |
| A06 | NOT_STARTED | A04, A05 | Timestamped PCM/Oboe and Opus path |
| A07 | NOT_STARTED | A02, A04, A05; A06 for audio | Native controls and baseline parity |
| A08 | NOT_STARTED | A05–A07 | Performance qualification and browser compressed path |
| A09 | NOT_STARTED | A02–A08 | CI, packaging and release qualification |
| X01 | DEFERRED | hostless topology requirement | Direct Android ADB provider only if needed |

G01–G14 are all NOT_STARTED; see the plan for evidence, priority, and closure
criteria. Source-level candidates have not been physically reproduced.

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

The plan's source map records observations. Architecture/performance benefits
are proposals/hypotheses until measured. Host-connected topology and API 27
receiver floor are proposed defaults; actual hardware, hostless need, SDK pins,
signing, audio budgets and protocol schema details remain to be established.

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

At authoring, these documentation files are not yet committed or pushed. This
statement describes the pre-review snapshot; after resuming, resolve their actual
containing commit and upstream state with Git. Do not rewrite reviewed content
just to insert its own commit SHA. Independent review approval covers only the
exact reviewed documentation snapshot, not implementation or device performance.

## Next eligible action

After reconciliation, start A00, then A01. Preserve the user's `.gitignore` edit.
Do not start by building a WebView wrapper or copying JPEG drop-old logic to
H.264 packets. Do not assume existing tests prove the gap register is resolved.

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
