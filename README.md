# taskman

A tiny CLI for the `tasks/` ledger convention used across these repos: one
markdown file per task, status carried by the filename, numbers minted by the
repo that owns the ledger.

```
001_description.md              pending
001_description.in-progress.md  in progress
001_description.done.md         done
qbd_description.md              LEGACY cross-repo ask filed unnumbered by
                                another repo's session ("qbd" = the filer);
                                renumber with `taskman adopt`
```

## Usage

```
taskman [list] [-all]        open tasks (-all includes done); flags duplicate
                             numbers and unadopted asks
taskman next                 next free task number
taskman new <description>    create the next numbered pending task
taskman start <n|slug>       mark in-progress   (rename)
taskman done <n|slug>        mark done          (rename)
taskman reopen <n|slug>      back to pending    (rename)
taskman adopt <name>         renumber a legacy prefixed cross-repo ask into
                             the ledger and stamp the number into its H1
taskman file [-as filer] <repo-dir> <description>
                             file a cross-repo ask into another repo's
                             tasks/ at THAT ledger's next number, committed
                             there immediately (filer credit defaults to the
                             current repo's dir name; duplicate slugs refuse)
taskman fix [-n]             repair the ledger: duplicate numbers are
                             renumbered into the lowest free slots (gaps
                             first) with the H1 restamped; -n reports only
```

The `tasks/` directory is discovered by walking up from the current
directory, so any subdirectory of a repo works. `start`/`done`/`reopen`
accept a task number or a unique slug fragment; a duplicate number (the
ledgers have historical collisions) or ambiguous fragment errors with the
candidates instead of guessing.

Every mutating command commits the touched task files automatically with a
pathspec-scoped `git add`/`git commit` (`chore(tasks): …`), so a concurrent
session's staged work in the same repo is never swept into the commit. Pass
`-no-commit` after the subcommand to skip it; outside a git repo the
operation still succeeds with a warning.

`fix` picks each duplicate's keeper deterministically -- the most advanced
status wins (done > in-progress > pending; ledger order breaks ties), since
the furthest-along task is the one history most likely references. Gaps no
duplicate can fill are reported but never compacted: task numbers appear in
commit messages and docs, so reusing or shifting them would corrupt
references.

Cross-repo asks are numbered at filing time: the immediate pathspec commit
in the receiving repo is what makes the number claim safe (the historical
prefix convention existed because asks used to sit uncommitted, invisible to
concurrent sessions). `taskman adopt` remains for legacy prefixed asks and
assigns the next free number, recording the filed name as a breadcrumb.

## Install

```
go install github.com/freeeve/taskman@latest   # or: make install
```
