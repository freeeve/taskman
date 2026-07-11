package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

// server holds the store root; all state lives on disk. mu serializes the
// mutating handlers end to end (load -> rename/write -> commit): without it
// a rename can interleave between another handler's pathspec filtering and
// its git commit, turning a lost same-task race into a spurious 500 instead
// of a clean 409. Mutations are millisecond-scale file renames, so full
// serialization costs nothing on a single-user localhost server.
type server struct {
	home string
	mu   sync.Mutex
}

// nameOK matches the slugs taskman itself generates; path segments that
// don't match never touch the filesystem, which is also the traversal guard.
var nameOK = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// markdown renders GitHub-flavored markdown; goldmark is the module's only
// dependency, kept server-side so the browser stays vanilla.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// taskJSON is the wire shape of one task.
type taskJSON struct {
	Num      int    `json:"num"`
	Lane     string `json:"lane"`
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Deferred bool   `json:"deferred"`
	File     string `json:"file"`
	Title    string `json:"title"`
}

// writeJSON emits v with the proper content type. no-store keeps browsers
// from heuristically caching ledger state that other writers change.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr emits the API's uniform error shape, with filesystem detail
// stripped first.
func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": sanitizeErr(err).Error()})
}

// sanitizeErr reduces every os error class to basenames: the absolute store
// path is server detail, not something a browser client should see. Renames
// (os.LinkError) are the common mutation failure -- every status, defer,
// resume, and ship is one.
func sanitizeErr(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s %s: %v", pe.Op, filepath.Base(pe.Path), pe.Err)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return fmt.Errorf("%s %s -> %s: %v", le.Op, filepath.Base(le.Old), filepath.Base(le.New), le.Err)
	}
	var se *os.SyscallError
	if errors.As(err, &se) {
		return fmt.Errorf("%s: %v", se.Syscall, se.Err)
	}
	return err
}

// projDir validates the {p} path segment and returns the project directory.
func (s *server) projDir(r *http.Request) (string, error) {
	p := r.PathValue("p")
	if !nameOK.MatchString(p) {
		return "", fmt.Errorf("invalid project %q", p)
	}
	dir := filepath.Join(s.home, p)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no project %q", p)
	}
	return dir, nil
}

// loadTasks reads a project's ledger in priority order.
func loadTasks(projDir string) ([]task.Task, []int, error) {
	tasks, err := task.Load(filepath.Join(projDir, "tasks"))
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	order := store.ReadOrder(projDir)
	return store.SortByOrder(tasks, order), order, nil
}

// titleRE strips the ledger's "NNN -- " H1 prefix for display.
var titleRE = regexp.MustCompile(`^#\s*(?:\d+\s*(?:--|\x{2014}|\x{2013}| - )\s*)?`)

// title returns the task's H1 with the number prefix stripped, falling back
// to the slug.
func title(t task.Task) string {
	data, err := os.ReadFile(t.Path())
	if err != nil {
		return t.Slug
	}
	line, _, _ := strings.Cut(string(data), "\n")
	if !strings.HasPrefix(line, "# ") {
		return t.Slug
	}
	if s := strings.TrimSpace(titleRE.ReplaceAllString(line, "")); s != "" {
		return s
	}
	return t.Slug
}

// toJSON converts a ledger task to its wire shape.
func toJSON(t task.Task) taskJSON {
	return taskJSON{
		Num: t.Num, Lane: t.Lane, Slug: t.Slug, Status: t.Status.String(),
		Deferred: t.Deferred, File: t.File, Title: title(t),
	}
}

