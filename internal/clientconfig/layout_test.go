package clientconfig_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// The layout of the file (D1731): the server and placeholders
// groups, and the flat keys they replaced, read until a realignment moves them.

const flatLayoutYAML = `# my machine
server_url: https://cartographer.example.com/mcp
server_name: carto
auth: true
token: ${MY_TOKENS}
allow_insecure_http: true
# my roots
search_roots:
  - ~/work
paths:
  work-tools: /opt/work-tools
ignored_paths:
  - repo:gone
custom_key: keep-me
`

const groupedLayoutYAML = `server:
  url: https://cartographer.example.com/mcp
  name: carto
  auth: true
  token: ${MY_TOKENS}
  allow_insecure_http: true
placeholders:
  search_roots:
    - ~/work
  paths:
    work-tools: /opt/work-tools
  ignored:
    - repo:gone
`

func assertLayoutValues(t *testing.T, cfg *clientconfig.Config) {
	t.Helper()
	if cfg.ServerURL != "https://cartographer.example.com/mcp" || cfg.ServerName != "carto" || !cfg.Auth ||
		cfg.Token != "${MY_TOKENS}" || !cfg.AllowInsecureHTTP {
		t.Errorf("server settings = %q %q %v %q %v", cfg.ServerURL, cfg.ServerName, cfg.Auth, cfg.Token, cfg.AllowInsecureHTTP)
	}
	if strings.Join(cfg.SearchRoots, ",") != "~/work" || cfg.Paths["work-tools"] != "/opt/work-tools" || strings.Join(cfg.IgnoredPaths, ",") != "repo:gone" {
		t.Errorf("placeholders = %v %v %v", cfg.SearchRoots, cfg.Paths, cfg.IgnoredPaths)
	}
}

// Both layouts load to the same Config, and a flat file loads as it is: a file on
// a read-only mount that no realignment can rewrite keeps working.
func TestLoadReadsBothLayouts(t *testing.T) {
	for name, body := range map[string]string{"flat": flatLayoutYAML, "grouped": groupedLayoutYAML} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfigFile(t, dir, body)
			cfg, err := clientconfig.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			assertLayoutValues(t, cfg)
			if raw := readFile(t, clientconfig.Path(dir)); raw != body {
				t.Errorf("Load rewrote the file:\n%s", raw)
			}
		})
	}
}

func TestLoadGroupedKeysWinOverEqualFlatOnes(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, groupedLayoutYAML+"server_name: carto\nsearch_roots:\n  - ~/work\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertLayoutValues(t, cfg)
}

// Never a silent choice: the same key at both places with different values is
// refused by the loader, by Save and by the realignment, which leaves the file.
func TestLayoutConflictIsRefused(t *testing.T) {
	body := groupedLayoutYAML + "server_name: other\n"
	dir := t.TempDir()
	writeConfigFile(t, dir, body)
	if _, err := clientconfig.Load(dir); err == nil || !strings.Contains(err.Error(), "server_name and server.name are both set") {
		t.Errorf("Load error = %v", err)
	}
	if err := clientconfig.Save(dir, clientconfig.Default()); err == nil || !strings.Contains(err.Error(), "server.name") {
		t.Errorf("Save error = %v", err)
	}
	if _, _, err := clientconfig.RealignFile(dir, "1.0.0"); err == nil {
		t.Error("the realignment chose between two values")
	}
	if raw := readFile(t, clientconfig.Path(dir)); raw != body {
		t.Errorf("a refused file was rewritten:\n%s", raw)
	}
	if _, err := os.Stat(clientconfig.Path(dir) + ".bak-1.0.0"); err == nil {
		t.Error("a backup was written for a refused realignment")
	}
}

