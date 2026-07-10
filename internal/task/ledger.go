package task

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Load reads every task file in dir, sorted numbered-first by number then
// slug, unadopted asks last by prefix.
func Load(dir string) ([]Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var tasks []Task
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if t, ok := Parse(dir, e.Name()); ok {
			tasks = append(tasks, t)
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if a.HasNum != b.HasNum {
			return a.HasNum
		}
		if a.HasNum && a.Num != b.Num {
			return a.Num < b.Num
		}
		if a.Prefix != b.Prefix {
			return a.Prefix < b.Prefix
		}
		return a.Slug < b.Slug
	})
	return tasks, nil
}

// FindTasksDir walks upward from start looking for a tasks/ directory,
// mirroring how git finds its root.
func FindTasksDir(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		cand := filepath.Join(dir, "tasks")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no tasks/ directory found from %s upward", start)
		}
		dir = parent
	}
}

// NextNum returns the next free task number: one past the highest in use, so
// historical collisions (duplicate numbers) never repeat.
func NextNum(tasks []Task) int {
	max := 0
	for _, t := range tasks {
		if t.HasNum && t.Num > max {
			max = t.Num
		}
	}
	return max + 1
}

// Dups returns the numbers claimed by more than one task.
func Dups(tasks []Task) map[int]bool {
	count := map[int]int{}
	for _, t := range tasks {
		if t.HasNum {
			count[t.Num]++
		}
	}
	dup := map[int]bool{}
	for n, c := range count {
		if c > 1 {
			dup[n] = true
		}
	}
	return dup
}

// Gaps returns the unused numbers below the highest in use, ascending.
func Gaps(tasks []Task) []int {
	used := map[int]bool{}
	max := 0
	for _, t := range tasks {
		if t.HasNum {
			used[t.Num] = true
			if t.Num > max {
				max = t.Num
			}
		}
	}
	var gaps []int
	for n := 1; n < max; n++ {
		if !used[n] {
			gaps = append(gaps, n)
		}
	}
	return gaps
}

// Repair is one planned renumbering: a duplicate-numbered task and the free
// number it moves to.
type Repair struct {
	T   Task
	Num int
}

// PlanRepairs resolves duplicate numbers deterministically: per duplicated
// number the most advanced task keeps it (done > in-progress > pending,
// ledger order breaking ties -- the furthest-along task is the one history
// most likely references), and each loser takes the lowest free number,
// filling gaps before extending past the maximum. Deferral plays no part: it
// is not a position on the progress axis, so a deferred task contests a number
// exactly as the pending or in-progress task it still is.
func PlanRepairs(tasks []Task) []Repair {
	used := map[int]bool{}
	byNum := map[int][]Task{}
	for _, t := range tasks {
		if t.HasNum {
			used[t.Num] = true
			byNum[t.Num] = append(byNum[t.Num], t)
		}
	}
	nums := make([]int, 0, len(byNum))
	for n, group := range byNum {
		if len(group) > 1 {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	free := 1
	var plan []Repair
	for _, n := range nums {
		group := byNum[n]
		keep := 0
		for i, t := range group {
			if t.Status > group[keep].Status {
				keep = i
			}
		}
		for i, t := range group {
			if i == keep {
				continue
			}
			for used[free] {
				free++
			}
			used[free] = true
			plan = append(plan, Repair{T: t, Num: free})
		}
	}
	return plan
}

// Find resolves a task by number or unique slug/stem fragment among tasks.
// A duplicate number or an ambiguous fragment is an error listing the
// candidates, so status renames never guess.
func Find(tasks []Task, key string) (Task, error) {
	if n, err := strconv.Atoi(key); err == nil {
		var hits []Task
		for _, t := range tasks {
			if t.HasNum && t.Num == n {
				hits = append(hits, t)
			}
		}
		return one(hits, key)
	}
	var hits []Task
	for _, t := range tasks {
		if strings.Contains(t.Stem(), key) {
			hits = append(hits, t)
		}
	}
	return one(hits, key)
}

// one reduces candidate matches to exactly one or a descriptive error.
func one(hits []Task, key string) (Task, error) {
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Task{}, fmt.Errorf("no task matches %q", key)
	default:
		names := make([]string, len(hits))
		for i, t := range hits {
			names[i] = t.File
		}
		return Task{}, fmt.Errorf("%q is ambiguous: %s", key, strings.Join(names, ", "))
	}
}
