package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/gitx"
)

// Control describes a freshly created control repository.
type Control struct {
	// Root is the canonical absolute path of the control repository.
	Root string
	// Git carries the repository facts of the created repository.
	Git gitx.Repository
}

// commitIdentity keeps the bootstrap commit deterministic and independent
// of the invoking user's git configuration.
const (
	commitUserName  = "homonto"
	commitUserEmail = "homonto@localhost"
	commitMessage   = "homonto: initialize workspace control repository"
)

// CreateControlRepository turns the workspace root into the control
// repository: it refuses an already-initialized root, writes a .gitignore
// covering the .homonto runtime directory and every member directory (the
// control itself is never self-ignored), initializes git on branch main,
// and makes exactly one commit.
func CreateControlRepository(ctx context.Context, root string, members []Candidate, git gitx.Runner) (Control, error) {
	canon, err := CanonicalPath(root)
	if err != nil {
		return Control{}, err
	}
	if info, err := os.Stat(canon); err != nil || !info.IsDir() {
		return Control{}, fmt.Errorf("workspace: control root %s: not a directory", canon)
	}
	if _, err := os.Stat(filepath.Join(canon, ".git")); err == nil {
		return Control{}, fmt.Errorf("workspace: control root %s: already a git repository", canon)
	}

	entries := []string{".homonto"}
	for _, m := range members {
		if m.Path == canon {
			continue // the control repository is not a member to ignore
		}
		if !within(canon, m.Path) {
			return Control{}, fmt.Errorf("workspace: member %s is outside control root %s", m.Path, canon)
		}
		rel, err := filepath.Rel(canon, m.Path)
		if err != nil {
			return Control{}, fmt.Errorf("workspace: member %s: %w", m.Path, err)
		}
		entries = append(entries, filepath.ToSlash(rel)+"/")
	}
	sort.Strings(entries[1:])

	var ignore strings.Builder
	ignore.WriteString("# homonto runtime state\n.homonto/\n")
	if len(entries) > 1 {
		ignore.WriteString("\n# homonto workspace members\n")
		for _, e := range entries[1:] {
			ignore.WriteString(e + "\n")
		}
	}
	if err := os.WriteFile(filepath.Join(canon, ".gitignore"), []byte(ignore.String()), 0o644); err != nil {
		return Control{}, fmt.Errorf("workspace: write .gitignore: %w", err)
	}

	if err := gitx.Init(ctx, git, canon); err != nil {
		return Control{}, err
	}
	if _, err := git.Run(ctx, canon, "add", "--", ".gitignore"); err != nil {
		return Control{}, fmt.Errorf("workspace: stage .gitignore: %w", err)
	}
	if _, err := git.Run(ctx, canon,
		"-c", "user.name="+commitUserName,
		"-c", "user.email="+commitUserEmail,
		"-c", "commit.gpgsign=false",
		"commit", "-m", commitMessage); err != nil {
		return Control{}, fmt.Errorf("workspace: bootstrap commit: %w", err)
	}

	repo, isGit, err := gitx.Inspect(ctx, git, canon)
	if err != nil {
		return Control{}, err
	}
	if !isGit {
		return Control{}, fmt.Errorf("workspace: control root %s: not a repository after init", canon)
	}
	return Control{Root: canon, Git: repo}, nil
}
