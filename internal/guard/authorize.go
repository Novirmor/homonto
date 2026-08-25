package guard

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// Assignments is the slice of the assignment store the guard needs: which
// action a host claims, whether it is still awaiting a result, and whether
// the presented token was issued for it.
type Assignments interface {
	Action(ctx context.Context, id identity.ActionID) (assignment.Action, error)
	Token(id identity.ActionID) identity.Token
}

// Grants is the slice of the artifact grant ledger the guard needs: which
// grant a host claims, which document and phase it opened, and whether it
// is still open.
type Grants interface {
	Lookup(ctx context.Context, id identity.ActionID) (artifact.GrantRecord, bool, error)
}

// Guard answers guard requests and validates assignment results.
type Guard struct {
	assignments Assignments
	grants      Grants
}

// New binds a guard to the assignment store and the grant ledger. Both are
// required: without either the guard could not tell a legitimate write
// from an unauthorized one, and a guard that cannot tell must refuse
// everything, which is not a guard.
func New(assignments Assignments, grants Grants) (*Guard, error) {
	if assignments == nil {
		return nil, fmt.Errorf("guard: assignment store must not be nil")
	}
	if grants == nil {
		return nil, fmt.Errorf("guard: grant ledger must not be nil")
	}
	return &Guard{assignments: assignments, grants: grants}, nil
}

// Authorize decides one presented write. It never returns an error for a
// refusal — a refusal IS the answer, and it carries its reason — and it
// returns an error only when the guard could not reach a verdict at all,
// which callers must treat as a refusal.
func (g *Guard) Authorize(ctx context.Context, req Request) (protocol.GuardDecision, error) {
	if err := req.Validate(); err != nil {
		return refuse(CodeMalformed, "%v", err), nil
	}
	// A read with no write intent needs no assignment: the guard exists to
	// bound writes, and refusing reads would only teach hosts to stop
	// asking.
	if len(req.Wire.WritePaths) == 0 {
		return allow("the operation writes nothing"), nil
	}
	if req.GrantID != "" {
		return g.authorizeGrant(ctx, req)
	}
	if req.ActionID == "" {
		return refuse(CodeNoPermission,
			"session %s presented neither an assignment nor an edit grant; nothing gives it permission to write",
			req.Wire.SessionID), nil
	}

	act, err := g.assignments.Action(ctx, req.ActionID)
	if errors.Is(err, assignment.ErrUnknownAction) {
		return refuse(CodeUnknownAssignment, "assignment %s does not exist", req.ActionID), nil
	}
	if err != nil {
		return protocol.GuardDecision{}, err
	}
	if act.State != assignment.StateIssued {
		return refuse(CodeStaleAssignment,
			"assignment %s is %s, not awaiting a result", act.ID, act.State), nil
	}
	if !tokenMatches(g.assignments.Token(act.ID), req.Token) {
		return refuse(CodeStaleToken, "the token presented for assignment %s was not issued for it", act.ID), nil
	}
	if act.Kind != protocol.KindAssignment {
		return refuse(CodeReadOnlyRole, "action %s is a %s; decisions write nothing", act.ID, act.Kind), nil
	}
	if act.Spec.WriteScope.ReadOnly {
		return refuse(CodeReadOnlyRole,
			"the %s assignment %s is read-only; it observes and reports, it does not write",
			act.Role, act.ID), nil
	}

	for _, p := range req.Wire.WritePaths {
		if decision, ok := g.checkPath(act, req.Wire.WorkingDirectory, p); !ok {
			return decision, nil
		}
	}
	return allow(fmt.Sprintf("within the scope issued to %s assignment %s", act.Role, act.ID)), nil
}

// authorizeGrant decides a write presented under an artifact edit grant.
// A grant opens exactly one document, for exactly one phase, once: the
// write must be that document and nothing else, and an already-accepted
// grant opens nothing.
func (g *Guard) authorizeGrant(ctx context.Context, req Request) (protocol.GuardDecision, error) {
	rec, found, err := g.grants.Lookup(ctx, req.GrantID)
	if err != nil {
		return protocol.GuardDecision{}, err
	}
	if !found {
		return refuse(CodeUnknownGrant, "edit grant %s does not exist", req.GrantID), nil
	}
	if rec.Consumed {
		return refuse(CodeStaleGrant, "edit grant %s was already accepted; grants are single-use", req.GrantID), nil
	}
	if !artifact.GrantTokenMatches(req.GrantToken, rec.TokenHash) {
		return refuse(CodeStaleToken, "the token presented for edit grant %s was not issued for it", req.GrantID), nil
	}
	granted, ok := normalize(rec.Ref.Path)
	if !ok {
		return refuse(CodeMalformed, "edit grant %s names an unusable document path %q", req.GrantID, rec.Ref.Path), nil
	}
	for _, p := range req.Wire.WritePaths {
		rel, ok := resolve(req.Wire.WorkingDirectory, p)
		if !ok {
			return refuse(CodeMalformed, "write path %q is not a clean relative path inside the workspace", p), nil
		}
		if rel != granted {
			return refuse(CodeWrongDocument,
				"edit grant %s opens %q, not %q", req.GrantID, granted, rel), nil
		}
	}
	return allow(fmt.Sprintf("edit grant %s opens %q to the %s in phase %s",
		req.GrantID, granted, rec.Owner, rec.Phase)), nil
}

