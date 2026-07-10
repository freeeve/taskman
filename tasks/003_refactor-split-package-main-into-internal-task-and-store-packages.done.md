# 003 -- refactor: split package main into internal task and store packages

Opened 2026-07-10.

## Outcome

Split the flat package main into internal/task (Task model, Parse, Load,
Find, mutations, repairs) and internal/store (git plumbing, exported
Commit/AutoCommit). main package now holds only dispatch plus cmd_task.go,
cmd_list.go, cmd_admin.go. Unit and fuzz tests moved to internal/task;
CLI end-to-end tests live in cli_test.go. No behavior change; all files
under 500 lines. Commit: refactor(core): split package main into
internal/task and internal/store packages.
