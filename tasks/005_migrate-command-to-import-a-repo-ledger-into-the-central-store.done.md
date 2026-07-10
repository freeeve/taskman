# 005 -- migrate command to import a repo ledger into the central store

Opened 2026-07-10.

## Outcome

taskman migrate <repo-dir> [project] [-prune]: byte-for-byte copy of
every Parse-accepted file into an empty store project, non-task files
skipped and reported, order file seeded with open numbers ascending,
one scoped store commit; -prune removes the source tasks/ and commits
a pointer in the source repo. Covered by TestMigrate end to end.
