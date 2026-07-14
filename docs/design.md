# taskman design decisions

The log of decisions and assumptions behind the v1 (central store) design,
so future changes argue with the reasoning, not just the code. Newest
context first appears in git history; this file records the durable "why".

## Decisions

**Markdown files, not a database.** Agents are the primary writers, and
they are best at reading, grepping, and editing plain files with ordinary
tools. The web UI renders the same files; nothing owns a cache. This also
keeps the whole system inspectable with `ls` and `git log`.

**The store is a git repo; every mutation commits.** History is the audit
trail, the undo mechanism, and the concurrency story. Pathspec-scoped
commits mean concurrent sessions (even on different projects in the same
store) never sweep each other's work into a commit. A bounded, jittered
retry absorbs transient `index.lock` collisions; a crashed git may need a
manual `rm .git/index.lock` (accepted for a local tool).

**No config file.** Every non-dot directory in the store root is a project;
resolution is flag > env > repo basename > cwd basename. A config becomes
worth its complexity only if two different repos share a basename and the
`-p`/`TASKMAN_PROJECT` escape hatches prove insufficient -- that is the
revisit trigger.

**Hard cutover from repo-local ledgers.** Supporting both modes forever
doubles every code path and test. `migrate` (with `-prune`) is the bridge;
the old `taskman file <repo-dir>` shape became `taskman file <project>`.

**Lane in the filename, not the body.** The filename is already the single
source of truth for state; putting the lane anywhere else (frontmatter, a
sidecar) would create a second metadata layer to parse and keep consistent.
As part of the stem, the lane survives every rename for free. Heads without
leading digits stay legacy filer prefixes, so `qbd-impl_x.md` is a prefix,
not a lane.

**One number sequence per project, across lanes.** Numbers are identity,
not priority or grouping. Splitting sequences per lane would break `Find`,
cross-references, and renumber repairs for no gain.

**Order file is advisory and lenient.** Priority is an opinion, not an
invariant: a stale or garbled order file must never break a ledger. Reads
skip anything unparseable; unknown numbers are ignored; unlisted tasks sort
after listed ones by number. Whole-file rewrite keeps a drag to one small
commit; concurrent reorders are last-write-wins (git history recovers).
`new` never touches the file -- an unprioritized task simply lands at the
bottom.

**`next` is a number; `top` is a task.** `next` (the next free number) is
kept stable because scripts may depend on it; the "what should I pick up"
question got its own verb instead of overloading it.

**Deferral stays a flag** (carried over from the pre-store design): it is
orthogonal to progress, requires a reason because the filename cannot carry
the why, and plays no part in duplicate-number repairs.

**Features are files; linking is a body edit.** Every `.md` in `features/`
is a feature; long requirements live in the feature body rather than
sibling documents (one convention, no ambiguity about what is a feature).
The `Tasks:` line is parsed leniently. No `feature link` command in v1 --
editing one line by hand or via the UI did not justify a command surface.
(The web UI later gained a link picker and pre-linked task creation over
the same line; the CLI surface stayed as decided.)

**Features stay flat -- depth lives inside the file.** (2026-07-11, user
decision.) No parent/child hierarchy of feature files: one
`features/<slug>.md` per feature, expressing whatever granularity it needs
internally with nested headings and checklists. Keeps the store flat,
greppable, and git-friendly, and required no model or migration change.
Revisit trigger: genuinely needing cross-file rollups (epic-level progress
across many features).

**goldmark, server-side, as the only dependency.** Full GFM was wanted;
hand-rolling GFM is irresponsible and a vendored JS renderer would be a
larger, less maintained dependency than goldmark (pure Go, zero transitive
deps, Hugo's engine). The browser stays vanilla.

**Screenshots are committed binaries outside `tasks/`.** Outside `tasks/`
so agents never spend tokens on image bytes (the CLAUDE.md snippet makes
that explicit); committed so an accidental delete or overwrite is
recoverable. Local-only store makes repo growth acceptable. Directories are
keyed by bare task number so attachments survive renames and lane moves.

**`serve` is localhost-only and unauthenticated.** It is a personal tool on
a trusted machine. Non-loopback binds require `-insecure-bind`, and the
refusal message says why. Anything on the machine can mutate the ledger --
accepted.

**Migrate fills empty projects only.** A merge story (renumbering
collisions across two live ledgers) is complexity with no current user;
refusing loudly is safer than guessing.

**Stateless handlers.** Every request re-reads disk. The ledgers are small
(hundreds of files); correctness and CLI/UI coexistence beat microseconds.

**Resource locks are not task status.** The obvious cheap lock -- a well-known
task you `start` to claim and `done` to release -- is wrong, and instructively
so: the store is a multi-writer git ledger with no cross-process locking, which
is exactly why `taskman fix` has to repair duplicate numbers. A status-flag
claim races the same way, so two sessions would both "acquire" and both
benchmark. Mutual exclusion needs an atomic primitive, so `taskman lock` rests
on `link(2)` into a gitignored `.locks/` and commits nothing. The ledger gives
an audit trail; it does not give exclusion.

**Locks are machine-scoped and per-resource.** CPU contention is a property of
the box, not of a project, so the lock lives in the store root (the one path
every session shares) and names a resource rather than a repo. One global
`bench` mutex would serialize a remote RageDB sweep behind a local Rust sweep,
which contend for nothing -- costing throughput without buying accuracy.

**A lock only excludes cooperating processes.** `taskman lock` cannot stop an
unrelated VM from saturating the machine, so a caller still needs a pre-flight
load gate and a post-flight canary on the host under test. Taskman owns mutual
exclusion; it does not own load measurement.

## Assumptions

- One human, one machine, local trust boundary; no remote store sync. If
  backup is wanted, add a private git remote to `~/.taskman` by hand.
- Ledgers stay small enough (<~1000 tasks/project) that full reloads and
  per-task title reads are negligible.
- Claude Code sessions honor CLAUDE.md conventions (TASKMAN_PROJECT, lane,
  no screenshot reads); taskman does not technically enforce them.
- Two sessions per project is convention, not a limit -- lanes are free-form.
