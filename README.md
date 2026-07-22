# taskman

Central task ledgers for multi-project, multi-agent development: one
markdown file per task in a store taskman owns (`$TASKMAN_HOME`, default
`~/.taskman`), status carried by the filename, every mutation a git commit,
and a kanban web UI over the same files (`taskman serve`).

```
~/.taskman/                    the store: a git repo, one dir per project
  <project>/
    tasks/                     the ledger
    features/                  product source of truth (markdown specs)
    screenshots/012/           images attached to task 012 (web UI only)
    order                      priority, one task number per line, top first
```

```
001_description.md                       pending
001_description.in-progress.md           in progress
001_description.done.md                  done
012-impl_description.md                  routed to the "impl" lane
001_description.deferred.md              deferred: held on an external decision
001_description.in-progress.deferred.md  ...and it was already underway
qbd_description.md                       LEGACY unnumbered cross-repo ask
                                         ("qbd" = the filer); `taskman adopt`
```

The project is resolved from `-p`, then `TASKMAN_PROJECT`, then the
enclosing git repo's basename, then the cwd basename -- so inside a repo,
plain `taskman` just works, and an agent session pins itself with one env
var. Docs: [requirements](docs/requirements.md),
[architecture](docs/architecture.md), [design decisions](docs/design.md).

## Usage

```
taskman [list] [-all] [-lane L]  open tasks in priority order (-all includes
                                 done and deferred); flags duplicates
taskman projects                 store projects with open/deferred counts
taskman next                     next free task number
taskman top [-lane L]            highest-priority pending task -- what to
                                 pick up ("next is a number, top is a task")
taskman new [-lane L] <desc>     create the next numbered pending task
taskman show [-path] <n|slug>    print a task's raw body (-path: its file path)
taskman update (-title <t> | -body <md>|- | -append <md>|-) <n|slug>
                                 edit a task in place and commit it, so the
                                 store file is never hand-edited; -body/-append
                                 read stdin when given "-"
taskman start <n|slug>           mark in-progress   (rename)
taskman done <n|slug>            mark done          (rename; prunes order)
taskman reopen <n|slug>          back to pending    (rename)
taskman lane <n|slug> <lane|->   set or clear a task's lane (rename)
taskman move <n|slug> (top | bottom | above <n|slug> | below <n|slug>)
                                 reprioritize a task in the order list list
                                 and top follow (above = higher priority)
taskman blocked                  list active cross-session blocks
taskman blocked <lane> "<msg>"   raise your lane's block (name the task(s)
                                 you're doing, the blocking task(s), and the
                                 lane that owns them); empty message clears it
taskman blocked -unblock <lane> [note]
                                 respond that a lane is unblocked
taskman defer -reason <why> <n|slug>
                                 hold on an external decision; the reason is
                                 appended to the body and is required
taskman resume <n|slug>          lift a deferral, restoring the status under it
taskman adopt <name>             renumber a legacy prefixed ask into the ledger
taskman feature new <desc>       create a feature spec in features/
taskman feature list [-all]      features with a done-task rollup
taskman feature show [-path] <slug>
                                 print a spec's raw body (-path: its file path)
taskman feature update (-body <md>|- | -append <md>|- | -tasks <nums>) <slug>
                                 edit a spec in place and commit it; -tasks
                                 rewrites the linking "Tasks:" line ("" clears)
taskman feature done <slug>      mark a feature shipped
taskman file [-as filer] <project> <desc>
                                 file an ask into another project's ledger at
                                 its next number, committed immediately
taskman fix [-n]                 renumber duplicates into the lowest free
                                 slots and prune stale order entries
taskman migrate [-prune] <repo-dir> [project]
                                 import a repo-local tasks/ ledger (empty
                                 project only); -prune removes the source
taskman serve [-addr host:port]  kanban web UI, localhost only by default
taskman lock acquire [-ttl] [-wait] [-reason why] <resource>
                                 take a machine-wide lock on a contended
                                 resource; non-zero exit if someone holds it
taskman lock run [-ttl] [-wait] <resource> -- <cmd>
                                 hold it for one command, heartbeating
taskman lock release|heartbeat [-token t] <resource>
                                 drop or refresh a lock you hold
taskman lock status [<resource>] who holds what, for how long, and why
taskman lock steal <resource>    break a wedged holder's lock (loud)
```

