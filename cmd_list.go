package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/freeeve/taskman/internal/task"
)

// tasksHere locates the ledger for the current directory.
func tasksHere() (string, []task.Task, error) {
	dir, err := task.FindTasksDir(".")
	if err != nil {
		return "", nil, err
	}
	tasks, err := task.Load(dir)
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
	dups := task.Dups(tasks)
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	shown, deferred := 0, 0
	for _, t := range tasks {
		if t.Deferred && t.Status != task.Done {
			deferred++
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
	fmt.Printf("%03d\n", task.NextNum(tasks))
	return nil
}
