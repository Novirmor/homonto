package engine

import (
	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/state"
)

// DescribeAll enumerates the managed resources every adapter currently
// projects — the config repo's adapter plus each repo target — each carrying
// its state partition implicitly in Tool ("opencode" vs "opencode@<repo>").
func (e *Engine) DescribeAll() []adapter.ManagedResource {
	var out []adapter.ManagedResource
	for _, a := range e.Adapters {
		if d, ok := a.(adapter.Describer); ok {
			out = append(out, d.Describe(e.Cfg)...)
		}
	}
	for _, t := range e.RepoTargets {
		if d, ok := t.Adapter.(adapter.Describer); ok {
			out = append(out, d.Describe(e.Cfg)...)
		}
	}
	return out
}

// enrichProvenance records apply history on state after an adapter wrote
// successfully: per-key origin + last event for live keys, tombstones for the
// deletes. The operation ID is allocated lazily — an all-noop apply never
// creates an operation (F3) — and the same ID names every event of one apply.
type enricher struct {
	ops         opid.Supplier
	descriptors map[string]map[string]adapter.ManagedResource // tool -> key -> resource
	op          string
	at          string
}

func newEnricher(e *Engine) *enricher {
	en := &enricher{ops: opid.New(), descriptors: map[string]map[string]adapter.ManagedResource{}}
	for _, r := range e.DescribeAll() {
		if en.descriptors[r.Tool] == nil {
			en.descriptors[r.Tool] = map[string]adapter.ManagedResource{}
		}
		en.descriptors[r.Tool][r.Key] = r
	}
	return en
}

func (en *enricher) allocate() {
	if en.op == "" {
		en.op = en.ops.NewID()
		en.at = en.ops.Now().Format("2006-01-02T15:04:05Z")
	}
}

// captureDeletes snapshots the entries a changeset is about to delete, so the
// tombstone can carry the pre-delete desired value after the adapter's own
// st.Delete removed the entry.
func (en *enricher) captureDeletes(tool string, cs adapter.ChangeSet, st *state.State) []state.Tombstone {
	var out []state.Tombstone
	for _, c := range cs.Changes {
		if c.Action != adapter.ActionDelete {
			continue
		}
		e, _ := st.Get(tool, c.Key)
		out = append(out, state.Tombstone{Tool: tool, Key: c.Key, Desired: e.Desired})
	}
	return out
}

// record applies provenance updates for one adapter's completed changeset.
func (en *enricher) record(cs adapter.ChangeSet, deletes []state.Tombstone, st *state.State) {
	for _, c := range cs.Changes {
		switch c.Action {
		case adapter.ActionNoop:
			continue // a no-op leaves history untouched
		case adapter.ActionDelete:
			continue // handled via tombstones below
		}
		en.allocate()
		var origin *state.Origin
		if r, ok := en.descriptors[cs.Tool][c.Key]; ok && r.Origin != nil {
			o := *r.Origin
			origin = &o
		}
		cause := c.Cause
		st.Enrich(cs.Tool, c.Key, origin, state.LastEvent{Op: en.op, Action: string(c.Action), Cause: string(cause), At: en.at})
	}
	for _, d := range deletes {
		en.allocate()
		d.Op, d.At = en.op, en.at
		d.Cause = "remove"
		st.AppendTombstone(d)
	}
}

// enrichApply wires provenance recording around the per-adapter apply loop:
// the returned pre/post closures bracket one adapter's Apply call.
func (e *Engine) enrichApply() func(cs adapter.ChangeSet, st *state.State) (post func()) {
	en := newEnricher(e)
	return func(cs adapter.ChangeSet, st *state.State) func() {
		deletes := en.captureDeletes(cs.Tool, cs, st)
		return func() { en.record(cs, deletes, st) }
	}
}

// Describe reports one resource's descriptor, if currently declared.
func (e *Engine) Describe(tool, key string) (adapter.ManagedResource, bool) {
	for _, r := range e.DescribeAll() {
		if r.Tool == tool && r.Key == key {
			return r, true
		}
	}
	return adapter.ManagedResource{}, false
}
