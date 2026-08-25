package change

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/adr"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/finding"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// fingerprintDigest is spelled out so ParseADRCandidates reads without an
// import alias at its call sites.
type fingerprintDigest = fingerprint.Digest

// stepVerificationRecord generates verification.md from the recorded
// evidence. It is a binary-owned write: the verification record is what
// Homonto observed, and a document a host could edit would be a claim
// rather than a record.
func (e *Engine) stepVerificationRecord(ctx context.Context, st State, step Step) (State, bool, error) {
	body, err := e.verificationBody(ctx, st)
	if err != nil {
		return st, false, err
	}
	if err := e.writeGenerated(ctx, st, step, artifact.KindVerification, body); err != nil {
		return st, false, err
	}
	return e.advance(ctx, st, step, EventVerificationRecorded)
}

// verificationBody renders what the spec says verification.md records:
// acceptance-item results, the exact commands, findings, repairs,
// deviations, and residual risks.
func (e *Engine) verificationBody(ctx context.Context, st State) ([]byte, error) {
	var b strings.Builder
	b.WriteString("## Acceptance\n\n")
	items, err := e.checklist(ctx, st)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		b.WriteString("The task list records no acceptance items.\n")
	}
	for _, it := range items {
		state := "not accepted"
		if it.Done {
			state = "accepted"
		}
		fmt.Fprintf(&b, "- %s: %s\n", it.Text, state)
	}

	b.WriteString("\n## Commands\n\n")
	set, err := e.evidence.Latest(ctx, st.WorkID)
	if err != nil {
		b.WriteString("No verification evidence was recorded.\n")
	} else {
		for _, r := range set.Results {
			dir := r.Spec.WorkingDir
			if dir == "" {
				dir = "."
			}
			fmt.Fprintf(&b, "- `%s` in %q: %s (exit %d, %s)\n",
				strings.Join(r.Spec.Command, " "), dir, r.Outcome, r.ExitCode, r.Duration)
		}
	}

	b.WriteString("\n## Findings\n\n")
	all, err := e.findings.All(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		b.WriteString("None.\n")
	}
	for _, f := range all {
		fmt.Fprintf(&b, "- %s %s (%s): %s\n", f.Severity, f.ExternalID, f.State, f.Summary)
	}

	rounds, err := e.findings.RepairRounds(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "\n## Repairs\n\n%d consecutive repair round(s) failed.\n", rounds)

	b.WriteString("\n## Accepted deviations\n\n")
	devs, err := e.findings.Deviations(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		b.WriteString("None.\n")
	}
	for _, d := range devs {
		fmt.Fprintf(&b, "- %s %s: %s — accepted because %s (decision %s)\n",
			d.Severity, d.ExternalID, d.Summary, d.Rationale, d.DecisionID)
	}

	b.WriteString("\n## Residual risks\n\n")
	risks := residualRisks(all)
	if len(risks) == 0 {
		b.WriteString("No unresolved finding remains.\n")
	}
	for _, r := range risks {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	return []byte(b.String()), nil
}

// residualRisks are the findings that were neither fixed nor withdrawn:
// what is still true about the result after everything that was going to
// be done about it has been. An ACCEPTED finding is a residual risk — that
// is what accepting one means — and leaving it out would make the record
// read as if the deviation had gone away.
func residualRisks(all []finding.Finding) []string {
	var out []string
	for _, f := range all {
		if f.State == finding.StateFixed || f.State == finding.StateWithdrawn {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s (%s): %s", f.Severity, f.ExternalID, f.State, f.Summary))
	}
	return out
}

// stepCloseADR decides whether the change owes any decision records,
// issues an implementer assignment per owed ADR, and refuses to close
// until each one is a real document.
//
// An ADR is owed when a durable decision settled a candidate the design
// identified. A durable decision Design never anticipated returns the
// change to Design instead: writing an ADR for a decision nobody designed
// would document an accident.
func (e *Engine) stepCloseADR(ctx context.Context, st State, step Step) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		assessment, err := e.AssessADRs(ctx, st)
		if err != nil {
			return st, false, err
		}
		if assessment.Blocked() {
			// Only a Full change can go back to Design. A preset has no
			// Design to return to — which is why its own decisions
			// synthesize their question instead of blocking — so anything
			// still blocking a preset here is a refusal to close.
			if st.Path == PathFull {
				// A new generation: the design is going to be rewritten,
				// and its draft and approval must be askable again.
				st.Generation++
				return e.advance(ctx, st, step, EventDecisionDiscovered)
			}
			return st, false, fmt.Errorf(
				"change: %s cannot close: %s", st.Name, describeBlockers(assessment))
		}
		if !assessment.Owed() {
			return e.advance(ctx, st, step, EventADRsWritten)
		}
		if err := e.issueADRs(ctx, st, step, assessment); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	// Every ADR assignment is answered; the documents must actually be
	// records, not empty reservations.
	if err := e.verifyADRs(ctx, st, step); err != nil {
		return st, false, err
	}
	return e.advance(ctx, st, step, EventADRsWritten)
}

