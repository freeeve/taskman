// Package store owns where ledgers live and how their mutations are
// persisted: the central taskman store directory and its git repository.
package store

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// gitDir reports whether dir sits inside a git work tree.
func gitDir(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// gitTracked reports whether path is known to the index, so a rename's
// vanished source can still be staged as a deletion.
func gitTracked(dir, path string) bool {
	return exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", "--", path).Run() == nil
}

// gitRetry runs a git command, retrying briefly when another process holds
// .git/index.lock -- multiple sessions and the web UI share the store repo,
// so short lock collisions are expected and transient.
func gitRetry(verb string, args []string) error {
	var lastErr error
	for range 3 {
		out, err := exec.Command("git", args...).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("git %s: %v: %s", verb, err, strings.TrimSpace(string(out)))
		if !strings.Contains(string(out), "index.lock") {
			return lastErr
		}
		time.Sleep(time.Duration(100+rand.Intn(200)) * time.Millisecond)
	}
	return lastErr
}

// commitMu serializes the add+commit pair within this process: the two git
// invocations are not atomic, and concurrent goroutines (the web server's
// handlers) interleaving on the shared index can leave a mutation staged or
// untracked while both report success. Cross-process collisions (a CLI run
// against a live server) are covered by gitRetry's index.lock backoff.
var commitMu sync.Mutex

// Commit stages exactly the given paths and commits them with a pathspec,
// so a concurrent session's staged work in the same repo is never swept into
// the commit. Paths that neither exist nor are tracked are skipped.
func Commit(dir, msg string, paths []string) error {
	commitMu.Lock()
	defer commitMu.Unlock()
	if !gitDir(dir) {
		return fmt.Errorf("%s is not in a git repository", dir)
	}
	var known []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil || gitTracked(dir, p) {
			known = append(known, p)
		}
	}
	if len(known) == 0 {
		return fmt.Errorf("no committable paths")
	}
	add := append([]string{"-C", dir, "add", "-A", "--"}, known...)
	if err := gitRetry("add", add); err != nil {
		return err
	}
	commit := append([]string{"-C", dir, "commit", "-q", "-m", msg, "--"}, known...)
	if err := gitRetry("commit", commit); err != nil {
		// Racing mutations can leave this pathspec with nothing uncommitted
		// (a concurrent commit already captured the state, or a rename made
		// the pathspec stale). A clean pathspec satisfies the
		// every-mutation-committed contract, so it is success, not failure.
		if strings.Contains(err.Error(), "nothing to commit") ||
			strings.Contains(err.Error(), "nothing added to commit") {
			return nil
		}
		return err
	}
	return nil
}

// AutoCommit commits task-file paths unless disabled, downgrading git
// problems to a warning so the ledger operation itself still succeeds. The
// error is also returned for callers whose contract is stricter (the web
// API tells its client when a mutation was applied but not committed); CLI
// callers deliberately ignore it.
func AutoCommit(noCommit bool, dir, msg string, paths ...string) error {
	if noCommit {
		return nil
	}
	if err := Commit(dir, msg, paths); err != nil {
		fmt.Fprintf(os.Stderr, "taskman: not committed (%v)\n", err)
		return err
	}
	fmt.Println("committed:", msg)
	return nil
}
