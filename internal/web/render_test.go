package web

import (
	"strings"
	"testing"
)

// TestRenderBodyTaskRefs covers linkifying same-project "task NNN" references:
// existing tasks become hash links, missing numbers and cross-project
// references stay plain text, and code spans are left literal.
func TestRenderBodyTaskRefs(t *testing.T) {
	refs := map[int]bool{174: true, 30: true}
	cases := []struct {
		name, md, want, notWant string
	}{
		{"existing ref links", "See task 174 for details.", `href="#/p/proj/task/174"`, ""},
		{"display text kept", "See task 174.", `>task 174</a>`, ""},
		{"missing task stays text", "See task 999.", "", "task-ref"},
		{"cross-project prefix untouched", "Rebench ragedb 064 later.", "", "task-ref"},
		{"case and zero-padding", "Blocked by Task 030.", `href="#/p/proj/task/30"`, ""},
		{"code span left literal", "Run `task 174` verbatim.", "", "task-ref"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html, err := renderBody([]byte(c.md), "proj", refs)
			if err != nil {
				t.Fatal(err)
			}
			if c.want != "" && !strings.Contains(html, c.want) {
				t.Errorf("want %q in:\n%s", c.want, html)
			}
			if c.notWant != "" && strings.Contains(html, c.notWant) {
				t.Errorf("did not want %q in:\n%s", c.notWant, html)
			}
		})
	}
}

// TestRenderBodyNoDoubleLink checks a reference already inside a markdown link
// is not re-linked.
func TestRenderBodyNoDoubleLink(t *testing.T) {
	html, err := renderBody([]byte("[task 174](https://example.com)"), "proj", map[int]bool{174: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "task-ref") {
		t.Errorf("reference inside an existing link was re-linked:\n%s", html)
	}
	if n := strings.Count(html, "<a "); n != 1 {
		t.Errorf("expected exactly one anchor, got %d:\n%s", n, html)
	}
}

// TestRenderBodyRefsDisabled checks nil refs (e.g. search snippets) disables
// task-reference linking entirely.
func TestRenderBodyRefsDisabled(t *testing.T) {
	html, err := renderBody([]byte("See task 174."), "proj", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "task-ref") {
		t.Errorf("nil refs should disable linking:\n%s", html)
	}
}
