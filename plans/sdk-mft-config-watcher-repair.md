# sdk-mft config watcher repair

counter: 3

## Pass 1 — plan, tasks, and architecture

### Objective

Repair the config watcher race reported by `sdk-mft`: a recursive convention
directory can emit its dirty/poke notification before asynchronous watcher
registration finishes, so an immediate file write inside the new subtree is
missed under scheduler or host load.

### Tasks and subtasks

1. Reproduce and bound the failure.
   - Run the prescribed `make test-fast-parallel` reproduction.
   - Run the focused watcher test and inspect the existing timeout helper.
   - Preserve evidence about full-suite behavior versus focused behavior.
2. Add a regression proof at the watcher boundary.
   - Keep the existing subtree-write scenario as the user-visible contract.
   - Ensure the proof observes registration-before-notification, not merely a
     later dirty flag.
3. Implement the smallest production repair.
   - Defer the directory-change dirty notification until recursive registration
     has completed.
   - Keep watcher event consumption asynchronous and cleanup race-free.
4. Verify and hand off.
   - Run focused tests, the affected `cmd/gc` shard, fast suite, and `go vet`.
   - Update the bead, commit the cohesive change, and push it.

### Architectural changes

Keep the existing `watchConfigTargets` boundary. Add only an internal
registration-completion signal between its worker goroutines and event loop;
do not introduce a new exported abstraction or move fsnotify behavior across
packages. The event loop remains the owner of debounce scheduling, while the
registration worker owns recursive `watcher.Add` traversal.

### Test plan and evidence layer

The target truth is: after a newly discovered convention directory causes a
watcher notification, a file written in that directory is observed by the
watcher. The required evidence layer is the real `fsnotify` plus filesystem
composition exercised by `cmd/gc` tests. Focused tests are useful but
insufficient for host-load scheduling behavior; the prescribed parallel suite
is the broader concurrency evidence. A clean focused run alone must not be
treated as proof that the race is fixed.

### Support structures and docs

Use the existing hang-budget helper and `TESTING.md` policy. Maintain
`agent-execution.log` for completed subtasks. No user documentation change is
expected because this is an internal reliability repair.

### Execution order and stability strategy

Reproduce -> add/strengthen regression proof -> implement -> focused test ->
affected shard -> fast suite -> vet -> commit/push. Avoid fixed sleeps and
avoid changing test deadlines. The repair must preserve debounce behavior,
recursive coverage, and cleanup during cancellation.

### Blocker avoidance and parallel candidates

Independent read-only history and test-policy inspection can run in parallel
with reproduction. After the production edit, focused test and static review
can run independently, but full-suite verification must account for shared
host load. No subagent is needed for this small boundary change.

### Proxy audit

- Target truth: a newly created convention subtree is watched before the
  corresponding dirty notification is consumed, so immediate nested writes
  are not lost.
- Required evidence layer: real fsnotify/filesystem watcher tests plus the
  affected sharded package and prescribed parallel suite.
- Cheaper but insufficient proxy: a focused test on an idle host; it may never
  schedule the registration goroutine late enough to expose the race.
- Tempting false completion: merely increasing `hangBudget` or changing the
  test expectation after a focused pass.
- If the plan succeeds yet the bug remains: the completion signal could be
  emitted before all `watcher.Add` calls finish, or cleanup could drop a
  pending registration; the regression must make those orderings observable.

### No-change decisions

- No new watcher interface: there is one existing implementation and the
  package already has a testable function boundary.
- No polling or sleeps: completion notification is the relevant fact.
- No broad rewrite of recursive watching: the defect is notification order,
  not target discovery policy.

## Pass 1 — critique

The plan identifies the likely race and preserves the real watcher boundary.
The test plan correctly distinguishes idle focused evidence from scheduler-load
evidence. The main gap is that the current existing test may not explicitly
prove registration completion; the implementation should make its observed
poke ordering sufficient by construction, and a targeted test hook should be
avoided unless the production boundary cannot expose the fact naturally.

The verification list is proportionate, but full-suite execution may be
expensive under concurrent fleet load. It must not be skipped silently; if it
cannot complete, record the exact reason and retain the focused and shard
results separately.

