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
	case "done":
		return cmdFeatureDone(args[1:])
	default:
		return fmt.Errorf("unknown feature subcommand %q (new|list|done)", args[0])
	}
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

// cmdFeatureDone marks a feature shipped and commits the rename.
func cmdFeatureDone(args []string) error {
	fs := flag.NewFlagSet("feature done", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman feature done [-p project] [-no-commit] <slug>")
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
	if f.Done {
		return fmt.Errorf("%s is already done", f.File)
	}
	nf, err := f.SetDone(true)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", f.File, nf.File)
	p.commit(*noCommit, "feature done "+nf.Slug, f.Path(), nf.Path())
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
