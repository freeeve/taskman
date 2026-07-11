package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/task"
)

// commit runs the same auto-commit convention as the CLI, so a drag in the
// browser and a command in a terminal leave identical history. Unlike the
// CLI, the web contract is strict: a failure must reach the client, so
// handlers route their success response through commitOK.
func (s *server) commit(project, msg string, paths ...string) error {
	return store.AutoCommit(false, s.home, fmt.Sprintf("chore(%s): %s", project, msg), paths...)
}

// commitOK reports whether the commit succeeded, answering 500 with an
// explicit applied-but-not-committed error when it did not -- the mutation
// is on disk either way, and pretending otherwise hides audit-trail gaps.
func (s *server) commitOK(w http.ResponseWriter, project, msg string, paths ...string) bool {
	if err := s.commit(project, msg, paths...); err != nil {
		writeErr(w, http.StatusInternalServerError,
			fmt.Errorf("change applied but not committed: %v", err))
		return false
	}
	return true
}

// readBody decodes a JSON request body into v.
func readBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// createTask handles POST tasks: {"description", "lane"} -> 201 + task.
func (s *server) createTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Description string `json:"description"`
		Lane        string `json:"lane"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	tasks, _, err := loadTasks(projDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	t, err := task.New(filepath.Join(projDir, "tasks"), tasks,
		strings.TrimSpace(req.Description), task.Slugify(req.Lane), today())
	if err != nil {
		if os.IsExist(err) {
			writeErr(w, http.StatusConflict,
				fmt.Errorf("task number was claimed concurrently; retry"))
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), "open "+t.Stem(), t.Path()) {
		return
	}
	writeJSON(w, http.StatusCreated, toJSON(t))
}

// setStatus handles POST tasks/{n}/status: {"status"} -> task. Marking a
// task done prunes its number from the order file in the same commit,
// exactly like the CLI.
func (s *server) setStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var target task.Status
	switch req.Status {
	case "pending":
		target = task.Pending
	case "in-progress":
		target = task.InProgress
	case "done":
		target = task.Done
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid status %q", req.Status))
		return
	}
	t, err := findByKey(projDir, r.PathValue("n"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	nt, err := t.SetStatus(target)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	paths := []string{t.Path(), nt.Path()}
	if target == task.Done && nt.HasNum {
		op, err := store.PruneOrder(projDir, map[int]bool{nt.Num: true})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if op != "" {
			paths = append(paths, op)
		}
	}
	verb := map[task.Status]string{task.InProgress: "start", task.Done: "done", task.Pending: "reopen"}[target]
	if !s.commitOK(w, r.PathValue("p"), fmt.Sprintf("%s %s", verb, nt.Stem()), paths...) {
		return
	}
	writeJSON(w, http.StatusOK, toJSON(nt))
}

// deferTask handles POST tasks/{n}/defer: {"reason"} -> task. The reason is
// mandatory here for the same cause as in the CLI: the filename cannot carry
// the why.
func (s *server) deferTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("defer requires a reason"))
		return
	}
	t, err := findByKey(projDir, r.PathValue("n"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	nt, err := t.Defer(reason, today())
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), fmt.Sprintf("defer %s (%s)", nt.Stem(), reason), t.Path(), nt.Path()) {
		return
	}
	writeJSON(w, http.StatusOK, toJSON(nt))
}

// resumeTask handles POST tasks/{n}/resume.
func (s *server) resumeTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	nt, err := t.Resume(today())
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), "resume "+nt.Stem(), t.Path(), nt.Path()) {
		return
	}
	writeJSON(w, http.StatusOK, toJSON(nt))
}

// setOrder handles PUT order: {"order":[3,7,12]} -> 204. One drag, one
// whole-file rewrite, one commit; concurrent writers are last-write-wins.
func (s *server) setOrder(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Order []int `json:"order"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := store.WriteOrder(projDir, req.Order)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), "reorder tasks", path) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// createFeature handles POST features: {"description"} -> 201 + feature.
func (s *server) createFeature(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Description string `json:"description"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := store.NewFeature(projDir, strings.TrimSpace(req.Description), today())
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), "feature "+f.Slug, f.Path()) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"slug": f.Slug, "done": false})
}

// featureDone handles POST features/{slug}/done.
func (s *server) featureDone(w http.ResponseWriter, r *http.Request) {
	s.featureSetDone(w, r, true)
}

// featureReopen handles POST features/{slug}/reopen: the un-ship path, so an
// accidental one-click ship is recoverable in-product.
func (s *server) featureReopen(w http.ResponseWriter, r *http.Request) {
	s.featureSetDone(w, r, false)
}

// featureSetDone moves a feature between active and shipped.
func (s *server) featureSetDone(w http.ResponseWriter, r *http.Request, done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	slug := r.PathValue("slug")
	if !nameOK.MatchString(slug) {
		writeErr(w, http.StatusNotFound, fmt.Errorf("invalid feature %q", slug))
		return
	}
	feats, err := store.LoadFeatures(projDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	verb := "feature done "
	if !done {
		verb = "feature reopen "
	}
	for _, f := range feats {
		if f.Slug != slug {
			continue
		}
		if f.Done == done {
			state := "already done"
			if !done {
				state = "not shipped"
			}
			writeErr(w, http.StatusConflict, fmt.Errorf("%s is %s", f.File, state))
			return
		}
		nf, err := f.SetDone(done)
		if err != nil {
			code := http.StatusInternalServerError
			if strings.Contains(err.Error(), "refusing to overwrite") {
				code = http.StatusConflict
			}
			writeErr(w, code, err)
			return
		}
		if !s.commitOK(w, r.PathValue("p"), verb+nf.Slug, f.Path(), nf.Path()) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"slug": nf.Slug, "done": done})
		return
	}
	writeErr(w, http.StatusNotFound, fmt.Errorf("no feature %q", slug))
}

// today stamps mutations with the same date format as the CLI.
func today() string { return time.Now().Format("2006-01-02") }
