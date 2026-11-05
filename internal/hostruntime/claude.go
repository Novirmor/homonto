package hostruntime

import (
	"errors"
	"slices"
)

// Dialect is the native question shape of the harness a Service serves.
type Dialect string

const (
	// DialectOpenCode is OpenCode's question tool: {header, question, options, multiple}.
	DialectOpenCode Dialect = ""
	// DialectClaude is Claude Code's AskUserQuestion: {question, header, options, multiSelect}.
	DialectClaude Dialect = "claude"
)

// AskUserQuestion takes one to four questions.
const claudeMaxQuestions = 4

type claudeQuestion struct {
	Question    string   `json:"question"`
	Header      string   `json:"header"`
	Options     []option `json:"options"`
	MultiSelect bool     `json:"multiSelect"`
}

type claudeQuestions struct {
	Questions []claudeQuestion `json:"questions"`
}

// SetDialect selects the native question shape. It must be set before the
// first draft is staged.
func (s *Service) SetDialect(d Dialect) { s.dialect = d }

func (s *Service) nativeQuestion(d *draft) any {
	if s.dialect != DialectClaude {
		return d.question
	}
	return claudeQuestionsOf(d)
}

func claudeQuestionsOf(d *draft) claudeQuestions {
	out := claudeQuestions{Questions: make([]claudeQuestion, len(d.question.Questions))}
	for n, q := range d.question.Questions {
		out.Questions[n] = claudeQuestion{Question: q.Question, Header: q.Header, Options: q.Options}
	}
	return out
}

// ErrPreAnswered refuses an AskUserQuestion call that carries its own
// answers: only the user may answer a publication question.
var ErrPreAnswered = errors.New("[question_preanswered] AskUserQuestion input must not carry answers or annotations; submit the draft's question arguments unchanged")

// ClaudeBefore correlates a PreToolUse(AskUserQuestion) from the coordinator
// with the session's current draft. It returns ErrPreAnswered for a call that
// would answer itself, which the hook turns into a denial.
func (s *Service) ClaudeBefore(sessionID, toolUseID string, input any) error {
	s.expire()
	in, ok := record(input)
	if !ok {
		return nil
	}
	for _, k := range []string{"answers", "annotations"} {
		if v, has := in[k]; has {
			if m, ok := record(v); !ok || len(m) > 0 {
				return ErrPreAnswered
			}
		}
	}
	if s.disposed || toolUseID == "" || len(toolUseID) > 256 {
		return nil
	}
	sess, d := s.currentDraft(sessionID)
	if sess == nil || sess.deleted || d == nil || d.answered || d.callID != "" || !allPending(d) ||
		s.requests[toolUseID] || len(s.requests) >= maxRequests || !exactJSON(in["questions"], claudeQuestionsOf(d).Questions) {
		return nil
	}
	d.callID, d.requestID = toolUseID, toolUseID
	s.requests[toolUseID] = true
	return nil
}

// ClaudeAnswered applies a PostToolUse(AskUserQuestion) result: answers keyed
// by question text. Anything but an exact option label declines the item.
func (s *Service) ClaudeAnswered(sessionID, toolUseID string, response any) {
	s.expire()
	sess, d := s.currentDraft(sessionID)
	if s.disposed || sess == nil || sess.deleted || d == nil || d.answered || d.requestID == "" || d.requestID != toolUseID {
		return
	}
	d.answered = true
	r, ok := record(response)
	answers, aok := record(r["answers"])
	valid := ok && aok && exactJSON(r["questions"], claudeQuestionsOf(d).Questions)
	for n, i := range d.items {
		q := d.question.Questions[n]
		label, _ := text(answers[q.Question])
		known := valid && slices.ContainsFunc(q.Options, func(o option) bool { return o.Label == label })
		switch {
		case !known || label == "Decline":
			i.Status = "declined"
		case label == "Revise":
			i.Status = "revision-requested"
		default:
			i.Status = "approved"
		}
	}
}

// ClaudeFailed declines a draft whose correlated question was rejected or
// failed (PostToolUseFailure).
func (s *Service) ClaudeFailed(sessionID, toolUseID string) {
	s.expire()
	sess, d := s.currentDraft(sessionID)
	if s.disposed || sess == nil || d == nil || d.answered || d.requestID == "" || d.requestID != toolUseID {
		return
	}
	d.answered = true
	for _, i := range d.items {
		i.Status = "declined"
	}
}

// SessionDeleted applies a session end.
func (s *Service) SessionDeleted(sessionID string) {
	s.Event("session.deleted", map[string]any{"sessionID": sessionID})
}
