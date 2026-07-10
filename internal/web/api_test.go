package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testStore builds a store home with one seeded project and returns the home
// and a test server over it.
func testStore(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "myproj", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"001_ship-it.done.md":              "# 001 -- Ship it\n\nDone long ago.\n",
		"002_build-board.md":               "# 002 -- Build the board\n\nA **bold** plan.\n",
		"003-impl_wire-api.in-progress.md": "# 003 -- Wire the API\n\nBody.\n",
		"004_held.deferred.md":             "# 004 -- Held\n\nWaiting.\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "myproj", "order"),
		[]byte("# header\n002\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fdir := filepath.Join(home, "myproj", "features")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "kanban.md"),
		[]byte("# Kanban\n\nTasks: 001, 002, 099\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(home))
	t.Cleanup(srv.Close)
	return home, srv
}

// get fetches path and decodes the JSON body into out, returning the status.
func get(t *testing.T, srv *httptest.Server, path string, out any) int {
	t.Helper()
	res, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, data)
		}
	}
	return res.StatusCode
}

func TestAPIProjects(t *testing.T) {
	_, srv := testStore(t)
	var projects []struct {
		Name     string `json:"name"`
		Open     int    `json:"open"`
		Deferred int    `json:"deferred"`
	}
	if code := get(t, srv, "/api/projects", &projects); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(projects) != 1 || projects[0].Name != "myproj" ||
		projects[0].Open != 2 || projects[0].Deferred != 1 {
		t.Errorf("projects = %+v", projects)
	}
}

func TestAPITasks(t *testing.T) {
	_, srv := testStore(t)
	var data struct {
		Tasks []struct {
			Num      int    `json:"num"`
			Lane     string `json:"lane"`
			Status   string `json:"status"`
			Deferred bool   `json:"deferred"`
			Title    string `json:"title"`
		} `json:"tasks"`
		Order []int    `json:"order"`
		Lanes []string `json:"lanes"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks", &data); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(data.Tasks) != 4 {
		t.Fatalf("tasks = %+v", data.Tasks)
	}
	// Order file puts 002 first; the rest follow in ledger order.
	if data.Tasks[0].Num != 2 || data.Tasks[0].Title != "Build the board" {
		t.Errorf("first task = %+v", data.Tasks[0])
	}
	if len(data.Order) != 1 || data.Order[0] != 2 {
		t.Errorf("order = %v", data.Order)
	}
	if len(data.Lanes) != 1 || data.Lanes[0] != "impl" {
		t.Errorf("lanes = %v", data.Lanes)
	}
	for _, tk := range data.Tasks {
		if tk.Num == 4 && !tk.Deferred {
			t.Errorf("004 must be deferred: %+v", tk)
		}
	}

	// Unknown project and traversal-shaped names 404 without touching disk.
	if code := get(t, srv, "/api/projects/nope/tasks", nil); code != 404 {
		t.Errorf("unknown project status %d", code)
	}
	if code := get(t, srv, "/api/projects/%2e%2e/tasks", nil); code != 404 {
		t.Errorf("traversal status %d", code)
	}
}

func TestAPITaskDetail(t *testing.T) {
	_, srv := testStore(t)
	var data struct {
		Task struct {
			Num  int    `json:"num"`
			File string `json:"file"`
		} `json:"task"`
		Body string `json:"body"`
		HTML string `json:"html"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/2", &data); code != 200 {
		t.Fatalf("status %d", code)
	}
	if data.Task.File != "002_build-board.md" || !strings.Contains(data.Body, "**bold**") {
		t.Errorf("detail = %+v", data)
	}
	if !strings.Contains(data.HTML, "<strong>bold</strong>") {
		t.Errorf("GFM not rendered: %q", data.HTML)
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/99", nil); code != 404 {
		t.Errorf("missing task status %d", code)
	}
}

func TestAPIFeatures(t *testing.T) {
	_, srv := testStore(t)
	var feats []struct {
		Slug  string `json:"slug"`
		Done  bool   `json:"done"`
		Title string `json:"title"`
		HTML  string `json:"html"`
		Tasks []struct {
			Num    int    `json:"num"`
			Status string `json:"status"`
		} `json:"tasks"`
	}
	if code := get(t, srv, "/api/projects/myproj/features", &feats); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(feats) != 1 || feats[0].Slug != "kanban" || feats[0].Title != "Kanban" {
		t.Fatalf("features = %+v", feats)
	}
	chips := feats[0].Tasks
	if len(chips) != 3 || chips[0].Status != "done" || chips[1].Status != "pending" ||
		chips[2].Status != "missing" {
		t.Errorf("chips = %+v", chips)
	}
}

func TestStaticAndIndex(t *testing.T) {
	_, srv := testStore(t)
	for _, path := range []string{"/", "/static/app.css", "/static/board.js"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Errorf("GET %s = %d", path, res.StatusCode)
		}
	}
}

func TestServeRefusesPublicBind(t *testing.T) {
	if err := Serve("0.0.0.0:7777", t.TempDir(), false); err == nil ||
		!strings.Contains(err.Error(), "insecure-bind") {
		t.Errorf("public bind must be refused: %v", err)
	}
	if err := Serve("not-an-addr", t.TempDir(), false); err == nil {
		t.Error("bad addr must error")
	}
}