// describeBlockers explains why an assessment prevents closing.
func describeBlockers(a adr.Assessment) string {
	var parts []string
	for _, d := range a.Undesigned {
		parts = append(parts, fmt.Sprintf(
			"the %s decision %q established something durable that the design never identified",
			d.Kind, d.Choice))
	}
	for _, c := range a.Stale {
		parts = append(parts, fmt.Sprintf(
			"the ADR candidate %q was identified against a design that has since changed", c.ID))
	}
	return strings.Join(parts, "; ")
}

// AssessADRs decides which decision records the change owes.
func (e *Engine) AssessADRs(ctx context.Context, st State) (adr.Assessment, error) {
	design := st.Baseline.Document(artifact.KindDesign)
	if st.Path.Preset() {
		// A preset has no design; its candidates are pinned to the input
		// document that stands in for one.
		kind, err := st.Path.InputKind()
		if err != nil {
			return adr.Assessment{}, err
		}
		design = st.Baseline.Document(kind)
	}
	candidates, err := e.adrCandidates(ctx, st)
	if err != nil {
		return adr.Assessment{}, err
	}
	records, err := e.durableDecisions(ctx, st)
	if err != nil {
		return adr.Assessment{}, err
	}
	declared := make([]string, 0, len(candidates))
	for _, c := range candidates {
		declared = append(declared, c.ID)
	}
	for i := range records {
		// A decision settles the candidates the document identified.
		// Approving a design approves the decisions it names; continuing a
		// preset past its tripwire settles the question its input document
		// named.
		records[i].CandidateIDs = declared
		if len(declared) > 0 {
			continue
		}
		if !st.Path.Preset() {
			// A design that identified nothing means an approval settled
			// nothing, and nothing is owed. That is the common case: most
			// changes make no decision a maintainer would question. Only a
			// decision that carries its OWN question — one a preset made
			// before it was upgraded — survives as undesigned and sends
			// the change back to Design.
			if records[i].Kind == decision.KindApproveDesign {
				records[i].Durable = false
			}
			continue
		}
		// A preset has no design to have identified the question, so the
		// decision supplies its own. Synthesizing here rather than sending
		// the change to Design is deliberate: a preset that a human chose
		// to continue has no Design to go back to, and the question is
		// perfectly well known — it is why the preset was continued.
		synthetic := syntheticCandidate(records[i], st, design)
		if synthetic.ID == "" {
			continue
		}
		candidates = append(candidates, synthetic)
		declaredSynthetic := append([]string(nil), synthetic.ID)
		records[i].CandidateIDs = declaredSynthetic
	}
	return adr.Assess(candidates, records, design)
}

// syntheticCandidate builds the ADR candidate a preset's own decision
// implies. Only the decisions that carry their own question get one; there
// is no general way to invent a question a decision did not ask.
func syntheticCandidate(r adr.Record, st State, design fingerprint.Digest) adr.Candidate {
	switch r.Kind {
	case decision.KindPresetTripwire:
		return adr.Candidate{
			ID:    "preset-continued",
			Title: fmt.Sprintf("Continue %s as a %s change", st.Name, st.Path),
			Question: fmt.Sprintf(
				"why was %q continued as a %s after its scope assessment fired", st.Name, st.Path),
			Design: design,
		}
	case decision.KindReproductionException:
		return adr.Candidate{
			ID:    "reproduction-exception",
			Title: fmt.Sprintf("Fix %s without a reproduction", st.Name),
			Question: fmt.Sprintf(
				"why was %q fixed without a failing test or a reproducible command", st.Name),
			Design: design,
		}
	}
	return adr.Candidate{}
}

