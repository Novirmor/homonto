package agentfm

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ValidateExpertWorker is the opt-in BYO advisory contract. Only these validated
// roles receive expert workspace context; other custom agents remain unchanged.
func ValidateExpertWorker(content []byte) error {
	fm, _, ok := split(content)
	if !ok {
		return fmt.Errorf("expert worker requires neutral homonto frontmatter")
	}
	h, present, err := parseHomonto(fm)
	if err != nil {
		return err
	}
	if !present || !h.ReadOnly || h.Primary || h.Bash == nil || *h.Bash || h.Dialogs || h.Spawn == nil || len(*h.Spawn) != 0 {
		return fmt.Errorf("expert worker requires read_only: true, bash: false, dialogs: false, spawn: [] and no primary capability")
	}
	var fields map[string]any
	if err := yaml.Unmarshal(fm, &fields); err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "name", "description", "mode", "homonto":
		default:
			return fmt.Errorf("expert worker field %q is not part of the advisory contract; use model routing in homonto.toml", key)
		}
	}
	return nil
}
