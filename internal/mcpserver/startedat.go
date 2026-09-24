package mcpserver

import "time"

// processStartedAt is when this process started, reported as `started_at` in
// /health (D266). It identifies the process, not the server value: a restart
// that has to prove the *new* process answers compares it before and after,
// because version alone cannot tell a replacement from the old process still
// holding the port (a dev build, or a restart to remount KBs, reports the same
// version on both sides). Package-level on purpose: every /health handler in
// this process must report the same instant.
var processStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
