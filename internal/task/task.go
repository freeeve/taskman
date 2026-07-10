// Package task models the taskman ledger: one numbered markdown file per
// task, status carried by the filename (001_slug.md -> .in-progress.md ->
// .done.md), deferral carried by an orthogonal .deferred marker on top of
// that status, cross-repo asks filed with a filer prefix (qbd_slug.md) and
// renumbered on adoption.
package task

import (
	"fmt"
	"path/filepath"
	"regexp"
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
//
// Deferred is deliberately a flag rather than a fourth Status: deferral says
// "not being worked, and that is a decision", which is orthogonal to how far
// along the work is. Keeping it off the Status axis means PlanRepairs never
// has to answer the meaningless question of whether deferred outranks pending.
// Lane is a free-form routing token between the number and the slug
// (012-impl_fix-thing.md): which session, submodule, or workstream the task
// belongs to. It lives in the filename so it survives every status rename
// without any extra bookkeeping.
type Task struct {
	Dir      string // the tasks/ directory
	File     string // current basename
	Num      int
	HasNum   bool
	Prefix   string // filer prefix for unadopted cross-repo asks ("" when numbered)
	Lane     string // optional routing token ("" when unrouted)
	Slug     string
	Status   Status
	Deferred bool
}

// Path returns the task file's full path.
func (t Task) Path() string { return filepath.Join(t.Dir, t.File) }

// Stem returns the filename without status suffix and extension. The lane
// rides inside the stem, which is why it survives every status rename,
// deferral, and renumbering for free.
func (t Task) Stem() string {
	switch {
	case t.HasNum && t.Lane != "":
		return fmt.Sprintf("%03d-%s_%s", t.Num, t.Lane, t.Slug)
	case t.HasNum:
		return fmt.Sprintf("%03d_%s", t.Num, t.Slug)
	default:
		return t.Prefix + "_" + t.Slug
	}
}

// Name returns the basename the task's current state encodes: stem, status
// suffix, deferral marker, extension.
func (t Task) Name() string {
	name := t.Stem() + t.Status.suffix()
	if t.Deferred {
		name += ".deferred"
	}
	return name + ".md"
}

// StatusLabel renders the task's state for display, folding the orthogonal
// deferral flag into the status column.
func (t Task) StatusLabel() string {
	switch {
	case t.Deferred && t.Status == Pending:
		return "deferred"
	case t.Deferred:
		return t.Status.String() + "/deferred"
	default:
		return t.Status.String()
	}
}

// nameRE splits a task basename into stem, status and deferral marker: group 1
// is the stem, group 2 the optional status tag, group 3 the optional
// ".deferred" marker (which follows the status, since it modifies it).
var nameRE = regexp.MustCompile(`^(.+?)(?:\.(in-progress|done))?(\.deferred)?\.md$`)

// headLaneRE splits a numbered head with a lane token: the number is the
// maximal leading digit run, everything after the first "-" is the lane
// (which may itself contain "-"). A head with no leading digits is a legacy
// filer prefix, so "qbd-impl" stays a prefix rather than gaining a lane.
var headLaneRE = regexp.MustCompile(`^(\d+)-(.+)$`)

// Parse decodes a tasks/ basename; ok is false for non-task files
// (README.md, dotfiles, files without a number-or-prefix separator).
//
// Grammar: stem[.in-progress|.done][.deferred].md, where stem is
// NUM[-lane]_slug for numbered tasks (012-impl_fix-thing.md) or prefix_slug
// for unadopted cross-repo asks.
func Parse(dir, name string) (Task, bool) {
	m := nameRE.FindStringSubmatch(name)
	if m == nil || strings.HasPrefix(name, ".") {
		return Task{}, false
	}
	stem := m[1]
	i := strings.Index(stem, "_")
	if i <= 0 || i == len(stem)-1 {
		return Task{}, false
	}
	t := Task{Dir: dir, File: name, Slug: stem[i+1:], Deferred: m[3] != ""}
	switch m[2] {
	case "in-progress":
		t.Status = InProgress
	case "done":
		t.Status = Done
	}
	head := stem[:i]
	if n, err := strconv.Atoi(head); err == nil {
		t.Num, t.HasNum = n, true
	} else if m := headLaneRE.FindStringSubmatch(head); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return Task{}, false
		}
		t.Num, t.HasNum, t.Lane = n, true, m[2]
	} else {
		t.Prefix = head
	}
	return t, true
}
