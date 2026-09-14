package ontocli

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

// retiredMigrationState identifies a receipt-listed abandoned record without
// using its historical change name as identity.
func retiredMigrationState(root, changeDir string, state ontostate.State) (bool, error) {
	retired, err := migrationrecord.IsRetired(workflowRoot(root), changeDir, state.ID)
	if err != nil || !retired {
		return retired, err
	}
	schemaVersion, err := ontostate.RawSchemaVersion(changeDir)
	if err != nil {
		return false, err
	}
	return migrationrecord.IsRetired(workflowRoot(root), changeDir, state.ID, schemaVersion)
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
