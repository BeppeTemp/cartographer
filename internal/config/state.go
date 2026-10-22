package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// StateFileName is the server's own state file, under <data>/.cartographer/
// beside the update-check cache (D906). A dot-directory of the data dir is
// never discovered as a KB. It holds what the server remembers and the
// operator does not set: today the old names of renamed KBs.
const StateFileName = "state.json"

// ServerState is the content of the server state file.
type ServerState struct {
	// KBAliases maps the old name of a renamed KB to its new one, so a routed
	// `?kbs=<old>` still reaches it (D772). `kb rename` writes it, one step
	// only, and a mounted name wins over an alias.
	KBAliases []KBAlias `json:"kb_aliases,omitempty"`
}

// ServerStatePath is where the state file of a server with data directory
// dataDir lives; "" when there is no data directory.
func ServerStatePath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, ".cartographer", StateFileName)
}

// ReadServerState reads the state file of dataDir. A missing file, or no data
// directory, is an empty state. A malformed list is an error naming the file:
// the server must not guess which alias was meant.
func ReadServerState(dataDir string) (ServerState, error) {
	var st ServerState
	path := ServerStatePath(dataDir)
	if path == "" {
		return st, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return ServerState{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateKBAliases(st.KBAliases); err != nil {
		return ServerState{}, fmt.Errorf("%s: %w", path, err)
	}
	return st, nil
}

// WriteServerState replaces the state file of dataDir atomically.
func WriteServerState(dataDir string, st ServerState) error {
	path := ServerStatePath(dataDir)
	if path == "" {
		return errors.New("no data directory: the server state file lives under it")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+StateFileName+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RecordKBRename keeps the alias list flat and single hop (D772): the item
// whose from is the restored name goes (that KB is mounted again), every item
// pointing at the old name now points at the new one, and old → new is added.
func RecordKBRename(aliases []KBAlias, old, newName string) []KBAlias {
	out := make([]KBAlias, 0, len(aliases)+1)
	for _, a := range aliases {
		if a.From == newName || a.From == old {
			continue
		}
		if a.To == old {
			a.To = newName
		}
		out = append(out, a)
	}
	return append(out, KBAlias{From: old, To: newName})
}

// PruneKBAliases removes the items that point at name and the ones that start
// at it: an alias records where a KB went, not who owns a name, so once that KB
// is unmounted a different KB later mounted under the name must not be reached
// by an old URL, and a leftover item named name would take effect again the
// moment the KB now mounted under it goes away (D772).
func PruneKBAliases(aliases []KBAlias, name string) []KBAlias {
	var out []KBAlias
	for _, a := range aliases {
		if a.To != name && a.From != name {
			out = append(out, a)
		}
	}
	return out
}

// KBAliases is the alias list the server resolves: the state file's, plus the
// ones the server YAML still carries until the realignment has moved them
// (D906). The state file wins where both name the same old KB.
func (c *Config) KBAliases() ([]KBAlias, error) {
	st, err := ReadServerState(c.Data)
	merged := append([]KBAlias(nil), st.KBAliases...)
	have := map[string]bool{}
	for _, a := range merged {
		have[a.From] = true
	}
	for _, a := range c.legacyAliases {
		if !have[a.From] {
			merged = append(merged, a)
		}
	}
	return merged, err
}

// ReadLegacyKBAliases is the kb_aliases list the server YAML at configPath
// still carries (D906), nil when it has none, cannot be read or holds a list
// that does not validate (Load warns about that one).
func ReadLegacyKBAliases(configPath string) []KBAlias {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	var raw struct {
		KBAliases []KBAlias `yaml:"kb_aliases"`
	}
	if yaml.Unmarshal(data, &raw) != nil || ValidateKBAliases(raw.KBAliases) != nil {
		return nil
	}
	return raw.KBAliases
}

// MigrateKBAliases moves the kb_aliases the server YAML at configPath holds
// into the state file of dataDir (D906), before the realignment drops the key.
// An item the state file already has for the same old name is kept as it is.
// It reports how many were added. Without a data directory there is nowhere to
// put them: it returns an error, and the key is left in the file.
func MigrateKBAliases(configPath, dataDir string) (int, error) {
	legacy := ReadLegacyKBAliases(configPath)
	if len(legacy) == 0 {
		return 0, nil
	}
	if dataDir == "" {
		return 0, errors.New("the server has no data directory to keep its state file in")
	}
	st, err := ReadServerState(dataDir)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, a := range st.KBAliases {
		have[a.From] = true
	}
	added := 0
	for _, a := range legacy {
		if !have[a.From] {
			st.KBAliases = append(st.KBAliases, a)
			added++
		}
	}
	if added == 0 {
		return 0, nil
	}
	return added, WriteServerState(dataDir, st)
}

// DropKBAliasesKey removes the kb_aliases key from the server YAML at
// configPath, keeping the rest of the file (comments and key order) as it is.
// A command that edits the state file calls it after MigrateKBAliases: a list
// left in the YAML would be merged back in behind the edit (KBAliases).
func DropKBAliasesKey(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "kb_aliases" {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			out, err := yaml.Marshal(&doc)
			if err != nil {
				return err
			}
			info, serr := os.Stat(configPath)
			mode := os.FileMode(0o600)
			if serr == nil {
				mode = info.Mode().Perm()
			}
			return os.WriteFile(configPath, out, mode)
		}
	}
	return nil
}
