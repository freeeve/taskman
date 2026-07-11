package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// searchHome seeds a two-project store with distinctive tokens.
func searchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	files := map[string]string{
		"alpha/tasks/001_flux.md":       "# 001 -- Fix the flux capacitor\n\nNeeds 1.21 gigawatts.\n",
		"alpha/tasks/002_other.done.md": "# 002 -- Other thing\n\nNothing relevant.\n",
		"beta/tasks/001-e2e_dash.md":    "# 001 -- Dashboard\n\nThe flux readings dashboard.\n",
		"beta/features/telemetry.md":    "# Telemetry\n\nTasks: 001\n\nCollect FLUX telemetry.\n",
	}
	for name, body := range files {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestSearchIndex(t *testing.T) {
	ix, err := BuildIndex(searchHome(t))
	if err != nil {
		t.Fatal(err)
	}

	// Case-insensitive, cross-project, tasks and features alike.
	hits := ix.Search("flux", 10)
	if len(hits) != 3 {
		t.Fatalf("flux hits = %+v", hits)
	}
	// Title match outranks body matches.
	if hits[0].Project != "alpha" || hits[0].Num != 1 {
		t.Errorf("title boost: first hit = %+v", hits[0])
	}
	projects := map[string]bool{}
	for _, h := range hits {
		projects[h.Project] = true
	}
	if !projects["alpha"] || !projects["beta"] {
		t.Errorf("hits must span projects: %+v", hits)
	}

	// Multi-term AND.
	if hits := ix.Search("flux capacitor", 10); len(hits) != 1 || hits[0].Slug != "flux" {
		t.Errorf("AND hits = %+v", hits)
	}
	// The final term matches as a prefix (search-as-you-type).
	if hits := ix.Search("telem", 10); len(hits) != 1 || hits[0].Kind != "feature" ||
		hits[0].Slug != "telemetry" {
		t.Errorf("prefix hits = %+v", hits)
	}
	// Lane and status ride along; snippets carry context.
	if hits := ix.Search("dashboard", 10); len(hits) != 1 || hits[0].Lane != "e2e" ||
		hits[0].Status != "pending" || !strings.Contains(hits[0].Snippet, "flux readings") {
		t.Errorf("dashboard hit = %+v", hits)
	}
	if hits := ix.Search("zanzibar", 10); len(hits) != 0 {
		t.Errorf("no-match hits = %+v", hits)
	}
	if hits := ix.Search("  ", 10); hits != nil {
		t.Errorf("empty query hits = %+v", hits)
	}
}
