// The change's documents: creation, seeding, path discovery, and digests.
package change

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// createDocuments creates the change's directory and the documents its
// path starts with, seeded with the confirmed request.
func (e *Engine) createDocuments(ctx context.Context, st State, request string) error {
	inputKind, err := st.Path.InputKind()
	if err != nil {
		return err
	}
	inputPath, err := st.DocumentPath(inputKind)
	if err != nil {
		return err
	}
	meta := artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: st.WorkID, Name: st.Name, Kind: inputKind,
	}
	if _, err := e.artifacts.Create(ctx, inputPath, meta); err != nil {
		return err
	}
	if err := e.seedDocument(ctx, artifact.Ref{
		WorkID: st.WorkID, Kind: inputKind, Path: inputPath,
	}, request); err != nil {
		return err
	}
	// Presets author tasks.md in Open alongside their input document;
	// Full's tasks.md is a Design output and is created there.
	if !st.Path.Preset() {
		return nil
	}
	tasksPath, err := st.DocumentPath(artifact.KindTasks)
	if err != nil {
		return err
	}
	_, err = e.artifacts.Create(ctx, tasksPath, artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: st.WorkID, Name: st.Name, Kind: artifact.KindTasks,
	})
	return err
}

// discoverPath reads the confirmed path back off the documents, since each
// path writes a different input document and only one of them exists.
func (e *Engine) discoverPath(ctx context.Context, workID identity.WorkID, name string) (Path, error) {
	for _, candidate := range []Path{PathFull, PathFix, PathTweak} {
		probe := State{WorkID: workID, Name: name, Path: candidate}
		kind, err := candidate.InputKind()
		if err != nil {
			return "", err
		}
		docPath, err := probe.DocumentPath(kind)
		if err != nil {
			return "", err
		}
		if _, err := e.artifacts.Read(ctx, artifact.Ref{
			WorkID: workID, Kind: kind, Path: docPath,
		}); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("change: %s carries no input document, so its path cannot be recovered", name)
}

// seedDocument writes the confirmed request into a freshly created
// document. It goes through a grant the engine issues and immediately
// accepts — the same path a host takes, with no shortcut around the
// ownership table.
func (e *Engine) seedDocument(ctx context.Context, ref artifact.Ref, request string) error {
	if strings.TrimSpace(request) == "" {
		return nil
	}
	grant, err := e.artifacts.GrantEdit(ctx, artifact.GrantRequest{
		Ref: ref, Phase: artifact.PhaseOpen, Regions: []artifact.Region{artifact.RegionWholeDocument},
	})
	if err != nil {
		return err
	}
	doc, err := e.artifacts.Read(ctx, ref)
	if err != nil {
		return err
	}
	body := "## Request\n\n" + strings.TrimSpace(request) + "\n"
	for i := range doc.Regions {
		if doc.Regions[i].Region == artifact.RegionWholeDocument {
			doc.Regions[i].Content = []byte(body)
		}
	}
	if err := artifact.WriteRaw(e.artifacts, ref, doc); err != nil {
		return err
	}
	_, err = e.artifacts.AcceptEdit(ctx, grant)
	return err
}

// hostDocumentKinds are the documents whose content the invalidation graph
// tracks, in canonical order.
var hostDocumentKinds = []artifact.Kind{
	artifact.KindProposal, artifact.KindDesign, artifact.KindTasks,
	artifact.KindPresetTasks, artifact.KindPlan, artifact.KindFix, artifact.KindTweak,
}

// documentDigests digests each of the change's host-authored documents.
// Absent documents get no entry, so one coming into existence moves the
// baseline.
//
// A tasks document's checkbox state is normalized away before digesting:
// a checked box is Homonto recording progress against the plan, not a
// change to it, and digesting it raw would make Homonto's own checkoffs
// invalidate the documents that produced them.
func (e *Engine) documentDigests(ctx context.Context, st State) (map[artifact.Kind]fingerprint.Digest, error) {
	out := map[artifact.Kind]fingerprint.Digest{}
	for _, kind := range hostDocumentKinds {
		path, err := st.DocumentPath(kind)
		if err != nil {
			return nil, err
		}
		doc, err := e.artifacts.Read(ctx, artifact.Ref{WorkID: st.WorkID, Kind: kind, Path: path})
		if errors.Is(err, artifact.ErrArtifactMissing) {
			continue
		}
		if err != nil {
			return nil, err
		}
		content := doc.Region(artifact.RegionWholeDocument)
		if kind == artifact.KindTasks || kind == artifact.KindPresetTasks {
			content = artifact.SemanticChecklist(content)
		}
		out[kind] = fingerprint.Bytes("change-document-"+string(kind), content)
	}
	return out, nil
}
