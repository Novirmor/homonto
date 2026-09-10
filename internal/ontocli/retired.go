package ontocli

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

// retiredMigrationState identifies a receipt-listed abandoned record without
// using its historical change name as identity.
func retiredMigrationState(root, changeDir string, state ontostate.State) (bool, error) {
	return migrationrecord.IsRetired(workflowRoot(root), changeDir, state.ID)
}

func rejectRetiredMigrationState(root, changeDir string, state ontostate.State) error {
	retired, err := retiredMigrationState(root, changeDir, state)
	if err != nil {
		return fmt.Errorf("retired migration record: %w", err)
	}
	if retired {
		return fmt.Errorf("retired migration record is audit-only")
	}
	return nil
}
