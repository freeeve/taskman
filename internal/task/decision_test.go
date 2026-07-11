package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decisionTask writes a deferred task file and returns it parsed.
func decisionTask(t *testing.T) Task {
	t.Helper()
	dir := ledger(t)
	path := filepath.Join(dir, "005_pick-an-approach.deferred.md")
	if err := os.WriteFile(path,
		[]byte("# 005 -- Pick an approach\n\nBody prose.\n\n## Deferred 2026-07-11\n\ndecision needed\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	tasks, _ := Load(dir)
	tk, err := Find(tasks, "5")
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func sample() Decision {
	return Decision{
		Question: "Retry inline or queue?",
		Options: []DecisionOption{
			{Label: "Retry inline", Explain: "Simpler; blocks the run."},
			{Label: "Queue later", Explain: "Keeps the run moving."},
		},
		AllowOther: true,
	}
}

// TestDecisionRoundTrip pins pose -> parse -> answer: the block is legible
// both ways, one live question at a time, and answering preserves the
// question as an answered record.
func TestDecisionRoundTrip(t *testing.T) {
	tk := decisionTask(t)
	if err := tk.PoseDecision(sample()); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(tk.Path())
	d, live := ParseDecision(string(body))
	if !live || d.Question != "Retry inline or queue?" || len(d.Options) != 2 ||
		d.Options[0].Label != "Retry inline" || d.Options[0].Explain != "Simpler; blocks the run." ||
		!d.AllowOther {
		t.Fatalf("parsed = %+v (live %v)", d, live)
	}
	if strings.Contains(string(body), "Body prose.") == false {
		t.Error("posing must not disturb the body")
	}

	// A second live question is refused, not stacked.
	if err := tk.PoseDecision(sample()); err == nil {
		t.Error("double pose must error")
	}

	// Answering rewrites the block into an answered record.
	if err := tk.AnswerDecision("Queue later", "prefer durable", "2026-07-11"); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(tk.Path())
	if _, live := ParseDecision(string(body)); live {
		t.Error("no live decision may remain after answering")
	}
	if !HasAnsweredDecision(string(body)) {
		t.Error("answered record missing")
	}
	s := string(body)
	if !strings.Contains(s, "chosen: Queue later") || !strings.Contains(s, "note: prefer durable") ||
		!strings.Contains(s, "question: Retry inline or queue?") {
		t.Errorf("answered block:\n%s", s)
	}

	// Answering again is a stale write.
	if err := tk.AnswerDecision("Retry inline", "", "2026-07-11"); err == nil {
		t.Error("answering with no live decision must error")
	}
}

func TestPoseDecisionValidation(t *testing.T) {
	tk := decisionTask(t)
	if err := tk.PoseDecision(Decision{Question: "q", Options: []DecisionOption{{Label: "only"}}}); err == nil {
		t.Error("one option must be refused")
	}
	if err := tk.PoseDecision(Decision{Options: sample().Options}); err == nil {
		t.Error("empty question must be refused")
	}
	if err := tk.PoseDecision(Decision{Question: "q",
		Options: []DecisionOption{{Label: "a"}, {Label: "  "}}}); err == nil {
		t.Error("blank label must be refused")
	}
}

// FuzzParseDecision pins leniency: arbitrary bodies never panic, and any
// parsed decision honors the invariants (question, >=2 labelled options).
func FuzzParseDecision(f *testing.F) {
	f.Add("```decision\nquestion: q\noptions:\n- label: a\n- label: b\nallow_other: false\n```")
	f.Add("```decision answered 2026-07-11\nquestion: q\nchosen: a\n```")
	f.Add("```decision\nquestion:\noptions:\n```")
	f.Add("no block at all")
	f.Fuzz(func(t *testing.T, body string) {
		d, live := ParseDecision(body)
		if !live {
			return
		}
		if d.Question == "" || len(d.Options) < 2 {
			t.Errorf("invalid decision accepted: %+v", d)
		}
		for _, opt := range d.Options {
			if opt.Label == "" {
				t.Errorf("empty label accepted: %+v", d)
			}
		}
	})
}
