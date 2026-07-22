package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxBlocks caps the blocked file: it is a live "who is stuck on whom" board
// meant to be read at a glance (and to stay cheap in an agent's context), not
// a log. One entry per lane, oldest evicted past the cap.
const MaxBlocks = 20

// blockedHeader documents the file for a human opening it; ReadBlocked skips it.
const blockedHeader = "# blocked lanes -- raised with `taskman blocked <lane> <msg>`, cleared with an empty message\n"

// Block is one lane's stall: a session in Lane went idle needing work it
// cannot do itself, and left Message saying what it is trying to do, what is
// blocking it, and which lane it thinks owns the blocker. A responder marks it
// Unblocked (with an optional Note) once the blocker is cleared; the raising
// session then removes the entry.
type Block struct {
	Lane      string
	Raised    string // RFC3339 timestamp the block was raised (age is shown from it)
	Unblocked bool
	Answered  string // RFC3339 timestamp a responder marked it unblocked
	Message   string
	Note      string // responder's note when unblocking
}

// BlockedPath returns the project's blocked file path.
func BlockedPath(projDir string) string { return filepath.Join(projDir, "blocked") }

// blockField strips tabs and newlines so a value survives the tab-separated,
// one-entry-per-line format intact.
func blockField(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
}

// ReadBlocked reads the project's blocked board. Like the order file it is
// advisory, so reading is lenient and never errors: a missing file means
// nothing is blocked, and blank lines, the header comment, and malformed rows
// are skipped.
func ReadBlocked(projDir string) []Block {
	data, err := os.ReadFile(BlockedPath(projDir))
	if err != nil {
		return nil
	}
	var blocks []Block
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 || f[0] == "" || f[4] == "" {
			continue
		}
		b := Block{Lane: f[0], Raised: f[1], Unblocked: f[2] == "1", Answered: f[3], Message: f[4]}
		if len(f) >= 6 {
			b.Note = f[5]
		}
		blocks = append(blocks, b)
	}
	return blocks
}

// WriteBlocked rewrites the project's blocked file and returns its path.
func WriteBlocked(projDir string, blocks []Block) (string, error) {
	path := BlockedPath(projDir)
	var b strings.Builder
	b.WriteString(blockedHeader)
	for _, bl := range blocks {
		unblocked := "0"
		if bl.Unblocked {
			unblocked = "1"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n",
			bl.Lane, bl.Raised, unblocked, bl.Answered, blockField(bl.Message), blockField(bl.Note))
	}
	return path, os.WriteFile(path, []byte(b.String()), 0o644)
}

// SetBlock raises or replaces the block for a lane, newest kept and the oldest
// dropped once MaxBlocks is reached. Re-raising a lane resets it to blocked.
func SetBlock(projDir, lane, message, stamp string) (string, error) {
	blocks := ReadBlocked(projDir)
	entry := Block{Lane: lane, Raised: stamp, Message: message}
	out := make([]Block, 0, len(blocks)+1)
	for _, b := range blocks {
		if b.Lane != lane {
			out = append(out, b)
		}
	}
	out = append(out, entry)
	for len(out) > MaxBlocks {
		out = out[1:]
	}
	return WriteBlocked(projDir, out)
}

// ClearBlock removes a lane's entry; ok reports whether one was present.
func ClearBlock(projDir, lane string) (string, bool, error) {
	blocks := ReadBlocked(projDir)
	out := make([]Block, 0, len(blocks))
	found := false
	for _, b := range blocks {
		if b.Lane == lane {
			found = true
			continue
		}
		out = append(out, b)
	}
	if !found {
		return "", false, nil
	}
	path, err := WriteBlocked(projDir, out)
	return path, true, err
}

// UnblockLane marks a lane's block resolved with an optional note; ok reports
// whether a matching entry existed.
func UnblockLane(projDir, lane, note, stamp string) (string, bool, error) {
	blocks := ReadBlocked(projDir)
	found := false
	for i := range blocks {
		if blocks[i].Lane == lane {
			blocks[i].Unblocked = true
			blocks[i].Answered = stamp
			blocks[i].Note = note
			found = true
		}
	}
	if !found {
		return "", false, nil
	}
	path, err := WriteBlocked(projDir, blocks)
	return path, true, err
}
