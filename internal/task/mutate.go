package task

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// SetStatus renames the task file to the given status and returns the updated
// task. Moving a task along its lifecycle clears any deferral: start, done and
// reopen all mean someone has acted on it, so the "held on an external
// decision" mark no longer holds.
func (t Task) SetStatus(s Status) (Task, error) {
	if t.Status == s && !t.Deferred {
		return t, fmt.Errorf("%s is already %s", t.File, s)
	}
	nt := t
	nt.Status, nt.Deferred = s, false
	nt.File = nt.Name()
	if err := os.Rename(t.Path(), nt.Path()); err != nil {
		return t, err
	}
	return nt, nil
}

// Defer marks the task held on an external decision, recording why in the body
// before the rename. A done task has no decision left to wait on, so it is
// refused rather than silently reopened.
func (t Task) Defer(reason, date string) (Task, error) {
	if t.Deferred {
		return t, fmt.Errorf("%s is already deferred", t.File)
	}
	if t.Status == Done {
		return t, fmt.Errorf("%s is done; reopen it before deferring", t.File)
	}
	nt := t
	nt.Deferred = true
	nt.File = nt.Name()
	if err := appendSection(t.Path(), "Deferred "+date, reason); err != nil {
		return t, err
	}
	if err := os.Rename(t.Path(), nt.Path()); err != nil {
		return t, err
	}
	return nt, nil
}

// Resume lifts a deferral, returning the task to the status it held when it
// was deferred (pending, for the ordinary case).
func (t Task) Resume(date string) (Task, error) {
	if !t.Deferred {
		return t, fmt.Errorf("%s is not deferred", t.File)
	}
	nt := t
	nt.Deferred = false
	nt.File = nt.Name()
	if err := appendSection(t.Path(), "Resumed "+date, ""); err != nil {
		return t, err
	}
	if err := os.Rename(t.Path(), nt.Path()); err != nil {
		return t, err
	}
	return nt, nil
}

// SetLane renames the task to carry the given lane token, or to drop it when
// lane is empty. Unadopted asks have no number to hang a lane on.
func (t Task) SetLane(lane string) (Task, error) {
	if !t.HasNum {
		return t, fmt.Errorf("%s has no number; adopt it before assigning a lane", t.File)
	}
	if t.Lane == lane {
		return t, fmt.Errorf("%s already has lane %q", t.File, lane)
	}
	nt := t
	nt.Lane = lane
	nt.File = nt.Name()
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
	nt.File = nt.Name()
	if err := renumberTitle(t.Path(), num, t.Stem()); err != nil {
		return t, err
	}
	if err := os.Rename(t.Path(), nt.Path()); err != nil {
		return t, err
	}
	return nt, nil
}

// Renumber moves a numbered task to num, renaming the file and restamping
// the number in its H1 title.
func (t Task) Renumber(num int) (Task, error) {
	if !t.HasNum {
		return t, fmt.Errorf("%s has no number; use adopt", t.File)
	}
	nt := t
	nt.Num = num
	nt.File = nt.Name()
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

// Create writes a new file, refusing to overwrite an existing task.
func Create(path, body string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(body)
	return err
}

// appendSection adds a dated H2 section to a task file, so the reason a task
// left or rejoined the working set outlives the filename that carried it.
func appendSection(path, heading, body string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := strings.TrimRight(string(data), "\n") + "\n\n## " + heading + "\n"
	if body != "" {
		out += "\n" + body + "\n"
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// titleNumRE matches an H1 that already leads with a task number, tolerating
// the separator variants found in older ledgers: --, em/en dash, or a spaced
// hyphen. A bare unspaced hyphen is deliberately NOT a separator so titles
// opening with a date ("# 2026-07 report") are treated as unnumbered.
var titleNumRE = regexp.MustCompile(`^# \d+\s*(?:--|\x{2014}|\x{2013}| - )\s*`)

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
		lines[0] = titleNumRE.ReplaceAllString(lines[0], fmt.Sprintf("# %03d -- ", num))
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
