// Command taskman manages the tasks/ ledger convention shared by Eve's
// repos: one numbered markdown file per task, status carried by filename
// (001_slug.md -> .in-progress.md -> .done.md), deferral carried by an
// orthogonal .deferred marker on top of that status, cross-repo asks filed
// with a filer prefix (qbd_slug.md) and renumbered on adoption. Every mutating
// command commits the touched task files with a git pathspec, so concurrent
// sessions' staged work is never swept along (-no-commit opts out).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
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
		return cmdStatus(rest, InProgress)
	case "done":
		return cmdStatus(rest, Done)
	case "reopen":
		return cmdStatus(rest, Pending)
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

// tasksHere locates the ledger for the current directory.
func tasksHere() (string, []Task, error) {
	dir, err := FindTasksDir(".")
	if err != nil {
		return "", nil, err
	}
	tasks, err := Load(dir)
	return dir, tasks, err
}

// cmdList prints the ledger, open tasks by default, flagging duplicate
// numbers and unadopted cross-repo asks. Done and deferred tasks are hidden
// without -all: keeping deferred work out of the "what should I pick up next"
// set is the point of deferring it. Hidden deferrals are still counted, so
// they cannot silently disappear from the ledger.
func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	all := fs.Bool("all", false, "include done and deferred tasks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, tasks, err := tasksHere()
	if err != nil {
		return err
	}
	dups := Dups(tasks)
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	shown, deferred := 0, 0
	for _, t := range tasks {
		if t.Deferred && t.Status != Done {
			deferred++
		}
		if (t.Status == Done || t.Deferred) && !*all {
			continue
		}
		shown++
		id, note := fmt.Sprintf("%03d", t.Num), ""
		if !t.HasNum {
			id, note = t.Prefix+"_", "unadopted ask"
		} else if dups[t.Num] {
			note = "DUPLICATE NUMBER (taskman fix)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", id, t.StatusLabel(), t.Slug, note)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if shown == 0 {
		fmt.Printf("no open tasks in %s\n", dir)
	}
	if deferred > 0 && !*all {
		fmt.Printf("%d deferred (taskman list -all)\n", deferred)
	}
	return nil
}

// cmdNext prints the next free number.
func cmdNext() error {
	_, tasks, err := tasksHere()
	if err != nil {
		return err
	}
	fmt.Printf("%03d\n", NextNum(tasks))
	return nil
}

// cmdNew creates and commits the next numbered pending task.
func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	desc := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if desc == "" {
		return fmt.Errorf("usage: taskman new [-no-commit] <description>")
	}
	dir, tasks, err := tasksHere()
	if err != nil {
		return err
	}
	num := NextNum(tasks)
	slug := Slugify(desc)
	if slug == "" {
		return fmt.Errorf("description %q yields an empty slug", desc)
	}
	path := filepath.Join(dir, fmt.Sprintf("%03d_%s.md", num, slug))
	body := fmt.Sprintf("# %03d -- %s\n\nOpened %s.\n", num, desc, time.Now().Format("2006-01-02"))
	if err := create(path, body); err != nil {
		return err
	}
	fmt.Println(path)
	autoCommit(*noCommit, dir, fmt.Sprintf("chore(tasks): open %03d %s", num, slug), path)
	return nil
}

// statusVerb names the transition for usage and commit messages.
var statusVerb = map[Status]string{InProgress: "start", Done: "done", Pending: "reopen"}

// cmdStatus renames the matched task to the target status and commits the
// rename.
func cmdStatus(args []string, s Status) error {
	fs := flag.NewFlagSet(statusVerb[s], flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman %s [-no-commit] <number|slug>", statusVerb[s])
	}
	dir, tasks, err := tasksHere()
	if err != nil {
		return err
	}
	t, err := Find(tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.SetStatus(s)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	autoCommit(*noCommit, dir,
		fmt.Sprintf("chore(tasks): %s %s", statusVerb[s], nt.Stem()),
		t.Path(), nt.Path())
	return nil
}

// cmdDefer holds a task on an external decision and commits the rename. The
// reason is mandatory: an unexplained deferral decays into an unexplained
// pending task, and the filename cannot carry the why.
func cmdDefer(args []string) error {
	fs := flag.NewFlagSet("defer", flag.ContinueOnError)
	reason := fs.String("reason", "", "why the task is held (required)")
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman defer -reason <why> [-no-commit] <number|slug>")
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("taskman defer requires -reason: record why this is held, not just that it is")
	}
	dir, tasks, err := tasksHere()
	if err != nil {
		return err
	}
	t, err := Find(tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.Defer(strings.TrimSpace(*reason), time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	autoCommit(*noCommit, dir,
		fmt.Sprintf("chore(tasks): defer %s (%s)", nt.Stem(), strings.TrimSpace(*reason)),
		t.Path(), nt.Path())
	return nil
}

// cmdResume lifts a deferral, returning the task to the working set at the
// status it held, and commits the rename.
func cmdResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman resume [-no-commit] <number|slug>")
	}
	dir, tasks, err := tasksHere()
	if err != nil {
		return err
	}
	t, err := Find(tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.Resume(time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	autoCommit(*noCommit, dir,
		fmt.Sprintf("chore(tasks): resume %s", nt.Stem()),
		t.Path(), nt.Path())
	return nil
}

// cmdAdopt renumbers a prefixed cross-repo ask into the ledger and commits
// the rename.
func cmdAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman adopt [-no-commit] <file|fragment>")
	}
	dir, tasks, err := tasksHere()
	if err != nil {
		return err
	}
	key := strings.TrimSuffix(filepath.Base(fs.Arg(0)), ".md")
	t, err := Find(tasks, key)
	if err != nil {
		return err
	}
	nt, err := t.Adopt(NextNum(tasks))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	autoCommit(*noCommit, dir,
		fmt.Sprintf("chore(tasks): adopt %s as %03d", t.Stem(), nt.Num),
		t.Path(), nt.Path())
	return nil
}