// activity lists recent commits touching the project, newest first: the
// audit trail as a read-only view. The summary strips the conventional
// prefix for display; the raw subject rides along.
func (s *server) activity(w http.ResponseWriter, r *http.Request) {
	if _, err := s.projDir(r); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	p := r.PathValue("p")
	entries, err := store.ProjectLog(s.home, p, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type entryJSON struct {
		Commit  string `json:"commit"`
		Subject string `json:"subject"`
		Summary string `json:"summary"`
		Time    string `json:"time"`
	}
	out := []entryJSON{}
	for _, e := range entries {
		out = append(out, entryJSON{
			Commit:  e.Hash,
			Subject: e.Subject,
			Summary: strings.TrimSpace(strings.TrimPrefix(e.Subject, "chore("+p+"):")),
			Time:    e.Time,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// projects lists store projects with open/deferred counts.
func (s *server) projects(w http.ResponseWriter, r *http.Request) {
	names, err := store.Projects(s.home)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type projJSON struct {
		Name     string `json:"name"`
		Open     int    `json:"open"`
		Deferred int    `json:"deferred"`
	}
	out := []projJSON{}
	for _, name := range names {
		tasks, _, err := loadTasks(filepath.Join(s.home, name))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		pj := projJSON{Name: name}
		for _, t := range tasks {
			switch {
			case t.Status == task.Done:
			case t.Deferred:
				pj.Deferred++
			default:
				pj.Open++
			}
		}
		out = append(out, pj)
	}
	writeJSON(w, http.StatusOK, out)
}

// tasks returns a project's ledger pre-sorted by priority, with the order
// and the lanes in use (for the filter dropdown).
func (s *server) tasks(w http.ResponseWriter, r *http.Request) {
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	tasks, order, err := loadTasks(projDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if order == nil {
		order = []int{}
	}
	laneSet := map[string]bool{}
	out := []taskJSON{}
	for _, t := range tasks {
		out = append(out, toJSON(t))
		if t.Lane != "" {
			laneSet[t.Lane] = true
		}
	}
	lanes := []string{}
	for l := range laneSet {
		lanes = append(lanes, l)
	}
	sort.Strings(lanes)
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out, "order": order, "lanes": lanes})
}

// findByKey resolves {n} (number or slug fragment) in the project's ledger.
func findByKey(projDir, key string) (task.Task, error) {
	tasks, _, err := loadTasks(projDir)
	if err != nil {
		return task.Task{}, err
	}
	return task.Find(tasks, key)
}

// taskDetail returns one task plus its raw markdown body and the rendered
// GFM html.
func (s *server) taskDetail(w http.ResponseWriter, r *http.Request) {
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	t, err := findByKey(projDir, r.PathValue("n"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	body, err := os.ReadFile(t.Path())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rendered, err := renderBody(body, r.PathValue("p"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task": toJSON(t), "body": string(body), "html": rendered,
	})
}

// rewriteShots redirects store-relative screenshot links through the
// /shots/ route: bodies link images relative to their directory
// (../screenshots/NNN/f.png), which no browser route serves directly.
func rewriteShots(html, project string) string {
	return strings.ReplaceAll(html, `src="../screenshots/`, `src="/shots/`+project+`/`)
}

// rewriteLinks opens absolute links in a new tab: the board is a single-page
// app, and same-tab navigation discards its view state. goldmark always
// emits href as the first anchor attribute, which is what makes the string
// rewrite reliable; relative and in-page links stay untouched.
func rewriteLinks(html string) string {
	const attrs = `<a target="_blank" rel="noopener noreferrer" href="`
	html = strings.ReplaceAll(html, `<a href="http://`, attrs+`http://`)
	return strings.ReplaceAll(html, `<a href="https://`, attrs+`https://`)
}

// renderBody converts markdown to html with the store-specific rewrites
// applied; both task bodies and feature specs render through here.
func renderBody(body []byte, project string) (string, error) {
	var html bytes.Buffer
	if err := markdown.Convert(body, &html); err != nil {
		return "", err
	}
	return rewriteLinks(rewriteShots(html.String(), project)), nil
}

// features returns the project's features with per-linked-task status chips.
func (s *server) features(w http.ResponseWriter, r *http.Request) {
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	feats, err := store.LoadFeatures(projDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	tasks, _, err := loadTasks(projDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	byNum := map[int]task.Task{}
	for _, t := range tasks {
		if t.HasNum {
			byNum[t.Num] = t
		}
	}
	type chip struct {
		Num    int    `json:"num"`
		Status string `json:"status"`
	}
	type featJSON struct {
		Slug  string `json:"slug"`
		Done  bool   `json:"done"`
		Title string `json:"title"`
		HTML  string `json:"html"`
		Tasks []chip `json:"tasks"`
	}
	out := []featJSON{}
	for _, f := range feats {
		fj := featJSON{Slug: f.Slug, Done: f.Done, Title: f.Title, Tasks: []chip{}}
		if body, err := os.ReadFile(f.Path()); err == nil {
			if rendered, err := renderBody(body, r.PathValue("p")); err == nil {
				fj.HTML = rendered
			}
		}
		for _, n := range f.Tasks {
			c := chip{Num: n, Status: "missing"}
			if t, ok := byNum[n]; ok {
				c.Status = t.StatusLabel()
			}
			fj.Tasks = append(fj.Tasks, c)
		}
		out = append(out, fj)
	}
	writeJSON(w, http.StatusOK, out)
}
