package provisioning

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// codextrust.go (D193) — Codex ignores a project's `.codex/` layer unless the
// project is trusted.
//
// That makes a Codex workspace projection the one case where writing the files
// correctly is not the same as the projection being active. Reporting it as
// installed would be the false-positive class D189 exists to eliminate, so the
// projection reports itself **inactive** and says what would activate it.
//
// The trust record lives in the user's own `~/.codex/config.toml`, under a
// `[projects."<path>"]` table with a `trust_level` key. Cartographer only reads
// it: trusting a project is the user's decision and `codex` has its own prompt
// for it.

// CodexProjectTrusted reports whether codexConfigPath records workspaceDir as a
// trusted project. A missing or unreadable config is "not trusted": absence of
// evidence is never evidence of trust.
//
// The parse is deliberately narrow — it looks for the project's own table
// header and the first trust_level in it — because this is a read of someone
// else's file format and a full TOML parse would fail on syntax Cartographer
// has no business rejecting.
func CodexProjectTrusted(codexConfigPath, workspaceDir string) bool {
	f, err := os.Open(codexConfigPath)
	if err != nil {
		return false
	}
	defer f.Close()

	want := filepath.Clean(workspaceDir)
	inProject := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inProject = isCodexProjectHeader(line, want)
			continue
		}
		if !inProject {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "trust_level" {
			continue
		}
		v := strings.Trim(strings.TrimSpace(value), `"'`)
		return strings.EqualFold(v, "trusted")
	}
	return false
}

// isCodexProjectHeader reports whether a TOML table header names this project.
func isCodexProjectHeader(line, want string) bool {
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	rest, ok := strings.CutPrefix(inner, "projects.")
	if !ok {
		return false
	}
	return filepath.Clean(strings.Trim(strings.TrimSpace(rest), `"'`)) == want
}

// CodexConfigPath is where the trust record is read from, given the client base
// dir. It is the same file the global MCP cell writes into.
func CodexConfigPath(baseDir string) string {
	return filepath.Join(baseDir, ".codex", "config.toml")
}
