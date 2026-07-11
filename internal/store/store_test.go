package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freeeve/taskman/internal/task"
)

// gitIdentity gives git an identity via the environment so commits work in
// pristine test homes without touching any config file.
func gitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.org")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.org")
}

// testHome points TASKMAN_HOME at a fresh temp store.
func testHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "store")
	t.Setenv("TASKMAN_HOME", home)
	gitIdentity(t)
	return home
}

// gitOut runs git in dir and returns its trimmed output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestHome(t *testing.T) {
	t.Setenv("TASKMAN_HOME", "/somewhere/else")
	if h, err := Home(); err != nil || h != "/somewhere/else" {
		t.Errorf("Home() = %q, %v", h, err)
	}
	t.Setenv("TASKMAN_HOME", "")
	h, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(h) != ".taskman" {
		t.Errorf("default Home() = %q, want ~/.taskman", h)
	}
}

func TestEnsureInitializesOnce(t *testing.T) {
	home := testHome(t)
	got, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if got != home {
		t.Errorf("Ensure() = %q, want %q", got, home)
	}
	if _, err := os.Stat(filepath.Join(home, ".git")); err != nil {
		t.Fatalf("store not a git repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "README.md")); err != nil {
		t.Fatalf("seed README missing: %v", err)
	}
	if log := gitOut(t, home, "log", "--format=%s"); !strings.Contains(log, "initialize taskman store") {
		t.Errorf("seed commit missing: %q", log)
	}
	// A second Ensure is a no-op: same home, still exactly one commit.
	if _, err := Ensure(); err != nil {
		t.Fatal(err)
	}
	if n := gitOut(t, home, "rev-list", "--count", "HEAD"); n != "1" {
		t.Errorf("Ensure must be idempotent; %s commits", n)
	}
}

func TestEnsureProject(t *testing.T) {
	home := testHome(t)
	if _, err := Ensure(); err != nil {
		t.Fatal(err)
	}
	dir, err := EnsureProject(home, "myproj")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"tasks", "features", "screenshots"} {
		if fi, err := os.Stat(filepath.Join(dir, d)); err != nil || !fi.IsDir() {
			t.Errorf("missing %s/: %v", d, err)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	gitIdentity(t)

	// The flag beats everything and is slugified.
	t.Setenv("TASKMAN_PROJECT", "envproj")
	if got, err := Resolve("Flag Proj"); err != nil || got != "flag-proj" {
		t.Errorf("Resolve(flag) = %q, %v", got, err)
	}
	// The env var beats directory-derived names.
	if got, err := Resolve(""); err != nil || got != "envproj" {
		t.Errorf("Resolve(env) = %q, %v", got, err)
	}

	// The enclosing git repo's basename beats the cwd basename.
	t.Setenv("TASKMAN_PROJECT", "")
	repo := filepath.Join(t.TempDir(), "My Repo")
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	t.Chdir(sub)
	if got, err := Resolve(""); err != nil || got != "my-repo" {
		t.Errorf("Resolve(git toplevel) = %q, %v", got, err)
	}

	// Outside any repo, the cwd basename is the fallback.
	plain := filepath.Join(t.TempDir(), "Plain Dir")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(plain)
	if got, err := Resolve(""); err != nil || got != "plain-dir" {
		t.Errorf("Resolve(cwd) = %q, %v", got, err)
	}

	// A name that slugifies to nothing is an error, not a mystery directory.
	if _, err := Resolve("---"); err == nil {
		t.Error("unslugifiable name must error")
	}

	// Paths are refused, not slugified into junk projects: the pre-store
	// `file <repo-dir>` habit misfiled real asks (users-efreeman-libcat).
	for _, bad := range []string{"~/libcat", "/Users/x/libcat", "./libcat", "..", `..\win`, "sub/dir"} {
		if _, err := Resolve(bad); err == nil ||
			!strings.Contains(err.Error(), "bare project name") {
			t.Errorf("Resolve(%q) = %v, want path refusal", bad, err)
		}
	}
	// The env var is guarded too.
	t.Setenv("TASKMAN_PROJECT", "~/libcat")
	if _, err := Resolve(""); err == nil {
		t.Error("path in TASKMAN_PROJECT must be refused")
	}
	t.Setenv("TASKMAN_PROJECT", "")
}

func TestProjects(t *testing.T) {
	home := testHome(t)
	if _, err := Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"beta", "alpha"} {
		if _, err := EnsureProject(home, p); err != nil {
			t.Fatal(err)
		}
	}
	names, err := Projects(home)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, " ") != "alpha beta" {
		t.Errorf("Projects = %v (dotdirs and files must be excluded, sorted)", names)
	}
}

