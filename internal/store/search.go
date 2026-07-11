package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/freeeve/taskman/internal/task"
)

// SearchDoc is one indexed item: a task or a feature, anywhere in the store.
type SearchDoc struct {
	Project string `json:"project"`
	Kind    string `json:"kind"` // "task" | "feature"
	Num     int    `json:"num"`  // tasks only
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Lane    string `json:"lane"`
	body    string
}

// SearchResult is a ranked hit with a context snippet.
type SearchResult struct {
	SearchDoc
	Snippet string `json:"snippet"`
}

// SearchIndex is an in-memory inverted index over the whole store. Plain Go
// maps, deliberately: at hundreds of documents a roaring-bitmap index is
// complexity without payoff, and the interface stays identical if the corpus
// ever grows into one (recorded in docs/design.md).
type SearchIndex struct {
	Head  string // git HEAD at build time; the freshness token
	docs  []SearchDoc
	terms map[string][]int // term -> ascending doc ids
}

// GitHead returns the store's current HEAD hash ("" outside git) -- every
// mutation commits, so HEAD movement is the cheap staleness signal.
func GitHead(home string) string {
	out, err := exec.Command("git", "-C", home, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var tokenRE = regexp.MustCompile(`[a-z0-9]+`)

// tokenize folds text to lowercase alphanumeric terms, dropping one-letter
// noise.
func tokenize(text string) []string {
	all := tokenRE.FindAllString(strings.ToLower(text), -1)
	terms := all[:0]
	for _, t := range all {
		if len(t) >= 2 {
			terms = append(terms, t)
		}
	}
	return terms
}

// titleFromBody pulls the H1 (numbered prefix stripped) or falls back.
var titlePrefixRE = regexp.MustCompile(`^#\s*(?:\d+\s*(?:--|\x{2014}|\x{2013}| - )\s*)?`)

func titleFromBody(body, fallback string) string {
	first, _, _ := strings.Cut(body, "\n")
	if strings.HasPrefix(first, "# ") {
		if s := strings.TrimSpace(titlePrefixRE.ReplaceAllString(first, "")); s != "" {
			return s
		}
	}
	return fallback
}

// BuildIndex reads every task and feature in every project into a fresh
// index. Rebuild-from-scratch is the whole strategy: the corpus is small,
// and incremental indexing is bug surface with no payoff here.
func BuildIndex(home string) (*SearchIndex, error) {
	ix := &SearchIndex{Head: GitHead(home), terms: map[string][]int{}}
	names, err := Projects(home)
	if err != nil {
		return nil, err
	}
	add := func(doc SearchDoc) {
		id := len(ix.docs)
		ix.docs = append(ix.docs, doc)
		seen := map[string]bool{}
		for _, term := range tokenize(doc.Title + " " + doc.body) {
			if !seen[term] {
				seen[term] = true
				ix.terms[term] = append(ix.terms[term], id)
			}
		}
	}
	for _, name := range names {
		tasks, err := task.Load(filepath.Join(home, name, "tasks"))
		if err != nil && !os.IsNotExist(err) {
			continue
		}
		for _, t := range tasks {
			body, _ := os.ReadFile(t.Path())
			add(SearchDoc{
				Project: name, Kind: "task", Num: t.Num, Slug: t.Slug,
				Title: titleFromBody(string(body), t.Slug), Status: t.StatusLabel(),
				Lane: t.Lane, body: string(body),
			})
		}
		feats, err := LoadFeatures(filepath.Join(home, name))
		if err != nil {
			continue
		}
		for _, f := range feats {
			body, _ := os.ReadFile(f.Path())
			status := "active"
			if f.Done {
				status = "done"
			}
			add(SearchDoc{
				Project: name, Kind: "feature", Slug: f.Slug, Title: f.Title,
				Status: status, body: string(body),
			})
		}
	}
	return ix, nil
}

// postings returns the doc ids for a term; the final query term also matches
// as a prefix so search-as-you-type works.
func (ix *SearchIndex) postings(term string, prefix bool) []int {
	if !prefix {
		return ix.terms[term]
	}
	set := map[int]bool{}
	for t, ids := range ix.terms {
		if strings.HasPrefix(t, term) {
			for _, id := range ids {
				set[id] = true
			}
		}
	}
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// intersect ANDs two ascending id lists.
func intersect(a, b []int) []int {
	var out []int
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// Search ANDs the query terms (case-insensitive; last term as prefix) and
// ranks title matches first, then newer tasks.
func (ix *SearchIndex) Search(query string, limit int) []SearchResult {
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	ids := ix.postings(terms[0], len(terms) == 1)
	for i := 1; i < len(terms) && len(ids) > 0; i++ {
		ids = intersect(ids, ix.postings(terms[i], i == len(terms)-1))
	}
	type scored struct {
		id    int
		score int
	}
	ranked := make([]scored, 0, len(ids))
	for _, id := range ids {
		doc := ix.docs[id]
		title := strings.ToLower(doc.Title)
		s := 0
		for _, term := range terms {
			if strings.Contains(title, term) {
				s += 3
			}
		}
		ranked = append(ranked, scored{id, s})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ix.docs[ranked[i].id].Num > ix.docs[ranked[j].id].Num
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]SearchResult, 0, len(ranked))
	for _, r := range ranked {
		doc := ix.docs[r.id]
		out = append(out, SearchResult{SearchDoc: doc, Snippet: snippet(doc.body, terms)})
	}
	return out
}

// snippet extracts a short window around the first term occurrence.
func snippet(body string, terms []string) string {
	lower := strings.ToLower(body)
	at := -1
	for _, term := range terms {
		if i := strings.Index(lower, term); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at < 0 {
		at = 0
	}
	start := max(at-50, 0)
	end := min(at+90, len(body))
	s := strings.Join(strings.Fields(body[start:end]), " ")
	if start > 0 {
		s = "..." + s
	}
	if end < len(body) {
		s += "..."
	}
	return s
}
