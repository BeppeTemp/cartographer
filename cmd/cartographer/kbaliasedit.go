package main

import (
	"errors"

	"github.com/BeppeTemp/cartographer/internal/config"
	"gopkg.in/yaml.v3"
)

// The list of renamed KBs' old names lives in the server state file under the
// data dir (D906, config.ServerState), not in the server YAML: it is written
// by `kb rename` and `kb unmount`, and read by the server at start.

// rootMapping returns the top-level mapping of the document, creating it for
// an empty file.
func rootMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if doc.Kind != yaml.DocumentNode {
		return nil, errors.New("not a YAML document")
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("the top level is not a mapping")
	}
	return doc.Content[0], nil
}

// readAliases is the alias list the server of dataDir will resolve, for a
// preflight: the state file's, plus the ones the server YAML at configPath ("" for
// none) has not yet handed over to it. It writes nothing.
func readAliases(dataDir, configPath string) ([]config.KBAlias, error) {
	st, err := config.ReadServerState(dataDir)
	if err != nil {
		return nil, err
	}
	out := st.KBAliases
	if configPath != "" {
		have := map[string]bool{}
		for _, a := range out {
			have[a.From] = true
		}
		for _, a := range config.ReadLegacyKBAliases(configPath) {
			if !have[a.From] {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

// settleAliases brings every alias into the state file before a command edits
// it (D906): the ones the server YAML still carries are moved there and the key
// is dropped from the YAML, so the list that is edited is the only one. It
// returns the state's list.
func settleAliases(dataDir, configPath string) ([]config.KBAlias, error) {
	if configPath != "" && len(config.ReadLegacyKBAliases(configPath)) > 0 {
		if _, err := config.MigrateKBAliases(configPath, dataDir); err != nil {
			return nil, err
		}
		if err := config.DropKBAliasesKey(configPath); err != nil {
			return nil, err
		}
	}
	st, err := config.ReadServerState(dataDir)
	return st.KBAliases, err
}

// aliasesPointingAt lists the items of aliases that start at or point at name,
// as "from → to" for a preflight.
func aliasesPointingAt(aliases []config.KBAlias, name string) []string {
	var out []string
	for _, a := range aliases {
		if a.To == name || a.From == name {
			out = append(out, a.From+" → "+a.To)
		}
	}
	return out
}