// TestStoreLock pins the cross-process serialization primitive: exclusive,
// blocking with a timeout, released explicitly (or at process exit).
func TestStoreLock(t *testing.T) {
	home := t.TempDir()
	l1, err := AcquireLock(home)
	if err != nil {
		t.Fatal(err)
	}
	saved := lockTimeout
	lockTimeout = 200 * time.Millisecond
	defer func() { lockTimeout = saved }()
	if _, err := AcquireLock(home); err == nil {
		t.Fatal("second acquire must time out while held")
	}
	l1.Release()
	l2, err := AcquireLock(home)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	l2.Release()
}

// TestConcurrentAllocationUnderLock pins the duplicate-number fix: N
// concurrent writers each doing lock -> load -> NextNum -> create -> release
// (the CLI's shape) mint N distinct numbers.
func TestConcurrentAllocationUnderLock(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "p", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := AcquireLock(home)
			if err != nil {
				errs[i] = err
				return
			}
			defer lock.Release()
			tasks, err := task.Load(dir)
			if err != nil {
				errs[i] = err
				return
			}
			num := task.NextNum(tasks)
			errs[i] = task.Create(filepath.Join(dir, fmt.Sprintf("%03d_t%02d.md", num, i)),
				"# body\n")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %02d: %v", i, err)
		}
	}
	tasks, _ := task.Load(dir)
	if len(tasks) != n {
		t.Fatalf("tasks = %d, want %d", len(tasks), n)
	}
	if dups := task.Dups(tasks); len(dups) != 0 {
		t.Errorf("duplicate numbers minted under lock: %v", dups)
	}
}

// TestConcurrentCommits pins commit atomicity within a process: concurrent
// Commit calls race the shared index (add and commit are separate git
// invocations), and every one must land -- no file left staged or untracked
// while its caller believes it succeeded.
func TestConcurrentCommits(t *testing.T) {
	home := testHome(t)
	if _, err := Ensure(); err != nil {
		t.Fatal(err)
	}
	const n = 12
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := filepath.Join(home, fmt.Sprintf("f%02d.txt", i))
			if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
				errs[i] = err
				return
			}
			errs[i] = Commit(home, fmt.Sprintf("chore(store): concurrent %02d", i), []string{path})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("commit %02d: %v", i, err)
		}
	}
	if status := gitOut(t, home, "status", "--porcelain"); status != "" {
		t.Errorf("working tree not clean after concurrent commits:\n%s", status)
	}
	// Seed commit + one per goroutine.
	if count := gitOut(t, home, "rev-list", "--count", "HEAD"); count != fmt.Sprint(n+1) {
		t.Errorf("commit count = %s, want %d", count, n+1)
	}
}

// TestCommitCleanPathspecIsSuccess pins the race tolerance: committing a
// pathspec whose state is already fully committed (a concurrent mutation
// got there first) is success, not an error surfaced to the client.
func TestCommitCleanPathspecIsSuccess(t *testing.T) {
	home := testHome(t)
	if _, err := Ensure(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "f.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(home, "chore(store): first", []string{path}); err != nil {
		t.Fatal(err)
	}
	if err := Commit(home, "chore(store): nothing new", []string{path}); err != nil {
		t.Errorf("clean pathspec must be success: %v", err)
	}
	if count := gitOut(t, home, "rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("commit count = %s, want 2 (seed + first)", count)
	}
}

// TestCommitRetriesIndexLock pins the shared-store contract: a transient
// index.lock held by another process delays a commit instead of failing it.
func TestCommitRetriesIndexLock(t *testing.T) {
	home := testHome(t)
	if _, err := Ensure(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "file.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(home, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		os.Remove(lock)
	}()
	if err := Commit(home, "chore(store): survive a transient lock", []string{path}); err != nil {
		t.Fatalf("Commit did not ride out the transient lock: %v", err)
	}
	if log := gitOut(t, home, "log", "-1", "--format=%s"); !strings.Contains(log, "transient lock") {
		t.Errorf("commit missing after retry: %q", log)
	}
}
