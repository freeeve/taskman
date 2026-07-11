package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freeeve/taskman/internal/task"
)

// seedReadme is written into a freshly initialized store so a human finding
// the directory knows what owns it.
const seedReadme = `# taskman store

Central task ledgers managed by taskman (github.com/freeeve/taskman).
One directory per project, each holding tasks/, features/, screenshots/,
and an order file. This directory is a git repository: every taskman
mutation is a pathspec-scoped commit, so history is the audit trail.
`

// Home returns the store root: $TASKMAN_HOME, defaulting to ~/.taskman.
func Home() (string, error) {
	if h := os.Getenv("TASKMAN_HOME"); h != "" {
		return h, nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".taskman"), nil
}

// Ensure creates the store root on first use and initializes its git repo,
// seeding a README so the directory is self-describing. The seed commit is
// best-effort: a missing git identity degrades to a warning, matching
// AutoCommit's contract that ledger operations never fail on git problems.
func Ensure() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(home, ".git")); os.IsNotExist(err) {
		if out, err := exec.Command("git", "-C", home, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
			return "", fmt.Errorf("git init %s: %v: %s", home, err, strings.TrimSpace(string(out)))
		}
	}
	readme := filepath.Join(home, "README.md")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		if err := os.WriteFile(readme, []byte(seedReadme), 0o644); err != nil {
			return "", err
		}
		if err := Commit(home, "chore(store): initialize taskman store", []string{readme}); err != nil {
			fmt.Fprintf(os.Stderr, "taskman: store seed not committed (%v)\n", err)
		}
	}
	return home, nil
}

// EnsureProject creates the project's directory skeleton on first use and
// returns the project directory.
func EnsureProject(home, project string) (string, error) {
	dir := filepath.Join(home, project)
	for _, d := range []string{"tasks", "features", "screenshots"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// Resolve picks the project name, most explicit source first: the -p flag,
// the TASKMAN_PROJECT environment variable (how a session pins itself to a
// project), the enclosing git repo's basename, and finally the cwd basename.
// The result is slugified so directory names and project names never drift.
//
// A path is refused rather than slugified: the pre-store `taskman file
// <repo-dir>` habit dies hard, and silently mangling ~/libcat into a junk
// "users-efreeman-libcat" project has misfiled real asks more than once.
func Resolve(flagVal string) (string, error) {
	name := flagVal
	if name == "" {
		name = os.Getenv("TASKMAN_PROJECT")
	}
	if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, "~") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("project %q looks like a path; pass a bare project name (e.g. %q)",
			name, filepath.Base(strings.TrimRight(name, `/\`)))
	}
	if name == "" {
		if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
			name = filepath.Base(strings.TrimSpace(string(out)))
		}
	}
	if name == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		name = filepath.Base(wd)
	}
	slug := task.Slugify(name)
	if slug == "" {
		return "", fmt.Errorf("cannot resolve a project name from %q; pass -p or set TASKMAN_PROJECT", name)
	}
	return slug, nil
}

// Projects lists the store's project names: every non-dot directory in the
// store root, sorted. Dot directories and plain files are reserved for
// taskman itself.
func Projects(home string) ([]string, error) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
