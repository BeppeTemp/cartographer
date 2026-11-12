package client

// IsServerHealth is the one rule for "this /health body is a Cartographer
// server's": status "ok" and a version. Any program can answer a 200 with
// {"status":"ok"}; the version is what a real server adds (a bootstrapping
// server carries both). setup, `service status`, the health probes behind
// `doctor` and the tray all read the verdict from here, so a foreign listener
// is told the same way everywhere (#1998).
func IsServerHealth(status, version string) bool {
	return status == "ok" && version != ""
}

// IsServer reports whether h is the /health answer of a Cartographer server.
func (h *Health) IsServer() bool {
	return h != nil && IsServerHealth(h.Status, h.Version)
}
