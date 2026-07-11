package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/freeeve/taskman/internal/task"
)

// maxShot caps uploads; screenshots are working notes, not archives.
const maxShot = 10 << 20

// shotExt maps sniffed content types to extensions; anything else is
// rejected -- the endpoint stores images, not arbitrary files.
var shotExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// uploadScreenshot handles POST tasks/{n}/screenshots: a multipart "file"
// field lands in <project>/screenshots/<NNN>/ (a directory keyed by the bare
// number, stable across renames and lane moves), and the task body gains a
// dated section linking it. Image and body change ride one commit.
//
// Screenshots live outside tasks/ on purpose: agents work the tasks/ files
// and never need to read image bytes -- the link is for this UI.
func (s *server) uploadScreenshot(w http.ResponseWriter, r *http.Request) {
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
	if !t.HasNum {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("adopt %s before attaching screenshots", t.File))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxShot)
	file, _, err := r.FormFile("file")
	if err != nil {
		code := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			code = http.StatusRequestEntityTooLarge
		}
		writeErr(w, code, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ext, ok := shotExt[http.DetectContentType(data)]
	if !ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unsupported upload type %q", http.DetectContentType(data)))
		return
	}
	dir := filepath.Join(projDir, "screenshots", fmt.Sprintf("%03d", t.Num))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	name, path, err := freeShotName(dir, time.Now().Format("20060102-150405"), ext, data)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rel := fmt.Sprintf("../screenshots/%03d/%s", t.Num, name)
	if err := task.AppendSection(t.Path(), "Screenshot "+today(),
		fmt.Sprintf("![screenshot](%s)", rel)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !s.commitOK(w, r.PathValue("p"), "screenshot for "+t.Stem(), path, t.Path()) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"path": fmt.Sprintf("screenshots/%03d/%s", t.Num, name),
	})
}

// freeShotName writes data under base+ext, suffixing -2, -3, ... when a
// paste burst lands several shots in one second.
func freeShotName(dir, base, ext string, data []byte) (string, string, error) {
	for i := 1; ; i++ {
		name := base + ext
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return name, path, err
	}
}

// serveScreenshot handles GET /shots/{p}/{n}/{file}. Every segment is
// validated before touching the filesystem; the mux has already cleaned the
// path, so a name with separators or a dot prefix simply cannot resolve.
func (s *server) serveScreenshot(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("p")
	n, err := strconv.Atoi(r.PathValue("n"))
	file := r.PathValue("file")
	if !nameOK.MatchString(p) || err != nil || n <= 0 ||
		file == "" || strings.HasPrefix(file, ".") || strings.ContainsAny(file, `/\`) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.home, p, "screenshots", fmt.Sprintf("%03d", n), file))
}