// cmdFile writes a cross-repo ask into another repo's tasks/ at that
// ledger's next free number and commits it there -- the immediate pathspec
// commit is what makes the number claim safe, so no filer-prefix indirection
// is needed anymore (taskman adopt remains for legacy prefixed asks). The
// filer name recorded in the body defaults to the current ledger's repo
// directory name.
func cmdFile(args []string) error {
	fs := flag.NewFlagSet("file", flag.ContinueOnError)
	as := fs.String("as", "", "filer name recorded in the body (default: current repo directory name)")
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: taskman file [-as filer] [-no-commit] <repo-dir> <description>")
	}
	repo, desc := rest[0], strings.TrimSpace(strings.Join(rest[1:], " "))
	filer := *as
	if filer == "" {
		if dir, err := FindTasksDir("."); err == nil {
			filer = filepath.Base(filepath.Dir(dir))
		} else if wd, err := os.Getwd(); err == nil {
			filer = filepath.Base(wd)
		}
	}
	filer = Slugify(filer)
	slug := Slugify(desc)
	if filer == "" || slug == "" {
		return fmt.Errorf("empty filer or slug (filer %q, description %q)", filer, desc)
	}
	dir := filepath.Join(repo, "tasks")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s has no tasks/ directory", repo)
	}
	tasks, err := Load(dir)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Slug == slug {
			return fmt.Errorf("already filed as %s", t.File)
		}
	}
	num := NextNum(tasks)
	path := filepath.Join(dir, fmt.Sprintf("%03d_%s.md", num, slug))
	body := fmt.Sprintf("# %03d -- %s\n\nFiled from %s on %s (cross-repo ask).\n",
		num, desc, filer, time.Now().Format("2006-01-02"))
	if err := create(path, body); err != nil {
		return err
	}
	fmt.Println(path)
	autoCommit(*noCommit, dir,
		fmt.Sprintf("chore(tasks): file %03d %s (cross-repo ask from %s)", num, slug, filer), path)
	return nil
}

// cmdFix repairs duplicate numbers -- the most advanced holder keeps each
// number, losers move to the lowest free slots (gaps first) -- and reports
// gaps that no duplicate fills (numbers already in history are not reused,
// so residual gaps are informational).
func cmdFix(args []string) error {
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	dry := fs.Bool("n", false, "report only, change nothing")
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, tasks, err := tasksHere()
	if err != nil {
		return err
	}
	plan := PlanRepairs(tasks)
	var paths []string
	var moves []string
	for _, r := range plan {
		nt := r.T
		nt.Num = r.Num
		nt.File = nt.Name()
		fmt.Printf("%s -> %s (duplicate %03d)\n", r.T.File, nt.File, r.T.Num)
		if *dry {
			continue
		}
		renamed, err := r.T.Renumber(r.Num)
		if err != nil {
			return err
		}
		paths = append(paths, r.T.Path(), renamed.Path())
		moves = append(moves, fmt.Sprintf("%03d->%03d %s", r.T.Num, renamed.Num, renamed.Slug))
	}
	after := tasks
	if !*dry && len(plan) > 0 {
		if after, err = Load(dir); err != nil {
			return err
		}
	}
	if gaps := Gaps(after); len(gaps) > 0 {
		nums := make([]string, len(gaps))
		for i, g := range gaps {
			nums[i] = fmt.Sprintf("%03d", g)
		}
		fmt.Printf("unfillable gaps (left alone; history may reference them): %s\n",
			strings.Join(nums, ", "))
	}
	if len(plan) == 0 {
		fmt.Println("no duplicate numbers")
		return nil
	}
	if !*dry {
		autoCommit(*noCommit, dir,
			"chore(tasks): renumber duplicate task numbers ("+strings.Join(moves, ", ")+")",
			paths...)
	}
	return nil
}

// create writes a new file, refusing to overwrite an existing task.
func create(path, body string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(body)
	return err
}
