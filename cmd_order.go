package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

// cmdMove repositions a task in the project's priority order (the order file
// that list and top follow) and commits the rewrite. Grammar mirrors the
// lane command's positional style:
//
//	taskman move <n|slug> top|bottom
//	taskman move <n|slug> above|below <n|slug>
//
// "above" is higher priority (nearer the top), "below" lower; before/after
// are accepted as synonyms. Editing the order file by hand is the thing this
// removes -- the CLI resolves numbers/slugs, materializes a full sequence so
// bottom and relative moves are well-defined, writes under the store lock,
// and commits.
func cmdMove(args []string) error {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	usage := fmt.Errorf("usage: taskman move [-p project] [-no-commit] <n|slug> (top | bottom | above <n|slug> | below <n|slug>)")
	if fs.NArg() < 2 {
		return usage
	}
	var pos store.Position
	needRef := false
	switch strings.ToLower(fs.Arg(1)) {
	case "top":
		pos = store.ToTop
	case "bottom":
		pos = store.ToBottom
	case "above", "before":
		pos, needRef = store.Above, true
	case "below", "after":
		pos, needRef = store.Below, true
	default:
		return fmt.Errorf("position must be top, bottom, above <n>, or below <n>, not %q", fs.Arg(1))
	}
	if needRef && fs.NArg() != 3 {
		return fmt.Errorf("%q needs a reference task: taskman move <n> %s <n>", fs.Arg(1), fs.Arg(1))
	}
	if !needRef && fs.NArg() != 2 {
		return usage
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := orderable(t); err != nil {
		return err
	}
	ref, dest := 0, strings.ToLower(fs.Arg(1))
	if needRef {
		rt, err := task.Find(p.Tasks, fs.Arg(2))
		if err != nil {
			return err
		}
		if err := orderable(rt); err != nil {
			return err
		}
		ref = rt.Num
		dest = fmt.Sprintf("%s %03d", dest, ref)
	}
	path, err := store.Reorder(filepath.Dir(p.Dir), openNums(p.Tasks), t.Num, pos, ref)
	if err != nil {
		return err
	}
	if path == "" {
		fmt.Printf("task %03d is already %s\n", t.Num, dest)
		return nil
	}
	fmt.Printf("moved %03d to %s\n", t.Num, dest)
	p.commit(*noCommit, fmt.Sprintf("reorder %03d to %s", t.Num, dest), path)
	return nil
}

// orderable rejects tasks that cannot carry a priority: unadopted asks (no
// number) and done tasks (pruned from the order the moment they finish).
func orderable(t task.Task) error {
	if !t.HasNum {
		return fmt.Errorf("%s has no number; adopt it before prioritizing", t.Ref())
	}
	if t.Status == task.Done {
		return fmt.Errorf("task %03d is done; reopen it before prioritizing", t.Num)
	}
	return nil
}

// openNums returns the sorted numbers of the project's orderable tasks
// (numbered, not done), the candidate set Reorder materializes over.
func openNums(tasks []task.Task) []int {
	nums := make([]int, 0, len(tasks))
	for _, t := range tasks {
		if t.HasNum && t.Status != task.Done {
			nums = append(nums, t.Num)
		}
	}
	sort.Ints(nums)
	return nums
}
