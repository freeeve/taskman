package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Status is a task's lifecycle state, encoded in its filename suffix:
// 001_slug.md (pending), 001_slug.in-progress.md, 001_slug.done.md.
type Status int

const (
	Pending Status = iota
	InProgress
	Done
)

// String returns the display name of a status.
func (s Status) String() string {
	switch s {
	case InProgress:
		return "in-progress"
	case Done:
		return "done"
	default:
		return "pending"
	}
}

// suffix returns the filename fragment between the stem and ".md".
func (s Status) suffix() string {
	switch s {
	case InProgress:
		return ".in-progress"
	case Done:
		return ".done"
	default:
		return ""
	}
}

// Task is one tasks/ file. Numbered tasks carry Num; cross-repo asks filed by
// another repo's session carry a non-numeric Prefix instead (numbering
// authority stays with the receiving repo until adoption).
type Task struct {
	Dir    string // the tasks/ directory
	File   string // current basename
	Num    int
	HasNum bool
	Prefix string // filer prefix for unadopted cross-repo asks ("" when numbered)
	Slug   string
	Status Status
}

// Path returns the task file's full path.
func (t Task) Path() string { return filepath.Join(t.Dir, t.File) }

// Stem returns the filename without status suffix and extension.
func (t Task) Stem() string {
	if t.HasNum {
		return fmt.Sprintf("%03d_%s", t.Num, t.Slug)
	}
	return t.Prefix + "_" + t.Slug
}

// nameRE splits a task basename into stem and status: group 1 is the stem,
// group 2 the optional status tag.
var nameRE = regexp.MustCompile(`^(.+?)(?:\.(in-progress|done))?\.md$`)

// parseName decodes a tasks/ basename; ok is false for non-task files
// (README.md, dotfiles, files without a number-or-prefix separator).
func parseName(dir, name string) (Task, bool) {
	m := nameRE.FindStringSubmatch(name)
	if m == nil || strings.HasPrefix(name, ".") {
		return Task{}, false
	}
	stem := m[1]
	i := strings.Index(stem, "_")
	if i <= 0 || i == len(stem)-1 {
		return Task{}, false
	}
	t := Task{Dir: dir, File: name, Slug: stem[i+1:]}
	switch m[2] {
	case "in-progress":
		t.Status = InProgress
	case "done":
		t.Status = Done
	}
	head := stem[:i]
	if n, err := strconv.Atoi(head); err == nil {
		t.Num, t.HasNum = n, true
	} else {
		t.Prefix = head
	}
	return t, true
}

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
		if t, ok := parseName(dir, e.Name()); ok {
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
// filling gaps before extending past the maximum.
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

// Renumber moves a numbered task to num, renaming the file and restamping
// the number in its H1 title.
func (t Task) Renumber(num int) (Task, error) {
	if !t.HasNum {
		return t, fmt.Errorf("%s has no number; use adopt", t.File)
	}
	nt := t
	nt.Num = num
	nt.File = nt.Stem() + t.Status.suffix() + ".md"
	if err := renumberTitle(t.Path(), num, ""); err != nil {
		return t, err
	}
	if err := os.Rename(t.Path(), nt.Path()); err != nil {
		return t, err
	}
	return nt, nil
}

// Slugify folds a description to the ledger's kebab case: lowercase
// alphanumeric runs joined by single dashes.
func Slugify(desc string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(desc) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		default:
			dash = true
		}
	}
	return b.String()
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

// SetStatus renames the task file to the given status and returns the
// updated task.
func (t Task) SetStatus(s Status) (Task, error) {
	if t.Status == s {
		return t, fmt.Errorf("%s is already %s", t.File, s)
	}
	nt := t
	nt.Status = s
	nt.File = t.Stem() + s.suffix() + ".md"
	if err := os.Rename(t.Path(), nt.Path()); err != nil {
		return t, err
	}
	return nt, nil
}

// Adopt renumbers an unadopted cross-repo ask into the ledger at num,
// renaming the file and stamping the number into its H1 title (recording the
// filed name in a follow-up line when the body doesn't already cite it).
func (t Task) Adopt(num int) (Task, error) {
	if t.HasNum {
		return t, fmt.Errorf("%s already has a number", t.File)
	}
	nt := t
	nt.Num, nt.HasNum, nt.Prefix = num, true, ""
	nt.File = nt.Stem() + t.Status.suffix() + ".md"
	if err := renumberTitle(t.Path(), num, t.Stem()); err != nil {
		return t, err
	}
	if err := os.Rename(t.Path(), nt.Path()); err != nil {
		return t, err
	}
	return nt, nil
}

// titleNumRE matches an H1 that already leads with a task number.
var titleNumRE = regexp.MustCompile(`^# \d+ --`)

// renumberTitle stamps num into the file's H1: an existing leading number is
// replaced, an unnumbered title gains a "NNN -- " prefix. A non-empty
// filedAs not already cited in the body is recorded as an adoption
// breadcrumb after the title.
func renumberTitle(path string, num int, filedAs string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.SplitN(string(data), "\n", 2)
	if !strings.HasPrefix(lines[0], "# ") {
		return nil
	}
	if titleNumRE.MatchString(lines[0]) {
		lines[0] = titleNumRE.ReplaceAllString(lines[0], fmt.Sprintf("# %03d --", num))
	} else {
		lines[0] = fmt.Sprintf("# %03d -- %s", num, strings.TrimPrefix(lines[0], "# "))
	}
	out := lines[0]
	if filedAs != "" && !strings.Contains(string(data), filedAs) {
		out += fmt.Sprintf("\n\n(Adopted from cross-repo ask %s.md.)", filedAs)
	}
	if len(lines) > 1 {
		out += "\n" + lines[1]
	}
	return os.WriteFile(path, []byte(out), 0o644)
}
