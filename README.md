# taskman

A tiny CLI for the `tasks/` ledger convention used across these repos: one
markdown file per task, status carried by the filename, numbers minted by the
repo that owns the ledger.

```
001_description.md                       pending
001_description.in-progress.md           in progress
001_description.done.md                  done
001_description.deferred.md              deferred: held on an external decision
001_description.in-progress.deferred.md  ...and it was already underway
qbd_description.md                       LEGACY cross-repo ask filed unnumbered
                                         by another repo's session ("qbd" = the
                                         filer); renumber with `taskman adopt`
```

## Usage

```
taskman [list] [-all]        open tasks (-all includes done and deferred);
                             flags duplicate numbers and unadopted asks
taskman next                 next free task number
taskman new <description>    create the next numbered pending task
taskman start <n|slug>       mark in-progress   (rename)
taskman done <n|slug>        mark done          (rename)
taskman reopen <n|slug>      back to pending    (rename)
taskman defer -reason <why> <n|slug>
                             hold on an external decision (rename); the reason
                             is appended to the task body and is required
taskman resume <n|slug>      lift a deferral, restoring the status underneath
taskman adopt <name>         renumber a legacy prefixed cross-repo ask into
                             the ledger and stamp the number into its H1
taskman file [-as filer] <repo-dir> <description>
                             file a cross-repo ask into another repo's
                             tasks/ at THAT ledger's next number, committed
                             there immediately (filer credit defaults to the
                             current repo's dir name; duplicate slugs refuse)
taskman fix [-n]             repair the ledger: duplicate numbers are
                             renumbered into the lowest free slots (gaps
                             first) with the H1 restamped; -n reports only
```

The `tasks/` directory is discovered by walking up from the current
directory, so any subdirectory of a repo works. `start`/`done`/`reopen`
accept a task number or a unique slug fragment; a duplicate number (the
ledgers have historical collisions) or ambiguous fragment errors with the
candidates instead of guessing.

Every mutating command commits the touched task files automatically with a
pathspec-scoped `git add`/`git commit` (`chore(tasks): …`), so a concurrent
session's staged work in the same repo is never swept into the commit. Pass
`-no-commit` after the subcommand to skip it; outside a git repo the
operation still succeeds with a warning.

## Deferral

A deferred task is one that is *not being worked, and that is a decision* --
it waits on someone's call, not on an engineer having time. The motivating
case: a task asking CI to publish a container image on every version tag is
outward-facing and irreversible, so it waits on the maintainer. Left
`pending`, the next agent through a cron loop picks it up, which is exactly
what must not happen; `list` does not print bodies, so a prose warning in the
file is invisible at the moment it matters. Marking it `done` is a lie, and
deleting it loses the reasoning.

```
taskman defer -reason "maintainer's call: outward-facing publish" 247
taskman resume 247
```

`defer` hides the task from `taskman list` (it shows under `-all`, marked,
and the default listing prints a `N deferred` count so nothing vanishes
silently) and appends a dated `## Deferred` section carrying the reason. The
reason is mandatory: a deferral without a recorded why decays into an
unexplained `pending` in six months, and the filename cannot carry it.

Deferral is a **flag, not a fourth status**. It is orthogonal to progress: a
task can be deferred while pending or while in progress, and `resume` puts it
back where it was. This is what keeps `fix` honest -- it ranks duplicate
claimants by how far along they are, and a `deferred` status would force an
answer to "is deferred more advanced than pending?", a question with no
meaning. A deferred task contests a number exactly as the pending or
in-progress task it still is. `start`, `done` and `reopen` all clear the
deferral: acting on a task ends the hold.

Note that `taskman next` prints the next free *number*, not the next task to
pick up, so deferral does not affect it. What keeps deferred work out of the
"what should I do next" set is its absence from `taskman list`.

## Repair

`fix` picks each duplicate's keeper deterministically -- the most advanced
status wins (done > in-progress > pending; ledger order breaks ties), since
the furthest-along task is the one history most likely references. Gaps no
duplicate can fill are reported but never compacted: task numbers appear in
commit messages and docs, so reusing or shifting them would corrupt
references.

Cross-repo asks are numbered at filing time: the immediate pathspec commit
in the receiving repo is what makes the number claim safe (the historical
prefix convention existed because asks used to sit uncommitted, invisible to
concurrent sessions). `taskman adopt` remains for legacy prefixed asks and
assigns the next free number, recording the filed name as a breadcrumb.

## Install

```
go install github.com/freeeve/taskman@latest   # or: make install
```
