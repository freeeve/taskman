# 009 -- serve command with read-only json api and kanban board shell

Opened 2026-07-10.

## Outcome

internal/web: stateless Handler over the store (re-reads disk per
request), GET /api/projects, /tasks (order-sorted + lanes), /tasks/{n}
(raw body + goldmark GFM html; goldmark is the module's first and only
dependency), /features (status chips). Embedded vanilla UI: three
columns, project switcher, lane filter, swimlanes, deferred
badge+toggle, capped done column, task dialog with rendered markdown.
Loopback-only bind unless -insecure-bind; slug-validated path segments
as traversal guard. httptest coverage plus a live curl smoke test.
