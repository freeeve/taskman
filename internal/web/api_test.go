package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
		"002_build-board.md":               "# 002 -- Build the board\n\nA **bold** plan. See https://example.com/docs and [local](other.md).\n",
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
		[]byte("# Kanban\n\nTasks: 001, 002, 099\n\n![diag](../screenshots/002/x.png)\n\n[pr](https://github.com/x/1)\n"), 0o644); err != nil {
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
	res, err := http.Get(srv.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (multi-writer store must not be cached)", cc)
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
	// Absolute links leave the SPA in a new tab; relative links stay as-is.
	if !strings.Contains(data.HTML,
		`<a target="_blank" rel="noopener noreferrer" href="https://example.com/docs"`) {
		t.Errorf("external link not retargeted: %q", data.HTML)
	}
	if !strings.Contains(data.HTML, `<a href="other.md"`) {
		t.Errorf("relative link must stay untouched: %q", data.HTML)
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
	// Screenshot links in feature bodies route through /shots/ exactly like
	// task bodies.
	if !strings.Contains(feats[0].HTML, `src="/shots/myproj/002/x.png"`) ||
		strings.Contains(feats[0].HTML, "../screenshots/") {
		t.Errorf("feature img src not rewritten: %q", feats[0].HTML)
	}
	if !strings.Contains(feats[0].HTML,
		`<a target="_blank" rel="noopener noreferrer" href="https://github.com/x/1"`) {
		t.Errorf("feature external link not retargeted: %q", feats[0].HTML)
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

// TestAPIFeatureMutations drives feature creation and shipping through the
// API with the same commit assertions as the task mutations.
func TestAPIFeatureMutations(t *testing.T) {
	home, srv := testStore(t)

	var created struct {
		Slug string `json:"slug"`
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/features",
		map[string]string{"description": "Search everything"}, &created); code != 201 {
		t.Fatalf("create status %d", code)
	}
	path := filepath.Join(home, "myproj", "features", "search-everything.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("feature file: %v", err)
	}
	if !strings.HasPrefix(string(data), "# Search everything\n") {
		t.Errorf("template:\n%s", data)
	}
	if s := lastSubject(t, home); s != "chore(myproj): feature search-everything" {
		t.Errorf("create commit = %q", s)
	}
	var dupErr struct {
		Error string `json:"error"`
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/features",
		map[string]string{"description": "Search everything"}, &dupErr); code != 409 {
		t.Errorf("duplicate feature status %d", code)
	}
	// The message names the feature, not the OS error or the store path.
	if dupErr.Error != `feature "search-everything" already exists` {
		t.Errorf("duplicate feature error = %q (must not leak paths)", dupErr.Error)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/features",
		map[string]string{"description": "!!!"}, nil); code != 400 {
		t.Errorf("empty-slug feature status %d", code)
	}

	if code := send(t, srv, "POST", "/api/projects/myproj/features/search-everything/done", nil, nil); code != 200 {
		t.Fatalf("done status %d", code)
	}
	if _, err := os.Stat(filepath.Join(home, "myproj", "features", "search-everything.done.md")); err != nil {
		t.Fatalf("done rename: %v", err)
	}
	if s := lastSubject(t, home); s != "chore(myproj): feature done search-everything" {
		t.Errorf("done commit = %q", s)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/features/search-everything/done", nil, nil); code != 409 {
		t.Errorf("re-done status %d", code)
	}
	// A shipped feature still owns its slug: re-creating it is refused, so
	// the original spec can never be clobbered by a later ship.
	if code := send(t, srv, "POST", "/api/projects/myproj/features",
		map[string]string{"description": "Search everything"}, &dupErr); code != 409 {
		t.Errorf("recreate-after-ship status %d", code)
	} else if dupErr.Error != `feature "search-everything" already exists (shipped)` {
		t.Errorf("recreate-after-ship error = %q", dupErr.Error)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/features/nope/done", nil, nil); code != 404 {
		t.Errorf("missing feature status %d", code)
	}

	// Linking: PUT tasks rewrites the Tasks: line (deduped, positive-only),
	// chips follow, and one scoped commit lands.
	var linked struct {
		Tasks []int `json:"tasks"`
	}
	if code := send(t, srv, "PUT", "/api/projects/myproj/features/search-everything/tasks",
		map[string][]int{"tasks": {2, 1, 2, -5}}, &linked); code != 200 {
		t.Fatalf("link status %d", code)
	}
	if len(linked.Tasks) != 2 || linked.Tasks[0] != 2 || linked.Tasks[1] != 1 {
		t.Errorf("linked = %v", linked.Tasks)
	}
	body, err := os.ReadFile(filepath.Join(home, "myproj", "features", "search-everything.done.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Tasks: 002, 001") {
		t.Errorf("Tasks line:\n%s", body)
	}
	if s := lastSubject(t, home); s != "chore(myproj): feature tasks search-everything" {
		t.Errorf("link commit = %q", s)
	}
	if code := send(t, srv, "PUT", "/api/projects/myproj/features/nope/tasks",
		map[string][]int{"tasks": {1}}, nil); code != 404 {
		t.Errorf("link to missing feature status %d", code)
	}

	// Create-pre-linked: the new task's number lands on the Tasks: line in
	// the same single commit as the task file.
	before, err := exec.Command("git", "-C", home, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	var newTask struct {
		Num int `json:"num"`
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks",
		map[string]string{"description": "Implement search", "feature": "search-everything"},
		&newTask); code != 201 {
		t.Fatalf("create-linked status %d", code)
	}
	body, _ = os.ReadFile(filepath.Join(home, "myproj", "features", "search-everything.done.md"))
	if !strings.Contains(string(body), fmt.Sprintf("Tasks: 002, 001, %03d", newTask.Num)) {
		t.Errorf("new task not linked:\n%s", body)
	}
	after, _ := exec.Command("git", "-C", home, "rev-list", "--count", "HEAD").Output()
	b, _ := strconv.Atoi(strings.TrimSpace(string(before)))
	a, _ := strconv.Atoi(strings.TrimSpace(string(after)))
	if a != b+1 {
		t.Errorf("create-linked commits %d -> %d, want one commit covering both files", b, a)
	}
	if s := lastSubject(t, home); !strings.Contains(s, "(feature search-everything)") {
		t.Errorf("create-linked commit = %q", s)
	}
	// A bogus feature slug fails BEFORE the task is created.
	preFiles, _ := os.ReadDir(filepath.Join(home, "myproj", "tasks"))
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks",
		map[string]string{"description": "Orphan", "feature": "nope"}, nil); code != 404 {
		t.Errorf("create with bogus feature status %d", code)
	}
	postFiles, _ := os.ReadDir(filepath.Join(home, "myproj", "tasks"))
	if len(postFiles) != len(preFiles) {
		t.Error("bogus feature slug must not leave an unlinked task behind")
	}

	// Ship is reversible: reopen renames back, commits, and 409s when the
	// feature is not shipped.
	if code := send(t, srv, "POST", "/api/projects/myproj/features/search-everything/reopen", nil, nil); code != 200 {
		t.Fatalf("reopen status %d", code)
	}
	if _, err := os.Stat(filepath.Join(home, "myproj", "features", "search-everything.md")); err != nil {
		t.Fatalf("reopen rename: %v", err)
	}
	if s := lastSubject(t, home); s != "chore(myproj): feature reopen search-everything" {
		t.Errorf("reopen commit = %q", s)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/features/search-everything/reopen", nil, nil); code != 409 {
		t.Errorf("re-reopen status %d", code)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/features/nope/reopen", nil, nil); code != 404 {
		t.Errorf("missing feature reopen status %d", code)
	}

	// Detail exposes the raw body; PUT body edits it in place with one
	// scoped commit, and rendering stays safe.
	var featDetail struct {
		Body string `json:"body"`
		File string `json:"file"`
	}
	if code := get(t, srv, "/api/projects/myproj/features/kanban", &featDetail); code != 200 ||
		!strings.Contains(featDetail.Body, "Tasks:") || featDetail.File != "kanban.md" {
		t.Fatalf("feature detail: code %d %+v", code, featDetail)
	}
	if code := send(t, srv, "PUT", "/api/projects/myproj/features/kanban",
		map[string]string{"body": "# Kanban\n\nTasks: 001\n\nRewritten spec <script>x</script>\n"}, nil); code != 200 {
		t.Fatalf("feature edit status %d", code)
	}
	if s := lastSubject(t, home); s != "chore(myproj): edit feature kanban" {
		t.Errorf("feature edit commit = %q", s)
	}
	var featAfter struct {
		Body string `json:"body"`
		HTML string `json:"html"`
	}
	if code := get(t, srv, "/api/projects/myproj/features/kanban", &featAfter); code != 200 ||
		!strings.Contains(featAfter.Body, "Rewritten spec") {
		t.Errorf("feature body not persisted: %+v", featAfter)
	}
	if strings.Contains(featAfter.HTML, "<script>") {
		t.Errorf("raw HTML must stay neutralized: %q", featAfter.HTML)
	}
	if code := send(t, srv, "PUT", "/api/projects/myproj/features/kanban",
		map[string]string{"body": "  "}, nil); code != 400 {
		t.Errorf("empty feature edit status %d", code)
	}
	if code := send(t, srv, "PUT", "/api/projects/myproj/features/nope",
		map[string]string{"body": "x"}, nil); code != 404 {
		t.Errorf("unknown feature edit status %d", code)
	}

	// Delete discards the spec (linked tasks untouched), commits once, and
	// project undo restores it.
	if code := send(t, srv, "DELETE", "/api/projects/myproj/features/kanban", nil, nil); code != 204 {
		t.Fatalf("delete status %d", code)
	}
	if _, err := os.Stat(filepath.Join(home, "myproj", "features", "kanban.md")); err == nil {
		t.Error("feature file must be removed")
	}
	if _, err := os.Stat(filepath.Join(home, "myproj", "tasks", "001_ship-it.done.md")); err != nil {
		t.Errorf("linked task must survive the delete: %v", err)
	}
	if s := lastSubject(t, home); s != "chore(myproj): remove feature kanban" {
		t.Errorf("delete commit = %q", s)
	}
	if code := send(t, srv, "DELETE", "/api/projects/myproj/features/kanban", nil, nil); code != 404 {
		t.Errorf("re-delete status %d", code)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/undo", nil, nil); code != 200 {
		t.Fatalf("undo of delete failed")
	}
	if _, err := os.Stat(filepath.Join(home, "myproj", "features", "kanban.md")); err != nil {
		t.Errorf("undo must restore the deleted feature: %v", err)
	}
}

// TestConcurrentMutationsAllCommitted pins the API's audit-trail contract
// under concurrency: N parallel creates must all succeed AND all land as
// commits, leaving the store working tree clean -- no straggler staged or
// untracked while its request got a 2xx.
func TestConcurrentMutationsAllCommitted(t *testing.T) {
	home, srv := testStore(t)
	before, err := exec.Command("git", "-C", home, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	const n = 12
	codes := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"description":"Conc feature %02d"}`, i)
			res, err := http.Post(srv.URL+"/api/projects/myproj/features",
				"application/json", strings.NewReader(body))
			if err != nil {
				errs[i] = err
				return
			}
			res.Body.Close()
			codes[i] = res.StatusCode
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || codes[i] != 201 {
			t.Errorf("create %02d: code %d err %v", i, codes[i], errs[i])
		}
	}
	if status := lastPorcelain(t, home); status != "" {
		t.Errorf("store dirty after concurrent creates:\n%s", status)
	}
	after, err := exec.Command("git", "-C", home, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := strconv.Atoi(strings.TrimSpace(string(before)))
	a, _ := strconv.Atoi(strings.TrimSpace(string(after)))
	if a != b+n {
		t.Errorf("commit count %d -> %d, want +%d", b, a, n)
	}
}

// TestConcurrentSameTaskUniform409 pins the race semantics: concurrent
// status changes on ONE task answer 200 for winners and 409 for losers --
// never 500 -- and leave exactly one file, committed, tree clean.
func TestConcurrentSameTaskUniform409(t *testing.T) {
	home, srv := testStore(t)
	statuses := []string{"done", "in-progress", "pending", "done", "in-progress", "done", "pending", "in-progress"}
	codes := make([]int, len(statuses))
	var wg sync.WaitGroup
	for i, status := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"status":%q}`, status)
			res, err := http.Post(srv.URL+"/api/projects/myproj/tasks/2/status",
				"application/json", strings.NewReader(body))
			if err != nil {
				return
			}
			res.Body.Close()
			codes[i] = res.StatusCode
		}()
	}
	wg.Wait()
	for i, code := range codes {
		if code != 200 && code != 409 {
			t.Errorf("request %d (%s): code %d, want 200 or 409", i, statuses[i], code)
		}
	}
	// Exactly one file for task 002 survives, and the tree is clean.
	matches, err := filepath.Glob(filepath.Join(home, "myproj", "tasks", "002_build-board*"))
	if err != nil || len(matches) != 1 {
		t.Errorf("task 002 files = %v (%v)", matches, err)
	}
	if status := lastPorcelain(t, home); status != "" {
		t.Errorf("store dirty after same-task races:\n%s", status)
	}
}

// lastPorcelain returns git status --porcelain for the store.
func lastPorcelain(t *testing.T, home string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", home, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// pngBytes is a valid 1x1 PNG, enough for content sniffing and serving.
var pngBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// upload POSTs a multipart screenshot and returns status and decoded body.
func upload(t *testing.T, srv *httptest.Server, path string, content []byte) (int, map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(srv.URL+path, mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]string{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// TestScreenshots pins the whole flow: multipart upload lands under
// screenshots/NNN/ outside tasks/, the task body links it, one commit covers
// both, /shots/ serves it, and junk uploads or traversal attempts bounce.
func TestScreenshots(t *testing.T) {
	home, srv := testStore(t)
	shotsURL := "/api/projects/myproj/tasks/2/screenshots"

	code, out := upload(t, srv, shotsURL, pngBytes)
	if code != 201 {
		t.Fatalf("upload status %d: %v", code, out)
	}
	if !strings.HasPrefix(out["path"], "screenshots/002/") || !strings.HasSuffix(out["path"], ".png") {
		t.Fatalf("upload path = %q", out["path"])
	}
	img := filepath.Join(home, "myproj", out["path"])
	if data, err := os.ReadFile(img); err != nil || !bytes.Equal(data, pngBytes) {
		t.Fatalf("stored image: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, "myproj", "tasks", "002_build-board.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "## Screenshot") ||
		!strings.Contains(string(body), "](../"+out["path"]+")") {
		t.Errorf("task body link:\n%s", body)
	}
	if s := lastSubject(t, home); s != "chore(myproj): screenshot for 002_build-board" {
		t.Errorf("commit = %q", s)
	}

	// The rendered task html routes the image through /shots/.
	var detail struct {
		HTML string `json:"html"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/2", &detail); code != 200 {
		t.Fatal("detail failed")
	}
	if !strings.Contains(detail.HTML, `src="/shots/myproj/002/`) {
		t.Errorf("img src not rewritten: %q", detail.HTML)
	}

	// Serving round-trips the bytes; bad names 404.
	res, err := http.Get(srv.URL + "/shots/myproj/2/" + filepath.Base(out["path"]))
	if err != nil {
		t.Fatal(err)
	}
	served, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Equal(served, pngBytes) {
		t.Errorf("serve status %d, %d bytes", res.StatusCode, len(served))
	}
	for _, path := range []string{
		"/shots/myproj/2/.hidden.png",
		"/shots/myproj/2/..%2forder",
		"/shots/bad%20name/2/x.png",
		"/shots/myproj/0/x.png",
	} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", path, res.StatusCode)
		}
	}

	// Non-image content is refused; a second upload in the same second gets
	// a distinct name.
	if code, out := upload(t, srv, shotsURL, []byte("just text, not an image")); code != 400 {
		t.Errorf("text upload = %d: %v", code, out)
	}
	code, out2 := upload(t, srv, shotsURL, pngBytes)
	if code != 201 || out2["path"] == out["path"] {
		t.Errorf("second upload = %d %q (first %q)", code, out2["path"], out["path"])
	}

	// Unknown task 404s.
	if code, _ := upload(t, srv, "/api/projects/myproj/tasks/99/screenshots", pngBytes); code != 404 {
		t.Errorf("unknown task upload = %d", code)
	}
}

// TestAPIErrorPaths sweeps the mutation routes' failure branches: unknown
// projects, malformed JSON, and state conflicts all answer with JSON errors
// and the right status.
func TestAPIErrorPaths(t *testing.T) {
	_, srv := testStore(t)

	// Unknown project 404s on every mutating route.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/projects/nope/tasks"},
		{"POST", "/api/projects/nope/tasks/1/status"},
		{"POST", "/api/projects/nope/tasks/1/defer"},
		{"POST", "/api/projects/nope/tasks/1/resume"},
		{"PUT", "/api/projects/nope/order"},
		{"POST", "/api/projects/nope/features"},
		{"POST", "/api/projects/nope/features/x/done"},
	} {
		if code := send(t, srv, c.method, c.path, map[string]string{}, nil); code != 404 {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, code)
		}
	}

	// Malformed JSON bodies 400.
	for _, path := range []string{
		"/api/projects/myproj/tasks",
		"/api/projects/myproj/tasks/2/status",
		"/api/projects/myproj/tasks/2/defer",
		"/api/projects/myproj/order",
	} {
		method := "POST"
		if strings.HasSuffix(path, "order") {
			method = "PUT"
		}
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader("{not json"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Errorf("%s %s with garbage = %d, want 400", method, path, res.StatusCode)
		}
	}

	// Over-long descriptions fail validation up front with a clean message;
	// no absolute store path may reach the client (generalizing task 017).
	long := strings.Repeat("a", 300)
	var lengthErr struct {
		Error string `json:"error"`
	}
	for _, path := range []string{"/api/projects/myproj/tasks", "/api/projects/myproj/features"} {
		if code := send(t, srv, "POST", path,
			map[string]string{"description": long}, &lengthErr); code != 400 {
			t.Errorf("over-long create on %s = %d, want 400", path, code)
		}
		if !strings.Contains(lengthErr.Error, "too long") || strings.Contains(lengthErr.Error, "/") {
			t.Errorf("over-long error on %s = %q (must be clean, no path)", path, lengthErr.Error)
		}
	}

	// State conflicts 409: resuming an undeferred task, deferring a done one.
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/resume", nil, nil); code != 409 {
		t.Errorf("resume undeferred = %d, want 409", code)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/1/defer",
		map[string]string{"reason": "why"}, nil); code != 409 {
		t.Errorf("defer done task = %d, want 409", code)
	}
	// Resume of a deferred task via slug key works (Find accepts fragments).
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/held/resume", nil, nil); code != 200 {
		t.Errorf("resume by slug = %d", code)
	}
}