// checkPath decides one intended write path. The path is interpreted
// relative to the host's working directory, resolved against the
// assignment's isolation root, and then checked against the isolation
// area, the declared scope, the protected state files, and the artifact
// ownership table, in that order — from the widest boundary inward, so a
// refusal always names the outermost rule that was broken.
func (g *Guard) checkPath(act assignment.Action, workingDir, p string) (protocol.GuardDecision, bool) {
	rel, ok := resolve(workingDir, p)
	if !ok {
		return refuse(CodeMalformed, "write path %q is not a clean relative path inside the workspace", p), false
	}
	root, ok := normalize(act.Spec.WorkingDirectory)
	if !ok {
		return refuse(CodeMalformed,
			"assignment %s declares an unusable isolation root %q", act.ID, act.Spec.WorkingDirectory), false
	}
	if !within(root, rel) {
		return refuse(CodeOutsideIsolation,
			"%q is outside the isolation area %q issued to assignment %s", rel, root, act.ID), false
	}
	inner := trimRoot(root, rel)
	if isProtected(inner) {
		return refuse(CodeProtectedPath,
			"%q is binary-owned control state; it is written by Homonto, never by an assignment", inner), false
	}
	if decision, ok := checkArtifact(act, inner); !ok {
		return decision, false
	}
	if !inScope(act.Spec.WriteScope.Paths, inner) {
		return refuse(CodeOutsideScope,
			"%q is outside the scope issued to assignment %s (%s)",
			inner, act.ID, strings.Join(act.Spec.WriteScope.Paths, ", ")), false
	}
	return protocol.GuardDecision{}, true
}

// checkArtifact refuses a workflow document written outside the phase that
// owns it, or in a region the binary owns. A path that is not a workflow
// document is not this rule's business.
func checkArtifact(act assignment.Action, rel string) (protocol.GuardDecision, bool) {
	kind, ok := documentKind(rel)
	if !ok {
		return protocol.GuardDecision{}, true
	}
	phase := artifact.Phase(act.Spec.Phase)
	owner, _, editable := artifact.Ownership(kind, phase)
	if !editable {
		return refuse(CodeWrongPhase,
			"%q is a %s document, which nobody writes in phase %s", rel, kind, phase), false
	}
	if owner == artifact.OwnerBinary {
		return refuse(CodeBinaryOwned,
			"%q is a %s document, which Homonto itself writes in phase %s", rel, kind, phase), false
	}
	if owner != artifact.OwnerImplementer {
		// Host-owned documents are edited through an edit grant, never by
		// an assignment writing files directly. Only the implementer-owned
		// kinds are an assignment's to write, and even those go through a
		// grant of their own.
		return refuse(CodeWrongPhase,
			"%q is %s-owned in phase %s; it is edited through an edit grant, not by assignment %s",
			rel, owner, phase, act.ID), false
	}
	return protocol.GuardDecision{}, true
}

// documentKind maps a control-root-relative path to the workflow document
// kind it is, when it is one. Only paths under Homonto's own document tree
// count: a file called proposal.md in the source tree is source, and
// refusing it would make the guard an obstacle rather than a boundary.
func documentKind(rel string) (artifact.Kind, bool) {
	if !within(artifact.DocsDir, rel) {
		return "", false
	}
	if within(artifact.TasksDir, rel) && strings.HasSuffix(rel, ".md") {
		return artifact.KindTaskDocument, true
	}
	if !within(artifact.ChangesDir, rel) {
		return "", false
	}
	base := path.Base(rel)
	for kind, name := range artifactFileNames() {
		if name == base {
			return kind, true
		}
	}
	return "", false
}

// artifactFileNames is the kind-to-file-name map, built from the artifact
// package so the two can never drift.
func artifactFileNames() map[artifact.Kind]string {
	kinds := []artifact.Kind{
		artifact.KindProposal, artifact.KindDesign, artifact.KindTasks,
		artifact.KindPresetTasks, artifact.KindPlan, artifact.KindFix,
		artifact.KindTweak, artifact.KindVerification, artifact.KindRecord,
	}
	out := make(map[artifact.Kind]string, len(kinds))
	for _, k := range kinds {
		if name, err := artifact.FileName(k); err == nil {
			out[k] = name
		}
	}
	return out
}

// resolve interprets a host-presented path relative to its working
// directory and returns the workspace-relative form.
func resolve(workingDir, p string) (string, bool) {
	dir, ok := normalize(workingDir)
	if !ok {
		return "", false
	}
	rel, ok := normalize(p)
	if !ok {
		return "", false
	}
	if dir == "." {
		return rel, true
	}
	return path.Join(dir, rel), true
}

// trimRoot returns rel expressed relative to the isolation root.
func trimRoot(root, rel string) string {
	if root == "." || root == "" {
		return rel
	}
	if rel == root {
		return "."
	}
	return strings.TrimPrefix(rel, root+"/")
}

// inScope reports whether an isolation-relative path is inside any
// declared scope entry.
func inScope(scope []string, rel string) bool {
	for _, s := range scope {
		clean, ok := normalize(s)
		if !ok {
			continue
		}
		if within(clean, rel) {
			return true
		}
	}
	return false
}
