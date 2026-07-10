// Command taskman manages central task ledgers for Eve's projects: one
// directory per project in the taskman store ($TASKMAN_HOME, default
// ~/.taskman, itself a git repository), one numbered markdown file per task,
// status carried by filename (001_slug.md -> .in-progress.md -> .done.md),
// deferral carried by an orthogonal .deferred marker on top of that status.
// Every mutating command commits the touched files with a git pathspec, so
// concurrent sessions' work is never swept along (-no-commit opts out).
package main

import (
	"fmt"
	"os"

	"github.com/freeeve/taskman/internal/task"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "taskman:", err)
		os.Exit(1)
	}
}

// run dispatches the subcommand; no arguments means "list".
func run(args []string) error {
	cmd, rest := "list", args
	if len(args) > 0 {
		cmd, rest = args[0], args[1:]
	}
	switch cmd {
	case "list", "ls":
		return cmdList(rest)
	case "next":
		return cmdNext(rest)
	case "top":
		return cmdTop(rest)
	case "new":
		return cmdNew(rest)
	case "start":
		return cmdStatus(rest, task.InProgress)
	case "done":
		return cmdStatus(rest, task.Done)
	case "reopen":
		return cmdStatus(rest, task.Pending)
	case "defer":
		return cmdDefer(rest)
	case "resume":
		return cmdResume(rest)
	case "lane":
		return cmdLane(rest)
	case "adopt":
		return cmdAdopt(rest)
	case "feature":
		return cmdFeature(rest)
	case "file":
		return cmdFile(rest)
	case "serve":
		return cmdServe(rest)
	case "fix", "doctor":
		return cmdFix(rest)
	case "projects":
		return cmdProjects(rest)
	case "migrate":
		return cmdMigrate(rest)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// usage prints the command summary.
func usage() {
	fmt.Fprint(os.Stderr, `taskman - central task ledger helper

Usage:
  taskman [list] [-all] [-lane L]
                               open tasks (-all includes done and deferred)
  taskman projects             store projects with open/deferred counts
  taskman next                 next free task number
  taskman top [-lane L]        highest-priority pending task (next is a
                               number; top is a task)
  taskman new [-lane L] <description>
                               create the next numbered pending task; the lane
                               token routes it to a session or submodule
  taskman lane <n|slug> <lane|->
                               set or clear a task's lane (rename)
  taskman start <n|slug>       mark in-progress
  taskman done <n|slug>        mark done
  taskman reopen <n|slug>      mark pending again
  taskman defer -reason <why> <n|slug>
                               hold on an external decision: hidden from list,
                               the reason recorded in the task body
  taskman resume <n|slug>      lift a deferral, restoring the prior status
  taskman adopt <name>         renumber a legacy prefixed cross-repo ask into the ledger
  taskman feature new <description>
                               create a feature spec in features/ (source of
                               truth for what the product should do)
  taskman feature list [-all]  features with a done-task rollup
  taskman feature done <slug>  mark a feature shipped
  taskman file [-as filer] <project> <description>
                               file an ask into another project's ledger at
                               its next number, committed immediately
  taskman fix [-n]             renumber duplicate numbers into the lowest free
                               slots (gaps first) and report unfillable gaps
  taskman serve [-addr host:port]
                               kanban web app over the store (localhost only
                               unless -insecure-bind; there is no auth)
  taskman migrate [-prune] <repo-dir> [project]
                               import a repo-local tasks/ ledger into the
                               store (empty project only); -prune removes the
                               source ledger and commits that in its repo

Ledgers live in the central store ($TASKMAN_HOME, default ~/.taskman), one
directory per project. The project is resolved from -p, TASKMAN_PROJECT, the
enclosing git repo's basename, then the cwd basename. Mutating commands
git-commit the touched files in the store (pathspec-scoped); pass -no-commit
after the subcommand to skip that.
`)
}
