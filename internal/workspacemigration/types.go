// Package workspacemigration provides the deliberately read-only inventory for
// the one supported legacy-records to schema-2 ownership transition.
package workspacemigration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	// ManifestVersion is the only input manifest format accepted by M1.
	ManifestVersion = 1
	// ControlOnlyAttestation is the explicit operator statement required before
	// a future migration may remove the legacy implicit configuration source.
	ControlOnlyAttestation = "legacy-implicit-config-source-contained-control-work-only"
)

// Manifest is an operator-supplied, external input. Record paths and IDs are
// both mandatory so a reused state name cannot select the wrong generation.
type Manifest struct {
	Version                int              `json:"version"`
	ControlOnlyAttestation string           `json:"control_only_attestation"`
	Records                []ManifestRecord `json:"records"`
}

type ManifestRecord struct {
	Path    string           `json:"path"`
	ID      string           `json:"id"`
	Sources []ManifestSource `json:"sources"`
}

type ManifestSource struct {
	Alias         string `json:"alias"`
	BaseRef       string `json:"base_ref"`
	BaseBranch    string `json:"base_branch"`
	GitCommonDir  string `json:"git_common_dir"`
	ExecutionPath string `json:"execution_path,omitempty"`
}

// ManifestRequirements documents the exact JSON input shape in every plan
// response without printing the manifest's raw bytes.
type ManifestRequirements struct {
	Version                int      `json:"version"`
	ControlOnlyAttestation string   `json:"control_only_attestation"`
	RecordFields           []string `json:"record_fields"`
	SourceFields           []string `json:"source_fields"`
}

type FileFingerprint struct {
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	Classification string `json:"classification"`
}

type Layout struct {
	SchemaVersion int          `json:"schema_version"`
	ConfigPath    string       `json:"config_path"`
	ConfigRoot    string       `json:"config_root"`
	WorkflowRoot  string       `json:"workflow_root"`
	GitMode       string       `json:"git_mode"`
	WorktreesDir  string       `json:"worktrees_dir,omitempty"`
	Repos         []Repository `json:"repos"`
}

type Repository struct {
	Alias        string `json:"alias"`
	Path         string `json:"path"`
	GitCommonDir string `json:"git_common_dir"`
}

type Dirt struct {
	Tracked   int `json:"tracked"`
	Untracked int `json:"untracked"`
	Ignored   int `json:"ignored"`
}

type RecordsGit struct {
	Path         string   `json:"path"`
	GitCommonDir string   `json:"git_common_dir"`
	Head         string   `json:"head"`
	IndexSHA256  string   `json:"index_sha256"`
	Dirt         Dirt     `json:"dirt"`
	RemoteNames  []string `json:"remote_names"`
}

type Execution struct {
	Path         string `json:"path"`
	GitCommonDir string `json:"git_common_dir"`
	Head         string `json:"head"`
	Branch       string `json:"branch"`
	Dirt         Dirt   `json:"dirt"`
}

type Source struct {
	Alias          string     `json:"alias"`
	Path           string     `json:"path"`
	BaseRef        string     `json:"base_ref"`
	BaseBranch     string     `json:"base_branch"`
	BaseBranchHead string     `json:"base_branch_head"`
	GitCommonDir   string     `json:"git_common_dir"`
	Head           string     `json:"head"`
	Execution      *Execution `json:"execution,omitempty"`
}

type Record struct {
	Path               string   `json:"path"`
	RelativePath       string   `json:"relative_path"`
	Framework          string   `json:"framework"`
	Workflow           string   `json:"workflow,omitempty"`
	Lifecycle          string   `json:"lifecycle"`
	StateFiles         []string `json:"state_files"`
	ID                 string   `json:"id,omitempty"`
	Name               string   `json:"name,omitempty"`
	Phase              string   `json:"phase,omitempty"`
	SchemaVersion      int      `json:"schema_version,omitempty"`
	RepoMode           string   `json:"repo_mode,omitempty"`
	ScalarBaseRef      string   `json:"scalar_base_ref,omitempty"`
	ScalarBaseBranch   string   `json:"scalar_base_branch,omitempty"`
	SourceAliases      []string `json:"source_aliases"`
	Sources            []Source `json:"sources"`
	LegacyConfigSource bool     `json:"legacy_config_source"`
	UnknownFields      []string `json:"unknown_fields"`
}

type Blocker struct {
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
}

