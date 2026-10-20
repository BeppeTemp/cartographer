package testkbs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// ClientReportFile is the name of the client settings report in the output
// directory of GenerateFleet's command: next to the KBs, not in them.
const ClientReportFile = "client-report.json"

// ClientReport is the client settings report of an imaginary workstation
// connected to the test fleet, what a real client sends with `cartographer
// sync` as POST /api/client-settings (D818), so the Atlas Settings page has a
// client to show. The file is written by the client's own writer
// (clientconfig.Save) and read back by its own reader (clientconfig.Report),
// so the entries, the commented YAML and their masking are the real ones and
// nothing here repeats the schema. The token is the default reference to an
// environment variable, as `connect` writes it, masked like any other.
func ClientReport() (*clientconfig.SettingsReport, error) {
	dir, err := os.MkdirTemp("", "testkbs-client-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	var all, cited []string // every KB of the fleet; those that cite placeholders
	for _, s := range Fleet() {
		all = append(all, s.Name)
		if !s.Bare {
			cited = append(cited, s.Name)
		}
	}
	// Three KBs of the fleet are the ones the workstation works on.
	const kbA, kbB, kbC = "test-300", "test-150", "test-80"
	// The bound folders are invented ones under $HOME, bound to fleet KBs.
	ws := []clientconfig.WorkspaceBinding{
		{Path: "$HOME/projects/field-guide", KBs: []string{kbA}},
		{Path: "$HOME/projects/survey-tools", Remote: "https://gitlab.example.com/tools/survey-tools.git", KBs: []string{kbB, kbC}},
	}
	cfg := clientconfig.Default()
	cfg.ServerURL = "http://127.0.0.1:39280/mcp"
	cfg.Clients = map[string]clientconfig.ClientBinding{
		"claude": {KBs: []string{kbA, kbB, kbC}},
		"codex":  {KBs: all},
	}
	cfg.Scopes = map[string]string{"claude": "workspace"}
	cfg.Workspaces = map[string][]clientconfig.WorkspaceBinding{"claude": ws}
	cfg.SearchRoots = []string{"~/projects", "~/src"}
	cfg.Paths = map[string]string{"shared-drive": "$HOME/shared"}
	cfg.IgnoredPaths = []string{"path:old-archive"}
	// A public key, pinned for one KB; this one is a dummy.
	cfg.SigningKeys = map[string][]string{kbA: {strings.Repeat("00112233445566778899aabbccddeeff", 2)}}
	cfg.Identities = map[string]string{
		clientconfig.IdentityDefault: "User <user@example.com>",
		kbA:                          "User <user@work.example.com>",
	}
	cfg.UpdateCheckOff = true
	if err := clientconfig.Save(dir, cfg); err != nil {
		return nil, fmt.Errorf("testkbs: client settings: %w", err)
	}
	r, err := clientconfig.Report(dir, "dev")
	if err != nil {
		return nil, fmt.Errorf("testkbs: client settings: %w", err)
	}
	r.Machine = "dev-laptop"
	r.Providers = []clientconfig.ReportProvider{
		{Name: "claude", Scope: "workspace", Entry: "bridge", Isolation: "workspace"},
		{Name: "codex", Scope: "provider", Entry: "http", Isolation: "none"},
	}
	for _, b := range ws {
		r.Providers[0].Workspaces = append(r.Providers[0].Workspaces, clientconfig.ReportWorkspace{Path: b.Path, Remote: b.Remote, KBs: b.KBs})
	}
	// What the fleet's KBs cite (paths.yaml and the planted defects of
	// testkbs.go), as the last sync resolved it here: two resolved, one
	// ignored, one unresolved.
	r.Placeholders = []clientconfig.ReportPlaceholder{
		{Key: "path:old-archive", KBs: cited, Reason: "no directory is mapped to it", Description: "an archive nobody cites any more", Ignored: true},
		{Key: "path:shared-drive", KBs: cited, Path: "$HOME/shared", Description: "the team's shared folder"},
		{Key: "repo:tooling", KBs: cited, Path: "$HOME/projects/tooling", Description: "the scripts the team runs"},
		{Key: "repo:undeclared-tool", KBs: cited, Reason: "no git clone with this name under the search roots"},
	}
	return r, nil
}

// WriteClientReport writes ClientReport as JSON to ClientReportFile in dir.
func WriteClientReport(dir string) error {
	r, err := ClientReport()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ClientReportFile), append(data, '\n'), 0o644)
}
