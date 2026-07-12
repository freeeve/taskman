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
	"sync/atomic"

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
//
// The search index is the one piece of in-memory state, and it is a cache:
// rebuilt whenever the store's git HEAD moves (every mutation commits, so
// HEAD is the freshness token) and swapped in atomically so concurrent
// queries keep serving the previous index.
type server struct {
	home     string
	mu       sync.Mutex
	index    atomic.Pointer[store.SearchIndex]
	searchMu sync.Mutex
}

// searchIndex returns a fresh-enough index, rebuilding under its own lock
// when HEAD moved.
func (s *server) searchIndex() (*store.SearchIndex, error) {
	head := store.GitHead(s.home)
	if ix := s.index.Load(); ix != nil && ix.Head == head {
		return ix, nil
	}
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if ix := s.index.Load(); ix != nil && ix.Head == head {
		return ix, nil
	}
	ix, err := store.BuildIndex(s.home)
	if err != nil {
		return nil, err
	}
	s.index.Store(ix)
	return ix, nil
}

// search handles GET /api/search?q=...: global, cross-project full-text
// search over task and feature titles and bodies.
func (s *server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("missing query ?q="))
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	ix, err := s.searchIndex()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	results := ix.Search(q, limit)
	if results == nil {
		results = []store.SearchResult{}
	}
	writeJSON(w, http.StatusOK, results)
}

// nameOK matches the slugs taskman itself generates; path segments that
// don't match never touch the filesystem, which is also the traversal guard.
var nameOK = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// markdown renders GitHub-flavored markdown; goldmark is the module's only
// dependency, kept server-side so the browser stays vanilla.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// taskJSON is the wire shape of one task.
type taskJSON struct {
	Num         int    `json:"num"`
	Lane        string `json:"lane"`
	Slug        string `json:"slug"`
	Status      string `json:"status"`
	Deferred    bool   `json:"deferred"`
	File        string `json:"file"`
	Title       string `json:"title"`
	HasDecision bool   `json:"has_decision"`
}

// decisionJSON is the wire shape of a live structured question.
type decisionJSON struct {
	Question   string       `json:"question"`
	Options    []optionJSON `json:"options"`
	AllowOther bool         `json:"allow_other"`
}

type optionJSON struct {
	Label   string `json:"label"`
	Explain string `json:"explain"`
}

func toDecisionJSON(d task.Decision) *decisionJSON {
	dj := &decisionJSON{Question: d.Question, AllowOther: d.AllowOther, Options: []optionJSON{}}
	for _, opt := range d.Options {
		dj.Options = append(dj.Options, optionJSON{Label: opt.Label, Explain: opt.Explain})
	}
	return dj
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

// title returns the body's H1 with the number prefix stripped, falling back
// to the slug.
func title(body, fallback string) string {
	line, _, _ := strings.Cut(body, "\n")
	if !strings.HasPrefix(line, "# ") {
		return fallback
	}
	if s := strings.TrimSpace(titleRE.ReplaceAllString(line, "")); s != "" {
		return s
	}
	return fallback
}

// toJSON converts a ledger task to its wire shape, reading the body once for
// both the display title and the live-decision flag.
func toJSON(t task.Task) taskJSON {
	out := taskJSON{
		Num: t.Num, Lane: t.Lane, Slug: t.Slug, Status: t.Status.String(),
		Deferred: t.Deferred, File: t.File, Title: t.Slug,
	}
	if data, err := os.ReadFile(t.Path()); err == nil {
		out.Title = title(string(data), t.Slug)
		// A live decision means POSED, not merely present: deferred plus an
		// unanswered block, the same rule the inbox uses. A body that only
		// documents the block format (a spec) must not light the badge.
		if t.Deferred {
			_, out.HasDecision = task.ParseDecision(string(data))
		}
	}
	return out
}

// decisionRow is one live question for the decisions views: enough to list
// and navigate, deliberately not the full body.
type decisionRow struct {
	Project  string `json:"project"`
	Num      int    `json:"num"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Question string `json:"question"`
	Options  int    `json:"options"`
}

// scanDecisions collects one project's live decisions: deferred tasks whose
// body carries an unanswered block. Plain reason-defers never appear.
func scanDecisions(home, project string) ([]decisionRow, error) {
	tasks, err := task.Load(filepath.Join(home, project, "tasks"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	rows := []decisionRow{}
	for _, t := range tasks {
		if !t.Deferred {
			continue
		}
		body, err := os.ReadFile(t.Path())
		if err != nil {
			continue
		}
		d, live := task.ParseDecision(string(body))
		if !live {
			continue
		}
		rows = append(rows, decisionRow{
			Project: project, Num: t.Num, Slug: t.Slug,
			Title: title(string(body), t.Slug), Question: d.Question, Options: len(d.Options),
		})
	}
	return rows, nil
}

// decisionsAll lists live decisions across every project: the inbox.
func (s *server) decisionsAll(w http.ResponseWriter, r *http.Request) {
	names, err := store.Projects(s.home)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := []decisionRow{}
	for _, name := range names {
		rows, err := scanDecisions(s.home, name)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, rows...)
	}
	writeJSON(w, http.StatusOK, out)
}

// decisionsProject lists one project's live decisions.
func (s *server) decisionsProject(w http.ResponseWriter, r *http.Request) {
	if _, err := s.projDir(r); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	rows, err := scanDecisions(s.home, r.PathValue("p"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
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
	// Decision blocks are storage format: the live one renders as the
	// interactive widget instead, answered ones as readable summaries. The
	// raw body field stays exact for agents and the editor.
	rendered, err := renderBody([]byte(task.PresentDecisions(string(body))), r.PathValue("p"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var dj *decisionJSON
	if d, live := task.ParseDecision(string(body)); live && t.Deferred {
		dj = toDecisionJSON(d)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task": toJSON(t), "body": string(body), "html": rendered, "decision": dj,
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

// featureDetail returns one feature with its raw body (for the editor) and
// rendered html.
func (s *server) featureDetail(w http.ResponseWriter, r *http.Request) {
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	f, err := findFeatureSlug(projDir, r.PathValue("slug"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	body, err := os.ReadFile(f.Path())
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
		"slug": f.Slug, "done": f.Done, "title": f.Title, "file": f.File,
		"body": string(body), "html": rendered,
	})
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
