package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

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
	num := task.NextNum(tasks)
	slug := task.Slugify(desc)
	if slug == "" {
		return fmt.Errorf("description %q yields an empty slug", desc)
	}
	path := filepath.Join(dir, fmt.Sprintf("%03d_%s.md", num, slug))
	body := fmt.Sprintf("# %03d -- %s\n\nOpened %s.\n", num, desc, time.Now().Format("2006-01-02"))
	if err := task.Create(path, body); err != nil {
		return err
	}
	fmt.Println(path)
	store.AutoCommit(*noCommit, dir, fmt.Sprintf("chore(tasks): open %03d %s", num, slug), path)
	return nil
}

// statusVerb names the transition for usage and commit messages.
var statusVerb = map[task.Status]string{task.InProgress: "start", task.Done: "done", task.Pending: "reopen"}

// cmdStatus renames the matched task to the target status and commits the
// rename.
func cmdStatus(args []string, s task.Status) error {
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
	t, err := task.Find(tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.SetStatus(s)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	store.AutoCommit(*noCommit, dir,
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
	t, err := task.Find(tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.Defer(strings.TrimSpace(*reason), time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	store.AutoCommit(*noCommit, dir,
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
	t, err := task.Find(tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.Resume(time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	store.AutoCommit(*noCommit, dir,
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
	t, err := task.Find(tasks, key)
	if err != nil {
		return err
	}
	nt, err := t.Adopt(task.NextNum(tasks))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	store.AutoCommit(*noCommit, dir,
		fmt.Sprintf("chore(tasks): adopt %s as %03d", t.Stem(), nt.Num),
		t.Path(), nt.Path())
	return nil
}
