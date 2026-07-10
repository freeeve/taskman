package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/freeeve/taskman/internal/task"
)

// orderHeader explains the file to a human opening it; ReadOrder skips it as
// a comment.
const orderHeader = "# priority order, top = next up; rewritten by taskman\n"

// OrderPath returns the project's order file path.
func OrderPath(projDir string) string { return filepath.Join(projDir, "order") }

// ReadOrder reads the project's priority list: one task number per line, top
// priority first. The file is advisory, so reading is lenient and never
// errors: a missing file means no explicit priority, and blank lines,
// comments, garbage, non-positive numbers, and repeats are skipped (first
// occurrence wins).
func ReadOrder(projDir string) []int {
	data, err := os.ReadFile(OrderPath(projDir))
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var order []int
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		order = append(order, n)
	}
	return order
}

// WriteOrder rewrites the project's order file, top priority first, and
// returns its path. Rewriting the whole file keeps a reorder to one small
// commit; concurrent writers are last-write-wins, recoverable from git
// history.
func WriteOrder(projDir string, nums []int) (string, error) {
	path := OrderPath(projDir)
	var b strings.Builder
	b.WriteString(orderHeader)
	for _, n := range nums {
		fmt.Fprintf(&b, "%03d\n", n)
	}
	return path, os.WriteFile(path, []byte(b.String()), 0o644)
}

// PruneOrder drops the given numbers from the order file, rewriting it only
// when it exists and actually changes. The returned path is empty when
// nothing was written, so callers can fold a real prune into the same
// pathspec commit as the change that caused it.
func PruneOrder(projDir string, gone map[int]bool) (string, error) {
	if _, err := os.Stat(OrderPath(projDir)); err != nil {
		return "", nil
	}
	order := ReadOrder(projDir)
	kept := make([]int, 0, len(order))
	for _, n := range order {
		if !gone[n] {
			kept = append(kept, n)
		}
	}
	if len(kept) == len(order) {
		return "", nil
	}
	return WriteOrder(projDir, kept)
}

// SortByOrder arranges tasks priority-first: tasks whose numbers appear in
// order come first in that sequence; everything else keeps ledger order
// (ascending number, asks last) after them. Unknown numbers in the order and
// unlisted tasks are both fine -- the file is advisory.
func SortByOrder(tasks []task.Task, order []int) []task.Task {
	if len(order) == 0 {
		return tasks
	}
	pos := map[int]int{}
	for i, n := range order {
		pos[n] = i
	}
	out := make([]task.Task, len(tasks))
	copy(out, tasks)
	sort.SliceStable(out, func(i, j int) bool {
		pi, iok := listed(out[i], pos)
		pj, jok := listed(out[j], pos)
		if iok != jok {
			return iok
		}
		if iok {
			return pi < pj
		}
		return false
	})
	return out
}

// listed reports a task's rank in the order map when it has one.
func listed(t task.Task, pos map[int]int) (int, bool) {
	if !t.HasNum {
		return 0, false
	}
	p, ok := pos[t.Num]
	return p, ok
}