// Plan is an inventory only. M1 has no transform, write set, journal, apply,
// verification, or recovery behavior.
type Plan struct {
	Version  int    `json:"version"`
	ReadOnly bool   `json:"read_only"`
	Status   string `json:"status"`
	// PlanHash binds the complete observed inventory, including blockers. A
	// future apply command must additionally require Status == "ready".
	PlanHash             string               `json:"plan_hash"`
	ManifestRequirements ManifestRequirements `json:"manifest_requirements"`
	Config               FileFingerprint      `json:"config"`
	Manifest             FileFingerprint      `json:"manifest"`
	Layout               Layout               `json:"layout"`
	RecordsGit           RecordsGit           `json:"records_git"`
	ControlFiles         []FileFingerprint    `json:"control_files"`
	Files                []FileFingerprint    `json:"files"`
	Records              []Record             `json:"records"`
	Blockers             []Blocker            `json:"blockers"`
}

// ErrBlocked is returned after a complete enough read-only inventory is
// encoded with blockers. It deliberately carries no raw config or record text.
var ErrBlocked = fmt.Errorf("workspace migration plan blocked")

func newPlan() Plan {
	return Plan{
		Version:  1,
		ReadOnly: true,
		Status:   "blocked",
		ManifestRequirements: ManifestRequirements{
			Version:                ManifestVersion,
			ControlOnlyAttestation: ControlOnlyAttestation,
			RecordFields:           []string{"path", "id", "sources"},
			SourceFields:           []string{"alias", "base_ref", "base_branch", "git_common_dir", "execution_path (optional)"},
		},
		ControlFiles: []FileFingerprint{},
		Files:        []FileFingerprint{},
		Records:      []Record{},
		Blockers:     []Blocker{},
	}
}

func (p *Plan) block(code, path, detail string) {
	p.Blockers = append(p.Blockers, Blocker{Code: code, Path: path, Detail: detail})
}

func (p *Plan) finish() (Plan, error) {
	sort.Slice(p.ControlFiles, func(i, j int) bool { return p.ControlFiles[i].Path < p.ControlFiles[j].Path })
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	sort.Slice(p.Records, func(i, j int) bool { return p.Records[i].Path < p.Records[j].Path })
	for i := range p.Records {
		sort.Strings(p.Records[i].StateFiles)
		sort.Strings(p.Records[i].SourceAliases)
		sort.Strings(p.Records[i].UnknownFields)
		sort.Slice(p.Records[i].Sources, func(a, b int) bool {
			return p.Records[i].Sources[a].Alias < p.Records[i].Sources[b].Alias
		})
	}
	sort.Slice(p.Blockers, func(i, j int) bool {
		a, b := p.Blockers[i], p.Blockers[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Detail < b.Detail
	})
	if len(p.Blockers) != 0 {
		p.Status = "blocked"
		p.PlanHash = planHash(*p)
		return *p, ErrBlocked
	}
	p.Status = "ready"
	p.PlanHash = planHash(*p)
	return *p, nil
}

func decodeManifest(data []byte) (Manifest, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Manifest{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var manifest Manifest
	if err := dec.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("trailing JSON value")
		}
		return Manifest{}, err
	}
	if manifest.Version != ManifestVersion {
		return Manifest{}, fmt.Errorf("unsupported manifest version %d", manifest.Version)
	}
	if manifest.ControlOnlyAttestation != ControlOnlyAttestation {
		return Manifest{}, fmt.Errorf("control_only_attestation must equal %q", ControlOnlyAttestation)
	}
	if manifest.Records == nil {
		return Manifest{}, fmt.Errorf("records is required")
	}
	return manifest, nil
}

// rejectDuplicateJSONKeys protects the typed decoder from silently accepting a
// last-wins manifest object. It validates only JSON structure, never values.
func rejectDuplicateJSONKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := inspectJSONValue(dec, ""); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func inspectJSONValue(dec *json.Decoder, path string) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("invalid JSON object key at %s", jsonPath(path))
			}
			field := joinJSONPath(path, key)
			if seen[key] {
				return fmt.Errorf("duplicate manifest field %q", field)
			}
			seen[key] = true
			if err := inspectJSONValue(dec, field); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := inspectJSONValue(dec, path+"[]"); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	default:
		return fmt.Errorf("invalid JSON delimiter at %s", jsonPath(path))
	}
}

func joinJSONPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func jsonPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "document root"
	}
	return path
}
