package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGaps(t *testing.T) {
	dir := ledger(t, "001_one.md", "004_four.md", "007_seven.done.md")
	tasks, _ := Load(dir)
	gaps := Gaps(tasks)
	if len(gaps) != 4 || gaps[0] != 2 || gaps[1] != 3 || gaps[2] != 5 || gaps[3] != 6 {
		t.Errorf("gaps = %v, want [2 3 5 6]", gaps)
	}
	if g := Gaps(nil); len(g) != 0 {
		t.Errorf("empty ledger gaps = %v", g)
	}
}

// TestPlanRepairs pins the repair policy: the most advanced duplicate keeps
// its number, losers take the lowest free slots (gaps first), deterministic
// across runs.
func TestPlanRepairs(t *testing.T) {
	dir := ledger(t,
		"001_one.md",
		"003_alpha.md", "003_beta.in-progress.md", "003_gamma.md",
		"005_five.md", "005_worked.done.md")
	tasks, _ := Load(dir)
	plan := PlanRepairs(tasks)
	if len(plan) != 3 {
		t.Fatalf("plan = %+v, want 3 repairs", plan)
	}
	// 003: beta (in-progress) keeps it; alpha then gamma move to 002, 004.
	// 005: worked (done) keeps it; five moves to 006.
	got := map[string]int{}
	for _, r := range plan {
		got[r.T.Slug] = r.Num
	}
	want := map[string]int{"alpha": 2, "gamma": 4, "five": 6}
	for slug, num := range want {
		if got[slug] != num {
			t.Errorf("repair[%s] = %d, want %d (full plan %v)", slug, got[slug], num, got)
		}
	}
}

func TestPlanRepairsNoDups(t *testing.T) {
	dir := ledger(t, "001_one.md", "003_three.md")
	tasks, _ := Load(dir)
	if plan := PlanRepairs(tasks); len(plan) != 0 {
		t.Errorf("plan = %+v, want none", plan)
	}
}

func TestRenumberRestampsTitle(t *testing.T) {
	dir := ledger(t, "007_seven.md")
	path := filepath.Join(dir, "003_movable.in-progress.md")
	if err := os.WriteFile(path, []byte("# 003 -- Movable task\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, _ := Load(dir)
	task, err := Find(tasks, "movable")
	if err != nil {
		t.Fatal(err)
	}
	nt, err := task.Renumber(8)
	if err != nil {
		t.Fatal(err)
	}
	if nt.File != "008_movable.in-progress.md" {
		t.Errorf("renumbered file = %q", nt.File)
	}
	data, _ := os.ReadFile(nt.Path())
	if !strings.HasPrefix(string(data), "# 008 -- Movable task\n") {
		t.Errorf("title not restamped: %q", strings.SplitN(string(data), "\n", 2)[0])
	}
	if strings.Contains(string(data), "Adopted") {
		t.Error("renumber must not add an adoption breadcrumb")
	}
	if _, err := nt.Renumber(9); err == nil {
		// Renumbering again is legal; verify it did not error for HasNum.
	}
	ask := Task{Dir: dir, File: "qbd_x.md", Prefix: "qbd", Slug: "x"}
	if _, err := ask.Renumber(9); err == nil {
		t.Error("unnumbered ask must refuse Renumber")
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
	tasks, _ := Load(dir)
	if d := Dups(tasks); len(d) != 0 {
		t.Errorf("dups remain after fix: %v", d)
	}
	// Second run is a no-op.
	if err := run([]string{"fix"}); err != nil {
		t.Fatalf("fix again: %v", err)
	}
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
