package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

// cmdMigrate imports a repo-local tasks/ ledger into the central store,
// byte-for-byte and only into an empty project (a merge story is deliberately
// out of scope). Open task numbers seed the project's order file as a
// no-opinion-yet priority baseline. -prune removes the source ledger and
// commits a pointer in its repo.
func cmdMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	prune := fs.Bool("prune", false, "remove the source tasks/ and commit the removal in the source repo")
	noCommit := fs.Bool("no-commit", false, "skip the git commits")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("usage: taskman migrate [-prune] [-no-commit] <repo-dir> [project]")
	}
	repo := fs.Arg(0)
	src := filepath.Join(repo, "tasks")
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s has no tasks/ directory", repo)
	}
	name := fs.Arg(1)
	if name == "" {
		abs, err := filepath.Abs(repo)
		if err != nil {
			return err
		}
		name = filepath.Base(abs)
	}
	p, err := openProject(name)
	if err != nil {
		return err
	}
	if len(p.Tasks) > 0 {
		return fmt.Errorf("project %s already has %d tasks; migrate only fills empty ledgers", p.Name, len(p.Tasks))
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	copied := 0
	var open []int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		t, ok := task.Parse(src, e.Name())
		if !ok {
			fmt.Printf("skipped (not a task file): %s\n", e.Name())
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := task.Create(filepath.Join(p.Dir, e.Name()), string(data)); err != nil {
			return err
		}
		copied++
		if t.HasNum && t.Status != task.Done {
			open = append(open, t.Num)
		}
	}
	if copied == 0 {
		return fmt.Errorf("no task files found in %s", src)
	}
	orderPath := store.OrderPath(filepath.Dir(p.Dir))
	if len(open) > 0 {
		sort.Ints(open)
		if _, err := store.WriteOrder(filepath.Dir(p.Dir), open); err != nil {
			return err
		}
	}
	fmt.Printf("migrated %d tasks from %s to %s\n", copied, src, p.Dir)
	p.commit(*noCommit, fmt.Sprintf("migrate %d tasks from %s", copied, repo), p.Dir, orderPath)
	if !*prune {
		fmt.Printf("source ledger left in place; remove it with:\n  taskman migrate -prune, or\n  git -C %s rm -r tasks && git -C %s commit -m 'chore(tasks): ledger moved to taskman store'\n", repo, repo)
		return nil
	}
	if err := os.RemoveAll(src); err != nil {
		return err
	}
	store.AutoCommit(*noCommit, repo,
		fmt.Sprintf("chore(tasks): ledger moved to central taskman store (project %s)", p.Name), src)
	return nil
}

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
	if filer == "" {
		return fmt.Errorf("empty filer (pass -as or run inside a project)")
	}
	if err := task.CheckSlug(slug); err != nil {
		return err
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

// cmdRmProject removes a whole project from the store as one pathspec-scoped
// commit -- the safe replacement for hand-run git in the shared repo, where a
// bare commit sweeps other sessions' staged work. Projects with open or
// deferred tasks are refused without -force.
func cmdRmProject(args []string) error {
	fs := flag.NewFlagSet("rmproject", flag.ContinueOnError)
	force := fs.Bool("force", false, "remove even with open or deferred tasks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman rmproject [-force] <project>")
	}
	home, err := store.Ensure()
	if err != nil {
		return err
	}
	if err := store.AcquireProcessLock(home); err != nil {
		return err
	}
	names, err := store.Projects(home)
	if err != nil {
		return err
	}
	if !slices.Contains(names, fs.Arg(0)) {
		return fmt.Errorf("no project %q in the store", fs.Arg(0))
	}
	name := fs.Arg(0)
	tasks, err := task.Load(filepath.Join(home, name, "tasks"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	open := 0
	for _, t := range tasks {
		if t.Status != task.Done {
			open++
		}
	}
	if open > 0 && !*force {
		return fmt.Errorf("project %s has %d open or deferred tasks; pass -force to remove anyway", name, open)
	}
	msg := fmt.Sprintf("chore(%s): remove project", name)
	if err := store.RemoveProject(home, name, msg); err != nil {
		return err
	}
	fmt.Println("committed:", msg)
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
	// Doctor duty: drop order entries whose task is gone or done. Advisory
	// reads ignore them anyway; this keeps the file honest.
	if !*dry {
		open := map[int]bool{}
		for _, t := range after {
			if t.HasNum && t.Status != task.Done {
				open[t.Num] = true
			}
		}
		gone := map[int]bool{}
		for _, n := range store.ReadOrder(filepath.Dir(p.Dir)) {
			if !open[n] {
				gone[n] = true
			}
		}
		op, err := store.PruneOrder(filepath.Dir(p.Dir), gone)
		if err != nil {
			return err
		}
		if op != "" {
			fmt.Println("pruned stale order entries")
			paths = append(paths, op)
		}
	}
	if len(paths) == 0 {
		if len(plan) == 0 {
			fmt.Println("no duplicate numbers")
		}
		return nil
	}
	if !*dry {
		msg := "prune stale order entries"
		if len(moves) > 0 {
			msg = "renumber duplicate task numbers (" + strings.Join(moves, ", ") + ")"
		}
		p.commit(*noCommit, msg, paths...)
	}
	return nil
}
