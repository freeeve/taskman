package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.org")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.org")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "seed"}} {
		cmd := exec.Command("git", append([]string{"-C", home}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	srv := httptest.NewServer(Handler(home))
	t.Cleanup(srv.Close)
	return home, srv
}

// lastSubject returns the store's HEAD commit subject.
func lastSubject(t *testing.T, home string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", home, "log", "-1", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// send issues a JSON request and decodes the response, returning the status.
func send(t *testing.T, srv *httptest.Server, method, path string, body any, out any) int {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s: %v: %s", method, path, err, raw)
		}
	}
	return res.StatusCode
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

// TestAPIMutations drives every write route and asserts three things each
// time: the filesystem changed, the store got exactly the CLI's commit
// subject, and error paths return JSON with the right status.
func TestAPIMutations(t *testing.T) {
	home, srv := testStore(t)
	dir := filepath.Join(home, "myproj", "tasks")

	// Create a task (with a lane).
	var created struct {
		Num  int    `json:"num"`
		File string `json:"file"`
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks",
		map[string]string{"description": "From the web", "lane": "e2e"}, &created); code != 201 {
		t.Fatalf("create status %d", code)
	}
	if created.File != "005-e2e_from-the-web.md" {
		t.Errorf("created = %+v", created)
	}
	if _, err := os.Stat(filepath.Join(dir, created.File)); err != nil {
		t.Fatalf("created file: %v", err)
	}
	if s := lastSubject(t, home); s != "chore(myproj): open 005-e2e_from-the-web" {
		t.Errorf("create commit = %q", s)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks",
		map[string]string{"description": "!!!"}, nil); code != 400 {
		t.Errorf("empty-slug create status %d", code)
	}

	// Status moves rename and commit; done prunes the order file.
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/status",
		map[string]string{"status": "in-progress"}, nil); code != 200 {
		t.Fatalf("start status %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "002_build-board.in-progress.md")); err != nil {
		t.Fatalf("start rename: %v", err)
	}
	if s := lastSubject(t, home); s != "chore(myproj): start 002_build-board" {
		t.Errorf("start commit = %q", s)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/status",
		map[string]string{"status": "done"}, nil); code != 200 {
		t.Fatalf("done status %d", code)
	}
	order, err := os.ReadFile(filepath.Join(home, "myproj", "order"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(order), "002") {
		t.Errorf("done must prune the order file:\n%s", order)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/status",
		map[string]string{"status": "done"}, nil); code != 409 {
		t.Errorf("re-done status %d", code)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/status",
		map[string]string{"status": "bogus"}, nil); code != 400 {
		t.Errorf("bogus status %d", code)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/99/status",
		map[string]string{"status": "done"}, nil); code != 404 {
		t.Errorf("missing task status %d", code)
	}

	// Defer requires a reason; resume lifts it.
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/5/defer",
		map[string]string{"reason": "  "}, nil); code != 400 {
		t.Errorf("reasonless defer status %d", code)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/5/defer",
		map[string]string{"reason": "waiting on review"}, nil); code != 200 {
		t.Fatalf("defer status %d", code)
	}
	deferred := filepath.Join(dir, "005-e2e_from-the-web.deferred.md")
	if _, err := os.Stat(deferred); err != nil {
		t.Fatalf("defer rename: %v", err)
	}
	if body, _ := os.ReadFile(deferred); !strings.Contains(string(body), "waiting on review") {
		t.Errorf("reason not recorded:\n%s", body)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/5/resume", nil, nil); code != 200 {
		t.Fatalf("resume status %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "005-e2e_from-the-web.md")); err != nil {
		t.Fatalf("resume rename: %v", err)
	}

	// Reorder rewrites the order file in one commit.
	if code := send(t, srv, "PUT", "/api/projects/myproj/order",
		map[string][]int{"order": {5, 3}}, nil); code != 204 {
		t.Fatalf("reorder status %d", code)
	}
	order, _ = os.ReadFile(filepath.Join(home, "myproj", "order"))
	if !strings.Contains(string(order), "005\n003\n") {
		t.Errorf("order after PUT:\n%s", order)
	}
	if s := lastSubject(t, home); s != "chore(myproj): reorder tasks" {
		t.Errorf("reorder commit = %q", s)
	}

	// The reorder shows up in the read API.
	var data struct {
		Tasks []struct {
			Num int `json:"num"`
		} `json:"tasks"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks", &data); code != 200 {
		t.Fatal("reread failed")
	}
	if data.Tasks[0].Num != 5 || data.Tasks[1].Num != 3 {
		t.Errorf("tasks after reorder = %+v", data.Tasks)
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