// TestWriteErrSanitizesOSErrors pins the catch-all: every os error class
// (open failures, rename failures, raw syscalls) loses its directories
// before reaching the client. Renames matter most -- every status, defer,
// resume, and ship mutation is one.
func TestWriteErrSanitizesOSErrors(t *testing.T) {
	cases := map[string]error{
		"path": &os.PathError{Op: "open", Path: "/abs/store/dir/file.md", Err: os.ErrPermission},
		"link": &os.LinkError{Op: "rename", Old: "/abs/store/dir/a.md",
			New: "/abs/store/dir/a.done.md", Err: os.ErrNotExist},
		"syscall": os.NewSyscallError("flock", os.ErrInvalid),
	}
	for name, in := range cases {
		rec := httptest.NewRecorder()
		writeErr(rec, 400, in)
		body := rec.Body.String()
		if strings.Contains(body, "/abs/store") {
			t.Errorf("%s: error leaks the store path: %q", name, body)
		}
		if name == "link" && (!strings.Contains(body, "a.md") || !strings.Contains(body, "a.done.md")) {
			t.Errorf("link: basenames missing: %q", body)
		}
	}
}

// TestDuplicateNumberStemOpen pins duplicate-number resilience: the bare
// number is ambiguous (404 listing both), but the stem still opens each half
// with a distinct slug, so an existing dup is manageable from the UI.
func TestDuplicateNumberStemOpen(t *testing.T) {
	home, srv := testStore(t)
	dir := filepath.Join(home, "myproj", "tasks")
	for _, n := range []string{"007_alpha.md", "007_beta.done.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("# 007 -- "+n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var ambiguous struct {
		Error string `json:"error"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/7", &ambiguous); code != 404 ||
		!strings.Contains(ambiguous.Error, "ambiguous") {
		t.Errorf("bare number: code %d err %q", code, ambiguous.Error)
	}
	var detail struct {
		Task struct {
			File string `json:"file"`
		} `json:"task"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/007_alpha", &detail); code != 200 ||
		detail.Task.File != "007_alpha.md" {
		t.Errorf("stem open: code %d file %q", code, detail.Task.File)
	}
}

// TestDecisionAPI drives the structured-question flow over HTTP: the flag
// and parsed payload surface, plain resume refuses, answering validates,
// records, un-defers, promotes to top-of-order, and stale answers 409.
func TestDecisionAPI(t *testing.T) {
	home, srv := testStore(t)
	dir := filepath.Join(home, "myproj", "tasks")

	// Pose a decision on the deferred fixture task (004_held.deferred.md).
	block := "\n```decision\nquestion: Inline or queue?\noptions:\n- label: Inline\n  explain: simpler\n- label: Queue\n  explain: durable\nallow_other: true\n```\n"
	path := filepath.Join(dir, "004_held.deferred.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte(block)...), 0o644); err != nil {
		t.Fatal(err)
	}

	// The list flags it; the detail carries the parsed decision.
	var list struct {
		Tasks []struct {
			Num         int  `json:"num"`
			HasDecision bool `json:"has_decision"`
		} `json:"tasks"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks", &list); code != 200 {
		t.Fatal("list failed")
	}
	found := false
	for _, tk := range list.Tasks {
		if tk.Num == 4 && tk.HasDecision {
			found = true
		}
	}
	if !found {
		t.Errorf("has_decision flag missing: %+v", list.Tasks)
	}
	var detail struct {
		Decision *struct {
			Question   string `json:"question"`
			AllowOther bool   `json:"allow_other"`
			Options    []struct {
				Label   string `json:"label"`
				Explain string `json:"explain"`
			} `json:"options"`
		} `json:"decision"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/4", &detail); code != 200 {
		t.Fatal("detail failed")
	}
	if detail.Decision == nil || detail.Decision.Question != "Inline or queue?" ||
		len(detail.Decision.Options) != 2 || detail.Decision.Options[1].Explain != "durable" {
		t.Fatalf("decision payload = %+v", detail.Decision)
	}
	// The raw block must not double-render as a code block below the widget.
	var detailHTML struct {
		HTML string `json:"html"`
		Body string `json:"body"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/4", &detailHTML); code != 200 {
		t.Fatal("detail failed")
	}
	if strings.Contains(detailHTML.HTML, "language-decision") {
		t.Errorf("decision fence leaked into rendered html: %q", detailHTML.HTML)
	}
	if !strings.Contains(detailHTML.Body, "```decision") {
		t.Errorf("raw body must keep the exact block for agents:\n%s", detailHTML.Body)
	}

	// Plain resume refuses while the decision is live.
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/4/resume", nil, nil); code != 409 {
		t.Errorf("plain resume status %d", code)
	}
	// Bad label and empty answers 400.
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/4/answer",
		map[string]string{"choice": "Nope"}, nil); code != 400 {
		t.Errorf("bad label status %d", code)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/4/answer",
		map[string]string{}, nil); code != 400 {
		t.Errorf("empty answer status %d", code)
	}

	// Answering records, un-defers, and jumps the order.
	var answered struct {
		Deferred bool   `json:"deferred"`
		File     string `json:"file"`
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/4/answer",
		map[string]string{"choice": "Queue"}, &answered); code != 200 {
		t.Fatalf("answer status %d", code)
	}
	if answered.Deferred || answered.File != "004_held.md" {
		t.Errorf("answered task = %+v", answered)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "004_held.md"))
	if !strings.Contains(string(after), "chosen: Queue") ||
		!strings.Contains(string(after), "```decision answered") {
		t.Errorf("answered record:\n%s", after)
	}
	// The answered history renders as a summary, not a raw code block.
	var answeredHTML struct {
		HTML string `json:"html"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/4", &answeredHTML); code != 200 {
		t.Fatal("post-answer detail failed")
	}
	if strings.Contains(answeredHTML.HTML, "language-decision") ||
		!strings.Contains(answeredHTML.HTML, "Chosen:") {
		t.Errorf("answered block not summarized: %q", answeredHTML.HTML)
	}
	order, _ := os.ReadFile(filepath.Join(home, "myproj", "order"))
	if !strings.Contains(string(order), "004\n002") {
		t.Errorf("answered decision must lead the order:\n%s", order)
	}
	if s := lastSubject(t, home); s != "chore(myproj): answer decision on 004_held (Queue)" {
		t.Errorf("answer commit = %q", s)
	}
	// Stale answer 409s; a task with no decision 400s.
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/4/answer",
		map[string]string{"choice": "Inline"}, nil); code != 409 {
		t.Errorf("stale answer status %d", code)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/answer",
		map[string]string{"choice": "x"}, nil); code != 400 {
		t.Errorf("no-decision answer status %d", code)
	}

	// Free-text Other records the note.
	block2 := "\n```decision\nquestion: Name it?\noptions:\n- label: alpha\n- label: beta\n```\n"
	p2 := filepath.Join(dir, "002_build-board.md")
	b2, _ := os.ReadFile(p2)
	if err := os.WriteFile(p2, append(b2, []byte(block2)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/answer",
		map[string]string{"other": "call it gamma"}, nil); code != 200 {
		t.Fatalf("other answer failed")
	}
	after, _ = os.ReadFile(p2)
	if !strings.Contains(string(after), "chosen: Other") ||
		!strings.Contains(string(after), "note: call it gamma") {
		t.Errorf("other record:\n%s", after)
	}
}

// TestSearchAPI pins the global search route: cross-project hits with
// context, rebuild on HEAD movement, and clean handling of empty queries.
func TestSearchAPI(t *testing.T) {
	_, srv := testStore(t)
	var hits []struct {
		Project string `json:"project"`
		Kind    string `json:"kind"`
		Num     int    `json:"num"`
		Title   string `json:"title"`
		Snippet string `json:"snippet"`
	}
	if code := get(t, srv, "/api/search?q=bold", &hits); code != 200 {
		t.Fatalf("search status %d", code)
	}
	if len(hits) != 1 || hits[0].Project != "myproj" || hits[0].Num != 2 ||
		!strings.Contains(hits[0].Snippet, "bold") {
		t.Errorf("hits = %+v", hits)
	}

	// A mutation moves HEAD; the next query sees the new content.
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks",
		map[string]string{"description": "Chase the zanzibar gazelle"}, nil); code != 201 {
		t.Fatal("create failed")
	}
	if code := get(t, srv, "/api/search?q=zanzibar", &hits); code != 200 || len(hits) != 1 {
		t.Errorf("post-mutation search: code %d hits %+v", 200, hits)
	}

	if code := get(t, srv, "/api/search?q=xyzzynope", &hits); code != 200 || len(hits) != 0 {
		t.Errorf("no-match: %+v", hits)
	}
	if code := get(t, srv, "/api/search", nil); code != 400 {
		t.Errorf("missing q status %d", code)
	}
}

// TestEditTask pins the edit endpoint: body replacement re-renders and
// commits once, a title change renames safely with tokens kept, raw HTML in
// an edited body stays neutralized, and clobber/empty edits answer cleanly.
func TestEditTask(t *testing.T) {
	home, srv := testStore(t)
	dir := filepath.Join(home, "myproj", "tasks")

	// Body edit: full raw markdown replacement.
	if code := send(t, srv, "PUT", "/api/projects/myproj/tasks/2",
		map[string]string{"body": "# 002 -- Build the board\n\nRewritten <script>alert(1)</script> body.\n"},
		nil); code != 200 {
		t.Fatalf("body edit status %d", code)
	}
	if s := lastSubject(t, home); s != "chore(myproj): edit 002_build-board" {
		t.Errorf("edit commit = %q", s)
	}
	var detail struct {
		Body string `json:"body"`
		HTML string `json:"html"`
	}
	if code := get(t, srv, "/api/projects/myproj/tasks/2", &detail); code != 200 {
		t.Fatal("detail failed")
	}
	if !strings.Contains(detail.Body, "Rewritten") {
		t.Errorf("body not replaced: %q", detail.Body)
	}
	if strings.Contains(detail.HTML, "<script>") {
		t.Errorf("raw HTML must stay neutralized: %q", detail.HTML)
	}

	// Title edit: rename with status kept; H1 restamped.
	var edited struct {
		File string `json:"file"`
	}
	if code := send(t, srv, "PUT", "/api/projects/myproj/tasks/3",
		map[string]string{"title": "Wire the whole API"}, &edited); code != 200 {
		t.Fatalf("title edit status %d", code)
	}
	if edited.File != "003-impl_wire-the-whole-api.in-progress.md" {
		t.Errorf("renamed file = %q (lane and status must survive)", edited.File)
	}
	body, err := os.ReadFile(filepath.Join(dir, edited.File))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "# 003 -- Wire the whole API\n") {
		t.Errorf("H1 not restamped:\n%s", body)
	}

	// A deferred task's marker survives a retitle (clobber refusal itself is
	// unit-tested in the task package -- it needs a duplicate number).
	var deferredEdit struct {
		File     string `json:"file"`
		Deferred bool   `json:"deferred"`
	}
	if code := send(t, srv, "PUT", "/api/projects/myproj/tasks/4",
		map[string]string{"title": "Still held"}, &deferredEdit); code != 200 {
		t.Fatalf("deferred retitle status %d", code)
	}
	if deferredEdit.File != "004_still-held.deferred.md" || !deferredEdit.Deferred {
		t.Errorf("deferred retitle = %+v", deferredEdit)
	}
	// Empty edit 400s; unknown task 404s.
	if code := send(t, srv, "PUT", "/api/projects/myproj/tasks/2", map[string]string{}, nil); code != 400 {
		t.Errorf("empty edit status %d", code)
	}
	if code := send(t, srv, "PUT", "/api/projects/myproj/tasks/99",
		map[string]string{"body": "x"}, nil); code != 404 {
		t.Errorf("unknown task status %d", code)
	}
	if status := lastPorcelain(t, home); status != "" {
		t.Errorf("tree dirty after edits:\n%s", status)
	}
}

// TestActivity pins the audit-trail view: newest-first project-scoped
// commits with stripped summaries and commit-metadata timestamps; other
// projects' commits do not bleed in.
func TestActivity(t *testing.T) {
	home, srv := testStore(t)
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/status",
		map[string]string{"status": "in-progress"}, nil); code != 200 {
		t.Fatal("setup mutation failed")
	}
	// Another project commits afterward; it must not appear.
	other := filepath.Join(home, "otherproj", "tasks")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "001_x.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "chore(otherproj): open 001_x"}} {
		if out, err := exec.Command("git", append([]string{"-C", home}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	var entries []struct {
		Commit  string `json:"commit"`
		Subject string `json:"subject"`
		Summary string `json:"summary"`
		Time    string `json:"time"`
	}
	if code := get(t, srv, "/api/projects/myproj/activity?limit=10", &entries); code != 200 {
		t.Fatalf("activity status %d", code)
	}
	if len(entries) < 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Summary != "start 002_build-board" ||
		entries[0].Subject != "chore(myproj): start 002_build-board" {
		t.Errorf("top entry = %+v", entries[0])
	}
	if entries[0].Time == "" || !strings.Contains(entries[0].Time, "T") {
		t.Errorf("time not ISO commit metadata: %q", entries[0].Time)
	}
	for _, e := range entries {
		if strings.Contains(e.Subject, "otherproj") {
			t.Errorf("foreign commit bled in: %+v", e)
		}
	}
	// limit is respected.
	if code := get(t, srv, "/api/projects/myproj/activity?limit=1", &entries); code != 200 || len(entries) != 1 {
		t.Errorf("limit=1 -> %d entries", len(entries))
	}
	if code := get(t, srv, "/api/projects/nope/activity", nil); code != 404 {
		t.Errorf("unknown project status %d", code)
	}
}

// TestUndo pins the revert path: undo restores the prior state as its own
// commit, targets THIS project's newest taskman commit (not repo HEAD),
// refuses stale hashes and foreign commits, and an undo is itself undoable.
func TestUndo(t *testing.T) {
	home, srv := testStore(t)
	dir := filepath.Join(home, "myproj", "tasks")

	// Mutate: 002 done (also prunes it from the order file).
	if code := send(t, srv, "POST", "/api/projects/myproj/tasks/2/status",
		map[string]string{"status": "done"}, nil); code != 200 {
		t.Fatal("setup mutation failed")
	}

	// Another project commits after ours; undo must still target myproj.
	other := filepath.Join(home, "otherproj", "tasks")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "001_x.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "chore(otherproj): open 001_x"}} {
		if out, err := exec.Command("git", append([]string{"-C", home}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// Peek shows the myproj mutation, not otherproj's HEAD.
	var peek struct {
		Commit  string `json:"commit"`
		Subject string `json:"subject"`
	}
	if code := get(t, srv, "/api/projects/myproj/undo", &peek); code != 200 {
		t.Fatalf("peek status %d", code)
	}
	if peek.Subject != "chore(myproj): done 002_build-board" {
		t.Fatalf("peek subject = %q", peek.Subject)
	}

	// A stale hash 409s.
	if code := send(t, srv, "POST", "/api/projects/myproj/undo",
		map[string]string{"commit": "0000000000000000000000000000000000000000"}, nil); code != 409 {
		t.Errorf("stale hash status %d", code)
	}

	// The real undo restores pending state and the order entry.
	if code := send(t, srv, "POST", "/api/projects/myproj/undo",
		map[string]string{"commit": peek.Commit}, nil); code != 200 {
		t.Fatalf("undo failed")
	}
	if _, err := os.Stat(filepath.Join(dir, "002_build-board.md")); err != nil {
		t.Errorf("undo did not restore the pending file: %v", err)
	}
	order, _ := os.ReadFile(filepath.Join(home, "myproj", "order"))
	if !strings.Contains(string(order), "002") {
		t.Errorf("undo did not restore the order entry:\n%s", order)
	}
	if s := lastSubject(t, home); !strings.HasPrefix(s, `Revert "chore(myproj): done`) {
		t.Errorf("revert commit = %q", s)
	}
	if status := lastPorcelain(t, home); status != "" {
		t.Errorf("tree dirty after undo:\n%s", status)
	}

	// Undoing the undo (redo) is allowed and restores done state.
	if code := send(t, srv, "POST", "/api/projects/myproj/undo", nil, nil); code != 200 {
		t.Fatalf("redo failed")
	}
	if _, err := os.Stat(filepath.Join(dir, "002_build-board.done.md")); err != nil {
		t.Errorf("redo did not restore done state: %v", err)
	}

	// A foreign (hand) commit touching the project is refused.
	probe := filepath.Join(home, "myproj", "notes.txt")
	if err := os.WriteFile(probe, []byte("hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "manual edit"}} {
		if out, err := exec.Command("git", append([]string{"-C", home}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	var refuse struct {
		Error string `json:"error"`
	}
	if code := send(t, srv, "POST", "/api/projects/myproj/undo", nil, &refuse); code != 409 {
		t.Errorf("foreign commit undo status %d", code)
	}
	if !strings.Contains(refuse.Error, "not a taskman mutation") {
		t.Errorf("foreign refusal = %q", refuse.Error)
	}
}

func TestStaticAndIndex(t *testing.T) {
	_, srv := testStore(t)
	for _, path := range []string{"/", "/static/app.css", "/static/board.js",
		"/static/features.js", "/static/activity.js", "/static/router.js"} {
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
