package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// ledger builds a tasks/ dir inside a fake repo and returns its path.
func ledger(t *testing.T, names ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "myrepo", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("# "+n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestParseName(t *testing.T) {
	cases := []struct {
		name   string
		ok     bool
		num    int
		hasNum bool
		prefix string
		slug   string
		status Status
	}{
		{"001_first-thing.md", true, 1, true, "", "first-thing", Pending},
		{"012_loc-sru-ingest.in-progress.md", true, 12, true, "", "loc-sru-ingest", InProgress},
		{"025_full-corpus.done.md", true, 25, true, "", "full-corpus", Done},
		{"qbd_spotlight-noindex.md", true, 0, false, "qbd", "spotlight-noindex", Pending},
		{"qbd_ask.done.md", true, 0, false, "qbd", "ask", Done},
		{"README.md", false, 0, false, "", "", Pending},
		{"notes.txt", false, 0, false, "", "", Pending},
		{".hidden_thing.md", false, 0, false, "", "", Pending},
		{"_leading-sep.md", false, 0, false, "", "", Pending},
		{"trailing_.md", false, 0, false, "", "", Pending},
	}
	for _, c := range cases {
		task, ok := parseName("tasks", c.name)
		if ok != c.ok {
			t.Errorf("parseName(%q) ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if task.Num != c.num || task.HasNum != c.hasNum || task.Prefix != c.prefix ||
			task.Slug != c.slug || task.Status != c.status {
			t.Errorf("parseName(%q) = %+v", c.name, task)
		}
	}
}

func TestLoadOrderAndDups(t *testing.T) {
	dir := ledger(t,
		"012_second-twelve.md", "002_two.done.md", "012_first-twelve.in-progress.md",
		"qbd_ask.md", "001_one.md", "README.md")
	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var stems []string
	for _, task := range tasks {
		stems = append(stems, task.Stem())
	}
	want := []string{"001_one", "002_two", "012_first-twelve", "012_second-twelve", "qbd_ask"}
	if strings.Join(stems, " ") != strings.Join(want, " ") {
		t.Errorf("order = %v, want %v", stems, want)
	}
	if dups := Dups(tasks); len(dups) != 1 || !dups[12] {
		t.Errorf("dups = %v, want {12}", dups)
	}
	if n := NextNum(tasks); n != 13 {
		t.Errorf("next = %d, want 13", n)
	}
}

func TestFindTasksDirWalksUp(t *testing.T) {
	dir := ledger(t, "001_one.md")
	nested := filepath.Join(filepath.Dir(dir), "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindTasksDir(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("FindTasksDir = %q, want %q", got, dir)
	}
	if _, err := FindTasksDir(t.TempDir()); err == nil {
		t.Error("want error when no tasks/ exists upward")
	}
}

func TestFind(t *testing.T) {
	dir := ledger(t, "001_alpha.md", "012_beta.md", "012_gamma.md", "qbd_ask.md")
	tasks, _ := Load(dir)
	if task, err := Find(tasks, "1"); err != nil || task.Slug != "alpha" {
		t.Errorf("Find(1) = %+v, %v", task, err)
	}
	if _, err := Find(tasks, "12"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("Find(12) must be ambiguous: %v", err)
	}
	if task, err := Find(tasks, "gam"); err != nil || task.Slug != "gamma" {
		t.Errorf("Find(gam) = %+v, %v", task, err)
	}
	if _, err := Find(tasks, "nope"); err == nil {
		t.Error("Find(nope) must fail")
	}
	if task, err := Find(tasks, "qbd_ask"); err != nil || task.Prefix != "qbd" {
		t.Errorf("Find(qbd_ask) = %+v, %v", task, err)
	}
}

func TestSetStatusLifecycle(t *testing.T) {
	dir := ledger(t, "001_alpha.md")
	tasks, _ := Load(dir)
	task := tasks[0]
	task, err := task.SetStatus(InProgress)
	if err != nil || task.File != "001_alpha.in-progress.md" {
		t.Fatalf("start: %v %+v", err, task)
	}
	task, err = task.SetStatus(Done)
	if err != nil || task.File != "001_alpha.done.md" {
		t.Fatalf("done: %v %+v", err, task)
	}
	if _, err := task.SetStatus(Done); err == nil {
		t.Error("re-done must error")
	}
	task, err = task.SetStatus(Pending)
	if err != nil || task.File != "001_alpha.md" {
		t.Fatalf("reopen: %v %+v", err, task)
	}
	if _, err := os.Stat(task.Path()); err != nil {
		t.Errorf("final file missing: %v", err)
	}
}

func TestAdopt(t *testing.T) {
	dir := ledger(t, "007_seven.md")
	askPath := filepath.Join(dir, "qbd_do-the-thing.in-progress.md")
	if err := os.WriteFile(askPath, []byte("# Do the thing\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, _ := Load(dir)
	ask, err := Find(tasks, "qbd_do-the-thing")
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := ask.Adopt(NextNum(tasks))
	if err != nil {
		t.Fatal(err)
	}
	if adopted.File != "008_do-the-thing.in-progress.md" {
		t.Errorf("adopted file = %q", adopted.File)
	}
	data, err := os.ReadFile(adopted.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# 008 -- Do the thing\n") {
		t.Errorf("title not renumbered: %q", strings.SplitN(string(data), "\n", 2)[0])
	}
	if !strings.Contains(string(data), "qbd_do-the-thing.md") {
		t.Errorf("filed-as breadcrumb missing:\n%s", data)
	}
	// A numbered task refuses adoption; an already-numbered title is kept.
	tasks, _ = Load(dir)
	seven, _ := Find(tasks, "7")
	if _, err := seven.Adopt(9); err == nil {
		t.Error("numbered task must refuse Adopt")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Full-corpus NQ export!":      "full-corpus-nq-export",
		"  spaces   and__underscores": "spaces-and-underscores",
		"CamelCase123":                "camelcase123",
		"---":                         "",
		"":                            "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCommands drives the CLI surface end to end in a temp ledger.
func TestCommands(t *testing.T) {
	dir := ledger(t, "001_alpha.done.md", "002_beta.md")
	repo := filepath.Dir(dir)
	t.Chdir(repo)

	if err := run([]string{"new", "Try the CLI"}); err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "003_try-the-cli.md")); err != nil {
		t.Fatalf("new file: %v", err)
	}
	if err := run([]string{"start", "3"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := run([]string{"done", "3"}); err != nil {
		t.Fatalf("done: %v", err)
	}
	if err := run([]string{"reopen", "3"}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := run([]string{"next"}); err != nil {
		t.Fatalf("next: %v", err)
	}
	if err := run([]string{"list", "-all"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := run([]string{"bogus"}); err == nil {
		t.Error("bogus command must error")
	}

	// File an ask into a second repo: it lands at THAT ledger's next number
	// (the immediate commit makes the claim safe), body crediting the filer.
	other := filepath.Join(t.TempDir(), "otherrepo")
	otherTasks := filepath.Join(other, "tasks")
	if err := os.MkdirAll(otherTasks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherTasks, "004_existing.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"file", other, "Please fix the flux capacitor"}); err != nil {
		t.Fatalf("file: %v", err)
	}
	ask := filepath.Join(otherTasks, "005_please-fix-the-flux-capacitor.md")
	data, err := os.ReadFile(ask)
	if err != nil {
		t.Fatalf("filed ask: %v", err)
	}
	if !strings.HasPrefix(string(data), "# 005 -- Please fix the flux capacitor\n") ||
		!strings.Contains(string(data), "Filed from myrepo") {
		t.Errorf("ask body:\n%s", data)
	}
	if err := run([]string{"file", other, "Please fix the flux capacitor"}); err == nil {
		t.Error("re-filing the same ask must refuse to overwrite")
	}
	// Legacy prefixed asks still adopt.
	legacy := filepath.Join(otherTasks, "qbd_old-style-ask.md")
	if err := os.WriteFile(legacy, []byte("# Old style ask\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(other)
	if err := run([]string{"adopt", "qbd_old-style-ask"}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(otherTasks, "006_old-style-ask.md")); err != nil {
		t.Fatalf("adopted file: %v", err)
	}
}

func FuzzSlugify(f *testing.F) {
	f.Add("Full-corpus NQ export!")
	f.Add("---")
	f.Add("Ünïcodé Näme 42")
	f.Fuzz(func(t *testing.T, in string) {
		s := Slugify(in)
		if s != strings.Trim(s, "-") || strings.Contains(s, "--") {
			t.Errorf("Slugify(%q) = %q has stray dashes", in, s)
		}
		for _, r := range s {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				t.Errorf("Slugify(%q) = %q has invalid rune %q", in, s, r)
			}
		}
	})
}

func FuzzParseName(f *testing.F) {
	f.Add("001_first.md")
	f.Add("qbd_ask.done.md")
	f.Add("_x.md")
	f.Add("weird..md")
	f.Fuzz(func(t *testing.T, name string) {
		task, ok := parseName("tasks", name)
		if !ok {
			return
		}
		if task.Slug == "" {
			t.Errorf("parseName(%q) accepted an empty slug", name)
		}
		if !task.HasNum && task.Prefix == "" {
			t.Errorf("parseName(%q) accepted neither number nor prefix", name)
		}
		if !utf8.ValidString(name) {
			return
		}
		// A parsed task's reconstructed filename must parse identically.
		round := task.Stem() + task.Status.suffix() + ".md"
		task2, ok2 := parseName("tasks", round)
		if !ok2 || task2.Stem() != task.Stem() || task2.Status != task.Status {
			t.Errorf("roundtrip %q -> %q -> %+v (%v)", name, round, task2, ok2)
		}
	})
}
