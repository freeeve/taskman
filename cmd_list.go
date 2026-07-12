package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

// proj is a resolved store project with its ledger loaded.
type proj struct {
	Home  string // store root (the git repo)
	Name  string // project name (directory under the root)
	Dir   string // the project's tasks/ directory
	Tasks []task.Task
}

// openProject ensures the store exists, resolves flagVal (falling back to
// TASKMAN_PROJECT, the enclosing repo's basename, then the cwd basename) to a
// project, creates its skeleton on first use, and loads its ledger.
//
// It also takes the cross-process store lock for the remainder of this
// invocation (released at process exit): every mutating command is a
// check-then-act over the ledger loaded here, and two unlocked CLIs racing
// the same project mint duplicate task numbers.
func openProject(flagVal string) (proj, error) {
	home, err := store.Ensure()
	if err != nil {
		return proj{}, err
	}
	if err := store.AcquireProcessLock(home); err != nil {
		return proj{}, err
	}
	name, err := store.Resolve(flagVal)
	if err != nil {
		return proj{}, err
	}
	pdir, err := store.EnsureProject(home, name)
	if err != nil {
		return proj{}, err
	}
	dir := filepath.Join(pdir, "tasks")
	tasks, err := task.Load(dir)
	if err != nil {
		return proj{}, err
	}
	return proj{Home: home, Name: name, Dir: dir, Tasks: tasks}, nil
}

// commit auto-commits paths in the store repo under the project-scoped
// conventional message.
func (p proj) commit(noCommit bool, msg string, paths ...string) {
	store.AutoCommit(noCommit, p.Home, fmt.Sprintf("chore(%s): %s", p.Name, msg), paths...)
}

// cmdList prints the ledger, open tasks by default, flagging duplicate
// numbers and unadopted cross-repo asks. Done and deferred tasks are hidden
// without -all: keeping deferred work out of the "what should I pick up next"
// set is the point of deferring it. Hidden deferrals are still counted, so
// they cannot silently disappear from the ledger.
func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	all := fs.Bool("all", false, "include done and deferred tasks")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	lane := fs.String("lane", "", "only tasks in this lane")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	dups := task.Dups(p.Tasks)
	ordered := store.SortByOrder(p.Tasks, store.ReadOrder(filepath.Dir(p.Dir)))
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	shown, deferred, decisions := 0, 0, 0
	for _, t := range ordered {
		if *lane != "" && t.Lane != *lane {
			continue
		}
		if t.Deferred && t.Status != task.Done {
			deferred++
			if hasLiveDecision(t) {
				decisions++
			}
		}
		if (t.Status == task.Done || t.Deferred) && !*all {
			continue
		}
		shown++
		id, note := fmt.Sprintf("%03d", t.Num), ""
		if !t.HasNum {
			id, note = t.Prefix+"_", "unadopted ask"
		} else if dups[t.Num] {
			note = "DUPLICATE NUMBER (taskman fix)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id, t.StatusLabel(), t.Lane, t.Slug, note)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if shown == 0 {
		fmt.Printf("no open tasks in project %s\n", p.Name)
	}
	if deferred > 0 && !*all {
		fmt.Printf("%d deferred (taskman list -all)\n", deferred)
	}
	if decisions > 0 {
		fmt.Printf("%d awaiting a decision (taskman decisions)\n", decisions)
	}
	return nil
}

// hasLiveDecision reports whether a task's body holds an unanswered decision
// block -- the "waiting on an answerable question" subset of deferrals.
func hasLiveDecision(t task.Task) bool {
	body, err := os.ReadFile(t.Path())
	if err != nil {
		return false
	}
	_, live := task.ParseDecision(string(body))
	return live
}

// liveDecisions counts the ledger's tasks holding an unanswered decision.
func liveDecisions(tasks []task.Task) int {
	n := 0
	for _, t := range tasks {
		if t.Deferred && t.Status != task.Done && hasLiveDecision(t) {
			n++
		}
	}
	return n
}

