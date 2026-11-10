package contract

// PathDecl is one key a KB declares in its paths.yaml (D263), as sync_pull's
// `path_registry` field carries it. It mirrors kb.PathDecl's JSON tags — the
// wire format — without importing the server's data plane into the client.
// Remote, for a repo key, is already normalized by the server.
type PathDecl struct {
	Description string `json:"description"`
	Default     string `json:"default,omitempty"`
	Remote      string `json:"remote,omitempty"`
	// Bind mirrors kb.PathDecl.Bind: nil or true binds the declared repository
	// to its KB (D1019), false opts it out.
	Bind *bool `json:"bind,omitempty"`
}

// BindsRepo reports whether the declared repository is bound to its KB.
func (d PathDecl) BindsRepo() bool { return d.Bind == nil || *d.Bind }

// PathRegistry is one KB's declared placeholder vocabulary (D263): `paths`
// declares {{path:<key>}} keys, `repos` {{repo:<key>}} keys. Unsigned KB
// content, like a concept body: it can only propose a home-anchored path, only
// as a fallback, and only one that exists — it never executes anything.
type PathRegistry struct {
	Paths map[string]PathDecl `json:"paths,omitempty"`
	Repos map[string]PathDecl `json:"repos,omitempty"`
}

// byID flattens the registry to "kind:key" -> declaration.
func (r PathRegistry) ByID() map[string]PathDecl {
	out := make(map[string]PathDecl, len(r.Paths)+len(r.Repos))
	for k, d := range r.Paths {
		out["path:"+k] = d
	}
	for k, d := range r.Repos {
		out["repo:"+k] = d
	}
	return out
}
