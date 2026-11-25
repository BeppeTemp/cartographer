package contract

// Relation is one dependency a KB declares in its settings.yaml `depends_on`,
// as written: the mount name of the KB it needs and that KB's normalized git
// remote. It mirrors kb.Relation's JSON tags without importing the server's
// data plane into the client.
type Relation struct {
	Name   string `json:"name"`
	Remote string `json:"remote"`
}

// RelationUnresolved is a declared relation no mounted KB answers, with the
// stable reason a client prints: not_mounted, remote_mismatch, no_remote, self,
// cycle or too_deep.
type RelationUnresolved struct {
	Name   string `json:"name"`
	Remote string `json:"remote"`
	Reason string `json:"reason"`
}

// Relations is sync_pull's per-KB `relations` field. A server that supports it
// always sends it, with empty arrays (never null) when the KB declares none:
// an absent field can only mean an older server. Resolved and Closure carry the
// current mount name of each target; Declared and Unresolved carry the name as
// written. Like path_registry it is unsigned data outside `revision`.
type Relations struct {
	Declared   []Relation           `json:"declared"`
	Resolved   []string             `json:"resolved"`
	Closure    []string             `json:"closure"`
	Unresolved []RelationUnresolved `json:"unresolved"`
}
