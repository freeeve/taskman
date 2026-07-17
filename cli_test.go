package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freeeve/taskman/internal/lock"
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
	// A repo path as the target is refused, not slugified into a junk
	// project (the pre-store `file <repo-dir>` habit).
	if err := run([]string{"file", "~/otherproj", "Misfiled ask"}); err == nil ||
		!strings.Contains(err.Error(), "bare project name") {
		t.Errorf("file with a path target = %v, want refusal", err)
	}
	if _, err := os.Stat(filepath.Join(home, "otherproj-misfile")); err == nil {
		t.Error("no junk project may be created for a path target")
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

// withStdin runs fn with os.Stdin backed by a pipe carrying content, so a
// command reading `-body -` / `-append -` sees it.
func withStdin(t *testing.T, content string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()
	fn()
}

// TestShowAndUpdate drives editing a task through the CLI instead of the store
// file: show prints the raw body, update replaces / appends / retitles and
// commits, and the status-suffixed filename is never touched by hand.
func TestShowAndUpdate(t *testing.T) {
	home, dir := storeLedger(t, "editproj")

	if err := run([]string{"new", "Draft the plan"}); err != nil {
		t.Fatalf("new: %v", err)
	}
	orig := filepath.Join(dir, "001_draft-the-plan.md")

	// show prints the raw body; -path prints the file path.
	out := capture(t, func() { _ = run([]string{"show", "1"}) })
	if !strings.Contains(out, "# 001 -- Draft the plan") {
		t.Errorf("show body:\n%s", out)
	}
	out = capture(t, func() { _ = run([]string{"show", "-path", "draft"}) })
	if strings.TrimSpace(out) != orig {
		t.Errorf("show -path = %q, want %q", strings.TrimSpace(out), orig)
	}

	// -append adds to the end without a heading; the caller supplies markup.
	if err := run([]string{"update", "-append", "## Outcome\n\nShipped it.", "1"}); err != nil {
		t.Fatalf("update -append: %v", err)
	}
	body, _ := os.ReadFile(orig)
	if !strings.Contains(string(body), "# 001 -- Draft the plan") || !strings.HasSuffix(string(body), "## Outcome\n\nShipped it.\n") {
		t.Errorf("after append:\n%s", body)
	}

	// -body - replaces the whole body from stdin, normalized to one trailing
	// newline.
	withStdin(t, "# 001 -- Draft the plan\n\nRewritten body.\n\n\n", func() {
		if err := run([]string{"update", "-body", "-", "1"}); err != nil {
			t.Fatalf("update -body -: %v", err)
		}
	})
	body, _ = os.ReadFile(orig)
	if string(body) != "# 001 -- Draft the plan\n\nRewritten body.\n" {
		t.Errorf("after body replace:\n%q", string(body))
	}

	// -title restamps the H1 and renames the slug, keeping the number.
	if err := run([]string{"update", "-title", "Ship the plan", "1"}); err != nil {
		t.Fatalf("update -title: %v", err)
	}
	renamed := filepath.Join(dir, "001_ship-the-plan.md")
	body, err := os.ReadFile(renamed)
	if err != nil {
		t.Fatalf("renamed file: %v", err)
	}
	if !strings.HasPrefix(string(body), "# 001 -- Ship the plan\n") {
		t.Errorf("retitled H1:\n%s", body)
	}
	if _, err := os.Stat(orig); !os.IsNotExist(err) {
		t.Errorf("old slug file still present after retitle")
	}

	// Guards: nothing to do, both replace and append, and a blank body.
	if err := run([]string{"update", "1"}); err == nil {
		t.Error("update with no edit flag should error")
	}
	if err := run([]string{"update", "-body", "x", "-append", "y", "1"}); err == nil {
		t.Error("update with both -body and -append should error")
	}
	withStdin(t, "   \n", func() {
		if err := run([]string{"update", "-body", "-", "1"}); err == nil {
			t.Error("update -body - with blank stdin should refuse to blank the task")
		}
	})

	// Each successful update committed under the project-scoped message.
	if log := git(t, home, "log", "--format=%s"); !strings.Contains(log, "chore(editproj): update 001_ship-the-plan") ||
		!strings.Contains(log, "chore(editproj): update 001_draft-the-plan") {
		t.Errorf("update commits:\n%s", log)
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

	// Shipping is reversible from the CLI too.
	if err := run([]string{"feature", "reopen", "kanban-board"}); err != nil {
		t.Fatalf("feature reopen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "featproj", "features", "kanban-board.md")); err != nil {
		t.Fatalf("reopen rename: %v", err)
	}
	if err := run([]string{"feature", "reopen", "kanban-board"}); err == nil {
		t.Error("reopening an active feature must error")
	}
	if err := run([]string{"feature", "done", "kanban-board"}); err != nil {
		t.Fatalf("re-ship after reopen: %v", err)
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

	// rm discards (shipped features included); ambiguity and misses refuse.
	if err := run([]string{"feature", "rm", "kanban-"}); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("ambiguous rm = %v", err)
	}
	if err := run([]string{"feature", "rm", "nope"}); err == nil {
		t.Error("missing feature rm must error")
	}
	if err := run([]string{"feature", "rm", "kanban-tablet"}); err != nil {
		t.Fatalf("feature rm: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "featproj", "features", "kanban-tablet.md")); err == nil {
		t.Error("rm must remove the file")
	}
	if log := git(t, home, "log", "-1", "--format=%s"); !strings.Contains(log, "remove feature kanban-tablet") {
		t.Errorf("rm commit = %q", log)
	}
}

// TestDecisionFlow drives the interactive-decision CLI end to end: pose via
// defer -question, bare resume refuses, resume -choose answers, records, and
// jumps the task to the top of the order; reason-only defer is unchanged.
func TestDecisionFlow(t *testing.T) {
	home, dir := storeLedger(t, "decproj", "001_first.md", "002_fork-choice.md", "003_third.md")
	if err := os.WriteFile(filepath.Join(home, "decproj", "order"),
		[]byte("001\n002\n003\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"defer", "-question", "Inline or queue?",
		"-option", "Inline::simpler", "-option", "Queue::durable", "2"}); err != nil {
		t.Fatalf("defer -question: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "002_fork-choice.deferred.md"))
	if err != nil {
		t.Fatalf("deferred file: %v", err)
	}
	if !strings.Contains(string(body), "question: Inline or queue?") ||
		!strings.Contains(string(body), "- label: Queue") ||
		!strings.Contains(string(body), "explain: durable") {
		t.Errorf("decision block:\n%s", body)
	}

	out := capture(t, func() { _ = run([]string{"decisions"}) })
	if !strings.Contains(out, "Inline or queue?") {
		t.Errorf("decisions listing:\n%s", out)
	}

	// One option is not a question; bare resume refuses; bogus label refuses.
	if err := run([]string{"defer", "-question", "q", "-option", "only", "3"}); err == nil {
		t.Error("single-option question must error")
	}
	if err := run([]string{"resume", "2"}); err == nil ||
		!strings.Contains(err.Error(), "unanswered decision") {
		t.Errorf("bare resume = %v", err)
	}
	if err := run([]string{"resume", "-choose", "Nope", "2"}); err == nil {
		t.Error("unknown label must error")
	}

	if err := run([]string{"resume", "-choose", "Queue", "2"}); err != nil {
		t.Fatalf("resume -choose: %v", err)
	}
	body, err = os.ReadFile(filepath.Join(dir, "002_fork-choice.md"))
	if err != nil {
		t.Fatalf("resumed file: %v", err)
	}
	if !strings.Contains(string(body), "chosen: Queue") ||
		!strings.Contains(string(body), "```decision answered") {
		t.Errorf("answered record:\n%s", body)
	}
	order, _ := os.ReadFile(filepath.Join(home, "decproj", "order"))
	if !strings.HasPrefix(strings.TrimPrefix(string(order), "# priority order, top = next up; rewritten by taskman\n"), "002\n") {
		t.Errorf("answered decision must jump the queue:\n%s", order)
	}
	if log := git(t, home, "log", "-1", "--format=%s"); !strings.Contains(log, "answer decision on 002_fork-choice (Queue)") {
		t.Errorf("answer commit = %q", log)
	}

	// choose on a task without a live decision refuses; reason-only defer
	// still works and writes no block.
	if err := run([]string{"resume", "-choose", "Queue", "2"}); err == nil {
		t.Error("choose without a live decision must error")
	}
	if err := run([]string{"defer", "-reason", "plain hold", "1"}); err != nil {
		t.Fatalf("reason-only defer: %v", err)
	}
	body, _ = os.ReadFile(filepath.Join(dir, "001_first.deferred.md"))
	if strings.Contains(string(body), "```decision") {
		t.Errorf("reason-only defer must not write a block:\n%s", body)
	}
	if err := run([]string{"resume", "1"}); err != nil {
		t.Fatalf("plain resume: %v", err)
	}
}

// TestDecisionSurfacing pins the answer queue's CLI visibility: list splits
// "deferred" from "awaiting a decision", an empty top names pending
// decisions, and decisions -all sweeps every project with a project column
// (task 119).
func TestDecisionSurfacing(t *testing.T) {
	home, _ := storeLedger(t, "decsurf",
		"001_open-work.md", "002_needs-answer.md", "003_plain-hold.md")
	if err := run([]string{"defer", "-question", "Ship or wait?",
		"-option", "Ship::now", "-option", "Wait::later", "2"}); err != nil {
		t.Fatalf("defer -question: %v", err)
	}
	if err := run([]string{"defer", "-reason", "blocked on vendor", "3"}); err != nil {
		t.Fatalf("reason-only defer: %v", err)
	}

	// list separates the answerable subset from plain holds.
	out := capture(t, func() { _ = run([]string{"list"}) })
	if !strings.Contains(out, "2 deferred (taskman list -all)") ||
		!strings.Contains(out, "1 awaiting a decision (taskman decisions)") {
		t.Errorf("list surfacing:\n%s", out)
	}

	// top with work available stays a bare path; an empty sweep (lane
	// filter matches nothing) names the pending decisions.
	out = capture(t, func() { _ = run([]string{"top"}) })
	if !strings.Contains(out, "001_open-work.md") || strings.Contains(out, "decision") {
		t.Errorf("non-empty top must stay a bare path:\n%s", out)
	}
	err := run([]string{"top", "-lane", "impl"})
	if err == nil || !strings.Contains(err.Error(), "no pending tasks") ||
		!strings.Contains(err.Error(), "1 deferred await a decision (taskman decisions)") {
		t.Errorf("empty top = %v", err)
	}

	// A second project's decision shows up in the store-wide sweep with a
	// project column; the per-project view stays scoped.
	otherDir := filepath.Join(home, "otherproj", "tasks")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "001_pick-db.md"),
		[]byte("# 001 -- Pick db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"defer", "-p", "otherproj", "-question", "Which db?",
		"-option", "A::a", "-option", "B::b", "1"}); err != nil {
		t.Fatalf("other-project defer: %v", err)
	}
	out = capture(t, func() { _ = run([]string{"decisions", "-all"}) })
	if !strings.Contains(out, "decsurf") || !strings.Contains(out, "Ship or wait?") ||
		!strings.Contains(out, "otherproj") || !strings.Contains(out, "Which db?") {
		t.Errorf("decisions -all:\n%s", out)
	}
	out = capture(t, func() { _ = run([]string{"decisions"}) })
	if strings.Contains(out, "Which db?") {
		t.Errorf("per-project decisions must stay scoped:\n%s", out)
	}
}

// TestRmProject pins the safe project-removal command: open tasks refuse
// without -force, removal is one scoped commit, and unknown or already
// removed projects error cleanly (task 122).
func TestRmProject(t *testing.T) {
	home, _ := storeLedger(t, "rmme", "001_open-item.md")
	if err := run([]string{"rmproject", "rmme"}); err == nil ||
		!strings.Contains(err.Error(), "-force") {
		t.Errorf("open tasks must refuse removal: %v", err)
	}
	if err := run([]string{"done", "1"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"rmproject", "rmme"}); err != nil {
		t.Fatalf("removal of a settled project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "rmme")); !os.IsNotExist(err) {
		t.Error("project directory must be gone")
	}
	if s := git(t, home, "log", "-1", "--format=%s"); !strings.Contains(s, "chore(rmme): remove project") {
		t.Errorf("removal commit = %q", s)
	}
	if err := run([]string{"rmproject", "rmme"}); err == nil {
		t.Error("removing a removed project must error")
	}
	if err := run([]string{"rmproject", "-force", "nope"}); err == nil {
		t.Error("unknown project must error")
	}
}

// TestVersion pins the skew diagnostic: version always prints something and
// never errors, whatever build info is present.
func TestVersion(t *testing.T) {
	out := capture(t, func() {
		if err := run([]string{"version"}); err != nil {
			t.Errorf("version: %v", err)
		}
	})
	if !strings.HasPrefix(out, "taskman") {
		t.Errorf("version output = %q", out)
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

// TestLockCommands drives the resource lock end to end: one holder at a time
// per resource, disjoint resources in parallel, a token-checked release, and a
// loud steal -- none of it touching git, because a lock is machine state and
// committing it would spam the store and reintroduce the very race it exists
// to avoid.
func TestLockCommands(t *testing.T) {
	home, _ := storeLedger(t, "benchproj")
	if err := run([]string{"list"}); err != nil { // initialize the store and its seed commit
		t.Fatalf("list: %v", err)
	}
	commits := git(t, home, "rev-list", "--count", "HEAD")

	token := strings.TrimSpace(capture(t, func() {
		if err := run([]string{"lock", "acquire", "-ttl", "5m", "-reason", "sweep a8a13e9", "local-cpu"}); err != nil {
			t.Fatalf("acquire: %v", err)
		}
	}))
	if token == "" {
		t.Fatal("acquire must print the holder token on stdout, for the sweep script to export")
	}

	// A second acquire on the same resource loses, and says who has it.
	err := run([]string{"lock", "acquire", "local-cpu"})
	if err == nil {
		t.Fatal("two sessions acquired local-cpu at once")
	}
	if !strings.Contains(err.Error(), "held by benchproj") || !strings.Contains(err.Error(), "sweep a8a13e9") {
		t.Errorf("busy error %q names neither the holder nor its reason", err)
	}

	// A different resource is not contended: a ragedb sweep is a thin client
	// locally, so it may run while a local sweep holds the CPU.
	if err := capture(t, func() {
		if err := run([]string{"lock", "acquire", "ragedb-ec2"}); err != nil {
			t.Fatalf("acquire ragedb-ec2 while local-cpu is held: %v", err)
		}
	}); err == "" {
		t.Error("acquire printed no token")
	}

	status := capture(t, func() {
		if err := run([]string{"lock", "status"}); err != nil {
			t.Fatalf("status: %v", err)
		}
	})
	for _, want := range []string{"local-cpu", "ragedb-ec2", "benchproj@", "sweep a8a13e9"} {
		if !strings.Contains(status, want) {
			t.Errorf("status does not mention %q:\n%s", want, status)
		}
	}

	// Release proves ownership: the token, from the flag or the environment.
	if err := run([]string{"lock", "release", "-token", "deadbeefdeadbeef", "local-cpu"}); err == nil {
		t.Error("release with a wrong token succeeded")
	}
	t.Setenv(lock.EnvToken, token)
	if err := run([]string{"lock", "release", "local-cpu"}); err != nil {
		t.Fatalf("release with $%s: %v", lock.EnvToken, err)
	}
	if err := run([]string{"lock", "heartbeat", "local-cpu"}); err == nil {
		t.Error("heartbeat on a released lock succeeded")
	}

	// Steal is the human override for a wedged holder.
	if err := run([]string{"lock", "steal", "ragedb-ec2"}); err != nil {
		t.Fatalf("steal: %v", err)
	}
	if out := capture(t, func() {
		if err := run([]string{"lock", "status"}); err != nil {
			t.Fatalf("status: %v", err)
		}
	}); !strings.Contains(out, "no locks held") {
		t.Errorf("status after release and steal:\n%s", out)
	}

	// Nothing about a lock is ledger history.
	if after := git(t, home, "rev-list", "--count", "HEAD"); after != commits {
		t.Errorf("locking committed to the store: %s commits before, %s after", strings.TrimSpace(commits), strings.TrimSpace(after))
	}
	if s := git(t, home, "status", "--porcelain"); strings.Contains(s, ".locks") {
		t.Errorf(".locks/ must be gitignored, git status shows:\n%s", s)
	}
}

// TestLockRun holds a resource for one command and releases it after, so a
// sweep needs no trap to clean up.
func TestLockRun(t *testing.T) {
	home, _ := storeLedger(t, "benchproj")
	out := capture(t, func() {
		if err := run([]string{"lock", "run", "-ttl", "5m", "local-cpu", "--", "echo", "swept"}); err != nil {
			t.Fatalf("lock run: %v", err)
		}
	})
	if !strings.Contains(out, "swept") {
		t.Errorf("the command's output must pass through: %q", out)
	}
	if _, ok, err := lock.Read(home, "local-cpu"); err != nil || ok {
		t.Error("lock run must release the lock when the command exits")
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
