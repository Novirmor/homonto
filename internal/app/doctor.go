package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/noviopenworks/homonto/internal/host"
	"github.com/noviopenworks/homonto/internal/update"
	"github.com/noviopenworks/homonto/internal/verify"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// Severity grades a diagnostic.
type Severity string

const (
	// SeverityError: something is broken and must be fixed.
	SeverityError Severity = "error"
	// SeverityWarning: something is off and will bite later.
	SeverityWarning Severity = "warning"
	// SeverityInfo: worth knowing, nothing to do.
	SeverityInfo Severity = "info"
)

// Diagnostic is one thing doctor found.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Summary  string   `json:"summary"`
	// Remedy says what to do about it. A diagnostic a user cannot act on
	// is a complaint.
	Remedy string `json:"remedy,omitempty"`
}

// Report is what doctor concluded.
type Report struct {
	Root        string       `json:"root"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Healthy reports whether nothing is broken.
func (r Report) Healthy() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			return false
		}
	}
	return true
}

// add appends a diagnostic.
func (r *Report) add(severity Severity, summary, remedy string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Severity: severity, Summary: summary, Remedy: remedy,
	})
}

// Doctor checks a workspace and reports what it finds.
//
// It reports rather than repairs. Everything here is something a human
// might have done deliberately — edited a wrapper, removed a member,
// interrupted an update — and a tool that silently undid those would be
// arguing rather than diagnosing.
func (a *App) Doctor(ctx context.Context) (Report, error) {
	report := Report{Root: a.root}

	// Membership: the manifest against what is actually on disk.
	scanner := workspace.Scanner{}
	discovered, err := scanner.Scan(ctx, a.root, workspace.ScanOptions{})
	if err != nil {
		report.add(SeverityWarning, fmt.Sprintf("the workspace could not be scanned: %v", err),
			"check that the root is readable")
	} else {
		for _, finding := range workspace.Reconcile(a.root, a.cfg, discovered) {
			report.add(SeverityWarning, fmt.Sprintf("%s: %s", finding.Kind, finding.Path),
				"run `homonto init` again with the members you want, or edit the manifest")
		}
	}

	// Host integrations: installed, drifted, or missing.
	observations, err := a.ObserveHosts(ctx, "")
	if err != nil {
		report.add(SeverityWarning, fmt.Sprintf("the host integrations could not be read: %v", err), "")
	}
	for _, obs := range observations {
		for _, path := range obs.Modified {
			report.add(SeverityWarning,
				fmt.Sprintf("%s was edited by hand", path),
				"`homonto host install --adopt` replaces it, or leave it and Homonto will not touch it")
		}
		for _, path := range obs.Missing {
			report.add(SeverityWarning, fmt.Sprintf("%s is missing", path),
				"run `homonto host install`")
		}
		for _, path := range obs.Foreign {
			report.add(SeverityWarning,
				fmt.Sprintf("%s was written by something other than Homonto", path),
				"`homonto host install --adopt` replaces it")
		}
	}

	// Work: what is active, and what is blocked on a human.
	active, err := a.activeWorks(ctx)
	if err != nil {
		return Report{}, err
	}
	for _, w := range active {
		report.add(SeverityInfo, fmt.Sprintf("%s %q is at %s", w.Kind, w.Name, w.Step), "")
	}
	if len(active) > 1 {
		report.add(SeverityWarning,
			fmt.Sprintf("%d works are active, so unqualified commands are ambiguous", len(active)),
			"name one, or finish or abandon the others")
	}

	// Evidence: recorded verification that no longer describes the world.
	for _, w := range active {
		if w.Kind != WorkTask {
			continue
		}
		if _, err := a.evidence.Latest(ctx, w.WorkID); errors.Is(err, verify.ErrNoEvidence) {
			continue
		} else if err != nil {
			report.add(SeverityWarning,
				fmt.Sprintf("the verification evidence for %q could not be read: %v", w.Name, err), "")
		}
	}

	// An interrupted self-update blocks everything.
	if pending, err := update.Pending(a.root); err != nil {
		report.add(SeverityError, fmt.Sprintf("the update journal is unreadable: %v", err),
			"restore .homonto/update/ from a backup, or remove the journal to accept the "+
				"installation as it stands")
	} else if pending {
		report.add(SeverityError, "an interrupted self-update is waiting to be recovered",
			"run any homonto command; recovery happens at startup")
	}

	// The trust store, so a user knows whether update is even available.
	if store := trustSummary(); store != "" {
		report.add(SeverityInfo, store, "")
	}
	return report, nil
}

// trustSummary describes what this build will accept a release from.
func trustSummary() string {
	meta := update.LocalMetadata()
	if len(meta.TrustRoots) == 0 {
		return "this build carries no signing root, so `homonto update` is unavailable"
	}
	return fmt.Sprintf("this build trusts %d signing root(s)", len(meta.TrustRoots))
}

// HostSummary describes the installed integrations for status output.
type HostSummary struct {
	Tool      host.Tool `json:"tool"`
	Installed int       `json:"installed"`
	Modified  int       `json:"modified"`
	Missing   int       `json:"missing"`
}

// Hosts summarizes the installed host integrations.
func (a *App) Hosts(ctx context.Context) ([]HostSummary, error) {
	observations, err := a.ObserveHosts(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]HostSummary, 0, len(observations))
	for _, obs := range observations {
		out = append(out, HostSummary{
			Tool: obs.Target.Tool, Installed: len(obs.Installed),
			Modified: len(obs.Modified), Missing: len(obs.Missing),
		})
	}
	return out, nil
}
