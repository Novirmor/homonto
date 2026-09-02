package state

// Origin records why a managed key exists: who declared it and where it
// projects. One entry may carry several origins when equivalent declarations
// deduplicate (a framework and a direct table naming the same resource); the
// first is primary. Nil means unknown (a legacy entry predating schema 3) —
// never guessed.
type Origin struct {
	Kind      string `json:"kind"`                // "direct" | "framework"
	Framework string `json:"framework,omitempty"` // config key of the declaring framework
	Provider  string `json:"provider,omitempty"`  // catalog framework providing the content
	Source    string `json:"source,omitempty"`    // builtin:<n> | local:<n> | remote:<url>
	Scope     string `json:"scope,omitempty"`     // user | project
	Repo      string `json:"repo,omitempty"`      // declared [repos] name, when repo-tagged
}

// LastEvent is the most recent apply operation that touched a resource: the
// operation ID, the action, the typed cause, and the UTC time. At is empty
// only for entries no operation ever touched (adopted by a schema-2 binary).
type LastEvent struct {
	Op     string `json:"op"`
	Action string `json:"action"`
	Cause  string `json:"cause,omitempty"`
	At     string `json:"at"`
}

// Tombstone is a removed resource's record: what it was, which operation
// removed it, when, and why. Bounded to the latest TombstoneLimit in
// operation order — a ring, not a log.
type Tombstone struct {
	Tool    string `json:"tool"`
	Key     string `json:"key"`
	Desired string `json:"desired,omitempty"`
	Op      string `json:"op"`
	At      string `json:"at"`
	Cause   string `json:"cause,omitempty"`
}

// TombstoneLimit bounds the removal ring. 100 covers a large deprecation
// sweep while keeping state.json small; older tombstones are dropped in
// operation order.
const TombstoneLimit = 100

// AppendTombstone adds a removal record to the ring (bounded, in operation
// order). Engine-level recording uses this after an adapter's Delete dropped
// the entry.
func (s *State) AppendTombstone(t Tombstone) {
	if t.Op == "" {
		return // a tombstone without an operation is a guess
	}
	s.Tombstones = append(s.Tombstones, t)
	if over := len(s.Tombstones) - TombstoneLimit; over > 0 {
		s.Tombstones = s.Tombstones[over:]
	}
}
