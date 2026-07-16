# Turn benchmark manifest

The baseline manifest (`manifests/baseline.json`) is the per-turn benchmark's
program keystone: it defines the tasks the harness runs, the workspace each
starts in, and — critically — what "pass" means for each task. Because `make
baseline` is re-run on every perf change, the manifest's pass/fail contract
matters for months, so the contract is written down here.

## Task count

48 tasks across seven classes:

| class     | count | oracle                      | tier        |
|-----------|-------|-----------------------------|-------------|
| nav       | 10    | none                        | latency     |
| edit      | 10    | substring grep              | correctness |
| fix       | 8     | scoped `go test -run <name>` | correctness |
| refactor  | 6     | `go build ./...`            | build       |
| longproc  | 4     | none                        | latency     |
| longctx   | 4     | none                        | latency     |
| parallel  | 6     | none                        | latency     |
| **total** | **48**|                             |             |

## Oracle tiers

Pass/fail is reported per tier so the report cannot be misread as a blanket
correctness verdict. The tier is decided per task from oracle presence and the
manifest's `buildOnlyClasses` list:

- **Correctness** (18 tasks: 10 edit + 8 fix) — a positive oracle (substring
  grep that the requested change landed, or a scoped `go test -run` that the
  bug fix passes). This is the only pass rate that can move with model quality:
  `tasksVerified` / `tasksPassed` / `correctnessPassRate`.
- **Build-only** (6 tasks: refactor) — `go build ./...` proves the edit
  compiles, not that the refactor achieved its goal (a no-op refactor passes).
  The manifest declares `buildOnlyClasses: ["refactor"]`. Reported as
  `buildCheckedTasks` / `buildPassedTasks` / `buildPassRate`, never in
  `correctnessPassRate`.
- **Latency-only** (24 tasks: nav, longproc, longctx, parallel) — no
  `verificationCommand`. An exit 0 only proves the turn ran, not that the answer
  was right. They contribute to latency and span attribution only and are
  counted in `latencyOnlyTasks`, never in any pass rate.

A task's tier is driven by oracle **presence** first: a task with no
`verificationCommand` is latency-only even if its class is listed in
`buildOnlyClasses`, so a missing oracle can never silently pass on exit 0.

## Known limitations (deferred)

These are accepted for the Phase 0 baseline and tracked in a follow-up issue;
they do not block the baseline because the tier split keeps the report honest:

- The edit grep oracles are substring checks. A rename oracle asserts the new
  name is present AND the old name is gone (compound `bash -c`), but an
  add-field oracle (`grep -R Label .`) only proves the string appears, not that
  it landed on the right struct. Strengthening these to `go vet`/`go build` +
  structural assertions is follow-up work.
- The refactor build oracle is non-positive (a no-op refactor passes). A
  structural verifier that proves the refactor happened is follow-up work;
  until then refactor is `buildPassRate`, not `correctnessPassRate`.
- The 24 read-only tasks have no oracle. Adding deterministic oracles for nav
  (diff against an expected answer) and documenting longproc/longctx/parallel
  as permanently latency-only is follow-up work.

## Fixtures

Each task's `workspaceFixture` points at a small self-contained workspace under
`testdata/` so the suite runs offline and repeatably. Mutating tasks (edit,
fix, refactor) run against a per-invocation **copy** of their fixture, so the
checked-in fixtures stay clean and one task's edits can't bleed into the next
iteration or a later task.