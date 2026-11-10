package contract

// AgentHashPath is the path a managed agent's single file is hashed under,
// whatever the client's file is called: Apply, verify and `doctor
// --repair-hashes` must agree, or a repaired agent reads as modified.
func AgentHashPath(name string) string { return name + ".md" }
