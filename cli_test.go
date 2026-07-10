package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// storeLedger pins the CLI to a fresh temp store and project via the
// environment, seeding the project's tasks/ with the given files. The store
// git repo itself is initialized lazily by the first command.
func storeLedger(t *testing.T, project string, names ...string) (home, dir string) {
	t.Helper()
	home = filepath.Join(t.TempDir(), "store")
	t.Setenv("TASKMAN_HOME", home)
	t.Setenv("TASKMAN_PROJECT", project)
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.org")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.org")
	dir = filepath.Join(home, project, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("# "+n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, dir
}

// capture runs fn with os.Stdout redirected and returns what it printed.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = saved
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// git runs a git command in dir and returns its output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// TestCommands drives the CLI surface end to end against a temp store.
func TestCommands(t *testing.T) {
	home, dir := storeLedger(t, "myproj", "001_alpha.done.md", "002_beta.md")

	if err := run([]string{"new", "Try the CLI"}); err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "003_try-the-cli.md")); err != nil {
		t.Fatalf("new file: %v", err)
	}
	if err := run([]string{"start", "3"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := run([]string{"done", "3"}); err != nil {
		t.Fatalf("done: %v", err)
	}
	if err := run([]string{"reopen", "3"}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := run([]string{"next"}); err != nil {
		t.Fatalf("next: %v", err)
	}
	if err := run([]string{"list", "-all"}); err != nil {
		t.Fatalf("list: %v", err)
	}

	// Deferral needs a reason, hides the task from the default list, and
	// survives a round trip through resume.
	if err := run([]string{"defer", "-no-commit", "3"}); err == nil {
		t.Error("defer without -reason must error")
	}
	if err := run([]string{"defer", "-no-commit", "-reason", "waiting on the maintainer", "3"}); err != nil {
		t.Fatalf("defer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "003_try-the-cli.deferred.md")); err != nil {
		t.Fatalf("deferred file: %v", err)
	}
	if out := capture(t, func() { _ = run([]string{"list"}) }); strings.Contains(out, "try-the-cli") {
		t.Errorf("deferred task must not appear in the default list:\n%s", out)
	} else if !strings.Contains(out, "1 deferred") {
		t.Errorf("hidden deferrals must still be counted:\n%s", out)
	}
	if out := capture(t, func() { _ = run([]string{"list", "-all"}) }); !strings.Contains(out, "deferred") ||
		!strings.Contains(out, "try-the-cli") {
		t.Errorf("list -all must show the deferred task, marked:\n%s", out)
	}
	if err := run([]string{"resume", "-no-commit", "3"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "003_try-the-cli.md")); err != nil {
		t.Fatalf("resumed file: %v", err)
	}

	if err := run([]string{"bogus"}); err == nil {
		t.Error("bogus command must error")
	}

	// File an ask into another project: it lands at THAT ledger's next
	// number in the same store, body crediting the filing project.
	if err := run([]string{"file", "otherproj", "Please fix the flux capacitor"}); err != nil {
		t.Fatalf("file: %v", err)
	}
	ask := filepath.Join(home, "otherproj", "tasks", "001_please-fix-the-flux-capacitor.md")
	data, err := os.ReadFile(ask)
	if err != nil {
		t.Fatalf("filed ask: %v", err)
	}
	if !strings.HasPrefix(string(data), "# 001 -- Please fix the flux capacitor\n") ||
		!strings.Contains(string(data), "Filed from myproj") {
		t.Errorf("ask body:\n%s", data)
	}
	if err := run([]string{"file", "otherproj", "Please fix the flux capacitor"}); err == nil {
		t.Error("re-filing the same ask must refuse to overwrite")
	}

	// Legacy prefixed asks still adopt, addressed cross-project via -p.
	legacy := filepath.Join(home, "otherproj", "tasks", "qbd_old-style-ask.md")
	if err := os.WriteFile(legacy, []byte("# Old style ask\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"adopt", "-p", "otherproj", "qbd_old-style-ask"}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "otherproj", "tasks", "002_old-style-ask.md")); err != nil {
		t.Fatalf("adopted file: %v", err)
	}

	// The projects command sees both ledgers.
	out := capture(t, func() { _ = run([]string{"projects"}) })
	if !strings.Contains(out, "myproj") || !strings.Contains(out, "otherproj") {
		t.Errorf("projects output:\n%s", out)
	}
}

// TestProjectFlagOverridesEnv pins resolution: -p beats the session's pinned
// TASKMAN_PROJECT.
func TestProjectFlagOverridesEnv(t *testing.T) {
	home, _ := storeLedger(t, "envproj")
	if err := run([]string{"new", "-p", "flagproj", "Land here"}); err != nil {
		t.Fatalf("new -p: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "flagproj", "tasks", "001_land-here.md")); err != nil {
		t.Errorf("task landed in the wrong project: %v", err)
	}
}

// TestCmdFix drives the fix command end to end: dry-run changes nothing,
// the real run repairs duplicates and leaves singles alone.
func TestCmdFix(t *testing.T) {
	_, dir := storeLedger(t, "fixproj",
		"001_one.md", "003_alpha.md", "003_beta.in-progress.md", "006_six.md")

	if err := run([]string{"fix", "-n"}); err != nil {
		t.Fatalf("fix -n: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "003_alpha.md")); err != nil {
		t.Fatal("dry run must not rename")
	}
	if err := run([]string{"fix", "-no-commit"}); err != nil {
		t.Fatalf("fix: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "002_alpha.md")); err != nil {
		t.Error("alpha must move to the 002 gap")
	}
	if _, err := os.Stat(filepath.Join(dir, "003_beta.in-progress.md")); err != nil {
		t.Error("in-progress beta must keep 003")
	}
	// Second run is a no-op.
	if err := run([]string{"fix"}); err != nil {
		t.Fatalf("fix again: %v", err)
	}
}

// TestAutoCommitPathspec pins the git integration: mutating commands commit
// exactly the touched task files in the store, never another project's (or
// session's) staged work in the same repo.
func TestAutoCommitPathspec(t *testing.T) {
	home, _ := storeLedger(t, "alpha", "001_first.md")

	// Initialize the store repo (first command runs Ensure).
	if err := run([]string{"list"}); err != nil {
		t.Fatalf("list: %v", err)
	}

	// A concurrent session has another project's work staged.
	bystander := filepath.Join(home, "beta", "tasks", "001_bee.md")
	if err := os.MkdirAll(filepath.Dir(bystander), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bystander, []byte("# bee\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, home, "add", "--", bystander)

	if err := run([]string{"start", "1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := run([]string{"new", "Second thing"}); err != nil {
		t.Fatalf("new: %v", err)
	}

	log := git(t, home, "log", "--format=%s")
	if !strings.Contains(log, "chore(alpha): start 001_first") ||
		!strings.Contains(log, "chore(alpha): open 002 second-thing") {
		t.Errorf("log = %q", log)
	}
	// The rename is fully committed and the bystander is still only staged.
	status := git(t, home, "status", "--porcelain")
	if strings.Contains(status, "first") || strings.Contains(status, "second-thing") {
		t.Errorf("task files left uncommitted:\n%s", status)
	}
	if !strings.Contains(status, "A  beta/tasks/001_bee.md") {
		t.Errorf("bystander staged file was disturbed:\n%s", status)
	}

	// -no-commit leaves the change in the working tree.
	if err := run([]string{"done", "-no-commit", "2"}); err != nil {
		t.Fatalf("done: %v", err)
	}
	status = git(t, home, "status", "--porcelain")
	if !strings.Contains(status, "second-thing") {
		t.Errorf("-no-commit still committed:\n%s", status)
	}
}

// TestFixCommits pins the fix command's single pathspec commit.
func TestFixCommits(t *testing.T) {
	home, _ := storeLedger(t, "fixproj", "001_one.md", "003_a.md", "003_b.done.md")
	if err := run([]string{"fix"}); err != nil {
		t.Fatalf("fix: %v", err)
	}
	log := git(t, home, "log", "-1", "--format=%s")
	if !strings.Contains(log, "chore(fixproj): renumber duplicate task numbers") ||
		!strings.Contains(log, "003->002 a") {
		t.Errorf("commit subject = %q", log)
	}
	// The repaired file is fully committed (seeded bystander files stay
	// untracked -- fix must touch only what it renamed).
	if status := git(t, home, "status", "--porcelain"); strings.Contains(status, "_a") {
		t.Errorf("repair left uncommitted:\n%s", status)
	}
}
