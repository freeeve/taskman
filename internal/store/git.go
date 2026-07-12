// Package store owns where ledgers live and how their mutations are
// persisted: the central taskman store directory and its git repository.
package store

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
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
		// the pathspec stale -- git then answers "did not match any file").
		// A clean pathspec satisfies the every-mutation-committed contract,
		// so both are success, not failure.
		if strings.Contains(err.Error(), "nothing to commit") ||
			strings.Contains(err.Error(), "nothing added to commit") ||
			strings.Contains(err.Error(), "did not match any file") {
			return nil
		}
		return err
	}
	return nil
}

// RemoveProject deletes a project's directory from the store as one
// pathspec-scoped removal commit. The pathspec matters: the store is
// multi-writer, and a bare commit here would sweep another session's
// concurrently staged work into a mislabeled removal.
func RemoveProject(home, name, msg string) error {
	commitMu.Lock()
	defer commitMu.Unlock()
	dir := filepath.Join(home, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("no project %q", name)
	}
	rm := []string{"-C", home, "rm", "-r", "-q", "--ignore-unmatch", "--", name + "/"}
	if err := gitRetry("rm", rm); err != nil {
		return err
	}
	// Untracked leftovers (an order file never committed, stray temp files)
	// survive git rm; the directory must go regardless.
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	commit := []string{"-C", home, "commit", "-q", "-m", msg, "--", name + "/"}
	if err := gitRetry("commit", commit); err != nil {
		if strings.Contains(err.Error(), "nothing to commit") ||
			strings.Contains(err.Error(), "did not match any file") {
			return nil
		}
		return err
	}
	return nil
}

// LastProjectCommit returns the newest commit touching the project's
// directory -- NOT the repo HEAD, because the store is multi-writer and HEAD
// may belong to another project.
func LastProjectCommit(home, project string) (hash, subject string, err error) {
	out, err := exec.Command("git", "-C", home, "log", "-1", "--format=%H%x00%s",
		"--", project+"/").Output()
	if err != nil {
		return "", "", fmt.Errorf("git log: %v", err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", "", fmt.Errorf("no commits touch project %s", project)
	}
	hash, subject, ok := strings.Cut(line, "\x00")
	if !ok {
		return "", "", fmt.Errorf("unparseable log entry %q", line)
	}
	return hash, subject, nil
}

// LogEntry is one commit in a project's history.
type LogEntry struct {
	Hash    string
	Subject string
	Time    string // author date, ISO 8601, straight from commit metadata
}

// ProjectLog returns recent commits touching the project's directory,
// newest first.
func ProjectLog(home, project string, limit int) ([]LogEntry, error) {
	out, err := exec.Command("git", "-C", home, "log", "-n", fmt.Sprint(limit),
		"--format=%H%x00%s%x00%aI", "--", project+"/").Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %v", err)
	}
	var entries []LogEntry
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		entries = append(entries, LogEntry{Hash: parts[0], Subject: parts[1], Time: parts[2]})
	}
	return entries, nil
}

// Revert reverts one commit as its own no-edit revert commit, keeping the
// trail append-only (and the undo itself undoable). A conflicting revert is
// aborted so the working tree is left clean.
func Revert(home, hash string) error {
	commitMu.Lock()
	defer commitMu.Unlock()
	if out, err := exec.Command("git", "-C", home, "revert", "--no-edit", hash).CombinedOutput(); err != nil {
		_ = exec.Command("git", "-C", home, "revert", "--abort").Run()
		return fmt.Errorf("revert would conflict with later changes: %s", strings.TrimSpace(string(out)))
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