All commands take `-p <project>`; mutating commands take `-no-commit`.
`start`/`done`/etc. accept a number or a unique slug fragment; ambiguity
errors with the candidates instead of guessing.

Every mutation commits the touched files with a pathspec-scoped
`git add`/`git commit` (`chore(<project>): …`) in the store repo, so
concurrent sessions -- even on different projects -- never sweep each
other's work into a commit. Outside git problems degrade to a warning; the
ledger operation itself always wins.

## Lanes

A lane is a routing token in the filename (`012-impl_fix-thing.md`): which
session, submodule, or workstream owns the task. The intended setup is two
Claude Code sessions per project -- `impl` (implements and releases) and
`e2e` (end-to-end tests) -- each picking work with `taskman top -lane X`.
Lanes are free-form, live inside the filename stem, and therefore survive
every status rename with no extra bookkeeping. Numbers stay one sequence
per project across lanes.

## Priority

Each project may have an `order` file: task numbers, one per line, top
first. It is *advisory* -- reading is lenient (comments, garbage, and
unknown numbers are skipped), unlisted tasks simply sort after listed ones,
and a missing file means ledger order. `list` and `top` follow it; marking
a task done prunes its number in the same commit; dragging cards in the web
UI rewrites the file as one commit. New tasks land unlisted at the bottom
until someone ranks them.

## Resource locks

Sibling repos run benchmark sweeps on one machine, in sessions that cannot see
each other. When two sweeps overlap -- or a sweep overlaps a sibling's
`cargo build --release` -- the timings are quietly inflated and the run still
"succeeds" and still publishes. The store is the one thing all those sessions
share, so it hosts the mutual exclusion.

```
taskman lock run -ttl 10m -wait 30m -max-load 2 \
    -reason "sweep $(git rev-parse --short HEAD)" local-cpu -- ./bench-sweep.sh
```

`run` is the shape to reach for: it acquires the resource, runs the command,
heartbeats while it runs, releases when it exits, and exits with the command's
status. Nothing to trap, nothing left held if the sweep panics.

## The lock is not the whole story: -max-load

A lock only excludes processes that *ask* for it. A daemon eating a core, a VM,
a sibling's `cargo build --release` -- none of them will ever call `acquire`, and
they are what actually ruin a measurement. A run can hold `local-cpu` and still
be timed on a machine at load 11.

So a timed run should also state how quiet a machine it needs:

```
-max-load 2      # start only if other work is under 2 cores, and keep it there
```

`-max-load` is enforced twice. **Before** the command starts, taskman takes the
resource (which stops any *cooperating* load) and then waits out the rest, up to
`-wait`; if the machine never settles it hands the lock back and exits non-zero,
naming who is holding the CPU:

```
taskman: machine is busy: 6.1 cores of other work, over the 2.0 allowed (waited 30s for it to settle)
  pebble_updater (pid 87881, 0.8 cores)
  rustc (pid 73673, 0.9 cores)
```

**During** the command, it keeps sampling. If the run averaged more foreign work
than the ceiling, `run` says so and exits **3** even though the command itself
succeeded -- so an ordinary `|| exit 1` refuses to publish a spoiled sweep:

```
taskman: local-cpu did NOT have the machine to itself: 3.7 cores of other work on
average, 4.6 at peak, worst: rustc (pid 94346, 1.0 cores), over the 2.5 allowed
taskman: these timings are not trustworthy -- do not publish them
```

Load is counted in **cores of work foreign to the command** -- everything outside
taskman's own process tree. A twelve-thread benchmark drives the load average to
twelve by design, so total load says nothing about whether a run had the machine
to itself; foreign load says exactly that. The verdict rests on the mean, not the
peak, so a brief compile blipping through a long sweep is noise while a daemon
holding a core throughout is not.

