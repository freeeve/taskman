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

// TestOrderAndTop drives priority through the CLI: list follows the order
// file, top prints the first pending task, and done prunes its number from
// the order inside the same commit.
func TestOrderAndTop(t *testing.T) {
	home, _ := storeLedger(t, "orderproj",
		"001_low.md", "002_started.in-progress.md", "003_urgent.md", "004-e2e_laned.md")
	if err := os.WriteFile(filepath.Join(home, "orderproj", "order"),
		[]byte("# header\n003\n004\n001\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := capture(t, func() { _ = run([]string{"list"}) })
	if u, l := strings.Index(out, "urgent"), strings.Index(out, "low"); u < 0 || l < 0 || u > l {
		t.Errorf("list must follow order file (urgent before low):\n%s", out)
	}

	out = capture(t, func() { _ = run([]string{"top"}) })
	if !strings.Contains(out, "003_urgent.md") {
		t.Errorf("top must print the first pending task:\n%s", out)
	}
	out = capture(t, func() { _ = run([]string{"top", "-lane", "e2e"}) })
	if !strings.Contains(out, "004-e2e_laned.md") {
		t.Errorf("top -lane must respect the lane:\n%s", out)
	}

	// done prunes the finished number from the order in the same commit.
	if err := run([]string{"done", "3"}); err != nil {
		t.Fatalf("done: %v", err)
	}
	order, err := os.ReadFile(filepath.Join(home, "orderproj", "order"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(order), "003") || !strings.Contains(string(order), "001") {
		t.Errorf("order after done:\n%s", order)
	}
	if status := git(t, home, "status", "--porcelain"); strings.Contains(status, "orderproj/order") {
		t.Errorf("order prune not folded into the done commit:\n%s", status)
	}

	// With 003 done, top moves on to the next listed number.
	out = capture(t, func() { _ = run([]string{"top"}) })
	if !strings.Contains(out, "004-e2e_laned.md") {
		t.Errorf("top after done:\n%s", out)
	}

	// An empty lane errors rather than printing nothing.
	if err := run([]string{"top", "-lane", "nope"}); err == nil {
		t.Error("top with no matching tasks must error")
	}
}

// TestLanes drives lane routing through the CLI: new -lane, the list filter,
// and the lane command's set/clear renames.
func TestLanes(t *testing.T) {
	_, dir := storeLedger(t, "laneproj", "001_shared.md")

	if err := run([]string{"new", "-lane", "impl", "Wire the API"}); err != nil {
		t.Fatalf("new -lane: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "002-impl_wire-the-api.md")); err != nil {
		t.Fatalf("laned file: %v", err)
	}
	if err := run([]string{"new", "-lane", "e2e", "Cover checkout"}); err != nil {
		t.Fatalf("new -lane e2e: %v", err)
	}

	out := capture(t, func() { _ = run([]string{"list", "-lane", "impl"}) })
	if !strings.Contains(out, "wire-the-api") || strings.Contains(out, "cover-checkout") ||
		strings.Contains(out, "shared") {
		t.Errorf("list -lane impl:\n%s", out)
	}

	// start keeps the lane; lane set/clear renames.
	if err := run([]string{"start", "2"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "002-impl_wire-the-api.in-progress.md")); err != nil {
		t.Fatalf("lane lost on start: %v", err)
	}
	if err := run([]string{"lane", "1", "e2e"}); err != nil {
		t.Fatalf("lane set: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "001-e2e_shared.md")); err != nil {
		t.Fatalf("lane rename: %v", err)
	}
	if err := run([]string{"lane", "1", "-"}); err != nil {
		t.Fatalf("lane clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "001_shared.md")); err != nil {
		t.Fatalf("lane clear rename: %v", err)
	}
}

// TestFeatures drives the feature CLI: template creation, the rollup against
// the ledger, hiding shipped features, and the done rename.
func TestFeatures(t *testing.T) {
	home, _ := storeLedger(t, "featproj", "001_build-board.done.md", "002_wire-dnd.md")

	if err := run([]string{"feature", "new", "Kanban board"}); err != nil {
		t.Fatalf("feature new: %v", err)
	}
	path := filepath.Join(home, "featproj", "features", "kanban-board.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("feature file: %v", err)
	}
	if !strings.HasPrefix(string(data), "# Kanban board\n") || !strings.Contains(string(data), "Tasks:") {
		t.Errorf("template:\n%s", data)
	}
	// Link the tasks (the documented flow: edit the Tasks: line).
	linked := strings.Replace(string(data), "Tasks:", "Tasks: 001, 002", 1)
	if err := os.WriteFile(path, []byte(linked), 0o644); err != nil {
		t.Fatal(err)
	}

	out := capture(t, func() { _ = run([]string{"feature", "list"}) })
	if !strings.Contains(out, "kanban-board") || !strings.Contains(out, "1/2 tasks done") {
		t.Errorf("feature list rollup:\n%s", out)
	}

	if err := run([]string{"feature", "done", "kanban"}); err != nil {
		t.Fatalf("feature done: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "featproj", "features", "kanban-board.done.md")); err != nil {
		t.Fatalf("done rename: %v", err)
	}
	out = capture(t, func() { _ = run([]string{"feature", "list"}) })
	if strings.Contains(out, "kanban-board") {
		t.Errorf("shipped feature must hide without -all:\n%s", out)
	}
	out = capture(t, func() { _ = run([]string{"feature", "list", "-all"}) })
	if !strings.Contains(out, "kanban-board") || !strings.Contains(out, "done") {
		t.Errorf("feature list -all:\n%s", out)
	}
	if log := git(t, home, "log", "--format=%s"); !strings.Contains(log, "chore(featproj): feature kanban-board") ||
		!strings.Contains(log, "chore(featproj): feature done kanban-board") {
		t.Errorf("feature commits:\n%s", log)
	}

	if err := run([]string{"feature", "bogus"}); err == nil {
		t.Error("unknown feature subcommand must error")
	}

	// findFeature refuses to guess: ambiguous fragments and misses error.
	if err := run([]string{"feature", "new", "Kanban mobile"}); err != nil {
		t.Fatalf("second feature: %v", err)
	}
	if err := run([]string{"feature", "new", "Kanban tablet"}); err != nil {
		t.Fatalf("third feature: %v", err)
	}
	if err := run([]string{"feature", "done", "kanban-"}); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("ambiguous feature fragment: %v", err)
	}
	if err := run([]string{"feature", "done", "nope"}); err == nil {
		t.Error("missing feature must error")
	}
	// An exact slug wins even when it is a prefix of another slug.
	if err := run([]string{"feature", "new", "Kanban"}); err != nil {
		t.Fatalf("prefix feature: %v", err)
	}
	if err := run([]string{"feature", "done", "kanban"}); err != nil {
		t.Errorf("exact slug must win over fragment matches: %v", err)
	}
}

// TestServeArgs pins the serve command's guard rails without binding.
func TestServeArgs(t *testing.T) {
	storeLedger(t, "serveproj")
	if err := run([]string{"serve", "extra-arg"}); err == nil {
		t.Error("stray args must error")
	}
	if err := run([]string{"serve", "-addr", "0.0.0.0:0"}); err == nil ||
		!strings.Contains(err.Error(), "insecure-bind") {
		t.Errorf("public bind must be refused: %v", err)
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
		!strings.Contains(log, "chore(alpha): open 002_second-thing") {
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

// srcRepo builds a git repo with a committed tasks/ ledger, mimicking a
// pre-cutover repo about to migrate.
func srcRepo(t *testing.T, dirname string, names ...string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), dirname)
	dir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("# "+n+"\nBody.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-q", "-m", "seed"},
	} {
		git(t, repo, args...)
	}
	return repo
}

// TestMigrate pins the import contract: byte-identical copies into an empty
// project only, an order file seeded with open numbers, non-task files
// skipped, one store commit, and -prune removing the source ledger with a
// pointer commit in its repo.
func TestMigrate(t *testing.T) {
	home, _ := storeLedger(t, "unrelated")
	repo := srcRepo(t, "My Old Repo",
		"001_alpha.done.md", "002_beta.md", "003_gamma.in-progress.md",
		"004_held.deferred.md", "qbd_legacy-ask.md")
	if err := os.WriteFile(filepath.Join(repo, "tasks", "README.md"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"migrate", repo}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dest := filepath.Join(home, "my-old-repo", "tasks")
	for _, n := range []string{"001_alpha.done.md", "002_beta.md", "003_gamma.in-progress.md",
		"004_held.deferred.md", "qbd_legacy-ask.md"} {
		want, _ := os.ReadFile(filepath.Join(repo, "tasks", n))
		got, err := os.ReadFile(filepath.Join(dest, n))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s not copied byte-for-byte: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err == nil {
		t.Error("non-task file must be skipped")
	}
	order, err := os.ReadFile(filepath.Join(home, "my-old-repo", "order"))
	if err != nil {
		t.Fatalf("order file: %v", err)
	}
	if s := string(order); !strings.Contains(s, "002\n003\n004\n") || strings.Contains(s, "001") {
		t.Errorf("order must list open numbers ascending:\n%s", s)
	}
	if log := git(t, home, "log", "-1", "--format=%s"); !strings.Contains(log, "chore(my-old-repo): migrate 5 tasks") {
		t.Errorf("store commit = %q", log)
	}
	// The source ledger is untouched without -prune.
	if _, err := os.Stat(filepath.Join(repo, "tasks", "002_beta.md")); err != nil {
		t.Error("source ledger must be left in place without -prune")
	}

	// A non-empty destination is refused.
	if err := run([]string{"migrate", repo}); err == nil {
		t.Error("migrating onto a non-empty project must error")
	}

	// -prune removes the source ledger and commits the pointer.
	repo2 := srcRepo(t, "prunerepo", "001_only.md")
	if err := run([]string{"migrate", "-prune", repo2, "pruned"}); err != nil {
		t.Fatalf("migrate -prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo2, "tasks")); err == nil {
		t.Error("-prune must remove the source tasks/")
	}
	if log := git(t, repo2, "log", "-1", "--format=%s"); !strings.Contains(log, "ledger moved to central taskman store") {
		t.Errorf("source pointer commit = %q", log)
	}
	if _, err := os.Stat(filepath.Join(home, "pruned", "tasks", "001_only.md")); err != nil {
		t.Errorf("explicit project name not honored: %v", err)
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
