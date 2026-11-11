package plugin

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// Fixtures the client tests share with the core's: a plugin tree, a schema 2
// manifest, the two provider descriptors. The core keeps its own copies in its
// test files; a test package cannot import another's.

const validManifest = `schema: 1
name: team-forks
version: 1.2.3
display_name: Team forks
description: Closed forks of supported clients and the skills that go with them.
cartographer: ">=0.28.0 <0.30.0"
`

var testOpts = core.Options{
	RunningVersion: "0.28.0",
	ReservedName:   func(n string) bool { return n == "sync" || n == "plugin" },
}

const validV2 = `schema: 2
name: team-forks
version: 1.2.3
display_name: Team forks
description: Closed forks of supported clients.
cartographer: ">=0.29.0"
components:
  client:
    sdk: {artifacts: 1}
`

var v2Opts = core.Options{RunningVersion: "0.29.0", ReservedName: testOpts.ReservedName}

// schema2Tree is a client plugin tree under a schema 2 manifest.
func schema2Tree(manifest string, extra ...core.TreeFile) []core.TreeFile {
	files := []core.TreeFile{
		{Path: core.ManifestFile, Content: []byte(manifest)},
		{Path: "skills/team-example/SKILL.md", Content: []byte("---\nname: team-example\ndescription: Example skill.\n---\n\n# Example\n")},
	}
	return append(files, extra...)
}

const forkCLI = `schema: 1
name: fork-cli
display_name: Fork CLI
base: crush
detect:
  binaries: [fork-cli]
  dirs:
    default: ["{home}/.config/fork-cli"]
    windows: ["{home}/.config/fork-cli", "{local_app_data}/fork-cli"]
mcp:
  mode: cli
paths:
  global:
    skill:
      dir:
        darwin: "{xdg_config}/fork-cli/skills"
        linux: "{xdg_config}/fork-cli/skills"
        windows: "{local_app_data}/fork-cli/skills"
    agent:
      format: opencode
      dir:
        darwin: "{xdg_config}/fork-cli/agents"
        linux: "{xdg_config}/fork-cli/agents"
        windows: "{local_app_data}/fork-cli/agents"
    instructions:
      file:
        darwin: "{xdg_config}/fork-cli/FORK-CLI.md"
        linux: "{xdg_config}/fork-cli/FORK-CLI.md"
        windows: "{local_app_data}/fork-cli/FORK-CLI.md"
  project:
    skill: {dir: ".fork-cli/skills"}
    instructions: {file: "AGENTS.md"}
capabilities:
  agent.delegate: verified
`

const forkExt = `schema: 1
name: fork-ext
display_name: Fork Ext
base: cline
detect:
  dirs:
    default: ["{home}/.vscode/extensions/acme.fork-ext-*"]
mcp:
  mode: native
  config:
    default: "{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp.json"
  stale:
    default: ["{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json"]
paths:
  global:
    instructions:
      file:
        default: "{home}/.fork-ext-rules/FORK-EXT.md"
    hook:
      dir:
        default: "{home}/.fork-ext/cartographer-hooks"
      scan: base
  project:
    skill: {dir: ".fork-ext/skills"}
    instructions: {file: "AGENTS.md"}
capabilities:
  agent.delegate: absent
`

func parsed(t *testing.T, stem, src string) core.ProviderSpec {
	t.Helper()
	s, problems := core.ParseProvider(stem, []byte(src))
	if len(problems) != 0 {
		t.Fatalf("%s: %v", stem, problems)
	}
	return s
}

func fakeEnv(goos string, vars map[string]string) core.ProviderEnv {
	home := "/home/user"
	switch goos {
	case "darwin":
		home = "/Users/user"
	case "windows":
		home = `C:\Users\user`
	}
	return core.ProviderEnv{
		GOOS: goos, Home: home,
		Getenv:    func(k string) string { return vars[k] },
		Documents: func(h string) string { return h + "/Documents" },
	}
}
