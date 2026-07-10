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

// cmdFile writes a cross-repo ask into another store project's tasks/ at
// that ledger's next free number and commits it -- the immediate pathspec
// commit is what makes the number claim safe. The filer name recorded in the
// body defaults to the project resolved from the current directory.
func cmdFile(args []string) error {
	fs := flag.NewFlagSet("file", flag.ContinueOnError)
	as := fs.String("as", "", "filer name recorded in the body (default: current project)")
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: taskman file [-as filer] [-no-commit] <project> <description>")
	}
	desc := strings.TrimSpace(strings.Join(rest[1:], " "))
	filer := *as
	if filer == "" {
		filer, _ = store.Resolve("")
	}
	filer = task.Slugify(filer)
	slug := task.Slugify(desc)
	if filer == "" || slug == "" {
		return fmt.Errorf("empty filer or slug (filer %q, description %q)", filer, desc)
	}
	p, err := openProject(rest[0])
	if err != nil {
		return err
	}
	for _, t := range p.Tasks {
		if t.Slug == slug {
			return fmt.Errorf("already filed as %s", t.File)
		}
	}
	num := task.NextNum(p.Tasks)
	path := filepath.Join(p.Dir, fmt.Sprintf("%03d_%s.md", num, slug))
	body := fmt.Sprintf("# %03d -- %s\n\nFiled from %s on %s (cross-project ask).\n",
		num, desc, filer, time.Now().Format("2006-01-02"))
	if err := task.Create(path, body); err != nil {
		return err
	}
	fmt.Println(path)
	p.commit(*noCommit, fmt.Sprintf("file %03d %s (cross-project ask from %s)", num, slug, filer), path)
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
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	plan := task.PlanRepairs(p.Tasks)
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
	after := p.Tasks
	if !*dry && len(plan) > 0 {
		if after, err = task.Load(p.Dir); err != nil {
			return err
		}
	}
	if gaps := task.Gaps(after); len(gaps) > 0 {
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
		p.commit(*noCommit,
			"renumber duplicate task numbers ("+strings.Join(moves, ", ")+")", paths...)
	}
	return nil
}
