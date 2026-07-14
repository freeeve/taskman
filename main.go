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
	"runtime/debug"

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
	case "decisions":
		return cmdDecisions(rest)
	case "lane":
		return cmdLane(rest)
	case "lock":
		return cmdLock(rest)
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
	case "rmproject":
		return cmdRmProject(rest)
	case "migrate":
		return cmdMigrate(rest)
	case "version", "-version", "--version":
		return cmdVersion()
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// cmdVersion prints what this binary was built from (VCS revision, time,
// dirty marker), so skew between the PATH cli and a running server is
// diagnosable rather than a mystery flag error.
func cmdVersion() error {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Println("taskman (no build info)")
		return nil
	}
	rev, when, dirty := "", "", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = " (modified)"
			}
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev == "" {
		fmt.Println("taskman", info.Main.Version)
		return nil
	}
	fmt.Printf("taskman %s %s%s\n", rev, when, dirty)
	return nil
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
  taskman defer -question <q> -option "Label::why" -option ... <n|slug>
                               hold on a structured question the web dialog
                               (or resume -choose) can answer
  taskman resume <n|slug>      lift a deferral, restoring the prior status
  taskman resume -choose <label> <n|slug>
                               answer the task's decision and jump it to the
                               top of the priority order (-choose-other for
                               free text)
  taskman decisions [-all]     tasks holding an unanswered decision; -all
                               sweeps every project in the store
  taskman lock acquire [-ttl 45m] [-wait 30m] [-reason why] <resource>
                               take a machine-wide lock on a contended
                               resource (local-cpu, ragedb-ec2, ...) so
                               concurrent sessions don't overlap on it; prints
                               the holder token on stdout and exits non-zero if
                               a live holder outlasts -wait
  taskman lock run [-ttl 45m] [-wait 30m] <resource> -- <command>
                               hold the resource for one command, heartbeating
                               while it runs and releasing when it exits
  taskman lock release|heartbeat [-token t] <resource>
                               drop or refresh a lock you hold (the token
                               defaults to $TASKMAN_LOCK_TOKEN)
  taskman lock status [<resource>]
                               who holds what, for how long, and why
  taskman lock steal <resource>
                               break a wedged holder's lock (human override)
  taskman adopt <name>         renumber a legacy prefixed cross-repo ask into the ledger
  taskman feature new <description>
                               create a feature spec in features/ (source of
                               truth for what the product should do)
  taskman feature list [-all]  features with a done-task rollup
  taskman feature done <slug>  mark a feature shipped
  taskman feature reopen <slug>
                               un-ship a feature (back to active)
  taskman feature rm <slug>    discard a feature spec (linked tasks stay;
                               the removal commit makes it undoable)
  taskman file [-as filer] <project> <description>
                               file an ask into another project's ledger at
                               its next number, committed immediately; the
                               target is a bare project name (libcat), never
                               a path
  taskman rmproject [-force] <project>
                               remove a project from the store as one scoped
                               commit; refuses open tasks without -force
  taskman fix [-n]             renumber duplicate numbers into the lowest free
                               slots (gaps first) and report unfillable gaps
  taskman version              build revision of this binary (diagnose
                               cli/server skew)
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
