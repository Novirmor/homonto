package assignment

import "github.com/noviopenworks/homonto/internal/protocol"

// SplitRepairActions separates the repair-limit decision from repair
// assignments of the current generation.
func SplitRepairActions(actions []Action) (gate *Action, rest []Action) {
	for i := range actions {
		if actions[i].Kind == protocol.KindDecision {
			gate = &actions[i]
			continue
		}
		rest = append(rest, actions[i])
	}
	return gate, rest
}
