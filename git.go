package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// gitCommit stages exactly the given paths and commits them with a pathspec,
// so a concurrent session's staged work in the same repo is never swept into
// the commit. Paths that neither exist nor are tracked are skipped.
func gitCommit(dir, msg string, paths []string) error {
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
	if out, err := exec.Command("git", add...).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	commit := append([]string{"-C", dir, "commit", "-q", "-m", msg, "--"}, known...)
	if out, err := exec.Command("git", commit...).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// autoCommit commits task-file paths unless disabled, downgrading git
// problems to a warning so the ledger operation itself still succeeds.
func autoCommit(noCommit bool, dir, msg string, paths ...string) {
	if noCommit {
		return
	}
	if err := gitCommit(dir, msg, paths); err != nil {
		fmt.Fprintf(os.Stderr, "taskman: not committed (%v)\n", err)
		return
	}
	fmt.Println("committed:", msg)
}
