package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ledger builds a tasks/ dir inside a fake repo and returns its path.
func ledger(t *testing.T, names ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "myrepo", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("# "+n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
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

// gitLedger builds a ledger inside a real git repo with an identity
// configured, returning the tasks dir.
func gitLedger(t *testing.T, names ...string) string {
	t.Helper()
	dir := ledger(t, names...)
	repo := filepath.Dir(dir)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.org"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "-q", "-m", "seed"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// git runs a git command in the repo containing dir and returns its output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// TestCommands drives the CLI surface end to end in a temp ledger.
func TestCommands(t *testing.T) {
	dir := ledger(t, "001_alpha.done.md", "002_beta.md")
	repo := filepath.Dir(dir)
	t.Chdir(repo)

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

	// File an ask into a second repo: it lands at THAT ledger's next number
	// (the immediate commit makes the claim safe), body crediting the filer.
	other := filepath.Join(t.TempDir(), "otherrepo")
	otherTasks := filepath.Join(other, "tasks")
	if err := os.MkdirAll(otherTasks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherTasks, "004_existing.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"file", other, "Please fix the flux capacitor"}); err != nil {
		t.Fatalf("file: %v", err)
	}
	ask := filepath.Join(otherTasks, "005_please-fix-the-flux-capacitor.md")
	data, err := os.ReadFile(ask)
	if err != nil {
		t.Fatalf("filed ask: %v", err)
	}
	if !strings.HasPrefix(string(data), "# 005 -- Please fix the flux capacitor\n") ||
		!strings.Contains(string(data), "Filed from myrepo") {
		t.Errorf("ask body:\n%s", data)
	}
	if err := run([]string{"file", other, "Please fix the flux capacitor"}); err == nil {
		t.Error("re-filing the same ask must refuse to overwrite")
	}
	// Legacy prefixed asks still adopt.
	legacy := filepath.Join(otherTasks, "qbd_old-style-ask.md")
	if err := os.WriteFile(legacy, []byte("# Old style ask\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(other)
	if err := run([]string{"adopt", "qbd_old-style-ask"}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(otherTasks, "006_old-style-ask.md")); err != nil {
		t.Fatalf("adopted file: %v", err)
	}
}

// TestCmdFix drives the fix command end to end: dry-run changes nothing,
// the real run repairs duplicates and leaves singles alone.
func TestCmdFix(t *testing.T) {
	dir := ledger(t,
		"001_one.md", "003_alpha.md", "003_beta.in-progress.md", "006_six.md")
	t.Chdir(filepath.Dir(dir))

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
// exactly the touched task files, never a concurrent session's staged work.
func TestAutoCommitPathspec(t *testing.T) {
	dir := gitLedger(t, "001_alpha.md")
	repo := filepath.Dir(dir)
	t.Chdir(repo)

	// A concurrent session has unrelated work staged.
	bystander := filepath.Join(repo, "other.go")
	if err := os.WriteFile(bystander, []byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "--", "other.go")

	if err := run([]string{"start", "1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := run([]string{"new", "Second thing"}); err != nil {
		t.Fatalf("new: %v", err)
	}

	log := git(t, repo, "log", "--format=%s")
	if !strings.Contains(log, "chore(tasks): start 001_alpha") ||
		!strings.Contains(log, "chore(tasks): open 002 second-thing") {
		t.Errorf("log = %q", log)
	}
	// The rename is fully committed and the bystander is still only staged.
	status := git(t, repo, "status", "--porcelain")
	if strings.Contains(status, "alpha") || strings.Contains(status, "second-thing") {
		t.Errorf("task files left uncommitted:\n%s", status)
	}
	if !strings.Contains(status, "A  other.go") {
		t.Errorf("bystander staged file was disturbed:\n%s", status)
	}

	// -no-commit leaves the change in the working tree.
	if err := run([]string{"done", "-no-commit", "2"}); err != nil {
		t.Fatalf("done: %v", err)
	}
	status = git(t, repo, "status", "--porcelain")
	if !strings.Contains(status, "second-thing") {
		t.Errorf("-no-commit still committed:\n%s", status)
	}
}

// TestFixCommits pins the fix command's single pathspec commit.
func TestFixCommits(t *testing.T) {
	dir := gitLedger(t, "001_one.md", "003_a.md", "003_b.done.md")
	repo := filepath.Dir(dir)
	t.Chdir(repo)
	if err := run([]string{"fix"}); err != nil {
		t.Fatalf("fix: %v", err)
	}
	log := git(t, repo, "log", "-1", "--format=%s")
	if !strings.Contains(log, "renumber duplicate task numbers") || !strings.Contains(log, "003->002 a") {
		t.Errorf("commit subject = %q", log)
	}
	if status := git(t, repo, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("working tree not clean after fix:\n%s", status)
	}
}
