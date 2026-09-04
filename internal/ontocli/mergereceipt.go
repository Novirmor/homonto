package ontocli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/workcli"
)

const mergeReceiptSchemaVersion = 1

type mergeReceipt struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Change        string              `json:"change"`
	Entries       []mergeReceiptEntry `json:"entries"`
}

type mergeReceiptEntry struct {
	Delta        string `json:"delta"`
	DeltaSHA256  string `json:"deltaSha256"`
	Target       string `json:"target"`
	BeforeExists bool   `json:"beforeExists"`
	BeforeSHA256 string `json:"beforeSha256,omitempty"`
	AfterSHA256  string `json:"afterSha256"`
}

type deltaInput struct {
	path, delta, capability, target, targetRel string
	data                                       []byte
	digest                                     string
}

func mergeReceiptPath(changeDir string) string {
	return filepath.Join(changeDir, ".onto", "merge-receipt.json")
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func deltaInputs(root, changeDir string) ([]deltaInput, error) {
	paths, err := deltaSpecPaths(filepath.Join(changeDir, "specs"))
	if err != nil {
		return nil, err
	}
	workflowRoot, err := workcli.WorkflowRoot(root)
	if err != nil {
		return nil, err
	}
	specsDir := filepath.Join(workflowRoot, "specs")
	specsRel, err := filepath.Rel(root, specsDir)
	if err != nil {
		return nil, err
	}
	inputs := make([]deltaInput, 0, len(paths))
	seenTargets := map[string]string{}
	for _, path := range paths {
		base := filepath.Base(path)
		ext := filepath.Ext(base)
		capability := strings.TrimSuffix(base, ext)
		if strings.EqualFold(capability, "README") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		targetRel := filepath.ToSlash(filepath.Join(specsRel, capability+".md"))
		if prior, duplicate := seenTargets[targetRel]; duplicate {
			return nil, fmt.Errorf("delta specs %s and %s map to the same living spec %s", prior, base, targetRel)
		}
		seenTargets[targetRel] = base
		inputs = append(inputs, deltaInput{
			path: path, delta: filepath.ToSlash(filepath.Join("specs", base)), capability: capability,
			target: filepath.Join(root, filepath.FromSlash(targetRel)), targetRel: targetRel,
			data: data, digest: digestBytes(data),
		})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].delta < inputs[j].delta })
	return inputs, nil
}

func loadMergeReceipt(changeDir, change string) (mergeReceipt, bool, error) {
	path := mergeReceiptPath(changeDir)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return mergeReceipt{}, false, nil
	}
	if err != nil {
		return mergeReceipt{}, false, fmt.Errorf("inspecting merge receipt: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return mergeReceipt{}, false, fmt.Errorf("merge receipt must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return mergeReceipt{}, false, fmt.Errorf("opening merge receipt: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var receipt mergeReceipt
	if err := dec.Decode(&receipt); err != nil {
		return mergeReceipt{}, false, fmt.Errorf("decoding merge receipt: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return mergeReceipt{}, false, fmt.Errorf("decoding merge receipt: %w", err)
	}
	if receipt.SchemaVersion != mergeReceiptSchemaVersion {
		return mergeReceipt{}, false, fmt.Errorf("merge receipt schemaVersion %d is unsupported", receipt.SchemaVersion)
	}
	if receipt.Change != change {
		return mergeReceipt{}, false, fmt.Errorf("merge receipt change %q does not match %q", receipt.Change, change)
	}
	for _, entry := range receipt.Entries {
		if entry.Delta == "" || entry.Target == "" || entry.DeltaSHA256 == "" || entry.AfterSHA256 == "" {
			return mergeReceipt{}, false, fmt.Errorf("merge receipt contains an incomplete entry")
		}
		if entry.BeforeExists == (entry.BeforeSHA256 == "") {
			return mergeReceipt{}, false, fmt.Errorf("merge receipt has inconsistent pre-image metadata for %s", entry.Target)
		}
	}
	return receipt, true, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected data after JSON document")
		}
		return err
	}
	return nil
}

func saveMergeReceipt(changeDir string, receipt mergeReceipt) error {
	receipt.SchemaVersion = mergeReceiptSchemaVersion
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding merge receipt: %w", err)
	}
	data = append(data, '\n')
	if err := fsutil.WriteControlPlaneWithin(changeDir, mergeReceiptPath(changeDir), data, 0o644); err != nil {
		return fmt.Errorf("writing merge receipt: %w", err)
	}
	return nil
}

func validateReceiptManifest(receipt mergeReceipt, inputs []deltaInput) error {
	if len(receipt.Entries) != len(inputs) {
		return fmt.Errorf("merge receipt delta set has %d entries; current workspace has %d", len(receipt.Entries), len(inputs))
	}
	for i, input := range inputs {
		entry := receipt.Entries[i]
		if entry.Delta != input.delta || entry.DeltaSHA256 != input.digest || entry.Target != input.targetRel {
			return fmt.Errorf("merge receipt does not match current delta %s", input.delta)
		}
	}
	return nil
}

func fileImage(path string) (exists bool, digest string, data []byte, err error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, "", nil, nil
	}
	if err != nil {
		return false, "", nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, "", nil, fmt.Errorf("%s is a symlink; refusing", path)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return false, "", nil, err
	}
	return true, digestBytes(data), data, nil
}

func validateCompletedMergeReceipt(root, changeDir, change string) error {
	receipt, ok, err := loadMergeReceipt(changeDir, change)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("close.merged is not bound to a merge receipt")
	}
	inputs, err := deltaInputs(root, changeDir)
	if err != nil {
		return err
	}
	if err := validateReceiptManifest(receipt, inputs); err != nil {
		return err
	}
	for i, input := range inputs {
		exists, digest, _, err := fileImage(input.target)
		if err != nil {
			return err
		}
		if !exists || digest != receipt.Entries[i].AfterSHA256 {
			return fmt.Errorf("living spec %s does not match the recorded post-image", input.targetRel)
		}
	}
	return nil
}
