package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// Number allocation is a check-then-act, so beyond s.mu (other handlers) it
// takes the cross-process store lock: a CLI invocation racing this handler
// would otherwise mint the same number.
func (s *server) createTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := store.AcquireLock(s.home)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	defer lock.Release()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Description string `json:"description"`
		Lane        string `json:"lane"`
		Feature     string `json:"feature"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Resolve the target feature before minting a number, so a bogus slug
	// cannot leave an unlinked task behind.
	var feat *store.Feature
	if req.Feature != "" {
		f, err := findFeatureSlug(projDir, req.Feature)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		feat = &f
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
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "already used") {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	msg, paths := "open "+t.Stem(), []string{t.Path()}
	if feat != nil {
		nf, err := feat.SetTasks(append(feat.Tasks, t.Num))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		msg += " (feature " + nf.Slug + ")"
		paths = append(paths, nf.Path())
	}
	if !s.commitOK(w, r.PathValue("p"), msg, paths...) {
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

// setLane handles POST tasks/{n}/lane: {"lane"} moves the task between
// lanes ("" or "-" clears), preserving number, slug, status, and deferral --
// the web twin of the lane command.
func (s *server) setLane(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Lane string `json:"lane"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lane := strings.TrimSpace(req.Lane)
	if lane == "-" {
		lane = ""
	}
	if lane != "" {
		lane = task.Slugify(lane)
		if lane == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("lane yields an empty token"))
			return
		}
	}
	t, err := findByKey(projDir, r.PathValue("n"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	nt, err := t.SetLane(lane)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "already has lane") {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	verb := "lane " + lane
	if lane == "" {
		verb = "clear lane"
	}
	if !s.commitOK(w, r.PathValue("p"), fmt.Sprintf("%s %s", verb, nt.Stem()), t.Path(), nt.Path()) {
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

// resumeTask handles POST tasks/{n}/resume. A live decision must be answered
// through the answer route, never silently dropped by a plain resume --
// same contract as the CLI.
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
	if body, err := os.ReadFile(t.Path()); err == nil {
		if _, live := task.ParseDecision(string(body)); live {
			writeErr(w, http.StatusConflict,
				fmt.Errorf("this task has an unanswered decision; answer it instead of resuming"))
			return
		}
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

// answerDecision handles POST tasks/{n}/answer: {"choice"} picks a labelled
// option, {"other"} answers free-text when allowed. Answering records the
// choice in the body, lifts the deferral, and promotes the task to the top
// of the priority order -- one scoped commit.
func (s *server) answerDecision(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Choice string `json:"choice"`
		Other  string `json:"other"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
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
	// Only a POSED decision is answerable: deferred plus a live block. The
	// already-answered case stays a 409 whatever the status (a stale writer
	// racing the winner); a non-deferred body carrying a live block is
	// documentation of the format, and answering would corrupt it.
	d, live := task.ParseDecision(string(body))
	if !live && task.HasAnsweredDecision(string(body)) {
		writeErr(w, http.StatusConflict, fmt.Errorf("this decision was already answered"))
		return
	}
	if !t.Deferred {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("this task is not deferred; nothing awaits an answer"))
		return
	}
	if !live {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("this task has no unanswered decision"))
		return
	}
	chosen, note := strings.TrimSpace(req.Choice), strings.TrimSpace(req.Other)
	switch {
	case chosen != "":
		valid := false
		for _, opt := range d.Options {
			if opt.Label == chosen {
				valid = true
				break
			}
		}
		if !valid {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%q is not one of the options", chosen))
			return
		}
	case note != "":
		if !d.AllowOther {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("this decision does not allow a free-text answer"))
			return
		}
		chosen = "Other"
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("pass a choice or a free-text other"))
		return
	}
	if err := t.AnswerDecision(chosen, note, today()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	nt, err := t.Resume(today())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	op, err := store.PromoteToTop(projDir, nt.Num)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"),
		fmt.Sprintf("answer decision on %s (%s)", nt.Stem(), chosen), t.Path(), nt.Path(), op) {
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
	// Normalize at the write boundary: the order file is git-tracked, so
	// numbers with no task (stale or malformed clients) must not persist as
	// cruft. Valid entries keep their given sequence; duplicates collapse.
	tasks, _, err := loadTasks(projDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	valid := map[int]bool{}
	for _, tk := range tasks {
		if tk.HasNum {
			valid[tk.Num] = true
		}
	}
	filtered := make([]int, 0, len(req.Order))
	seen := map[int]bool{}
	for _, n := range req.Order {
		if valid[n] && !seen[n] {
			seen[n] = true
			filtered = append(filtered, n)
		}
	}
	path, err := store.WriteOrder(projDir, filtered)
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

// featureTasks handles PUT features/{slug}/tasks: {"tasks":[12,19]} rewrites
// the feature's Tasks: line -- the link/unlink path that makes the features
// map populatable without a text editor.
func (s *server) featureTasks(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Tasks []int `json:"tasks"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := findFeatureSlug(projDir, r.PathValue("slug"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	nf, err := f.SetTasks(req.Tasks)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), "feature tasks "+nf.Slug, nf.Path()) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": nf.Slug, "tasks": nf.Tasks})
}

// findFeatureSlug resolves an exact feature slug in the project.
func findFeatureSlug(projDir, slug string) (store.Feature, error) {
	if !nameOK.MatchString(slug) {
		return store.Feature{}, fmt.Errorf("invalid feature %q", slug)
	}
	feats, err := store.LoadFeatures(projDir)
	if err != nil {
		return store.Feature{}, err
	}
	for _, f := range feats {
		if f.Slug == slug {
			return f, nil
		}
	}
	return store.Feature{}, fmt.Errorf("no feature %q", slug)
}

// editTask handles PUT tasks/{n}: {"title"?, "body"?} rewrites the task file
// (body is the full raw markdown, exactly what GET returns) and/or retitles
// it -- H1 restamped and file renamed to the new slug with number, lane,
// status, and deferral kept, refusing to clobber. Tasks were create-only
// from the UI before this.
func (s *server) editTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Title == "" && req.Body == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("nothing to edit: pass title and/or body"))
		return
	}
	t, err := findByKey(projDir, r.PathValue("n"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	paths := []string{t.Path()}
	if req.Body != "" {
		body := strings.TrimRight(req.Body, "\n") + "\n"
		if err := os.WriteFile(t.Path(), []byte(body), 0o644); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if req.Title != "" {
		nt, err := t.Retitle(strings.TrimSpace(req.Title))
		if err != nil {
			code := http.StatusBadRequest
			if strings.Contains(err.Error(), "refusing to overwrite") ||
				strings.Contains(err.Error(), "already used") {
				code = http.StatusConflict
			}
			writeErr(w, code, err)
			return
		}
		paths = append(paths, nt.Path())
		t = nt
	}
	if !s.commitOK(w, r.PathValue("p"), "edit "+t.Stem(), paths...) {
		return
	}
	writeJSON(w, http.StatusOK, toJSON(t))
}

// editFeature handles PUT features/{slug}: {"body"} replaces the spec's raw
// markdown (the Tasks: line rides inside it, exactly as on disk). The title
// and slug stay immutable here -- a slug rename ripples through deep links
// and chips and stays a separate step if ever wanted.
func (s *server) editFeature(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projDir, err := s.projDir(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("nothing to edit: pass a body"))
		return
	}
	f, err := findFeatureSlug(projDir, r.PathValue("slug"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	body := strings.TrimRight(req.Body, "\n") + "\n"
	if err := os.WriteFile(f.Path(), []byte(body), 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), "edit feature "+f.Slug, f.Path()) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": f.Slug, "done": f.Done})
}

// deleteFeature handles DELETE features/{slug}: discard the spec file
// (active or shipped) with one scoped removal commit -- undoable via the
// project undo. Linked tasks are independent files and stay untouched.
func (s *server) deleteFeature(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if err := f.Remove(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), "remove feature "+f.Slug, f.Path()) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// undoable reports whether a commit subject is one this store minted for the
// project (a taskman mutation or a previous undo of one); anything else --
// hand commits, seeds, other tools -- is not ours to revert.
func undoable(subject, project string) bool {
	return strings.HasPrefix(subject, "chore("+project+"):") ||
		strings.HasPrefix(subject, `Revert "chore(`+project+`):`)
}

// undoTarget resolves the project's newest commit and vets it.
func (s *server) undoTarget(w http.ResponseWriter, r *http.Request) (hash, subject string, ok bool) {
	if _, err := s.projDir(r); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return "", "", false
	}
	p := r.PathValue("p")
	hash, subject, err := store.LastProjectCommit(s.home, p)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return "", "", false
	}
	if !undoable(subject, p) {
		writeErr(w, http.StatusConflict,
			fmt.Errorf("refusing to undo %q: not a taskman mutation for this project", subject))
		return "", "", false
	}
	return hash, subject, true
}

// undoPeek handles GET undo: what WOULD be undone, so the client can confirm
// with the user and pass the hash back as its staleness guard.
func (s *server) undoPeek(w http.ResponseWriter, r *http.Request) {
	hash, subject, ok := s.undoTarget(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"commit": hash, "subject": subject})
}

// undo handles POST undo: {"commit"?} reverts the project's newest taskman
// commit as its own revert commit. A supplied commit hash must still be the
// newest one -- the store is multi-writer, and undoing something other than
// what the user confirmed would be worse than refusing.
func (s *server) undo(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var req struct {
		Commit string `json:"commit"`
	}
	if err := readBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	hash, subject, ok := s.undoTarget(w, r)
	if !ok {
		return
	}
	if req.Commit != "" && req.Commit != hash {
		writeErr(w, http.StatusConflict,
			fmt.Errorf("the project changed since you looked; refresh and retry"))
		return
	}
	if err := store.Revert(s.home, hash); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	fmt.Println("reverted:", subject)
	writeJSON(w, http.StatusOK, map[string]string{"reverted": hash, "subject": subject})
}

// today stamps mutations with the same date format as the CLI.
func today() string { return time.Now().Format("2006-01-02") }
