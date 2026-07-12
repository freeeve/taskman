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
	if err := AppendSection(t.Path(), "Deferred "+date, reason); err != nil {
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
	if err := AppendSection(t.Path(), "Resumed "+date, ""); err != nil {
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
	if err := CheckLane(lane); err != nil {
		return t, err
	}
	if err := checkName(t.Num, lane, t.Slug); err != nil {
		return t, err
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

// Retitle gives the task a new description: the H1 is rewritten to the
// numbered form and the file renamed to the new slug, keeping number, lane,
// status, and deferral. A slug another task already uses is refused --
// slugs are lookup keys, and a duplicate makes both tasks unreachable by
// name -- as is an existing file at the target (os.Rename replaces
// silently).
func (t Task) Retitle(desc string) (Task, error) {
	if !t.HasNum {
		return t, fmt.Errorf("%s has no number; adopt it before retitling", t.File)
	}
	slug := Slugify(desc)
	if err := CheckSlug(slug); err != nil {
		return t, err
	}
	if err := checkName(t.Num, t.Lane, slug); err != nil {
		return t, err
	}
	siblings, err := Load(t.Dir)
	if err != nil {
		return t, err
	}
	for _, other := range siblings {
		if other.Slug != slug {
			continue
		}
		if other.HasNum && other.Num == t.Num {
			continue
		}
		return t, fmt.Errorf("slug %q is already used by %s", slug, other.File)
	}
	if err := retitleH1(t.Path(), t.Num, desc); err != nil {
		return t, err
	}
	nt := t
	nt.Slug = slug
	nt.File = nt.Name()
	if nt.File == t.File {
		return nt, nil
	}
	if _, err := os.Stat(nt.Path()); err == nil {
		return t, fmt.Errorf("%s already exists; refusing to overwrite it", nt.File)
	}
	if err := os.Rename(t.Path(), nt.Path()); err != nil {
		return t, err
	}
	return nt, nil
}

// retitleH1 replaces the file's H1 with the numbered title, inserting one
// when the body lacks it.
func retitleH1(path string, num int, desc string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("# %03d -- %s", num, desc)
	first, rest, _ := strings.Cut(string(data), "\n")
	if strings.HasPrefix(first, "# ") {
		return os.WriteFile(path, []byte(title+"\n"+rest), 0o644)
	}
	return os.WriteFile(path, []byte(title+"\n\n"+string(data)), 0o644)
}

// latinFold maps common accented Latin runes (post-lowercase) to their ASCII
// base so accented words slugify whole (resume, jose, zurich) instead of
// splitting at every accent. Deliberately a small table rather than a
// unicode-normalization dependency; non-Latin scripts stay out of slugs.
var latinFold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ą': "a", 'ă': "a",
	'ç': "c", 'ć': "c", 'č': "c",
	'ď': "d", 'đ': "d", 'ð': "d",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ę': "e", 'ě': "e",
	'ğ': "g",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'ı': "i",
	'ł': "l",
	'ñ': "n", 'ń': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ő': "o",
	'ř': "r",
	'ś': "s", 'š': "s", 'ş': "s",
	'ť': "t", 'ţ': "t", 'þ': "th",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u", 'ű': "u",
	'ý': "y", 'ÿ': "y",
	'ź': "z", 'ż': "z", 'ž': "z",
	'æ': "ae", 'œ': "oe", 'ß': "ss",
}

// Slugify folds a description to the ledger's kebab case: lowercase
// alphanumeric runs joined by single dashes, accented Latin folded to its
// base letter, apostrophes silent (joses, not jos-s).
func Slugify(desc string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(desc) {
		if r == '\'' || r == '’' {
			continue
		}
		if folded, ok := latinFold[r]; ok {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteString(folded)
			dash = false
			continue
		}
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

// MaxSlugLen bounds a slug so the full basename -- number, lane, the longest
// status suffixes, extension -- stays comfortably under the common 255-byte
// filename limit, and the failure is a clean validation error instead of a
// platform-specific ENAMETOOLONG from the filesystem.
const MaxSlugLen = 200

// CheckSlug rejects slugs that cannot become filenames: empty (nothing
// slugifiable in the description) or over MaxSlugLen.
func CheckSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("description yields an empty slug")
	}
	if len(slug) > MaxSlugLen {
		return fmt.Errorf("description too long: slug is %d bytes (max %d)", len(slug), MaxSlugLen)
	}
	return nil
}

// MaxLaneLen bounds a lane token: lanes are short routing labels, and the
// cap keeps the failure a clean validation error instead of a filesystem
// name-length error at rename time.
const MaxLaneLen = 40

// CheckLane rejects lane tokens that cannot become part of a filename. An
// empty lane is fine here -- it means "no lane"; callers that require a
// token check that separately.
func CheckLane(lane string) error {
	if len(lane) > MaxLaneLen {
		return fmt.Errorf("lane too long: %d bytes (max %d)", len(lane), MaxLaneLen)
	}
	return nil
}

// checkName rejects a create or rename whose worst-case basename -- number,
// lane, slug, both status suffixes, extension -- would exceed the common
// 255-byte filename limit. Per-field caps alone cannot guarantee this (a
// near-cap slug plus a lane can overflow the total), and every later status
// rename must also fit, hence the worst-case suffix.
func checkName(num int, lane, slug string) error {
	n := len(fmt.Sprintf("%03d", num)) + 1 + len(slug) + len(".in-progress.deferred.md")
	if lane != "" {
		n += 1 + len(lane)
	}
	if n > 255 {
		return fmt.Errorf("name too long: number, lane, and slug need %d bytes (max 255); shorten the lane or the title", n)
	}
	return nil
}

// New creates the next numbered pending task in dir with the standard body,
// returning it. The lane must already be slugified (or empty); desc keeps
// its human form in the title. A slug already in use is refused (matching
// the file command's long-standing rule): slugs are lookup keys, and a
// duplicate makes both tasks unreachable by name.
func New(dir string, tasks []Task, desc, lane, date string) (Task, error) {
	slug := Slugify(desc)
	if err := CheckSlug(slug); err != nil {
		return Task{}, err
	}
	if err := CheckLane(lane); err != nil {
		return Task{}, err
	}
	if err := checkName(NextNum(tasks), lane, slug); err != nil {
		return Task{}, err
	}
	for _, other := range tasks {
		if other.Slug == slug {
			return Task{}, fmt.Errorf("slug %q is already used by %s; pick a distinct description", slug, other.File)
		}
	}
	t := Task{Dir: dir, Num: NextNum(tasks), HasNum: true, Slug: slug, Lane: lane}
	t.File = t.Name()
	body := fmt.Sprintf("# %03d -- %s\n\nOpened %s.\n", t.Num, desc, date)
	if err := Create(t.Path(), body); err != nil {
		return Task{}, err
	}
	return t, nil
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

// AppendSection adds a dated H2 section to a task file, so the reason a task
// left or rejoined the working set outlives the filename that carried it.
func AppendSection(path, heading, body string) error {
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
