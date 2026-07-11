package task

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
		name     string
		ok       bool
		num      int
		hasNum   bool
		prefix   string
		lane     string
		slug     string
		status   Status
		deferred bool
	}{
		{"001_first-thing.md", true, 1, true, "", "", "first-thing", Pending, false},
		{"012_loc-sru-ingest.in-progress.md", true, 12, true, "", "", "loc-sru-ingest", InProgress, false},
		{"025_full-corpus.done.md", true, 25, true, "", "", "full-corpus", Done, false},
		{"247_ghcr-publish.deferred.md", true, 247, true, "", "", "ghcr-publish", Pending, true},
		{"031_paused.in-progress.deferred.md", true, 31, true, "", "", "paused", InProgress, true},
		{"012-impl_fix-thing.md", true, 12, true, "", "impl", "fix-thing", Pending, false},
		{"012-impl_fix-thing.in-progress.md", true, 12, true, "", "impl", "fix-thing", InProgress, false},
		{"031-e2e_checkout-flow.in-progress.deferred.md", true, 31, true, "", "e2e", "checkout-flow", InProgress, true},
		{"012-3_numeric-lane.md", true, 12, true, "", "3", "numeric-lane", Pending, false},
		{"012-ui-web_dashed-lane.md", true, 12, true, "", "ui-web", "dashed-lane", Pending, false},
		{"qbd_spotlight-noindex.md", true, 0, false, "qbd", "", "spotlight-noindex", Pending, false},
		{"qbd-impl_prefixed-not-laned.md", true, 0, false, "qbd-impl", "", "prefixed-not-laned", Pending, false},
		{"qbd_ask.done.md", true, 0, false, "qbd", "", "ask", Done, false},
		{"qbd_ask.deferred.md", true, 0, false, "qbd", "", "ask", Pending, true},
		{"README.md", false, 0, false, "", "", "", Pending, false},
		{"notes.txt", false, 0, false, "", "", "", Pending, false},
		{".hidden_thing.md", false, 0, false, "", "", "", Pending, false},
		{"_leading-sep.md", false, 0, false, "", "", "", Pending, false},
		{"trailing_.md", false, 0, false, "", "", "", Pending, false},
	}
	for _, c := range cases {
		tk, ok := Parse("tasks", c.name)
		if ok != c.ok {
			t.Errorf("Parse(%q) ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if tk.Num != c.num || tk.HasNum != c.hasNum || tk.Prefix != c.prefix ||
			tk.Lane != c.lane || tk.Slug != c.slug || tk.Status != c.status || tk.Deferred != c.deferred {
			t.Errorf("Parse(%q) = %+v", c.name, tk)
		}
		if tk.Name() != c.name {
			t.Errorf("Parse(%q).Name() = %q", c.name, tk.Name())
		}
	}
}

// TestLaneLifecycle pins the lane's free ride through every rename path:
// status moves, deferral round trips, renumbering, and SetLane itself.
func TestLaneLifecycle(t *testing.T) {
	dir := ledger(t, "012-impl_fix-thing.md", "007_unrouted.md")
	tasks, _ := Load(dir)

	tk, err := Find(tasks, "12")
	if err != nil || tk.Lane != "impl" {
		t.Fatalf("Find(12) = %+v, %v", tk, err)
	}
	if tk, err = tk.SetStatus(InProgress); err != nil || tk.File != "012-impl_fix-thing.in-progress.md" {
		t.Fatalf("start: %v %+v", err, tk)
	}
	if tk, err = tk.Defer("waiting", "2026-07-10"); err != nil || tk.File != "012-impl_fix-thing.in-progress.deferred.md" {
		t.Fatalf("defer: %v %+v", err, tk)
	}
	if tk, err = tk.Resume("2026-07-10"); err != nil || tk.File != "012-impl_fix-thing.in-progress.md" {
		t.Fatalf("resume: %v %+v", err, tk)
	}
	if tk, err = tk.SetStatus(Done); err != nil || tk.File != "012-impl_fix-thing.done.md" {
		t.Fatalf("done: %v %+v", err, tk)
	}
	if tk, err = tk.Renumber(15); err != nil || tk.File != "015-impl_fix-thing.done.md" {
		t.Fatalf("renumber: %v %+v", err, tk)
	}
	if tk.Lane != "impl" {
		t.Errorf("lane lost along the lifecycle: %+v", tk)
	}

	// SetLane moves a task between lanes and clears with "".
	tasks, _ = Load(dir)
	un, _ := Find(tasks, "7")
	un, err = un.SetLane("e2e")
	if err != nil || un.File != "007-e2e_unrouted.md" {
		t.Fatalf("SetLane: %v %+v", err, un)
	}
	if _, err := un.SetLane("e2e"); err == nil {
		t.Error("same lane must error")
	}
	if un, err = un.SetLane(""); err != nil || un.File != "007_unrouted.md" {
		t.Fatalf("clear lane: %v %+v", err, un)
	}
	ask := Task{Dir: dir, File: "qbd_x.md", Prefix: "qbd", Slug: "x"}
	if _, err := ask.SetLane("impl"); err == nil {
		t.Error("unadopted ask must refuse SetLane")
	}

	// Find narrows by lane fragment because the lane sits in the stem.
	tasks, _ = Load(dir)
	if tk, err := Find(tasks, "impl"); err != nil || tk.Num != 15 {
		t.Errorf("Find(impl) = %+v, %v", tk, err)
	}
}

// TestDeferLifecycle covers the flag's orthogonality: a deferral rides on top
// of whatever status the task holds, resume restores that status, and any
// explicit lifecycle move clears the deferral.
func TestDeferLifecycle(t *testing.T) {
	dir := ledger(t, "001_alpha.md", "002_beta.in-progress.md", "003_gamma.done.md")
	tasks, _ := Load(dir)

	alpha, _ := Find(tasks, "1")
	alpha, err := alpha.Defer("maintainer's call: outward-facing publish", "2026-07-09")
	if err != nil || alpha.File != "001_alpha.deferred.md" {
		t.Fatalf("defer: %v %+v", err, alpha)
	}
	if alpha.Status != Pending || !alpha.Deferred {
		t.Errorf("defer must keep status and set the flag: %+v", alpha)
	}
	if alpha.StatusLabel() != "deferred" {
		t.Errorf("label = %q", alpha.StatusLabel())
	}
	body, err := os.ReadFile(alpha.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "## Deferred 2026-07-09") ||
		!strings.Contains(string(body), "outward-facing publish") {
		t.Errorf("reason not recorded:\n%s", body)
	}
	if _, err := alpha.Defer("again", "2026-07-10"); err == nil {
		t.Error("re-deferring must error")
	}
	resumed, err := alpha.Resume("2026-07-10")
	if err != nil || resumed.File != "001_alpha.md" || resumed.Deferred {
		t.Fatalf("resume: %v %+v", err, resumed)
	}
	if body, _ := os.ReadFile(resumed.Path()); !strings.Contains(string(body), "## Resumed 2026-07-10") {
		t.Errorf("resume not recorded:\n%s", body)
	}
	if _, err := resumed.Resume("2026-07-11"); err == nil {
		t.Error("resuming an undeferred task must error")
	}

	// Deferral rides on in-progress, and resume restores it.
	beta, _ := Find(tasks, "2")
	beta, err = beta.Defer("blocked on upstream", "2026-07-09")
	if err != nil || beta.File != "002_beta.in-progress.deferred.md" {
		t.Fatalf("defer in-progress: %v %+v", err, beta)
	}
	if beta.StatusLabel() != "in-progress/deferred" {
		t.Errorf("label = %q", beta.StatusLabel())
	}
	if beta, err = beta.Resume("2026-07-10"); err != nil || beta.File != "002_beta.in-progress.md" {
		t.Fatalf("resume in-progress: %v %+v", err, beta)
	}

	// A done task has no pending decision to wait on.
	gamma, _ := Find(tasks, "3")
	if _, err := gamma.Defer("why", "2026-07-09"); err == nil {
		t.Error("deferring a done task must error")
	}

	// Any lifecycle move clears the deferral.
	held, err := beta.Defer("held again", "2026-07-11")
	if err != nil {
		t.Fatal(err)
	}
	started, err := held.SetStatus(InProgress)
	if err != nil || started.File != "002_beta.in-progress.md" || started.Deferred {
		t.Fatalf("start must clear deferral: %v %+v", err, started)
	}
}

// TestDeferredNumberContest pins the reason deferral is a flag: it must not
// influence which task keeps a contested number.
func TestDeferredNumberContest(t *testing.T) {
	dir := ledger(t, "005_first.deferred.md", "005_second.in-progress.md")
	tasks, _ := Load(dir)
	plan := PlanRepairs(tasks)
	if len(plan) != 1 {
		t.Fatalf("plan = %+v, want one move", plan)
	}
	if plan[0].T.Slug != "first" {
		t.Errorf("in-progress must outrank deferred-pending for the number; moved %q", plan[0].T.Slug)
	}
	moved, err := plan[0].T.Renumber(plan[0].Num)
	if err != nil {
		t.Fatal(err)
	}
	if moved.File != "001_first.deferred.md" {
		t.Errorf("renumber must preserve the deferral marker: %q", moved.File)
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
	for _, tk := range tasks {
		stems = append(stems, tk.Stem())
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

func TestFind(t *testing.T) {
	dir := ledger(t, "001_alpha.md", "012_beta.md", "012_gamma.md", "qbd_ask.md")
	tasks, _ := Load(dir)
	if tk, err := Find(tasks, "1"); err != nil || tk.Slug != "alpha" {
		t.Errorf("Find(1) = %+v, %v", tk, err)
	}
	if _, err := Find(tasks, "12"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("Find(12) must be ambiguous: %v", err)
	}
	if tk, err := Find(tasks, "gam"); err != nil || tk.Slug != "gamma" {
		t.Errorf("Find(gam) = %+v, %v", tk, err)
	}
	if _, err := Find(tasks, "nope"); err == nil {
		t.Error("Find(nope) must fail")
	}
	if tk, err := Find(tasks, "qbd_ask"); err != nil || tk.Prefix != "qbd" {
		t.Errorf("Find(qbd_ask) = %+v, %v", tk, err)
	}
}

func TestSetStatusLifecycle(t *testing.T) {
	dir := ledger(t, "001_alpha.md")
	tasks, _ := Load(dir)
	tk := tasks[0]
	tk, err := tk.SetStatus(InProgress)
	if err != nil || tk.File != "001_alpha.in-progress.md" {
		t.Fatalf("start: %v %+v", err, tk)
	}
	tk, err = tk.SetStatus(Done)
	if err != nil || tk.File != "001_alpha.done.md" {
		t.Fatalf("done: %v %+v", err, tk)
	}
	if _, err := tk.SetStatus(Done); err == nil {
		t.Error("re-done must error")
	}
	tk, err = tk.SetStatus(Pending)
	if err != nil || tk.File != "001_alpha.md" {
		t.Fatalf("reopen: %v %+v", err, tk)
	}
	if _, err := os.Stat(tk.Path()); err != nil {
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

// TestRetitle pins editing: H1 restamped to the numbered form, file renamed
// to the new slug with lane/status/deferral kept, same-slug edits restamp
// only, and an existing target refuses rather than clobbers.
func TestRetitle(t *testing.T) {
	dir := ledger(t, "007_taken.md")
	path := filepath.Join(dir, "012-impl_old-name.in-progress.md")
	if err := os.WriteFile(path, []byte("# 012 -- Old name\n\nBody stays.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, _ := Load(dir)
	tk, err := Find(tasks, "12")
	if err != nil {
		t.Fatal(err)
	}
	nt, err := tk.Retitle("Shiny new name")
	if err != nil || nt.File != "012-impl_shiny-new-name.in-progress.md" {
		t.Fatalf("retitle: %v %+v", err, nt)
	}
	data, _ := os.ReadFile(nt.Path())
	if !strings.HasPrefix(string(data), "# 012 -- Shiny new name\n") ||
		!strings.Contains(string(data), "Body stays.") {
		t.Errorf("body after retitle:\n%s", data)
	}
	// Same slug: restamp only, no rename.
	nt2, err := nt.Retitle("Shiny NEW name")
	if err != nil || nt2.File != nt.File {
		t.Fatalf("same-slug retitle: %v %+v", err, nt2)
	}
	data, _ = os.ReadFile(nt2.Path())
	if !strings.HasPrefix(string(data), "# 012 -- Shiny NEW name\n") {
		t.Errorf("H1 not restamped: %s", data)
	}
	// Clobber refusal: 007_taken.md exists.
	seven := Task{Dir: dir, File: "007_taken.md", Num: 7, HasNum: true, Slug: "taken"}
	other := Task{Dir: dir, File: "012-impl_shiny-new-name.in-progress.md", Num: 12, HasNum: true,
		Lane: "impl", Slug: "shiny-new-name", Status: InProgress}
	_ = seven
	if _, err := other.SetLane(""); err != nil {
		t.Fatal(err)
	}
	tasks, _ = Load(dir)
	tw, _ := Find(tasks, "7")
	if _, err := tw.Retitle("!!!"); err == nil {
		t.Error("empty slug must be refused")
	}
	renamed, _ := Find(tasks, "12")
	if err := os.WriteFile(filepath.Join(dir, "012_taken.in-progress.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := renamed.Retitle("Taken"); err == nil ||
		!strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("clobber refusal = %v", err)
	}
	ask := Task{Dir: dir, File: "qbd_x.md", Prefix: "qbd", Slug: "x"}
	if _, err := ask.Retitle("New"); err == nil {
		t.Error("unadopted ask must refuse Retitle")
	}
}

// TestCheckSlug pins up-front length validation: creation must fail with a
// clean message before the filesystem gets to reject the name.
func TestCheckSlug(t *testing.T) {
	if err := CheckSlug(""); err == nil {
		t.Error("empty slug must be rejected")
	}
	if err := CheckSlug(strings.Repeat("a", MaxSlugLen)); err != nil {
		t.Errorf("slug at the limit must pass: %v", err)
	}
	if err := CheckSlug(strings.Repeat("a", MaxSlugLen+1)); err == nil ||
		!strings.Contains(err.Error(), "too long") {
		t.Errorf("over-long slug error = %v", err)
	}

	dir := ledger(t)
	if _, err := New(dir, nil, strings.Repeat("a", 300), "", "2026-07-10"); err == nil ||
		!strings.Contains(err.Error(), "too long") || strings.Contains(err.Error(), dir) {
		t.Errorf("New over-long = %v (must be clean, no path)", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Error("no file may be created for a rejected description")
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
	f.Add("003_held.deferred.md")
	f.Add("004_held.in-progress.deferred.md")
	f.Add("012-impl_fix-thing.md")
	f.Add("012-3_numeric-lane.in-progress.md")
	f.Add("qbd-impl_prefixed.md")
	f.Add("_x.md")
	f.Add("weird..md")
	f.Fuzz(func(t *testing.T, name string) {
		tk, ok := Parse("tasks", name)
		if !ok {
			return
		}
		if tk.Slug == "" {
			t.Errorf("Parse(%q) accepted an empty slug", name)
		}
		if !tk.HasNum && tk.Prefix == "" {
			t.Errorf("Parse(%q) accepted neither number nor prefix", name)
		}
		if !utf8.ValidString(name) {
			return
		}
		// A parsed task's reconstructed filename must parse identically.
		round := tk.Name()
		tk2, ok2 := Parse("tasks", round)
		if !ok2 || tk2.Stem() != tk.Stem() || tk2.Status != tk.Status ||
			tk2.Deferred != tk.Deferred {
			t.Errorf("roundtrip %q -> %q -> %+v (%v)", name, round, tk2, ok2)
		}
	})
}