Without `-max-load` there is no gate at all: `lock run` is then pure exclusion,
which is the right thing for a build that merely must not disturb others.

For a script that wants the lock across several steps, acquire it directly.
The token printed on stdout is the proof of ownership the later release must
present, so keep it:

```sh
TASKMAN_LOCK_TOKEN=$(taskman lock acquire -ttl 45m -wait 30m local-cpu) || exit 1
export TASKMAN_LOCK_TOKEN
trap 'taskman lock release local-cpu' EXIT
```

A resource is any free-form name. Give things that contend for the same
hardware the same name, and things that do not, different ones -- a remote
RageDB sweep is a thin client locally, so it may run alongside a native sweep,
and forcing them through one global mutex would cost throughput without buying
accuracy:

| run | resource |
|---|---|
| rust / go / neo4j / kuzu, anything timed on this box | `local-cpu` |
| the RageDB EC2 instance | `ragedb-ec2` |
| the managed Neptune endpoint | `neptune-aws` |

Locks are machine state, not ledger history: they live in a gitignored
`.locks/` and nothing about them is committed. They carry a TTL and a
heartbeat, so a holder that is killed mid-sweep frees the resource within the
TTL rather than wedging it forever, and the next acquirer says loudly whose
dead run it broke. A holder whose lock was broken cannot release its
successor's -- the token check refuses. `taskman lock steal` is the human
override for a holder that is wedged but not yet expired; `taskman lock status`
says who holds what.

One caveat worth stating plainly: a lock cannot be built out of task status.
The ledger is a multi-writer git store with no cross-process locking (which is
why `taskman fix` exists), so two sessions "claiming" a lock task would both
succeed and both benchmark. Exclusion needs an atomic primitive, which is what
`link(2)` into `.locks/` is.

Locks are machine-scoped, so `-max-load` measures *this* box. A run against a
remote engine (`ragedb-ec2`, `neptune-aws`) is a thin client locally: gate it on
the load of the host under test, over ssh, and confirm the result against a
known-clean band before publishing. Taskman cannot see that machine.

## Features

`features/` holds the product's source of truth: one markdown spec per
feature (requirements and design notes live in the body), renamed to
`.done.md` when shipped. A `Tasks: 012, 019` line links the implementing
tasks; `taskman feature list` and the web UI roll up their completion.

### Authoring workflow

Features are deliberately **flat**: no parent/child feature files. One
`features/<slug>.md` per feature, with depth expressed *inside* the file --
nested headings, sub-sections, and checklists, as deep as the spec needs.

- **Open a feature** when the work describes *what the product should do*
  and outlives a single task: a capability, surface, or behavior whose
  requirements are worth recording. **Open a plain task** when it is one
  bounded change (a fix, a chore, one step of a feature).
- **Write the spec in the body**: motivation, requirements, decisions,
  edge cases. Structure goes into the file, never into child features.
- **Link the implementing tasks** and keep the `Tasks:` line current:
  `taskman feature update -tasks "012, 019" <slug>` rewrites it (and
  `taskman feature show <slug>` prints the spec), the web UI's link picker
  toggles membership, or its "+ task" creates a task pre-linked. Chips and
  the N/M rollup follow the ledger automatically.
- **Ship it** (`taskman feature done <slug>` or the ship-it button) when
  the linked tasks are done and the behavior is live; `feature reopen` /
  unship reverses a premature ship.

Agent sessions should treat the features map as first-class: when picking
up work that introduces a capability, open or update its feature spec and
link the tasks -- an empty features map means the source of truth lives
nowhere.

## Web UI

