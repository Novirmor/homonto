package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Probe answers the read-only resume probe.
//
// It performs no write, no migration, and no network access: a host runs
// it at the start of every session, and starting a session must change
// nothing. It reports one unambiguous active work, the competing ones when
// there are several, or nothing — and it never picks, because resuming the
// wrong work is worse than asking.
func (a *App) Probe(ctx context.Context, host protocol.Host) (protocol.ProbeResponse, error) {
	active, err := a.activeWorks(ctx)
	if err != nil {
		return protocol.ProbeResponse{}, err
	}
	resp := protocol.ProbeResponse{ProtocolVersion: protocol.CurrentVersion}
	switch len(active) {
	case 0:
		resp.State = protocol.ProbeIdle
		resp.Message = fmt.Sprintf("No Homonto work is in progress. Start one with `homonto %s start`.",
			a.cfg.Workspace.Workflow)
	case 1:
		work := a.probeWork(active[0])
		resp.State = protocol.ProbeResumable
		resp.Work = &work
		resp.Message = fmt.Sprintf(
			"Homonto has one active %s: %q, at %s. Run `homonto next --json` to continue it. "+
				"If the user asked for something unrelated, do that instead and leave this alone.",
			work.Kind, work.Name, work.Step)
	default:
		names := make([]string, 0, len(active))
		for _, w := range active {
			probed := a.probeWork(w)
			resp.Candidates = append(resp.Candidates, probed)
			names = append(names, fmt.Sprintf("%s (%s, %s)", probed.Name, probed.Kind, probed.Step))
		}
		resp.State = protocol.ProbeAmbiguous
		resp.Message = fmt.Sprintf(
			"Homonto has %d active works: %s. Ask the human which one to continue; "+
				"do not choose for them.", len(active), strings.Join(names, "; "))
	}
	if err := resp.Validate(); err != nil {
		return protocol.ProbeResponse{}, err
	}
	return resp, nil
}

// probeWork renders one active work for a host.
func (a *App) probeWork(s Status) protocol.ProbeWork {
	return protocol.ProbeWork{
		ID: s.WorkID, Name: s.Name, Kind: string(s.Kind),
		Step: s.Step, Workflow: a.cfg.Workspace.Workflow,
	}
}

// UnavailableProbe is the answer for a directory that is not an
// initialized workspace, or whose state could not be read.
//
// It is a successful, empty answer rather than an error because a host
// runs the probe in every directory a user opens. Failing loudly there
// would turn "this is not a Homonto project" into an error message on
// every unrelated session.
func UnavailableProbe(reason string) protocol.ProbeResponse {
	return protocol.ProbeResponse{
		ProtocolVersion: protocol.CurrentVersion,
		State:           protocol.ProbeUnavailable,
		Message:         "No Homonto workspace here: " + reason,
	}
}

// idleProbe is the answer for an initialized workspace that has never run
// anything, and so has no runtime database yet.
func idleProbe(workflow workspacecfg.Workflow) protocol.ProbeResponse {
	return protocol.ProbeResponse{
		ProtocolVersion: protocol.CurrentVersion,
		State:           protocol.ProbeIdle,
		Message: fmt.Sprintf("No Homonto work is in progress. Start one with `homonto %s start`.",
			workflow),
	}
}

// ProbeAt answers the resume probe for a directory, without changing
// anything in it.
//
// It resolves three cases a host cannot distinguish for itself: a
// directory that is not a workspace, a workspace that has never run
// anything (and so has no runtime database to open READ-ONLY), and a
// workspace with state to report. Only the last one opens anything.
func ProbeAt(ctx context.Context, root string, host protocol.Host) protocol.ProbeResponse {
	resolved, err := resolveRoot(root)
	if err != nil {
		return UnavailableProbe(err.Error())
	}
	cfg, err := workspacecfg.Load(filepath.Join(resolved, ControlDir, ManifestName))
	if err != nil {
		return UnavailableProbe(err.Error())
	}
	if _, err := os.Stat(filepath.Join(resolved, ControlDir, "runtime.db")); errors.Is(err, fs.ErrNotExist) {
		// An initialized workspace that has never run anything has no
		// runtime database. Creating one to answer a probe would be the
		// probe writing, which is the one thing it must not do.
		return idleProbe(cfg.Workspace.Workflow)
	}
	a, err := Open(ctx, Options{Root: resolved, ReadOnly: true})
	if err != nil {
		return UnavailableProbe(err.Error())
	}
	defer a.Close()
	resp, err := a.Probe(ctx, host)
	if err != nil {
		return UnavailableProbe(err.Error())
	}
	return resp
}
