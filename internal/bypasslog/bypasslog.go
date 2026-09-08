// Package bypasslog owns the versioned audit sidecars written by explicit
// workflow bypasses. Keeping this data out of workflow state prevents older
// binaries from silently dropping an audit record when they rewrite state.
package bypasslog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
)

const schemaVersion = 1

type Record struct {
	At      string   `json:"at"`
	Command string   `json:"command"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Reason  string   `json:"reason"`
	Skipped []string `json:"skipped"`
}

type Sidecar struct {
	SchemaVersion int      `json:"schemaVersion"`
	Change        string   `json:"change"`
	Framework     string   `json:"framework"`
	Records       []Record `json:"records"`
}

func Path(changeDir, framework string) string {
	return filepath.Join(changeDir, "."+framework, "bypass.json")
}

// Append writes one explicit bypass request. The sidecar is versioned and
// confined to the workspace so the reason remains available after archiving.
func Append(changeDir, change, framework string, record Record) error {
	if err := validateRecord(record); err != nil {
		return err
	}
	path := Path(changeDir, framework)
	if err := RequireRealParents(changeDir, filepath.Dir(path)); err != nil {
		return err
	}
	sc, exists, err := Load(path, change, framework)
	if err != nil {
		return err
	}
	if !exists {
		sc = &Sidecar{SchemaVersion: schemaVersion, Change: change, Framework: framework, Records: []Record{}}
	}
	sc.Records = append(sc.Records, record)
	return save(path, sc)
}

func Load(path, change, framework string) (*Sidecar, bool, error) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, false, fmt.Errorf("bypass: %s is not a regular file (symlinks are refused)", path)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, false, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var sc Sidecar
	if err := json.Unmarshal(b, &sc); err != nil {
		return nil, false, fmt.Errorf("bypass: %s is not valid JSON: %w", path, err)
	}
	if sc.SchemaVersion != schemaVersion {
		return nil, false, fmt.Errorf("bypass: %s schemaVersion %d is unsupported", path, sc.SchemaVersion)
	}
	if sc.Change != change || sc.Framework != framework {
		return nil, false, fmt.Errorf("bypass: %s belongs to %s/%s, not %s/%s", path, sc.Framework, sc.Change, framework, change)
	}
	for _, record := range sc.Records {
		if err := validateRecord(record); err != nil {
			return nil, false, fmt.Errorf("bypass: %s has an invalid record: %w", path, err)
		}
	}
	return &sc, true, nil
}

// ArchiveBypassed reports whether a change's audit history includes an archive
// bypass. Such an archive preserved unfinished work and must not be mistaken
// for the successful completion another workflow may depend on.
func ArchiveBypassed(changeDir, change, framework string) (bool, error) {
	sc, exists, err := Load(Path(changeDir, framework), change, framework)
	if err != nil || !exists {
		return false, err
	}
	for _, record := range sc.Records {
		if record.To == "archive" {
			return true, nil
		}
	}
	return false, nil
}

func validateRecord(record Record) error {
	if strings.TrimSpace(record.At) == "" || strings.TrimSpace(record.Command) == "" || strings.TrimSpace(record.From) == "" || strings.TrimSpace(record.To) == "" || strings.TrimSpace(record.Reason) == "" || len(record.Skipped) == 0 {
		return fmt.Errorf("record must include timestamp, command, source, target, reason, and skipped gates")
	}
	return nil
}

// RequireRealParents verifies root and every component through dir is a real
// directory. It confines a sidecar or archive path to its workspace rather than
// following a planted parent symlink outside it.
func RequireRealParents(root, dir string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := fsutil.RequireRealParents(filepath.VolumeName(root)+string(filepath.Separator), root); err != nil {
		return err
	}
	return fsutil.RequireRealParents(root, dir)
}

func save(path string, sc *Sidecar) error {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bypass: %s is a symlink; refusing", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	return fsutil.WriteControlPlaneWithin(filepath.VolumeName(path)+string(filepath.Separator), path, b, 0o644)
}
