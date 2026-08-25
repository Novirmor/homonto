package change

import (
	"context"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/finding"
)

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

// stepCloseADR is a placeholder gate until the ADR workstream fills it in:
// it confirms there is no outstanding ADR work and moves on. The step
// exists now so the transition table and the invalidation graph are whole,
// and so the ADR work has somewhere to land.
func (e *Engine) stepCloseADR(ctx context.Context, st State, step Step) (State, bool, error) {
	return e.advance(ctx, st, step, EventADRsWritten)
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
