package task

import (
	"fmt"
	"os"
	"strings"
)

// Decision is a structured question a deferred task carries: the hold is not
// just prose, it is answerable. Exactly one live (unanswered) decision may
// exist per task; answering rewrites its block into an answered record, so
// the body keeps the audit trail and the resuming agent reads the choice.
type Decision struct {
	Question   string
	Options    []DecisionOption
	AllowOther bool
	Answered   bool
	Chosen     string
	Note       string
}

// DecisionOption is one labelled choice with its long-form explanation.
type DecisionOption struct {
	Label   string
	Explain string
}

const (
	decisionFence         = "```decision"
	decisionAnsweredFence = "```decision answered"
)

// findLiveBlock returns the start/end line indexes (inclusive fence lines)
// of the live decision block, or ok=false.
func findLiveBlock(lines []string) (start, end int, ok bool) {
	for i := range lines {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == decisionFence {
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "```" {
					return i, j, true
				}
			}
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// ParseDecision extracts the body's live decision, ok=false when none.
// Parsing is lenient in the ledger tradition: unknown lines are ignored, and
// a block without a question or at least two labelled options is not a
// decision.
func ParseDecision(body string) (Decision, bool) {
	lines := strings.Split(body, "\n")
	start, end, ok := findLiveBlock(lines)
	if !ok {
		return Decision{}, false
	}
	d := Decision{AllowOther: true}
	inOptions := false
	for _, raw := range lines[start+1 : end] {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "question:"):
			d.Question = strings.TrimSpace(strings.TrimPrefix(line, "question:"))
			inOptions = false
		case line == "options:":
			inOptions = true
		case strings.HasPrefix(line, "allow_other:"):
			d.AllowOther = strings.TrimSpace(strings.TrimPrefix(line, "allow_other:")) != "false"
			inOptions = false
		case inOptions && strings.HasPrefix(line, "- label:"):
			label := strings.TrimSpace(strings.TrimPrefix(line, "- label:"))
			if label != "" {
				d.Options = append(d.Options, DecisionOption{Label: label})
			}
		case inOptions && strings.HasPrefix(line, "explain:") && len(d.Options) > 0:
			d.Options[len(d.Options)-1].Explain = strings.TrimSpace(strings.TrimPrefix(line, "explain:"))
		}
	}
	if d.Question == "" || len(d.Options) < 2 {
		return Decision{}, false
	}
	return d, true
}

// HasAnsweredDecision reports whether the body carries any answered record,
// which distinguishes "already answered" (a stale writer) from "never had a
// question".
func HasAnsweredDecision(body string) bool {
	return strings.Contains(body, decisionAnsweredFence)
}

// formatDecision serializes the live block.
func formatDecision(d Decision) string {
	var b strings.Builder
	fmt.Fprintln(&b, decisionFence)
	fmt.Fprintln(&b, "question:", d.Question)
	fmt.Fprintln(&b, "options:")
	for _, opt := range d.Options {
		fmt.Fprintln(&b, "- label:", opt.Label)
		if opt.Explain != "" {
			fmt.Fprintln(&b, "  explain:", opt.Explain)
		}
	}
	fmt.Fprintf(&b, "allow_other: %v\n", d.AllowOther)
	b.WriteString("```")
	return b.String()
}

// PoseDecision appends a live decision block to the task body. A second live
// question is refused rather than stacked -- one open fork at a time.
func (t Task) PoseDecision(d Decision) error {
	if d.Question == "" || len(d.Options) < 2 {
		return fmt.Errorf("a decision needs a question and at least two options")
	}
	for _, opt := range d.Options {
		if strings.TrimSpace(opt.Label) == "" {
			return fmt.Errorf("every option needs a non-empty label")
		}
	}
	data, err := os.ReadFile(t.Path())
	if err != nil {
		return err
	}
	if _, live := ParseDecision(string(data)); live {
		return fmt.Errorf("%s already has an unanswered decision", t.File)
	}
	out := strings.TrimRight(string(data), "\n") + "\n\n" + formatDecision(d) + "\n"
	return os.WriteFile(t.Path(), []byte(out), 0o644)
}

// AnswerDecision rewrites the live block into an answered record carrying
// the choice (and note, for Other or commentary), so history and the
// resuming agent both see it. Choice validation belongs to the caller, which
// has the parsed options.
func (t Task) AnswerDecision(chosen, note, date string) error {
	data, err := os.ReadFile(t.Path())
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	start, end, ok := findLiveBlock(lines)
	if !ok {
		return fmt.Errorf("%s has no unanswered decision", t.File)
	}
	d, ok := ParseDecision(string(data))
	if !ok {
		return fmt.Errorf("%s has no unanswered decision", t.File)
	}
	var b strings.Builder
	fmt.Fprintln(&b, decisionAnsweredFence, date)
	fmt.Fprintln(&b, "question:", d.Question)
	fmt.Fprintln(&b, "chosen:", chosen)
	if note != "" {
		fmt.Fprintln(&b, "note:", note)
	}
	b.WriteString("```")
	out := append(append([]string{}, lines[:start]...), strings.Split(b.String(), "\n")...)
	out = append(out, lines[end+1:]...)
	return os.WriteFile(t.Path(), []byte(strings.Join(out, "\n")), 0o644)
}
