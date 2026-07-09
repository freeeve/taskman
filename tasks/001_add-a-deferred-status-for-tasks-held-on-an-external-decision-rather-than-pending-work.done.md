# 001 -- add a deferred status, for tasks held on an external decision rather than pending work

Filed from libcat on 2026-07-09 (cross-repo ask).

## Context

The ledger has three states, encoded in the filename:

| State | Filename |
|---|---|
| Pending | `001_description.md` |
| In progress | `001_description.in-progress.md` |
| Complete | `001_description.done.md` |

There is no way to say **"this task is not being worked, and that is a
decision, not a backlog position."**

The case that prompted this: libcat's `tasks/247` asks for a CI workflow that
publishes a container image to GHCR on every version tag. That is an
outward-facing, hard-to-reverse action -- a published image tag cannot be
recalled from anyone who already pulled it -- so it waits on the maintainer,
not on an engineer having time. The maintainer's instruction was literally
"mark 247 as deferred for now", and there was no state to mark it with.

`pending` is wrong: it invites the next agent through the loop to pick it up,
which is exactly what must not happen. A prose note in the body is the only
brake, and `taskman list` does not print bodies. In a cron-driven loop where an
agent picks the highest-numbered open task, "pending with a warning buried in
the file" is a footgun -- the warning is invisible at exactly the moment it
matters.

Marking it `done` is a lie. Deleting it loses the reasoning.

## Scope

- A `deferred` state: `001_description.deferred.md`.
- `taskman defer <n|slug-fragment>` and `taskman resume <n>` (or reuse
  `reopen`), following the existing rename-and-auto-commit pattern with a
  pathspec, like `start`/`done`/`reopen`.
- `taskman list` should hide deferred tasks by default, the way `done` is
  hidden, and show them under `-all`, marked. Getting them out of the "what
  should I work on next" set is the whole point. `taskman next` must never
  return one.
- Consider requiring a reason: `taskman defer 247 -reason "maintainer's call:
  outward-facing publish"`, appended to the file. A deferral without a recorded
  why decays into an unexplained `pending` in six months.

## Open questions

- Is `deferred` distinct enough from a `blocked` state (waiting on another task,
  which will resolve on its own) to be worth both? My instinct is one state with
  a reason string, not two: the difference lives in the reason, and a tool that
  makes you pick a taxonomy up front gets the choice wrong.
- **Should this be a fourth status at all, or a flag on a pending task?**
  `taskman fix` reasons about "the most advanced status keeps the contested
  number", and deferred is not on that axis -- it is orthogonal to progress. A
  `.deferred.md` file would force an answer to "is deferred more advanced than
  pending?" that has no meaning. A flag sidesteps it. This is the decision worth
  making before any code.

## Acceptance

- `taskman defer 247` marks it and commits with a pathspec.
- `taskman list` (no flags) does not list it; `taskman list -all` does, marked.
- `taskman next` skips it.
- `taskman resume 247` returns it to pending.
- `taskman fix` treats a deferred task's number the way it treats a pending
  one, whatever the representation.

## Note

`~/taskman` had no `tasks/` directory; filing this created it. If you would
rather this repo track its own work as GitHub issues, say so and I will move it.

Answered: the ledger stays. This repo tracks its own work in `tasks/`.

## Outcome

Implemented in 95655cb, released as v0.4.0.

**Deferred is a flag, not a fourth status.** The open question resolved in
favour of the flag, and the reason is visible in the code: `PlanRepairs`
compares `t.Status > group[keep].Status` to pick which duplicate keeps a
contested number. A fourth `Status` value would have to sit somewhere in that
ordinal comparison, and every position is a lie. As a flag, deferral never
enters the comparison at all -- a deferred task contests a number exactly as
the pending or in-progress task it still is. `TestDeferredNumberContest` pins
this.

On disk the flag is a `.deferred` marker layered on the status suffix, so it
stays greppable and consistent with the existing convention:
`001_slug.deferred.md`, or `001_slug.in-progress.deferred.md` for work that
was already underway when it was held. `resume` restores the status
underneath; `start`/`done`/`reopen` clear the deferral, on the grounds that
acting on a task ends the hold. Deferring a `done` task is refused -- there is
no decision left to wait on.

`-reason` is **required**, going beyond the "consider requiring" in Scope. The
task's own argument decided it: the filename cannot carry a why, and an
unexplained deferral decays into an unexplained `pending`. It is appended to
the body as a dated `## Deferred` section; `resume` appends `## Resumed`, so
the file keeps the log after the filename stops carrying it.

Two deviations from Acceptance, both deliberate:

- *"`taskman next` skips it"* does not apply. `next` prints the next free
  **number**, not the next task to work on -- an easy misread from outside this
  repo. Nothing about deferral touches numbering, so `next` is unchanged. What
  actually keeps deferred work out of the "what should I pick up" set is its
  absence from `taskman list`, which is where the cron-loop agent looks.
- *"`resume` returns it to pending"* holds for the motivating case but is
  stated too narrowly: `resume` returns the task to whatever status it was
  deferred from. For a pending task that is pending.

`list` prints a `N deferred (taskman list -all)` line when it hides any, so a
deferred task cannot silently vanish from the ledger -- the failure mode of
hiding it in the first place.

The `deferred` vs `blocked` open question needs no answer yet: one state with a
mandatory reason string, as the task proposed. The distinction lives in the
reason.

Verified by driving the real binary against libcat's actual 247 case
(defer -> hidden from `list` -> shown under `-all` -> `resume`), plus a
duplicate-number contest between a deferred and an in-progress task, in which
the in-progress task keeps the number and the deferred one is renumbered with
its marker intact. Tests at 85.5% statement coverage; `FuzzParseName` now
round-trips the deferral marker through 3M execs.

**Follow-up for libcat:** `taskman defer` is available as of v0.4.0, so
libcat's `tasks/247` can now be marked
`247_<slug>.deferred.md` with the maintainer's reasoning recorded in the body.
