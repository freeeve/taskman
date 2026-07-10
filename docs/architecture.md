# taskman architecture

## Store layout

```
$TASKMAN_HOME (default ~/.taskman)     git repository, auto-initialized
  README.md                            seed pointer written on first use
  <project>/
    tasks/                             the ledger (see filename grammar)
    features/                          slug.md | slug.done.md
    screenshots/<NNN>/                 images per task number, committed
    order                              priority list, one number per line
```

Every non-dot directory in the store root is a project; dot entries and
top-level files are reserved for taskman. There is no config file.

**Project resolution**, most explicit first: `-p` flag > `TASKMAN_PROJECT`
env var > basename of the enclosing git repo (`git rev-parse
--show-toplevel`) > cwd basename. The result is slugified. A session pins
itself by exporting `TASKMAN_PROJECT`.

## Filename grammar

```
name    = stem [status] [".deferred"] ".md"
status  = ".in-progress" | ".done"          (absent = pending)
stem    = head "_" slug
head    = NUM ["-" lane]                    numbered task
        | prefix                            legacy unadopted cross-repo ask
NUM     = digits (rendered %03d)            maximal leading digit run
lane    = free-form token, may contain "-"  (e.g. impl, e2e, ui-web)
slug    = kebab-case, never contains "_"
```

`012-impl_fix-thing.in-progress.md` = task 12, lane impl, in progress. A
head without leading digits (`qbd`, `qbd-impl`) is a filer prefix, not a
lane. The lane lives inside the stem, so every rename (status, deferral,
renumber) preserves it for free. Deferral is a flag orthogonal to status:
`NNN_slug.in-progress.deferred.md` is "in progress, and held on an external
decision". Numbers form one sequence per project across all lanes; they are
minted at `highest + 1` and never reused.

## Order file

Plain text, one task number per line, top priority first, `#` comments
allowed. Reading is lenient and never errors: blanks, comments, garbage,
non-positive numbers, and repeats are skipped (first occurrence wins).
Consumers treat it as advisory -- listed tasks sort first in file order,
everything else follows in ledger order. Writers rewrite the whole file
(one drag = one commit); `done` and `fix` prune stale numbers inside the
same commit as the change that caused them. Concurrent writers are
last-write-wins; git history recovers anything lost.

## Package map

```
main.go            dispatch and usage only
cmd_task.go        new/start/done/reopen/defer/resume/adopt/lane
cmd_list.go        list/next/top/projects + openProject helper
cmd_admin.go       file/migrate/fix
cmd_feature.go     feature new/list/done
cmd_serve.go       serve flags -> web.Serve

internal/task      the ledger domain: Task, Status, Parse, Load, Find,
                   NextNum/Dups/Gaps/PlanRepairs, SetStatus/Defer/Resume/
                   SetLane/Adopt/Renumber/New, Slugify, AppendSection
internal/store     where ledgers live: Home/Ensure/EnsureProject/Resolve/
                   Projects, order file (Read/Write/Prune/SortByOrder),
                   features (Load/New/SetDone), git plumbing
                   (Commit/AutoCommit with pathspec scoping + index.lock retry)
internal/web       net/http server over the store: JSON API, goldmark GFM
                   rendering, screenshot upload/serving, embedded static UI
internal/web/static  index.html, app.css, board.js, features.js (vanilla)
```

Dependency direction: `web -> store -> task`; the cmd layer uses all three.
goldmark (GFM rendering) is the module's only external dependency.

## Data flow

Every mutation -- CLI command or HTTP request -- follows the same path:
resolve project -> load ledger from disk -> mutate by renaming/writing files
-> `store.AutoCommit` with a pathspec covering exactly the touched paths and
a `chore(<project>): <verb> <stem>` subject. Handlers hold no state; each
HTTP request re-reads the store, so CLI and UI never conflict beyond git's
index lock, which `Commit` retries with jitter.

## HTTP API

| Route | Method | Body -> Response |
|---|---|---|
| `/api/projects` | GET | `[{name, open, deferred}]` |
| `/api/projects/{p}/tasks` | GET | `{tasks: [...], order: [...], lanes: [...]}` (priority-sorted) |
| `/api/projects/{p}/tasks` | POST | `{description, lane}` -> 201 task |
| `/api/projects/{p}/tasks/{n}` | GET | `{task, body, html}` (GFM-rendered) |
| `/api/projects/{p}/tasks/{n}/status` | POST | `{status}` -> task; done prunes order |
| `/api/projects/{p}/tasks/{n}/defer` | POST | `{reason}` (required) -> task |
| `/api/projects/{p}/tasks/{n}/resume` | POST | -> task |
| `/api/projects/{p}/tasks/{n}/screenshots` | POST | multipart `file` -> 201 `{path}` |
| `/api/projects/{p}/order` | PUT | `{order: [...]}` -> 204 |
| `/api/projects/{p}/features` | GET | `[{slug, done, title, html, tasks: [{num, status}]}]` |
| `/api/projects/{p}/features` | POST | `{description}` -> 201 |
| `/api/projects/{p}/features/{slug}/done` | POST | -> 200 |
| `/shots/{p}/{n}/{file}` | GET | image bytes |
| `/`, `/static/*` | GET | embedded UI |

Errors are `{"error": "..."}` with 4xx/5xx; task lookups reuse `task.Find`,
so ambiguity errors surface verbatim. Path segments are validated against
the slug alphabet before touching the filesystem (the traversal guard).
Uploads are capped at 10MB and content-sniffed to png/jpeg/gif/webp.

## Screenshots

Stored at `<project>/screenshots/<NNN>/<yyyymmdd-hhmmss>[-k].<ext>`, keyed
by the bare task number so attachments survive renames and lane moves. The
upload appends a `## Screenshot <date>` section with a tasks-relative link
(`../screenshots/NNN/f.png`) to the task body and commits image and body
together. The rendered HTML rewrites those links through `/shots/` so they
display inline. Keeping images outside `tasks/` is the token-cost mechanism:
agents read ledgers, never image bytes.

## Migration

`taskman migrate <repo-dir> [project] [-prune]` copies every parseable task
file byte-for-byte into an **empty** project (merging is out of scope),
reports skipped non-task files, seeds `order` with open numbers ascending,
and makes one store commit. `-prune` removes the source `tasks/` and commits
a pointer in the source repo.
