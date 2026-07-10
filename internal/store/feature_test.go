package store

import (
	"os"
	"path/filepath"
	"testing"
)

// featureFile writes a features/ file into a temp project dir.
func featureFile(t *testing.T, projDir, name, body string) {
	t.Helper()
	dir := FeaturesDir(projDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFeatures(t *testing.T) {
	projDir := t.TempDir()

	// No features/ at all is fine.
	if fs, err := LoadFeatures(projDir); err != nil || fs != nil {
		t.Errorf("missing dir: %v, %v", fs, err)
	}

	featureFile(t, projDir, "kanban-board.md",
		"# Kanban board\n\nTasks: 012, 019 034\n\nDrag cards around.\n")
	featureFile(t, projDir, "shipped-thing.done.md",
		"# Shipped\n\nTasks:\n")
	featureFile(t, projDir, "untitled.md", "no heading here\nTasks: junk, 7\n")
	featureFile(t, projDir, ".hidden.md", "# no\n")
	featureFile(t, projDir, "notes.txt", "# not markdown\n")

	features, err := LoadFeatures(projDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(features) != 3 {
		t.Fatalf("features = %+v, want 3 (dotfiles and non-md skipped)", features)
	}
	byslug := map[string]Feature{}
	for _, f := range features {
		byslug[f.Slug] = f
	}
	kb := byslug["kanban-board"]
	if kb.Title != "Kanban board" || kb.Done ||
		len(kb.Tasks) != 3 || kb.Tasks[0] != 12 || kb.Tasks[1] != 19 || kb.Tasks[2] != 34 {
		t.Errorf("kanban = %+v", kb)
	}
	if sh := byslug["shipped-thing"]; !sh.Done || len(sh.Tasks) != 0 {
		t.Errorf("shipped = %+v", sh)
	}
	// A missing H1 falls back to the slug; a garbage Tasks: line yields what
	// it can.
	if un := byslug["untitled"]; un.Title != "untitled" || len(un.Tasks) != 1 || un.Tasks[0] != 7 {
		t.Errorf("untitled = %+v", un)
	}

	// SetDone renames both ways.
	nf, err := kb.SetDone(true)
	if err != nil || nf.File != "kanban-board.done.md" {
		t.Fatalf("SetDone: %v %+v", err, nf)
	}
	if back, err := nf.SetDone(false); err != nil || back.File != "kanban-board.md" {
		t.Fatalf("SetDone(false): %v %+v", err, back)
	}
}

// TestNewFeatureDuplicate pins the clean conflict error: no OS error text,
// no absolute store path.
func TestNewFeatureDuplicate(t *testing.T) {
	projDir := t.TempDir()
	if _, err := NewFeature(projDir, "Search everything", "2026-07-10"); err != nil {
		t.Fatal(err)
	}
	_, err := NewFeature(projDir, "Search everything", "2026-07-10")
	if err == nil {
		t.Fatal("duplicate must error")
	}
	if err.Error() != `feature "search-everything" already exists` {
		t.Errorf("error = %q (must not leak the path)", err)
	}
}

// TestShippedFeatureOwnsItsSlug pins the data-loss guard: a shipped feature
// blocks re-creation of its slug, and SetDone never renames onto an
// existing file.
func TestShippedFeatureOwnsItsSlug(t *testing.T) {
	projDir := t.TempDir()
	f, err := NewFeature(projDir, "Deep ship", "2026-07-10")
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := f.SetDone(true)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(shipped.Path())
	if err != nil {
		t.Fatal(err)
	}

	// Re-creating the same description is refused while the shipped file owns
	// the slug.
	if _, err := NewFeature(projDir, "Deep ship", "2026-07-10"); err == nil ||
		err.Error() != `feature "deep-ship" already exists (shipped)` {
		t.Errorf("recreate after ship = %v", err)
	}

	// Even with a same-slug pair forced onto disk, SetDone refuses to clobber.
	dupe := Feature{Dir: FeaturesDir(projDir), File: "deep-ship.md", Slug: "deep-ship"}
	if err := os.WriteFile(dupe.Path(), []byte("# Impostor\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := dupe.SetDone(true); err == nil {
		t.Fatal("SetDone onto an existing target must refuse")
	}
	after, err := os.ReadFile(shipped.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Error("shipped feature body was clobbered")
	}
}

// FuzzParseTaskNums pins leniency: any Tasks: payload parses without panic
// into unique positive numbers.
func FuzzParseTaskNums(f *testing.F) {
	f.Add("012, 019 034")
	f.Add("junk, 7, 7, -3, 0")
	f.Add("99999999999999999999999")
	f.Fuzz(func(t *testing.T, s string) {
		seen := map[int]bool{}
		for _, n := range parseTaskNums(s) {
			if n <= 0 {
				t.Errorf("non-positive %d from %q", n, s)
			}
			if seen[n] {
				t.Errorf("duplicate %d from %q", n, s)
			}
			seen[n] = true
		}
	})
}
