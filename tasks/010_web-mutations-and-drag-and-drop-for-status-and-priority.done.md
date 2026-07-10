# 010 -- web mutations and drag and drop for status and priority

Opened 2026-07-10.

## Outcome

POST tasks / tasks/{n}/status|defer|resume and PUT order, all through
the same task/store code paths and commit convention as the CLI (one
scoped commit per action; done prunes order in-commit). task.New
extracted and shared by cmdNew and the API. Board DnD: across columns
= status, within pending = reorder preserving hidden tasks' relative
positions; deferred cards move via dialog only; dialog action buttons
+ header new-task button. TestAPIMutations asserts rename + commit
subject + order rewrite + 4xx paths per route; board.js node-checked.