// adrCandidates reads the candidates the design (or a preset's input
// document) declared. They are carried as explicit lines rather than
// inferred from prose, because "which decisions does this design make" is
// not a question Homonto can answer by reading English.
func (e *Engine) adrCandidates(ctx context.Context, st State) ([]adr.Candidate, error) {
	kind := artifact.KindDesign
	if st.Path.Preset() {
		var err error
		if kind, err = st.Path.InputKind(); err != nil {
			return nil, err
		}
	}
	path, err := st.DocumentPath(kind)
	if err != nil {
		return nil, err
	}
	doc, err := e.artifacts.Read(ctx, artifact.Ref{WorkID: st.WorkID, Kind: kind, Path: path})
	if errors.Is(err, artifact.ErrArtifactMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	design := st.Baseline.Document(kind)
	return ParseADRCandidates(doc.Region(artifact.RegionWholeDocument), design), nil
}

// adrCandidateMarker is the line prefix a design uses to declare an ADR
// candidate: "adr-candidate: <id> | <title> | <question>".
const adrCandidateMarker = "adr-candidate:"

// ParseADRCandidates extracts the ADR candidates a document declares.
func ParseADRCandidates(body []byte, design fingerprintDigest) []adr.Candidate {
	var out []adr.Candidate
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "-*# "))
		if !strings.HasPrefix(strings.ToLower(trimmed), adrCandidateMarker) {
			continue
		}
		fields := strings.SplitN(trimmed[len(adrCandidateMarker):], "|", 3)
		if len(fields) != 3 {
			continue
		}
		out = append(out, adr.Candidate{
			ID:       strings.TrimSpace(fields[0]),
			Title:    strings.TrimSpace(fields[1]),
			Question: strings.TrimSpace(fields[2]),
			Design:   design,
		})
	}
	return out
}

