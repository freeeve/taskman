package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGaps(t *testing.T) {
	dir := ledger(t, "001_one.md", "004_four.md", "007_seven.done.md")
	tasks, _ := Load(dir)
	gaps := Gaps(tasks)
	if len(gaps) != 4 || gaps[0] != 2 || gaps[1] != 3 || gaps[2] != 5 || gaps[3] != 6 {
		t.Errorf("gaps = %v, want [2 3 5 6]", gaps)
	}
	if g := Gaps(nil); len(g) != 0 {
		t.Errorf("empty ledger gaps = %v", g)
	}
}

// TestPlanRepairs pins the repair policy: the most advanced duplicate keeps
// its number, losers take the lowest free slots (gaps first), deterministic
// across runs.
func TestPlanRepairs(t *testing.T) {
	dir := ledger(t,
		"001_one.md",
		"003_alpha.md", "003_beta.in-progress.md", "003_gamma.md",
		"005_five.md", "005_worked.done.md")
	tasks, _ := Load(dir)
	plan := PlanRepairs(tasks)
	if len(plan) != 3 {
		t.Fatalf("plan = %+v, want 3 repairs", plan)
	}
	// 003: beta (in-progress) keeps it; alpha then gamma move to 002, 004.
	// 005: worked (done) keeps it; five moves to 006.
	got := map[string]int{}
	for _, r := range plan {
		got[r.T.Slug] = r.Num
	}
	want := map[string]int{"alpha": 2, "gamma": 4, "five": 6}
	for slug, num := range want {
		if got[slug] != num {
			t.Errorf("repair[%s] = %d, want %d (full plan %v)", slug, got[slug], num, got)
		}
	}
}

func TestPlanRepairsNoDups(t *testing.T) {
	dir := ledger(t, "001_one.md", "003_three.md")
	tasks, _ := Load(dir)
	if plan := PlanRepairs(tasks); len(plan) != 0 {
		t.Errorf("plan = %+v, want none", plan)
	}
}

// TestRenumberTitleSeparators pins the H1 restamp across the separator
// variants found in real ledgers: an existing number is replaced whatever
// dash spelled it, while date-led and unnumbered titles gain a prefix.
func TestRenumberTitleSeparators(t *testing.T) {
	cases := map[string]string{
		"# 012 -- Old double dash": "# 027 -- Old double dash",
		"# 012 — Em dash task":     "# 027 -- Em dash task",
		"# 012 – En dash task":     "# 027 -- En dash task",
		"# 012 - Spaced hyphen":    "# 027 -- Spaced hyphen",
		"# 2026-07 report":         "# 027 -- 2026-07 report",
		"# Unnumbered title":       "# 027 -- Unnumbered title",
	}
	for in, want := range cases {
		path := filepath.Join(t.TempDir(), "t.md")
		if err := os.WriteFile(path, []byte(in+"\n\nBody.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := renumberTitle(path, 27, ""); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		got := strings.SplitN(string(data), "\n", 2)[0]
		if got != want {
			t.Errorf("renumberTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenumberRestampsTitle(t *testing.T) {
	dir := ledger(t, "007_seven.md")
	path := filepath.Join(dir, "003_movable.in-progress.md")
	if err := os.WriteFile(path, []byte("# 003 -- Movable task\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, _ := Load(dir)
	tk, err := Find(tasks, "movable")
	if err != nil {
		t.Fatal(err)
	}
	nt, err := tk.Renumber(8)
	if err != nil {
		t.Fatal(err)
	}
	if nt.File != "008_movable.in-progress.md" {
		t.Errorf("renumbered file = %q", nt.File)
	}
	data, _ := os.ReadFile(nt.Path())
	if !strings.HasPrefix(string(data), "# 008 -- Movable task\n") {
		t.Errorf("title not restamped: %q", strings.SplitN(string(data), "\n", 2)[0])
	}
	if strings.Contains(string(data), "Adopted") {
		t.Error("renumber must not add an adoption breadcrumb")
	}
	if _, err := nt.Renumber(9); err == nil {
		// Renumbering again is legal; verify it did not error for HasNum.
	}
	ask := Task{Dir: dir, File: "qbd_x.md", Prefix: "qbd", Slug: "x"}
	if _, err := ask.Renumber(9); err == nil {
		t.Error("unnumbered ask must refuse Renumber")
	}
}
