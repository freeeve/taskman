# 004 -- central store in TASKMAN_HOME with project resolution and hard cutover from repo-local tasks

Opened 2026-07-10.

## Outcome

Ledgers moved to $TASKMAN_HOME (default ~/.taskman), a git repo taskman
auto-initializes, one directory per project (tasks/, features/,
screenshots/). Resolution: -p flag > TASKMAN_PROJECT > git toplevel
basename > cwd basename, slugified. FindTasksDir deleted (hard cutover);
file targets a store project; commits scoped chore(<project>); new
projects command; index.lock retry in store.Commit. Tests: resolution
precedence table, store-based end-to-end, two-project pathspec
isolation, transient-lock retry.

NOTE for this repo: taskman's own ledger still lives in tasks/ here
until the migrate command (005) exists and the real migration runs
(014); the freshly built binary can no longer see it, so ledger chores
in the meantime use the previously built bin/taskman (pre-cutover) or
manual renames + commits.
