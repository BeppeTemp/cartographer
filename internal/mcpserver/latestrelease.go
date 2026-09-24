package mcpserver

// The server's knowledge of a newer release (D254). serve owns the check
// (internal/updatecheck, one goroutine, failing silent); this package only
// reports what it is told, through a source read at request time so a
// check finishing after the KBs were mounted is visible at once.
//
// The server reports versions only, never an upgrade command: how it was
// deployed is not knowable from inside.

// SetLatestVersionSource installs the function /health and kb_status ask
// for the newer release; it returns "" when none is known. Nil (the default)
// means never known, and both stay byte-identical to a server without the
// check.
func (s *Server) SetLatestVersionSource(fn func() string) { s.latestVersion = fn }

func (s *Server) knownLatestVersion() string {
	if s == nil || s.latestVersion == nil {
		return ""
	}
	return s.latestVersion()
}

// SetLatestVersionSource installs the source for the multi-KB /health. The
// per-KB servers' kb_status is wired by the caller's setupFn, like every
// other per-server setting.
func (m *MultiKBServer) SetLatestVersionSource(fn func() string) { m.latestVersion = fn }