## Pass 1 — critique of critique

The critique is sound: the test's immediate nested write is already a strong
behavioral proof once the production contract says the notification follows
registration. Adding a test-only hook would risk testing a proxy instead of
the real fsnotify boundary. The plan should therefore prefer a minimal code
change that gives the existing test deterministic meaning. It also needs an
explicit review of duplicate debounce notifications from registration events.

## Pass 1 — roll-up

Retain the existing real-filesystem regression scenario and require the event
loop to schedule its dirty signal only after recursive registration completion.
Review coalescing and cleanup so the change does not introduce duplicate
observable pokes or goroutine leaks. Preserve separate reporting of focused,
sharded, and parallel-suite evidence.

## Pass 1 — revised plan roll-up

The implementation subtask now explicitly includes event-loop debounce
coalescing for registration-completion notifications and cleanup behavior.
No architecture or scope changes are otherwise required.

## Pass 2 — refined plan

### Full plan and tasks

Keep the objective, task order, and evidence distinction from Pass 1. The
production change will use a bounded completion channel from registration
workers to the event loop. Directory events that require recursive watches
will not arm the dirty debounce until the completion signal is consumed.

### Architecture, tests, support, and execution

The event loop remains the sole debounce owner. Registration completion is an
internal coordination detail. The existing real-fsnotify regression remains
the primary test; run focused, `cmd/gc` shard, fast parallel, and vet checks.
The plan file and temporary execution log remain the only support artifacts.

### Stability, blockers, and parallel work

Use channel completion rather than sleeps or polling. Ensure completion sends
cannot block shutdown and that cleanup waits for all registration workers.
History inspection and test execution remain parallelizable; no additional
subagent is justified.

### Proxy audit

Target truth and required evidence remain the real watcher boundary. A focused
idle run is still insufficient. Raising timeouts remains a false completion.
The remaining failure mode is a completion notification that races cleanup or
does not represent every `watcher.Add` in the recursive walk.

### Critique

The refined plan is narrow and gives the existing test a causal ordering
guarantee. A buffered completion channel must be large enough to avoid losing
the only completion fact before the event loop receives it, or coalescing must
be intentional. Registration failure must still cause a dirty signal so a
reload is not lost.

### Critical evaluation of critique

Those concerns are valid. A one-slot channel is sufficient if completion is a
coalesced “some registration finished” signal, because any completion causes a
dirty notification and all registration workers are still joined at cleanup.
The implementation should send after both success and failure, and select on
the shutdown channel to avoid a blocked sender.

### Roll-up and no-change decisions

Add the one-slot coalesced completion signal, schedule the normal debounce on
completion, and retain immediate dirty scheduling for events that do not need
registration. Do not change watcher targets, public APIs, or test deadlines.

## Pass 3 — final plan

### Full plan/tasks/subtasks

Execute the refined repair, inspect the diff for boundary leakage, run the
named evidence layers, update the bead and log, then commit and push. Confirm
that the current untracked user files are preserved.

### Architectural and test plan

Only `cmd/gc` watcher internals and its adjacent regression proof may change.
The target truth is ordering between recursive `watcher.Add` completion and
dirty notification. Focused fsnotify evidence, the affected shard, the
parallel fast suite, and `go vet` are required and must be reported by layer.

### Support, order, stability, blockers, and parallel candidates

Use the existing completion channel and wait group, no new abstraction. Run
tests in increasing scope. If shared host load prevents a suite from
completing, preserve the timeout output and do not call it a pass. No
subagent is required for the localized edit.

### Proxy audit

Do not substitute focused success, changed timeout budgets, or a semantic
preview for real watcher evidence. If the bug remains, it will present as a
missed nested write after a directory-change poke or as cleanup dropping a
registration; inspect both directly.

### Critique, critical evaluation, and roll-up

The final plan is complete and bounded. The only material risk is accidental
duplicate pokes or altered debounce semantics; a diff review and repeated
focused run cover that risk. No requirement has been removed, and no-change
decisions from earlier passes remain valid. Proceed to implementation.

### No-change decision

No further plan changes are warranted after three passes.
