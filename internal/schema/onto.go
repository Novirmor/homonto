package schema

import "fmt"

// OntoStateVersion is shared by the lifecycle and worktree readers, which
// cannot depend on one another. Only the live onto state format is supported.
const OntoStateVersion = 3

// ValidateOntoState checks the format before either reader interprets state.
// A nil version distinguishes an unversioned file from an explicit version 0.
func ValidateOntoState(version *int, workflow, source string) error {
	if version == nil {
		return fmt.Errorf("onto-state: %s: unversioned state is no longer supported; finish or abandon the change with the onto release that created it", source)
	}
	if *version > OntoStateVersion {
		return fmt.Errorf("onto-state: %s: unknown schema_version %d (this binary supports %d) — upgrade onto: %w", source, *version, OntoStateVersion, ErrTooNew)
	}
	if *version != OntoStateVersion {
		return fmt.Errorf("onto-state: %s: schema_version %d is no longer supported (this binary reads only %d); finish or abandon the change with the onto release that created it", source, *version, OntoStateVersion)
	}
	if workflow != "" && workflow != "full" && workflow != "light" {
		return fmt.Errorf("onto-state: %s: workflow %q is not one of full|light; finish or abandon the change with the onto release that created it", source, workflow)
	}
	return nil
}
