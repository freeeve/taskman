// Package store owns where ledgers live and how their mutations are
// persisted: the central taskman store directory and its git repository.
package store

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
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

// Commit stages exactly the given paths and commits them with a pathspec,
// so a concurrent session's staged work in the same repo is never swept into
// the commit. Paths that neither exist nor are tracked are skipped.
func Commit(dir, msg string, paths []string) error {
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
	return gitRetry("commit", commit)
}

// AutoCommit commits task-file paths unless disabled, downgrading git
// problems to a warning so the ledger operation itself still succeeds.
func AutoCommit(noCommit bool, dir, msg string, paths ...string) {
	if noCommit {
		return
	}
	if err := Commit(dir, msg, paths); err != nil {
		fmt.Fprintf(os.Stderr, "taskman: not committed (%v)\n", err)
		return
	}
	fmt.Println("committed:", msg)
}
