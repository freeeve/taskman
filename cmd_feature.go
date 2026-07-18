package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

// cmdFeature dispatches the feature subcommands: features are per-project
// markdown files under features/, the source of truth for what the product
// should do, linked to implementing tasks by a "Tasks:" line.
func cmdFeature(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taskman feature <new|list|done> ...")
	}
	switch args[0] {
	case "new":
		return cmdFeatureNew(args[1:])
	case "list", "ls":
		return cmdFeatureList(args[1:])
	case "show", "cat":
		return cmdFeatureShow(args[1:])
	case "update", "edit":
		return cmdFeatureUpdate(args[1:])
	case "done":
		return cmdFeatureSetDone(args[1:], true)
	case "reopen":
		return cmdFeatureSetDone(args[1:], false)
	case "rm", "remove":
		return cmdFeatureRm(args[1:])
	default:
		return fmt.Errorf("unknown feature subcommand %q (new|list|show|update|done|reopen|rm)", args[0])
	}
}

// cmdFeatureShow prints a feature spec's raw markdown to stdout, resolved by
// slug fragment; -path prints the file path instead. The read half of editing
// a feature through the CLI (see cmdFeatureUpdate), so the store file is never
// opened by hand.
func cmdFeatureShow(args []string) error {
	fs := flag.NewFlagSet("feature show", flag.ContinueOnError)
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	pathOnly := fs.Bool("path", false, "print the feature's file path instead of its body")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman feature show [-p project] [-path] <slug>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	features, err := store.LoadFeatures(filepath.Dir(p.Dir))
	if err != nil {
		return err
	}
	f, err := findFeature(features, fs.Arg(0))
	if err != nil {
		return err
	}
	if *pathOnly {
		fmt.Println(f.Path())
		return nil
	}
	data, err := os.ReadFile(f.Path())
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// cmdFeatureUpdate edits a feature spec in place and commits it, so the store
// file is never hand-edited. -body replaces the whole spec, -append adds to
// the end, and -tasks rewrites the "Tasks:" line that links implementing
// tasks (comma/space-separated numbers; "" or "-" clears every link).
// -body/-append read stdin when given "-". A single-shot CLI holds the store
// lock for the whole read-modify-write, so no etag is needed. The title and
// slug stay immutable here, matching the web editor -- a slug rename ripples
// through deep links and chips and would be a separate step.
func cmdFeatureUpdate(args []string) error {
	fs := flag.NewFlagSet("feature update", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	body := fs.String("body", "", `replace the whole spec ("-" reads stdin)`)
	appendText := fs.String("append", "", `append text to the end of the spec ("-" reads stdin)`)
	tasks := fs.String("tasks", "", `set the linked task numbers, e.g. "12, 19" ("" or "-" clears)`)
	if err := fs.Parse(args); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman feature update [-p project] [-no-commit] (-body <md>|- | -append <md>|- | -tasks <nums>) <slug>")
	}
	if set["body"] && set["append"] {
		return fmt.Errorf("-body replaces and -append adds; pass one, not both")
	}
	if !set["body"] && !set["append"] && !set["tasks"] {
		return fmt.Errorf("nothing to update: pass -body, -append, and/or -tasks")
	}
	if set["body"] && strings.TrimSpace(*body) == "" && *body != "-" {
		return fmt.Errorf("-body is empty; a spec needs a body (use -append to add, or pass content)")
	}
	if set["body"] && *body == "-" {
		in, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(in)) == "" {
			return fmt.Errorf("stdin was empty; refusing to blank the spec")
		}
		*body = string(in)
	}
	if set["append"] && *appendText == "-" {
		in, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		*appendText = string(in)
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	features, err := store.LoadFeatures(filepath.Dir(p.Dir))
	if err != nil {
		return err
	}
	f, err := findFeature(features, fs.Arg(0))
	if err != nil {
		return err
	}
	if set["body"] {
		if err := f.SetBody(*body); err != nil {
			return err
		}
	}
	if set["append"] {
		if err := f.AppendRaw(*appendText); err != nil {
			return err
		}
	}
	if set["tasks"] {
		nums, err := parseTaskList(*tasks)
		if err != nil {
			return err
		}
		if _, err := f.SetTasks(nums); err != nil {
			return err
		}
	}
	fmt.Println(f.Path())
	p.commit(*noCommit, "edit feature "+f.Slug, f.Path())
	return nil
}

// parseTaskList parses a comma/space-separated list of task numbers for the
// -tasks flag; "-" and the empty string both mean "no links". A non-numeric
// entry is an error rather than silently dropped, so a typo is caught instead
// of quietly unlinking a task.
func parseTaskList(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return nil, nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	nums := make([]int, 0, len(fields))
	for _, fld := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(fld))
		if err != nil {
			return nil, fmt.Errorf("not a task number: %q", fld)
		}
		nums = append(nums, n)
	}
	return nums, nil
}

