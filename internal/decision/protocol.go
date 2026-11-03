package decision

import (
	"fmt"
	"maps"
	"slices"
	"sync"
)

// Protocol is one provider wire format. It keeps the protocol's own request
// and response shapes out of the shared API: Encode builds the native
// request for one Request, Decode maps the native response back. Adding a
// protocol means implementing this interface and registering it; nothing
// else in homonto changes (ADR 0101).
type Protocol interface {
	// Name is the value of [decisions.providers.<name>].protocol.
	Name() string
	// DefaultEndpoint is used when the provider names none; empty means the
	// endpoint is required.
	DefaultEndpoint() string
	// Supports rejects a request this protocol cannot express, before any
	// network call.
	Supports(req Request) error
	// Encode returns the request body.
	Encode(model string, req Request) ([]byte, error)
	// Decode maps a 200 response body to an answer.
	Decode(body []byte, req Request) (Answer, error)
}

// Answer is a decoded response, before the provider name is attached.
type Answer struct {
	Result Result
	// Refused is set when the model declined this question.
	Refused bool
	// Model is the model the provider says served the call, when it says.
	Model string
	Usage Usage
}

// Usage is what the provider reports about cost.
type Usage struct {
	InputTokens  int      `json:"inputTokens,omitempty"`
	OutputTokens int      `json:"outputTokens,omitempty"`
	CostUSD      *float64 `json:"costUSD,omitempty"`
}

var (
	protocolsMu sync.RWMutex
	protocols   = map[string]Protocol{}
)

// RegisterProtocol makes a protocol available to configuration. Registering
// a name twice panics: two wire formats under one name would be ambiguous.
func RegisterProtocol(p Protocol) {
	protocolsMu.Lock()
	defer protocolsMu.Unlock()
	if _, dup := protocols[p.Name()]; dup {
		panic(fmt.Sprintf("decision: protocol %q registered twice", p.Name()))
	}
	protocols[p.Name()] = p
}

// LookupProtocol returns the registered protocol with name.
func LookupProtocol(name string) (Protocol, bool) {
	protocolsMu.RLock()
	defer protocolsMu.RUnlock()
	p, ok := protocols[name]
	return p, ok
}

// Protocols lists the registered protocol names, sorted.
func Protocols() []string {
	protocolsMu.RLock()
	defer protocolsMu.RUnlock()
	return slices.Sorted(maps.Keys(protocols))
}

func init() {
	RegisterProtocol(SystemOne{})
	RegisterProtocol(OpenAIDecisions{})
}

// questionName is the single question each call carries. Both protocols
// batch named questions; homonto sends one per decision so failures and
// records stay per point.
const questionName = "answer"

// levelToScale maps a probability-weighted level position in [0, n-1] onto
// the request's scale.
func levelToScale(position float64, req Request) (float64, error) {
	top := float64(len(req.Levels) - 1)
	if isBad(position) || position < 0 || position > top {
		return 0, fmt.Errorf("score position %v is outside the %d levels", position, len(req.Levels))
	}
	return req.Scale.Min + position/top*(req.Scale.Max-req.Scale.Min), nil
}

func requireLevels(protocol string, req Request) error {
	if req.Kind == Score && len(req.Levels) == 0 {
		return fmt.Errorf("decision %s: protocol %s scores on named levels; give the request 2 to 10 levels, lowest first", req.Point, protocol)
	}
	return nil
}

// contextText renders the evidence as labelled sections in key order.
func contextText(ctx map[string]string) string {
	out := ""
	for _, k := range slices.Sorted(maps.Keys(ctx)) {
		out += k + ":\n" + ctx[k] + "\n\n"
	}
	return out
}
