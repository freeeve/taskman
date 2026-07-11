package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

// cmdNew creates and commits the next numbered pending task.
func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	lane := fs.String("lane", "", "routing token carried in the filename (012-impl_slug.md)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	desc := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if desc == "" {
		return fmt.Errorf("usage: taskman new [-p project] [-lane lane] [-no-commit] <description>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	laneTok := task.Slugify(*lane)
	if *lane != "" && laneTok == "" {
		return fmt.Errorf("lane %q yields an empty token", *lane)
	}
	t, err := task.New(p.Dir, p.Tasks, desc, laneTok, time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	fmt.Println(t.Path())
	p.commit(*noCommit, fmt.Sprintf("open %s", t.Stem()), t.Path())
	return nil
}

// cmdLane sets or clears ("-") a task's lane token and commits the rename.
func cmdLane(args []string) error {
	fs := flag.NewFlagSet("lane", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: taskman lane [-p project] [-no-commit] <number|slug> <lane|->")
	}
	lane := fs.Arg(1)
	if lane == "-" {
		lane = ""
	} else if lane = task.Slugify(lane); lane == "" {
		return fmt.Errorf("lane %q yields an empty token", fs.Arg(1))
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.SetLane(lane)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	verb := "lane " + lane
	if lane == "" {
		verb = "clear lane"
	}
	p.commit(*noCommit, fmt.Sprintf("%s %s", verb, nt.Stem()), t.Path(), nt.Path())
	return nil
}

// statusVerb names the transition for usage and commit messages.
var statusVerb = map[task.Status]string{task.InProgress: "start", task.Done: "done", task.Pending: "reopen"}

// cmdStatus renames the matched task to the target status and commits the
// rename.
func cmdStatus(args []string, s task.Status) error {
	fs := flag.NewFlagSet(statusVerb[s], flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman %s [-p project] [-no-commit] <number|slug>", statusVerb[s])
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.SetStatus(s)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	paths := []string{t.Path(), nt.Path()}
	if s == task.Done && nt.HasNum {
		op, err := store.PruneOrder(filepath.Dir(p.Dir), map[int]bool{nt.Num: true})
		if err != nil {
			return err
		}
		if op != "" {
			paths = append(paths, op)
		}
	}
	p.commit(*noCommit, fmt.Sprintf("%s %s", statusVerb[s], nt.Stem()), paths...)
	return nil
}

// optionFlags collects repeatable -option values ("Label::explanation").
type optionFlags []string

func (o *optionFlags) String() string { return strings.Join(*o, "; ") }
func (o *optionFlags) Set(v string) error {
	*o = append(*o, v)
	return nil
}

// cmdDefer holds a task on an external decision and commits the rename. The
// hold must carry its why: either a -reason, or a structured -question with
// labelled -option choices that the web dialog (or resume -choose) can
// answer -- an unexplained deferral decays into an unexplained pending task,
// and the filename cannot carry the why.
func cmdDefer(args []string) error {
	fs := flag.NewFlagSet("defer", flag.ContinueOnError)
	reason := fs.String("reason", "", "why the task is held")
	question := fs.String("question", "", "structured question to pose (with -option choices)")
	var options optionFlags
	fs.Var(&options, "option", `answer choice as "Label::explanation" (repeatable, >=2)`)
	noOther := fs.Bool("no-other", false, "disallow a free-text Other answer")
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman defer (-reason <why> | -question <q> -option <a> -option <b>) [-p project] [-no-commit] <number|slug>")
	}
	why := strings.TrimSpace(*reason)
	q := strings.TrimSpace(*question)
	if why == "" && q == "" {
		return fmt.Errorf("taskman defer requires -reason or -question: record why this is held, not just that it is")
	}
	var decision task.Decision
	if q != "" {
		decision = task.Decision{Question: q, AllowOther: !*noOther}
		for _, raw := range options {
			label, explain, _ := strings.Cut(raw, "::")
			decision.Options = append(decision.Options,
				task.DecisionOption{Label: strings.TrimSpace(label), Explain: strings.TrimSpace(explain)})
		}
		if len(decision.Options) < 2 {
			return fmt.Errorf("a -question needs at least two -option choices")
		}
		if why == "" {
			why = "decision needed: " + q
		}
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	nt, err := t.Defer(why, time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	if q != "" {
		if err := nt.PoseDecision(decision); err != nil {
			return err
		}
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	p.commit(*noCommit, fmt.Sprintf("defer %s (%s)", nt.Stem(), why), t.Path(), nt.Path())
	return nil
}

// cmdResume lifts a deferral, returning the task to the working set at the
// status it held, and commits the rename. A task holding an unanswered
// decision must be answered (-choose / -choose-other), never silently
// dropped; an answered decision jumps to the top of the priority order.
func cmdResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	choose := fs.String("choose", "", "answer the task's decision with this option label")
	chooseOther := fs.String("choose-other", "", "answer the task's decision with free text")
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman resume [-choose <label> | -choose-other <text>] [-p project] [-no-commit] <number|slug>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	body, err := os.ReadFile(t.Path())
	if err != nil {
		return err
	}
	decision, live := task.ParseDecision(string(body))
	chosen, note := strings.TrimSpace(*choose), strings.TrimSpace(*chooseOther)
	if !live && (chosen != "" || note != "") {
		return fmt.Errorf("%s has no unanswered decision", t.File)
	}
	if live {
		switch {
		case chosen != "":
			okLabel := false
			for _, opt := range decision.Options {
				if opt.Label == chosen {
					okLabel = true
					break
				}
			}
			if !okLabel {
				return fmt.Errorf("%q is not one of the options; pick a label or use -choose-other", chosen)
			}
		case note != "":
			if !decision.AllowOther {
				return fmt.Errorf("this decision does not allow a free-text answer; pick a label with -choose")
			}
			chosen = "Other"
		default:
			return fmt.Errorf("this task has an unanswered decision; answer it with -choose <label> or -choose-other <text>")
		}
		if err := t.AnswerDecision(chosen, note, time.Now().Format("2006-01-02")); err != nil {
			return err
		}
	}
	nt, err := t.Resume(time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	paths := []string{t.Path(), nt.Path()}
	msg := fmt.Sprintf("resume %s", nt.Stem())
	if live {
		op, err := store.PromoteToTop(filepath.Dir(p.Dir), nt.Num)
		if err != nil {
			return err
		}
		paths = append(paths, op)
		msg = fmt.Sprintf("answer decision on %s (%s)", nt.Stem(), chosen)
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	p.commit(*noCommit, msg, paths...)
	return nil
}

// cmdAdopt renumbers a prefixed cross-repo ask into the ledger and commits
// the rename.
func cmdAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman adopt [-p project] [-no-commit] <file|fragment>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	key := strings.TrimSuffix(filepath.Base(fs.Arg(0)), ".md")
	t, err := task.Find(p.Tasks, key)
	if err != nil {
		return err
	}
	nt, err := t.Adopt(task.NextNum(p.Tasks))
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", t.File, nt.File)
	p.commit(*noCommit, fmt.Sprintf("adopt %s as %03d", t.Stem(), nt.Num), t.Path(), nt.Path())
	return nil
}
