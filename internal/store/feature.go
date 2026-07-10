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
	if slug == "" {
		return Feature{}, fmt.Errorf("description %q yields an empty slug", desc)
	}
	dir := FeaturesDir(projDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Feature{}, err
	}
	f := Feature{Dir: dir, File: slug + ".md", Slug: slug, Title: desc}
	body := fmt.Sprintf("# %s\n\nTasks:\n\nOpened %s.\n", desc, date)
	if err := task.Create(f.Path(), body); err != nil {
		if os.IsExist(err) {
			return Feature{}, fmt.Errorf("feature %q already exists", slug)
		}
		return Feature{}, err
	}
	return f, nil
}

// SetDone renames the feature to its shipped (or, for false, active) form.
func (f Feature) SetDone(done bool) (Feature, error) {
	nf := f
	nf.Done = done
	nf.File = nf.Slug + ".md"
	if done {
		nf.File = nf.Slug + ".done.md"
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