// durableDecisions returns the change's answered decisions that establish
// something a future maintainer could question.
func (e *Engine) durableDecisions(ctx context.Context, st State) ([]adr.Record, error) {
	actions, err := e.assignments.Actions(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	var out []adr.Record
	for _, act := range actions {
		if act.Kind != protocol.KindDecision || act.State != assignment.StateSubmitted {
			continue
		}
		if act.Spec.Decision == nil {
			continue
		}
		kind := decision.Kind(act.Spec.Decision.Kind)
		if !adr.IsDurableKind(kind) {
			continue
		}
		sub, found, err := e.assignments.Decision(ctx, act.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		// Approving a design is durable because approving it approves
		// the decisions it identifies — and a design that identifies none
		// therefore owes nothing, which is the common case. Continuing a
		// preset past its tripwire and accepting a fix with no
		// reproduction are durable because both went against the easy
		// path, which is exactly what a maintainer asks about later.
		out = append(out, adr.Record{
			ActionID:  act.ID,
			Kind:      kind,
			Choice:    sub.Choice,
			Rationale: sub.Rationale,
			Durable:   durableChoice(kind, sub.Choice),
		})
	}
	return out, nil
}

// durableChoice reports whether a particular answer established something
// durable.
func durableChoice(kind decision.Kind, choice string) bool {
	switch kind {
	case decision.KindPresetTripwire:
		// Continuing a preset past its tripwire decides the architectural
		// question in passing; upgrading defers it to Design.
		return choice == "continue"
	case decision.KindReproductionException:
		return choice == "accept"
	case decision.KindApproveDesign:
		return choice == "approve"
	}
	return false
}

// issueADRs allocates a numbered path per owed ADR and dispatches one
// implementer assignment to write it.
func (e *Engine) issueADRs(ctx context.Context, st State, step Step, a adr.Assessment) error {
	control, err := e.env.Control(ctx)
	if err != nil {
		return err
	}
	root := e.artifacts.Root()
	for _, req := range a.Required {
		rel, err := adr.AllocatePath(root, req.Candidate.Title)
		if err != nil {
			return err
		}
		unit := Unit{
			Label:  "adr-" + req.Candidate.ID,
			Member: control,
			Root:   control.Path,
			Scope:  []string{rel},
			Prompt: "Write the decision record at " + rel + ".\n\n" +
				req.Reason + "\n\nStart from this skeleton and keep it short; the " +
				"Consequences section must include what the decision costs:\n\n" +
				adr.Template(req.Candidate, req.Decision, e.now().UTC().Format("2006-01-02")),
		}
		spec, err := e.implementer(st, step, unit, "write the decision record for "+req.Candidate.ID)
		if err != nil {
			return err
		}
		actionID, err := identity.NewActionID()
		if err != nil {
			return err
		}
		spec.ActionID = actionID
		act, err := e.assignments.Create(ctx, spec)
		if err != nil {
			return err
		}
		if err := e.saveUnit(ctx, st.WorkID, step, act.ID, unit); err != nil {
			return err
		}
	}
	return nil
}

// verifyADRs confirms every allocated record is a real document. An empty
// reservation is not an ADR, and closing over one would leave a numbered
// blank in the directory forever.
func (e *Engine) verifyADRs(ctx context.Context, st State, step Step) error {
	assessment, err := e.AssessADRs(ctx, st)
	if err != nil {
		return err
	}
	byCandidate := map[string]adr.Candidate{}
	for _, req := range assessment.Required {
		byCandidate[req.Candidate.ID] = req.Candidate
	}
	actions, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return err
	}
	for _, act := range actions {
		unit, found, err := e.unitOf(ctx, act.ID)
		if err != nil {
			return err
		}
		if !found || len(unit.Scope) != 1 {
			continue
		}
		id := strings.TrimPrefix(unit.Label, "adr-")
		candidate, known := byCandidate[id]
		if !known {
			continue
		}
		if err := adr.ValidateDocument(e.artifacts.Root(), unit.Scope[0], candidate); err != nil {
			return err
		}
	}
	return nil
}

// stepFinalize writes record.md, archives the change directory, and
// finishes. Only Homonto writes here: the record is what Homonto observed.
func (e *Engine) stepFinalize(ctx context.Context, st State, step Step) (State, bool, error) {
	body, err := e.recordBody(ctx, st)
	if err != nil {
		return st, false, err
	}
	if err := e.writeGenerated(ctx, st, step, artifact.KindRecord, body); err != nil {
		return st, false, err
	}
	if _, err := e.archive.ArchiveWork(ctx, st.WorkID, e.now()); err != nil {
		return st, false, err
	}
	if err := e.findings.ResetRepairs(ctx, st.WorkID); err != nil {
		return st, false, err
	}
	return e.advance(ctx, st, step, EventFinalized)
}

// recordBody renders the change's final record.
func (e *Engine) recordBody(ctx context.Context, st State) ([]byte, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "## Outcome\n\nThe %s change %q completed.\n", st.Path, st.Name)
	if st.UpgradedFrom != "" {
		fmt.Fprintf(&b, "\nIt began as a %s preset and was upgraded to full.\n", st.UpgradedFrom)
	}

	b.WriteString("\n## Verification\n\n")
	set, err := e.evidence.Latest(ctx, st.WorkID)
	if err != nil {
		b.WriteString("No verification evidence was recorded.\n")
	} else {
		for _, r := range set.Results {
			fmt.Fprintf(&b, "- `%s`: %s (exit %d)\n",
				strings.Join(r.Spec.Command, " "), r.Outcome, r.ExitCode)
		}
	}

	b.WriteString("\n## Integration\n\n")
	if len(st.Baseline.Sources) == 0 {
		b.WriteString("No integrated source fingerprints were recorded.\n")
	}
	for _, d := range st.Baseline.Sources {
		fmt.Fprintf(&b, "- %s\n", d)
	}
	b.WriteString("\nThe integration branches and staged directories are left ready " +
		"for external handling; nothing was merged into any member's own branch.\n")

	b.WriteString("\n## Accepted deviations\n\n")
	devs, err := e.findings.Deviations(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		b.WriteString("None.\n")
	}
	for _, d := range devs {
		fmt.Fprintf(&b, "- %s %s: %s — accepted because %s (decision %s)\n",
			d.Severity, d.ExternalID, d.Summary, d.Rationale, d.DecisionID)
	}
	return []byte(b.String()), nil
}

// writeGenerated creates a binary-owned document if needed and writes it.
func (e *Engine) writeGenerated(ctx context.Context, st State, step Step, kind artifact.Kind, body []byte) error {
	path, err := st.DocumentPath(kind)
	if err != nil {
		return err
	}
	ref := artifact.Ref{WorkID: st.WorkID, Kind: kind, Path: path}
	if _, err := e.artifacts.Read(ctx, ref); err != nil {
		if _, cerr := e.artifacts.Create(ctx, path, artifact.Metadata{
			Schema: artifact.MetadataSchema, WorkID: st.WorkID, Name: st.Name, Kind: kind,
		}); cerr != nil {
			return cerr
		}
	}
	phase, err := Phase(st.Path, step)
	if err != nil {
		return err
	}
	_, err = e.artifacts.WriteGenerated(ctx, ref, phase, []artifact.RegionContent{
		{Region: artifact.RegionWholeDocument, Content: body},
	})
	return err
}