`taskman serve` (default `127.0.0.1:7777`; `scripts/restart-webui.sh` for a
rebuild-and-restart loop) renders the kanban board: columns by status, drag
across columns to change status, drag within pending to reprioritize, lane
filter and swimlanes, a deferred toggle (deferred cards are badged and
greyed, moved only via explicit dialog actions), a features tab with
per-task status chips, and full GitHub-flavored markdown in the task dialog
(goldmark, server-side -- the module's only dependency). Every UI action
runs the same code paths and leaves the same commit as its CLI twin.

Screenshots: paste or drop an image on the task dialog. It lands in
`<project>/screenshots/<NNN>/`, the task body gains a dated link, and both
commit together. Images live outside `tasks/` so agent sessions never spend
tokens reading them. The server refuses non-loopback binds without
`-insecure-bind` because the API has no auth.

## Deferral

A deferred task is one that is *not being worked, and that is a decision* --
it waits on someone's call, not on an engineer having time. `defer` hides
the task from `taskman list` and `top` (it shows under `list -all`, marked,
and the default listing prints a `N deferred` count so nothing vanishes
silently) and appends a dated `## Deferred` section carrying the mandatory
reason -- an unexplained deferral decays into an unexplained pending task,
and the filename cannot carry the why.

Deferral is a **flag, not a fourth status**: orthogonal to progress, so a
task can be deferred while pending or in progress, and `resume` restores
exactly what was underneath. This keeps `fix` honest -- deferral plays no
part in ranking duplicate claimants. `start`, `done`, and `reopen` clear
it: acting on a task ends the hold.

## Repair

`fix` picks each duplicate number's keeper deterministically -- the most
advanced status wins (done > in-progress > pending; ledger order breaks
ties) -- and moves losers to the lowest free slots, restamping their H1
titles. Gaps are reported but never compacted: numbers appear in commit
messages and docs, so shifting them would corrupt references. It also
prunes order-file entries whose tasks are gone or done.

## Agent sessions

The store is built for several autonomous coding sessions working the same
projects at once. The setup that works: put the conventions in the
**global** agent config once (project names resolve from the repo you're
standing in, so the same text serves every repo), give each session a lane,
and drive each session with a recurring "work your lane" loop.

### Global conventions (`~/.claude/CLAUDE.md` or equivalent)

```markdown
## Task tracking
Tasks live in the central taskman store (~/.taskman), one dir per project;
the project resolves from the enclosing repo's basename (pin long sessions
with TASKMAN_PROJECT). Sessions with a role use a lane (impl | e2e | ...).
- Pick work: `taskman top -lane <lane>`, then `taskman start <n>` ->
  work -> semantic commit(s) in the code repo -> `taskman update -append`
  an Outcome section onto the task -> `taskman done <n>` (commits outcome +
  rename together). Read a task with `taskman show <n>` and edit it with
  `taskman update` rather than opening the store file -- the CLI resolves the
  status-suffixed name, writes under the store lock, and commits for you.
- New work: `taskman new -lane <lane> <desc>`. Ask another project:
  `taskman file <project> <desc>` -- a BARE project name, never a path.
- Use the taskman CLI for ALL ledger chores; never rename task files by
  hand and never fold ledger renames into code commits -- taskman commits
  every mutation itself, pathspec-scoped.
- Only modify your session's own working repo. If another repo needs a
  change, file a task there instead of editing it.
- Never leak task numbers into production code, help text, errors, or
  logs -- they're tracker-internal. (Exception: a test may cite the task a
  regression covers.) Put the "why" in the task file or commit message.
- Duplicate numbers happen (the store is multi-writer and allocation has
  no cross-process lock): when a number shows twice, `taskman fix`.
- Never read ~/.taskman/<project>/screenshots/ -- images are for the web UI
  (`taskman serve`). Task bodies may link them; ignore the links.
```

### Startup loops

Each session runs one standing loop (in Claude Code: `/loop 15m <prompt>`;
any scheduler that re-prompts the session works). The prompt is the whole
contract: sweep the lane, work each task through the full flow, and do
*nothing* when the lane is empty -- an idle cycle costs one `taskman top`.

Size the interval to your token budget: a timed loop matched to your
plan's credit is what keeps sessions from exhausting the short rolling
rate-limit windows. Idle sweeps are nearly free; cycles that *find work*
are not, and every concurrent session draws on the same window. On a $200
(20x) plan, 5-10 minutes per session is comfortable for a project or two;
running many projects (or more sessions per project), stretch it somewhat
-- 15-30 minutes -- so simultaneous working cycles don't stack up against
the cap. Tasks queue harmlessly in the ledger either way; a longer
interval only delays pickup, it never loses work.

The implementation session owns the code and releases:

```
Work the <project> impl lane: run `taskman top -lane impl` (also check
`taskman list` for unlaned open tasks). For each open task:
`taskman start <n>`, implement it following the repo conventions (tests,
formatting, semantic commit), push, append an Outcome section to the task
file, then `taskman done <n>`. Do not touch e2e/ (the e2e session owns it)
or other repos; file cross-project asks with `taskman file`. If there are
no open impl tasks, do nothing and wait for the next cycle.
```

The e2e session owns only the test suite and *files* everything it finds:

```
Work the <project> e2e lane: probe the running app end to end and lock in
behavior as tests under e2e/ -- the only directory this session writes.
File every defect as an impl task (`taskman new -lane impl "<symptom;
root cause; suggested fix>"`) with enough body that impl needs no shared
context; never fix product code yourself. If nothing new surfaced, re-run
the suite and wait for the next cycle.
```

On multi-module projects, lanes double as module ownership (`api`, `ui`,
`worker`, ...): one session per module, same loop shape with its own lane
token. Numbers stay one sequence per project, so one order file still
ranks priority across all lanes.

What makes this safe to leave running:

- **Disjoint ownership.** Each session writes only its own directories
  (impl: the code; e2e: `e2e/`; per-module lanes: their module). Sessions
  never edit each other's territory -- they file tasks instead.
- **Tasks are the channel.** A filed task carries symptom, root cause, and
  suggested fix in the body, so the receiving session needs no shared
  conversation state. The Outcome section closes the loop the same way.
- **Every mutation is a scoped commit.** Concurrent sessions -- even on the
  same project -- never sweep each other's files into a commit, and the
  store's git history is the audit trail (and the undo).
- **Deferral absorbs the human.** Anything waiting on a person's call gets
  `taskman defer -reason` (or a posed decision, `defer -question -option
  "Label::why" ...`) and drops out of every `top` sweep until someone answers
  on the board. A blocked loop *poses* its blockers this way -- it never idles
  reciting an "open set" or "waiting on you" list into its turn output.
  Narrated blockers are invisible: the human only sees what reaches the
  decisions inbox and the `(N) taskman` tab badge. So a choice among options
  becomes one decision with an option per choice, a confirmation becomes a
  yes/no decision, and a bare hold becomes a `defer -reason`. If nothing is
  posed and nothing is unblocked, the correct turn is silence, not narration.

### Cross-session blocks (`taskman blocked`)

`defer` is for a blocker only a *human* can clear -- it goes to the decisions
inbox. When a session instead stalls on work **another lane owns** (it can't
touch that code, or the fix is that lane's call), it raises a one-line block
so the owning lane sees it and acts, instead of idling until the next sweep
happens to unstick it. One entry per lane, capped and glanceable.

Add to each session's loop prompt (`~/.claude/CLAUDE.md` or the loop itself):

```
Cross-session unblocking, every cycle:
- Run `taskman blocked` first. If an entry names YOUR lane as owning the
  blocking task, that handoff is yours: clear it, then respond
  `taskman blocked -unblock <their-lane> "<what you did / commit>"`.
- If you go idle because you need work another lane owns and cannot do it
  yourself (ownership or permission), raise one line instead of waiting:
  `taskman blocked <your-lane> "doing <task(s)>; blocked by <task(s)>, owned
  by <lane>"`. Keep it to a sentence -- it is a signal, not a report.
- Re-raising your lane replaces your entry; there is at most one per lane.
- When your block is resolved (listed as `unblocked`, or the blocker is
  done), proceed and clear your own entry: `taskman blocked <your-lane> ""`.
- A blocker waiting on a person is still a `defer`, not a block.
```

## Install

```
go install github.com/freeeve/taskman@latest   # or: make install
```

Coming from a pre-v1 repo-local ledger? `taskman migrate <repo-dir>` copies
it into the store (empty project only) and seeds the order file;
`-prune` removes the old `tasks/` with a pointer commit. The store has no
remote by default -- add a private one to `~/.taskman` if you want backup.