// cmdFeatureRm discards a feature spec (active or shipped) and commits the
// removal; linked tasks stay untouched, and the commit makes the discard
// undoable.
func cmdFeatureRm(args []string) error {
	fs := flag.NewFlagSet("feature rm", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman feature rm [-p project] [-no-commit] <slug>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	features, err := store.LoadFeatures(filepath.Dir(p.Dir))
	if err != nil {
		return err
	}
	f, err := findFeature(features, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := f.Remove(); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", f.File)
	p.commit(*noCommit, "remove feature "+f.Slug, f.Path())
	return nil
}

// cmdFeatureNew creates and commits a feature spec from the template.
func cmdFeatureNew(args []string) error {
	fs := flag.NewFlagSet("feature new", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	desc := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if desc == "" {
		return fmt.Errorf("usage: taskman feature new [-p project] [-no-commit] <description>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	f, err := store.NewFeature(filepath.Dir(p.Dir), desc, time.Now().Format("2006-01-02"))
	if err != nil {
		return err
	}
	fmt.Println(f.Path())
	p.commit(*noCommit, "feature "+f.Slug, f.Path())
	return nil
}

// cmdFeatureList prints the project's features with a done-task rollup
// computed against the ledger. Shipped features hide without -all.
func cmdFeatureList(args []string) error {
	fs := flag.NewFlagSet("feature list", flag.ContinueOnError)
	all := fs.Bool("all", false, "include shipped features")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	features, err := store.LoadFeatures(filepath.Dir(p.Dir))
	if err != nil {
		return err
	}
	byNum := map[int]task.Task{}
	for _, t := range p.Tasks {
		if t.HasNum {
			byNum[t.Num] = t
		}
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	shown := 0
	for _, f := range features {
		if f.Done && !*all {
			continue
		}
		shown++
		status := "active"
		if f.Done {
			status = "done"
		}
		rollup := "-"
		if len(f.Tasks) > 0 {
			done := 0
			for _, n := range f.Tasks {
				if t, ok := byNum[n]; ok && t.Status == task.Done {
					done++
				}
			}
			rollup = fmt.Sprintf("%d/%d tasks done", done, len(f.Tasks))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Slug, status, rollup, f.Title)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if shown == 0 {
		fmt.Printf("no active features in project %s\n", p.Name)
	}
	return nil
}

// cmdFeatureSetDone moves a feature between active and shipped (done, and
// its reverse reopen -- an accidental ship must be recoverable) and commits
// the rename.
func cmdFeatureSetDone(args []string, done bool) error {
	verb := "done"
	if !done {
		verb = "reopen"
	}
	fs := flag.NewFlagSet("feature "+verb, flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman feature %s [-p project] [-no-commit] <slug>", verb)
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	features, err := store.LoadFeatures(filepath.Dir(p.Dir))
	if err != nil {
		return err
	}
	f, err := findFeature(features, fs.Arg(0))
	if err != nil {
		return err
	}
	if f.Done == done {
		state := "already done"
		if !done {
			state = "not shipped"
		}
		return fmt.Errorf("%s is %s", f.File, state)
	}
	nf, err := f.SetDone(done)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", f.File, nf.File)
	p.commit(*noCommit, fmt.Sprintf("feature %s %s", verb, nf.Slug), f.Path(), nf.Path())
	return nil
}

// findFeature resolves a feature by exact slug or unique fragment, mirroring
// task.Find's refusal to guess.
func findFeature(features []store.Feature, key string) (store.Feature, error) {
	var hits []store.Feature
	for _, f := range features {
		if f.Slug == key {
			return f, nil
		}
		if strings.Contains(f.Slug, key) {
			hits = append(hits, f)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return store.Feature{}, fmt.Errorf("no feature matches %q", key)
	default:
		names := make([]string, len(hits))
		for i, f := range hits {
			names[i] = f.File
		}
		return store.Feature{}, fmt.Errorf("%q is ambiguous: %s", key, strings.Join(names, ", "))
	}
}
