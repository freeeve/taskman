package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freeeve/taskman/internal/task"
)

// orderDir returns a temp project dir with the given order file content
// (none when content is empty).
func orderDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		if err := os.WriteFile(OrderPath(dir), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadOrderLenient(t *testing.T) {
	cases := map[string]struct {
		content string
		want    []int
	}{
		"missing file":     {"", nil},
		"plain":            {"003\n001\n012\n", []int{3, 1, 12}},
		"comments+blanks":  {"# header\n\n003\n\n# mid\n001\n", []int{3, 1}},
		"garbage skipped":  {"003\nnot-a-number\n001\n-5\n0\n", []int{3, 1}},
		"first dup wins":   {"003\n001\n003\n", []int{3, 1}},
		"whitespace":       {"  003  \n\t001\n", []int{3, 1}},
		"unpadded numbers": {"3\n1\n", []int{3, 1}},
	}
	for name, c := range cases {
		got := ReadOrder(orderDir(t, c.content))
		if len(got) != len(c.want) {
			t.Errorf("%s: ReadOrder = %v, want %v", name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: ReadOrder = %v, want %v", name, got, c.want)
				break
			}
		}
	}
}

func TestWriteOrderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteOrder(dir, []int{7, 12, 3})
	if err != nil {
		t.Fatal(err)
	}
	if path != OrderPath(dir) {
		t.Errorf("path = %q", path)
	}
	if got := ReadOrder(dir); len(got) != 3 || got[0] != 7 || got[1] != 12 || got[2] != 3 {
		t.Errorf("roundtrip = %v", got)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "#") || !strings.Contains(string(data), "007\n012\n003\n") {
		t.Errorf("file format:\n%s", data)
	}
}

func TestPruneOrder(t *testing.T) {
	// No file: nothing to do, no path returned.
	if path, err := PruneOrder(t.TempDir(), map[int]bool{3: true}); err != nil || path != "" {
		t.Errorf("prune without file = %q, %v", path, err)
	}
	// No change: file untouched, no path returned.
	dir := orderDir(t, "003\n001\n")
	if path, err := PruneOrder(dir, map[int]bool{9: true}); err != nil || path != "" {
		t.Errorf("no-op prune = %q, %v", path, err)
	}
	// A real prune rewrites and reports the path.
	path, err := PruneOrder(dir, map[int]bool{3: true})
	if err != nil || path == "" {
		t.Fatalf("prune = %q, %v", path, err)
	}
	if got := ReadOrder(dir); len(got) != 1 || got[0] != 1 {
		t.Errorf("after prune = %v", got)
	}
}

func TestSortByOrder(t *testing.T) {
	mk := func(n int) task.Task { return task.Task{Num: n, HasNum: true, Slug: "s"} }
	ask := task.Task{Prefix: "qbd", Slug: "ask"}
	tasks := []task.Task{mk(1), mk(2), mk(3), mk(12), ask}

	got := SortByOrder(tasks, []int{3, 1, 99})
	wantNums := []int{3, 1, 2, 12}
	for i, w := range wantNums {
		if got[i].Num != w {
			t.Fatalf("order = %v (listed first in file order, unlisted after by number)", got)
		}
	}
	if got[4].Prefix != "qbd" {
		t.Errorf("asks must stay last: %+v", got[4])
	}

	// No order file: input order preserved.
	got = SortByOrder(tasks, nil)
	for i := range tasks {
		if got[i].Num != tasks[i].Num || got[i].Prefix != tasks[i].Prefix {
			t.Errorf("nil order must be identity: %v", got)
		}
	}
}

// FuzzReadOrder pins leniency: arbitrary file content never panics and never
// yields duplicates or non-positive numbers.
func FuzzReadOrder(f *testing.F) {
	f.Add("003\n001\n")
	f.Add("# c\n\nx\n-1\n0\n003\n003\n")
	f.Add("999999999999999999999999\n")
	f.Fuzz(func(t *testing.T, content string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "order"), []byte(content), 0o644); err != nil {
			t.Skip()
		}
		seen := map[int]bool{}
		for _, n := range ReadOrder(dir) {
			if n <= 0 {
				t.Errorf("non-positive number %d from %q", n, content)
			}
			if seen[n] {
				t.Errorf("duplicate %d from %q", n, content)
			}
			seen[n] = true
		}
	})
}
