package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
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
	if code := send(t, srv, "POST", "/api/projects/myproj/features",
		map[string]string{"description": "Search everything"}, nil); code != 409 {
		t.Errorf("duplicate feature status %d", code)
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
	if code := send(t, srv, "POST", "/api/projects/myproj/features/nope/done", nil, nil); code != 404 {
		t.Errorf("missing feature status %d", code)
	}
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

func TestStaticAndIndex(t *testing.T) {
	_, srv := testStore(t)
	for _, path := range []string{"/", "/static/app.css", "/static/board.js", "/static/features.js"} {
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
