package decision

import (
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

func TestRequestShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		want string
	}{
		{"choice", Request{Point: "onto.next-step", Kind: Choice, Question: "Next?", Options: []string{"verify", "repair"}}, ""},
		{"yes-no", Request{Point: "h-workflow-gh.needs-design", Kind: YesNo, Question: "Needs a design?"}, ""},
		{"score", Request{Point: "no.risk", Kind: Score, Question: "Risk?", Scale: &Scale{Min: 0, Max: 10}}, ""},
		{"bad point", Request{Point: "risk", Kind: YesNo, Question: "q"}, "<package>.<point>"},
		{"one option", Request{Point: "a.b", Kind: Choice, Question: "q", Options: []string{"x"}}, "at least two options"},
		{"duplicate options", Request{Point: "a.b", Kind: Choice, Question: "q", Options: []string{"x", "x"}}, "distinct"},
		{"yes-no with options", Request{Point: "a.b", Kind: YesNo, Question: "q", Options: []string{"x", "y"}}, "takes no options"},
		{"score without scale", Request{Point: "a.b", Kind: Score, Question: "q"}, "min < max"},
		{"unknown kind", Request{Point: "a.b", Kind: "rank", Question: "q"}, `unknown kind "rank"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResultMustAnswerItsRequest(t *testing.T) {
	choice := Request{Point: "a.b", Kind: Choice, Question: "q", Options: []string{"x", "y"}}
	yesno := Request{Point: "a.b", Kind: YesNo, Question: "q"}
	score := Request{Point: "a.b", Kind: Score, Question: "q", Scale: &Scale{Min: 1, Max: 5}}
	for _, tc := range []struct {
		name string
		req  Request
		res  Result
		ok   bool
	}{
		{"choice", choice, Result{Kind: Choice, Choice: "y", Provider: "p"}, true},
		{"choice outside options", choice, Result{Kind: Choice, Choice: "z", Provider: "p"}, false},
		{"yes-no", yesno, Result{Kind: YesNo, Probability: f(0.7), Confidence: f(0.9), Provider: "p"}, true},
		{"yes-no out of range", yesno, Result{Kind: YesNo, Probability: f(1.2), Provider: "p"}, false},
		{"score", score, Result{Kind: Score, Score: f(4), Provider: "p"}, true},
		{"score off scale", score, Result{Kind: Score, Score: f(6), Provider: "p"}, false},
		{"wrong kind", score, Result{Kind: YesNo, Probability: f(0.5), Provider: "p"}, false},
		{"extra field", yesno, Result{Kind: YesNo, Probability: f(0.5), Score: f(1), Provider: "p"}, false},
		{"no provider", yesno, Result{Kind: YesNo, Probability: f(0.5)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.res.Validate(tc.req); (err == nil) != tc.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
