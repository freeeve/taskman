// Command taskman manages the tasks/ ledger convention shared by Eve's
// repos: one numbered markdown file per task, status carried by filename
// (001_slug.md -> .in-progress.md -> .done.md), deferral carried by an
// orthogonal .deferred marker on top of that status, cross-repo asks filed
// with a filer prefix (qbd_slug.md) and renumbered on adoption. Every mutating
// command commits the touched task files with a git pathspec, so concurrent
// sessions' staged work is never swept along (-no-commit opts out).
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
		return cmdNext()
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
	case "adopt":
		return cmdAdopt(rest)
	case "file":
		return cmdFile(rest)
	case "fix", "doctor":
		return cmdFix(rest)
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
	fmt.Fprint(os.Stderr, `taskman - tasks/ ledger helper

Usage:
  taskman [list] [-all]        open tasks (-all includes done and deferred)
  taskman next                 next free task number
  taskman new <description>    create the next numbered pending task
  taskman start <n|slug>       mark in-progress
  taskman done <n|slug>        mark done
  taskman reopen <n|slug>      mark pending again
  taskman defer -reason <why> <n|slug>
                               hold on an external decision: hidden from list,
                               the reason recorded in the task body
  taskman resume <n|slug>      lift a deferral, restoring the prior status
  taskman adopt <name>         renumber a legacy prefixed cross-repo ask into the ledger
  taskman file [-as filer] <repo-dir> <description>
                               file a cross-repo ask into another repo's tasks/
                               at that ledger's next number, committed there
  taskman fix [-n]             renumber duplicate numbers into the lowest free
                               slots (gaps first) and report unfillable gaps

The tasks/ directory is found by walking up from the current directory.
Mutating commands git-commit the touched task files (pathspec-scoped);
pass -no-commit after the subcommand to skip that.
`)
}
