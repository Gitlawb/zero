# Turn benchmark manifest

The baseline manifest (`manifests/baseline.json`) is the per-turn benchmark's
program keystone: it defines the tasks the harness runs, the workspace each
starts in, and — critically — what "pass" means for each task. Because `make
baseline` is re-run on every perf change, the manifest's pass/fail contract
matters for months, so the contract is written down here.

## Task count

48 tasks across seven classes:

| class     | count | oracle                                  | tier        |
|-----------|-------|-----------------------------------------|-------------|
| nav       | 10    | grep on captured final answer           | correctness |
| edit      | 10    | stamped `oracle_test.go` + `go test`    | correctness |
| fix       | 8     | scoped `go test -run <name>`            | correctness |
| refactor  | 6     | stamped `oracle_test.go` + `go test`    | correctness |
| longproc  | 4     | none                                    | latency     |
| longctx   | 4     | none                                    | latency     |
| parallel  | 6     | none                                    | latency     |
| **total** | **48**|                                         |             |

Tier counts: **correctness 34** (10 edit + 8 fix + 10 nav + 6 refactor),
**build 0**, **latency 14** (4 longproc + 4 longctx + 6 parallel).

## Oracle tiers

Pass/fail is reported per tier so the report cannot be misread as a blanket
correctness verdict. The tier is decided per task from oracle presence and the
manifest's `buildOnlyClasses` list:

- **Correctness** (34 tasks: edit, fix, nav, refactor) — a positive oracle. For
  edit/refactor the harness stamps an `oracle_test.go` (from the task's
  `oracleTest` field) into the fixture copy **after** the agent run, then runs
  `go test ./...`: the Go compiler is the structural verifier, so a no-op
  refactor, a missing field, or a reworded-but-not-removed line fails to
  compile/run. fix uses a scoped `go test -run <name>`. nav greps the agent's
  captured final answer (`.zero-answer.txt`) for determinable facts. This is
  the only pass rate that can move with model quality: `tasksVerified` /
  `tasksPassed` / `correctnessPassRate`.
- **Build-only** (0 tasks) — the tier is empty. refactor used to live here with
  a non-positive `go build ./...` (a no-op refactor passed); PR #701 gave it a
  structural oracle, graduating it to correctness. `buildOnlyClasses` is `[]`.
  `buildCheckedTasks` / `buildPassedTasks` / `buildPassRate` are reported as 0
  and never in `correctnessPassRate`.
- **Latency-only** (14 tasks: longproc, longctx, parallel) — no
  `verificationCommand`. An exit 0 only proves the turn ran, not that the answer
  was right. They contribute to latency and span attribution only and are
  counted in `latencyOnlyTasks`, never in any pass rate.

A task's tier is driven by oracle **presence** first: a task with no
`verificationCommand` is latency-only even if its class is listed in
`buildOnlyClasses`, so a missing oracle can never silently pass on exit 0.

### How the stamped oracle and captured answer work

The production runner (`NewTurnExecRunner` in `turn_bench.go`) copies the fixture
into an isolated temp dir, runs `zero exec --trace`, then — before running the
`verificationCommand` — stamps two files into the copy:

- `oracle_test.go` from `task.OracleTest` (edit/refactor). It is stamped *after*
  the agent run so it can't interfere with the agent's own `go build`/`go test`
  during the task (refactor-03's `package zeroapp` test would break a pre-rename
  build) and can't be pre-seen or tampered with. The `verificationCommand`
  (`go test ./...`) then compiles and runs it.
- `.zero-answer.txt` from the `{"type":"final","text":...}` event in the
  stream-json output (nav). The `verificationCommand` is a compound `bash -c`
  grep requiring the determinable facts (e.g. nav-09 requires the answer to
  mention `port`, `name`, and `retries`). An empty file (no `final` event) makes
  the grep fail, which is the correct outcome for a run that produced no answer.

The `oracleTest` source uses compile-time references where the assertion is
structural (`var _ = Config{}.Label` for edit-03, `var _ = formatGreeting` for
refactor-01) and behavioral `Test…` functions where the assertion is a value
(edit-04 `Version != "1.1.0"`, edit-10 `greet("x") != "hello, world"`, edit-09
`load("nonexistent")` wrapping). Tasks whose oracle is a plain grep/build with
no stamped test (edit-02, refactor-02, refactor-05) carry no `oracleTest`.

## Known limitations

These are accepted for the Phase 0 baseline; the tier split keeps the report
honest where they apply:

- **longproc / longctx / parallel are permanently latency-only.** There is no
  deterministic oracle for "report whether the build succeeds", "summarize a
  large generated file", or "report the first line of each of six files" — the
  answer is free-form prose and a contains-check would rubber-stamp. They stay
  in `latencyOnlyTasks` and never enter a pass rate.
- **nav count tasks (nav-04, nav-05, nav-08) require a structured `count: N`
  token.** Their prompts ask the agent to state the count as `count: N` on its
  own line, and the oracle greps for `count: 0`. This trades a little output
  freedom for determinism (a loose prose-contains on "0" would match almost any
  answer).
- **nav-03 / nav-06 / nav-07 are lenient contains-oracles on real facts.** A
  wrong-but-plausible answer that happens to mention the real symbols (e.g.
  "Config" and "MaxRetries" for nav-06) could in principle pass. Accepted for a
  latency-first baseline; the oracle still catches an agent that didn't look at
  the code at all.
- **Rename oracles use a declaration-site negative grep** (`! grep -RIn
  'MaxRetries =' .`) rather than a broad `! grep -R MaxRetries .`, so a correct
  rename that leaves a doc-comment mention of the old name is not false-failed.
  The negative grep is still required: without it, a no-op that *adds* the new
  name and *keeps* the old would pass the compile-time `var _ = RetryLimit` and
  `go test`.

## Fixtures

Each task's `workspaceFixture` points at a small self-contained workspace under
`testdata/` so the suite runs offline and repeatably. Mutating tasks (edit,
fix, refactor) run against a per-invocation **copy** of their fixture, so the
checked-in fixtures stay clean and one task's edits can't bleed into the next
iteration or a later task.

Each mutating fixture (and nav) carries a `go.mod` (`module <pkg>; go 1.22`) so
`go test ./...` / `go build ./...` oracles work in the copy: `copyFixture`
places the copy under `os.TempDir()/zero-turn-bench/` — one level below the
system temp root — because Go ignores `go.mod` in direct children of the system
temp dir (a hijack guard), which would otherwise make every compiler-backed
oracle fail with "cannot find main module".