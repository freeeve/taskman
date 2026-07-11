// Package web serves the taskman store as a kanban web app: a JSON API plus
// an embedded vanilla HTML/CSS/JS board. Handlers are stateless -- every
// request re-reads the store from disk, so the CLI and the UI never fight
// over cached state, and every mutation goes through the same internal/task
// and internal/store code paths (and commits) as the CLI.
package web

import (
	"embed"
	"fmt"
	"net"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// Handler builds the full route table over the store at home. Separated from
// Serve so tests can drive it with httptest.
func Handler(home string) http.Handler {
	s := &server{home: home}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", s.projects)
	mux.HandleFunc("GET /api/projects/{p}/tasks", s.tasks)
	mux.HandleFunc("GET /api/projects/{p}/tasks/{n}", s.taskDetail)
	mux.HandleFunc("GET /api/projects/{p}/features", s.features)
	mux.HandleFunc("POST /api/projects/{p}/tasks", s.createTask)
	mux.HandleFunc("POST /api/projects/{p}/tasks/{n}/status", s.setStatus)
	mux.HandleFunc("POST /api/projects/{p}/tasks/{n}/defer", s.deferTask)
	mux.HandleFunc("POST /api/projects/{p}/tasks/{n}/resume", s.resumeTask)
	mux.HandleFunc("PUT /api/projects/{p}/order", s.setOrder)
	mux.HandleFunc("POST /api/projects/{p}/features", s.createFeature)
	mux.HandleFunc("POST /api/projects/{p}/features/{slug}/done", s.featureDone)
	mux.HandleFunc("POST /api/projects/{p}/features/{slug}/reopen", s.featureReopen)
	mux.HandleFunc("PUT /api/projects/{p}/features/{slug}/tasks", s.featureTasks)
	mux.HandleFunc("GET /api/projects/{p}/activity", s.activity)
	mux.HandleFunc("GET /api/projects/{p}/undo", s.undoPeek)
	mux.HandleFunc("POST /api/projects/{p}/undo", s.undo)
	mux.HandleFunc("POST /api/projects/{p}/tasks/{n}/screenshots", s.uploadScreenshot)
	mux.HandleFunc("GET /shots/{p}/{n}/{file}", s.serveScreenshot)
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "static/index.html")
	})
	return mux
}

// Serve runs the web app on addr. The API has no auth, so non-loopback binds
// are refused unless explicitly requested.
func Serve(addr, home string, insecureBind bool) error {
	if !insecureBind {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return fmt.Errorf("invalid -addr %q: %v", addr, err)
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return fmt.Errorf("refusing to bind %q without -insecure-bind: the API has no auth", addr)
		}
	}
	fmt.Printf("taskman store %s\nserving http://%s\n", home, addr)
	return http.ListenAndServe(addr, Handler(home))
}
