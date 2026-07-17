package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/freeeve/taskman/internal/task"
)

// cmdShow prints a task's raw markdown body to stdout, resolved by number or
// slug fragment. It is the read half of editing a task through the CLI:
// pull the current content here, edit it, and write it back with `update` --
// so the store file is never touched (and its status-suffixed name never
// guessed) by hand. -path prints the file path instead, for the rare caller
// that does need it.
func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	pathOnly := fs.Bool("path", false, "print the task's file path instead of its body")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman show [-p project] [-path] <number|slug>")
	}
	p, err := openProject(*project)
	if err != nil {
		return err
	}
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	if *pathOnly {
		fmt.Println(t.Path())
		return nil
	}
	data, err := os.ReadFile(t.Path())
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// cmdUpdate edits a task's title and/or body in place and commits the change,
// so callers never edit the store file directly. It mirrors the web editor's
// PUT semantics: -body replaces the whole body (the H1 restamped if -title
// also changes it), -append adds to the end, -title restamps the H1 and
// renames the slug (keeping number, lane, status, deferral). -body and
// -append both read stdin when their value is "-", so a `show | edit | update
// -body -` round-trip needs no shell quoting. A single-shot CLI holds the
// store lock for the whole call, so no etag is needed: the read-modify-write
// cannot interleave with another writer the way an open browser tab can.
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	noCommit := fs.Bool("no-commit", false, "skip the git commit")
	project := fs.String("p", "", "project name (default: resolved from the current directory)")
	title := fs.String("title", "", "new title: restamps the H1 and renames the slug")
	body := fs.String("body", "", `replace the whole body ("-" reads stdin)`)
	appendText := fs.String("append", "", `append text to the end of the body ("-" reads stdin)`)
	if err := fs.Parse(args); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman update [-p project] [-no-commit] (-title <t> | -body <md>|- | -append <md>|-) <number|slug>")
	}
	if set["body"] && set["append"] {
		return fmt.Errorf("-body replaces and -append adds; pass one, not both")
	}
	if !set["title"] && !set["body"] && !set["append"] {
		return fmt.Errorf("nothing to update: pass -title, -body, and/or -append")
	}
	if set["body"] && strings.TrimSpace(*body) == "" && *body != "-" {
		return fmt.Errorf("-body is empty; a task needs a body (use -append to add, or pass content)")
	}
	if set["body"] && *body == "-" {
		in, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(in)) == "" {
			return fmt.Errorf("stdin was empty; refusing to blank the task body")
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
	t, err := task.Find(p.Tasks, fs.Arg(0))
	if err != nil {
		return err
	}
	if set["body"] {
		if err := task.SetBody(t.Path(), *body); err != nil {
			return err
		}
	}
	if set["append"] {
		if err := task.AppendRaw(t.Path(), *appendText); err != nil {
			return err
		}
	}
	paths := []string{t.Path()}
	if set["title"] {
		nt, err := t.Retitle(strings.TrimSpace(*title))
		if err != nil {
			return err
		}
		if nt.File != t.File {
			fmt.Printf("%s -> %s\n", t.File, nt.File)
		}
		paths = append(paths, nt.Path())
		t = nt
	}
	p.commit(*noCommit, fmt.Sprintf("update %s", t.Stem()), paths...)
	return nil
}
