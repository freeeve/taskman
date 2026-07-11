package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/freeeve/taskman/internal/task"
)

// Feature is one features/ markdown file: the source of truth for something
// the product should do, requirements and design notes living in its body.
// slug.md is active spec; slug.done.md is shipped. A "Tasks:" line links the
// task numbers implementing it.
type Feature struct {
	Dir   string // the features/ directory
	File  string // current basename
	Slug  string
	Done  bool
	Title string
	Tasks []int
}

// Path returns the feature file's full path.
func (f Feature) Path() string { return filepath.Join(f.Dir, f.File) }

// FeaturesDir returns the project's features/ directory.
func FeaturesDir(projDir string) string { return filepath.Join(projDir, "features") }

// LoadFeatures reads every feature in the project, in filename order (active
// and shipped alike; callers filter). Every .md file in features/ is a
// feature -- longer requirements live in the feature body, not in sibling
// documents.
func LoadFeatures(projDir string) ([]Feature, error) {
	dir := FeaturesDir(projDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var features []Feature
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
			continue
		}
		f := Feature{Dir: dir, File: name, Slug: strings.TrimSuffix(name, ".md")}
		if s, ok := strings.CutSuffix(f.Slug, ".done"); ok {
			f.Slug, f.Done = s, true
		}
		f.Title, f.Tasks = parseFeatureBody(f.Path())
		if f.Title == "" {
			f.Title = f.Slug
		}
		features = append(features, f)
	}
	return features, nil
}

// NewFeature creates a feature spec from the standard template and returns
// it; the description keeps its human form in the title.
func NewFeature(projDir, desc, date string) (Feature, error) {
	slug := task.Slugify(desc)
	if err := task.CheckSlug(slug); err != nil {
		return Feature{}, err
	}
	dir := FeaturesDir(projDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Feature{}, err
	}
	f := Feature{Dir: dir, File: slug + ".md", Slug: slug, Title: desc}
	// A shipped feature owns its slug too: without this guard a re-created
	// feature would later SetDone onto slug.done.md and destroy the
	// original's spec (os.Rename replaces silently).
	if _, err := os.Stat(filepath.Join(dir, slug+".done.md")); err == nil {
		return Feature{}, fmt.Errorf("feature %q already exists (shipped)", slug)
	}
	body := fmt.Sprintf("# %s\n\nTasks:\n\nOpened %s.\n", desc, date)
	if err := task.Create(f.Path(), body); err != nil {
		if os.IsExist(err) {
			return Feature{}, fmt.Errorf("feature %q already exists", slug)
		}
		return Feature{}, err
	}
	return f, nil
}

// SetTasks rewrites the feature's Tasks: line with nums -- deduped, positive
// only, in the given order -- inserting the line after the H1 title when the
// body has none yet. The write is a full-file rewrite of just that line, so
// the rest of the spec is untouched.
func (f Feature) SetTasks(nums []int) (Feature, error) {
	clean := make([]int, 0, len(nums))
	seen := map[int]bool{}
	for _, n := range nums {
		if n > 0 && !seen[n] {
			seen[n] = true
			clean = append(clean, n)
		}
	}
	parts := make([]string, len(clean))
	for i, n := range clean {
		parts[i] = fmt.Sprintf("%03d", n)
	}
	line := "Tasks: " + strings.Join(parts, ", ")
	data, err := os.ReadFile(f.Path())
	if err != nil {
		return f, err
	}
	lines := strings.Split(string(data), "\n")
	replaced := false
	for i, l := range lines {
		if strings.HasPrefix(l, "Tasks:") {
			lines[i] = line
			replaced = true
			break
		}
	}
	if !replaced {
		out := make([]string, 0, len(lines)+2)
		inserted := false
		for _, l := range lines {
			out = append(out, l)
			if !inserted && strings.HasPrefix(l, "# ") {
				out = append(out, "", line)
				inserted = true
			}
		}
		if !inserted {
			out = append(out, line)
		}
		lines = out
	}
	if err := os.WriteFile(f.Path(), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return f, err
	}
	nf := f
	nf.Tasks = clean
	return nf, nil
}

// Remove deletes the feature's spec file. Linked tasks are independent
// files and stay untouched; the removal is one commit, so project undo
// restores a mistaken discard.
func (f Feature) Remove() error { return os.Remove(f.Path()) }

// SetDone renames the feature to its shipped (or, for false, active) form.
// It refuses to rename onto an existing file: os.Rename replaces its
// destination silently, and the destination here is another feature's spec.
func (f Feature) SetDone(done bool) (Feature, error) {
	nf := f
	nf.Done = done
	nf.File = nf.Slug + ".md"
	if done {
		nf.File = nf.Slug + ".done.md"
	}
	if _, err := os.Stat(nf.Path()); err == nil {
		return f, fmt.Errorf("%s already exists; refusing to overwrite it", nf.File)
	}
	if err := os.Rename(f.Path(), nf.Path()); err != nil {
		return f, err
	}
	return nf, nil
}

// parseFeatureBody extracts the H1 title and the first "Tasks:" line's
// numbers. Parsing is lenient -- the body is a document for humans first, so
// anything unparseable simply contributes nothing.
func parseFeatureBody(path string) (string, []int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	title := ""
	var tasks []int
	tasksSeen := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if title == "" && strings.HasPrefix(line, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
		if !tasksSeen && strings.HasPrefix(line, "Tasks:") {
			tasksSeen = true
			tasks = parseTaskNums(strings.TrimPrefix(line, "Tasks:"))
		}
	}
	return title, tasks
}

// parseTaskNums pulls task numbers out of a free-form list like
// "012, 019 034": digit runs are numbers, everything else is separator.
func parseTaskNums(s string) []int {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	var nums []int
	seen := map[int]bool{}
	for _, f := range fields {
		if n, err := strconv.Atoi(f); err == nil && n > 0 && !seen[n] {
			seen[n] = true
			nums = append(nums, n)
		}
	}
	return nums
}
