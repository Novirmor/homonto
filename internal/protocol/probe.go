package protocol

import (
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// ProbeState is what the read-only resume probe found.
type ProbeState string

const (
	// ProbeIdle: the workspace has no active work. The host says nothing
	// and gets on with whatever the user asked for.
	ProbeIdle ProbeState = "idle"
	// ProbeResumable: exactly one active work. The host may offer to
	// resume it.
	ProbeResumable ProbeState = "resumable"
	// ProbeAmbiguous: more than one active work. The host asks the human
	// which — Homonto does not pick, because resuming the wrong work is
	// worse than asking.
	ProbeAmbiguous ProbeState = "ambiguous"
	// ProbeUnavailable: this directory is not an initialized Homonto
	// workspace, or its state could not be read. The probe is read-only
	// and never initializes anything, so it reports and stops.
	ProbeUnavailable ProbeState = "unavailable"
)

// ProbeWork identifies one active work to a host.
type ProbeWork struct {
	ID       identity.WorkID       `json:"id"`
	Name     string                `json:"name"`
	Kind     string                `json:"kind"`
	Step     string                `json:"step"`
	Workflow workspacecfg.Workflow `json:"workflow"`
}

// ProbeResponse is the payload `homonto host probe --json` returns.
//
// The probe performs no write, no migration, and no network access. It
// reads what is there and describes it; a host that runs it on every
// session start must be able to trust that starting a session changes
// nothing.
type ProbeResponse struct {
	ProtocolVersion int        `json:"protocol_version"`
	State           ProbeState `json:"state"`
	// Work is the single resumable work, when there is exactly one.
	Work *ProbeWork `json:"work,omitempty"`
	// Candidates are the competing works, when there is more than one.
	Candidates []ProbeWork `json:"candidates,omitempty"`
	// Message is what the host shows the human. It is written here rather
	// than in a wrapper so the wording is versioned with the binary.
	Message string `json:"message"`
}

// Validate checks the probe payload's envelope and its state/field
// contract.
func (r ProbeResponse) Validate() error {
	if r.ProtocolVersion != CurrentVersion {
		return fmt.Errorf("protocol: protocol_version %d, want exactly %d", r.ProtocolVersion, CurrentVersion)
	}
	switch r.State {
	case ProbeIdle, ProbeResumable, ProbeAmbiguous, ProbeUnavailable:
	default:
		return fmt.Errorf("protocol: probe state %q is not known", r.State)
	}
	if strings.TrimSpace(r.Message) == "" {
		return fmt.Errorf("protocol: a probe response must explain itself")
	}
	switch r.State {
	case ProbeResumable:
		if r.Work == nil {
			return fmt.Errorf("protocol: a resumable probe must name the work")
		}
		if len(r.Candidates) != 0 {
			return fmt.Errorf("protocol: a resumable probe must not also list candidates")
		}
	case ProbeAmbiguous:
		if len(r.Candidates) < 2 {
			return fmt.Errorf("protocol: an ambiguous probe must list at least two candidates")
		}
		if r.Work != nil {
			return fmt.Errorf("protocol: an ambiguous probe must not name one work")
		}
	default:
		if r.Work != nil || len(r.Candidates) != 0 {
			return fmt.Errorf("protocol: a %s probe must name no work", r.State)
		}
	}
	if r.Work != nil {
		if err := r.Work.Validate(); err != nil {
			return fmt.Errorf("protocol: work: %w", err)
		}
	}
	for i := range r.Candidates {
		if err := r.Candidates[i].Validate(); err != nil {
			return fmt.Errorf("protocol: candidates[%d]: %w", i, err)
		}
	}
	return nil
}

// Validate checks one probed work.
func (w ProbeWork) Validate() error {
	if err := identity.ValidateUUID(string(w.ID)); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if strings.TrimSpace(w.Name) == "" {
		return fmt.Errorf("name must not be blank")
	}
	if strings.TrimSpace(w.Kind) == "" {
		return fmt.Errorf("kind must not be blank")
	}
	if strings.TrimSpace(w.Step) == "" {
		return fmt.Errorf("step must not be blank")
	}
	switch w.Workflow {
	case workspacecfg.WorkflowTask, workspacecfg.WorkflowChange:
	default:
		return fmt.Errorf("workflow %q must be %q or %q",
			w.Workflow, workspacecfg.WorkflowTask, workspacecfg.WorkflowChange)
	}
	return nil
}