// cmdTop prints the path of the highest-priority open task: the first
// pending, undeferred task in order-file order. Where next answers "what
// number is free", top answers "what should I pick up".
func cmdTop(args []string) error {
	fs := flag.NewFlagSet("top", flag.ContinueOnError)
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	lane := fs.String("lane", "", "only tasks in this lane")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	for _, t := range store.SortByOrder(p.Tasks, store.ReadOrder(filepath.Dir(p.Dir))) {
		if !t.HasNum || t.Status != task.Pending || t.Deferred {
			continue
		}
		if *lane != "" && t.Lane != *lane {
			continue
		}
		fmt.Println(t.Path())
		return nil
	}
	// An empty top names pending decisions: a session that finds no work
	// learns the ledger is waiting on an answer, not actually idle.
	msg := fmt.Sprintf("no pending tasks in project %s", p.Name)
	if *lane != "" {
		msg = fmt.Sprintf("%s lane %s", msg, *lane)
	}
	if n := liveDecisions(p.Tasks); n > 0 {
		msg = fmt.Sprintf("%s; %d deferred await a decision (taskman decisions)", msg, n)
	}
	return fmt.Errorf("%s", msg)
}

// cmdNext prints the next free number.
func cmdNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	fmt.Printf("%03d\n", task.NextNum(p.Tasks))
	return nil
}

// cmdDecisions lists tasks holding an unanswered decision -- the human's
// answer queue. -all sweeps every project in the store, like the web inbox.
func cmdDecisions(args []string) error {
	fs := flag.NewFlagSet("decisions", flag.ContinueOnError)
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	all := fs.Bool("all", false, "every project in the store, with a project column")
	if err := fs.Parse(args); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	if *all {
		home, err := store.Ensure()
		if err != nil {
			return err
		}
		names, err := store.Projects(home)
		if err != nil {
			return err
		}
		shown := 0
		for _, name := range names {
			tasks, err := task.Load(filepath.Join(home, name, "tasks"))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			shown += printDecisions(w, tasks, name)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if shown == 0 {
			fmt.Println("no unanswered decisions in the store")
		}
		return nil
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	shown := printDecisions(w, p.Tasks, "")
	if err := w.Flush(); err != nil {
		return err
	}
	if shown == 0 {
		fmt.Printf("no unanswered decisions in project %s\n", p.Name)
	}
	return nil
}

// printDecisions writes one row per unanswered decision in tasks, prefixed
// with the project column when project is non-empty, returning the count.
func printDecisions(w *tabwriter.Writer, tasks []task.Task, project string) int {
	shown := 0
	for _, t := range tasks {
		if !t.Deferred || !t.HasNum {
			continue
		}
		body, err := os.ReadFile(t.Path())
		if err != nil {
			continue
		}
		if d, live := task.ParseDecision(string(body)); live {
			shown++
			if project != "" {
				fmt.Fprintf(w, "%s\t%03d\t%s\t%s\n", project, t.Num, t.Slug, d.Question)
			} else {
				fmt.Fprintf(w, "%03d\t%s\t%s\n", t.Num, t.Slug, d.Question)
			}
		}
	}
	return shown
}

// cmdProjects lists the store's projects with open and deferred counts.
func cmdProjects(args []string) error {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	home, err := store.Ensure()
	if err != nil {
		return err
	}
	names, err := store.Projects(home)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Printf("no projects in %s\n", home)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	for _, name := range names {
		tasks, err := task.Load(filepath.Join(home, name, "tasks"))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		open, deferred := 0, 0
		for _, t := range tasks {
			switch {
			case t.Status == task.Done:
			case t.Deferred:
				deferred++
			default:
				open++
			}
		}
		fmt.Fprintf(w, "%s\t%d open\t%d deferred\n", name, open, deferred)
	}
	return w.Flush()
}
