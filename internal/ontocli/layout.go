package ontocli

import (
	"path/filepath"

	"github.com/Novirmor/homonto/internal/workcli"
)

func workflowRoot(root string) string { return workcli.WorkflowRootOrDefault(root) }

func changesDir(root string) string { return filepath.Join(workflowRoot(root), "changes") }

func ontoArchiveDir(root string) string { return filepath.Join(changesDir(root), "archive") }
