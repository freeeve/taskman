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
taskman start <n|slug>           mark in-progress   (rename)
taskman done <n|slug>            mark done          (rename; prunes order)
taskman reopen <n|slug>          back to pending    (rename)
taskman lane <n|slug> <lane|->   set or clear a task's lane (rename)
taskman defer -reason <why> <n|slug>
                                 hold on an external decision; the reason is
                                 appended to the body and is required
taskman resume <n|slug>          lift a deferral, restoring the status under it
taskman adopt <name>             renumber a legacy prefixed ask into the ledger
taskman feature new <desc>       create a feature spec in features/
taskman feature list [-all]      features with a done-task rollup
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
- **Link the implementing tasks** and keep the `Tasks:` line current: the
  web UI's link picker toggles membership, its "+ task" creates a task
  pre-linked, or edit the line by hand (`Tasks: 012, 019`). Chips and the
  N/M rollup follow the ledger automatically.
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
  work -> semantic commit(s) in the code repo -> append an Outcome section
  to the task file -> `taskman done <n>` (commits outcome + rename together).
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

## Install

```
go install github.com/freeeve/taskman@latest   # or: make install
```

Coming from a pre-v1 repo-local ledger? `taskman migrate <repo-dir>` copies
it into the store (empty project only) and seeds the order file;
`-prune` removes the old `tasks/` with a pointer commit. The store has no
remote by default -- add a private one to `~/.taskman` if you want backup.
