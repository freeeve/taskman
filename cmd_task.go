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
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	lane := fs.String("lane", "", "routing token carried in the filename (012-impl_slug.md)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	desc := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if desc == "" {
		return fmt.Errorf("usage: taskman new [-p project] [-lane lane] [-no-commit] <description>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	num := task.NextNum(p.Tasks)
	slug := task.Slugify(desc)
	if slug == "" {
		return fmt.Errorf("description %q yields an empty slug", desc)
	}
	t := task.Task{Dir: p.Dir, Num: num, HasNum: true, Slug: slug, Lane: task.Slugify(*lane)}
	if *lane != "" && t.Lane == "" {
		return fmt.Errorf("lane %q yields an empty token", *lane)
	}
	t.File = t.Name()
	body := fmt.Sprintf("# %03d -- %s\n\nOpened %s.\n", num, desc, time.Now().Format("2006-01-02"))
	if err := task.Create(t.Path(), body); err != nil {
		return err
	}
	fmt.Println(t.Path())
	p.commit(*noCommit, fmt.Sprintf("open %s", t.Stem()), t.Path())
	return nil
}

// cmdLane sets or clears ("-") a task's lane token and commits the rename.
func cmdLane(args []string) error {
	fs := flag.NewFlagSet("lane", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: taskman lane [-p project] [-no-commit] <number|slug> <lane|->")
	}
	lane := fs.Arg(1)
	if lane == "-" {
		lane = ""
	} else if lane = task.Slugify(lane); lane == "" {
		return fmt.Errorf("lane %q yields an empty token", fs.Arg(1))
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.SetLane(lane)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	verb := "lane " + lane
	if lane == "" {
		verb = "clear lane"
	}
	p.commit(*noCommit, fmt.Sprintf("%s %s", verb, nt.Stem()), t.Path(), nt.Path())
	return nil
}

// statusVerb names the transition for usage and commit messages.
var statusVerb = map[task.Status]string{task.InProgress: "start", task.Done: "done", task.Pending: "reopen"}

// cmdStatus renames the matched task to the target status and commits the
// rename.
func cmdStatus(args []string, s task.Status) error {
	fs := flag.NewFlagSet(statusVerb[s], flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman %s [-p project] [-no-commit] <number|slug>", statusVerb[s])
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.SetStatus(s)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	paths := []string{t.Path(), nt.Path()}
	if s == task.Done && nt.HasNum {
		op, err := store.PruneOrder(filepath.Dir(p.Dir), map[int]bool{nt.Num: true})
		if err != nil {
			return err
		}
		if op != "" {
			paths = append(paths, op)
		}
	}
	p.commit(*noCommit, fmt.Sprintf("%s %s", statusVerb[s], nt.Stem()), paths...)
	return nil
}

// cmdDefer holds a task on an external decision and commits the rename. The
// reason is mandatory: an unexplained deferral decays into an unexplained
// pending task, and the filename cannot carry the why.
func cmdDefer(args []string) error {
	fs := flag.NewFlagSet("defer", flag.ContinueOnError)
	reason := fs.String("reason", "", "why the task is held (required)")
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman defer -reason <why> [-p project] [-no-commit] <number|slug>")
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("taskman defer requires -reason: record why this is held, not just that it is")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.Defer(strings.TrimSpace(*reason), time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	p.commit(*noCommit, fmt.Sprintf("defer %s (%s)", nt.Stem(), strings.TrimSpace(*reason)),
		t.Path(), nt.Path())
	return nil
}

// cmdResume lifts a deferral, returning the task to the working set at the
// status it held, and commits the rename.
func cmdResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman resume [-p project] [-no-commit] <number|slug>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.Resume(time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	p.commit(*noCommit, fmt.Sprintf("resume %s", nt.Stem()), t.Path(), nt.Path())
	return nil
}

// cmdAdopt renumbers a prefixed cross-repo ask into the ledger and commits
// the rename.
func cmdAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman adopt [-p project] [-no-commit] <file|fragment>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	key := strings.TrimSuffix(filepath.Base(fs.Arg(0)), ".md")
	t, err := task.Find(p.Tasks, key)
	if err != nil {
		return err
	}
	nt, err := t.Adopt(task.NextNum(p.Tasks))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	p.commit(*noCommit, fmt.Sprintf("adopt %s as %03d", t.Stem(), nt.Num), t.Path(), nt.Path())
	return nil
}
