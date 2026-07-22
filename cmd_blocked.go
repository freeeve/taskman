package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

// maxBlockMsg keeps a block one glanceable line: the message names what the
// stalled session is doing, the blocking task(s), and the lane it thinks owns
// them -- not a paragraph.
const maxBlockMsg = 500

// cmdBlocked is the cross-session stall board: a session that goes idle
// needing work another lane owns raises a one-line block instead of waiting
// silently, and the lane that can fix it sees the alert and responds.
//
//	taskman blocked                         list every active block
//	taskman blocked <lane> "<what/who>"     raise or replace your lane's block
//	taskman blocked <lane> ""               clear your lane's block once unblocked
//	taskman blocked -unblock <lane> [note]  respond: mark that lane unblocked
//
// One entry per lane, capped and committed like the rest of the ledger so
// sibling sessions sharing the store see it immediately.
func cmdBlocked(args []string) error {
	fs := flag.NewFlagSet("blocked", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	unblock := fs.Bool("unblock", false, "mark the given lane's block resolved (a response), not raise one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	projDir := filepath.Dir(p.Dir)
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339)

	if *unblock {
		if fs.NArg() < 1 {
			return fmt.Errorf("usage: taskman blocked -unblock [-p project] <lane> [note]")
		}
		lane := fs.Arg(0)
		note := strings.TrimSpace(strings.Join(fs.Args()[1:], " "))
		path, ok, err := store.UnblockLane(projDir, lane, note, stamp)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no block raised by lane %q", lane)
		}
		fmt.Printf("marked %s unblocked\n", lane)
		p.commit(*noCommit, fmt.Sprintf("unblock %s", lane), path)
		return nil
	}

	if fs.NArg() == 0 {
		return printBlocked(store.ReadBlocked(projDir), now)
	}
	if fs.NArg() < 2 {
		return fmt.Errorf(`usage: taskman blocked [-p project] <lane> "<message>"  (empty message clears the lane)`)
	}
	lane := fs.Arg(0)
	if err := task.CheckLane(task.Slugify(lane)); err != nil {
		return err
	}
	lane = task.Slugify(lane)
	if lane == "" {
		return fmt.Errorf("lane %q yields an empty token", fs.Arg(0))
	}
	message := strings.TrimSpace(strings.Join(fs.Args()[1:], " "))

	if message == "" {
		path, ok, err := store.ClearBlock(projDir, lane)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Printf("no block raised by lane %s\n", lane)
			return nil
		}
		fmt.Printf("cleared block for %s\n", lane)
		p.commit(*noCommit, fmt.Sprintf("clear block %s", lane), path)
		return nil
	}
	if len(message) > maxBlockMsg {
		return fmt.Errorf("message is %d bytes; keep a block to one line (max %d)", len(message), maxBlockMsg)
	}
	path, err := store.SetBlock(projDir, lane, message, stamp)
	if err != nil {
		return err
	}
	fmt.Printf("blocked %s: %s\n", lane, message)
	p.commit(*noCommit, fmt.Sprintf("block %s", lane), path)
	return nil
}

// printBlocked renders the stall board with each block's age (how long it has
// been stuck, the actionable signal) instead of a calendar date. Unblocked
// entries are flagged so the raising session sees the response and can clear
// its own block.
func printBlocked(blocks []store.Block, now time.Time) error {
	if len(blocks) == 0 {
		fmt.Println("no blocked lanes")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	for _, b := range blocks {
		status, msg := "blocked", b.Message
		if b.Unblocked {
			status = "unblocked"
			if b.Note != "" {
				msg = msg + "  || " + b.Note
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Lane, status, humanizeAge(b.Raised, now), msg)
	}
	return w.Flush()
}

// humanizeAge renders how long ago a block was raised as a compact duration.
// It reads the RFC3339 timestamp new blocks store, and falls back to the
// date-only form older entries carried (approximate, from that day's start)
// so a mid-migration board still reads sensibly; an unparseable value shows
// raw rather than lying about the age.
func humanizeAge(stamp string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		if t, err = time.Parse("2006-01-02", stamp); err != nil {
			return stamp
		}
	}
	return humanizeDuration(now.Sub(t))
}

// humanizeDuration coarsens an elapsed time to one unit: under a minute reads
// "<1m", then minutes, hours, and days as the block ages.
func humanizeDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