// The migration: the values arrive in their groups, a person's comment and the
// unknown keys stay, the original is kept beside the file, and it runs once.
func TestRealignFileMovesTheFlatKeysIntoTheirGroups(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, flatLayoutYAML)

	rep, backup, err := clientconfig.RealignFile(dir, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if backup != clientconfig.Path(dir)+".bak-1.0.0" || readFile(t, backup) != flatLayoutYAML {
		t.Errorf("backup = %q, does not hold the original", backup)
	}
	if got := strings.Join(rep.Lines(), "\n"); !strings.Contains(got, "server_url") || !strings.Contains(got, "now placeholders.ignored") {
		t.Errorf("report lacks the moves:\n%s", got)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, key := range []string{"server_url", "server_name", "auth", "token", "allow_insecure_http", "search_roots", "paths", "ignored_paths"} {
		if regexp.MustCompile(`(?m)^` + key + `:`).MatchString(raw) {
			t.Errorf("%s is still at the root:\n%s", key, raw)
		}
	}
	for _, kept := range []string{"# my roots", "# my machine", "custom_key: keep-me", "\nserver:\n", "\nplaceholders:\n", "  ignored:"} {
		if !strings.Contains(raw, kept) {
			t.Errorf("%q was lost:\n%s", kept, raw)
		}
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertLayoutValues(t, cfg)

	again, backup, err := clientconfig.RealignFile(dir, "1.0.0")
	if err != nil || !again.Empty() || backup != "" || readFile(t, clientconfig.Path(dir)) != raw {
		t.Errorf("second run: report %v backup %q err %v", again.Lines(), backup, err)
	}
}

// A save over a file the realignment has not reached yet writes the groups and
// leaves no flat key behind to conflict with them.
func TestSaveOverAFlatFileWritesTheGroups(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, flatLayoutYAML)
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerName = "renamed"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, key := range []string{"server_url", "server_name", "token", "search_roots", "paths", "ignored_paths"} {
		if hasKey(raw, key) {
			t.Errorf("%s survived at the root:\n%s", key, raw)
		}
	}
	for _, want := range []string{"  name: renamed", "  token: ${MY_TOKENS}", "# my roots", "custom_key: keep-me"} {
		if !strings.Contains(raw, want) {
			t.Errorf("lost %q:\n%s", want, raw)
		}
	}
	reloaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ServerName != "renamed" || reloaded.Token != "${MY_TOKENS}" || reloaded.Paths["work-tools"] != "/opt/work-tools" {
		t.Errorf("reloaded = %q %q %v", reloaded.ServerName, reloaded.Token, reloaded.Paths)
	}
}

// The mask of the report covers the bearer token wherever the file keeps it.
func TestRedactYAMLMasksTheTokenOfBothLayouts(t *testing.T) {
	for name, body := range map[string]string{"flat": "token: s3cret\n", "grouped": "server:\n  token: s3cret\n"} {
		out, err := clientconfig.RedactYAML([]byte(body))
		if err != nil || strings.Contains(string(out), "s3cret") {
			t.Errorf("%s: %q, %v", name, out, err)
		}
	}
}

const unorderedLayoutYAML = `# my machine
usage_scan: false
custom_key: keep-me
knowledge_bases:
  work-kb:
    identity: User <user@example.com>
    access: read
  notes.v2:
    identity: User <user@example.com>
    artifacts: false
providers:
  claude:
    workspaces:
      - path: $HOME/projects/work
        kbs: [work-kb]
    # which KBs
    kbs: [work-kb]
    access: read
    tools: minimal
placeholders:
  # my roots
  search_roots:
    - ~/work
server:
  token: ${MY_TOKENS}
  url: https://cartographer.example.com/mcp
`

// The migration writes the schema order, comments and unknown keys kept; a file
// that needs no migration keeps its order until `client realign --reorder`.
func TestMigrationWritesSchemaOrderAndReorderFileRewritesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, flatLayoutYAML)
	if _, _, err := clientconfig.RealignFile(dir, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	if i, j := strings.Index(raw, "\nserver:"), strings.Index(raw, "\nplaceholders:"); i < 0 || j < i {
		t.Errorf("a migrated file is not in schema order:\n%s", raw)
	}
	if !strings.Contains(raw, "# my roots") || !strings.Contains(raw, "custom_key: keep-me") {
		t.Errorf("the migration lost a comment or an unknown key:\n%s", raw)
	}

	other := t.TempDir()
	writeConfigFile(t, other, unorderedLayoutYAML)
	if rep, _, err := clientconfig.RealignFile(other, "1.0.0"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(strings.Join(rep.Lines(), "\n"), "order") {
		t.Errorf("a realignment with nothing to move reordered the file: %v", rep.Lines())
	}
	before := readFile(t, clientconfig.Path(other))
	rep, backup, err := clientconfig.ReorderFile(other, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" || readFile(t, backup) != before || !strings.Contains(strings.Join(rep.Lines(), "\n"), "order") {
		t.Errorf("backup %q, report %v", backup, rep.Lines())
	}
	got := readFile(t, clientconfig.Path(other))
	// The comments and blank lines the schema writes are set aside: the keys alone.
	var keys []string
	for _, l := range strings.Split(got, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			keys = append(keys, l)
		}
	}
	flat := strings.Join(keys, "\n")
	last := -1
	for _, key := range []string{"server:", "\nproviders:", "\nknowledge_bases:", "\nplaceholders:", "\nusage_scan:", "\ncustom_key:"} {
		i := strings.Index("\n"+flat, "\n"+strings.TrimPrefix(key, "\n"))
		if i <= last {
			t.Fatalf("%q out of order in\n%s", key, got)
		}
		last = i
	}
	for _, want := range []string{"  url: https://cartographer.example.com/mcp\n  token:", "    kbs: [work-kb]\n    access: read\n    tools: minimal\n    workspaces:", "    access: read\n    identity:", "  notes.v2:\n    artifacts: false\n    identity:", "  search_roots:"} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q in\n%s", want, flat)
		}
	}
	for _, want := range []string{"# my machine", "# my roots", "# which KBs\n    kbs: [work-kb]"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost comment %q in\n%s", want, got)
		}
	}
	if again, backup, err := clientconfig.ReorderFile(other, "1.0.0"); err != nil || !again.Empty() || backup != "" {
		t.Errorf("second run: %v %q %v", again.Lines(), backup, err)
	}
	if _, err := clientconfig.Load(other); err != nil {
		t.Errorf("the reordered file does not load: %v", err)
	}
}
