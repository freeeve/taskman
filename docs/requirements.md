# taskman requirements

taskman is the backbone of a multi-project development workflow where most
work is done by Claude Code sessions and the human steers via a lightweight
web UI. This document records what the tool must do and why; architecture.md
records how it is built; design.md records the decisions and assumptions
made along the way.

## The workflow being served

- Several projects are developed in parallel, each in its own git repo.
- Each project typically runs **two Claude Code sessions**: one implements
  code changes and cuts releases (`impl`), one builds and maintains
  end-to-end tests (`e2e`). Complex projects may add more sessions or split
  by submodule.
- The human prioritizes work visually (kanban-style drag and drop) and
  reviews specs; agents pick up the top of the queue and report back by
  moving tasks and appending outcomes.
- Requirements, architecture, and design decisions are documented in
  markdown, close to the work they describe.

## Functional requirements

1. **Central store.** All task ledgers live outside the code repos, in one
   taskman-owned directory (`$TASKMAN_HOME`, default `~/.taskman`), one
   subdirectory per project. Repo-local `tasks/` directories are no longer
   supported (hard cutover); `taskman migrate` imports them.
2. **Store history.** The store is a local git repository. Every mutation is
   a pathspec-scoped semantic commit, so history is the audit trail and any
   mistake is recoverable. No remote is required.
3. **Tasks.** One numbered markdown file per task. The filename is the
   single source of truth for state: pending, in-progress, done, plus an
   orthogonal deferred flag with a mandatory reason. Numbers are minted once
   and never reused.
4. **Lanes.** A task can carry a routing token in its filename
   (`012-impl_fix-thing.md`) assigning it to a session, submodule, or
   workstream. Lanes survive every status change without bookkeeping.
5. **Priority.** Each project may have an `order` file listing task numbers
   top-first. It is advisory: garbage never breaks a ledger, unlisted tasks
   simply sort after listed ones. `taskman top` answers "what should I pick
   up"; drag and drop in the web UI rewrites the file in one commit.
6. **Features.** Each project has a `features/` directory of markdown files,
   the source of truth for what the product should do. A feature links its
   implementing tasks via a `Tasks:` line and is marked shipped by renaming
   to `.done.md`. Requirements and design notes live in the feature body.
7. **Web UI.** `taskman serve` runs a localhost kanban board over the store:
   columns by status, drag across columns to change status, drag within
   pending to reprioritize, lane filter and swimlanes, deferred toggle, a
   features view with per-task status chips, and full GitHub-flavored
   markdown rendering of task and feature bodies. Every UI action commits
   exactly like the equivalent CLI command.
8. **Screenshots.** Images attach to tasks by paste or drop in the web UI,
   are stored under `<project>/screenshots/<NNN>/` -- outside `tasks/` -- and
   are linked from the task body. Agent sessions read ledgers, never image
   bytes, so screenshots cost no tokens.
9. **Cross-project asks.** `taskman file <project> <description>` files a
   task into another project's ledger at that ledger's next number,
   committed immediately so the number claim is safe.
10. **Agent ergonomics.** Everything an agent needs is a plain file or a CLI
    command with stable output: `top` to pick work, `start`/`done` to move
    it, `defer -reason` to park it, `new`/`file` to create work. Sessions pin
    themselves with `TASKMAN_PROJECT` and a lane convention.
11. **Resource locks.** `taskman lock` gives sessions that share the machine
    but nothing else -- benchmark sweeps in sibling repos -- mutual exclusion
    over a named resource, so overlapping runs cannot silently inflate each
    other's timings. Locks are per-resource (runs that contend for different
    hardware never serialize), TTL-bounded and heartbeated (a killed holder
    frees the resource; it does not wedge it), token-checked on release (a
    broken holder cannot drop its successor's lock), and machine state rather
    than ledger history (gitignored, never committed).

## Non-functional requirements

- Go, stdlib plus a single dependency (goldmark for GFM rendering).
- Vanilla HTML/CSS/JS embedded via go:embed; no npm, no build step.
- Source files under 500 lines; gofmt -s clean; semantic commits; >80% test
  coverage with fuzz tests on every parser.
- The web server binds loopback only unless explicitly overridden; it has no
  authentication.

## Session conventions

The task-tracking convention lives once in the **global** `~/.claude/CLAUDE.md`
(project names resolve from the repo a session stands in, so the same text
serves every repo; see the README's "Agent sessions" section for the
snippet). A session pins itself with `TASKMAN_PROJECT` when the repo
basename is not the project name, and works its lane via
`taskman top -lane <lane>`.
