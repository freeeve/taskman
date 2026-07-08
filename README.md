# taskman

A tiny CLI for the `tasks/` ledger convention used across these repos: one
markdown file per task, status carried by the filename, numbers minted by the
repo that owns the ledger.

```
001_description.md              pending
001_description.in-progress.md  in progress
001_description.done.md         done
qbd_description.md              cross-repo ask filed by another repo's
                                session ("qbd" = the filer), unnumbered
                                until this repo adopts it
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
taskman adopt <name>         renumber a prefixed cross-repo ask into the
                             ledger and stamp the number into its H1
taskman file [-as prefix] <repo-dir> <description>
                             drop a prefixed ask into another repo's tasks/
                             (prefix defaults to the current repo's dir name)
```

The `tasks/` directory is discovered by walking up from the current
directory, so any subdirectory of a repo works. `start`/`done`/`reopen`
accept a task number or a unique slug fragment; a duplicate number (the
ledgers have historical collisions) or ambiguous fragment errors with the
candidates instead of guessing.

Cross-repo asks are filed with a prefix rather than a number because
numbering authority stays with the receiving repo -- two sessions filing
concurrently would otherwise mint the same number. `taskman adopt` assigns
the next free number at adoption time and records the filed name as a
breadcrumb.

## Install

```
go install github.com/freeeve/taskman@latest   # or: make install
```
